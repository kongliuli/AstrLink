package hunyuanapi

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"sync"

	"github.com/QuantumNous/astrlink/core/contract"
)

type gatewayRequestKey struct{}

type GatewayProvider struct {
	Handler http.Handler
	Models  func() ([]string, error)
}

func (GatewayProvider) Name() string { return "gateway" }

func (provider GatewayProvider) Capabilities() []ModelCapability {
	models, err := provider.Models()
	if err != nil {
		return nil
	}
	sort.Strings(models)
	seen := map[string]bool{}
	result := make([]ModelCapability, 0, len(models))
	for _, model := range models {
		if model == "" || seen[model] {
			continue
		}
		seen[model] = true
		result = append(result, ModelCapability{ID: model, Stream: contract.HunyuanSupportUnknown, Tools: contract.HunyuanSupportUnknown, StructuredOutput: contract.HunyuanSupportUnknown})
	}
	return result
}

func (provider GatewayProvider) Support(model, feature string) string {
	if feature != "model" {
		return contract.HunyuanSupportUnknown
	}
	for _, item := range provider.Capabilities() {
		if item.ID == model {
			return contract.HunyuanSupportSupported
		}
	}
	return contract.HunyuanSupportUnsupported
}

func (provider GatewayProvider) Generate(ctx context.Context, req InvokeRequest, emit func(string) error) (Output, error) {
	original, ok := ctx.Value(gatewayRequestKey{}).(*http.Request)
	if !ok || provider.Handler == nil {
		return Output{}, fmt.Errorf("gateway request context is missing")
	}
	body, err := chatPayload(req, emit != nil)
	if err != nil {
		return Output{}, err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	request := original.Clone(ctx)
	request.Method = http.MethodPost
	request.URL = &url.URL{Path: "/v1/chat/completions"}
	request.RequestURI = request.URL.Path
	request.Body = io.NopCloser(bytes.NewReader(body))
	request.ContentLength = int64(len(body))
	request.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	request.Header.Set("Content-Type", "application/json")
	request.Header.Del("Content-Encoding")
	request.Header.Del("Content-Length")
	request.Header.Del("Idempotency-Key")
	request.Header.Set("Accept", "application/json")
	if emit != nil {
		request.Header.Set("Accept", "text/event-stream")
	}
	reader, writer := io.Pipe()
	defer reader.Close()
	response := &gatewayResponseWriter{header: make(http.Header), body: writer, status: make(chan int, 1)}
	go func() {
		defer writer.Close()
		defer response.WriteHeader(http.StatusOK)
		provider.Handler.ServeHTTP(response, request)
	}()
	select {
	case status := <-response.status:
		return readChatResponse(status, reader, emit)
	case <-ctx.Done():
		return Output{}, ctx.Err()
	}
}

type gatewayResponseWriter struct {
	header http.Header
	body   *io.PipeWriter
	status chan int
	once   sync.Once
}

func (writer *gatewayResponseWriter) Header() http.Header { return writer.header }
func (writer *gatewayResponseWriter) WriteHeader(status int) {
	writer.once.Do(func() { writer.status <- status })
}
func (writer *gatewayResponseWriter) Write(data []byte) (int, error) {
	writer.WriteHeader(http.StatusOK)
	return writer.body.Write(data)
}
func (writer *gatewayResponseWriter) Flush() { writer.WriteHeader(http.StatusOK) }
