using System.Diagnostics;
using AstrLink.Shell;

namespace AstrLink.Shell.Tray;

sealed class TrayApplicationContext : ApplicationContext
{
    readonly ShellOptions _options;
    readonly CoreSupervisor _supervisor;
    readonly NotifyIcon _tray;
    readonly ToolStripMenuItem _statusItem;
    readonly ToolStripMenuItem _copyItem;
    readonly ToolStripMenuItem _autostartItem;

    public TrayApplicationContext(ShellOptions options)
    {
        _options = options;
        _supervisor = new CoreSupervisor(options);
        _supervisor.StateChanged += () =>
        {
            if (Application.OpenForms.Count >= 0)
            {
                // Ensure UI updates land on the UI thread.
            }

            void Update() => RefreshStatus();
            if (SynchronizationContext.Current is null)
            {
                try
                {
                    _tray.ContextMenuStrip?.BeginInvoke(Update);
                }
                catch
                {
                    Update();
                }
            }
            else
            {
                Update();
            }
        };

        _statusItem = new ToolStripMenuItem("状态: 启动中…") { Enabled = false };
        _copyItem = new ToolStripMenuItem("复制 API 地址", null, (_, _) => CopyApi());
        _autostartItem = new ToolStripMenuItem("开机自启", null, (_, _) => ToggleAutostart())
        {
            Checked = Autostart.IsEnabled(),
            CheckOnClick = false,
        };

        var menu = new ContextMenuStrip();
        menu.Items.Add(_statusItem);
        menu.Items.Add(_copyItem);
        menu.Items.Add(new ToolStripSeparator());
        menu.Items.Add("启动网关", null, async (_, _) => await SafeAsync(() => _supervisor.StartAsync()));
        menu.Items.Add("停止网关", null, async (_, _) => await SafeAsync(() => _supervisor.StopAsync()));
        menu.Items.Add("重启网关", null, async (_, _) => await SafeAsync(() => _supervisor.RestartAsync()));
        menu.Items.Add(new ToolStripSeparator());
        menu.Items.Add("导入本机 API Key…", null, (_, _) => OpenImportKeys());
        menu.Items.Add("用量与余额…", null, (_, _) => OpenUsage());
        menu.Items.Add("意图路由设置…", null, (_, _) => OpenIntentSettings());
        menu.Items.Add("提供商连接…", null, (_, _) => OpenProviders());
        menu.Items.Add("完整设置（桌面端）…", null, (_, _) => OpenDesktop());
        menu.Items.Add("打开数据目录", null, (_, _) => OpenDirectory(_options.DataDirectory));
        menu.Items.Add(_autostartItem);
        menu.Items.Add(new ToolStripSeparator());
        menu.Items.Add("退出", null, async (_, _) =>
        {
            await _supervisor.StopAsync();
            _tray.Visible = false;
            ExitThread();
        });

        _tray = new NotifyIcon
        {
            Text = "AstrLink",
            Icon = SystemIcons.Application,
            Visible = true,
            ContextMenuStrip = menu,
        };
        _tray.DoubleClick += (_, _) => OpenIntentSettings();

        _ = SafeAsync(() => _supervisor.StartAsync());
    }

    void RefreshStatus()
    {
        var state = _supervisor.State switch
        {
            CoreState.Ready => $"Ready · {_supervisor.InferenceListen}",
            CoreState.Starting => "启动中…",
            CoreState.Failed => $"失败 · {_supervisor.LastError}",
            _ => "已停止",
        };
        _statusItem.Text = "状态: " + state;
        _tray.Text = "AstrLink · " + state;
        _copyItem.Enabled = _supervisor.State == CoreState.Ready;
    }

    void CopyApi()
    {
        if (!string.IsNullOrWhiteSpace(_supervisor.InferenceListen))
        {
            Clipboard.SetText(_supervisor.InferenceListen!);
        }
    }

    void ToggleAutostart()
    {
        var exe = Environment.ProcessPath ?? Application.ExecutablePath;
        var next = !_autostartItem.Checked;
        Autostart.SetEnabled(next, exe);
        _autostartItem.Checked = next;
    }

    void OpenIntentSettings()
    {
        if (!TryGetReadyClient(out var client))
        {
            return;
        }

        using var form = new IntentSettingsForm(_options, client);
        form.ShowDialog();
    }

    void OpenImportKeys()
    {
        if (!TryGetReadyClient(out var client))
        {
            return;
        }

        using var form = new ImportKeysForm(client);
        form.ShowDialog();
    }

    void OpenUsage()
    {
        if (!TryGetReadyClient(out var client))
        {
            return;
        }

        using var form = new UsageForm(client);
        form.ShowDialog();
    }

    bool TryGetReadyClient(out ControlClient client)
    {
        if (_supervisor.Client is null || _supervisor.State != CoreState.Ready)
        {
            MessageBox.Show("网关尚未就绪", "AstrLink", MessageBoxButtons.OK, MessageBoxIcon.Information);
            client = null!;
            return false;
        }

        client = _supervisor.Client;
        return true;
    }

    void OpenProviders()
    {
        if (!TryGetReadyClient(out var client))
        {
            return;
        }

        using var form = new ProvidersForm(client);
        form.ShowDialog();
    }

    async void OpenDesktop()
    {
        if (string.IsNullOrWhiteSpace(_options.DesktopExecutable) || !File.Exists(_options.DesktopExecutable))
        {
            OpenProviders();
            return;
        }

        await _supervisor.StopAsync();
        try
        {
            var process = Process.Start(new ProcessStartInfo
            {
                FileName = _options.DesktopExecutable,
                UseShellExecute = true,
            });
            if (process is not null)
            {
                // Wait off the UI thread. A synchronous WaitForExit freezes the tray
                // and any settings window for as long as the desktop stays open.
                await Task.Run(() =>
                {
                    process.WaitForExit();
                    process.Dispose();
                });
            }
        }
        finally
        {
            await SafeAsync(() => _supervisor.StartAsync());
        }
    }

    static void OpenDirectory(string path)
    {
        Directory.CreateDirectory(path);
        Process.Start(new ProcessStartInfo { FileName = path, UseShellExecute = true });
    }

    static async Task SafeAsync(Func<Task> action)
    {
        try
        {
            await action();
        }
        catch (Exception ex)
        {
            MessageBox.Show(ex.Message, "AstrLink", MessageBoxButtons.OK, MessageBoxIcon.Error);
        }
    }

    protected override void Dispose(bool disposing)
    {
        if (disposing)
        {
            _tray.Visible = false;
            _tray.Dispose();
            _supervisor.Dispose();
        }

        base.Dispose(disposing);
    }
}
