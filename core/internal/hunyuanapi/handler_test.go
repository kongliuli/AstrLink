package hunyuanapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accesstoken"
	"github.com/QuantumNous/astrlink/core/internal/hunyuanapi"
	"github.com/QuantumNous/astrlink/core/internal/ingress"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
)

func setup(t *testing.T) (*hunyuanapi.Handler, string, *sqlite.Store) {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "astrlink.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	manager, err := accesstoken.NewManager(store)
	if err != nil {
		t.Fatal(err)
	}
	created, err := manager.Create(context.Background(), "混元测试")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BindAllAccessTokensToHunyuanDev(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := store.SetHunyuanBudget(context.Background(), contract.HunyuanDefaultProject, contract.HunyuanDefaultBudget, 100, 8); err != nil {
		t.Fatal(err)
	}
	handler := hunyuanapi.New(store, ingress.AccessTokenAuthenticatorFunc(manager.Authenticate), hunyuanapi.MockProvider{})
	return handler, created.Value, store
}

func authReq(method, path, token string, body any) *http.Request {
	var reader *bytes.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

func TestCapabilitiesAndJSONInvoke(t *testing.T) {
	handler, token, _ := setup(t)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, authReq(http.MethodGet, "/hunyuan/ai/v1/capabilities", token, nil))
	if rec.Code != 200 {
		t.Fatalf("capabilities status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, authReq(http.MethodPost, "/hunyuan/ai/v1/invoke", token, map[string]any{
		"model": "mock-echo", "input": "ping",
	}))
	if rec.Code != 200 {
		t.Fatalf("invoke status=%d body=%s", rec.Code, rec.Body.String())
	}
	var view hunyuanapi.InvocationView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.Status != contract.HunyuanStatusSucceeded || view.CostStatus != contract.HunyuanCostUnknown || view.Cost != nil {
		t.Fatalf("unexpected view: %+v", view)
	}
	if view.Output == nil || view.Output.Text != "echo:ping" {
		t.Fatalf("output=%+v", view.Output)
	}
}

func TestProjectMismatchAndAgentNotConnected(t *testing.T) {
	handler, token, _ := setup(t)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, authReq(http.MethodPost, "/hunyuan/ai/v1/invoke", token, map[string]any{
		"project": "other", "model": "mock-echo", "input": "x",
	}))
	if rec.Code != 403 {
		t.Fatalf("status=%d", rec.Code)
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, authReq(http.MethodPost, "/hunyuan/ai/v1/agent/run", token, map[string]any{}))
	if rec.Code != 501 {
		t.Fatalf("agent status=%d", rec.Code)
	}
	var body hunyuanapi.ErrorBody
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Error.Code != contract.HunyuanErrNotConnected {
		t.Fatalf("code=%s", body.Error.Code)
	}
}

func TestIdempotencyConflictAndReplay(t *testing.T) {
	handler, token, _ := setup(t)
	req := authReq(http.MethodPost, "/hunyuan/ai/v1/invoke", token, map[string]any{"model": "mock-echo", "input": "one"})
	req.Header.Set("Idempotency-Key", "k1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var first hunyuanapi.InvocationView
	_ = json.Unmarshal(rec.Body.Bytes(), &first)

	req2 := authReq(http.MethodPost, "/hunyuan/ai/v1/invoke", token, map[string]any{"model": "mock-echo", "input": "one"})
	req2.Header.Set("Idempotency-Key", "k1")
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	var second hunyuanapi.InvocationView
	_ = json.Unmarshal(rec2.Body.Bytes(), &second)
	if first.InvocationID != second.InvocationID {
		t.Fatalf("replay created new id %s vs %s", first.InvocationID, second.InvocationID)
	}

	req3 := authReq(http.MethodPost, "/hunyuan/ai/v1/invoke", token, map[string]any{"model": "mock-echo", "input": "two"})
	req3.Header.Set("Idempotency-Key", "k1")
	rec3 := httptest.NewRecorder()
	handler.ServeHTTP(rec3, req3)
	if rec3.Code != 409 {
		t.Fatalf("conflict status=%d body=%s", rec3.Code, rec3.Body.String())
	}
}

func TestBudgetRaceLastUnit(t *testing.T) {
	handler, token, store := setup(t)
	if err := store.SetHunyuanBudget(context.Background(), contract.HunyuanDefaultProject, contract.HunyuanDefaultBudget, 1, 8); err != nil {
		t.Fatal(err)
	}
	var okCount atomic.Int32
	var failCount atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, authReq(http.MethodPost, "/hunyuan/ai/v1/invoke", token, map[string]any{
				"model": "mock-echo", "input": "race",
			}))
			if rec.Code == 200 {
				okCount.Add(1)
			} else {
				failCount.Add(1)
			}
		}()
	}
	wg.Wait()
	if okCount.Load() != 1 {
		t.Fatalf("ok=%d fail=%d want exactly one success", okCount.Load(), failCount.Load())
	}
}

