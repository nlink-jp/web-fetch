package app

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/nlink-jp/mcp"
	"github.com/nlink-jp/mcp/client"

	"github.com/nlink-jp/web-fetch/internal/config"
	mcpsrv "github.com/nlink-jp/web-fetch/internal/mcp"
)

// openTools serves the real tool set and opens it with nlink-jp/mcp's client.
// A call that got past the argument check would fetch only what it names;
// the arguments sent here name no fetchable URL.
func openTools(t *testing.T) (*client.Client, []mcpsrv.Tool) {
	t.Helper()
	cfg := config.Defaults()
	_ = cfg
	srv := mcpsrv.New("t", tools(cfg, "t")...)
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = srv.Serve(ctx, inR, outW); _ = outW.Close(); close(done) }()
	kill := func() { cancel(); _ = inW.Close(); _ = outR.Close() }
	conn, err := client.NewLineConn(outR, inW, kill, client.LineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	octx, ocancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer ocancel()
	cl, err := client.Open(octx, conn, client.Options{Client: mcp.Implementation{Name: "test", Version: "0"}})
	if err != nil {
		kill()
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = cl.Close(); <-done; kill() })
	return cl, srv.Tools()
}

func toolMessage(t *testing.T, cl *client.Client, tool, args string) (string, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := cl.CallTool(ctx, tool, json.RawMessage(args), nil)
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	var block struct{ Text string }
	_ = json.Unmarshal(res.Content[0], &block)
	var e struct{ Message string }
	_ = json.Unmarshal([]byte(block.Text), &e)
	return e.Message, res.IsError
}

// Organization ADR-021 §4: every tool, including get_usage, refuses an
// argument it does not declare, by name.
func TestEveryToolRefusesAnUndeclaredArgumentByName(t *testing.T) {
	cl, defs := openTools(t)
	for _, def := range defs {
		msg, isErr := toolMessage(t, cl, def.Name, `{"no_such_argument":1}`)
		if !isErr || !strings.Contains(msg, `unknown field "no_such_argument"`) {
			t.Errorf("%s accepted an undeclared argument: %q", def.Name, msg)
		}
	}
}

// encoding/json matches names case-insensitively; the schema does not. Every
// declared argument, spelt with a capital first letter, is refused by name.
func TestAnArgumentInAnotherCaseIsRefused(t *testing.T) {
	cl, defs := openTools(t)
	for _, def := range defs {
		props, _ := def.InputSchema["properties"].(map[string]any)
		for prop, spec := range props {
			other := strings.ToUpper(prop[:1]) + prop[1:]
			if other == prop {
				continue
			}
			v := `"x"`
			if m, ok := spec.(map[string]any); ok {
				switch m["type"] {
				case "integer", "number":
					v = "1"
				case "boolean":
					v = "true"
				case "array":
					v = "[]"
				case "object":
					v = "{}"
				}
			}
			msg, isErr := toolMessage(t, cl, def.Name, `{"`+other+`":`+v+`}`)
			if !isErr || !strings.Contains(msg, `unknown field "`+other+`"`) {
				t.Errorf("%s accepted %q for %q: %q", def.Name, other, prop, msg)
			}
		}
	}
}

// Every tool's annotations reach tools/list as the tool declares them: the
// readOnlyHint and openWorldHint a client decides on are not lost on the way
// to the module.
func TestToolAnnotationsReachTheWire(t *testing.T) {
	cl, defs := openTools(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	list, err := cl.ListTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]map[string]any{}
	for _, tl := range list.Tools {
		var def struct {
			Annotations map[string]any `json:"annotations"`
		}
		if err := json.Unmarshal(tl.Raw, &def); err != nil {
			t.Fatal(err)
		}
		got[tl.Name] = def.Annotations
	}
	for _, def := range defs {
		want, _ := json.Marshal(def.Annotations)
		have, _ := json.Marshal(got[def.Name])
		if len(def.Annotations) == 0 || string(want) != string(have) {
			t.Errorf("%s: annotations on the wire %s, declared %s", def.Name, have, want)
		}
	}
}
