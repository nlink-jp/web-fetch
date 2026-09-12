// Package engine is the one path both faces share: it validates a request
// against the configuration, fetches (or takes from the MCP cache), decodes,
// renders and pages, and shapes the response the RFP specifies. The CLI and
// the MCP tool cannot diverge because neither has logic of its own.
package engine

import (
	"context"
	"net/url"
	"time"

	"github.com/nlink-jp/web-fetch/internal/cache"
	"github.com/nlink-jp/web-fetch/internal/config"
	"github.com/nlink-jp/web-fetch/internal/extract"
	"github.com/nlink-jp/web-fetch/internal/fetch"
	"github.com/nlink-jp/web-fetch/internal/guard"
	"github.com/nlink-jp/web-fetch/internal/toolerr"
)

// FormatRaw is the format that returns the decoded body as is. It is also
// the effective format of every non-HTML body.
const FormatRaw = "raw"

// Document is one decoded fetch — the unit the MCP cache holds. Extraction
// runs per call from Body, so the format is not part of it.
type Document struct {
	URL           string
	FinalURL      string
	Status        int
	ContentType   string
	Kind          fetch.Kind
	Charset       string
	CharsetSource string
	Body          string // decoded UTF-8
	DocID         string
	Redirects     []string
	FetchedAt     time.Time
	Bytes         int
	Elapsed       time.Duration
}

// Request is one fetch call. Zero values mean "the configured default".
type Request struct {
	URL      string
	Format   string
	MaxChars int
	Offset   int
	Fresh    bool // MCP only: drop the cached document first
}

// Meta is what rides beside the payload.
type Meta struct {
	Bytes     int    `json:"bytes"`
	ElapsedMS int64  `json:"elapsed_ms"`
	Cache     string `json:"cache,omitempty"` // MCP only: hit | miss
}

// Result is the response of both faces (RFP §2 Input / Output).
type Result struct {
	URL           string    `json:"url"`
	FinalURL      string    `json:"final_url"`
	Status        int       `json:"status"`
	ContentType   string    `json:"content_type"`
	Charset       string    `json:"charset"`
	CharsetSource string    `json:"charset_source"`
	Title         string    `json:"title"`
	Format        string    `json:"format"`
	DocID         string    `json:"doc_id"`
	Text          string    `json:"text"`
	Offset        int       `json:"offset"`
	ReturnedChars int       `json:"returned_chars"`
	TotalChars    int       `json:"total_chars"`
	Truncated     bool      `json:"truncated,omitempty"`
	NextOffset    int       `json:"next_offset,omitempty"`
	Redirects     []string  `json:"redirects,omitempty"`
	FetchedAt     time.Time `json:"fetched_at"`
	Meta          Meta      `json:"meta"`
}

// Fetcher is what the engine fetches through; *fetch.Client is one.
type Fetcher interface {
	Fetch(ctx context.Context, rawURL string) (*fetch.Response, error)
}

// Engine binds a configuration, a fetcher and (for MCP) a cache.
type Engine struct {
	cfg     *config.Config
	fetcher Fetcher
	cache   *cache.Cache[*Document] // nil: no cache (the CLI)
	now     func() time.Time
}

// New builds an engine. cache may be nil.
func New(cfg *config.Config, f Fetcher, c *cache.Cache[*Document]) *Engine {
	return &Engine{cfg: cfg, fetcher: f, cache: c, now: time.Now}
}

// NewClient wires the HTTP client from a configuration: the guard's dialer
// with the operator's allow_private, the deadline, the caps and the headers.
func NewClient(cfg *config.Config, version string) *fetch.Client {
	ua := cfg.Fetch.UserAgent
	if ua == "" {
		ua = config.DefaultUserAgent(version)
	}
	return fetch.New(fetch.Options{
		Dialer:         &guard.Dialer{AllowPrivate: cfg.Network.AllowPrivate},
		Timeout:        cfg.Fetch.Timeout,
		MaxRedirects:   cfg.Network.MaxRedirects,
		MaxBytes:       cfg.Fetch.MaxBytes,
		UserAgent:      ua,
		AcceptLanguage: cfg.Fetch.AcceptLanguage,
	})
}

// NewCache builds the MCP cache from the configuration.
func NewCache(cfg *config.Config) *cache.Cache[*Document] {
	return cache.New[*Document](cfg.Cache.TTL, cfg.Cache.MaxEntries)
}

