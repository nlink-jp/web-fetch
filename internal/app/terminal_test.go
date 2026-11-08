package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// Page text printed to a terminal is made inert (code-audit run-1,
// 2026-11-08): escape sequences, C1 and bidi controls in a page's title or
// text used to reach the operator's terminal as written.

// tty is a writer that reports itself as a character device, the way
// os.Stdout does on a terminal.
type tty struct{ strings.Builder }

func (*tty) Stat() (os.FileInfo, error) { return charDevice{}, nil }

type charDevice struct{}

func (charDevice) Name() string       { return "tty" }
func (charDevice) Size() int64        { return 0 }
func (charDevice) Mode() os.FileMode  { return os.ModeCharDevice | 0o620 }
func (charDevice) ModTime() time.Time { return time.Time{} }
func (charDevice) IsDir() bool        { return false }
func (charDevice) Sys() any           { return nil }

// hostile is the page: an OSC title write and a screen clear in the title,
// a C1 CSI, a bidi override and DEL in the text. Hand-written.
const hostileTitle = "Page\x1b]0;owned\x07\x1b[2J"
const hostileText = "before \u009b31m red \u202e desrever \x7f after"

func hostileServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<html><head><title>%s</title></head><body><main><p>%s</p></main></body></html>`, hostileTitle, hostileText)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// esc is the \uXXXX escape of r, as the encoder and inertJSON write it.
func esc(r rune) string { return fmt.Sprintf(`\u%04x`, r) }

func TestIsTerminalIsTheCharacterDeviceBit(t *testing.T) {
	if isTerminal(&strings.Builder{}) {
		t.Error("a Builder is not a terminal")
	}
	if !isTerminal(&tty{}) {
		t.Error("a character device is")
	}
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if isTerminal(f) {
		t.Error("a regular file is not a terminal")
	}
}

func TestInertReplacesWhatATerminalWouldActOn(t *testing.T) {
	in := "a\tb\nc\x1b[2Jd\x7fe\u0085f\u009bg\u202eh\u2066i\u200ej\x00k"
	want := "a\tb\nc�[2Jd�e�f�g�h�i\u200ej�k"
	if got := inert(in); got != want {
		t.Errorf("inert:\n got %q\nwant %q", got, want)
	}
	if got := inert("plain 日本語 text"); got != "plain 日本語 text" {
		t.Errorf("ordinary text changed: %q", got)
	}
}

func TestInertJSONEscapesWhatTheEncoderLeavesRaw(t *testing.T) {
	in := []byte("{\"t\":\"a\\u001b\u009b\u202e\x7f\u200e日\"}")
	want := `{"t":"a` + esc(0x1b) + esc(0x9b) + esc(0x202e) + esc(0x7f) + "\u200e\u65e5" + `"}`
	got := inertJSON(in)
	if string(got) != want {
		t.Errorf("inertJSON:\n got %q\nwant %q", got, want)
	}
	if !json.Valid(got) {
		t.Error("the rewritten document is not JSON")
	}
	plain := []byte(`{"t":"plain"}`)
	if string(inertJSON(plain)) != string(plain) {
		t.Error("a clean document changed")
	}
}

func TestPageTextIsInertOnATerminalAndVerbatimOnAPipe(t *testing.T) {
	isolate(t)
	allowPrivateConfig(t, "")
	srv := hostileServer(t)

	var term tty
	var errb strings.Builder
	if code := run([]string{srv.URL + "/"}, "v9", strings.NewReader(""), &term, &errb); code != exitOK {
		t.Fatalf("code=%d stderr=%q", code, errb.String())
	}
	out := term.String()
	for _, bad := range []string{"\x1b", "\u009b", "\u202e", "\x7f"} {
		if strings.Contains(out, bad) {
			t.Errorf("terminal output carries %q: %q", bad, out)
		}
	}
	if !strings.HasPrefix(out, "Page�]0;owned��[2J\n") || !strings.Contains(out, "before �31m red � desrever � after") {
		t.Errorf("terminal output: %q", out)
	}

	var pipe strings.Builder
	if code := run([]string{srv.URL + "/"}, "v9", strings.NewReader(""), &pipe, &errb); code != exitOK {
		t.Fatalf("code=%d stderr=%q", code, errb.String())
	}
	if !strings.Contains(pipe.String(), hostileText) || !strings.HasPrefix(pipe.String(), hostileTitle+"\n") {
		t.Errorf("piped output is not verbatim: %q", pipe.String())
	}

	// --json on a terminal: every control rune is a \uXXXX escape, and the
	// document still decodes to the same page.
	var jterm tty
	if code := run([]string{"--json", srv.URL + "/"}, "v9", strings.NewReader(""), &jterm, &errb); code != exitOK {
		t.Fatalf("json: code=%d stderr=%q", code, errb.String())
	}
	jout := jterm.String()
	for _, bad := range []string{"\x1b", "\u009b", "\u202e", "\x7f"} {
		if strings.Contains(jout, bad) {
			t.Errorf("terminal JSON carries %q: %q", bad, jout)
		}
	}
	for _, esc := range []string{esc(0x1b), esc(0x9b), esc(0x202e), esc(0x7f)} {
		if !strings.Contains(jout, esc) {
			t.Errorf("terminal JSON lacks %s: %q", esc, jout)
		}
	}
	var res map[string]any
	if err := json.Unmarshal([]byte(jout), &res); err != nil {
		t.Fatalf("terminal JSON does not parse: %v", err)
	}
	if res["title"] != hostileTitle || !strings.Contains(res["text"].(string), hostileText) {
		t.Errorf("terminal JSON decodes to a changed page: %v", res)
	}

	var jpipe strings.Builder
	if code := run([]string{"--json", srv.URL + "/"}, "v9", strings.NewReader(""), &jpipe, &errb); code != exitOK {
		t.Fatalf("json pipe: code=%d stderr=%q", code, errb.String())
	}
	if !strings.Contains(jpipe.String(), "\u009b") || !strings.Contains(jpipe.String(), "\u202e") {
		t.Errorf("piped JSON is not verbatim: %q", jpipe.String())
	}
}

// stderr is an egress too (code-audit run-2, 2026-11-08): a failed fetch's
// error JSON quotes the remote Content-Type and final URL, and it reached a
// terminal as encoded — C1 and bidi controls raw.
func TestErrorJSONIsInertOnATerminalAndVerbatimOnAPipe(t *testing.T) {
	isolate(t)
	allowPrivateConfig(t, "")
	// A refused media type whose header carries a RIGHT-TO-LEFT OVERRIDE
	// and a C1 CSI: textproto accepts bytes >= 0x80 in a header value.
	const hostileType = "application/pdf\u202e fdp/noitacilppa\u009b"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", hostileType)
		fmt.Fprint(w, "%PDF-1.4")
	}))
	t.Cleanup(srv.Close)

	var out strings.Builder
	var term tty
	if code := run([]string{srv.URL + "/"}, "v9", strings.NewReader(""), &out, &term); code != exitFetch {
		t.Fatalf("code=%d stderr=%q", code, term.String())
	}
	errLine := term.String()
	for _, bad := range []string{"\u202e", "\u009b"} {
		if strings.Contains(errLine, bad) {
			t.Errorf("terminal stderr carries %q: %q", bad, errLine)
		}
	}
	for _, e := range []string{esc(0x202e), esc(0x9b)} {
		if !strings.Contains(errLine, e) {
			t.Errorf("terminal stderr lacks %s: %q", e, errLine)
		}
	}
	var e map[string]any
	if err := json.Unmarshal([]byte(errLine), &e); err != nil || e["code"] != "unsupported_content_type" {
		t.Errorf("stderr is not the error JSON: %v %q", err, errLine)
	}
	if d, _ := e["details"].(map[string]any); d["content_type"] != hostileType {
		t.Errorf("the escapes must decode back to the header: %v", e["details"])
	}

	var pipe strings.Builder
	if code := run([]string{srv.URL + "/"}, "v9", strings.NewReader(""), &out, &pipe); code != exitFetch {
		t.Fatalf("code=%d stderr=%q", code, pipe.String())
	}
	if !strings.Contains(pipe.String(), hostileType) {
		t.Errorf("piped stderr is not verbatim: %q", pipe.String())
	}
}

// web-fetch's own one-line messages quote errors, and an error may quote
// remote text, so fail takes the same gate.
func TestFailLineIsInertOnATerminal(t *testing.T) {
	var term tty
	if code := fail(&term, exitError, "config %s: %v", "x.toml", "\u202ereversed\x1b[2J"); code != exitError {
		t.Fatalf("code=%d", code)
	}
	if term.String() != "web-fetch: config x.toml: �reversed�[2J\n" {
		t.Errorf("terminal: %q", term.String())
	}
	var pipe strings.Builder
	fail(&pipe, exitError, "%s", "\u202eraw")
	if pipe.String() != "web-fetch: \u202eraw\n" {
		t.Errorf("pipe: %q", pipe.String())
	}
}
