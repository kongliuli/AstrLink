using System.Text.Json.Nodes;

namespace AstrLink.Shell;

/// <summary>
/// Mirrors apps/desktop/src/service-presets.ts for the providers the shell can
/// import from plain API keys. Keep base URLs and capabilities in sync.
/// </summary>
public sealed record ProviderPreset(
    string Id,
    string Kind,
    string Label,
    string BaseUrl,
    string AuthScheme,
    string[] Protocols,
    string[] EnvVars,
    string[] OpenCodeIds,
    IReadOnlyDictionary<string, string>? ConvertTo = null)
{
    static readonly HashSet<string> NonStreaming = new(StringComparer.Ordinal)
    {
        "openai.responses.compact", "openai.models", "google.models",
    };

    public JsonObject ToCreateRequest(string name, string secret)
    {
        var capabilities = new JsonArray();
        foreach (var protocol in Protocols)
        {
            var capability = new JsonObject
            {
                ["protocol"] = protocol,
                ["mode"] = "native",
                ["streaming"] = !NonStreaming.Contains(protocol),
            };
            if (ConvertTo is not null && ConvertTo.TryGetValue(protocol, out var target))
            {
                capability["convert_to"] = target;
            }

            capabilities.Add(capability);
        }

        return new JsonObject
        {
            ["name"] = name,
            ["kind"] = Kind,
            ["http"] = new JsonObject
            {
                ["base_url"] = BaseUrl,
                ["auth"] = new JsonObject { ["scheme"] = AuthScheme },
                ["credential"] = new JsonObject { ["secret"] = secret },
            },
            ["capabilities"] = capabilities,
        };
    }
}

public static class ProviderCatalog
{
    public static readonly ProviderPreset[] All =
    [
        new("openrouter", "openrouter", "OpenRouter", "https://openrouter.ai/api/v1", "bearer",
            ["openai.chat", "openai.responses", "openai.models"],
            ["OPENROUTER_API_KEY"], ["openrouter"]),
        new("opencode_zen", "opencode_zen", "OpenCode Zen", "https://opencode.ai/zen/v1", "bearer",
            ["openai.responses", "openai.chat", "anthropic.messages", "openai.models"],
            ["OPENCODE_API_KEY"], ["opencode"]),
        new("openai", "openai", "OpenAI", "https://api.openai.com/v1", "bearer",
            ["openai.responses", "openai.responses.compact", "openai.chat", "openai.completions", "openai.models"],
            ["OPENAI_API_KEY"], ["openai"]),
        new("anthropic", "anthropic", "Anthropic", "https://api.anthropic.com", "anthropic_api_key",
            ["anthropic.messages", "openai.models"],
            ["ANTHROPIC_API_KEY"], ["anthropic"]),
        new("gemini", "gemini", "Gemini", "https://generativelanguage.googleapis.com", "google_api_key",
            ["google.generate_content", "google.models", "openai.chat"],
            ["GEMINI_API_KEY", "GOOGLE_API_KEY", "GOOGLE_GENERATIVE_AI_API_KEY"], ["google"],
            new Dictionary<string, string> { ["openai.chat"] = "google.generate_content" }),
        new("deepseek", "deepseek", "DeepSeek", "https://api.deepseek.com/v1", "bearer",
            ["openai.responses", "anthropic.messages", "openai.chat", "openai.models"],
            ["DEEPSEEK_API_KEY"], ["deepseek"]),
        new("moonshot", "moonshot", "Moonshot", "https://api.moonshot.cn/v1", "bearer",
            ["openai.responses", "anthropic.messages", "openai.chat", "openai.models"],
            ["MOONSHOT_API_KEY"], ["moonshotai-cn", "moonshotai"]),
        new("xai", "xai", "xAI", "https://api.x.ai/v1", "bearer",
            ["openai.responses", "openai.responses.compact", "anthropic.messages", "openai.chat", "openai.completions", "openai.models"],
            ["XAI_API_KEY"], ["xai"]),
        new("qwen", "qwen", "通义千问", "https://dashscope.aliyuncs.com/compatible-mode/v1", "bearer",
            ["openai.responses", "anthropic.messages", "openai.chat"],
            ["DASHSCOPE_API_KEY"], ["alibaba-cn", "alibaba"]),
        new("glm", "glm", "智谱 GLM", "https://open.bigmodel.cn/api/paas/v4", "bearer",
            ["anthropic.messages", "openai.chat"],
            ["ZHIPU_API_KEY", "ZHIPUAI_API_KEY"], ["zhipuai"]),
        new("minimax", "minimax", "MiniMax", "https://api.minimax.cn/v1", "bearer",
            ["openai.responses", "anthropic.messages", "openai.chat", "openai.models"],
            ["MINIMAX_API_KEY"], ["minimax-cn", "minimax"]),
    ];

    public static ProviderPreset? ByOpenCodeId(string id) =>
        All.FirstOrDefault(p => p.OpenCodeIds.Contains(id, StringComparer.OrdinalIgnoreCase));

    public static ProviderPreset? ByEnvVar(string name) =>
        All.FirstOrDefault(p => p.EnvVars.Contains(name, StringComparer.OrdinalIgnoreCase));
}
