package hunyuanapi

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestStreamTerminalAndFragmentedTools(t *testing.T) {
	partial := `data: {"choices":[{"delta":{"content":"partial"}}]}` + "\n\n"
	if _, err := readChatSSE(strings.NewReader(partial), nil); !errors.Is(err, errUnknownResult) {
		t.Fatalf("early EOF: %v", err)
	}
	if _, err := readChatSSE(strings.NewReader("data: broken\n\ndata: [DONE]\n\n"), nil); err == nil {
		t.Fatal("malformed event accepted")
	}
	if _, err := readChatSSE(strings.NewReader("data: {\"error\":{\"code\":\"server_error\"}}\n\n"), nil); err == nil {
		t.Fatal("upstream error accepted")
	}
	stream := `data: {"model":"actual","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"lookup","arguments":"{\"q\":"}}]}}]}` + "\n\n" +
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"ok\"}"}}]},"finish_reason":"tool_calls"}]}` + "\n\n" +
		`data: {"choices":[],"usage":{"prompt_tokens":7,"completion_tokens":3}}` + "\n\ndata: [DONE]\n\n"
	out, err := readChatSSE(strings.NewReader(stream), nil)
	if err != nil || len(out.ToolCalls) != 1 || out.ToolCalls[0].ID != "call_1" || string(out.ToolCalls[0].Arguments) != `{"q":"ok"}` || out.Usage == nil || out.Usage.InputTokens != 7 || out.ActualModel != "actual" {
		t.Fatalf("output=%+v error=%v", out, err)
	}
	if _, err := json.Marshal(out); err != nil {
		t.Fatal(err)
	}
}

func TestSchemaConstraintsAndUnsupportedKeywords(t *testing.T) {
	for _, row := range []struct {
		schema, instance string
		valid            bool
	}{
		{`{"type":"integer"}`, `1.5`, false},
		{`{"type":"integer","minimum":0}`, `-1`, false},
		{`{"enum":["allowed"]}`, `"forbidden"`, false},
		{`{"type":"array","items":{"type":"integer"}}`, `["one"]`, false},
		{`{"type":"array","items":{"type":"integer"}}`, `[1,2]`, true},
		{`{"type":"null"}`, `null`, true},
		{`{"type":"object","properties":{"n":{"type":"integer"}},"required":["n"],"additionalProperties":false}`, `{"n":1}`, true},
		{`{"type":"string","pattern":"^allowed$"}`, `"forbidden"`, false},
	} {
		err := validateInstance(json.RawMessage(row.schema), json.RawMessage(row.instance))
		if (err == nil) != row.valid {
			t.Fatalf("schema=%s instance=%s error=%v", row.schema, row.instance, err)
		}
	}
}
