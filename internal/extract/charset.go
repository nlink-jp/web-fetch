package extract

import (
	"bytes"
	"mime"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
	"golang.org/x/text/encoding"

	"github.com/nlink-jp/web-fetch/internal/toolerr"
)

// Decoded is a body converted to UTF-8 and how its charset was decided.
type Decoded struct {
	Text    string
	Charset string // the canonical WHATWG name, e.g. "shift_jis", "utf-8"
	Source  string // one of the Source* constants
}

// Where the charset came from (RFP §2 charset_source).
const (
	SourceBOM      = "bom"      // a byte-order mark
	SourceHeader   = "header"   // the Content-Type charset parameter
	SourceMeta     = "meta"     // a <meta charset> or http-equiv in the first 1,024 bytes
	SourceUTF8     = "utf8"     // no declaration; the bytes are valid UTF-8
	SourceFallback = "fallback" // no declaration, not UTF-8; the WHATWG default
)

// PrescanBytes is how far into the body the <meta> prescan looks, per WHATWG.
const PrescanBytes = 1024

const fallbackLabel = "windows-1252"

// Decode converts body to UTF-8. contentType is the response header (may be
// empty); isHTML enables the <meta> prescan.
func Decode(body []byte, contentType string, isHTML bool) (Decoded, error) {
	if enc, name, rest := bom(body); enc != nil {
		return decodeWith(enc, name, SourceBOM, rest)
	}
	if label := headerCharset(contentType); label != "" {
		enc, name, err := lookup(label, "Content-Type header")
		if err != nil {
			return Decoded{}, err
		}
		return decodeWith(enc, name, SourceHeader, body)
	}
	if isHTML {
		if label := prescan(body); label != "" {
			enc, name, err := lookup(label, "<meta> element")
			if err != nil {
				return Decoded{}, err
			}
			// WHATWG: a document cannot declare itself UTF-16 in a byte-wide
			// meta element; such a label means UTF-8.
			if strings.HasPrefix(name, "utf-16") {
				enc, name = charset.Lookup("utf-8")
			}
			return decodeWith(enc, name, SourceMeta, body)
		}
	}
	if utf8.Valid(body) {
		return Decoded{Text: string(body), Charset: "utf-8", Source: SourceUTF8}, nil
	}
	enc, name := charset.Lookup(fallbackLabel)
	return decodeWith(enc, name, SourceFallback, body)
}

func decodeWith(enc encoding.Encoding, name, source string, body []byte) (Decoded, error) {
	out, err := enc.NewDecoder().Bytes(body)
	if err != nil {
		return Decoded{}, toolerr.Newf(toolerr.CodeCharsetUnsupported, "the body could not be decoded as %s: %v", name, err).
			With("charset", name)
	}
	return Decoded{Text: string(out), Charset: name, Source: source}, nil
}

// bom recognises a UTF-8 or UTF-16 byte-order mark and returns the encoding
// and the body without the mark.
func bom(body []byte) (encoding.Encoding, string, []byte) {
	switch {
	case bytes.HasPrefix(body, []byte{0xEF, 0xBB, 0xBF}):
		e, n := charset.Lookup("utf-8")
		return e, n, body[3:]
	case bytes.HasPrefix(body, []byte{0xFE, 0xFF}):
		e, n := charset.Lookup("utf-16be")
		return e, n, body[2:]
	case bytes.HasPrefix(body, []byte{0xFF, 0xFE}):
		e, n := charset.Lookup("utf-16le")
		return e, n, body[2:]
	}
	return nil, "", body
}

// headerCharset returns the charset parameter of a Content-Type value, or "".
func headerCharset(contentType string) string {
	if contentType == "" {
		return ""
	}
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(params["charset"])
}

// lookup maps a declared label to an encoding; an unknown label is the one
// case that is an error rather than a fallback, because the page said
// something the tool cannot honour.
func lookup(label, where string) (encoding.Encoding, string, error) {
	enc, name := charset.Lookup(label)
	if enc == nil {
		return nil, "", toolerr.Newf(toolerr.CodeCharsetUnsupported,
			"the page declares charset %q in its %s, which web-fetch cannot decode", label, where).
			With("charset", label)
	}
	return enc, name, nil
}

// prescan looks for a charset declaration in the first PrescanBytes of an HTML
// body: <meta charset="..."> or <meta http-equiv="content-type"
// content="...; charset=...">. It stops at <body> or at the end of the window.
func prescan(body []byte) string {
	if len(body) > PrescanBytes {
		body = body[:PrescanBytes]
	}
	z := html.NewTokenizer(bytes.NewReader(body))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return ""
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			switch string(name) {
			case "body":
				return ""
			case "meta":
				if !hasAttr {
					continue
				}
				var charsetAttr, httpEquiv, content string
				for {
					k, v, more := z.TagAttr()
					switch strings.ToLower(string(k)) {
					case "charset":
						charsetAttr = string(v)
					case "http-equiv":
						httpEquiv = string(v)
					case "content":
						content = string(v)
					}
					if !more {
						break
					}
				}
				if l := strings.TrimSpace(charsetAttr); l != "" {
					return l
				}
				if strings.EqualFold(strings.TrimSpace(httpEquiv), "content-type") {
					if l := charsetFromContent(content); l != "" {
						return l
					}
				}
			}
		}
	}
}

// charsetFromContent pulls the charset out of a content="text/html;
// charset=..." attribute value. The attribute is the page's own bytes,
// undecoded, so the case-insensitive search folds ASCII only: strings.ToLower
// rewrites an invalid byte as U+FFFD (three bytes) and shortens some runes,
// so an index found in the lowered copy does not hold in the original — a
// page could put it past the end and panic the process (code-audit run-2).
func charsetFromContent(content string) string {
	i := indexASCIIFold(content, "charset=")
	if i < 0 {
		return ""
	}
	rest := strings.TrimSpace(content[i+len("charset="):])
	rest = strings.Trim(rest, `"'`)
	if j := strings.IndexAny(rest, `;" '`); j >= 0 {
		rest = rest[:j]
	}
	return strings.TrimSpace(rest)
}

// indexASCIIFold returns the byte offset of the first match of needle in s,
// comparing ASCII letters without regard to case and every other byte as is,
// or -1. needle must be lower-case ASCII. Offsets are into s itself.
func indexASCIIFold(s, needle string) int {
	for i := 0; i+len(needle) <= len(s); i++ {
		ok := true
		for k := 0; k < len(needle); k++ {
			c := s[i+k]
			if 'A' <= c && c <= 'Z' {
				c += 'a' - 'A'
			}
			if c != needle[k] {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}
