# 状态机与错误码

## 状态

```text
accepted -> reserved -> dispatching -> running|streaming -> succeeded|failed|canceled|timed_out
                                                      \-> unknown (不明结果，不自动重发)
```

终态：`succeeded` `failed` `canceled` `timed_out`

非终态可查询；`unknown` 保留原 `invocation_id`，相同幂等键不再派发。

## 取消语义

| 字段                      | 含义                                         |
| ------------------------- | -------------------------------------------- |
| `cancel_accepted`         | 取消请求已被受理                             |
| `local_connection_closed` | 本地 SSE/连接已断开                          |
| `remote_stop`             | `stopped` / `unknown` / `not_applicable`     |
| `cost_status`             | 未知时为 `unknown`，`cost` 为 null，禁止填 0 |

## 错误码

| code                   | 何时                                    |
| ---------------------- | --------------------------------------- |
| unauthorized           | 无令牌或未绑定项目                      |
| project_mismatch       | body.project 与认证绑定不一致           |
| idempotency_conflict   | 同幂等键不同内容                        |
| budget_exhausted       | 额度用尽                                |
| budget_unguaranteed    | 无法硬保证的预算策略                    |
| concurrency_limit      | 并发占满                                |
| capability_unsupported | 模型/流式/工具/结构化不支持             |
| schema_invalid         | 输出未通过 JSON Schema                  |
| schema_unsupported     | 不支持或非法的响应 Schema，在派发前拒绝 |
| timeout                | 超时                                    |
| cancel_race            | （保留）取消竞态                        |
| not_connected          | Agent 执行入口                          |
| invocation_not_found   | 查询/取消找不到或不属于本项目           |
| invalid_request        | 请求体非法                              |

OpenAPI：`contracts/hunyuan-ai-v1.openapi.yaml`
