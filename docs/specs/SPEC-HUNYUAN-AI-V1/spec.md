# SPEC-HUNYUAN-AI-V1

混元是 AstrLink 第一客户。本规格定义推理面上的统一 LLM 调用接口
`/hunyuan/ai/v1`。混元只提供网关地址、获准认证引用和调用参数；不理解上游厂商协议，也不操作 UI。源码拆解与蓝图保存仍归混元；网关只存引用 ID。

## 范围

- 必须：capabilities、invoke（JSON/SSE）、query、cancel、统一结果、项目隔离、幂等、预算预留、JSON
  Schema 校验、脱敏观测、mock 可运行链。
- Agent 执行入口返回 `not_connected`。LLM 路由与 Codex/Cursor Agent 执行分开。
- 不实现：分布式集群、可视化编排、账户采集、自动 Agent 启动、真实外部发布。
- 默认关闭。使用 --hunyuan-mock 或 ASTRLINK_HUNYUAN_MOCK=1 开启 mock；真实模式使用 ASTRLINK_HUNYUAN_REAL=1，复用网关已配置服务、隐私与审计链路，并要求显式项目绑定。不读取个人凭据文件。

## 认证

Bearer 或 `X-Api-Key` 使用本地 `astr_*` 推理令牌。项目身份只来自
`hunyuan_project_bindings`，不信任请求自报的 `project`。每次派发前复查绑定。

## 状态机

非终态：`accepted` → `reserved` → `dispatching` → `running` |
`streaming`；结果不明：`unknown`。终态：`succeeded` | `failed` | `canceled` |
`timed_out`。已派出且结果不明时保留 `unknown`，不盲目重发。

## 错误码

`unauthorized` `project_mismatch` `idempotency_conflict` `budget_exhausted`
`budget_unguaranteed` `concurrency_limit` `capability_unsupported`
`schema_invalid` `schema_unsupported` `timeout` `cancel_race` `not_connected`
`invocation_not_found`

费用未知时 `cost_status=unknown` 且 `cost` 为 null，禁止填 0。

契约：`contracts/hunyuan-ai-v1.openapi.yaml`。
