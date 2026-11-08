package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type codedErr struct {
	code    string
	msg     string
	details map[string]any
}

func (e *codedErr) Error() string                { return e.msg }
func (e *codedErr) ErrorCode() string            { return e.code }
func (e *codedErr) ErrorDetails() map[string]any { return e.details }

type echoArgs struct {
	URL   string `json:"url"`
	Count int    `json:"count"`
}

// echoTool exercises the protocol layer: strict argument decoding, a required
// argument, an injected failure, and a payload with characters JSON would
// otherwise HTML-escape.
func echoTool(fail error) Tool {
	return Tool{
		Name:        "echo",
		Description: "Echoes its arguments back, for exercising the protocol layer in tests.",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"url": map[string]any{"type": "string"}, "count": map[string]any{"type": "integer"}},
			"required":   []string{"url"},
		},
		Annotations: map[string]any{"readOnlyHint": true, "openWorldHint": true},
		Handle: func(_ context.Context, raw json.RawMessage) (any, error) {
			var a echoArgs
			if err := DecodeArgs(raw, &a); err != nil {
				return nil, err
			}
			if a.URL == "" {
				return nil, ArgErrorf("url is required")
			}
			if fail != nil {
				return nil, fail
			}
			return map[string]any{"url": a.URL, "text": "<b>bold</b> & more"}, nil
		},
	}
}

// converse runs a whole stdio session and returns every response in order.
func converse(t *testing.T, s *Server, lines ...string) []map[string]any {
	t.Helper()
	in := strings.NewReader(strings.Join(lines, "\n") + "\n")
	var out bytes.Buffer
	if err := s.Serve(context.Background(), in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	var responses []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("response is not JSON: %q: %v", line, err)
		}
		responses = append(responses, m)
	}
	return responses
}

func result(t *testing.T, resp map[string]any) map[string]any {
	t.Helper()
	r, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result in %v", resp)
	}
	return r
}

func contentText(t *testing.T, r map[string]any) string {
	t.Helper()
	blocks, _ := r["content"].([]any)
	if len(blocks) != 1 {
		t.Fatalf("want one content block, got %v", r["content"])
	}
	b := blocks[0].(map[string]any)
	return b["text"].(string)
}

func TestInitializeReportsRevisionNameAndInstructions(t *testing.T) {
	s := New("v0.0.1")
	rs := converse(t, s, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	r := result(t, rs[0])
	if r["protocolVersion"] != "2025-03-26" {
		t.Errorf("protocolVersion = %v; annotations need 2025-03-26", r["protocolVersion"])
	}
	info := r["serverInfo"].(map[string]any)
	if info["name"] != ServerName || info["version"] != "v0.0.1" {
		t.Errorf("serverInfo = %v", info)
	}
	if instr, _ := r["instructions"].(string); !strings.Contains(instr, "get_usage") {
		t.Errorf("instructions must point at get_usage: %q", instr)
	}
}

func TestToolsListCarriesAnnotationsAndGetUsage(t *testing.T) {
	s := New("test", echoTool(nil))
	rs := converse(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	tools := result(t, rs[0])["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("want echo + get_usage, got %d tools", len(tools))
	}
	seen := map[string]bool{}
	for _, raw := range tools {
		def := raw.(map[string]any)
		name := def["name"].(string)
		seen[name] = true
		ann, ok := def["annotations"].(map[string]any)
		if !ok {
			t.Errorf("%s has no annotations in tools/list", name)
			continue
		}
		if _, ok := ann["readOnlyHint"]; !ok {
			t.Errorf("%s annotations lack readOnlyHint: %v", name, ann)
		}
	}
	if !seen["echo"] || !seen[ToolGetUsage] {
		t.Errorf("tools listed: %v", seen)
	}
}

func TestGetUsageReturnsTheManualAsRawText(t *testing.T) {
	s := New("test")
	rs := converse(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_usage","arguments":{}}}`)
	text := contentText(t, result(t, rs[0]))
	if text != UsageDoc() {
		t.Error("get_usage must return usage.md verbatim, not JSON-wrapped")
	}
}

func TestToolResultIsJSONWithoutHTMLEscaping(t *testing.T) {
	s := New("test", echoTool(nil))
	rs := converse(t, s, `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"echo","arguments":{"url":"https://example.org/"}}}`)
	r := result(t, rs[0])
	if r["isError"] == true {
		t.Fatalf("unexpected error: %v", r)
	}
	text := contentText(t, r)
	if !strings.Contains(text, `"<b>bold</b> & more"`) {
		t.Errorf("page text must not be HTML-escaped inside the JSON: %s", text)
	}
	var back map[string]any
	if err := json.Unmarshal([]byte(text), &back); err != nil || back["url"] != "https://example.org/" {
		t.Errorf("result is not the JSON payload: %s (%v)", text, err)
	}
}

func TestStrictArgumentsAndUnknownToolAreInvalidArgument(t *testing.T) {
	s := New("test", echoTool(nil))
	rs := converse(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"url":"x","typo":1}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"nope","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"echo","arguments":{"url":"x","count":"three"}}}`,
	)
	for i, resp := range rs {
		r := result(t, resp)
		if r["isError"] != true {
			t.Errorf("call %d should be a tool error: %v", i+1, r)
			continue
		}
		var e map[string]any
		if err := json.Unmarshal([]byte(contentText(t, r)), &e); err != nil {
			t.Fatalf("error text is not JSON: %v", err)
		}
		if e["code"] != CodeInvalidArgument {
			t.Errorf("call %d: code = %v, want %s", i+1, e["code"], CodeInvalidArgument)
		}
	}
}

func TestCodedErrorsSurfaceCodeAndDetails(t *testing.T) {
	fail := &codedErr{code: "address_not_allowed", msg: "resolves to a loopback address", details: map[string]any{"host": "localhost", "class": "loopback"}}
	s := New("test", echoTool(fail))
	rs := converse(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"url":"http://localhost/"}}}`)
	r := result(t, rs[0])
	var e map[string]any
	if err := json.Unmarshal([]byte(contentText(t, r)), &e); err != nil {
		t.Fatal(err)
	}
	if e["code"] != "address_not_allowed" || e["message"] != fail.msg {
		t.Errorf("code/message: %v", e)
	}
	d, _ := e["details"].(map[string]any)
	if d["class"] != "loopback" {
		t.Errorf("details lost: %v", e)
	}
	// A plain error without a code still reaches the model, under the layer's own code.
	s2 := New("test", echoTool(errors.New("boom")))
	rs = converse(t, s2, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"url":"x"}}}`)
	if err := json.Unmarshal([]byte(contentText(t, result(t, rs[0]))), &e); err != nil || e["code"] != CodeInvalidArgument {
		t.Errorf("plain error mapping: %v %v", e, err)
	}
}

