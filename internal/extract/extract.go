package extract

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"
	"unicode"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/nlink-jp/web-fetch/internal/toolerr"
)

// Formats Extract renders. "raw" is not a rendering and never reaches here.
const (
	FormatMarkdown = "markdown"
	FormatText     = "text"
)

// Result is an extracted page.
type Result struct {
	Title string
	Text  string
}

// Extract renders decoded HTML as markdown or plain text from the selected
// body root. page is the final URL, used to absolutise links when the
// document has no <base href>; nil leaves relative URLs as written.
func Extract(decoded string, page *url.URL, format string) (Result, error) {
	doc, err := html.Parse(strings.NewReader(decoded))
	if err != nil {
		return Result{}, toolerr.Newf(toolerr.CodeUnsupportedContentType, "the HTML could not be parsed: %v", err).With("cause", err.Error())
	}
	root := selectRoot(doc)
	// A root that is itself hidden (a <main hidden>) would render its content
	// because the walk starts below it; fall back to the body in that case.
	if root.Type == html.ElementNode && root.DataAtom != atom.Body && skip(root) {
		if body := findFirst(doc, func(n *html.Node) bool { return n.DataAtom == atom.Body }); body != nil {
			root = body
		}
	}
	writeLimit, textLimit := renderLimits(len(decoded))
	r := &renderer{markdown: format != FormatText, base: baseURL(doc, page), budget: &budget{left: writeLimit}}
	r.walk(root)
	if r.budget.exhausted || r.out.Len() > textLimit {
		return Result{}, toolerr.Newf(toolerr.CodeTooLarge,
			"the page renders to more than %d bytes of %s: its nested blocks multiply the text far past the %d bytes fetched", textLimit, format, len(decoded)).
			With("render_limit", textLimit).With("decoded_bytes", len(decoded))
	}
	return Result{Title: titleOf(doc, root), Text: tidy(r.out.String())}, nil
}

// renderLimits derives the two bounds of one render from the decoded size.
// writeLimit caps the bytes written across every renderer (the work done:
// each nesting level copies its content once, and a legitimate page nests a
// handful of levels); textLimit caps the rendered text itself (what the
// engine then pages by runes, at four bytes each). Both are a multiple of
// the input plus a floor, so a tiny page with a long table or list is not
// refused. See budget.
func renderLimits(decoded int) (writeLimit, textLimit int) {
	return 16*decoded + 1<<20, 4*decoded + 1<<20
}

// DocID identifies a decoded body: "sha256:" plus the hex digest. Two page
// turns that see the same DocID walk the same text (RFP §2).
func DocID(decoded string) string {
	sum := sha256.Sum256([]byte(decoded))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// selectRoot picks the element whose content is the page's body (RFP §2
// extraction rule 1).
func selectRoot(doc *html.Node) *html.Node {
	body := findFirst(doc, func(n *html.Node) bool { return n.DataAtom == atom.Body })
	if body == nil {
		return doc
	}
	if m := findFirst(body, func(n *html.Node) bool { return n.DataAtom == atom.Main }); m != nil {
		return m
	}
	if m := findFirst(body, func(n *html.Node) bool { return strings.EqualFold(attr(n, "role"), "main") }); m != nil {
		return m
	}
	articles := findAll(body, func(n *html.Node) bool { return n.DataAtom == atom.Article })
	switch len(articles) {
	case 0:
		return body
	case 1:
		return articles[0]
	}
	largest, largestLen := articles[0], textLen(articles[0])
	for _, a := range articles[1:] {
		if l := textLen(a); l > largestLen {
			largest, largestLen = a, l
		}
	}
	if largestLen*4 < textLen(body) {
		return body
	}
	return largest
}

// titleOf is <title>, else the first <h1> inside the root.
func titleOf(doc, root *html.Node) string {
	if t := findFirst(doc, func(n *html.Node) bool { return n.DataAtom == atom.Title }); t != nil {
		if s := collapse(textContent(t)); s != "" {
			return s
		}
	}
	if h := findFirst(root, func(n *html.Node) bool { return n.DataAtom == atom.H1 }); h != nil {
		return collapse(textContent(h))
	}
	return ""
}

// baseURL is <base href> resolved against the page URL, else the page URL.
func baseURL(doc *html.Node, page *url.URL) *url.URL {
	b := findFirst(doc, func(n *html.Node) bool { return n.DataAtom == atom.Base })
	if b == nil {
		return page
	}
	href := strings.TrimSpace(attr(b, "href"))
	if href == "" {
		return page
	}
	u, err := url.Parse(href)
	if err != nil {
		return page
	}
	if page != nil {
		return page.ResolveReference(u)
	}
	return u
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func hasAttr(n *html.Node, key string) bool {
	for _, a := range n.Attr {
		if a.Key == key {
			return true
		}
	}
	return false
}

func findFirst(n *html.Node, pred func(*html.Node) bool) *html.Node {
	if n.Type == html.ElementNode && pred(n) {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if f := findFirst(c, pred); f != nil {
			return f
		}
	}
	return nil
}

func findAll(n *html.Node, pred func(*html.Node) bool) []*html.Node {
	var out []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && pred(n) {
			out = append(out, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return out
}

// textContent concatenates the text nodes under n, whitespace as written.
func textContent(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return sb.String()
}

// textLen measures visible text for root selection, skipping script and
// style bodies.
func textLen(n *html.Node) int {
	total := 0
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			total += len(strings.TrimSpace(n.Data))
			return
		}
		if n.Type == html.ElementNode && (n.DataAtom == atom.Script || n.DataAtom == atom.Style) {
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return total
}

// collapse folds every whitespace run (NBSP included) to one space and trims.
func collapse(s string) string {
	var sb strings.Builder
	space := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			space = true
			continue
		}
		if space && sb.Len() > 0 {
			sb.WriteByte(' ')
		}
		space = false
		sb.WriteRune(r)
	}
	return sb.String()
}

// tidy trims trailing spaces per line, folds runs of blank lines to one, and
// trims the whole.
func tidy(s string) string {
	lines := strings.Split(s, "\n")
	var out []string
	blank := 0
	for _, l := range lines {
		l = strings.TrimRight(l, " \t")
		if l == "" {
			blank++
			if blank > 1 {
				continue
			}
		} else {
			blank = 0
		}
		out = append(out, l)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}
