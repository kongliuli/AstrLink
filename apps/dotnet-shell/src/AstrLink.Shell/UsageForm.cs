using System.Text.Json.Nodes;
using AstrLink.Shell;

namespace AstrLink.Shell.Tray;

sealed class UsageForm : Form
{
    readonly ControlClient _client;
    readonly TextBox _summary = new()
    {
        Dock = DockStyle.Fill,
        Multiline = true,
        ReadOnly = true,
        ScrollBars = ScrollBars.Vertical,
        Font = new Font(FontFamily.GenericMonospace, 9f),
    };

    public UsageForm(ControlClient client)
    {
        _client = client;
        Text = "用量与余额";
        Width = 720;
        Height = 520;
        StartPosition = FormStartPosition.CenterScreen;

        var root = new TableLayoutPanel { Dock = DockStyle.Fill, ColumnCount = 1, RowCount = 2, Padding = new Padding(12) };
        root.RowStyles.Add(new RowStyle(SizeType.Percent, 100));
        root.RowStyles.Add(new RowStyle(SizeType.Absolute, 40));
        root.Controls.Add(_summary);

        var buttons = new FlowLayoutPanel { Dock = DockStyle.Fill, FlowDirection = FlowDirection.RightToLeft };
        var refresh = new Button { Text = "刷新", AutoSize = true };
        refresh.Click += async (_, _) => await LoadAsync();
        var close = new Button { Text = "关闭", AutoSize = true, DialogResult = DialogResult.Cancel };
        buttons.Controls.Add(close);
        buttons.Controls.Add(refresh);
        root.Controls.Add(buttons);
        Controls.Add(root);
        CancelButton = close;
        Load += async (_, _) => await LoadAsync();
    }

    async Task LoadAsync()
    {
        try
        {
            _summary.Text = "加载中…";
            var lines = new List<string> { $"更新时间 {DateTime.Now:HH:mm:ss}", "" };

            try
            {
                var usage = await _client.GetUsageSummaryAsync(TimeSpan.FromHours(24), "day");
                lines.Add("=== 近 24 小时用量（本机统计） ===");
                lines.Add(FormatUsageSummary(usage));
                lines.Add("");
            }
            catch (Exception ex)
            {
                lines.Add($"用量汇总不可用: {ex.Message}");
                lines.Add("");
            }

            lines.Add("=== 提供商余额 / 套餐额度 ===");
            var services = await _client.ListServicesAsync();
            if (services.Count == 0)
            {
                lines.Add("（还没有 API 提供商）");
            }

            foreach (var item in services)
            {
                if (item is not JsonObject service)
                {
                    continue;
                }

                var id = service["id"]?.GetValue<string>() ?? "";
                var name = service["name"]?.GetValue<string>() ?? id;
                var kind = service["kind"]?.GetValue<string>() ?? "";
                var enabled = service["enabled"]?.GetValue<bool>() ?? true;
                lines.Add($"· {name} [{kind}]{(enabled ? "" : " · 已禁用")}");
                if (string.IsNullOrEmpty(id))
                {
                    continue;
                }

                var usage = await _client.GetServiceUsageAsync(id);
                lines.Add("  " + FormatServiceUsage(usage));
            }

            _summary.Text = string.Join(Environment.NewLine, lines);
        }
        catch (Exception ex)
        {
            _summary.Text = "加载失败: " + ex.Message;
        }
    }

    static string FormatUsageSummary(JsonObject usage)
    {
        var totals = usage["totals"] as JsonObject ?? usage;
        return $"请求 {Node(totals["requests"])}（失败 {Node(totals["failed_requests"])}） · " +
               $"Token {Node(totals["total_tokens"])}（入 {Node(totals["input_tokens"])} / 出 {Node(totals["output_tokens"])}）";
    }

    static string FormatServiceUsage(JsonObject? usage)
    {
        if (usage is null)
        {
            return "无额度数据（按量付费官方 API 通常不查余额）";
        }

        if (usage["error"] is JsonObject error)
        {
            return "查询失败: " + (error["message"]?.GetValue<string>() ?? error.ToJsonString());
        }

        var quota = usage["quota"] as JsonObject;
        if (quota is not null)
        {
            if (quota["unlimited"]?.GetValue<bool>() == true)
            {
                return $"不限额 · 已用 ${Node(quota["used_usd"])}";
            }

            return $"余额 ${Node(quota["remaining_usd"])} / ${Node(quota["total_usd"])}（已用 ${Node(quota["used_usd"])}）";
        }

        var primary = usage["primary"] as JsonObject;
        var secondary = usage["secondary"] as JsonObject;
        if (primary is not null || secondary is not null)
        {
            return $"窗口用量 主 {Percent(primary)} · 次 {Percent(secondary)}";
        }

        return usage.ToJsonString();
    }

    static string Percent(JsonObject? window) =>
        window?["used_percent"] is null ? "-" : $"{window["used_percent"]}%";

    static string Node(JsonNode? node) => node?.ToString() ?? "-";
}
