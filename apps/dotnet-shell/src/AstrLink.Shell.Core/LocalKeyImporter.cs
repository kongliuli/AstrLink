using System.Text.Json;
using System.Text.Json.Nodes;

namespace AstrLink.Shell;

public sealed record KeyCandidate(ProviderPreset Provider, string Source, string Secret)
{
    public string Masked => Secret.Length <= 8 ? "****" : $"{Secret[..4]}…{Secret[^4..]}";

    public string Id => $"{Provider.Id}@{Source}";
}

/// <summary>
/// Finds plain provider API keys on this machine. Subscription OAuth tokens
/// (opencode "oauth", Codex "tokens", Claude .credentials.json) are skipped:
/// sharing a refresh token with the official CLI makes them evict each other.
/// </summary>
public sealed class LocalKeyImporter(string homeDirectory, Func<string, string?>? environment = null)
{
    readonly Func<string, string?> _env = environment ?? DefaultEnvironment;

    public IReadOnlyList<KeyCandidate> Scan(IEnumerable<string>? extraFiles = null)
    {
        var found = new List<KeyCandidate>();
        foreach (var path in OpenCodeAuthPaths())
        {
            found.AddRange(ReadOpenCodeAuth(path));
        }

        found.AddRange(ReadCodexAuth(Path.Combine(homeDirectory, ".codex", "auth.json")));
        found.AddRange(ReadClaudeSettings(Path.Combine(homeDirectory, ".claude", "settings.json")));
        found.AddRange(ReadEnvironment());
        foreach (var file in extraFiles ?? [])
        {
            found.AddRange(ReadFile(file));
        }

        return found
            .Where(c => LooksLikeKey(c.Secret))
            .GroupBy(c => (c.Provider.Id, c.Secret))
            .Select(g => g.First())
            .ToList();
    }

    public IReadOnlyList<KeyCandidate> ReadFile(string path)
    {
        if (!File.Exists(path))
        {
            return [];
        }

        var text = File.ReadAllText(path).TrimStart('\uFEFF');
        return text.TrimStart().StartsWith('{')
            ? ReadOpenCodeAuth(path).Concat(ReadCodexAuth(path)).ToList()
            : ReadDotEnv(text, $"file:{path}");
    }

    IEnumerable<string> OpenCodeAuthPaths()
    {
        var xdg = _env("XDG_DATA_HOME");
        if (!string.IsNullOrWhiteSpace(xdg))
        {
            yield return Path.Combine(xdg, "opencode", "auth.json");
        }

        yield return Path.Combine(homeDirectory, ".local", "share", "opencode", "auth.json");
        var localAppData = _env("LOCALAPPDATA");
        if (!string.IsNullOrWhiteSpace(localAppData))
        {
            yield return Path.Combine(localAppData, "opencode", "auth.json");
        }
    }

    static IEnumerable<KeyCandidate> ReadOpenCodeAuth(string path)
    {
        var root = ReadJson(path);
        if (root is null)
        {
            yield break;
        }

        foreach (var (providerId, node) in root)
        {
            if (node is not JsonObject entry ||
                !string.Equals(entry["type"]?.GetValue<string>(), "api", StringComparison.Ordinal) ||
                entry["key"]?.GetValue<string>() is not { } key)
            {
                continue;
            }

            if (ProviderCatalog.ByOpenCodeId(providerId) is { } preset)
            {
                yield return new KeyCandidate(preset, $"opencode:{providerId}", key.Trim());
            }
        }
    }

    static IEnumerable<KeyCandidate> ReadCodexAuth(string path)
    {
        var root = ReadJson(path);
        if (root?["OPENAI_API_KEY"] is JsonValue value &&
            value.TryGetValue<string>(out var key) &&
            !string.IsNullOrWhiteSpace(key))
        {
            yield return new KeyCandidate(ProviderCatalog.ByEnvVar("OPENAI_API_KEY")!, "codex:auth.json", key.Trim());
        }
    }

