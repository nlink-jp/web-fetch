package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/nlink-jp/mcp"
	"github.com/nlink-jp/mcp/client"
)

// runMCPOverClient runs the subcommand args over pipes and opens it with
// nlink-jp/mcp's client, the way a dual-era client does (server-migration §6),
// then replays the JSON-RPC request lines read from stdin through the client
// and writes the answers to stdout in the JSON-RPC shape the tests read. It
// returns the subcommand's exit code. initialize is answered from what the
// client learnt when it opened the server; notifications, ping and other
// protocol details are the module's and fail the test. A subcommand that
// exits before it can be opened (a refused config) writes nothing to stdout.
func runMCPOverClient(t *testing.T, args []string, version string, stdin io.Reader, stdout, stderr io.Writer) int {
	t.Helper()
	input, err := io.ReadAll(stdin)
	if err != nil {
		t.Fatal(err)
	}
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan int, 1)
	go func() {
		done <- run(args, version, inR, outW, stderr)
		_ = outW.Close()
	}()
	kill := func() { _ = inW.Close(); _ = outR.Close() }
	// Whatever ends the test, nothing is left running: closing the pipes
	// ends the subcommand's reads and writes.
	t.Cleanup(kill)
	conn, err := client.NewLineConn(outR, inW, kill, client.LineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	octx, ocancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer ocancel()
	type opened struct {
		cl  *client.Client
		err error
	}
	openCh := make(chan opened, 1)
	go func() {
		c, err := client.Open(octx, conn, client.Options{Client: mcp.Implementation{Name: "test", Version: "0"}})
		openCh <- opened{c, err}
	}()
	var cl *client.Client
	select {
	case code := <-done:
		// The subcommand ended before it could be opened (a refused config):
		// that exit is the answer, and stdout stays empty.
		kill()
		return code
	case o := <-openCh:
		if o.err != nil {
			kill()
			code := <-done
			t.Fatalf("open: %v (the subcommand exited %d)", o.err, code)
		}
		cl = o.cl
	}
	sc := bufio.NewScanner(bytes.NewReader(input))
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(line, &req); err != nil {
			t.Fatalf("not a request: %s", line)
		}
		if len(req.ID) == 0 || string(req.ID) == "null" {
			t.Fatalf("a notification (%s) is the protocol module's; it is tested there", req.Method)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		answer := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		switch req.Method {
		case "initialize":
			result := map[string]any{"protocolVersion": cl.Version(), "serverInfo": cl.Server()}
			if s := cl.Instructions(); s != "" {
				result["instructions"] = s
			}
			answer["result"] = result
		case "tools/list":
			list, err := cl.ListTools(ctx)
			if err != nil {
				t.Fatalf("tools/list: %v", err)
			}
			tools := []json.RawMessage{}
			for _, tl := range list.Tools {
				tools = append(tools, tl.Raw)
			}
			answer["result"] = map[string]any{"tools": tools}
		case "tools/call":
			var p struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
				Meta      map[string]any  `json:"_meta"`
			}
			_ = json.Unmarshal(req.Params, &p)
			var a any
			if len(p.Arguments) > 0 {
				a = p.Arguments
			}
			res, err := cl.CallTool(ctx, p.Name, a, p.Meta)
			var rpcErr *mcp.Error
			switch {
			case errors.As(err, &rpcErr):
				answer["error"] = map[string]any{"code": rpcErr.Code, "message": rpcErr.Message}
			case err != nil:
				t.Fatalf("tools/call %s: %v", p.Name, err)
			default:
				result := map[string]any{"content": res.Content}
				if res.IsError {
					result["isError"] = true
				}
				answer["result"] = result
			}
		default:
			t.Fatalf("%s is the protocol module's to answer; it is tested there", req.Method)
		}
		cancel()
		b, err := json.Marshal(answer)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = stdout.Write(append(b, '\n'))
	}
	_ = cl.Close()
	select {
	case code := <-done:
		return code
	case <-time.After(10 * time.Second):
		t.Fatal("the mcp subcommand did not end after the client closed")
	}
	return -1
}