func TestProtocolEdges(t *testing.T) {
	s := New("test")
	rs := converse(t, s,
		`not json`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1}}`,
		`{"jsonrpc":"2.0","id":5,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":6,"method":"resources/list"}`,
	)
	if len(rs) != 3 {
		t.Fatalf("want 3 replies (parse error, ping, method-not-found), got %d: %v", len(rs), rs)
	}
	if e, _ := rs[0]["error"].(map[string]any); e == nil || e["code"].(float64) != -32700 || rs[0]["id"] != nil {
		t.Errorf("malformed frame must get a -32700 with a null id: %v", rs[0])
	}
	if _, ok := rs[1]["result"]; !ok || rs[1]["id"].(float64) != 5 {
		t.Errorf("ping: %v", rs[1])
	}
	if e, _ := rs[2]["error"].(map[string]any); e == nil || e["code"].(float64) != -32601 {
		t.Errorf("unknown method must be -32601: %v", rs[2])
	}
}

func TestServeWithoutStdinIsAnError(t *testing.T) {
	if err := New("test").Serve(context.Background(), nil, &bytes.Buffer{}); err == nil {
		t.Error("nil stdin must be refused, not hang")
	}
}

// A handler that panics must not end the server (code-audit run-2,
// 2026-11-08: a page-authored <meta> attribute panicked the extractor and
// the stdio process with it). The panic is a tool error, and the next
// request is served.
func TestHandlerPanicIsAToolErrorAndTheServerGoesOn(t *testing.T) {
	bomb := Tool{
		Name: "bomb", Description: "Panics, for the recover test.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		Annotations: map[string]any{"readOnlyHint": true, "openWorldHint": false},
		Handle: func(context.Context, json.RawMessage) (any, error) {
			var s []byte
			_ = s[23:] // slice bounds out of range, as the extractor did
			return nil, nil
		},
	}
	s := New("test", bomb, echoTool(nil))
	rs := converse(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"bomb","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo","arguments":{"url":"x"}}}`,
	)
	if len(rs) != 2 {
		t.Fatalf("want 2 replies, got %d: %v", len(rs), rs)
	}
	r := result(t, rs[0])
	if r["isError"] != true {
		t.Errorf("a panic must be a tool error: %v", r)
	}
	var e map[string]any
	if err := json.Unmarshal([]byte(contentText(t, r)), &e); err != nil || e["code"] != CodeInternalError {
		t.Errorf("code: %v %v", e, err)
	}
	if msg, _ := e["message"].(string); strings.Contains(msg, "out of range") || strings.Contains(msg, "runtime error") {
		t.Errorf("the panic value must not reach the model: %q", msg)
	}
	if contentText(t, result(t, rs[1])) == "" || result(t, rs[1])["isError"] == true {
		t.Errorf("the request after the panic was not served: %v", rs[1])
	}
}
