package fetch

import (
	"compress/gzip"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nlink-jp/web-fetch/internal/guard"
	"github.com/nlink-jp/web-fetch/internal/toolerr"
)

// The test rig: names resolve to public fixture addresses (TEST-NET-3, not in
// the guard's table) so the table stays live, and the connect function points
// every vetted literal at the httptest listener while recording what it was
// handed. "evil.test" resolves to a private address.
type rig struct {
	srv     *httptest.Server
	dialed  []string
	headers []http.Header
	mu      sync.Mutex
}

type resolver map[string][]netip.Addr

func (r resolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	if a, ok := r[host]; ok {
		return a, nil
	}
	return nil, fmt.Errorf("no such host %q", host)
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{}
	mux := http.NewServeMux()
	record := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, req *http.Request) {
			r.mu.Lock()
			r.headers = append(r.headers, req.Header.Clone())
			r.mu.Unlock()
			h(w, req)
		}
	}
	mux.HandleFunc("/html", record(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, "<html><body><main><p>hello</p></main></body></html>")
	}))
	mux.HandleFunc("/plain", record(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "just text")
	}))
	mux.HandleFunc("/json", record(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"a":1}`)
	}))
	mux.HandleFunc("/pdf", record(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		fmt.Fprint(w, "%PDF-1.4 ...")
	}))
	mux.HandleFunc("/nohead", record(func(w http.ResponseWriter, _ *http.Request) {
		w.Header()["Content-Type"] = nil // suppress Go's own sniffing header
		fmt.Fprint(w, "<!DOCTYPE html><html><body>sniffed</body></html>")
	}))
	mux.HandleFunc("/binary", record(func(w http.ResponseWriter, _ *http.Request) {
		w.Header()["Content-Type"] = nil
		_, _ = w.Write([]byte{0x00, 0x01, 0x02, 0xff, 0xfe, 0x00, 0x00, 0x10})
	}))
	mux.HandleFunc("/redir1", record(func(w http.ResponseWriter, req *http.Request) { http.Redirect(w, req, "/redir2", http.StatusFound) }))
	mux.HandleFunc("/redir2", record(func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, "/html", http.StatusMovedPermanently)
	}))
	mux.HandleFunc("/loop", record(func(w http.ResponseWriter, req *http.Request) { http.Redirect(w, req, "/loop", http.StatusFound) }))
	mux.HandleFunc("/user", record(func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, "http://u:p@"+req.Host+"/html", http.StatusFound)
	}))
	mux.HandleFunc("/ftp", record(func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, "ftp://files.test/x", http.StatusFound)
	}))
	mux.HandleFunc("/evil", record(func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, "http://evil.test/", http.StatusFound)
	}))
	mux.HandleFunc("/nolocation", record(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusFound) }))
	mux.HandleFunc("/404", record(func(w http.ResponseWriter, _ *http.Request) { http.NotFound(w, nil) }))
	mux.HandleFunc("/gzipbomb", record(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		zeros := make([]byte, 64*1024)
		for i := 0; i < 48; i++ { // 3 MiB of zeros, a few KB on the wire
			_, _ = gz.Write(zeros)
		}
		_ = gz.Close()
	}))
	mux.HandleFunc("/br", record(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Content-Encoding", "br")
		_, _ = w.Write([]byte{0x0b, 0x02, 0x80})
	}))
	mux.HandleFunc("/big", record(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		chunk := strings.Repeat("x", 64*1024)
		for i := 0; i < 40; i++ { // 2.5 MiB, chunked, no Content-Length
			fmt.Fprint(w, chunk)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
	mux.HandleFunc("/slowheaders", record(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(1500 * time.Millisecond)
		fmt.Fprint(w, "late")
	}))
	mux.HandleFunc("/stall", record(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "partial ")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(1500 * time.Millisecond)
	}))
	// Pre-release review fixtures.
	mux.HandleFunc("/cross", record(func(w http.ResponseWriter, req *http.Request) {
		_, port, _ := net.SplitHostPort(req.Host)
		http.Redirect(w, req, "http://ok2.test:"+port+"/echo", http.StatusFound)
	}))
	mux.HandleFunc("/echo", record(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintf(w, "UA=%s;AL=%s;REF=%s;", req.Header.Get("User-Agent"), req.Header.Get("Accept-Language"), req.Header.Get("Referer"))
	}))
	mux.HandleFunc("/schemerel", record(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Location", "//u:p@"+req.Host+"/html")
		w.WriteHeader(http.StatusFound)
	}))
	mux.HandleFunc("/identity", record(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Encoding", "identity")
		fmt.Fprint(w, "plain identity")
	}))
	mux.HandleFunc("/xgzip", record(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Content-Encoding", "x-gzip")
		_, _ = w.Write([]byte{0x1f, 0x8b, 0x08, 0x00})
	}))
	mux.HandleFunc("/svg", record(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		fmt.Fprint(w, `<svg xmlns="http://www.w3.org/2000/svg"/>`)
	}))
	r.srv = httptest.NewServer(mux)
	t.Cleanup(r.srv.Close)
	return r
}

func (r *rig) connect(ctx context.Context, network, address string) (net.Conn, error) {
	r.mu.Lock()
	r.dialed = append(r.dialed, address)
	r.mu.Unlock()
	return (&net.Dialer{}).DialContext(ctx, network, r.srv.Listener.Addr().String())
}

func (r *rig) client(o Options) *Client {
	res := resolver{
		"ok.test":   {netip.MustParseAddr("203.0.113.10")},
		"ok2.test":  {netip.MustParseAddr("203.0.113.11")},
		"evil.test": {netip.MustParseAddr("10.0.0.1")},
	}
	o.Dialer = &guard.Dialer{Resolver: res, Connect: r.connect}
	if o.Timeout == 0 {
		o.Timeout = 5 * time.Second
	}
	if o.MaxRedirects == 0 {
		o.MaxRedirects = 5
	}
	if o.MaxBytes == 0 {
		o.MaxBytes = 1 << 20
	}
	if o.UserAgent == "" {
		o.UserAgent = "web-fetch/test (+https://github.com/nlink-jp/web-fetch)"
	}
	return New(o)
}

func (r *rig) url(path string) string {
	_, port, _ := net.SplitHostPort(r.srv.Listener.Addr().String())
	return "http://ok.test:" + port + path
}

func TestFetchHTMLThroughTheVettedLiteral(t *testing.T) {
	r := newRig(t)
	c := r.client(Options{AcceptLanguage: "ja, en;q=0.8"})
	resp, err := c.Fetch(context.Background(), r.url("/html#frag"))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Kind != KindHTML || resp.MediaType != "text/html" || !strings.Contains(string(resp.Body), "hello") || resp.Status != 200 {
		t.Errorf("%+v", resp)
	}
	if strings.Contains(resp.URL, "#") || resp.FinalURL != r.url("/html") || resp.Redirects != nil {
		t.Errorf("urls: %q %q %v", resp.URL, resp.FinalURL, resp.Redirects)
	}
	_, port, _ := net.SplitHostPort(r.srv.Listener.Addr().String())
	if len(r.dialed) != 1 || r.dialed[0] != "203.0.113.10:"+port {
		t.Errorf("dialed %v; must be the judged literal", r.dialed)
	}
	h := r.headers[0]
	if !strings.HasPrefix(h.Get("User-Agent"), "web-fetch/") || h.Get("Accept-Language") != "ja, en;q=0.8" || h.Get("Accept") != Accept {
		t.Errorf("headers: %v", h)
	}
	for _, forbidden := range []string{"Cookie", "Authorization"} {
		if h.Get(forbidden) != "" {
			t.Errorf("%s must never be sent", forbidden)
		}
	}
	if resp.Elapsed <= 0 {
		t.Error("elapsed not measured")
	}
}

func TestKindsAndUnsupportedTypes(t *testing.T) {
	r := newRig(t)
	c := r.client(Options{})
	for path, want := range map[string]Kind{"/plain": KindText, "/json": KindText, "/nohead": KindHTML} {
		resp, err := c.Fetch(context.Background(), r.url(path))
		if err != nil || resp.Kind != want {
			t.Errorf("%s: kind=%v err=%v", path, resp, err)
		}
	}
	for path, wantCT := range map[string]string{"/pdf": "application/pdf", "/binary": "application/octet-stream (sniffed)"} {
		_, err := c.Fetch(context.Background(), r.url(path))
		if toolerr.Code(err) != toolerr.CodeUnsupportedContentType || toolerr.Detail(err, "content_type") != wantCT {
			t.Errorf("%s: %v (%v)", path, err, toolerr.Detail(err, "content_type"))
		}
	}
	// The remote header is quoted in the message (it is the site's text);
	// a sniffed type says so outside the quotes.
	_, err := c.Fetch(context.Background(), r.url("/pdf"))
	if msg := err.Error(); !strings.Contains(msg, ` is "application/pdf", which`) {
		t.Errorf("declared type not quoted: %s", msg)
	}
	_, err = c.Fetch(context.Background(), r.url("/binary"))
	if msg := err.Error(); !strings.Contains(msg, `declares no media type and sniffs as "application/octet-stream", which`) {
		t.Errorf("sniffed type: %s", msg)
	}
	for mt, want := range map[string]Kind{"text/html": KindHTML, "application/xhtml+xml": KindHTML, "text/csv": KindText, "application/ld+json": KindText, "application/rss+xml": KindText, "image/png": 0, "application/octet-stream": 0} {
		got, _ := classify(mt)
		if got != want {
			t.Errorf("classify(%s) = %v, want %v", mt, got, want)
		}
	}
	if mediaType("Text/HTML; charset=Shift_JIS", nil) != "text/html" || mediaType("weird header", nil) != "weird header" {
		t.Error("header media type parsing")
	}
}

func TestRedirectsAreRecordedAndInspected(t *testing.T) {
	r := newRig(t)
	c := r.client(Options{})
	resp, err := c.Fetch(context.Background(), r.url("/redir1"))
	if err != nil {
		t.Fatal(err)
	}
	if resp.FinalURL != r.url("/html") || len(resp.Redirects) != 2 || resp.Redirects[0] != r.url("/redir1") || resp.Redirects[1] != r.url("/redir2") {
		t.Errorf("final=%q redirects=%v", resp.FinalURL, resp.Redirects)
	}
	cases := map[string]struct {
		code   string
		detail string
	}{
		"/loop":       {toolerr.CodeTooManyRedirects, "hops"},
		"/user":       {toolerr.CodeInvalidURL, "hop"},
		"/ftp":        {toolerr.CodeSchemeNotAllowed, "hop"},
		"/evil":       {toolerr.CodeAddressNotAllowed, "class"},
		"/nolocation": {toolerr.CodeHTTPStatus, "status"},
		"/404":        {toolerr.CodeHTTPStatus, "final_url"},
	}
	for path, want := range cases {
		_, err := c.Fetch(context.Background(), r.url(path))
		if toolerr.Code(err) != want.code || toolerr.Detail(err, want.detail) == nil {
			t.Errorf("%s: want %s with %s, got %v", path, want.code, want.detail, err)
		}
	}
	if _, err := c.Fetch(context.Background(), r.url("/user")); strings.Contains(err.Error(), ":p@") || strings.Contains(fmt.Sprint(toolerr.Detail(err, "url")), ":p@") {
		t.Errorf("the password must be redacted: %v %v", err, toolerr.Detail(err, "url"))
	}
	zero := r.client(Options{MaxRedirects: -1})
	zero.opts.MaxRedirects = 0
	if _, err := zero.Fetch(context.Background(), r.url("/redir1")); toolerr.Code(err) != toolerr.CodeTooManyRedirects {
		t.Errorf("max_redirects 0 must refuse the first hop: %v", err)
	}
}

func TestBodyCapIsOnTheDecodedStream(t *testing.T) {
	r := newRig(t)
	c := r.client(Options{MaxBytes: 1 << 20})
	_, err := c.Fetch(context.Background(), r.url("/gzipbomb"))
	if toolerr.Code(err) != toolerr.CodeTooLarge || toolerr.Detail(err, "max_bytes") != int64(1<<20) {
		t.Errorf("a few KB of gzip decoding to 3 MiB must be too_large: %v", err)
	}
	if _, err := c.Fetch(context.Background(), r.url("/big")); toolerr.Code(err) != toolerr.CodeTooLarge {
		t.Errorf("chunked body without Content-Length past the cap: %v", err)
	}
	_, err = c.Fetch(context.Background(), r.url("/br"))
	if toolerr.Code(err) != toolerr.CodeUnsupportedContentType || toolerr.Detail(err, "content_encoding") != "br" {
		t.Errorf("an encoding the transport did not ask for: %v", err)
	}
	exact := r.client(Options{MaxBytes: int64(len("just text"))})
	if _, err := exact.Fetch(context.Background(), r.url("/plain")); err != nil {
		t.Errorf("a body exactly at the cap fits: %v", err)
	}
}

func TestTimeoutsCoverHeadersAndBody(t *testing.T) {
	r := newRig(t)
	c := r.client(Options{Timeout: 400 * time.Millisecond})
	for _, path := range []string{"/slowheaders", "/stall"} {
		start := time.Now()
		_, err := c.Fetch(context.Background(), r.url(path))
		if toolerr.Code(err) != toolerr.CodeTimeout {
			t.Errorf("%s: %v", path, err)
		}
		if time.Since(start) > 1200*time.Millisecond {
			t.Errorf("%s: the deadline did not cut the wait (%s)", path, time.Since(start))
		}
	}
}

func TestInputURLValidation(t *testing.T) {
	cases := map[string]string{
		"ftp://files.test/x":         toolerr.CodeSchemeNotAllowed,
		"file:///etc/hosts":          toolerr.CodeSchemeNotAllowed,
		"example.org/path":           toolerr.CodeInvalidURL,
		"http://u:p@example.org/":    toolerr.CodeInvalidURL,
		"http:///nohost":             toolerr.CodeInvalidURL,
		"://bad":                     toolerr.CodeInvalidURL,
		"https://example.org/ok#top": "",
		"HTTPS://EXAMPLE.ORG/":       "",
	}
	for in, want := range cases {
		u, err := ParseURL(in)
		if toolerr.Code(err) != want {
			t.Errorf("ParseURL(%q) = %v, want code %q", in, err, want)
		}
		if err == nil && u.Fragment != "" {
			t.Errorf("fragment must be dropped: %v", u)
		}
	}
	if _, err := ParseURL("http://u:secret@example.org/"); strings.Contains(fmt.Sprint(toolerr.Detail(err, "url")), "secret") {
		t.Error("the password must be redacted in details")
	}
}

func TestProxyEnvironmentIsIgnored(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:9/")
	t.Setenv("http_proxy", "http://127.0.0.1:9/")
	r := newRig(t)
	if _, err := r.client(Options{}).Fetch(context.Background(), r.url("/plain")); err != nil {
		t.Errorf("a proxy variable must not reroute the fetch: %v", err)
	}
}

func TestDNSFailureAndDeniedInput(t *testing.T) {
	r := newRig(t)
	c := r.client(Options{})
	if _, err := c.Fetch(context.Background(), "http://unknown.test/"); toolerr.Code(err) != toolerr.CodeDNSFailed {
		t.Errorf("%v", err)
	}
	if _, err := c.Fetch(context.Background(), "http://evil.test/"); toolerr.Code(err) != toolerr.CodeAddressNotAllowed {
		t.Errorf("%v", err)
	}
	if _, err := c.Fetch(context.Background(), "http://127.0.0.1:1/"); toolerr.Code(err) != toolerr.CodeAddressNotAllowed {
		t.Errorf("%v", err)
	}
	if len(r.dialed) != 0 {
		t.Errorf("nothing may be dialed for a refused address: %v", r.dialed)
	}
}
