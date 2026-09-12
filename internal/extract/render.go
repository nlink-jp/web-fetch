package extract

import (
	"bytes"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// renderer writes one node tree as markdown or plain text. Block elements
// separate paragraphs with one blank line; inline text has its whitespace
// folded. Sub-renderers handle the contexts that need their lines prefixed
// (list items, block quotes) or folded to one line (headings, cells, links).
//
// The output is a bytes.Buffer, never rewritten: trailing whitespace is
// removed with Truncate, which costs the whitespace's length, not the
// output's. (A first draft rebuilt the buffer at every block boundary and
// took 28 s on a 4 MiB page — pre-release review, finding 1.)
type renderer struct {
	markdown bool
	base     *url.URL
	inCell   bool // inside a table cell: a nested table renders compactly
	out      bytes.Buffer
}

func (r *renderer) sub() *renderer {
	return &renderer{markdown: r.markdown, base: r.base, inCell: r.inCell}
}

func (r *renderer) walk(n *html.Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		r.node(c)
	}
}

func (r *renderer) node(n *html.Node) {
	switch n.Type {
	case html.TextNode:
		r.text(n.Data, false)
	case html.ElementNode:
		r.element(n)
	}
}

// skip is the drop list (RFP §2 extraction rule 2).
func skip(n *html.Node) bool {
	switch n.DataAtom {
	case atom.Script, atom.Style, atom.Noscript, atom.Template, atom.Svg, atom.Iframe,
		atom.Nav, atom.Footer, atom.Aside, atom.Form, atom.Head,
		atom.Object, atom.Embed, atom.Canvas, atom.Audio, atom.Video, atom.Map, atom.Area,
		atom.Input, atom.Select, atom.Textarea, atom.Option, atom.Button:
		return true
	case atom.Header:
		// A body-level header is the site's banner; an article's header is
		// its title block.
		if n.Parent != nil && n.Parent.DataAtom == atom.Body {
			return true
		}
		return strings.EqualFold(attr(n, "role"), "banner")
	}
	if hasAttr(n, "hidden") && !strings.EqualFold(attr(n, "hidden"), "until-found") {
		return true
	}
	if strings.EqualFold(attr(n, "aria-hidden"), "true") {
		return true
	}
	return false
}

func (r *renderer) element(n *html.Node) {
	if skip(n) {
		return
	}
	switch n.DataAtom {
	case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
		level := int(n.Data[1] - '0')
		content := r.inline(n)
		if content == "" {
			return
		}
		if r.markdown {
			content = strings.Repeat("#", level) + " " + content
		}
		r.block(content)
	case atom.P, atom.Div, atom.Section, atom.Article, atom.Main, atom.Address, atom.Figure,
		atom.Figcaption, atom.Header, atom.Hgroup, atom.Details, atom.Dialog, atom.Fieldset,
		atom.Legend, atom.Summary, atom.Center, atom.Body, atom.Html, atom.Caption, atom.Dd, atom.Dl:
		r.breakBlock()
		r.walk(n)
		r.breakBlock()
	case atom.Dt:
		content := r.inline(n)
		if content == "" {
			return
		}
		if r.markdown {
			content = "**" + content + "**"
		}
		r.block(content)
	case atom.Br:
		r.out.WriteByte('\n')
	case atom.Hr:
		r.breakBlock()
		if r.markdown {
			r.out.WriteString("---")
		}
		r.breakBlock()
	case atom.Ul, atom.Ol:
		r.list(n)
	case atom.Li:
		// A stray item outside a list renders as one.
		r.listItem(n, "- ")
	case atom.Pre:
		r.pre(n)
	case atom.Blockquote:
		sub := r.sub()
		sub.walk(n)
		quoted := tidy(sub.out.String())
		if quoted == "" {
			return
		}
		if r.markdown {
			quoted = "> " + strings.ReplaceAll(quoted, "\n", "\n> ")
		}
		r.block(quoted)
	case atom.Table:
		r.table(n)
	case atom.Img:
		if alt := collapse(attr(n, "alt")); alt != "" {
			r.text(alt, true)
		}
	case atom.A:
		r.link(n)
	case atom.Strong, atom.B:
		r.wrap(n, "**")
	case atom.Em, atom.I, atom.Cite, atom.Dfn, atom.Var:
		r.wrap(n, "*")
	case atom.Del, atom.S, atom.Strike:
		r.wrap(n, "~~")
	case atom.Code, atom.Kbd, atom.Samp, atom.Tt:
		content := r.inline(n)
		if content == "" {
			return
		}
		if r.markdown {
			content = "`" + content + "`"
		}
		r.text(content, true)
	default:
		r.walk(n)
	}
}

