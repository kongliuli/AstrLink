using System.Text.Json.Nodes;
using AstrLink.Shell;

namespace AstrLink.Shell.Tray;

sealed class IntentSettingsForm : Form
{
    readonly ShellOptions _options;
    readonly ControlClient _client;
    readonly IntentRoutingService _intent;
    readonly TextBox _modelDir;
    readonly ComboBox _fallback;
    readonly TextBox _previewInput;
    readonly TextBox _previewOutput;
    readonly Dictionary<string, ComboBox> _targets = new(StringComparer.Ordinal);
    readonly CheckBox _enabled;

    public IntentSettingsForm(ShellOptions options, ControlClient client)
    {
        _options = options;
        _client = client;
        _intent = new IntentRoutingService(client);

        Text = "意图路由设置";
        Width = 640;
        Height = 680;
        StartPosition = FormStartPosition.CenterScreen;
        FormBorderStyle = FormBorderStyle.FixedDialog;
        MaximizeBox = false;
        MinimizeBox = false;

        var root = new TableLayoutPanel
        {
            Dock = DockStyle.Fill,
            ColumnCount = 1,
            RowCount = 10,
            Padding = new Padding(12),
        };
        for (var i = 0; i < 8; i++)
        {
            root.RowStyles.Add(new RowStyle(SizeType.Absolute, 36));
        }

        root.RowStyles.Add(new RowStyle(SizeType.Percent, 100));
        root.RowStyles.Add(new RowStyle(SizeType.Absolute, 40));

        _modelDir = new TextBox { Dock = DockStyle.Fill, Text = options.IntentModelDirectory ?? "" };
        var modelRow = Row("模型目录", _modelDir, "导入", async () => await ImportAsync());
        root.Controls.Add(modelRow);

        _enabled = new CheckBox { Text = "启用意图路由（astrlink/auto）", AutoSize = true };
        root.Controls.Add(_enabled);

        foreach (var category in IntentRoutingService.Categories)
        {
            var box = ModelBox();
            _targets[category] = box;
            root.Controls.Add(Row($"{category} → 模型", box));
        }

        _fallback = ModelBox();
        root.Controls.Add(Row("兜底模型", _fallback, "刷新模型", async () => await FillModelChoicesAsync()));

        _previewInput = new TextBox { Dock = DockStyle.Fill };
        _previewOutput = new TextBox
        {
            Dock = DockStyle.Fill,
            ReadOnly = true,
            Multiline = true,
            ScrollBars = ScrollBars.Vertical,
        };
        root.Controls.Add(Row("试分类", _previewInput, "预览", async () => await PreviewAsync()));
        root.Controls.Add(_previewOutput);

        var buttons = new FlowLayoutPanel { Dock = DockStyle.Fill, FlowDirection = FlowDirection.RightToLeft };
        var save = new Button { Text = "保存", AutoSize = true };
        save.Click += async (_, _) => await SaveAsync();
        var close = new Button { Text = "关闭", AutoSize = true, DialogResult = DialogResult.Cancel };
        buttons.Controls.Add(close);
        buttons.Controls.Add(save);
        root.Controls.Add(buttons);

        Controls.Add(root);
        CancelButton = close;
        Load += async (_, _) => await LoadCurrentAsync();
    }

    static ComboBox ModelBox() => new() { Dock = DockStyle.Fill, DropDownStyle = ComboBoxStyle.DropDown };

    async Task FillModelChoicesAsync()
    {
        var models = ChannelModels.FromServices(await _client.ListServicesAsync());
        foreach (var box in _targets.Values)
        {
            FillBox(box, models, box.Text);
        }

        FillBox(_fallback, models, _fallback.Text);
    }

    static void FillBox(ComboBox box, IReadOnlyList<string> models, string current)
    {
        box.Items.Clear();
        foreach (var model in models)
        {
            box.Items.Add(model);
        }

        if (!string.IsNullOrWhiteSpace(current) && !box.Items.Contains(current))
        {
            box.Items.Insert(0, current);
        }

        box.Text = current;
    }

