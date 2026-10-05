import { useEffect, useId, useMemo, useState } from "react";

import {
  installAutoClassifier,
  listAutoClassifiers,
  previewAutoClassifier,
  probeLocalAutoClassifier,
} from "../bridge";
import {
  astrlinkAutoModelId,
  compactIntentRouting,
  expandIntentRouting,
  intentRoutingIssue,
  intentTaxonomyLabels,
  maxRedirectModelLength,
  type IntentRouting,
} from "../failure-policy-model";
import { useT } from "../i18n";
import { Field } from "./Field";
import { FormMessage } from "./FormMessage";
import { ModelSelect } from "./ModelSelect";
import { Panel, PanelHeader } from "./Panel";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { Switch } from "./ui/switch";
import { Textarea } from "./ui/textarea";

export function IntentRoutingEditor({
  value,
  onChange,
  modelOptions,
  disabled = false,
  onEditingChange,
}: {
  value: IntentRouting | undefined;
  onChange: (value: IntentRouting) => void;
  modelOptions: readonly string[];
  disabled?: boolean;
  onEditingChange?: (editing: boolean) => void;
}) {
  const t = useT();
  const id = useId();
  const routing = expandIntentRouting(value);
  const issue = intentRoutingIssue(routing);
  const targets = useMemo(
    () => modelOptions.filter((model) => model !== astrlinkAutoModelId),
    [modelOptions],
  );
  const [path, setPath] = useState("");
  const [installed, setInstalled] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [previewText, setPreviewText] = useState("");
  const [previewResult, setPreviewResult] = useState<string | null>(null);

  useEffect(() => {
    let active = true;
    void listAutoClassifiers()
      .then((list) => {
        if (!active) return;
        const ready = list.items[0];
        setInstalled(ready ? (ready.name ?? ready.id) : null);
      })
      .catch(() => {
        if (active) setInstalled(null);
      });
    return () => {
      active = false;
    };
  }, []);

  const change = (next: IntentRouting) => onChange(expandIntentRouting(next));

  const importModel = async () => {
    setActionError(null);
    setBusy(true);
    try {
      const directory = path.trim();
      if (!directory) throw new Error(t("routing.intent.pathRequired"));
      await probeLocalAutoClassifier(directory);
      const installation = await installAutoClassifier(directory);
      if (installation.id === "already-installed") {
        const list = await listAutoClassifiers();
        setInstalled(
          list.items[0]?.name ?? t("routing.intent.alreadyInstalled"),
        );
      } else {
        setInstalled(installation.name ?? installation.id);
      }
    } catch (error) {
      setActionError(
        error instanceof Error
          ? error.message
          : t("routing.intent.importFailed"),
      );
    } finally {
      setBusy(false);
    }
  };

  const preview = async () => {
    setActionError(null);
    setBusy(true);
    try {
      const result = await previewAutoClassifier(previewText);
      if (result.fallback_reason) {
        const key =
          `routing.intent.fallback.${result.fallback_reason}` as const;
        const translated = t(key);
        setPreviewResult(
          translated === key ? result.fallback_reason : translated,
        );
        return;
      }
      const scores = intentTaxonomyLabels
        .map((label, index) => {
          const logit = result.logits?.[index];
          return logit === undefined
            ? null
            : `${t(`routing.intent.categories.${label}`)} ${logit}`;
        })
        .filter((line): line is string => line !== null);
      setPreviewResult(
        [
          t("routing.intent.previewCategory", {
            category: result.category ?? "",
            ms: result.latency_ms,
          }),
          ...scores,
        ].join("\n"),
      );
    } catch (error) {
      setActionError(
        error instanceof Error
          ? error.message
          : t("routing.intent.previewFailed"),
      );
    } finally {
      setBusy(false);
    }
  };

  return (
    <Panel data-testid="intent-routing-editor">
      <PanelHeader
        actions={
          <Switch
            aria-labelledby={id}
            checked={routing.enabled}
            disabled={disabled}
            onCheckedChange={(enabled) =>
              change({ ...compactIntentRouting(routing), enabled })
            }
          />
        }
      >
        <h2 id={id} className="text-sm font-semibold">
          {t("routing.intent.title")}
        </h2>
        <p className="mt-1 text-xs text-muted-foreground">
          {t("routing.intent.hint")}
        </p>
      </PanelHeader>
      <div className="grid gap-3 p-4">
        {issue === "empty_fallback" ? (
          <FormMessage tone="error">
            {t("routing.intent.fallbackRequired")}
          </FormMessage>
        ) : null}
        {issue === "auto_target" ? (
          <FormMessage tone="error">
            {t("routing.intent.autoTarget")}
          </FormMessage>
        ) : null}
        {intentTaxonomyLabels.map((category) => (
          <div key={category} className="grid gap-1.5">
            <span className="text-xs font-medium text-text-secondary">
              {t(`routing.intent.categories.${category}`)}
            </span>
            <ModelSelect
              aria-label={t(`routing.intent.categories.${category}`)}
              options={targets}
              value={routing.targets[category] ?? ""}
              placeholder={t("routing.intent.targetPlaceholder")}
              maxLength={maxRedirectModelLength}
              disabled={disabled}
              onValueChange={(model) => {
                onEditingChange?.(true);
                change({
                  ...routing,
                  targets: { ...routing.targets, [category]: model },
                });
              }}
              onValueCommit={() => onEditingChange?.(false)}
            />
          </div>
        ))}
        <div className="grid gap-1.5">
          <span className="text-xs font-medium text-text-secondary">
            {t("routing.intent.fallbackLabel")}
          </span>
          <ModelSelect
            aria-label={t("routing.intent.fallbackLabel")}
            options={targets}
            value={routing.fallback}
            placeholder={t("routing.intent.targetPlaceholder")}
            maxLength={maxRedirectModelLength}
            disabled={disabled}
            onValueChange={(fallback) => {
              onEditingChange?.(true);
              change({ ...routing, fallback });
            }}
            onValueCommit={() => onEditingChange?.(false)}
          />
        </div>
        <Field
          label={t("routing.intent.pathLabel")}
          hint={
            installed
              ? t("routing.intent.installed", { name: installed })
              : t("routing.intent.pathHint")
          }
          htmlFor={`${id}-path`}
        >
          <div className="flex min-w-0 gap-2">
            <Input
              id={`${id}-path`}
              value={path}
              disabled={disabled || busy}
              onChange={(event) => setPath(event.target.value)}
            />
            <Button
              type="button"
              variant="secondary"
              disabled={disabled || busy}
              onClick={() => void importModel()}
            >
              {t("routing.intent.import")}
            </Button>
          </div>
        </Field>
        <Field
          label={t("routing.intent.previewLabel")}
          htmlFor={`${id}-preview`}
        >
          <div className="flex min-w-0 gap-2">
            <Input
              id={`${id}-preview`}
              value={previewText}
              disabled={disabled || busy}
              onChange={(event) => setPreviewText(event.target.value)}
            />
            <Button
              type="button"
              variant="secondary"
              disabled={disabled || busy}
              onClick={() => void preview()}
            >
              {t("routing.intent.preview")}
            </Button>
          </div>
        </Field>
        {previewResult ? (
          <Textarea readOnly value={previewResult} rows={6} />
        ) : null}
        {actionError ? (
          <FormMessage tone="error">{actionError}</FormMessage>
        ) : null}
      </div>
    </Panel>
  );
}
