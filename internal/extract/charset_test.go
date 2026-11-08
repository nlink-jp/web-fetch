package extract

import (
	"bytes"
	"strings"
	"testing"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/unicode"

	"github.com/nlink-jp/web-fetch/internal/toolerr"
)

// jp stays inside JIS X 0208 so every legacy encoder in the test can encode it.
const jp = "日本語のテスト。文字コード判定"

func encode(t *testing.T, enc encoding.Encoding, s string) []byte {
	t.Helper()
	b, err := enc.NewEncoder().Bytes([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestHeaderCharsetDecodesLegacyJapanese(t *testing.T) {
	cases := []struct {
		label string
		enc   encoding.Encoding
		want  string
	}{
		{"Shift_JIS", japanese.ShiftJIS, "shift_jis"},
		{"EUC-JP", japanese.EUCJP, "euc-jp"},
		{"ISO-2022-JP", japanese.ISO2022JP, "iso-2022-jp"},
		{"x-sjis", japanese.ShiftJIS, "shift_jis"}, // a WHATWG alias
	}
	for _, c := range cases {
		body := append([]byte("<html><body><p>"), encode(t, c.enc, jp)...)
		d, err := Decode(body, "text/html; charset="+c.label, true)
		if err != nil {
			t.Fatalf("%s: %v", c.label, err)
		}
		if d.Charset != c.want || d.Source != SourceHeader || !strings.Contains(d.Text, jp) {
			t.Errorf("%s: charset=%q source=%q text=%q", c.label, d.Charset, d.Source, d.Text)
		}
	}
}

func TestMetaPrescanDecidesWhenTheHeaderIsSilent(t *testing.T) {
	sjis := encode(t, japanese.ShiftJIS, jp)
	for name, head := range map[string]string{
		"charset attribute": `<html><head><meta charset="shift_jis"><title>t</title></head><body><p>`,
		"http-equiv":        `<html><head><meta http-equiv="Content-Type" content="text/html; charset=Shift_JIS"></head><body><p>`,
		"uppercase attr":    `<HTML><HEAD><META CHARSET=Shift_JIS></HEAD><BODY><P>`,
	} {
		body := append([]byte(head), sjis...)
		d, err := Decode(body, "text/html", true)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if d.Charset != "shift_jis" || d.Source != SourceMeta || !strings.Contains(d.Text, jp) {
			t.Errorf("%s: charset=%q source=%q", name, d.Charset, d.Source)
		}
	}
}

func TestHeaderWinsOverMeta(t *testing.T) {
	body := []byte(`<html><head><meta charset="shift_jis"></head><body>plain ascii</body></html>`)
	d, err := Decode(body, "text/html; charset=utf-8", true)
	if err != nil {
		t.Fatal(err)
	}
	if d.Charset != "utf-8" || d.Source != SourceHeader {
		t.Errorf("charset=%q source=%q", d.Charset, d.Source)
	}
}

func TestBOMWinsOverEverything(t *testing.T) {
	utf8bom := append([]byte{0xEF, 0xBB, 0xBF}, []byte(`<meta charset="shift_jis">`+jp)...)
	d, err := Decode(utf8bom, "text/html; charset=euc-jp", true)
	if err != nil {
		t.Fatal(err)
	}
	if d.Charset != "utf-8" || d.Source != SourceBOM || strings.HasPrefix(d.Text, string(rune(0xFEFF))) || !strings.Contains(d.Text, jp) {
		t.Errorf("utf-8 bom: charset=%q source=%q text=%q", d.Charset, d.Source, d.Text[:10])
	}
	le := encode(t, unicode.UTF16(unicode.LittleEndian, unicode.UseBOM), jp)
	d, err = Decode(le, "text/plain", false)
	if err != nil {
		t.Fatal(err)
	}
	if d.Charset != "utf-16le" || d.Source != SourceBOM || d.Text != jp {
		t.Errorf("utf-16le bom: charset=%q source=%q text=%q", d.Charset, d.Source, d.Text)
	}
}

func TestUndeclaredBodies(t *testing.T) {
	d, err := Decode([]byte("<p>"+jp+"</p>"), "text/html", true)
	if err != nil {
		t.Fatal(err)
	}
	if d.Charset != "utf-8" || d.Source != SourceUTF8 {
		t.Errorf("valid utf-8: charset=%q source=%q", d.Charset, d.Source)
	}
	// 0x93 0x94 are curly quotes in windows-1252 and invalid UTF-8.
	d, err = Decode([]byte{'<', 'p', '>', 0x93, 'q', 0x94}, "text/html", true)
	if err != nil {
		t.Fatal(err)
	}
	if d.Charset != "windows-1252" || d.Source != SourceFallback || !strings.Contains(d.Text, "“q”") {
		t.Errorf("fallback: charset=%q source=%q text=%q", d.Charset, d.Source, d.Text)
	}
}

func TestDeclaredUnknownLabelIsCharsetUnsupported(t *testing.T) {
	_, err := Decode([]byte("x"), "text/html; charset=x-klingon", true)
	if toolerr.Code(err) != toolerr.CodeCharsetUnsupported || toolerr.Detail(err, "charset") != "x-klingon" {
		t.Errorf("header: %v", err)
	}
	_, err = Decode([]byte(`<meta charset="x-klingon">`), "text/html", true)
	if toolerr.Code(err) != toolerr.CodeCharsetUnsupported {
		t.Errorf("meta: %v", err)
	}
}

func TestPrescanBoundaries(t *testing.T) {
	// The meta element is ignored for non-HTML bodies …
	d, err := Decode([]byte(`<meta charset="shift_jis"> plain text`), "text/plain", false)
	if err != nil || d.Source != SourceUTF8 {
		t.Errorf("non-html: %v %+v", err, d)
	}
	// … and beyond the first 1,024 bytes.
	late := append(bytes.Repeat([]byte(" "), PrescanBytes), []byte(`<meta charset="shift_jis">ascii`)...)
	d, err = Decode(late, "text/html", true)
	if err != nil || d.Source != SourceUTF8 {
		t.Errorf("late meta: %v %+v", err, d)
	}
	// A body element ends the prescan.
	d, err = Decode([]byte(`<body><meta charset="shift_jis">ascii`), "text/html", true)
	if err != nil || d.Source != SourceUTF8 {
		t.Errorf("meta after body: %v %+v", err, d)
	}
	// A meta claiming UTF-16 means UTF-8 (WHATWG).
	d, err = Decode([]byte(`<meta charset="utf-16">ascii`), "text/html", true)
	if err != nil || d.Charset != "utf-8" || d.Source != SourceMeta {
		t.Errorf("meta utf-16: %v %+v", err, d)
	}
}

func TestCharsetFromContent(t *testing.T) {
	for in, want := range map[string]string{
		`text/html; charset=Shift_JIS`:     "Shift_JIS",
		`text/html; charset="euc-jp"; x=y`: "euc-jp",
		`text/html;charset=utf-8`:          "utf-8",
		`text/html; CHARSET=Shift_JIS`:     "Shift_JIS",
		`text/html`:                        "",
		// The attribute is the page's undecoded bytes. Invalid UTF-8 before
		// the needle used to panic (strings.ToLower rewrote each byte as
		// three), and runes whose lower case is shorter — KELVIN SIGN
		// (U+212A, 3 bytes) lowers to k (1 byte) — cut the label in the
		// wrong place. Code-audit run-2, 2026-11-08.
		"\xff\xff\xff\xff\xffcharset=utf-8":                       "utf-8",
		"\u212a\u212a\u212a\u212a charset=euc-jp":                 "euc-jp",
		"\u0130\u0130\u0130\u0130\u0130\u0130; charset=shift_jis": "shift_jis",
		"\xff\xff\xff\xff\xffcharset":                             "",
		"\xffCHARSET=\xff":                                        "\xff",
	} {
		if got := charsetFromContent(in); got != want {
			t.Errorf("charsetFromContent(%q) = %q, want %q", in, got, want)
		}
	}
}

// The same bytes through Decode: the <meta http-equiv> prescan on a body with
// no BOM and no header charset is where a page reaches charsetFromContent.
func TestMetaContentWithInvalidBytesDoesNotPanic(t *testing.T) {
	body := "<html><head><meta http-equiv=\"content-type\" content=\"\xff\xff\xff\xff\xffcharset=utf-8\"></head><body>hi</body></html>"
	d, err := Decode([]byte(body), "text/html", true)
	if err != nil || d.Charset != "utf-8" || d.Source != SourceMeta {
		t.Errorf("got %v %+v", err, d)
	}
	if !strings.Contains(d.Text, "hi") {
		t.Errorf("text lost: %q", d.Text)
	}
}

func TestIndexASCIIFold(t *testing.T) {
	for _, c := range []struct {
		s, needle string
		want      int
	}{
		{"", "x", -1}, {"x", "", 0}, {"abc", "B", -1}, {"aBc", "b", 1}, {"aBc", "bc", 1},
		{"ABCDEF", "cde", 2}, {"a\xffB", "\xffb", 1}, {"charsetcharset=", "charset=", 7},
	} {
		if got := indexASCIIFold(c.s, c.needle); got != c.want {
			t.Errorf("indexASCIIFold(%q, %q) = %d, want %d", c.s, c.needle, got, c.want)
		}
	}
}
