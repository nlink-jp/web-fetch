package app

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
)

// The CLI's text output is a page's own words printed where a terminal
// interprets them. A page can carry escape sequences in its <title> or text
// (screen clears, cursor moves, OSC window titles and hyperlinks, clipboard
// writes where the terminal allows them) and bidi overrides that reorder
// what the operator sees. When stdout is a terminal, everything the page
// authored goes through one function that makes it inert; to a pipe or a
// file the text stays verbatim, so `web-fetch --format raw URL > file`
// keeps every byte.

// isTerminal reports whether w is an interactive terminal: a character
// device, which is what os.Stdout is on a tty and what a pipe or file is not.
func isTerminal(w io.Writer) bool {
	f, ok := w.(interface{ Stat() (os.FileInfo, error) })
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// controlRune reports whether r is one a terminal would act on rather than
// show: C0 except newline and tab, DEL, C1 (U+0080–U+009F, which many
// terminals read as 7-bit escapes), and the bidi overrides and isolates
// (U+202A–U+202E, U+2066–U+2069).
func controlRune(r rune) bool {
	switch {
	case r == '\n' || r == '\t':
		return false
	case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f:
		return true
	case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069:
		return true
	}
	return false
}

// inert replaces every control rune in s with U+FFFD.
func inert(s string) string {
	return strings.Map(func(r rune) rune {
		if controlRune(r) {
			return '�'
		}
		return r
	}, s)
}

// inertJSON rewrites encoder output so every control rune inside a string
// is a \uXXXX escape. encoding/json already escapes bytes below 0x20; DEL,
// C1 and the bidi controls it writes raw. The runes rewritten here cannot
// occur outside a JSON string, so the document stays valid.
func inertJSON(b []byte) []byte {
	if !bytes.ContainsFunc(b, controlRune) {
		return b
	}
	var out bytes.Buffer
	out.Grow(len(b) + 64)
	for _, r := range string(b) {
		if controlRune(r) {
			fmt.Fprintf(&out, `\u%04x`, r)
			continue
		}
		out.WriteRune(r)
	}
	return out.Bytes()
}
