package mcp

import (
	"encoding/json"
	"testing"
)

// Finding 9 of the pre-release review: the layer's own invalid_argument named
// no argument, and get_usage ignored arguments.
func TestInvalidArgumentNamesTheArgumentAndGetUsageIsStrict(t *testing.T) {
	s := New("test", echoTool(nil))
	rs := converse(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"url":"x","typo":1}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo","arguments":{"url":"x","count":"three"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_usage","arguments":{"verbose":true}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"get_usage","arguments":{}}}`,
	)
	want := []string{"typo", "count", "verbose"}
	for i, arg := range want {
		r := result(t, rs[i])
		var e map[string]any
		if err := json.Unmarshal([]byte(contentText(t, r)), &e); err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
		d, _ := e["details"].(map[string]any)
		if e["code"] != CodeInvalidArgument || d["argument"] != arg {
			t.Errorf("call %d: code=%v details=%v, want argument %q", i+1, e["code"], d, arg)
		}
	}
	if contentText(t, result(t, rs[3])) != UsageDoc() {
		t.Error("get_usage with an empty object still returns the manual")
	}
}
