using System.Net.Http.Headers;
using System.Net.Http.Json;
using System.Text;
using System.Text.Json;
using System.Text.Json.Nodes;

namespace AstrLink.Shell;

public sealed class ControlClient : IDisposable
{
  static readonly JsonSerializerOptions JsonOptions = new()
  {
    PropertyNamingPolicy = JsonNamingPolicy.SnakeCaseLower,
    PropertyNameCaseInsensitive = true,
  };

  readonly HttpClient _http;

  public ControlClient(string controlUrl, string controlToken, HttpMessageHandler? handler = null)
  {
    var baseUrl = controlUrl.TrimEnd('/') + "/";
    _http = handler is null ? new HttpClient() : new HttpClient(handler);
    _http.BaseAddress = new Uri(baseUrl);
    _http.Timeout = TimeSpan.FromSeconds(30);
    _http.DefaultRequestHeaders.Authorization = new AuthenticationHeaderValue("Bearer", controlToken);
  }

  public async Task<bool> HealthAsync(CancellationToken ct = default)
  {
    try
    {
      using var response = await _http.GetAsync("control/v1/health", ct);
      return response.IsSuccessStatusCode;
    }
    catch
    {
      return false;
    }
  }

  public async Task<JsonObject> GetRoutingSettingsAsync(CancellationToken ct = default)
  {
    return await _http.GetFromJsonAsync<JsonObject>("control/v1/routing-settings", JsonOptions, ct)
           ?? new JsonObject();
  }

  public async Task<JsonObject> PatchRoutingSettingsAsync(JsonObject patch, CancellationToken ct = default)
  {
    using var content = new StringContent(patch.ToJsonString(), Encoding.UTF8, "application/merge-patch+json");
    using var response = await _http.PatchAsync("control/v1/routing-settings", content, ct);
    var body = await response.Content.ReadAsStringAsync(ct);
    if (!response.IsSuccessStatusCode)
    {
      throw ControlApiException.From("保存路由设置", (int)response.StatusCode, body);
    }

    return JsonNode.Parse(body)?.AsObject() ?? new JsonObject();
  }

  public async Task<JsonObject> ProbeClassifierAsync(string path, CancellationToken ct = default)
  {
    return await PostJsonAsync("control/v1/auto-classifier/local/probe", new { path }, ct);
  }

  public async Task<JsonObject> InstallClassifierAsync(string path, CancellationToken ct = default)
  {
    return await PostJsonAsync("control/v1/auto-classifier", new { path }, ct);
  }

  public async Task<JsonArray> ListClassifiersAsync(CancellationToken ct = default)
  {
    var document = await _http.GetFromJsonAsync<JsonObject>("control/v1/auto-classifier", JsonOptions, ct);
    return document?["items"]?.AsArray() ?? [];
  }

  public async Task<JsonObject> ClassifyPreviewAsync(string text, CancellationToken ct = default)
  {
    return await PostJsonAsync("control/v1/auto-classifier/classify-preview", new { text }, ct);
  }

  public async Task<JsonArray> ListServicesAsync(CancellationToken ct = default)
  {
    var document = await _http.GetFromJsonAsync<JsonObject>("control/v1/services", JsonOptions, ct);
    return document?["items"]?.AsArray() ?? [];
  }

  public async Task<JsonObject> CreateServiceAsync(JsonObject request, CancellationToken ct = default)
  {
    using var content = new StringContent(request.ToJsonString(), Encoding.UTF8, "application/json");
    using var response = await _http.PostAsync("control/v1/services", content, ct);
    var body = await response.Content.ReadAsStringAsync(ct);
    if (!response.IsSuccessStatusCode)
    {
      throw new InvalidOperationException($"create service failed: {(int)response.StatusCode} {body}");
    }

    return JsonNode.Parse(body)?.AsObject() ?? new JsonObject();
  }

  /// <summary>
  /// Local inference stats. The control API requires exact from/to/time_zone/bucket.
  /// Default window: last 24h in local TZ, day buckets.
  /// </summary>
  public Task<JsonObject> GetUsageSummaryAsync(CancellationToken ct = default) =>
      GetUsageSummaryAsync(TimeSpan.FromHours(24), "day", null, ct);

  public async Task<JsonObject> GetUsageSummaryAsync(
      TimeSpan window,
      string bucket = "day",
      string? timeZone = null,
      CancellationToken ct = default)
  {
    var to = DateTimeOffset.UtcNow;
    var from = to - window;
    // Control API rejects "Local"; prefer an IANA id when the OS exposes one.
    var tz = ResolveIanaTimeZone(timeZone);
    var query =
        $"from={Uri.EscapeDataString(from.UtcDateTime.ToString("yyyy-MM-ddTHH:mm:ssZ"))}" +
        $"&to={Uri.EscapeDataString(to.UtcDateTime.ToString("yyyy-MM-ddTHH:mm:ssZ"))}" +
        $"&time_zone={Uri.EscapeDataString(tz)}" +
        $"&bucket={Uri.EscapeDataString(bucket)}";
    using var response = await _http.GetAsync("control/v1/usage-summary?" + query, ct);
    var body = await response.Content.ReadAsStringAsync(ct);
    if (!response.IsSuccessStatusCode)
    {
      throw new InvalidOperationException($"usage-summary failed: {(int)response.StatusCode} {body}");
    }

    return JsonNode.Parse(body)?.AsObject() ?? new JsonObject();
  }

