// @vitest-environment happy-dom
import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const bridge = vi.hoisted(() => ({
  listAutoClassifiers: vi.fn().mockResolvedValue({ items: [] }),
  probeLocalAutoClassifier: vi.fn(),
  installAutoClassifier: vi.fn(),
  previewAutoClassifier: vi.fn(),
}));
vi.mock("../bridge", () => bridge);

import type { IntentRouting } from "@/failure-policy-model";
import { applyLocale } from "@/i18n";

import {
  defaultAutoClassifierPath,
  IntentRoutingEditor,
} from "./IntentRoutingEditor";

describe("IntentRoutingEditor", () => {
  let container: HTMLDivElement;
  let root: Root;
  beforeEach(async () => {
    await applyLocale("zh-CN");
    (
      globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }
    ).IS_REACT_ACT_ENVIRONMENT = true;
    bridge.listAutoClassifiers.mockReset().mockResolvedValue({ items: [] });
    bridge.probeLocalAutoClassifier.mockReset().mockResolvedValue({});
    bridge.installAutoClassifier
      .mockReset()
      .mockResolvedValue({ id: "astrlink-intent-v1", name: "intent-v1" });
    bridge.previewAutoClassifier.mockReset().mockResolvedValue({
      category: "coding",
      latency_ms: 4,
      logits: [0.1, 0.2, 0.9, 0.3],
    });
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });
  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
  });

  function Harness({
    initial,
    models = [],
    change = () => {},
  }: {
    initial?: IntentRouting;
    models?: string[];
    change?: (value: IntentRouting) => void;
  }) {
    const [value, setValue] = useState(initial);
    return (
      <IntentRoutingEditor
        value={value}
        modelOptions={models}
        onChange={(next) => {
          setValue(next);
          change(next);
        }}
      />
    );
  }

  const button = (label: string) =>
    [...container.querySelectorAll("button")].find(
      (element) => element.textContent === label,
    )!;

  async function typeCombobox(label: string, value: string) {
    await act(async () => {
      const input = container.querySelector<HTMLInputElement>(
        `input[role="combobox"][aria-label="${label}"]`,
      )!;
      Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )!.set!.call(input, value);
      input.dispatchEvent(new Event("input", { bubbles: true }));
      input.dispatchEvent(
        new KeyboardEvent("keydown", { key: "Enter", bubbles: true }),
      );
    });
  }

  it("prefills the local classifier path and accepts typed model IDs without a catalog", async () => {
    const change = vi.fn();
    await act(async () => root.render(<Harness models={[]} change={change} />));
    expect(
      container.querySelector<HTMLInputElement>('input[id$="-path"]')?.value,
    ).toBe(defaultAutoClassifierPath);
    expect(container.textContent).toContain("不接 API");
    await typeCombobox("编码", "local-dev");
    expect(change).toHaveBeenLastCalledWith(
      expect.objectContaining({
        enabled: false,
        targets: expect.objectContaining({ coding: "local-dev" }),
      }),
    );
  });

  it("blocks enablement until a fallback is set", async () => {
    await act(async () => root.render(<Harness />));
    await act(async () =>
      container
        .querySelector<HTMLButtonElement>(
          '[data-testid="intent-routing-editor"] [role="switch"]',
        )!
        .click(),
    );
    expect(container.textContent).toContain("启用意图路由时必须填写兜底模型。");
  });

  it("fills taxonomy targets from the connected catalog", async () => {
    const change = vi.fn();
    await act(async () =>
      root.render(
        <Harness
          models={["minimax-m3", "kimi-k3", "gpt-5.6-luna", "qwen3.8-max"]}
          change={change}
        />,
      ),
    );
    await act(async () => button("按已接入模型填充分类").click());
    expect(change).toHaveBeenLastCalledWith({
      enabled: true,
      fallback: "gpt-5.6-luna",
      targets: {
        general: "minimax-m3",
        research: "kimi-k3",
        coding: "gpt-5.6-luna",
        architect: "qwen3.8-max",
      },
    });
  });

  it("imports the default path and previews a local classification", async () => {
    await act(async () => root.render(<Harness />));
    await act(async () => button("导入").click());
    expect(bridge.probeLocalAutoClassifier).toHaveBeenCalledWith(
      defaultAutoClassifierPath,
    );
    expect(bridge.installAutoClassifier).toHaveBeenCalledWith(
      defaultAutoClassifierPath,
    );
    await act(async () => {});
    expect(container.textContent).toContain("已安装：intent-v1");

    const preview = container.querySelector<HTMLInputElement>(
      'input[id$="-preview"]',
    )!;
    await act(async () => {
      Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )!.set!.call(preview, "fix the rustc pin");
      preview.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await act(async () => button("预览").click());
    expect(bridge.previewAutoClassifier).toHaveBeenCalledWith(
      "fix the rustc pin",
    );
    expect(container.textContent).toContain("分类：coding");
  });
});