    static Control Row(string label, Control field, string? actionText = null, Func<Task>? action = null)
    {
        var panel = new TableLayoutPanel { ColumnCount = actionText is null ? 2 : 3, Dock = DockStyle.Fill, Padding = new Padding(0, 4, 0, 4) };
        panel.ColumnStyles.Add(new ColumnStyle(SizeType.Absolute, 120));
        panel.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 100));
        if (actionText is not null)
        {
            panel.ColumnStyles.Add(new ColumnStyle(SizeType.AutoSize));
        }

        panel.Controls.Add(new Label { Text = label, TextAlign = ContentAlignment.MiddleLeft, Dock = DockStyle.Fill }, 0, 0);
        panel.Controls.Add(field, 1, 0);
        if (actionText is not null && action is not null)
        {
            var button = new Button { Text = actionText, AutoSize = true };
            button.Click += async (_, _) =>
            {
                try
                {
                    await action();
                }
                catch (Exception ex)
                {
                    MessageBox.Show(ex.Message, "AstrLink", MessageBoxButtons.OK, MessageBoxIcon.Error);
                }
            };
            panel.Controls.Add(button, 2, 0);
        }

        return panel;
    }

    async Task LoadCurrentAsync()
    {
        try
        {
            var settings = await _client.GetRoutingSettingsAsync();
            var routing = settings["intent_routing"] as JsonObject;
            _enabled.Checked = routing?["enabled"]?.GetValue<bool>() ?? false;
            _fallback.Text = routing?["fallback"]?.GetValue<string>() ?? "";
            var targets = routing?["targets"] as JsonObject;
            foreach (var category in IntentRoutingService.Categories)
            {
                _targets[category].Text = targets?[category]?.GetValue<string>() ?? "";
            }

            await FillModelChoicesAsync();
        }
        catch (Exception ex)
        {
            MessageBox.Show("读取路由设置失败: " + ex.Message, "AstrLink", MessageBoxButtons.OK, MessageBoxIcon.Warning);
        }
    }

    async Task ImportAsync()
    {
        var dir = ShellOptions.ResolveIntentModelDirectory(_modelDir.Text.Trim())
            ?? ShellOptions.ResolveIntentModelDirectory(AppContext.BaseDirectory);
        if (string.IsNullOrEmpty(dir) || !Directory.Exists(dir))
        {
            throw new DirectoryNotFoundException("找不到模型目录 models\\astrlink-intent-v1");
        }

        _modelDir.Text = dir;

        var installed = await _intent.ImportModelAsync(dir);
        var id = installed["id"]?.GetValue<string>();
        MessageBox.Show(
            id == "already-installed" ? "这个模型目录已经导入过了，可以直接预览。" : "已导入 " + id,
            "AstrLink",
            MessageBoxButtons.OK,
            MessageBoxIcon.Information);
    }

    async Task PreviewAsync()
    {
        var preview = await _intent.PreviewAsync(_previewInput.Text);
        _previewOutput.Text = IntentRoutingService.FormatPreview(preview);
    }

    async Task SaveAsync()
    {
        try
        {
            var map = _targets.ToDictionary(kv => kv.Key, kv => kv.Value.Text.Trim());
            IntentRoutingService.ValidateTargets(_enabled.Checked, map, _fallback.Text.Trim());
            if (_enabled.Checked)
            {
                await _intent.EnableAsync(map, _fallback.Text.Trim());
            }
            else
            {
                await _intent.DisableAsync();
            }

            MessageBox.Show("已保存", "AstrLink", MessageBoxButtons.OK, MessageBoxIcon.Information);
        }
        catch (Exception ex)
        {
            MessageBox.Show(ex.Message, "AstrLink", MessageBoxButtons.OK, MessageBoxIcon.Error);
        }
    }
}