  static string ResolveIanaTimeZone(string? preferred)
  {
    if (!string.IsNullOrWhiteSpace(preferred) &&
        !string.Equals(preferred, "Local", StringComparison.OrdinalIgnoreCase))
    {
      return preferred;
    }

    var id = TimeZoneInfo.Local.Id;
    if (!string.IsNullOrWhiteSpace(id) &&
        !string.Equals(id, "Local", StringComparison.OrdinalIgnoreCase) &&
        id.Contains('/'))
    {
      return id;
    }

    return "UTC";
  }

  public async Task<JsonObject?> GetServiceUsageAsync(string serviceId, CancellationToken ct = default)
  {
    using var response = await _http.GetAsync($"control/v1/services/{Uri.EscapeDataString(serviceId)}/usage", ct);
    if (!response.IsSuccessStatusCode)
    {
      return null;
    }

    return JsonNode.Parse(await response.Content.ReadAsStringAsync(ct))?.AsObject();
  }

  async Task<JsonObject> PostJsonAsync(string path, object payload, CancellationToken ct)
  {
    using var response = await _http.PostAsJsonAsync(path, payload, JsonOptions, ct);
    var body = await response.Content.ReadAsStringAsync(ct);
    if (!response.IsSuccessStatusCode)
    {
      throw ControlApiException.From(path, (int)response.StatusCode, body);
    }

    return JsonNode.Parse(body)?.AsObject() ?? new JsonObject();
  }

  public async Task<IReadOnlyList<string>> ProbeServiceModelsAsync(string serviceId, CancellationToken ct = default)
  {
    using var content = new ByteArrayContent([]);
    content.Headers.ContentLength = 0;
    using var response = await _http.PostAsync(
        $"control/v1/services/{Uri.EscapeDataString(serviceId)}/probe-models",
        content,
        ct);
    var body = await response.Content.ReadAsStringAsync(ct);
    if (!response.IsSuccessStatusCode)
    {
      throw ControlApiException.From("拉取模型", (int)response.StatusCode, body);
    }

    var ids = JsonNode.Parse(body)?["model_ids"] as JsonArray;
    return ChannelModels.FromIds(ids);
  }

  public async Task PatchServiceModelsAsync(string serviceId, IReadOnlyList<string> models, CancellationToken ct = default)
  {
    var array = new JsonArray();
    foreach (var model in models)
    {
      array.Add(model);
    }

    using var content = new StringContent(
        new JsonObject { ["models"] = array }.ToJsonString(),
        Encoding.UTF8,
        "application/merge-patch+json");
    using var response = await _http.PatchAsync(
        $"control/v1/services/{Uri.EscapeDataString(serviceId)}",
        content,
        ct);
    var body = await response.Content.ReadAsStringAsync(ct);
    if (!response.IsSuccessStatusCode)
    {
      throw ControlApiException.From("写入模型列表", (int)response.StatusCode, body);
    }
  }

  public void Dispose() => _http.Dispose();
}

public static class ChannelModels
{
  public static IReadOnlyList<string> FromServices(JsonArray? services)
  {
    var names = new SortedSet<string>(StringComparer.Ordinal);
    if (services is null)
    {
      return [];
    }

    foreach (var item in services)
    {
      if (item is not JsonObject service || service["enabled"]?.GetValue<bool>() == false)
      {
        continue;
      }

      foreach (var model in FromIds(service["models"] as JsonArray))
      {
        names.Add(model);
      }
    }

    return names.ToArray();
  }

  public static IReadOnlyList<string> FromIds(JsonArray? ids)
  {
    var names = new List<string>();
    if (ids is null)
    {
      return names;
    }

    foreach (var node in ids)
    {
      if (node?.GetValue<string>() is { Length: > 0 } model)
      {
        names.Add(model);
      }
    }

    return names;
  }
}

public sealed class ControlApiException : InvalidOperationException
{
  public int Status { get; }
  public string Code { get; }

  ControlApiException(int status, string code, string message) : base(message)
  {
    Status = status;
    Code = code;
  }

  public static ControlApiException From(string operation, int status, string body)
  {
    var code = "";
    var message = "";
    try
    {
      var error = JsonNode.Parse(body)?["error"] as JsonObject;
      code = error?["code"]?.GetValue<string>() ?? "";
      message = error?["message"]?.GetValue<string>() ?? "";
    }
    catch (System.Text.Json.JsonException)
    {
    }

    return new ControlApiException(status, code, Describe(operation, status, code, message, body));
  }

  public static string Describe(string operation, int status, string code, string message, string body)
  {
    if (code == "auto_classifier_already_installed")
    {
      return "这个模型目录已经导入过了";
    }

    if (code == "invalid_routing_settings" && message == "invalid field value")
    {
      return "路由设置不合法：启用时必须填写兜底模型，目标模型和兜底模型都不能填 astrlink/auto";
    }

    if (code == "auto_classifier_local_source_unavailable")
    {
      return "模型目录不存在，或里面没有可用的分类模型";
    }

    if (!string.IsNullOrWhiteSpace(message))
    {
      return message;
    }

    return string.IsNullOrWhiteSpace(body) ? $"{operation}失败（{status}）" : $"{operation}失败（{status}）{body}";
  }
}