func TestUnsupportedCapabilityAndSchema(t *testing.T) {
	handler, token, _ := setup(t)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, authReq(http.MethodPost, "/hunyuan/ai/v1/invoke", token, map[string]any{
		"model": "mock-text", "input": "x", "tools": []map[string]string{{"name": "search"}},
	}))
	if rec.Code != 422 {
		t.Fatalf("tools unsupported status=%d", rec.Code)
	}
	rec = httptest.NewRecorder()
	req := authReq(http.MethodPost, "/hunyuan/ai/v1/invoke", token, map[string]any{
		"model": "mock-echo", "input": "hi",
		"response_schema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"echo": map[string]any{"type": "string"},
			},
			"required":             []string{"echo"},
			"additionalProperties": false,
		},
	})
	handler.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("schema invoke=%d %s", rec.Code, rec.Body.String())
	}
}

func TestUnknownResultNotRetried(t *testing.T) {
	handler, token, _ := setup(t)
	req := authReq(http.MethodPost, "/hunyuan/ai/v1/invoke", token, map[string]any{"model": "mock-unknown", "input": "x"})
	req.Header.Set("Idempotency-Key", "unk")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var view hunyuanapi.InvocationView
	_ = json.Unmarshal(rec.Body.Bytes(), &view)
	if view.Status != contract.HunyuanStatusUnknown {
		t.Fatalf("status=%s", view.Status)
	}
	req2 := authReq(http.MethodPost, "/hunyuan/ai/v1/invoke", token, map[string]any{"model": "mock-unknown", "input": "x"})
	req2.Header.Set("Idempotency-Key", "unk")
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	var view2 hunyuanapi.InvocationView
	_ = json.Unmarshal(rec2.Body.Bytes(), &view2)
	if view2.InvocationID != view.InvocationID || view2.Status != contract.HunyuanStatusUnknown {
		t.Fatalf("retried or changed: %+v", view2)
	}
}

func TestQueryCancelAndTimeout(t *testing.T) {
	handler, token, _ := setup(t)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, authReq(http.MethodPost, "/hunyuan/ai/v1/invoke", token, map[string]any{
		"model": "mock-slow", "input": "x", "timeout_ms": 20,
	}))
	var timed hunyuanapi.InvocationView
	_ = json.Unmarshal(rec.Body.Bytes(), &timed)
	if timed.Status != contract.HunyuanStatusTimedOut {
		t.Fatalf("status=%s body=%s", timed.Status, rec.Body.String())
	}
	q := httptest.NewRecorder()
	handler.ServeHTTP(q, authReq(http.MethodGet, "/hunyuan/ai/v1/invocations/"+timed.InvocationID, token, nil))
	if q.Code != 200 {
		t.Fatalf("query=%d", q.Code)
	}
	c := httptest.NewRecorder()
	handler.ServeHTTP(c, authReq(http.MethodPost, "/hunyuan/ai/v1/invocations/"+timed.InvocationID+"/cancel", token, nil))
	var cancel hunyuanapi.CancelResponse
	_ = json.Unmarshal(c.Body.Bytes(), &cancel)
	if cancel.Cost != nil || cancel.CostStatus != contract.HunyuanCostUnknown {
		t.Fatalf("cancel must keep unknown cost: %+v", cancel)
	}
}

func TestSSEInvoke(t *testing.T) {
	handler, token, _ := setup(t)
	req := authReq(http.MethodPost, "/hunyuan/ai/v1/invoke", token, map[string]any{"model": "mock-echo", "input": "sse"})
	req.Header.Set("Accept", "text/event-stream")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("sse status=%d", rec.Code)
	}
	body := rec.Body.String()
	if !bytes.Contains(rec.Body.Bytes(), []byte("event: delta")) || !bytes.Contains([]byte(body), []byte("event: result")) {
		t.Fatalf("sse body=%s", body)
	}
}

