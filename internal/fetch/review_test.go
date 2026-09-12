package fetch

import (
	"context"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/nlink-jp/web-fetch/internal/guard"
	"github.com/nlink-jp/web-fetch/internal/toolerr"
)

// Findings of the pre-release review (2026-09-12), pinned.

// Finding 10: Go adds a Referer on every redirect hop; the header policy is
// "what this tool chose to send", so it is removed, while the headers the
// tool did choose survive a cross-host hop.
func TestCrossHostRedirectCarriesOurHeadersAndNoReferer(t *testing.T) {
	r := newRig(t)
	c := r.client(Options{AcceptLanguage: "ja"})
	resp, err := c.Fetch(context.Background(), r.url("/cross"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(resp.FinalURL, "http://ok2.test:") || len(resp.Redirects) != 1 {
		t.Errorf("final=%q redirects=%v", resp.FinalURL, resp.Redirects)
	}
	echo := string(resp.Body)
	if !strings.Contains(echo, "UA=web-fetch/") || !strings.Contains(echo, "AL=ja") || !strings.Contains(echo, "REF=;") {
		t.Errorf("second hop saw: %s", echo)
	}
}

// Finding 14: a scheme-relative Location carrying credentials.
func TestSchemeRelativeRedirectWithUserinfoIsRefused(t *testing.T) {
	r := newRig(t)
	_, err := r.client(Options{}).Fetch(context.Background(), r.url("/schemerel"))
	if toolerr.Code(err) != toolerr.CodeInvalidURL || toolerr.Detail(err, "hop") == nil {
		t.Errorf("%v", err)
	}
	if strings.Contains(err.Error(), ":p@") {
		t.Error("the password must be redacted")
	}
}

// Finding 14: Content-Encoding spellings, and finding 7: image/svg+xml.
func TestEncodingSpellingsAndSVG(t *testing.T) {
	r := newRig(t)
	c := r.client(Options{})
	if resp, err := c.Fetch(context.Background(), r.url("/identity")); err != nil || string(resp.Body) != "plain identity" {
		t.Errorf("identity must be accepted: %v", err)
	}
	if _, err := c.Fetch(context.Background(), r.url("/xgzip")); toolerr.Code(err) != toolerr.CodeUnsupportedContentType || toolerr.Detail(err, "content_encoding") != "x-gzip" {
		t.Errorf("x-gzip is not the gzip the transport asked for: %v", err)
	}
	if _, err := c.Fetch(context.Background(), r.url("/svg")); toolerr.Code(err) != toolerr.CodeUnsupportedContentType {
		t.Errorf("image/svg+xml is an image: %v", err)
	}
	if k, ok := classify("image/svg+xml"); ok || k != 0 {
		t.Error("classify must refuse image/svg+xml")
	}
	if k, ok := classify("application/atom+xml"); !ok || k != KindText {
		t.Error("classify must accept application/*+xml")
	}
}

// stallingResolver never answers until the context ends.
type stallingResolver struct{}

func (stallingResolver) LookupNetIP(ctx context.Context, _, _ string) ([]netip.Addr, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// Finding 8: every timeout the caller sees names the deadline, the ones the
// dialer raised included.
func TestDialerTimeoutNamesTheDeadline(t *testing.T) {
	c := New(Options{
		Dialer:       &guard.Dialer{Resolver: stallingResolver{}},
		Timeout:      300 * time.Millisecond,
		MaxRedirects: 5,
		MaxBytes:     1 << 20,
		UserAgent:    "web-fetch/test",
	})
	start := time.Now()
	_, err := c.Fetch(context.Background(), "http://stalls.test/")
	if toolerr.Code(err) != toolerr.CodeTimeout {
		t.Fatalf("%v", err)
	}
	if toolerr.Detail(err, "timeout") != "300ms" {
		t.Errorf("timeout detail: %v", toolerr.Detail(err, "timeout"))
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("the deadline did not cut the stalled lookup: %s", time.Since(start))
	}
}
