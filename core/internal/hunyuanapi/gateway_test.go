package hunyuanapi_test

import (
	"context"
	"encoding/json"
	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accesstoken"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/hunyuanapi"
	"github.com/QuantumNous/astrlink/core/internal/ingress"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/transport"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type gatewayResolver struct{ url string }

func (r gatewayResolver) Resolve(_ context.Context, req endpoint.ResolveRequest) (endpoint.Resolved, error) {
	if req.Model != "model" {
		return endpoint.Resolved{}, endpoint.ErrNoEndpoint
	}
	return endpoint.Resolved{Endpoint: contract.Endpoint{ID: "endpoint_test", Name: "test", Kind: contract.EndpointKindOpenAICompatible, BaseURL: r.url, Enabled: true, Auth: contract.EndpointAuth{Scheme: contract.AuthSchemeNone}, Capabilities: []contract.Capability{{Protocol: contract.ProtocolOpenAIChat, Mode: contract.CapabilityModeNative, Streaming: true}}}}, nil
}

type gatewayAuthorizer struct{}

func (gatewayAuthorizer) Headers(context.Context, contract.Endpoint, http.Header) (http.Header, error) {
	return http.Header{"Authorization": {"Bearer upstream-test"}, "X-AstrLink-Overlay": {"local"}}, nil
}

func TestGatewayIngressBoundaryPrivacyAuditAndOutgoingIdentity(t *testing.T) {
	_, token, store := setup(t)
	manager, err := accesstoken.NewManager(store)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer upstream-test" {
			t.Error("incorrect outgoing path/auth")
		}
		for key, values := range r.Header {
			if strings.HasPrefix(strings.ToLower(key), "x-astrlink-") {
				t.Errorf("reserved header %s", key)
			}
			if (strings.EqualFold(key, "User-Agent") || strings.EqualFold(key, "originator")) && strings.Contains(strings.ToLower(strings.Join(values, " ")), "astrlink") {
				t.Errorf("identity %s", key)
			}
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "AstrLink") {
			t.Error("caller content removed")
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(string(body), `"stream":true`) {
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		} else {
			io.WriteString(w, `{"model":"model","choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":5,"completion_tokens":1}}`)
		}
	}))
	defer upstream.Close()
	blocked := false
	filter, err := privacy.New(privacy.PolicyProviderFunc(func(_ context.Context, scope privacy.Scope) (privacy.Policy, error) {
		if scope.AccessTokenID == "" {
			t.Error("token scope lost")
		}
		if blocked {
			return privacy.Policy{Enabled: true, Mode: privacy.ModeRegex, Action: privacy.ActionBlock}, nil
		}
		return privacy.Policy{}, nil
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	auth := ingress.AccessTokenAuthenticatorFunc(manager.Authenticate)
	base, err := ingress.NewProduction(ingress.Dependencies{Resolver: gatewayResolver{upstream.URL}, Authorizer: gatewayAuthorizer{}, Forwarder: transport.New(upstream.Client().Transport), AccessTokenAuthenticator: auth, PrivacyFilter: filter, RequestRecords: store, AllowedHost: "127.0.0.1:8317"})
	if err != nil {
		t.Fatal(err)
	}
	provider := hunyuanapi.GatewayProvider{Handler: base, Models: func() ([]string, error) { return []string{"model"}, nil }}
	handler := hunyuanapi.Mount(base, hunyuanapi.New(store, auth, provider))
	call := func(input string, edit func(*http.Request)) *httptest.ResponseRecorder {
		r := authReq(http.MethodPost, "/hunyuan/ai/v1/invoke", token, map[string]any{"model": "model", "input": input})
		r.Host = "127.0.0.1:8317"
		r.Header.Set("X-AstrLink-Caller", "local")
		if edit != nil {
			edit(r)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, row := range []struct {
		edit   func(*http.Request)
		status int
	}{
		{func(r *http.Request) { r.Host = "evil.example" }, 421},
		{func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }, 403},
		{func(r *http.Request) { r.Header.Set("X-Api-Key", "different") }, 401},
	} {
		w := call("AstrLink", row.edit)
		if w.Code != row.status {
			t.Fatalf("boundary=%d %s", w.Code, w.Body.String())
		}
	}
	if calls != 0 {
		t.Fatal("boundary dispatched")
	}
	w := call("AstrLink caller text", nil)
	var view hunyuanapi.InvocationView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil || w.Code != 200 || view.Output == nil || view.Output.Text != "ok" || view.Usage == nil || view.Usage.InputTokens != 5 {
		t.Fatalf("result=%s error=%v", w.Body.String(), err)
	}
	w = call("AstrLink caller text", func(r *http.Request) { r.Header.Set("Accept", "text/event-stream") })
	if w.Code != 200 || !strings.Contains(w.Body.String(), "event: result") {
		t.Fatalf("stream=%s", w.Body.String())
	}
	blocked = true
	w = call("AstrLink alice@example.com", nil)
	if w.Code == 200 || calls != 2 {
		t.Fatalf("privacy bypass=%d calls=%d body=%s", w.Code, calls, w.Body.String())
	}
	records, err := store.ListRequestRecords(context.Background(), storagecontract.RequestRecordListOptions{Limit: 20})
	if err != nil || len(records.Items) < 3 {
		t.Fatalf("audit count=%d error=%v", len(records.Items), err)
	}
}
