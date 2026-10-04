using AstrLink.Shell;

namespace AstrLink.Shell.Tray;

sealed class ImportKeysForm : Form
{
    readonly ControlClient _client;
    readonly CheckedListBox _list = new() { Dock = DockStyle.Fill, CheckOnClick = true };
    readonly TextBox _extraFile = new() { Dock = DockStyle.Fill };
    readonly TextBox _log = new() { Dock = DockStyle.Fill, Multiline = true, ReadOnly = true, ScrollBars = ScrollBars.Vertical };
    List<KeyCandidate> _candidates = [];

    public ImportKeysForm(ControlClient client)
    {
        _client = client;
        Text = "导入本机 API Key";
        Width = 640;
        Height = 480;
        StartPosition = FormStartPosition.CenterScreen;
        MinimizeBox = false;

        var root = new TableLayoutPanel { Dock = DockStyle.Fill, ColumnCount = 1, RowCount = 5, Padding = new Padding(12) };
        root.RowStyles.Add(new RowStyle(SizeType.Absolute, 28));
        root.RowStyles.Add(new RowStyle(SizeType.Absolute, 36));
        root.RowStyles.Add(new RowStyle(SizeType.Percent, 55));
        root.RowStyles.Add(new RowStyle(SizeType.Percent, 30));
        root.RowStyles.Add(new RowStyle(SizeType.Absolute, 40));

        root.Controls.Add(new Label
        {
            Text = "扫描 opencode / Codex / Claude 配置与环境变量中的纯 API Key（跳过订阅登录令牌）",
            Dock = DockStyle.Fill,
            AutoEllipsis = true,
        });

        var fileRow = new TableLayoutPanel { ColumnCount = 3, Dock = DockStyle.Fill };
        fileRow.ColumnStyles.Add(new ColumnStyle(SizeType.Absolute, 90));
        fileRow.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 100));
        fileRow.ColumnStyles.Add(new ColumnStyle(SizeType.AutoSize));
        fileRow.Controls.Add(new Label { Text = "额外文件", Dock = DockStyle.Fill, TextAlign = ContentAlignment.MiddleLeft }, 0, 0);
        fileRow.Controls.Add(_extraFile, 1, 0);
        var browse = new Button { Text = "浏览…", AutoSize = true };
        browse.Click += (_, _) =>
        {
            using var dialog = new OpenFileDialog { Filter = "Key files|*.json;*.env;*.txt|All|*.*" };
            if (dialog.ShowDialog(this) == DialogResult.OK)
            {
                _extraFile.Text = dialog.FileName;
            }
        };
        fileRow.Controls.Add(browse, 2, 0);
        root.Controls.Add(fileRow);
        root.Controls.Add(_list);
        root.Controls.Add(_log);

        var buttons = new FlowLayoutPanel { Dock = DockStyle.Fill, FlowDirection = FlowDirection.RightToLeft };
        var import = new Button { Text = "导入勾选项", AutoSize = true };
        import.Click += async (_, _) => await ImportAsync();
        var scan = new Button { Text = "重新扫描", AutoSize = true };
        scan.Click += (_, _) => Rescan();
        var close = new Button { Text = "关闭", AutoSize = true, DialogResult = DialogResult.Cancel };
        buttons.Controls.Add(close);
        buttons.Controls.Add(import);
        buttons.Controls.Add(scan);
        root.Controls.Add(buttons);
        Controls.Add(root);
        CancelButton = close;
        Load += (_, _) => Rescan();
    }

    void Rescan()
    {
        var home = Environment.GetFolderPath(Environment.SpecialFolder.UserProfile);
        var extra = string.IsNullOrWhiteSpace(_extraFile.Text) ? null : new[] { _extraFile.Text.Trim() };
        _candidates = new LocalKeyImporter(home).Scan(extra).ToList();
        _list.Items.Clear();
        foreach (var candidate in _candidates)
        {
            _list.Items.Add($"{candidate.Provider.Label}  {candidate.Masked}  ← {candidate.Source}", true);
        }

        _log.Text = _candidates.Count == 0
            ? "未找到可导入的 API Key。"
            : $"找到 {_candidates.Count} 个候选（订阅 OAuth 令牌已跳过）。";
    }

    async Task ImportAsync()
    {
        var selected = new List<KeyCandidate>();
        for (var i = 0; i < _list.Items.Count; i++)
        {
            if (_list.GetItemChecked(i))
            {
                selected.Add(_candidates[i]);
            }
        }

        if (selected.Count == 0)
        {
            MessageBox.Show("请先勾选要导入的 Key", "AstrLink", MessageBoxButtons.OK, MessageBoxIcon.Information);
            return;
        }

        try
        {
            var results = await new KeyImportService(_client).ImportAsync(selected);
            _log.Text = string.Join(Environment.NewLine, results.Select(r =>
                $"{r.Status,-8} {r.Candidate.Provider.Label} {r.Candidate.Masked}" +
                (r.ServiceId is null ? "" : $" → {r.ServiceId}") +
                (r.Error is null ? "" : $" · {r.Error}")));
            MessageBox.Show(
                $"完成：创建 {results.Count(r => r.Status == "created")}，跳过 {results.Count(r => r.Status == "skipped")}，失败 {results.Count(r => r.Status == "failed")}",
                "AstrLink",
                MessageBoxButtons.OK,
                MessageBoxIcon.Information);
        }
        catch (Exception ex)
        {
            MessageBox.Show(ex.Message, "AstrLink", MessageBoxButtons.OK, MessageBoxIcon.Error);
        }
    }
}
