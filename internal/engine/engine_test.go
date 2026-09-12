package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/encoding/japanese"

	"github.com/nlink-jp/web-fetch/internal/cache"
	"github.com/nlink-jp/web-fetch/internal/config"
	"github.com/nlink-jp/web-fetch/internal/fetch"
	"github.com/nlink-jp/web-fetch/internal/toolerr"
)

// fakeFetcher answers from a table and counts calls.
type fakeFetcher struct {
	pages map[string]*fetch.Response
	fail  map[string]error
	calls []string
}

func (f *fakeFetcher) Fetch(_ context.Context, rawURL string) (*fetch.Response, error) {
	f.calls = append(f.calls, rawURL)
	if err, ok := f.fail[rawURL]; ok {
		return nil, err
	}
	p, ok := f.pages[rawURL]
	if !ok {
		return nil, toolerr.Newf(toolerr.CodeDNSFailed, "no page %s", rawURL)
	}
	cp := *p
	cp.URL = rawURL
	if cp.FinalURL == "" {
		cp.FinalURL = rawURL
	}
	return &cp, nil
}

const htmlPage = `<html><head><title>Page</title></head><body><main><h1>Heading</h1><p>` +
	`The quick brown fox jumps over the lazy dog. </p><p><a href="/next">next</a></p></main></body></html>`

func newFetcher() *fakeFetcher {
	sjis, _ := japanese.ShiftJIS.NewEncoder().Bytes([]byte("<html><body><p>日本語の本文</p></body></html>"))
	return &fakeFetcher{
		pages: map[string]*fetch.Response{
			"https://example.org/page":  {Status: 200, ContentType: "text/html; charset=utf-8", Kind: fetch.KindHTML, Body: []byte(htmlPage), Elapsed: 42 * time.Millisecond},
			"https://example.org/plain": {Status: 200, ContentType: "text/plain", Kind: fetch.KindText, Body: []byte("just\n\ntext")},
			"https://example.org/sjis":  {Status: 200, ContentType: "text/html; charset=Shift_JIS", Kind: fetch.KindHTML, Body: sjis},
			"https://example.org/bad":   {Status: 200, ContentType: "text/html; charset=x-klingon", Kind: fetch.KindHTML, Body: []byte("x")},
			"https://example.org/redir": {Status: 200, FinalURL: "https://example.org/final", ContentType: "text/html", Kind: fetch.KindHTML, Body: []byte(htmlPage), Redirects: []string{"https://example.org/redir"}},
		},
	}
}

func newEngine(t *testing.T, f Fetcher, c *cache.Cache[*Document]) *Engine {
	t.Helper()
	cfg := config.Defaults()
	e := New(cfg, f, c)
	e.now = func() time.Time { return time.Date(2026, 9, 12, 7, 28, 41, 0, time.UTC) }
	return e
}

func TestMarkdownResultShape(t *testing.T) {
	e := newEngine(t, newFetcher(), nil)
	res, err := e.Fetch(context.Background(), Request{URL: "https://example.org/page#frag"})
	if err != nil {
		t.Fatal(err)
	}
	want := "# Heading\n\nThe quick brown fox jumps over the lazy dog.\n\n[next](https://example.org/next)"
	if res.Text != want {
		t.Errorf("text:\n%s", res.Text)
	}
	if res.Title != "Page" || res.Format != "markdown" || res.Charset != "utf-8" || res.CharsetSource != "header" || res.Status != 200 {
		t.Errorf("%+v", res)
	}
	if res.URL != "https://example.org/page" || res.FinalURL != "https://example.org/page" {
		t.Errorf("urls: %q %q (fragment must be gone)", res.URL, res.FinalURL)
	}
	if res.TotalChars != len([]rune(want)) || res.ReturnedChars != res.TotalChars || res.Truncated || res.NextOffset != 0 {
		t.Errorf("counts: %+v", res)
	}
	if !strings.HasPrefix(res.DocID, "sha256:") || res.Meta.ElapsedMS != 42 || res.Meta.Bytes != len(htmlPage) || res.Meta.Cache != "" {
		t.Errorf("doc_id/meta: %q %+v", res.DocID, res.Meta)
	}
	if !res.FetchedAt.Equal(time.Date(2026, 9, 12, 7, 28, 41, 0, time.UTC)) {
		t.Errorf("fetched_at: %v", res.FetchedAt)
	}
	b, _ := json.Marshal(res)
	for _, absent := range []string{`"truncated"`, `"next_offset"`, `"redirects"`, `"cache"`} {
		if strings.Contains(string(b), absent) {
			t.Errorf("%s must be omitted when not applicable: %s", absent, b)
		}
	}
}

