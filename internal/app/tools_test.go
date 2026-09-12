package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nlink-jp/web-fetch/internal/config"
	"github.com/nlink-jp/web-fetch/internal/mcp"
	"github.com/nlink-jp/web-fetch/internal/toolerr"
)

// The manual meta-tests, applied to the tool set the server actually
// registers (the mcp package's own tests see get_usage alone).

func TestManualDocumentsTheFetchTool(t *testing.T) {
	doc := mcp.UsageDoc()
	for _, tool := range tools(config.Defaults(), "test") {
		if !strings.Contains(doc, "`"+tool.Name+"`") {
			t.Errorf("usage.md never mentions %q", tool.Name)
		}
		props := tool.InputSchema["properties"].(map[string]any)
		for arg := range props {
			if !strings.Contains(doc, "`"+arg+"`") {
				t.Errorf("usage.md never mentions %s's argument %q", tool.Name, arg)
			}
		}
		for _, key := range []string{"readOnlyHint", "openWorldHint"} {
			if _, ok := tool.Annotations[key]; !ok {
				t.Errorf("%s lacks %s", tool.Name, key)
			}
		}
	}
}

// The description is the trigger and the contract (ADR-0002 §4): it must say
// when to call and how paging works, and must not carry defensive prose.
func TestFetchDescriptionCarriesTriggerAndContract(t *testing.T) {
	desc := fetchTool(nil).Description
	for _, word := range []string{"URL", "offset", "truncated", "next_offset", "doc_id", "this machine", "investigation"} {
		if !strings.Contains(desc, word) {
			t.Errorf("description lacks %q", word)
		}
	}
	for _, prose := range []string{"never follow", "not instructions", "ignore any"} {
		if strings.Contains(strings.ToLower(desc), prose) {
			t.Errorf("description carries defensive prose %q", prose)
		}
	}
	if toolerr.CodeInvalidArgument != mcp.CodeInvalidArgument {
		t.Error("the MCP layer and the tool packages must spell invalid_argument the same way")
	}
}

// A local server reached through the real guard: allow_private in a
// default-search-path config opens loopback, which is the one way a test can
// run the whole path without a fixture resolver.
func localServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/page", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<html><head><title>Local</title></head><body><main><h1>Hi</h1><p>`+strings.Repeat("word ", 400)+`</p></main></body></html>`)
	})
	mux.HandleFunc("/json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func allowPrivateConfig(t *testing.T, extra string) {
	t.Helper()
	path := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "web-fetch", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[network]\nallow_private = true\n"+extra), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestMCPFetchRoundTrip(t *testing.T) {
	isolate(t)
	allowPrivateConfig(t, "")
	srv := localServer(t)
	session := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"fetch","arguments":{"url":"` + srv.URL + `/page","max_chars":1000}}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"fetch","arguments":{"url":"` + srv.URL + `/page","max_chars":1000,"offset":1000}}}` + "\n" +
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"fetch","arguments":{"url":"http://127.0.0.1:1/","typo":true}}}` + "\n"
	var out, errb strings.Builder
	code := run([]string{"mcp"}, "v9", strings.NewReader(session), &out, &errb)
	if code != exitOK || errb.Len() != 0 {
		t.Fatalf("code=%d stderr=%q", code, errb.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 replies, got %d: %s", len(lines), out.String())
	}
	first := toolResult(t, lines[0])
	if first["title"] != "Local" || first["truncated"] != true || first["meta"].(map[string]any)["cache"] != "miss" {
		t.Errorf("first page: %v", first)
	}
	second := toolResult(t, lines[1])
	if second["meta"].(map[string]any)["cache"] != "hit" || second["offset"].(float64) != 1000 || second["doc_id"] != first["doc_id"] {
		t.Errorf("second page: %v", second)
	}
	var third map[string]any
	if err := json.Unmarshal([]byte(lines[2]), &third); err != nil {
		t.Fatal(err)
	}
	r := third["result"].(map[string]any)
	if r["isError"] != true || !strings.Contains(r["content"].([]any)[0].(map[string]any)["text"].(string), `"invalid_argument"`) {
		t.Errorf("an unknown argument is invalid_argument: %v", r)
	}
}

