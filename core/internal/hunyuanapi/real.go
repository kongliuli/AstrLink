package hunyuanapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

// RealConfig is an opt-in OpenAI-compatible upstream. Credentials come only from
// explicit fields or env vars — never from personal credential files on disk.
type RealConfig struct {
	BaseURL string
	APIKey  string
	Client  *http.Client
	Models  []string
}

func RealEnabled() bool {
	return os.Getenv("ASTRLINK_HUNYUAN_REAL") == "1"
}

func RealConfigFromEnv() (RealConfig, error) {
	if !RealEnabled() {
		return RealConfig{}, fmt.Errorf("ASTRLINK_HUNYUAN_REAL is not enabled")
	}
	cfg := RealConfig{
		BaseURL: strings.TrimRight(strings.TrimSpace(os.Getenv("ASTRLINK_HUNYUAN_REAL_BASE_URL")), "/"),
		APIKey:  strings.TrimSpace(os.Getenv("ASTRLINK_HUNYUAN_REAL_API_KEY")),
		Models:  splitCSV(os.Getenv("ASTRLINK_HUNYUAN_REAL_MODELS")),
	}
	if cfg.BaseURL == "" || cfg.APIKey == "" {
		return RealConfig{}, fmt.Errorf("ASTRLINK_HUNYUAN_REAL_BASE_URL and ASTRLINK_HUNYUAN_REAL_API_KEY are required when real mode is on")
	}
	if len(cfg.Models) == 0 {
		cfg.Models = []string{"gpt-4o-mini"}
	}
	return cfg, nil
}

type RealProvider struct {
	cfg RealConfig
}

func NewRealProvider(cfg RealConfig) (*RealProvider, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" || strings.TrimSpace(cfg.APIKey) == "" {
		return nil, fmt.Errorf("real provider base URL and API key are required")
	}
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: 60 * time.Second}
	}
	if len(cfg.Models) == 0 {
		cfg.Models = []string{"gpt-4o-mini"}
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &RealProvider{cfg: cfg}, nil
}

func (RealProvider) Name() string { return "openai_compatible" }

func (provider *RealProvider) Capabilities() []ModelCapability {
	out := make([]ModelCapability, 0, len(provider.cfg.Models))
	for _, id := range provider.cfg.Models {
		out = append(out, ModelCapability{
			ID:               id,
			Stream:           contract.HunyuanSupportSupported,
			Tools:            contract.HunyuanSupportSupported,
			StructuredOutput: contract.HunyuanSupportUnknown,
			Notes:            "upstream OpenAI-compatible; cost stays unknown",
		})
	}
	return out
}

func (provider *RealProvider) Support(model, feature string) string {
	known := false
	for _, id := range provider.cfg.Models {
		if id == model {
			known = true
			break
		}
	}
	if feature == "model" {
		if known {
			return contract.HunyuanSupportSupported
		}
		// Allow any model id; upstream may reject.
		return contract.HunyuanSupportUnknown
	}
	if !known {
		return contract.HunyuanSupportUnknown
	}
	switch feature {
	case "stream", "tools":
		return contract.HunyuanSupportSupported
	case "structured_output":
		return contract.HunyuanSupportUnknown
	default:
		return contract.HunyuanSupportUnknown
	}
}

func (provider *RealProvider) Generate(ctx context.Context, req InvokeRequest, emit func(string) error) (Output, error) {
	if len(req.ResponseSchema) > 0 && provider.Support(req.Model, "structured_output") == contract.HunyuanSupportUnsupported {
		return Output{}, fmt.Errorf("%s: structured output unsupported", contract.HunyuanErrCapabilityUnsupported)
	}
	stream := emit != nil
	body, err := chatPayload(req, stream)
	if err != nil {
		return Output{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, provider.cfg.BaseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Output{}, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+provider.cfg.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")
	if stream {
		httpReq.Header.Set("Accept", "text/event-stream")
	}
	resp, err := provider.cfg.Client.Do(httpReq)
	if err != nil {
		return Output{}, err
	}
	defer resp.Body.Close()
	return readChatResponse(resp.StatusCode, resp.Body, emit)
}

func chatPayload(req InvokeRequest, stream bool) ([]byte, error) {
	payload := map[string]any{
		"model": req.Model,
		"messages": []map[string]string{
			{"role": "user", "content": req.Input},
		},
		"stream": stream,
	}
	if req.MaxOutputTokens > 0 {
		payload["max_tokens"] = req.MaxOutputTokens
	}
	if len(req.ResponseSchema) > 0 {
		payload["response_format"] = map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "response", "schema": req.ResponseSchema, "strict": true}}
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, tool := range req.Tools {
			parameters := tool.Parameters
			if len(parameters) == 0 {
				parameters = json.RawMessage(`{"type":"object"}`)
			}
			tools = append(tools, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        tool.Name,
					"description": tool.Description,
					"parameters":  parameters,
				},
			})
		}
		payload["tools"] = tools
	}
	return json.Marshal(payload)
}

