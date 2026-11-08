package extract

import (
	"net/url"
	"strings"
	"testing"
)

// Every fixture here is invented to exercise one rule; no fetched page is
// pasted into the repository.

const pageA = `<!doctype html><html><head><title> Hello  World </title><base href="https://example.org/dir/"></head>
<body><header><h1>Site</h1></header><nav><a href="/x">nav</a></nav>
<main>
<h1>Title</h1>
<p>This is <strong>bold</strong>, <em>em</em>, <code>code</code> and <a href="a?b=1">a link</a>.<br>Next line &amp; more.</p>
<ul><li>one</li><li>two<ul><li>nested</li></ul></li></ul>
<ol start="3"><li>three</li><li>four</li></ol>
<pre><code class="language-go">fmt.Println("x")
</code></pre>
<blockquote><p>quoted</p></blockquote>
<table><tr><th>A</th><th>B</th></tr><tr><td>1</td><td>2|3</td></tr></table>
<img src="i.png" alt="An image">
<hr>
<p hidden>secret</p><p aria-hidden="true">also secret</p><p hidden="until-found">found</p>
<details><summary>More</summary><p>details body</p></details>
</main><footer>footer</footer><aside>aside</aside><script>var x = "<p>not text</p>"</script></body></html>`

const wantMarkdownA = "# Title\n" +
	"\n" +
	"This is **bold**, *em*, `code` and [a link](https://example.org/dir/a?b=1).\n" +
	"Next line & more.\n" +
	"\n" +
	"- one\n" +
	"- two\n" +
	"  - nested\n" +
	"\n" +
	"3. three\n" +
	"4. four\n" +
	"\n" +
	"```go\n" +
	"fmt.Println(\"x\")\n" +
	"```\n" +
	"\n" +
	"> quoted\n" +
	"\n" +
	"| A | B |\n" +
	"| --- | --- |\n" +
	"| 1 | 2\\|3 |\n" +
	"\n" +
	"An image\n" +
	"\n" +
	"---\n" +
	"\n" +
	"found\n" +
	"\n" +
	"More\n" +
	"\n" +
	"details body"

const wantTextA = "Title\n" +
	"\n" +
	"This is bold, em, code and a link.\n" +
	"Next line & more.\n" +
	"\n" +
	"one\n" +
	"two\n" +
	"nested\n" +
	"\n" +
	"three\n" +
	"four\n" +
	"\n" +
	"fmt.Println(\"x\")\n" +
	"\n" +
	"quoted\n" +
	"\n" +
	"A\tB\n" +
	"1\t2|3\n" +
	"\n" +
	"An image\n" +
	"\n" +
	"found\n" +
	"\n" +
	"More\n" +
	"\n" +
	"details body"

