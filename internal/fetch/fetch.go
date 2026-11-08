// Package fetch is web-fetch's HTTP client: one GET of one URL through the
// guard's dialer, with the redirect inspection, the decoded-stream byte cap and
// the Content-Type judgment the RFP specifies. It returns bytes and facts; the
// extract and engine packages turn them into text.
//
// What the client never does (ADR-0001): send cookies, Authorization or
// caller-supplied headers; honour proxy environment variables; dial by name;
// follow a redirect whose Location carries userinfo or a non-http(s) scheme.
package fetch

import (
	"context"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/nlink-jp/web-fetch/internal/guard"
	"github.com/nlink-jp/web-fetch/internal/toolerr"
)

// Kind is what the body is, for the layer that renders it.
type Kind int

const (
	// KindHTML is text/html or application/xhtml+xml: extracted.
	KindHTML Kind = iota + 1
	// KindText is text/*, JSON or XML: returned as is.
	KindText
)

// Options configure a Client. Every field is required except AcceptLanguage.
type Options struct {
	Dialer         *guard.Dialer
	Timeout        time.Duration // from request start (DNS included) through the end of the body
	MaxRedirects   int
	MaxBytes       int64 // applied to the decoded stream
	UserAgent      string
	AcceptLanguage string // "" sends no header
}

// Response is one fetched document.
type Response struct {
	URL         string // as requested, fragment removed
	FinalURL    string // after redirects
	Status      int    // 2xx
	ContentType string // the Content-Type header as received; "" when the server sent none
	MediaType   string // the media type judged: from the header, else sniffed
	Kind        Kind
	Body        []byte   // at most Options.MaxBytes, transport decoding already applied
	Redirects   []string // URLs that answered 3xx, in order; nil when none
	Elapsed     time.Duration
}

// Client fetches. Construct with New.
type Client struct {
	opts      Options
	transport *http.Transport
}

// Accept is what the client asks for: it reads HTML, text and JSON, and says
// so, so a server that negotiates sends text over an application format.
const Accept = "text/html, application/xhtml+xml, text/*;q=0.9, application/json;q=0.8, application/xml;q=0.8, */*;q=0.1"

// New builds a client. The transport ignores proxy environment variables, has
// no cookie jar, and dials only through the guard.
func New(o Options) *Client {
	return &Client{
		opts: o,
		transport: &http.Transport{
			Proxy:             nil, // never a proxy: it would blind the address judgment
			DialContext:       o.Dialer.DialContext,
			ForceAttemptHTTP2: true,
			// No TLSHandshakeTimeout of its own: the client's deadline bounds
			// the handshake too, and a second limit would produce a "timeout"
			// naming a deadline that had not passed.
			MaxIdleConns:    4,
			IdleConnTimeout: 30 * time.Second,
		},
	}
}

