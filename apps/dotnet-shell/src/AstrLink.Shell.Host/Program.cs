using System.Text.Json.Nodes;
using AstrLink.Shell;

var options = ShellOptions.FromEnvironment(Directory.GetCurrentDirectory());
if (args.Contains("--help") || args.Contains("-h"))
{
    Console.WriteLine("""
        AstrLink.Shell.Host — headless supervisor for astrlink-core (.NET 10)

          --start                 start core and keep running (default)
          --import-model [dir]    probe+install classifier bundle then exit
          --enable-intent         enable intent_routing (requires --targets/--fallback)
          --targets k=v,k=v       e.g. general=m1,research=m2,coding=m3,architect=m4
          --fallback model        fallback model id
          --preview "text"        classify-preview then exit
          --import-keys [file]    scan local API keys (optional .env/json) and create services
          --scan-keys [file]      scan only; print candidates without creating services
          --usage                 print usage summary + per-service balance then exit
          --status                print ready state once
        """);
    return 0;
}

using var supervisor = new CoreSupervisor(options);
Console.CancelKeyPress += (_, e) =>
{
    e.Cancel = true;
    supervisor.StopAsync().GetAwaiter().GetResult();
};

await supervisor.StartAsync();
Console.WriteLine($"ready inference={supervisor.InferenceListen} control={supervisor.ControlUrl}");

var client = supervisor.Client ?? throw new InvalidOperationException("no control client");
var intent = new IntentRoutingService(client);
var oneShot = false;

if (args.Contains("--import-model"))
{
    oneShot = true;
    var dir = ArgValue(args, "--import-model") ?? options.IntentModelDirectory
              ?? throw new InvalidOperationException("model directory required");
    var installed = await intent.ImportModelAsync(dir);
    Console.WriteLine(installed.ToJsonString(new() { WriteIndented = true }));
}

if (args.Contains("--enable-intent"))
{
    oneShot = true;
    var fallback = ArgValue(args, "--fallback") ?? throw new InvalidOperationException("--fallback required");
    var targets = ParseTargets(ArgValue(args, "--targets") ?? "");
    var settings = await intent.EnableAsync(targets, fallback);
    Console.WriteLine(settings["intent_routing"]?.ToJsonString(new() { WriteIndented = true }));
}

if (args.Contains("--preview"))
{
    oneShot = true;
    var text = ArgValue(args, "--preview") ?? throw new InvalidOperationException("--preview text required");
    var preview = await intent.PreviewAsync(text);
    Console.WriteLine(preview.ToJsonString(new() { WriteIndented = true }));
}

if (args.Contains("--scan-keys") || args.Contains("--import-keys"))
{
    oneShot = true;
    var file = ArgValue(args, args.Contains("--import-keys") ? "--import-keys" : "--scan-keys");
    string[]? extra = string.IsNullOrWhiteSpace(file) ? null : [file];
    var home = Environment.GetFolderPath(Environment.SpecialFolder.UserProfile);
    var candidates = new LocalKeyImporter(home).Scan(extra);
    if (candidates.Count == 0)
    {
        Console.WriteLine("no importable API keys found");
    }
    else if (args.Contains("--scan-keys") && !args.Contains("--import-keys"))
    {
        foreach (var candidate in candidates)
        {
            Console.WriteLine($"{candidate.Provider.Id,-12} {candidate.Masked,-18} {candidate.Source}");
        }
    }
    else
    {
        var results = await new KeyImportService(client).ImportAsync(candidates);
        foreach (var result in results)
        {
            Console.WriteLine($"{result.Status,-8} {result.Candidate.Provider.Id,-12} {result.Candidate.Masked}" +
                              (result.ServiceId is null ? "" : $" id={result.ServiceId}") +
                              (result.Error is null ? "" : $" err={result.Error}"));
        }
    }
}

if (args.Contains("--usage"))
{
    oneShot = true;
    try
    {
        var summary = await client.GetUsageSummaryAsync(TimeSpan.FromHours(24), "day");
        Console.WriteLine("=== usage-summary (last 24h) ===");
        Console.WriteLine(summary.ToJsonString(new() { WriteIndented = true }));
    }
    catch (Exception ex)
    {
        Console.WriteLine("usage-summary unavailable: " + ex.Message);
    }

    Console.WriteLine("=== service balances ===");
    foreach (var item in await client.ListServicesAsync())
    {
        if (item is not JsonObject service)
        {
            continue;
        }

        var id = service["id"]?.GetValue<string>() ?? "";
        var name = service["name"]?.GetValue<string>() ?? id;
        var kind = service["kind"]?.GetValue<string>() ?? "";
        Console.WriteLine($"# {name} [{kind}] {id}");
        var usage = string.IsNullOrEmpty(id) ? null : await client.GetServiceUsageAsync(id);
        Console.WriteLine(usage?.ToJsonString(new() { WriteIndented = true }) ?? "null");
    }
}

if (oneShot || args.Contains("--status"))
{
    await supervisor.StopAsync();
    return 0;
}

Console.WriteLine("running; Ctrl+C to stop");
await Task.Delay(Timeout.InfiniteTimeSpan);
return 0;

static string? ArgValue(string[] args, string name)
{
    for (var i = 0; i < args.Length; i++)
    {
        if (args[i] == name)
        {
            return i + 1 < args.Length && !args[i + 1].StartsWith("--", StringComparison.Ordinal)
                ? args[i + 1]
                : "";
        }

        if (args[i].StartsWith(name + "=", StringComparison.Ordinal))
        {
            return args[i][(name.Length + 1)..];
        }
    }

    return null;
}

static Dictionary<string, string> ParseTargets(string raw)
{
    var map = new Dictionary<string, string>(StringComparer.Ordinal);
    if (string.IsNullOrWhiteSpace(raw))
    {
        return map;
    }

    foreach (var part in raw.Split(',', StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries))
    {
        var idx = part.IndexOf('=');
        if (idx <= 0)
        {
            continue;
        }

        map[part[..idx]] = part[(idx + 1)..];
    }

    return map;
}
