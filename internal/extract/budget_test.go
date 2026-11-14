package extract

import (
	"strings"
	"testing"
	"time"

	"github.com/nlink-jp/web-fetch/internal/toolerr"
)

// The render budget (code-audit run-1, 2026-11-08: nested-block-prefix
// amplification). A page's nested blockquotes or list items put one prefix
// per level on every line, so the markdown grows as lines x depth and the
// copying as lines x depth²: 509 levels around a million short lines fits in
// the default max_bytes and used to render to about 1 GiB after 1e12 bytes
// of copying. The budget refuses such a page as too_large in bounded time.

// nested wraps body in depth copies of open...close.
func nested(open, close string, depth int, body string) string {
	return strings.Repeat(open, depth) + body + strings.Repeat(close, depth)
}

func TestDeeplyNestedBlockquotesAreRefusedQuickly(t *testing.T) {
	lines := strings.Repeat("a<br>", 100_000)
	doc := "<html><body>" + nested("<blockquote>", "</blockquote>", 500, lines) + "</body></html>"
	for _, format := range []string{FormatMarkdown, FormatText} {
		start := time.Now()
		res, err := Extract(doc, nil, format)
		elapsed := time.Since(start)
		if toolerr.Code(err) != toolerr.CodeTooLarge {
			t.Errorf("%s: want too_large, got err=%v text=%d bytes", format, err, len(res.Text))
		}
		if toolerr.Detail(err, "render_limit") == nil {
			t.Errorf("%s: the error names no render_limit: %v", format, err)
		}
		if elapsed > 5*time.Second {
			t.Errorf("%s: refusing took %s", format, elapsed)
		}
		t.Logf("%s: %d bytes of HTML refused in %s", format, len(doc), elapsed)
	}
}

func TestDeeplyNestedWideOrderedListsAreRefused(t *testing.T) {
	lines := strings.Repeat("a<br>", 100_000)
	doc := "<html><body>" + nested(`<ol start="999999999"><li>`, "</li></ol>", 250, lines) + "</body></html>"
	start := time.Now()
	_, err := Extract(doc, nil, FormatMarkdown)
	if toolerr.Code(err) != toolerr.CodeTooLarge {
		t.Errorf("want too_large, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("refusing took %s", elapsed)
	}
}

// An <ol start> wider than any real list counts from 1: the marker's width
// is the indent of every continuation line under it.
func TestOrderedListStartIsHonouredOnlyWithinNineDigits(t *testing.T) {
	for in, want := range map[string]string{
		`<ol start="7"><li>x</li></ol>`:                    "7. x",
		`<ol start="-3"><li>x</li></ol>`:                   "-3. x",
		`<ol start="999999999"><li>x</li></ol>`:            "999999999. x",
		`<ol start="1000000000"><li>x</li></ol>`:           "1. x",
		`<ol start="-9223372036854775808"><li>x</li></ol>`: "1. x",
	} {
		res, err := Extract("<body>"+in+"</body>", nil, FormatMarkdown)
		if err != nil || res.Text != want {
			t.Errorf("%s: got %q, %v; want %q", in, res.Text, err, want)
		}
	}
}

// Nesting a page can plausibly have — a long comment thread, a deep menu —
// renders as before: the budget is a multiple of the page, not a fixed cap.
func TestOrdinaryNestingStaysWithinTheBudget(t *testing.T) {
	thread := nested("<blockquote>", "</blockquote>", 20, strings.Repeat("<p>a reply of ordinary length, a sentence or two long.</p>", 200))
	menu := nested("<ul><li>", "</li></ul>", 12, strings.Repeat("<li><a href=\"/x\"><strong>item</strong></a></li>", 300))
	for name, body := range map[string]string{"thread": thread, "menu": menu} {
		res, err := Extract("<html><body>"+body+"</body></html>", nil, FormatMarkdown)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !strings.Contains(res.Text, strings.Repeat("> ", 20)+"a reply") && !strings.Contains(res.Text, "- [**item**](/x)") {
			t.Errorf("%s: unexpected rendering: %.200q", name, res.Text)
		}
	}
	// Tiny input, long legitimate output: the floor, not the multiple, rules.
	write, text := renderLimits(100)
	if write < 1<<20 || text < 1<<20 {
		t.Errorf("limits for a 100-byte page have no floor: %d %d", write, text)
	}
}

// A <pre> of backticks (code-audit run-2, 2026-11-08): the fence around a
// block must be longer than its longest backtick run, and finding that
// length used to cost the square of the block — the fence grew one backtick
// at a time and the whole block was searched again at every step, uncharged.
// One pass now, charged to the budget.
func TestPreOfBackticksRendersInLinearTime(t *testing.T) {
	const n = 1 << 20
	ticks := strings.Repeat("`", n)
	doc := "<html><body><pre>" + ticks + "</pre></body></html>"
	start := time.Now()
	res, err := Extract(doc, nil, FormatMarkdown)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("rendering took %s", elapsed)
	}
	fence := ticks + "`"
	if res.Text != fence+"\n"+ticks+"\n"+fence {
		t.Errorf("fence is not the longest run + 1: %.40q… (%d bytes)", res.Text, len(res.Text))
	}
	t.Logf("%d backticks rendered in %s", n, elapsed)
}

func TestLongestRun(t *testing.T) {
	for in, want := range map[string]int{"": 0, "abc": 0, "`": 1, "a``b```c``": 3, "````": 4, "x = ```\ny": 3} {
		if got := longestRun(in, '`'); got != want {
			t.Errorf("longestRun(%q) = %d, want %d", in, got, want)
		}
	}
}

// TestParsingManyShortTextRunsStaysLinear holds the parser to linear time on
// the input GO-2026-6860 describes: short text runs separated by end tags the
// parser ignores. Up to golang.org/x/net v0.61 each run was appended to the
// previous text node by copying the whole accumulated string. Measured on
// 2026-11-14 with this 4 MiB page: 17 s on v0.59.0 (25 s under the race
// detector), 0.11 s on v0.62.0 (1.7 s under the race detector). The default
// fetch cap is 5 MiB and the largest allowed is 50 MiB, where the old parser
// would run for the better part of an hour. The limit sits between the two
// measurements with room on both sides, so it fails on a dependency that has
// lost the fix and not on a busy machine.
func TestParsingManyShortTextRunsStaysLinear(t *testing.T) {
	page := "<body>" + strings.Repeat("a</x>", (4<<20)/5) + "</body>"
	start := time.Now()
	if _, err := Extract(page, nil, FormatText); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if took := time.Since(start); took > 8*time.Second {
		t.Fatalf("4 MiB of short text runs took %v; the parser is quadratic again (golang.org/x/net below v0.62.0?)", took)
	}
}
