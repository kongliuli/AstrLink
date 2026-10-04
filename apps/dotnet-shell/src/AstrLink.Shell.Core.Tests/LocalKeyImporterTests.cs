using AstrLink.Shell;
using Xunit;

namespace AstrLink.Shell.Core.Tests;

public class LocalKeyImporterTests
{
    [Fact]
    public void Scan_ReadsOpenCodeApiKeysAndSkipsOAuth()
    {
        var home = Directory.CreateTempSubdirectory("astrlink-home-");
        try
        {
            var auth = Path.Combine(home.FullName, ".local", "share", "opencode", "auth.json");
            Directory.CreateDirectory(Path.GetDirectoryName(auth)!);
            File.WriteAllText(auth, """
                {
                  "openrouter": { "type": "api", "key": "sk-or-v1-abc123456789" },
                  "anthropic": { "type": "oauth", "refresh": "rt_should_skip", "access": "at_should_skip" },
                  "openai": { "type": "api", "key": "sk-openai-abc123456789" }
                }
                """);
            var importer = new LocalKeyImporter(home.FullName, _ => null);
            var found = importer.Scan();
            Assert.Contains(found, c => c.Provider.Id == "openrouter" && c.Secret.StartsWith("sk-or-"));
            Assert.Contains(found, c => c.Provider.Id == "openai");
            Assert.DoesNotContain(found, c => c.Secret.Contains("should_skip"));
        }
        finally
        {
            home.Delete(true);
        }
    }

    [Fact]
    public void Scan_ReadsCodexOpenAiKeyButNotTokensObject()
    {
        var home = Directory.CreateTempSubdirectory("astrlink-home-");
        try
        {
            var auth = Path.Combine(home.FullName, ".codex", "auth.json");
            Directory.CreateDirectory(Path.GetDirectoryName(auth)!);
            File.WriteAllText(auth, """
                {
                  "OPENAI_API_KEY": "sk-codex-plain-key-123456",
                  "tokens": { "access_token": "at_skip", "refresh_token": "rt_skip" }
                }
                """);
            var found = new LocalKeyImporter(home.FullName, _ => null).Scan();
            Assert.Single(found);
            Assert.Equal("openai", found[0].Provider.Id);
            Assert.Equal("sk-codex-plain-key-123456", found[0].Secret);
        }
        finally
        {
            home.Delete(true);
        }
    }

    [Fact]
    public void Scan_ReadsClaudeSettingsAndEnv()
    {
        var home = Directory.CreateTempSubdirectory("astrlink-home-");
        try
        {
            var settings = Path.Combine(home.FullName, ".claude", "settings.json");
            Directory.CreateDirectory(Path.GetDirectoryName(settings)!);
            File.WriteAllText(settings, """
                { "env": { "ANTHROPIC_API_KEY": "sk-ant-api03-plain-key-123" } }
                """);
            var env = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase)
            {
                ["OPENROUTER_API_KEY"] = "sk-or-env-key-abcdef",
            };
            var found = new LocalKeyImporter(home.FullName, name => env.GetValueOrDefault(name)).Scan();
            Assert.Contains(found, c => c.Provider.Id == "anthropic");
            Assert.Contains(found, c => c.Provider.Id == "openrouter" && c.Source.StartsWith("env:"));
        }
        finally
        {
            home.Delete(true);
        }
    }

    [Fact]
    public void Scan_ReadsDotEnvFile()
    {
        var home = Directory.CreateTempSubdirectory("astrlink-home-");
        try
        {
            var file = Path.Combine(home.FullName, "keys.env");
            File.WriteAllText(file, """
                # comment
                export DEEPSEEK_API_KEY="sk-deepseek-from-dotenv-123"
                OPENAI_API_KEY=sk-openai-from-dotenv-456
                """);
            var found = new LocalKeyImporter(home.FullName, _ => null).Scan([file]);
            Assert.Contains(found, c => c.Provider.Id == "deepseek");
            Assert.Contains(found, c => c.Provider.Id == "openai");
        }
        finally
        {
            home.Delete(true);
        }
    }

    [Fact]
    public void ProviderCatalog_OpenRouterCreateRequestMatchesPreset()
    {
        var request = ProviderCatalog.All.Single(p => p.Id == "openrouter")
            .ToCreateRequest("OpenRouter · test", "sk-or-test-key");
        Assert.Equal("openrouter", request["kind"]!.GetValue<string>());
        Assert.Equal("https://openrouter.ai/api/v1", request["http"]!["base_url"]!.GetValue<string>());
        Assert.Equal("bearer", request["http"]!["auth"]!["scheme"]!.GetValue<string>());
        Assert.Equal("sk-or-test-key", request["http"]!["credential"]!["secret"]!.GetValue<string>());
        Assert.Contains(request["capabilities"]!.AsArray(), n => n!["protocol"]!.GetValue<string>() == "openai.chat");
    }
}
