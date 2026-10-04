using AstrLink.Shell;

namespace AstrLink.Shell.Tray;

sealed class ProvidersForm : Form
{
    readonly ControlClient _client;
    readonly ComboBox _preset = new() { Dock = DockStyle.Fill, DropDownStyle = ComboBoxStyle.DropDownList };
    readonly TextBox _name = new() { Dock = DockStyle.Fill };
    readonly TextBox _key = new() { Dock = DockStyle.Fill, UseSystemPasswordChar = true };
    readonly ListBox _services = new() { Dock = DockStyle.Fill };
    readonly TextBox _log = new() { Dock = DockStyle.Fill, Multiline = true, ReadOnly = true, ScrollBars = ScrollBars.Vertical };

    public ProvidersForm(ControlClient client)
    {
        _client = client;
        Text = "提供商连接";
        Width = 640;
        Height = 520;
        StartPosition = FormStartPosition.CenterScreen;
        MinimizeBox = false;

        foreach (var preset in ProviderCatalog.All)
        {
            _preset.Items.Add(preset);
        }

        _preset.DisplayMember = nameof(ProviderPreset.Label);
        if (_preset.Items.Count > 0)
        {
            _preset.SelectedIndex = 0;
        }

        _preset.SelectedIndexChanged += (_, _) =>
        {
            if (_preset.SelectedItem is ProviderPreset preset && string.IsNullOrWhiteSpace(_name.Text))
            {
                _name.Text = preset.Label;
            }
        };
        if (_preset.SelectedItem is ProviderPreset first)
        {
            _name.Text = first.Label;
        }

        var root = new TableLayoutPanel { Dock = DockStyle.Fill, ColumnCount = 1, RowCount = 6, Padding = new Padding(12) };
        root.RowStyles.Add(new RowStyle(SizeType.Absolute, 36));
        root.RowStyles.Add(new RowStyle(SizeType.Absolute, 36));
        root.RowStyles.Add(new RowStyle(SizeType.Absolute, 36));
        root.RowStyles.Add(new RowStyle(SizeType.Percent, 55));
        root.RowStyles.Add(new RowStyle(SizeType.Percent, 45));
        root.RowStyles.Add(new RowStyle(SizeType.Absolute, 40));
        root.Controls.Add(Field("提供商", _preset));
        root.Controls.Add(Field("名称", _name));
        root.Controls.Add(Field("API Key", _key));
        root.Controls.Add(_services);
        root.Controls.Add(_log);

        var buttons = new FlowLayoutPanel { Dock = DockStyle.Fill, FlowDirection = FlowDirection.RightToLeft };
        var add = new Button { Text = "添加并拉取模型", AutoSize = true };
        add.Click += async (_, _) => await AddAsync();
        var refresh = new Button { Text = "刷新", AutoSize = true };
        refresh.Click += async (_, _) => await ReloadAsync();
        var close = new Button { Text = "关闭", AutoSize = true, DialogResult = DialogResult.Cancel };
        buttons.Controls.Add(close);
        buttons.Controls.Add(add);
        buttons.Controls.Add(refresh);
        root.Controls.Add(buttons);
        Controls.Add(root);
        CancelButton = close;
        Load += async (_, _) => await ReloadAsync();
    }

    static Control Field(string label, Control input)
    {
        var row = new TableLayoutPanel { ColumnCount = 2, Dock = DockStyle.Fill };
        row.ColumnStyles.Add(new ColumnStyle(SizeType.Absolute, 90));
        row.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 100));
        row.Controls.Add(new Label { Text = label, Dock = DockStyle.Fill, TextAlign = ContentAlignment.MiddleLeft }, 0, 0);
        row.Controls.Add(input, 1, 0);
        return row;
    }

    async Task ReloadAsync()
    {
        try
        {
            var items = await _client.ListServicesAsync();
            _services.Items.Clear();
            foreach (var item in items)
            {
                if (item is not System.Text.Json.Nodes.JsonObject service)
                {
                    continue;
                }

                var name = service["name"]?.GetValue<string>() ?? service["id"]?.GetValue<string>() ?? "?";
                var count = (service["models"] as System.Text.Json.Nodes.JsonArray)?.Count ?? 0;
                _services.Items.Add($"{name}    {count} 个模型");
            }

            if (_services.Items.Count == 0)
            {
                _services.Items.Add("还没有连接。选一个提供商，填 API Key，然后添加。");
            }
        }
        catch (Exception ex)
        {
            _log.Text = ex.Message;
        }
    }

    async Task AddAsync()
    {
        if (_preset.SelectedItem is not ProviderPreset preset)
        {
            return;
        }

        var secret = _key.Text.Trim();
        if (secret.Length == 0)
        {
            _log.Text = "请填写 API Key";
            return;
        }

        var name = string.IsNullOrWhiteSpace(_name.Text) ? preset.Label : _name.Text.Trim();
        try
        {
            var created = await _client.CreateServiceAsync(preset.ToCreateRequest(name, secret));
            var id = created["id"]?.GetValue<string>() ?? "";
            var models = await _client.ProbeServiceModelsAsync(id);
            if (models.Count > 0)
            {
                await _client.PatchServiceModelsAsync(id, models);
            }

            _key.Clear();
            _log.Text = models.Count == 0
                ? $"{name} 已添加，但没有拉到模型。检查 Key 和网络后再试。"
                : $"{name} 已添加，拉到 {models.Count} 个模型。回到意图路由里点「刷新模型」即可选择。";
            await ReloadAsync();
        }
        catch (Exception ex)
        {
            _log.Text = ex.Message;
        }
    }
}
