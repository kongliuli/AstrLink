package hunyuanapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/hunyuanapi"
)

func TestRealProviderChatCompletionViaHTTPTest(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("auth=%q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["stream"] == true {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": "hello-real"}},
			},
		})
	}))
	defer upstream.Close()

	provider, err := hunyuanapi.NewRealProvider(hunyuanapi.RealConfig{
		BaseURL: upstream.URL,
		APIKey:  "test-key",
		Models:  []string{"demo"},
		Client:  upstream.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if provider.Name() != "openai_compatible" {
		t.Fatalf("name=%s", provider.Name())
	}
	out, err := provider.Generate(context.Background(), hunyuanapi.InvokeRequest{
		Model: "demo", Input: "ping",
	}, nil)
	if err != nil || out.Text != "hello-real" {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	var deltas []string
	out, err = provider.Generate(context.Background(), hunyuanapi.InvokeRequest{
		Model: "demo", Input: "ping",
	}, func(delta string) error {
		deltas = append(deltas, delta)
		return nil
	})
	if err != nil || strings.Join(deltas, "") != "hi" || out.Text != "hi" {
		t.Fatalf("stream out=%+v deltas=%v err=%v", out, deltas, err)
	}
}

func TestRealConfigFromEnvRequiresExplicitVars(t *testing.T) {
	t.Setenv("ASTRLINK_HUNYUAN_REAL", "")
	if _, err := hunyuanapi.RealConfigFromEnv(); err == nil {
		t.Fatal("expected disabled error")
	}
	t.Setenv("ASTRLINK_HUNYUAN_REAL", "1")
	t.Setenv("ASTRLINK_HUNYUAN_REAL_BASE_URL", "")
	t.Setenv("ASTRLINK_HUNYUAN_REAL_API_KEY", "")
	if _, err := hunyuanapi.RealConfigFromEnv(); err == nil {
		t.Fatal("expected missing url/key error")
	}
	t.Setenv("ASTRLINK_HUNYUAN_REAL_BASE_URL", "http://127.0.0.1:9")
	t.Setenv("ASTRLINK_HUNYUAN_REAL_API_KEY", "k")
	cfg, err := hunyuanapi.RealConfigFromEnv()
	if err != nil || cfg.BaseURL == "" {
		t.Fatalf("cfg=%+v err=%v", cfg, err)
	}
	_ = contract.HunyuanMockProvider
}
