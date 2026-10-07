package hunyuanapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

type MockProvider struct{}

func (MockProvider) Name() string { return contract.HunyuanMockProvider }

func (MockProvider) Capabilities() []ModelCapability {
	return []ModelCapability{
		{ID: "mock-echo", Stream: contract.HunyuanSupportSupported, Tools: contract.HunyuanSupportSupported, StructuredOutput: contract.HunyuanSupportSupported},
		{ID: "mock-text", Stream: contract.HunyuanSupportUnsupported, Tools: contract.HunyuanSupportUnsupported, StructuredOutput: contract.HunyuanSupportUnsupported, Notes: "plain text only"},
		{ID: "mock-unknown", Stream: contract.HunyuanSupportUnknown, Tools: contract.HunyuanSupportUnknown, StructuredOutput: contract.HunyuanSupportUnknown, Notes: "leaves result unknown"},
		{ID: "mock-slow", Stream: contract.HunyuanSupportSupported, Tools: contract.HunyuanSupportUnsupported, StructuredOutput: contract.HunyuanSupportUnsupported},
	}
}

func (provider MockProvider) Support(model, feature string) string {
	for _, item := range provider.Capabilities() {
		if item.ID != model {
			continue
		}
		switch feature {
		case "model":
			return contract.HunyuanSupportSupported
		case "stream":
			return item.Stream
		case "tools":
			return item.Tools
		case "structured_output":
			return item.StructuredOutput
		}
	}
	if feature == "model" {
		return contract.HunyuanSupportUnsupported
	}
	return contract.HunyuanSupportUnknown
}

func (MockProvider) Generate(ctx context.Context, req InvokeRequest, emit func(string) error) (Output, error) {
	switch req.Model {
	case "mock-unknown":
		return Output{}, errUnknownResult
	case "mock-slow":
		timer := time.NewTimer(time.Hour)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return Output{}, ctx.Err()
		case <-timer.C:
			return Output{Type: "text", Text: "late"}, nil
		}
	case "mock-text":
		return Output{Type: "text", Text: "plain:" + req.Input}, nil
	}
	if emit != nil {
		for _, part := range []string{"he", "llo"} {
			if err := emit(part); err != nil {
				return Output{}, err
			}
		}
	}
	if strings.HasPrefix(req.Input, "call:") && len(req.Tools) > 0 {
		name := strings.TrimPrefix(req.Input, "call:")
		args, _ := json.Marshal(map[string]string{"q": name})
		return Output{Type: "tool_calls", ToolCalls: []ToolCall{{Name: req.Tools[0].Name, Arguments: args}}}, nil
	}
	if len(req.ResponseSchema) > 0 {
		body, _ := json.Marshal(map[string]string{"echo": req.Input})
		return Output{Type: "json", JSON: body}, nil
	}
	text := "echo:" + req.Input
	if req.MaxOutputTokens > 0 && len(text) > req.MaxOutputTokens {
		text = text[:req.MaxOutputTokens]
	}
	return Output{Type: "text", Text: text}, nil
}

var errUnknownResult = fmt.Errorf("unknown")
