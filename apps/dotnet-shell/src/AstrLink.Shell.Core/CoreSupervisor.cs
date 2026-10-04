using System.Diagnostics;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;

namespace AstrLink.Shell;

public enum CoreState
{
    Stopped,
    Starting,
    Ready,
    Failed,
}

public sealed class CoreSupervisor : IDisposable
{
    static readonly JsonSerializerOptions JsonOptions = new() { PropertyNameCaseInsensitive = true };

    readonly ShellOptions _options;
    readonly object _gate = new();
    readonly JobObject? _job = JobObject.TryCreate();
    Process? _process;
    ControlClient? _client;
    CancellationTokenSource? _watchCts;
    Task? _watchTask;
    int _failures;
    bool _disposeRequested;

    public CoreState State { get; private set; } = CoreState.Stopped;
    public string? InferenceListen { get; private set; }
    public string? ControlUrl { get; private set; }
    public string? LastError { get; private set; }
    public string ControlToken { get; private set; } = "";
    public ControlClient? Client => _client;

    public event Action? StateChanged;

    public CoreSupervisor(ShellOptions options) => _options = options;

    public async Task StartAsync(CancellationToken ct = default)
    {
        lock (_gate)
        {
            if (State is CoreState.Starting or CoreState.Ready)
            {
                return;
            }

            State = CoreState.Starting;
            LastError = null;
        }

        _disposeRequested = false;
        Raise();
        try
        {
            Directory.CreateDirectory(_options.DataDirectory);
            Directory.CreateDirectory(_options.HomeDirectory);
            if (!File.Exists(_options.CoreExecutable))
            {
                throw new FileNotFoundException("astrlink-core not found", _options.CoreExecutable);
            }

            ControlToken = Convert.ToHexString(RandomNumberGenerator.GetBytes(32)).ToLowerInvariant();
            var psi = new ProcessStartInfo
            {
                FileName = _options.CoreExecutable,
                UseShellExecute = false,
                RedirectStandardInput = true,
                RedirectStandardOutput = true,
                RedirectStandardError = true,
                CreateNoWindow = true,
                WorkingDirectory = Path.GetDirectoryName(_options.CoreExecutable) ?? _options.DataDirectory,
            };
            foreach (var arg in BuildArgs())
            {
                psi.ArgumentList.Add(arg);
            }

            var process = new Process { StartInfo = psi, EnableRaisingEvents = true };
            if (!process.Start())
            {
                throw new InvalidOperationException("failed to start astrlink-core");
            }

            _job?.Assign(process);
            await process.StandardInput.WriteAsync((ControlToken + "\n").AsMemory(), ct);
            await process.StandardInput.FlushAsync(ct);
            process.StandardInput.Close();

            var readyLine = await ReadReadyLineAsync(process, ct);
            var ready = JsonSerializer.Deserialize<ReadyEvent>(readyLine, JsonOptions)
                        ?? throw new InvalidOperationException("invalid ready event");
            if (!string.Equals(ready.Event, "ready", StringComparison.OrdinalIgnoreCase))
            {
                throw new InvalidOperationException($"unexpected event {ready.Event}");
            }

            InferenceListen = ready.InferenceUrl;
            ControlUrl = ready.ControlUrl;
            if (string.IsNullOrWhiteSpace(ControlUrl))
            {
                throw new InvalidOperationException("ready event missing control_url");
            }

            _client?.Dispose();
            _client = new ControlClient(ControlUrl, ControlToken);
            PublishControlSession(process.Id);
            lock (_gate)
            {
                _process = process;
                State = CoreState.Ready;
                _failures = 0;
            }

            StartWatch(process);
            Raise();
        }
        catch (Exception ex)
        {
            LastError = ex.Message;
            lock (_gate)
            {
                State = CoreState.Failed;
            }

            Raise();
            throw;
        }
    }

    public async Task StopAsync()
    {
        _disposeRequested = true;
        _watchCts?.Cancel();
        Process? process;
        lock (_gate)
        {
            process = _process;
            _process = null;
            State = CoreState.Stopped;
        }

        ClearControlSession();
        if (process is null)
        {
            Raise();
            return;
        }

        try
        {
            if (!process.HasExited)
            {
                process.Kill(entireProcessTree: true);
                await process.WaitForExitAsync();
            }
        }
        catch
        {
            // ignored
        }
        finally
        {
            process.Dispose();
            _client?.Dispose();
            _client = null;
            Raise();
        }
    }

    public async Task RestartAsync(CancellationToken ct = default)
    {
        await StopAsync();
        _disposeRequested = false;
        await StartAsync(ct);
    }