func TestBudgetUnguaranteed(t *testing.T) {
	handler, token, _ := setup(t)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, authReq(http.MethodPost, "/hunyuan/ai/v1/invoke", token, map[string]any{
		"model": "mock-echo", "input": "x", "budget_policy_ref": "unguaranteed",
	}))
	if rec.Code != 422 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMountKeepsOtherPaths(t *testing.T) {
	handler, token, _ := setup(t)
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(418)
	})
	mounted := hunyuanapi.Mount(next, handler)
	rec := httptest.NewRecorder()
	mounted.ServeHTTP(rec, authReq(http.MethodGet, "/v1/models", token, nil))
	if !called || rec.Code != 418 {
		t.Fatalf("next not called")
	}
}

func TestToolCallsReturnedNotExecuted(t *testing.T) {
	handler, token, _ := setup(t)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, authReq(http.MethodPost, "/hunyuan/ai/v1/invoke", token, map[string]any{
		"model": "mock-echo",
		"input": "call:lookup",
		"tools": []map[string]string{{"name": "lookup"}},
	}))
	var view hunyuanapi.InvocationView
	_ = json.Unmarshal(rec.Body.Bytes(), &view)
	if view.Status != contract.HunyuanStatusSucceeded || view.Output == nil || view.Output.Type != "tool_calls" {
		t.Fatalf("view=%+v body=%s", view, rec.Body.String())
	}
	if len(view.Output.ToolCalls) != 1 || view.Output.ToolCalls[0].Name != "lookup" {
		t.Fatalf("tools=%+v", view.Output.ToolCalls)
	}
}

func TestSchemaInvalidRejected(t *testing.T) {
	handler, token, _ := setup(t)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, authReq(http.MethodPost, "/hunyuan/ai/v1/invoke", token, map[string]any{
		"model": "mock-echo",
		"input": "hi",
		"response_schema": map[string]any{
			"type":                 "object",
			"properties":           map[string]any{"missing": map[string]any{"type": "string"}},
			"required":             []string{"missing"},
			"additionalProperties": false,
		},
	}))
	var view hunyuanapi.InvocationView
	_ = json.Unmarshal(rec.Body.Bytes(), &view)
	if view.Status != contract.HunyuanStatusFailed || view.Error == nil || view.Error.Code != contract.HunyuanErrSchemaInvalid {
		t.Fatalf("view=%+v body=%s", view, rec.Body.String())
	}
}

func TestStreamDisconnectBecomesUnknown(t *testing.T) {
	handler, token, _ := setup(t)
	req := authReq(http.MethodPost, "/hunyuan/ai/v1/invoke", token, map[string]any{"model": "mock-echo", "input": "cut"})
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Idempotency-Key", "cut-stream")
	rec := httptest.NewRecorder()
	failing := &failAfterWrites{ResponseWriter: rec, failAfter: 1}
	handler.ServeHTTP(failing, req)

	replay := authReq(http.MethodPost, "/hunyuan/ai/v1/invoke", token, map[string]any{"model": "mock-echo", "input": "cut"})
	replay.Header.Set("Idempotency-Key", "cut-stream")
	out := httptest.NewRecorder()
	handler.ServeHTTP(out, replay)
	var view hunyuanapi.InvocationView
	_ = json.Unmarshal(out.Body.Bytes(), &view)
	if view.Status != contract.HunyuanStatusUnknown {
		t.Fatalf("status=%s body=%s", view.Status, out.Body.String())
	}
	q := httptest.NewRecorder()
	handler.ServeHTTP(q, authReq(http.MethodGet, "/hunyuan/ai/v1/invocations/"+view.InvocationID, token, nil))
	_ = json.Unmarshal(q.Body.Bytes(), &view)
	if !bytes.Contains(q.Body.Bytes(), []byte(`"local_connection_closed"`)) && view.Status != contract.HunyuanStatusUnknown {
		t.Fatalf("query body=%s", q.Body.String())
	}
	// LocalConnectionClosed is on cancel response; query returns InvocationView without that field.
	// Confirm via cancel envelope.
	c := httptest.NewRecorder()
	handler.ServeHTTP(c, authReq(http.MethodPost, "/hunyuan/ai/v1/invocations/"+view.InvocationID+"/cancel", token, nil))
	var cancel hunyuanapi.CancelResponse
	_ = json.Unmarshal(c.Body.Bytes(), &cancel)
	if !cancel.LocalConnectionClosed || cancel.Invocation.Status != contract.HunyuanStatusUnknown {
		t.Fatalf("cancel=%+v", cancel)
	}
}

func TestInvocationSurvivesStoreReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "astrlink.db")
	store, err := sqlite.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := accesstoken.NewManager(store)
	if err != nil {
		t.Fatal(err)
	}
	created, err := manager.Create(context.Background(), "persist")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BindAllAccessTokensToHunyuanDev(context.Background()); err != nil {
		t.Fatal(err)
	}
	handler := hunyuanapi.New(store, ingress.AccessTokenAuthenticatorFunc(manager.Authenticate), hunyuanapi.MockProvider{})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, authReq(http.MethodPost, "/hunyuan/ai/v1/invoke", created.Value, map[string]any{
		"model": "mock-echo", "input": "persist-me",
	}))
	var view hunyuanapi.InvocationView
	_ = json.Unmarshal(rec.Body.Bytes(), &view)
	if view.InvocationID == "" {
		t.Fatalf("body=%s", rec.Body.String())
	}
	_ = store.Close()

	reopened, err := sqlite.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	got, err := reopened.GetHunyuanInvocation(context.Background(), view.InvocationID, contract.HunyuanDefaultProject)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != contract.HunyuanStatusSucceeded {
		t.Fatalf("status=%s", got.Status)
	}
}

func TestCrossProjectQueryForbidden(t *testing.T) {
	handler, token, store := setup(t)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, authReq(http.MethodPost, "/hunyuan/ai/v1/invoke", token, map[string]any{
		"model": "mock-echo", "input": "secret",
	}))
	var view hunyuanapi.InvocationView
	_ = json.Unmarshal(rec.Body.Bytes(), &view)

	manager, err := accesstoken.NewManager(store)
	if err != nil {
		t.Fatal(err)
	}
	other, err := manager.Create(context.Background(), "other-project")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureHunyuanProjectBinding(context.Background(), other.Token.ID, "other-project"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetHunyuanBudget(context.Background(), "other-project", contract.HunyuanDefaultBudget, 10, 4); err != nil {
		t.Fatal(err)
	}
	otherHandler := hunyuanapi.New(store, ingress.AccessTokenAuthenticatorFunc(manager.Authenticate), hunyuanapi.MockProvider{})
	q := httptest.NewRecorder()
	otherHandler.ServeHTTP(q, authReq(http.MethodGet, "/hunyuan/ai/v1/invocations/"+view.InvocationID, other.Value, nil))
	if q.Code != 404 {
		t.Fatalf("status=%d body=%s", q.Code, q.Body.String())
	}
}

type failAfterWrites struct {
	http.ResponseWriter
	failAfter int
	writes    int
}

func (w *failAfterWrites) Write(p []byte) (int, error) {
	w.writes++
	if w.writes > w.failAfter {
		return 0, context.Canceled
	}
	return w.ResponseWriter.Write(p)
}

func (w *failAfterWrites) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func TestUnboundTokenAndUnsupportedSchemaFailBeforeDispatch(t *testing.T) {
	_, _, store := setup(t)
	manager, err := accesstoken.NewManager(store)
	if err != nil {
		t.Fatal(err)
	}
	created, err := manager.Create(context.Background(), "explicit binding")
	if err != nil {
		t.Fatal(err)
	}
	handler := hunyuanapi.New(store, ingress.AccessTokenAuthenticatorFunc(manager.Authenticate), hunyuanapi.MockProvider{})
	invoke := func(body map[string]any) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, authReq(http.MethodPost, "/hunyuan/ai/v1/invoke", created.Value, body))
		return w
	}
	body := map[string]any{"model": "mock-echo", "input": "hello"}
	if w := invoke(body); w.Code != http.StatusForbidden {
		t.Fatalf("unbound=%d", w.Code)
	}
	if err := store.EnsureHunyuanProjectBinding(context.Background(), created.Token.ID, contract.HunyuanDefaultProject); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureHunyuanProjectBinding(context.Background(), created.Token.ID, "other"); err == nil {
		t.Fatal("silently moved binding")
	}
	if err := store.SetHunyuanBudget(context.Background(), contract.HunyuanDefaultProject, contract.HunyuanDefaultBudget, 1, 1); err != nil {
		t.Fatal(err)
	}
	body["response_schema"] = map[string]any{"type": "string", "pattern": "x"}
	if w := invoke(body); w.Code != 422 || !bytes.Contains(w.Body.Bytes(), []byte(contract.HunyuanErrSchemaUnsupported)) {
		t.Fatalf("unsupported=%s", w.Body.String())
	}
	delete(body, "response_schema")
	if w := invoke(body); w.Code != http.StatusOK {
		t.Fatalf("schema rejection consumed budget: %s", w.Body.String())
	}
}