// Fetch performs one GET.
func (c *Client) Fetch(ctx context.Context, rawURL string) (*Response, error) {
	start := time.Now()
	u, err := ParseURL(rawURL)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, toolerr.Newf(toolerr.CodeInvalidURL, "the URL cannot be requested: %v", err).With("url", rawURL)
	}
	req.Header.Set("User-Agent", c.opts.UserAgent)
	req.Header.Set("Accept", Accept)
	if c.opts.AcceptLanguage != "" {
		req.Header.Set("Accept-Language", c.opts.AcceptLanguage)
	}

	var hops []string
	client := &http.Client{
		Transport: c.transport,
		Timeout:   c.opts.Timeout,
		Jar:       nil,
		CheckRedirect: func(next *http.Request, via []*http.Request) error {
			return c.checkRedirect(next, via, &hops)
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, c.transportError(err, u.Host)
	}
	defer func() { _ = resp.Body.Close() }()

	out := &Response{
		URL:         u.String(),
		FinalURL:    resp.Request.URL.String(),
		Status:      resp.StatusCode,
		ContentType: resp.Header.Get("Content-Type"),
		Redirects:   hops,
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, toolerr.Newf(toolerr.CodeHTTPStatus, "%s answered %d %s", out.FinalURL, resp.StatusCode, http.StatusText(resp.StatusCode)).
			With("status", resp.StatusCode).With("final_url", out.FinalURL)
	}
	// The transport decodes only the gzip it asked for itself and removes
	// the header when it did; anything still declared is an encoding this
	// tool does not read.
	if ce := resp.Header.Get("Content-Encoding"); ce != "" && !strings.EqualFold(ce, "identity") {
		return nil, toolerr.Newf(toolerr.CodeUnsupportedContentType, "%s is encoded as %q, which web-fetch does not decode", out.FinalURL, ce).
			With("content_type", out.ContentType).With("content_encoding", ce)
	}

	// The cap is on the decoded stream, one byte past the limit so an exact
	// fit is not mistaken for an overflow. Content-Length is never consulted.
	body, err := io.ReadAll(io.LimitReader(resp.Body, c.opts.MaxBytes+1))
	if err != nil {
		return nil, c.transportError(err, u.Host)
	}
	if int64(len(body)) > c.opts.MaxBytes {
		return nil, toolerr.Newf(toolerr.CodeTooLarge, "%s is larger than %d bytes after decoding; raise max_bytes to read it", out.FinalURL, c.opts.MaxBytes).
			With("max_bytes", c.opts.MaxBytes)
	}
	out.Body = body
	out.Elapsed = time.Since(start)

	out.MediaType = mediaType(out.ContentType, body)
	kind, ok := classify(out.MediaType)
	if !ok {
		// The header is the remote site's text; quoted, like the
		// Content-Encoding and charset labels, so the message says whose
		// words they are.
		if out.ContentType == "" {
			return nil, toolerr.Newf(toolerr.CodeUnsupportedContentType, "%s declares no media type and sniffs as %q, which web-fetch does not extract", out.FinalURL, out.MediaType).
				With("content_type", out.MediaType+" (sniffed)")
		}
		return nil, toolerr.Newf(toolerr.CodeUnsupportedContentType, "%s is %q, which web-fetch does not extract", out.FinalURL, out.ContentType).
			With("content_type", out.ContentType)
	}
	out.Kind = kind
	return out, nil
}

// ParseURL validates the input URL: absolute, http or https, a host, no
// userinfo. The fragment is dropped (it never reaches a server).
func ParseURL(rawURL string) (*url.URL, error) {
	rawURL = strings.TrimSpace(rawURL)
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, toolerr.Newf(toolerr.CodeInvalidURL, "not a URL: %v", err).With("url", rawURL)
	}
	if u.Scheme == "" {
		return nil, toolerr.Newf(toolerr.CodeInvalidURL, "the URL has no scheme; write it as https://host/path").With("url", rawURL)
	}
	if !isHTTP(u.Scheme) {
		return nil, toolerr.Newf(toolerr.CodeSchemeNotAllowed, "scheme %q is not fetched; only http and https are", u.Scheme).
			With("scheme", u.Scheme)
	}
	if u.Host == "" || u.Hostname() == "" {
		return nil, toolerr.Newf(toolerr.CodeInvalidURL, "the URL has no host").With("url", rawURL)
	}
	if u.User != nil {
		return nil, toolerr.Newf(toolerr.CodeInvalidURL, "the URL carries user:password@, which web-fetch never sends").
			With("url", u.Redacted())
	}
	u.Fragment, u.RawFragment = "", ""
	return u, nil
}

func isHTTP(scheme string) bool {
	return strings.EqualFold(scheme, "http") || strings.EqualFold(scheme, "https")
}