// Fetch answers one request.
func (e *Engine) Fetch(ctx context.Context, req Request) (*Result, error) {
	format := req.Format
	if format == "" {
		format = e.cfg.Fetch.Format
	}
	if err := config.CheckFormat(format); err != nil {
		return nil, toolerr.Newf(toolerr.CodeInvalidArgument, "%v", err).
			With("argument", "format").With("allowed", config.Formats)
	}
	maxChars := req.MaxChars
	if maxChars == 0 {
		maxChars = e.cfg.Fetch.MaxChars
	}
	if maxChars < config.MaxCharsMin || maxChars > config.MaxCharsMax {
		return nil, toolerr.Newf(toolerr.CodeInvalidArgument, "max_chars must be between %d and %d (got %d)", config.MaxCharsMin, config.MaxCharsMax, maxChars).
			With("argument", "max_chars").With("allowed", []int{config.MaxCharsMin, config.MaxCharsMax})
	}
	if req.Offset < 0 {
		return nil, toolerr.Newf(toolerr.CodeInvalidArgument, "offset cannot be negative (got %d)", req.Offset).
			With("argument", "offset").With("allowed", "0 or more")
	}
	u, err := fetch.ParseURL(req.URL)
	if err != nil {
		return nil, err
	}
	key := u.String()

	doc, cacheState, err := e.document(ctx, key, req.Fresh)
	if err != nil {
		return nil, err
	}

	// Render. A non-HTML body has one rendering, raw, whatever was asked.
	effective := format
	title, text := "", doc.Body
	if doc.Kind != fetch.KindHTML {
		effective = FormatRaw
	} else if format != FormatRaw {
		final, perr := url.Parse(doc.FinalURL)
		if perr != nil {
			final = nil
		}
		res, xerr := extract.Extract(doc.Body, final, format)
		if xerr != nil {
			return nil, xerr
		}
		title, text = res.Title, res.Text
	}

	// Page by runes: every character reachable, the cut position stable for
	// the same DocID.
	runes := []rune(text)
	total := len(runes)
	start := req.Offset
	if start > total {
		start = total
	}
	end := start + maxChars
	if end > total {
		end = total
	}
	out := &Result{
		URL:           doc.URL,
		FinalURL:      doc.FinalURL,
		Status:        doc.Status,
		ContentType:   doc.ContentType,
		Charset:       doc.Charset,
		CharsetSource: doc.CharsetSource,
		Title:         title,
		Format:        effective,
		DocID:         doc.DocID,
		Text:          string(runes[start:end]),
		Offset:        req.Offset,
		ReturnedChars: end - start,
		TotalChars:    total,
		Redirects:     doc.Redirects,
		FetchedAt:     doc.FetchedAt,
		Meta:          Meta{Bytes: doc.Bytes, ElapsedMS: doc.Elapsed.Milliseconds(), Cache: cacheState},
	}
	if end < total {
		out.Truncated = true
		out.NextOffset = end
	}
	return out, nil
}

// document returns the decoded document for key: from the cache when there
// is one and the entry is live, else fetched, decoded and (on success) cached.
// Errors are never cached.
func (e *Engine) document(ctx context.Context, key string, fresh bool) (*Document, string, error) {
	state := ""
	if e.cache != nil && e.cache.Enabled() {
		if fresh {
			e.cache.Delete(key)
		} else if doc, _, ok := e.cache.Get(key); ok {
			return doc, "hit", nil
		}
		state = "miss"
	}
	resp, err := e.fetcher.Fetch(ctx, key)
	if err != nil {
		return nil, "", err
	}
	decoded, err := extract.Decode(resp.Body, resp.ContentType, resp.Kind == fetch.KindHTML)
	if err != nil {
		return nil, "", err
	}
	doc := &Document{
		URL:           resp.URL,
		FinalURL:      resp.FinalURL,
		Status:        resp.Status,
		ContentType:   resp.ContentType,
		Kind:          resp.Kind,
		Charset:       decoded.Charset,
		CharsetSource: decoded.Source,
		Body:          decoded.Text,
		DocID:         extract.DocID(decoded.Text),
		Redirects:     resp.Redirects,
		FetchedAt:     e.now().UTC(),
		Bytes:         len(resp.Body),
		Elapsed:       resp.Elapsed,
	}
	if state == "miss" {
		e.cache.Put(key, doc)
	}
	return doc, state, nil
}
