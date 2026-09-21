package app

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nlink-jp/web-fetch/internal/config"
	"github.com/nlink-jp/web-fetch/internal/mcp"
)

// The arch test lives here rather than in internal/mcp for the same reason
// the manual meta-tests do: internal/mcp's own tests see get_usage alone,
// because the real tool set is assembled here with a configuration.

// TestEveryToolSchemaIsClosed is the arch test organization ADR-021 §10
// requires: every registered tool's input schema sets
// additionalProperties:false, so a client validating arguments against the
// schema refuses a mistyped parameter instead of sending it on.
//
// Both halves of the contract are needed and each is tested: the schema stops
// the typo at the client, and mcp.DecodeArgs stops it at the server — which
// TestMCPFetchRoundTrip in tools_test.go already drives end to end over real
// JSON-RPC, asserting an unknown argument comes back as invalid_argument.
func TestEveryToolSchemaIsClosed(t *testing.T) {
	srv := mcp.New("t", tools(config.Defaults(), "t")...)
	registered := srv.Tools()
	// Vacuity guard: with an empty list every assertion below passes without
	// having examined anything.
	if len(registered) == 0 {
		t.Fatal("the server registers no tools, so this test proves nothing")
	}
	for _, tool := range registered {
		if tool.InputSchema == nil {
			t.Errorf("tool %q has no input schema", tool.Name)
			continue
		}
		if tool.InputSchema["type"] != "object" {
			t.Errorf("tool %q: schema type = %v, want object", tool.Name, tool.InputSchema["type"])
		}
		if tool.InputSchema["additionalProperties"] != false {
			t.Errorf("tool %q: input schema does not set additionalProperties:false "+
				"(got %v) — a validating client would pass an agent's mistyped "+
				"argument through unnoticed (organization ADR-021 §10)",
				tool.Name, tool.InputSchema["additionalProperties"])
		}
	}
}

// TestClosedSchemaSurvivesTheWire checks the closing where it has to hold —
// in the tools/list JSON a client actually receives — not just on the Go
// value, since that is the only copy a client ever validates against.
func TestClosedSchemaSurvivesTheWire(t *testing.T) {
	srv := mcp.New("t", tools(config.Defaults(), "t")...)
	var out strings.Builder
	if err := srv.Serve(t.Context(),
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`+"\n"), &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	var resp struct {
		Result struct {
			Tools []struct {
				Name   string `json:"name"`
				Schema struct {
					AdditionalProperties *bool `json:"additionalProperties"`
				} `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out.String()), &resp); err != nil {
		t.Fatalf("decode tools/list %q: %v", out.String(), err)
	}
	if len(resp.Result.Tools) == 0 {
		t.Fatalf("tools/list advertised nothing: %s", out.String())
	}
	for _, tl := range resp.Result.Tools {
		if tl.Schema.AdditionalProperties == nil || *tl.Schema.AdditionalProperties {
			t.Errorf("tool %q: additionalProperties did not reach the wire as false", tl.Name)
		}
	}
}