// text appends inline text. Unless raw, whitespace runs fold to one space and
// a leading space is dropped at the start of a line.
func (r *renderer) text(s string, raw bool) {
	if !raw {
		s = foldSpace(s)
	}
	if s == "" {
		return
	}
	if s[0] == ' ' && r.atLineStart() {
		s = s[1:]
	}
	r.out.WriteString(s)
}

// atLineStart looks at the last byte only.
func (r *renderer) atLineStart() bool {
	b := r.out.Bytes()
	if len(b) == 0 {
		return true
	}
	last := b[len(b)-1]
	return last == '\n' || last == ' '
}

// foldSpace folds whitespace runs to one space, keeping a leading or trailing
// space so words on either side of an inline element stay separated.
func foldSpace(s string) string {
	var sb strings.Builder
	space := false
	for _, c := range s {
		if unicode.IsSpace(c) {
			space = true
			continue
		}
		if space {
			sb.WriteByte(' ')
			space = false
		}
		sb.WriteRune(c)
	}
	if space {
		sb.WriteByte(' ')
	}
	return sb.String()
}

// inline renders n's children folded to one line.
func (r *renderer) inline(n *html.Node) string {
	sub := r.sub()
	sub.walk(n)
	return collapse(sub.out.String())
}

// trimTrailing removes trailing spaces, tabs and (when newlines is set)
// newlines by truncating the buffer — proportional to the whitespace, not to
// the output.
func (r *renderer) trimTrailing(newlines bool) {
	b := r.out.Bytes()
	n := len(b)
	for n > 0 {
		c := b[n-1]
		if c == ' ' || c == '\t' || (newlines && c == '\n') {
			n--
			continue
		}
		break
	}
	r.out.Truncate(n)
}

// breakBlock ends the current block: trailing whitespace goes, and one blank
// line follows whatever is there.
func (r *renderer) breakBlock() {
	r.trimTrailing(true)
	if r.out.Len() == 0 {
		return
	}
	r.out.WriteString("\n\n")
}

// ensureNewline ends the current line without opening a blank one.
func (r *renderer) ensureNewline() {
	r.trimTrailing(false)
	b := r.out.Bytes()
	if len(b) == 0 {
		return
	}
	if b[len(b)-1] != '\n' {
		r.out.WriteByte('\n')
	}
}

func (r *renderer) block(text string) {
	r.breakBlock()
	r.out.WriteString(text)
	r.breakBlock()
}

func (r *renderer) wrap(n *html.Node, marker string) {
	content := r.inline(n)
	if content == "" {
		return
	}
	if r.markdown {
		content = marker + content + marker
	}
	r.text(content, true)
}

// link renders <a>: markdown [text](absolute URL), plain text otherwise. A
// javascript: target or an unparsable href leaves the text alone.
func (r *renderer) link(n *html.Node) {
	content := r.inline(n)
	href := r.resolve(attr(n, "href"))
	if content == "" {
		content = href
	}
	if content == "" {
		return
	}
	if !r.markdown || href == "" {
		r.text(content, true)
		return
	}
	r.text("["+content+"]("+href+")", true)
}

// resolve absolutises href against the base; "" when unusable.
func (r *renderer) resolve(href string) string {
	href = strings.TrimSpace(href)
	if href == "" {
		return ""
	}
	u, err := url.Parse(href)
	if err != nil {
		return ""
	}
	if strings.EqualFold(u.Scheme, "javascript") || strings.EqualFold(u.Scheme, "data") {
		return ""
	}
	if r.base != nil {
		u = r.base.ResolveReference(u)
	}
	return u.String()
}