    IEnumerable<string> BuildArgs()
    {
        yield return "--data-dir";
        yield return _options.DataDirectory;
        yield return "--inference-listen";
        yield return $"127.0.0.1:{_options.InferencePort}";
        yield return "--inference-port-fallback";
        yield return "--control-listen";
        yield return "127.0.0.1:0";
        yield return "--control-token-stdin";
        yield return "--max-concurrent-inspections";
        yield return _options.MaxConcurrentInspections.ToString();
        yield return "--response-start-timeout-seconds";
        yield return _options.ResponseStartTimeoutSeconds.ToString();
        yield return "--max-request-body-mib";
        yield return _options.MaxRequestBodyMiB.ToString();
        yield return $"--outbound-proxy={(_options.UseSystemProxy ? "system" : "direct")}";
        if (!string.IsNullOrWhiteSpace(_options.ClassifierWorker) && File.Exists(_options.ClassifierWorker))
        {
            yield return "--classifier-worker";
            yield return _options.ClassifierWorker;
        }

        if (!string.IsNullOrWhiteSpace(_options.PrivacyWorker) && File.Exists(_options.PrivacyWorker))
        {
            yield return "--privacy-worker";
            yield return _options.PrivacyWorker;
        }
    }

    static async Task<string> ReadReadyLineAsync(Process process, CancellationToken ct)
    {
        using var timeout = CancellationTokenSource.CreateLinkedTokenSource(ct);
        timeout.CancelAfter(TimeSpan.FromSeconds(30));
        while (!timeout.IsCancellationRequested)
        {
            var line = await process.StandardOutput.ReadLineAsync(timeout.Token);
            if (line is null)
            {
                var stderr = await process.StandardError.ReadToEndAsync(ct);
                throw new InvalidOperationException($"core exited before ready: {stderr}");
            }

            if (line.Contains("\"event\"", StringComparison.Ordinal) && line.Contains("ready", StringComparison.Ordinal))
            {
                return line;
            }
        }

        throw new TimeoutException("timed out waiting for core ready event");
    }

    void StartWatch(Process process)
    {
        _watchCts?.Cancel();
        _watchCts = new CancellationTokenSource();
        var token = _watchCts.Token;
        _watchTask = Task.Run(async () =>
        {
            try
            {
                await process.WaitForExitAsync(token);
            }
            catch (OperationCanceledException)
            {
                return;
            }

            if (_disposeRequested)
            {
                return;
            }

            lock (_gate)
            {
                if (!ReferenceEquals(_process, process))
                {
                    return;
                }

                State = CoreState.Failed;
                LastError = $"core exited with code {process.ExitCode}";
                _failures++;
            }

            Raise();
            ClearControlSession();
            var delay = TimeSpan.FromSeconds(Math.Min(30, Math.Pow(2, Math.Min(_failures, 4))));
            try
            {
                await Task.Delay(delay, token);
                if (!_disposeRequested)
                {
                    await StartAsync(token);
                }
            }
            catch
            {
                // ignored
            }
        }, token);
    }

    void PublishControlSession(int pid)
    {
        var path = Path.Combine(_options.HomeDirectory, "control-session.json");
        var document = new ControlSessionFile
        {
            Pid = pid,
            DesktopPid = Environment.ProcessId,
        };
        if (OperatingSystem.IsWindows())
        {
            document.ControlUrl = ControlUrl;
            document.ControlToken = ControlToken;
        }
        else
        {
            document.ControlSocket = Path.Combine(_options.DataDirectory, "control.sock");
            // Linux MCP prefers the socket; keep URL+token as a fallback for Host tooling.
            document.ControlUrl = ControlUrl;
            document.ControlToken = ControlToken;
        }

        var json = JsonSerializer.Serialize(document, new JsonSerializerOptions { WriteIndented = true });
        File.WriteAllText(path, json, new UTF8Encoding(encoderShouldEmitUTF8Identifier: false));
    }

    void ClearControlSession()
    {
        var path = Path.Combine(_options.HomeDirectory, "control-session.json");
        try
        {
            if (File.Exists(path))
            {
                File.Delete(path);
            }
        }
        catch
        {
            // ignored
        }
    }

    void Raise() => StateChanged?.Invoke();

    public void Dispose()
    {
        _disposeRequested = true;
        try
        {
            StopAsync().GetAwaiter().GetResult();
        }
        catch
        {
            // ignored
        }

        _job?.Dispose();
        _watchCts?.Dispose();
    }
}