// checkRedirect inspects every hop before it is followed: the cap, userinfo
// (Go would otherwise synthesise Authorization: Basic from it) and the scheme.
// The address itself is judged by the dialer when the hop connects.
func (c *Client) checkRedirect(next *http.Request, via []*http.Request, hops *[]string) error {
	*hops = append(*hops, via[len(via)-1].URL.String())
	if len(via) > c.opts.MaxRedirects {
		return toolerr.Newf(toolerr.CodeTooManyRedirects, "more than %d redirects", c.opts.MaxRedirects).
			With("hops", len(via))
	}
	if next.URL.User != nil {
		return toolerr.Newf(toolerr.CodeInvalidURL, "a redirect points at a URL with user:password@, which web-fetch never follows").
			With("url", next.URL.Redacted()).With("hop", len(via))
	}
	if !isHTTP(next.URL.Scheme) {
		return toolerr.Newf(toolerr.CodeSchemeNotAllowed, "a redirect points at scheme %q; only http and https are followed", next.URL.Scheme).
			With("scheme", next.URL.Scheme).With("hop", len(via))
	}
	// Go adds a Referer naming the previous hop. The header policy is "what
	// this tool chose to send and nothing else", so it goes.
	next.Header.Del("Referer")
	return nil
}

// transportError maps a failed exchange onto the error table. Errors the
// dialer or checkRedirect produced come back wrapped and keep their code.
func (c *Client) transportError(err error, host string) error {
	var te *toolerr.Error
	if errors.As(err, &te) {
		// A timeout the dialer raised knows the host but not the deadline;
		// every timeout the caller sees names it (RFP error table).
		if te.Code == toolerr.CodeTimeout && te.Details["timeout"] == nil {
			te = te.With("timeout", c.opts.Timeout.String())
		}
		return te
	}
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		return toolerr.Newf(toolerr.CodeTimeout, "fetching from %s exceeded the %s deadline", host, c.opts.Timeout).
			With("timeout", c.opts.Timeout.String()).With("host", host)
	}
	if errors.Is(err, context.Canceled) {
		return toolerr.Newf(toolerr.CodeConnectFailed, "the fetch from %s was cancelled", host).With("host", host).With("cause", "cancelled")
	}
	cause := err.Error()
	var ue *url.Error
	if errors.As(err, &ue) && ue.Err != nil {
		cause = ue.Err.Error()
	}
	return toolerr.Newf(toolerr.CodeConnectFailed, "could not complete the fetch from %s: %s", host, cause).
		With("host", host).With("cause", cause)
}

// mediaType is the header's media type, else the sniffed one. The header wins
// even when the bytes disagree (RFP §2).
func mediaType(contentType string, body []byte) string {
	if contentType != "" {
		if mt, _, err := mime.ParseMediaType(contentType); err == nil {
			return strings.ToLower(mt)
		}
		// An unparsable header still names something; keep its first token.
		if i := strings.IndexByte(contentType, ';'); i >= 0 {
			return strings.ToLower(strings.TrimSpace(contentType[:i]))
		}
		return strings.ToLower(strings.TrimSpace(contentType))
	}
	sniff := body
	if len(sniff) > 1024 {
		sniff = sniff[:1024]
	}
	mt, _, err := mime.ParseMediaType(http.DetectContentType(sniff))
	if err != nil {
		return "application/octet-stream"
	}
	return mt
}

// classify maps a media type onto a Kind (RFP §2 Content-Type table).
func classify(mediaType string) (Kind, bool) {
	switch {
	case mediaType == "text/html", mediaType == "application/xhtml+xml":
		return KindHTML, true
	case strings.HasPrefix(mediaType, "text/"):
		return KindText, true
	case mediaType == "application/json", mediaType == "application/xml", strings.HasSuffix(mediaType, "+json"):
		return KindText, true
	case strings.HasPrefix(mediaType, "application/") && strings.HasSuffix(mediaType, "+xml"):
		// application/*+xml (Atom, RSS, XHTML variants) is text; image/svg+xml
		// is an image and stays refused (RFP §2 table).
		return KindText, true
	}
	return 0, false
}
