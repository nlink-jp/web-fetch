package extract

import (
	"strings"
	"testing"
	"time"
)

// Findings of the pre-release review (2026-09-12), pinned.

// Finding 1: the renderer rebuilt its buffer at every block boundary and was
// quadratic in output size (28 s for 4 MiB). Linear now: four times the
// input must not cost more than about four times the time.
func TestRenderingScalesLinearly(t *testing.T) {
	page := func(mib int) string {
		var sb strings.Builder
		para := "<p>word word word word word word word word word word.</p>\n"
		for sb.Len() < mib<<20 {
			sb.WriteString(para)
		}
		return "<html><body><main>" + sb.String() + "</main></body></html>"
	}
	small, large := page(1), page(4)
	timeIt := func(doc string) time.Duration {
		start := time.Now()
		res, err := Extract(doc, nil, FormatMarkdown)
		if err != nil || len(res.Text) < 1000 {
			t.Fatalf("extract: %v (%d chars)", err, len(res.Text))
		}
		return time.Since(start)
	}
	_ = timeIt(small) // warm the caches
	t1 := timeIt(small)
	t4 := timeIt(large)
	if t4 > 8*t1+500*time.Millisecond {
		t.Errorf("rendering is superlinear: 1 MiB in %s, 4 MiB in %s", t1, t4)
	}
	t.Logf("1 MiB in %s, 4 MiB in %s", t1, t4)
}

// Finding 3: a nested table rendered twice — flattened into the outer table's
// rows and again inside its cell. It now renders once, compactly, in the cell.
func TestNestedTableRendersOnceInsideItsCell(t *testing.T) {
	doc := `<body><table><tr><td>outer A</td><td><table><tr><td>inner 1</td><td>inner 2</td></tr><tr><td>inner 3</td><td>inner 4</td></tr></table></td></tr></table></body>`
	res, err := Extract(doc, nil, FormatMarkdown)
	if err != nil {
		t.Fatal(err)
	}
	want := "| outer A | inner 1 / inner 2; inner 3 / inner 4 |\n| --- | --- |"
	if res.Text != want {
		t.Errorf("got  %q\nwant %q", res.Text, want)
	}
	if strings.Count(res.Text, "inner 1") != 1 {
		t.Error("the inner table must appear exactly once")
	}
}

// Finding 5: the selected root's own hidden attribute was never checked.
func TestHiddenRootFallsBackToBody(t *testing.T) {
	doc := `<body><main hidden><p>secret</p></main><p>visible</p></body>`
	res, err := Extract(doc, nil, FormatText)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Text, "secret") || !strings.Contains(res.Text, "visible") {
		t.Errorf("%q", res.Text)
	}
}

// Finding 6: list children that are not <li> were dropped with their content.
func TestListChildrenThatAreNotItemsStillRender(t *testing.T) {
	doc := `<body><ul><div><li>wrapped</li></div><li>plain</li><p>stray</p></ul></body>`
	res, err := Extract(doc, nil, FormatMarkdown)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"- wrapped", "- plain", "stray"} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("missing %q in %q", want, res.Text)
		}
	}
}

// The blank-line rule, as implemented and as the RFP now states it: blocks are
// separated by exactly one blank line, however many the source had.
func TestBlankLinesFoldToOne(t *testing.T) {
	doc := "<body><p>a</p>\n\n\n\n<div></div><div></div><p>b</p><br><br><br><p>c</p></body>"
	res, _ := Extract(doc, nil, FormatText)
	if strings.Contains(res.Text, "\n\n\n") {
		t.Errorf("more than one blank line survived: %q", res.Text)
	}
}
