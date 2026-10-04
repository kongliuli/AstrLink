# AstrLink.Shell（.NET 10）

Windows 托盘壳 + 无头 Host，守护上游 `astrlink-core`，并提供意图路由、本机 Key 导入、用量/余额查看。

目录：`apps/dotnet-shell`

## 组成

| 项目 | 说明 |
| --- | --- |
| `src/AstrLink.Shell.Core` | 进程守护、控制 API、意图路由、本机 Key 扫描/导入、Job Object |
| `src/AstrLink.Shell.Core.Tests` | Key 扫描与 ProviderCatalog 单测 |
| `src/AstrLink.Shell` | WinForms 托盘（`net10.0-windows`） |
| `src/AstrLink.Shell.Host` | Linux/Windows 无头宿主，便于本机验收 |
| `models/astrlink-intent-v1` | 已训练的四分类 ONNX 包 |
| `bin/` | 构建产物（core / worker / Host / Windows 托盘） |

## 构建

```bash
./scripts/build.sh
dotnet test src/AstrLink.Shell.Core.Tests -c Release
```

Windows 上托盘：

```powershell
dotnet build src/AstrLink.Shell/AstrLink.Shell.csproj -c Release -p:EnableWindowsTargeting=true
# 将 bin/ 中的 astrlink-core.exe、astrlink-classifier-worker.exe、onnxruntime.dll 放在一起后运行 AstrLink.Shell.exe
```

## 无头验收（Linux）

```bash
cd /home/ubuntu/AstrLink.Shell
./bin/AstrLink.Shell.Host \
  --import-model ./models/astrlink-intent-v1 \
  --enable-intent \
  --targets general=gpt-general,research=gpt-research,coding=gpt-coding,architect=gpt-architect \
  --fallback gpt-general \
  --preview "帮我修这个单元测试"

# 扫描 / 导入本机 API Key（跳过 opencode/Codex OAuth 订阅令牌）
./bin/AstrLink.Shell.Host --scan-keys
./bin/AstrLink.Shell.Host --import-keys ~/keys.env

# 今日用量 + 各服务余额
./bin/AstrLink.Shell.Host --usage
```

## 托盘菜单

- 启停/重启网关、复制 API 地址、开机自启
- **导入本机 API Key**：扫描 opencode / Codex / Claude settings / 环境变量 / 额外 `.env`
- **用量与余额**：本机今日用量 + 各提供商额度（OpenRouter credits、Coding Plan 等）
- **意图路由设置**：导入模型、四类目标、兜底、试分类
- **完整设置**：停掉壳内 core，拉起原 Tauri 桌面端（共用 `%APPDATA%\com.astrlink.desktop`）

## 凭据

- 本机扫描只导入**纯 API Key**；OpenCode/Codex 的订阅 OAuth refresh 令牌会跳过（避免和官方 CLI 互踢）。
- Core 在 Windows 上用 DPAPI（`astrlink-dpapi:v1:`）封装落库；Linux 验收路径保持明文兼容。

## 意图路由

客户端模型填 `astrlink/auto`。Core 用本地分类器映射到配置的目标模型；失败走兜底；同一会话（WebSocket / `previous_response_id`）粘住首次结果。
