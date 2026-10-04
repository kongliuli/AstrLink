using System.Text.Json.Nodes;
using AstrLink.Shell;
using Xunit;

namespace AstrLink.Shell.Core.Tests;

public class IntentRoutingServiceTests
{
  [Fact]
  public async Task DisableAsync_PreservesTargetsAndSendsDisabledSettings()
  {
    using var handler = new RoutingHandler();
    using var client = new ControlClient("http://127.0.0.1:1", "test-token", handler);

    await new IntentRoutingService(client).DisableAsync();

    var routing = Assert.IsType<JsonObject>(handler.Patch?["intent_routing"]);
    Assert.False(routing["enabled"]!.GetValue<bool>());
    Assert.Equal("code-model", routing["targets"]!["coding"]!.GetValue<string>());
    Assert.Equal("fallback-model", routing["fallback"]!.GetValue<string>());
  }

  sealed class RoutingHandler : HttpMessageHandler
  {
    public JsonObject? Patch { get; private set; }

    protected override async Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken)
    {
      Assert.Equal("/control/v1/routing-settings", request.RequestUri!.AbsolutePath);
      if (request.Method == HttpMethod.Get)
      {
        return new HttpResponseMessage(System.Net.HttpStatusCode.OK)
        {
          Content = new StringContent("""
                        {"intent_routing":{"enabled":true,"targets":{"coding":"code-model"},"fallback":"fallback-model"}}
                        """, System.Text.Encoding.UTF8, "application/json"),
        };
      }

      Assert.Equal(HttpMethod.Patch, request.Method);
      Patch = JsonNode.Parse(await request.Content!.ReadAsStringAsync(cancellationToken))!.AsObject();
      return new HttpResponseMessage(System.Net.HttpStatusCode.OK)
      {
        Content = new StringContent(Patch.ToJsonString(), System.Text.Encoding.UTF8, "application/json"),
      };
    }
  }

  [Fact]
  public void ValidateTargets_RequiresFallbackWhenEnabled()
  {
    var error = Assert.Throws<InvalidOperationException>(() =>
        IntentRoutingService.ValidateTargets(true, new Dictionary<string, string>(), ""));
    Assert.Contains("兜底模型", error.Message);
  }

  [Fact]
  public void ValidateTargets_RejectsAutoModel()
  {
    var error = Assert.Throws<InvalidOperationException>(() =>
        IntentRoutingService.ValidateTargets(false, new Dictionary<string, string> { ["coding"] = "astrlink/auto" }, "gpt"));
    Assert.Contains("coding", error.Message);
  }

  [Fact]
  public void FormatPreview_NamesTimeout()
  {
    var text = IntentRoutingService.FormatPreview(new JsonObject { ["fallback_reason"] = "timeout" });
    Assert.Contains("再点一次预览", text);
  }

  [Fact]
  public void ResolveIntentModelDirectory_FindsPackageAboveTheExe()
  {
    var root = Directory.CreateTempSubdirectory("astrlink-model-");
    try
    {
      var model = Path.Combine(root.FullName, "models", "astrlink-intent-v1");
      Directory.CreateDirectory(model);
      File.WriteAllText(Path.Combine(model, "model.onnx"), "x");
      File.WriteAllText(Path.Combine(model, "tokenizer.json"), "{}");
      var nested = Path.Combine(root.FullName, "src", "bin");
      Directory.CreateDirectory(nested);

      var found = ShellOptions.ResolveIntentModelDirectory(nested);

      Assert.Equal(Path.GetFullPath(model), Path.GetFullPath(found!));
    }
    finally
    {
      root.Delete(true);
    }
  }

  [Fact]
  public void FindBuiltDesktop_WalksUpToTauriRelease()
  {
    var root = Directory.CreateTempSubdirectory("astrlink-desktop-");
    try
    {
      var exe = Path.Combine(root.FullName, "apps", "desktop", "src-tauri", "target", "release");
      Directory.CreateDirectory(exe);
      var file = Path.Combine(exe, "astrlink-desktop.exe");
      File.WriteAllText(file, "x");
      var nested = Path.Combine(root.FullName, "AstrLink.Shell", "src", "bin");
      Directory.CreateDirectory(nested);

      var found = ShellOptions.FindBuiltDesktop(nested);

      Assert.Equal(Path.GetFullPath(file), Path.GetFullPath(found!));
    }
    finally
    {
      root.Delete(true);
    }
  }

  [Fact]
  public void FromServices_CollectsEnabledChannelModels()
  {
    var services = new JsonArray
        {
            new JsonObject
            {
                ["enabled"] = true,
                ["models"] = new JsonArray("gpt-coding", "gpt-general"),
            },
            new JsonObject
            {
                ["enabled"] = false,
                ["models"] = new JsonArray("skipped"),
            },
        };

    var models = ChannelModels.FromServices(services);

    Assert.Equal(new[] { "gpt-coding", "gpt-general" }, models);
  }

  [Fact]
  public void Describe_ExplainsInvalidRoutingField()
  {
    var text = ControlApiException.Describe(
        "保存路由设置",
        422,
        "invalid_routing_settings",
        "invalid field value",
        "");
    Assert.Contains("兜底模型", text);
  }
}