func readChatResponse(status int, body io.Reader, emit func(string) error) (Output, error) {
	if status < 200 || status >= 300 {
		return Output{}, fmt.Errorf("upstream status %d", status)
	}
	if emit != nil {
		return readChatSSE(body, emit)
	}
	limited, err := io.ReadAll(io.LimitReader(body, (1<<20)+1))
	if err != nil {
		return Output{}, err
	}
	if len(limited) > 1<<20 {
		return Output{}, fmt.Errorf("upstream response exceeds size limit")
	}
	return parseChatCompletion(limited)
}

func readChatSSE(body io.Reader, emit func(string) error) (Output, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var text strings.Builder
	toolCalls := map[int]*ToolCall{}
	terminal := false
	done := false
	var model string
	var usage *Usage
	var eventLines []string
	consume := func(data string) error {
		if data == "[DONE]" {
			terminal = true
			done = true
			return nil
		}
		var chunk struct {
			Model string          `json:"model"`
			Error json.RawMessage `json:"error"`
			Usage *struct {
				Input  int `json:"prompt_tokens"`
				Output int `json:"completion_tokens"`
			} `json:"usage"`
			Choices []struct {
				Index        int     `json:"index"`
				FinishReason *string `json:"finish_reason"`
				Delta        struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return fmt.Errorf("invalid upstream stream event: %w", err)
		}
		if len(chunk.Error) > 0 && string(chunk.Error) != "null" {
			return fmt.Errorf("upstream stream reported failure")
		}
		if chunk.Model != "" {
			model = chunk.Model
		}
		if chunk.Usage != nil {
			usage = &Usage{InputTokens: chunk.Usage.Input, OutputTokens: chunk.Usage.Output}
		}
		for _, choice := range chunk.Choices {
			if choice.Index != 0 {
				continue
			}
			if choice.FinishReason != nil && *choice.FinishReason != "" {
				terminal = true
			}
			if delta := choice.Delta.Content; delta != "" {
				text.WriteString(delta)
				if emit != nil {
					if err := emit(delta); err != nil {
						return err
					}
				}
			}
			for _, fragment := range choice.Delta.ToolCalls {
				call := toolCalls[fragment.Index]
				if call == nil {
					call = &ToolCall{}
					toolCalls[fragment.Index] = call
				}
				if fragment.ID != "" {
					call.ID = fragment.ID
				}
				call.Name += fragment.Function.Name
				call.Arguments = append(call.Arguments, fragment.Function.Arguments...)
			}
		}
		return nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if len(eventLines) > 0 {
				if err := consume(strings.Join(eventLines, "\n")); err != nil {
					return Output{}, err
				}
				eventLines = nil
			}
			if done {
				break
			}
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		eventLines = append(eventLines, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
	}
	if err := scanner.Err(); err != nil {
		return Output{}, err
	}
	if len(eventLines) > 0 {
		if err := consume(strings.Join(eventLines, "\n")); err != nil {
			return Output{}, err
		}
	}
	if !terminal {
		return Output{}, errUnknownResult
	}
	if len(toolCalls) > 0 {
		indices := make([]int, 0, len(toolCalls))
		for index := range toolCalls {
			indices = append(indices, index)
		}
		sort.Ints(indices)
		calls := make([]ToolCall, 0, len(indices))
		for _, index := range indices {
			call := *toolCalls[index]
			if call.Name == "" || !json.Valid(call.Arguments) {
				return Output{}, fmt.Errorf("invalid upstream tool call")
			}
			calls = append(calls, call)
		}
		return Output{Type: "tool_calls", ToolCalls: calls, Usage: usage, ActualModel: model}, nil
	}
	return Output{Type: "text", Text: text.String(), Usage: usage, ActualModel: model}, nil
}

func parseChatCompletion(raw []byte) (Output, error) {
	var doc struct {
		Model string `json:"model"`
		Usage *struct {
			Input  int `json:"prompt_tokens"`
			Output int `json:"completion_tokens"`
		} `json:"usage"`
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return Output{}, err
	}
	if len(doc.Choices) == 0 {
		return Output{}, fmt.Errorf("upstream returned no choices")
	}
	msg := doc.Choices[0].Message
	var usage *Usage
	if doc.Usage != nil {
		usage = &Usage{InputTokens: doc.Usage.Input, OutputTokens: doc.Usage.Output}
	}
	if len(msg.ToolCalls) > 0 {
		calls := make([]ToolCall, 0, len(msg.ToolCalls))
		for _, call := range msg.ToolCalls {
			args := json.RawMessage(call.Function.Arguments)
			if call.Function.Name == "" || !json.Valid(args) {
				return Output{}, fmt.Errorf("invalid upstream tool call")
			}
			calls = append(calls, ToolCall{ID: call.ID, Name: call.Function.Name, Arguments: args})
		}
		return Output{Type: "tool_calls", ToolCalls: calls, Usage: usage, ActualModel: doc.Model}, nil
	}
	return Output{Type: "text", Text: msg.Content, Usage: usage, ActualModel: doc.Model}, nil
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