func page(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestMarkdownRendering(t *testing.T) {
	res, err := Extract(pageA, page(t, "https://example.org/page"), FormatMarkdown)
	if err != nil {
		t.Fatal(err)
	}
	if res.Title != "Hello World" {
		t.Errorf("title = %q", res.Title)
	}
	if res.Text != wantMarkdownA {
		t.Errorf("markdown differs:\n--- got ---\n%s\n--- want ---\n%s", res.Text, wantMarkdownA)
	}
	for _, dropped := range []string{"Site", "nav", "footer", "aside", "secret", "not text", "var x"} {
		if strings.Contains(res.Text, dropped) {
			t.Errorf("%q should have been dropped", dropped)
		}
	}
}

func TestTextRendering(t *testing.T) {
	res, err := Extract(pageA, page(t, "https://example.org/page"), FormatText)
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != wantTextA {
		t.Errorf("text differs:\n--- got ---\n%s\n--- want ---\n%s", res.Text, wantTextA)
	}
}

func TestRootSelection(t *testing.T) {
	long := strings.Repeat("body text ", 50)
	cases := map[string]struct {
		html     string
		contains string
		excludes string
	}{
		"main beats article": {
			`<body><article>art</article><main><p>main text</p></main></body>`, "main text", "art"},
		"role=main beats article": {
			`<body><div role="main"><p>role main</p></div><article>art</article></body>`, "role main", "art"},
		"single article": {
			`<body><p>chrome</p><article><p>the article</p></article></body>`, "the article", "chrome"},
		"largest of several articles": {
			`<body><article><p>tiny</p></article><article><p>` + long + `</p></article></body>`, "body text", "tiny"},
		"small articles fall back to body": {
			`<body><p>` + long + `</p><article><p>a</p></article><article><p>bb</p></article></body>`, "bb", ""},
		"no landmarks: body": {
			`<body><p>just body</p></body>`, "just body", ""},
	}
	for name, c := range cases {
		res, err := Extract(c.html, nil, FormatText)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.Contains(res.Text, c.contains) {
			t.Errorf("%s: missing %q in %q", name, c.contains, res.Text)
		}
		if c.excludes != "" && strings.Contains(res.Text, c.excludes) {
			t.Errorf("%s: %q should not be in %q", name, c.excludes, res.Text)
		}
	}
}

func TestArticleHeaderIsKeptAndBannerDropped(t *testing.T) {
	doc := `<body><header><p>banner</p></header><article><header><h1>Art Title</h1><p>byline</p></header><p>body</p><header role="banner">ad</header></article></body>`
	res, err := Extract(doc, nil, FormatMarkdown)
	if err != nil {
		t.Fatal(err)
	}
	if res.Title != "Art Title" {
		t.Errorf("title fallback to the root's h1: %q", res.Title)
	}
	if !strings.HasPrefix(res.Text, "# Art Title\n\nbyline\n\nbody") {
		t.Errorf("article header lost: %q", res.Text)
	}
	for _, d := range []string{"banner", "ad"} {
		if strings.Contains(res.Text, d) {
			t.Errorf("%q should have been dropped", d)
		}
	}
}

func TestLinksResolveAgainstBaseThenPage(t *testing.T) {
	withBase := `<head><base href="https://cdn.example/assets/"></head><body><p><a href="x.html">x</a> <a href="/root">r</a> <a href="#frag">f</a> <a href="javascript:void(0)">js</a> <a href="mailto:a@b.c">m</a></p></body>`
	res, err := Extract(withBase, page(t, "https://example.org/dir/page"), FormatMarkdown)
	if err != nil {
		t.Fatal(err)
	}
	want := "[x](https://cdn.example/assets/x.html) [r](https://cdn.example/root) [f](https://cdn.example/assets/#frag) js [m](mailto:a@b.c)"
	if res.Text != want {
		t.Errorf("got  %q\nwant %q", res.Text, want)
	}
	noBase := `<body><p><a href="x.html">x</a></p></body>`
	res, _ = Extract(noBase, page(t, "https://example.org/dir/page"), FormatMarkdown)
	if res.Text != "[x](https://example.org/dir/x.html)" {
		t.Errorf("page-relative: %q", res.Text)
	}
	res, _ = Extract(noBase, nil, FormatMarkdown)
	if res.Text != "[x](x.html)" {
		t.Errorf("no page URL leaves the href as written: %q", res.Text)
	}
	imgLink := `<body><p><a href="/p"><img src="i" alt="pic"></a><a href="/q"></a></p></body>`
	res, _ = Extract(imgLink, page(t, "https://e.org/"), FormatMarkdown)
	if res.Text != "[pic](https://e.org/p)[https://e.org/q](https://e.org/q)" {
		t.Errorf("image link and empty link: %q", res.Text)
	}
}

func TestWhitespaceAndEntities(t *testing.T) {
	doc := "<body><p>a\n\n   b&nbsp;&nbsp;c &lt;tag&gt; &amp; &quot;q&quot;</p>\n\n\n<p>second</p></body>"
	res, err := Extract(doc, nil, FormatText)
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "a b c <tag> & \"q\"\n\nsecond" {
		t.Errorf("%q", res.Text)
	}
}

func TestPreFenceGrowsPastBackticksInContent(t *testing.T) {
	doc := "<body><pre>x = ```\ny</pre></body>"
	res, _ := Extract(doc, nil, FormatMarkdown)
	if res.Text != "````\nx = ```\ny\n````" {
		t.Errorf("%q", res.Text)
	}
}

func TestTitleFallbacks(t *testing.T) {
	res, _ := Extract(`<head><title>  </title></head><body><main><h2>h2</h2><h1>First H1</h1></main></body>`, nil, FormatText)
	if res.Title != "First H1" {
		t.Errorf("blank title falls back to h1: %q", res.Title)
	}
	res, _ = Extract(`<body><p>no headings</p></body>`, nil, FormatText)
	if res.Title != "" {
		t.Errorf("no title: %q", res.Title)
	}
}

func TestDefinitionListsAndNestedOrdered(t *testing.T) {
	doc := `<body><dl><dt>Term</dt><dd>Definition</dd></dl><ol><li>a<ol><li>b</li></ol></li></ol></body>`
	res, _ := Extract(doc, nil, FormatMarkdown)
	want := "**Term**\n\nDefinition\n\n1. a\n   1. b"
	if res.Text != want {
		t.Errorf("got  %q\nwant %q", res.Text, want)
	}
}

func TestDocIDIsStableAndPrefixed(t *testing.T) {
	a, b := DocID("same"), DocID("same")
	if a != b || !strings.HasPrefix(a, "sha256:") || len(a) != len("sha256:")+64 {
		t.Errorf("%q %q", a, b)
	}
	if DocID("same") == DocID("other") {
		t.Error("different bodies must differ")
	}
}

// A link's scheme is judged after resolution against an allow-list (http,
// https, mailto, or none when there is no base), not before it against a
// deny-list: a <base href="javascript:..."> used to pass its scheme to every
// fragment- or query-only href (code-audit run-1, 2026-11-08). A base outside
// http(s) is ignored in favour of the page URL.
func TestLinkSchemesAreAllowListedAfterResolution(t *testing.T) {
	cases := []struct {
		name, base, href, want string
	}{
		{"javascript base, fragment href", "javascript:alert(1)", "#top", "[f](https://example.org/dir/page#top)"},
		{"javascript base, query href", "javascript:alert(1)", "?q", "[f](https://example.org/dir/page?q)"},
		{"javascript base, relative href", "javascript:alert(1)", "x.html", "[f](https://example.org/dir/x.html)"},
		{"data base, fragment href", "data:text/html,hi", "#top", "[f](https://example.org/dir/page#top)"},
		{"https base, fragment href", "https://cdn.example/a/", "#top", "[f](https://cdn.example/a/#top)"},
		{"no base, javascript href", "", "javascript:void(0)", "f"},
		{"no base, JAVASCRIPT href", "", "JavaScript:void(0)", "f"},
		{"no base, data href", "", "data:text/plain,hi", "f"},
		{"no base, vbscript href", "", "vbscript:msgbox(1)", "f"},
		{"no base, file href", "", "file:///etc/hosts", "f"},
		{"no base, custom scheme href", "", "myapp://open?x=1", "f"},
		{"no base, mailto href", "", "mailto:a@b.c", "[f](mailto:a@b.c)"},
		{"no base, absolute http href", "", "http://other.example/p", "[f](http://other.example/p)"},
	}
	for _, c := range cases {
		doc := "<head>"
		if c.base != "" {
			doc += `<base href="` + c.base + `">`
		}
		doc += `</head><body><p><a href="` + c.href + `">f</a></p></body>`
		res, err := Extract(doc, page(t, "https://example.org/dir/page"), FormatMarkdown)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if res.Text != c.want {
			t.Errorf("%s: got %q, want %q", c.name, res.Text, c.want)
		}
	}
	// Without a page URL a relative href stays as written and a non-http(s)
	// base cannot stand in for one.
	res, _ := Extract(`<head><base href="javascript:alert(1)"></head><body><p><a href="#top">f</a></p></body>`, nil, FormatMarkdown)
	if res.Text != "[f](#top)" {
		t.Errorf("no page URL, javascript base: %q", res.Text)
	}
}