// list renders <ul>/<ol>. A list that follows inline text on the same line
// (an item's own text before its nested list) starts on the next line, not
// after a blank one, so nesting stays tight. Children that are not <li>
// (a wrapping <div>, a stray element) render in place rather than vanish.
func (r *renderer) list(n *html.Node) {
	ordered := n.DataAtom == atom.Ol
	index := 1
	if s := attr(n, "start"); s != "" {
		if v, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
			index = v
		}
	}
	r.ensureNewline()
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode || c.DataAtom != atom.Li {
			r.node(c)
			continue
		}
		marker := "- "
		if ordered {
			marker = fmt.Sprintf("%d. ", index)
			index++
		}
		r.listItem(c, marker)
	}
	r.breakBlock()
}

// listItem renders one <li>: the marker on the first line, continuation lines
// (including nested lists) indented to the marker's width.
func (r *renderer) listItem(li *html.Node, marker string) {
	if skip(li) {
		return
	}
	sub := r.sub()
	sub.walk(li)
	content := tidy(sub.out.String())
	if !r.markdown {
		marker = ""
	}
	indent := strings.Repeat(" ", len(marker))
	lines := strings.Split(content, "\n")
	r.ensureNewline()
	for i, l := range lines {
		switch {
		case i == 0:
			r.out.WriteString(marker + l)
		case l == "":
		default:
			r.out.WriteString(indent + l)
		}
		r.out.WriteByte('\n')
	}
}

// pre renders preformatted text as a fenced block (markdown) or as is.
func (r *renderer) pre(n *html.Node) {
	raw := textContent(n)
	raw = strings.TrimPrefix(raw, "\n")
	raw = strings.TrimRight(raw, " \t\n")
	if raw == "" {
		return
	}
	if !r.markdown {
		r.block(raw)
		return
	}
	lang := ""
	if code := findFirst(n, func(c *html.Node) bool { return c.DataAtom == atom.Code }); code != nil {
		for _, class := range strings.Fields(attr(code, "class")) {
			for _, prefix := range []string{"language-", "lang-"} {
				if strings.HasPrefix(class, prefix) {
					lang = strings.TrimPrefix(class, prefix)
				}
			}
		}
	}
	fence := "```"
	for strings.Contains(raw, fence) {
		fence += "`"
	}
	r.block(fence + lang + "\n" + raw + "\n" + fence)
}

// directRows returns the <tr> elements of one table, through thead / tbody /
// tfoot but not into a nested table — that one renders inside its cell.
func directRows(table *html.Node) []*html.Node {
	var rows []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode {
				continue
			}
			switch c.DataAtom {
			case atom.Table:
				continue
			case atom.Tr:
				rows = append(rows, c)
			default:
				walk(c)
			}
		}
	}
	walk(table)
	return rows
}

// table renders rows as pipe rows (markdown, with a separator after the first
// row) or tab-separated lines. Inside a cell, a nested table becomes one line:
// cells joined by " / ", rows by "; ".
func (r *renderer) table(n *html.Node) {
	rows := directRows(n)
	var lines []string
	for i, tr := range rows {
		var cells []string
		for c := tr.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode || (c.DataAtom != atom.Td && c.DataAtom != atom.Th) {
				continue
			}
			cellR := r.sub()
			cellR.inCell = true
			cellR.walk(c)
			cell := collapse(cellR.out.String())
			if r.markdown && !r.inCell {
				cell = strings.ReplaceAll(cell, "|", `\|`)
			}
			cells = append(cells, cell)
		}
		if len(cells) == 0 {
			continue
		}
		switch {
		case r.inCell:
			lines = append(lines, strings.Join(cells, " / "))
		case r.markdown:
			lines = append(lines, "| "+strings.Join(cells, " | ")+" |")
			if i == 0 {
				seps := make([]string, len(cells))
				for j := range seps {
					seps[j] = "---"
				}
				lines = append(lines, "| "+strings.Join(seps, " | ")+" |")
			}
		default:
			lines = append(lines, strings.Join(cells, "\t"))
		}
	}
	if len(lines) == 0 {
		return
	}
	if r.inCell {
		r.text(strings.Join(lines, "; "), true)
		return
	}
	r.block(strings.Join(lines, "\n"))
}