func TestPagingReachesEveryRune(t *testing.T) {
	e := newEngine(t, newFetcher(), nil)
	full, _ := e.Fetch(context.Background(), Request{URL: "https://example.org/page"})
	var pages []string
	offset := 0
	for {
		res, err := e.Fetch(context.Background(), Request{URL: "https://example.org/page", MaxChars: 1000, Offset: offset})
		if err != nil {
			t.Fatal(err)
		}
		pages = append(pages, res.Text)
		if res.DocID != full.DocID {
			t.Error("doc_id must be stable across page turns")
		}
		if !res.Truncated {
			break
		}
		if res.NextOffset != offset+res.ReturnedChars {
			t.Errorf("next_offset %d != offset %d + returned %d", res.NextOffset, offset, res.ReturnedChars)
		}
		offset = res.NextOffset
	}
	if strings.Join(pages, "") != full.Text {
		t.Errorf("concatenated pages differ from the whole text")
	}
	// The minimum max_chars is 1,000, so a short text is one page; force a
	// cut by asking from the middle.
	mid, _ := e.Fetch(context.Background(), Request{URL: "https://example.org/page", MaxChars: 1000, Offset: 10})
	if mid.Offset != 10 || mid.ReturnedChars != full.TotalChars-10 || mid.Truncated {
		t.Errorf("offset page: %+v", mid)
	}
	past, err := e.Fetch(context.Background(), Request{URL: "https://example.org/page", Offset: full.TotalChars + 5})
	if err != nil || past.ReturnedChars != 0 || past.Text != "" || past.Truncated || past.TotalChars != full.TotalChars {
		t.Errorf("offset past the end is an empty page: %+v %v", past, err)
	}
}

