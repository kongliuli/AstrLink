using System.Text.Json.Nodes;

namespace AstrLink.Shell;

public sealed class IntentRoutingService(ControlClient client)
{
  public static readonly string[] Categories = ["general", "research", "coding", "architect"];

  public async Task<JsonObject> ImportModelAsync(string directory, CancellationToken ct = default)
  {
    await client.ProbeClassifierAsync(directory, ct);
    try
    {
      return await client.InstallClassifierAsync(directory, ct);
    }
    catch (ControlApiException ex) when (ex.Code == "auto_classifier_already_installed")
    {
      return new JsonObject { ["status"] = "ready", ["id"] = "already-installed" };
    }
  }

  public static void ValidateTargets(bool enabled, IReadOnlyDictionary<string, string> targets, string fallback)
  {
    if (IsAuto(fallback))
    {
      throw new InvalidOperationException("兜底模型不能填 astrlink/auto");
    }

    foreach (var (category, model) in targets)
    {
      if (IsAuto(model))
      {
        throw new InvalidOperationException($"{category} 的目标模型不能填 astrlink/auto");
      }
    }

    if (enabled && string.IsNullOrWhiteSpace(fallback))
    {
      throw new InvalidOperationException("启用意图路由时必须填写兜底模型");
    }
  }

  public static string FormatPreview(JsonObject preview)
  {
    var reason = preview["fallback_reason"]?.GetValue<string>();
    if (!string.IsNullOrEmpty(reason))
    {
      return reason switch
      {
        "timeout" => "分类器还在加载模型，请再点一次预览",
        "classifier_unavailable" => "分类器不可用。先导入模型目录，并确认托盘旁有 astrlink-classifier-worker.exe",
        "empty_text" => "请先输入一句要分类的话",
        "invalid_text" => "输入文本无效",
        _ => "分类失败：" + reason,
      };
    }

    var category = preview["category"]?.GetValue<string>() ?? "";
    var lines = new List<string> { $"分类：{category}（{preview["latency_ms"]} ms）" };
    if (preview["logits"] is JsonArray logits)
    {
      for (var i = 0; i < Categories.Length && i < logits.Count; i++)
      {
        lines.Add($"{Categories[i]}  {logits[i]}");
      }
    }

    return string.Join(Environment.NewLine, lines);
  }

  static bool IsAuto(string? value) =>
      string.Equals(value?.Trim(), "astrlink/auto", StringComparison.OrdinalIgnoreCase);

  public async Task<JsonObject> EnableAsync(
      IReadOnlyDictionary<string, string> targets,
      string fallback,
      CancellationToken ct = default)
  {
    var targetObject = new JsonObject();
    foreach (var category in Categories)
    {
      if (targets.TryGetValue(category, out var model) && !string.IsNullOrWhiteSpace(model))
      {
        targetObject[category] = model;
      }
    }

    var patch = new JsonObject
    {
      ["intent_routing"] = new JsonObject
      {
        ["enabled"] = true,
        ["targets"] = targetObject,
        ["fallback"] = fallback,
      },
    };
    return await client.PatchRoutingSettingsAsync(patch, ct);
  }

  public async Task<JsonObject> DisableAsync(CancellationToken ct = default)
  {
    var current = await client.GetRoutingSettingsAsync(ct);
    var existing = current["intent_routing"]?.DeepClone() as JsonObject ?? new JsonObject
    {
      ["targets"] = new JsonObject(),
      ["fallback"] = "",
    };
    existing["enabled"] = false;
    if (existing["targets"] is null)
    {
      existing["targets"] = new JsonObject();
    }

    if (existing["fallback"] is null)
    {
      existing["fallback"] = "";
    }

    return await client.PatchRoutingSettingsAsync(new JsonObject { ["intent_routing"] = existing }, ct);
  }

  public Task<JsonObject> PreviewAsync(string text, CancellationToken ct = default) =>
      client.ClassifyPreviewAsync(text, ct);
}
