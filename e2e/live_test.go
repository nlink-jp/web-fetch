//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nlink-jp/web-fetch/internal/config"
	"github.com/nlink-jp/web-fetch/internal/engine"
	"github.com/nlink-jp/web-fetch/internal/toolerr"
)

// lagentInlineCap is lagent's MCP intake cap in bytes of text block (its
// tools.OutputCap, 2026-09-12): a default response above it is parked in the
// work directory instead of shown inline. The Japanese-page test below is the
// RFP's Phase 1 milestone.
const lagentInlineCap = 20_000

func live(t *testing.T) *engine.Engine {
	t.Helper()
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	return engine.New(cfg, engine.NewClient(cfg, "e2e"), nil)
}

func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return c
}

func TestLiveStablePage(t *testing.T) {
	e := live(t)
	res, err := e.Fetch(ctx(t), engine.Request{URL: "https://example.com/"})
	if err != nil {
		t.Fatal(err)
	}
	// The page's body no longer repeats its title in an <h1> (seen 2026-11-14:
	// one paragraph, 577 bytes), so the title is read from where it still is
	// and the body is held to a phrase of that paragraph.
	if res.Status != 200 || res.Format != "markdown" || res.Title != "Example Domain" ||
		!strings.Contains(res.Text, "documentation examples") {
		t.Errorf("%+v", res)
	}
	t.Logf("MEASURED: example.com bytes=%d elapsed_ms=%d total_chars=%d charset=%s/%s title=%q",
		res.Meta.Bytes, res.Meta.ElapsedMS, res.TotalChars, res.Charset, res.CharsetSource, res.Title)
}

func TestLiveRedirectIsRecorded(t *testing.T) {
	e := live(t)
	res, err := e.Fetch(ctx(t), engine.Request{URL: "http://go.dev/", MaxChars: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.FinalURL, "https://go.dev/") || len(res.Redirects) == 0 {
		t.Errorf("expected an http→https redirect: final=%q redirects=%v", res.FinalURL, res.Redirects)
	}
	t.Logf("MEASURED: go.dev redirects=%v final=%s", res.Redirects, res.FinalURL)
}

func TestLiveDocumentationPageDefaultsPage(t *testing.T) {
	e := live(t)
	res, err := e.Fetch(ctx(t), engine.Request{URL: "https://go.dev/doc/go1.26"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Title, "Go 1.26") || !strings.Contains(res.Text, "# Go 1.26 Release Notes") {
		t.Errorf("title=%q text starts %q", res.Title, res.Text[:min(len(res.Text), 80)])
	}
	turns := (res.TotalChars + 5999) / 6000
	t.Logf("MEASURED: go1.26 total_chars=%d page_turns_at_6000=%d truncated=%v bytes=%d elapsed_ms=%d",
		res.TotalChars, turns, res.Truncated, res.Meta.Bytes, res.Meta.ElapsedMS)
}

// The milestone: a Japanese page's default response, JSON envelope included,
// stays under lagent's inline cap.
func TestLiveJapanesePageStaysInline(t *testing.T) {
	url := os.Getenv("WEB_FETCH_E2E_JA_URL")
	if url == "" {
		url = "https://ja.wikipedia.org/wiki/%E6%97%A5%E6%9C%AC%E8%AA%9E"
	}
	e := live(t)
	res, err := e.Fetch(ctx(t), engine.Request{URL: url})
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("MEASURED: ja page returned_chars=%d total_chars=%d json_bytes=%d text_bytes=%d charset=%s/%s",
		res.ReturnedChars, res.TotalChars, len(b), len(res.Text), res.Charset, res.CharsetSource)
	if len(b) >= lagentInlineCap {
		t.Errorf("the default response is %d bytes; lagent parks anything at or above %d", len(b), lagentInlineCap)
	}
}

func TestLiveLoopbackIsRefusedWithoutANetworkCall(t *testing.T) {
	e := live(t)
	_, err := e.Fetch(ctx(t), engine.Request{URL: "http://127.0.0.1:1/"})
	if toolerr.Code(err) != toolerr.CodeAddressNotAllowed {
		t.Errorf("%v (allow_private set in the operator's config would explain a different code)", err)
	}
}

// A public name that answers with a loopback address is the rebinding class:
// the judgment happens on the answer, not on the spelling.
func TestLivePublicNameResolvingToLoopbackIsRefused(t *testing.T) {
	e := live(t)
	_, err := e.Fetch(ctx(t), engine.Request{URL: "http://127.0.0.1.nip.io/"})
	switch toolerr.Code(err) {
	case toolerr.CodeAddressNotAllowed:
		t.Logf("MEASURED: 127.0.0.1.nip.io refused as %v via %v", toolerr.Detail(err, "class"), toolerr.Detail(err, "address"))
	case toolerr.CodeDNSFailed:
		t.Skipf("the nip.io fixture did not resolve here (%v); name another public loopback name in AGENTS.md", err)
	default:
		t.Errorf("%v", err)
	}
}

func TestLiveShiftJISPageOptional(t *testing.T) {
	url := os.Getenv("WEB_FETCH_E2E_SJIS_URL")
	if url == "" {
		t.Skip("set WEB_FETCH_E2E_SJIS_URL to a public Shift_JIS page to run this")
	}
	e := live(t)
	res, err := e.Fetch(ctx(t), engine.Request{URL: url, MaxChars: 1000})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("MEASURED: sjis page charset=%s/%s title=%q", res.Charset, res.CharsetSource, res.Title)
	if res.Charset != "shift_jis" {
		t.Errorf("expected shift_jis, got %s", res.Charset)
	}
}