func TestCutIsReported(t *testing.T) {
	long := &fakeFetcher{pages: map[string]*fetch.Response{
		"https://example.org/long": {Status: 200, ContentType: "text/plain", Kind: fetch.KindText, Body: []byte(strings.Repeat("あ", 2500))},
	}}
	e := newEngine(t, long, nil)
	res, err := e.Fetch(context.Background(), Request{URL: "https://example.org/long", MaxChars: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Truncated || res.NextOffset != 1000 || res.ReturnedChars != 1000 || res.TotalChars != 2500 || len([]rune(res.Text)) != 1000 {
		t.Errorf("%+v", res)
	}
}

func TestNonHTMLIsRawAndRawIsVerbatim(t *testing.T) {
	e := newEngine(t, newFetcher(), nil)
	res, err := e.Fetch(context.Background(), Request{URL: "https://example.org/plain", Format: "markdown"})
	if err != nil || res.Format != "raw" || res.Title != "" || res.Text != "just\n\ntext" {
		t.Errorf("text/plain: %+v %v", res, err)
	}
	res, err = e.Fetch(context.Background(), Request{URL: "https://example.org/page", Format: "raw"})
	if err != nil || res.Format != "raw" || res.Title != "" || res.Text != htmlPage {
		t.Errorf("raw html: %+v %v", res, err)
	}
	res, err = e.Fetch(context.Background(), Request{URL: "https://example.org/page", Format: "text"})
	if err != nil || res.Format != "text" || strings.Contains(res.Text, "#") || !strings.HasPrefix(res.Text, "Heading") {
		t.Errorf("text: %+v %v", res, err)
	}
}

func TestLegacyCharsetAndUnsupportedLabel(t *testing.T) {
	f := newFetcher()
	e := newEngine(t, f, cache.New[*Document](time.Minute, 4))
	res, err := e.Fetch(context.Background(), Request{URL: "https://example.org/sjis"})
	if err != nil || res.Charset != "shift_jis" || res.CharsetSource != "header" || !strings.Contains(res.Text, "日本語の本文") {
		t.Errorf("sjis: %+v %v", res, err)
	}
	_, err = e.Fetch(context.Background(), Request{URL: "https://example.org/bad"})
	if toolerr.Code(err) != toolerr.CodeCharsetUnsupported {
		t.Errorf("unsupported label: %v", err)
	}
	_, _ = e.Fetch(context.Background(), Request{URL: "https://example.org/bad"})
	if n := strings.Count(strings.Join(f.calls, " "), "/bad"); n != 2 {
		t.Errorf("an error must not be cached: %d fetches", n)
	}
}

func TestArgumentValidation(t *testing.T) {
	e := newEngine(t, newFetcher(), nil)
	cases := map[string]Request{
		"format":    {URL: "https://example.org/page", Format: "html"},
		"max_chars": {URL: "https://example.org/page", MaxChars: 10},
		"offset":    {URL: "https://example.org/page", Offset: -1},
	}
	for arg, req := range cases {
		_, err := e.Fetch(context.Background(), req)
		if toolerr.Code(err) != toolerr.CodeInvalidArgument || toolerr.Detail(err, "argument") != arg || toolerr.Detail(err, "allowed") == nil {
			t.Errorf("%s: %v", arg, err)
		}
	}
	if _, err := e.Fetch(context.Background(), Request{URL: "ftp://x/"}); toolerr.Code(err) != toolerr.CodeSchemeNotAllowed {
		t.Errorf("url validation runs before any fetch: %v", err)
	}
}

func TestCacheHitFreshAndKey(t *testing.T) {
	f := newFetcher()
	e := newEngine(t, f, cache.New[*Document](time.Minute, 4))
	first, _ := e.Fetch(context.Background(), Request{URL: "https://example.org/page#a"})
	second, _ := e.Fetch(context.Background(), Request{URL: "https://example.org/page#b", Format: "text"})
	if first.Meta.Cache != "miss" || second.Meta.Cache != "hit" || len(f.calls) != 1 {
		t.Errorf("fragment-stripped key must hit: %s %s calls=%v", first.Meta.Cache, second.Meta.Cache, f.calls)
	}
	if second.Format != "text" || strings.Contains(second.Text, "#") {
		t.Error("extraction runs per call; the format is not part of the key")
	}
	if !second.FetchedAt.Equal(first.FetchedAt) {
		t.Error("a hit reports the original fetch time")
	}
	third, _ := e.Fetch(context.Background(), Request{URL: "https://example.org/page", Fresh: true})
	if third.Meta.Cache != "miss" || len(f.calls) != 2 {
		t.Errorf("fresh must refetch: %s calls=%v", third.Meta.Cache, f.calls)
	}
	disabled := newEngine(t, newFetcher(), cache.New[*Document](0, 4))
	res, _ := disabled.Fetch(context.Background(), Request{URL: "https://example.org/page"})
	if res.Meta.Cache != "" {
		t.Errorf("a disabled cache reports nothing: %q", res.Meta.Cache)
	}
}

func TestRedirectsPassThrough(t *testing.T) {
	e := newEngine(t, newFetcher(), nil)
	res, err := e.Fetch(context.Background(), Request{URL: "https://example.org/redir"})
	if err != nil || res.FinalURL != "https://example.org/final" || len(res.Redirects) != 1 {
		t.Errorf("%+v %v", res, err)
	}
	if !strings.Contains(res.Text, "https://example.org/next") {
		t.Errorf("links resolve against the final URL: %s", res.Text)
	}
}

func TestNewClientAndCacheFromConfig(t *testing.T) {
	cfg := config.Defaults()
	if c := NewClient(cfg, "v1"); c == nil {
		t.Fatal("client")
	}
	if c := NewCache(cfg); !c.Enabled() {
		t.Error("default cache is enabled")
	}
	cfg.Cache.TTL = 0
	if c := NewCache(cfg); c.Enabled() {
		t.Error("ttl 0 disables")
	}
}