    static IEnumerable<KeyCandidate> ReadClaudeSettings(string path)
    {
        var env = ReadJson(path)?["env"] as JsonObject;
        if (env is null)
        {
            yield break;
        }

        // ponytail: a custom ANTHROPIC_BASE_URL means a proxy with its own
        // semantics; import only keys for the official endpoint.
        var baseUrl = env["ANTHROPIC_BASE_URL"]?.GetValue<string>();
        if (!string.IsNullOrWhiteSpace(baseUrl) &&
            !baseUrl.Contains("api.anthropic.com", StringComparison.OrdinalIgnoreCase))
        {
            yield break;
        }

        if (env["ANTHROPIC_API_KEY"]?.GetValue<string>() is { } key && !string.IsNullOrWhiteSpace(key))
        {
            yield return new KeyCandidate(ProviderCatalog.ByEnvVar("ANTHROPIC_API_KEY")!, "claude:settings.json", key.Trim());
        }
    }

    IEnumerable<KeyCandidate> ReadEnvironment()
    {
        foreach (var preset in ProviderCatalog.All)
        {
            foreach (var name in preset.EnvVars)
            {
                if (_env(name) is { } value && !string.IsNullOrWhiteSpace(value))
                {
                    yield return new KeyCandidate(preset, $"env:{name}", value.Trim());
                }
            }
        }
    }

    static List<KeyCandidate> ReadDotEnv(string text, string source)
    {
        var found = new List<KeyCandidate>();
        foreach (var raw in text.Split('\n'))
        {
            var line = raw.Trim();
            if (line.Length == 0 || line.StartsWith('#'))
            {
                continue;
            }

            if (line.StartsWith("export ", StringComparison.Ordinal))
            {
                line = line[7..].TrimStart();
            }

            var index = line.IndexOf('=');
            if (index <= 0)
            {
                continue;
            }

            var name = line[..index].Trim();
            var value = line[(index + 1)..].Trim().Trim('"', '\'');
            if (ProviderCatalog.ByEnvVar(name) is { } preset && value.Length > 0)
            {
                found.Add(new KeyCandidate(preset, $"{source}:{name}", value));
            }
        }

        return found;
    }

    static JsonObject? ReadJson(string path)
    {
        try
        {
            if (!File.Exists(path))
            {
                return null;
            }

            return JsonNode.Parse(File.ReadAllText(path).TrimStart('\uFEFF')) as JsonObject;
        }
        catch (Exception ex) when (ex is JsonException or IOException or UnauthorizedAccessException or InvalidOperationException)
        {
            return null;
        }
    }

    static bool LooksLikeKey(string value) =>
        value.Length is >= 8 and <= 4096 && !value.Any(c => c < 0x20 || c == 0x7f) && !value.Contains(' ');

    static string? DefaultEnvironment(string name) =>
        Environment.GetEnvironmentVariable(name)
        ?? (OperatingSystem.IsWindows() ? Environment.GetEnvironmentVariable(name, EnvironmentVariableTarget.User) : null);
}

public sealed record ImportResult(KeyCandidate Candidate, string Status, string? ServiceId = null, string? Error = null);

public sealed class KeyImportService(ControlClient client)
{
    public async Task<IReadOnlyList<ImportResult>> ImportAsync(IEnumerable<KeyCandidate> candidates, CancellationToken ct = default)
    {
        var existing = new HashSet<string>(StringComparer.Ordinal);
        foreach (var item in await client.ListServicesAsync(ct))
        {
            if (item?["name"]?.GetValue<string>() is { } name)
            {
                existing.Add(name);
            }
        }

        var results = new List<ImportResult>();
        foreach (var candidate in candidates)
        {
            var name = $"{candidate.Provider.Label} · {candidate.Masked}";
            if (existing.Contains(name))
            {
                results.Add(new ImportResult(candidate, "skipped"));
                continue;
            }

            try
            {
                var created = await client.CreateServiceAsync(candidate.Provider.ToCreateRequest(name, candidate.Secret), ct);
                existing.Add(name);
                results.Add(new ImportResult(candidate, "created", created["id"]?.GetValue<string>()));
            }
            catch (Exception ex)
            {
                results.Add(new ImportResult(candidate, "failed", Error: ex.Message));
            }
        }

        return results;
    }
}
