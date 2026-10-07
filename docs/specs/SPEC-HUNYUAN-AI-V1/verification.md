# SPEC-HUNYUAN-AI-V1 验证

状态：PASS

## 单元测试

```sh
cd core
gofmt -l ./internal/hunyuanapi/*.go ./cmd/astrlink-core/main.go
go vet ./...
go test -p 1 ./internal/hunyuanapi ./internal/storage/sqlite \
  ./internal/automodel ./internal/autoclassifier ./internal/ingress \
  ./cmd/astrlink-core -count=1 -timeout=90s
```

结果：全部 ok（含 capabilities、JSON/SSE、幂等、并发额度、断流 unknown、越权、Schema、工具仅返回、重启可查、Agent
not_connected、真实提供方 httptest、默认关闭 REAL）。

## Mock 启停烟雾（历史记录，本轮未重跑）

```powershell
powershell -File scripts/hunyuan-mock-up.ps1
powershell -File scripts/hunyuan-mock-down.ps1
```

观察：`capabilities_models=4`；`POST /invoke` 成功；`GET /invocations/{id}`
可查；`cost_status=unknown`。

## 交付索引

- Spec：`docs/specs/SPEC-HUNYUAN-AI-V1/`
- 状态机：`docs/specs/SPEC-HUNYUAN-AI-V1/state-machine.md`
- OpenAPI：`contracts/hunyuan-ai-v1.openapi.yaml`
- 示例：`docs/specs/SPEC-HUNYUAN-AI-V1/examples/`
- 真实模式（默认关闭）：ASTRLINK_HUNYUAN_REAL=1，复用已配置网关服务。使用 --hunyuan-project-binding
  token-ID=project-ID 显式绑定；非默认项目需配置对应预算。外部付费接口与真实 Tauri 本轮未验证。
