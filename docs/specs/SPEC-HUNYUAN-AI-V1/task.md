# SPEC-HUNYUAN-AI-V1 任务

1. [x] OpenAPI 与契约类型。
2. [x] SQLite 迁移 41：绑定、预算、调用、幂等。
3. [x] hunyuanapi：mock 引擎、HTTP、Mount。
4. [x] 自动测试（httptest，不启 GUI）：并发额度、幂等、断流、越权、unknown、不支持能力、Agent 未接入、Schema、工具仅返回、重启可查。
5. [x] mock 启停脚本与 examples / 状态机文档。
6. [x] 可选真实提供方（`ASTRLINK_HUNYUAN_REAL=1`，默认关；httptest 覆盖，不读个人凭据文件）。
