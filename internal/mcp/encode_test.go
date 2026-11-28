package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

// A result this layer cannot encode is a defect in the server, reported as
// internal_error — not as the caller's argument error.
func TestAnUnencodableResultIsAnInternalError(t *testing.T) {
	bad := Tool{
		Name: "bad", Description: "Returns what JSON cannot hold.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		Handle:      func(context.Context, json.RawMessage) (any, error) { return make(chan int), nil },
	}
	res := New("test", bad).callTool(context.Background(), "bad", json.RawMessage(`{}`))
	if res["isError"] != true {
		t.Fatalf("an unencodable result was not an error: %v", res)
	}
	var e struct{ Code string }
	text := res["content"].([]map[string]any)[0]["text"].(string)
	if err := json.Unmarshal([]byte(text), &e); err != nil || e.Code != CodeInternalError {
		t.Errorf("code = %q (%v), want %s", e.Code, err, CodeInternalError)
	}
}
