# Hunyuan 调用示例

## JSON invoke

```sh

# TOKEN and BASE from scripts/hunyuan-mock-up.ps1

curl -sS -X POST "$BASE/invoke" \
 -H "Authorization: Bearer $TOKEN" \
 -H "Content-Type: application/json" \
 -H "Idempotency-Key: demo-json-1" \
 --data-binary @invoke.json

```

## SSE invoke

```sh

curl -sS -N -X POST "$BASE/invoke" \
 -H "Authorization: Bearer $TOKEN" \
 -H "Content-Type: application/json" \
 -H "Accept: text/event-stream" \
 --data-binary @invoke.json

```

## Query

```sh

curl -sS "$BASE/invocations/$INVOCATION_ID" \
 -H "Authorization: Bearer $TOKEN"

```

## Cancel

```sh

curl -sS -X POST "$BASE/invocations/$INVOCATION_ID/cancel" \
 -H "Authorization: Bearer $TOKEN"

```

## Timeout (mock-slow)

```sh

curl -sS -X POST "$BASE/invoke" \
 -H "Authorization: Bearer $TOKEN" \
 -H "Content-Type: application/json" \
 -d '{"model":"mock-slow","input":"x","timeout_ms":50}'

```

## Idempotent replay (same key + body)

```sh

curl -sS -X POST "$BASE/invoke" \
 -H "Authorization: Bearer $TOKEN" \
 -H "Content-Type: application/json" \
 -H "Idempotency-Key: demo-json-1" \
 --data-binary @invoke.json
```