func toolResult(t *testing.T, line string) map[string]any {
	t.Helper()
	var resp map[string]any
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatal(err)
	}
	r := resp["result"].(map[string]any)
	if r["isError"] == true {
		t.Fatalf("unexpected tool error: %v", r)
	}
	text := r["content"].([]any)[0].(map[string]any)["text"].(string)
	var payload map[string]any
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("payload is not JSON: %v", err)
	}
	return payload
}

func TestFetchCommandTextJSONAndExitCodes(t *testing.T) {
	isolate(t)
	allowPrivateConfig(t, "")
	srv := localServer(t)
	var out, errb strings.Builder
	code := run([]string{"--json", srv.URL + "/page", "--max-chars", "1000"}, "v9", strings.NewReader(""), &out, &errb)
	if code != exitOK || errb.Len() != 0 {
		t.Fatalf("json: code=%d stderr=%q", code, errb.String())
	}
	var res map[string]any
	if err := json.Unmarshal([]byte(out.String()), &res); err != nil {
		t.Fatal(err)
	}
	if res["title"] != "Local" || res["truncated"] != true || res["next_offset"].(float64) != 1000 {
		t.Errorf("json result: %v", res)
	}
	if _, hasCache := res["meta"].(map[string]any)["cache"]; hasCache {
		t.Error("the CLI has no cache and must not report one")
	}

	out.Reset()
	code = run([]string{srv.URL + "/page", "--max-chars", "1000", "--format", "text"}, "v9", strings.NewReader(""), &out, &errb)
	text := out.String()
	if code != exitOK || !strings.HasPrefix(text, "Local\n"+srv.URL+"/page\n\nHi\n") || !strings.Contains(text, "[truncated: 0–1000 of ") || !strings.Contains(text, "--offset 1000]") {
		t.Errorf("text output: code=%d %q", code, text)
	}

	out.Reset()
	code = run([]string{srv.URL + "/json"}, "v9", strings.NewReader(""), &out, &errb)
	if code != exitOK || !strings.Contains(out.String(), `{"ok":true}`) {
		t.Errorf("json body passes through raw: code=%d %q", code, out.String())
	}

	cases := []struct {
		args []string
		code int
		errc string
	}{
		{[]string{"ftp://files.test/x"}, exitError, toolerr.CodeSchemeNotAllowed},
		{[]string{"--max-chars", "10", srv.URL + "/page"}, exitError, toolerr.CodeInvalidArgument},
		{[]string{"http://u:p@" + strings.TrimPrefix(srv.URL, "http://") + "/page"}, exitError, toolerr.CodeInvalidURL},
		{[]string{srv.URL + "/missing"}, exitFetch, toolerr.CodeHTTPStatus},
	}
	for _, c := range cases {
		errb.Reset()
		code := run(c.args, "v9", strings.NewReader(""), &out, &errb)
		var e map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(errb.String())), &e); err != nil {
			t.Errorf("%v: stderr is not one JSON object: %q", c.args, errb.String())
			continue
		}
		if code != c.code || e["code"] != c.errc {
			t.Errorf("%v: code=%d err=%v, want %d %s", c.args, code, e["code"], c.code, c.errc)
		}
	}
}

func TestFetchRefusesLoopbackWithoutAllowPrivate(t *testing.T) {
	isolate(t)
	var out, errb strings.Builder
	code := run([]string{"http://127.0.0.1:1/"}, "v9", strings.NewReader(""), &out, &errb)
	if code != exitFetch || !strings.Contains(errb.String(), toolerr.CodeAddressNotAllowed) {
		t.Errorf("code=%d stderr=%q", code, errb.String())
	}
	code = run([]string{"--timeout", "abc", "http://example.test/"}, "v9", strings.NewReader(""), &out, &errb)
	if code != exitError {
		t.Errorf("bad --timeout: %d", code)
	}
	code = run([]string{"http://a.test/", "http://b.test/"}, "v9", strings.NewReader(""), &out, &errb)
	if code != exitError {
		t.Errorf("two URLs: %d", code)
	}
}
