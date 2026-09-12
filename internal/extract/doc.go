// Package extract turns a fetched body into the text web-fetch returns: it
// decides the charset and decodes to UTF-8 (Decode), and renders HTML as
// markdown or plain text from a chosen body root (Extract). Both are pure:
// bytes in, strings out, no I/O.
//
// Charset order is the WHATWG order the adopted library implements — BOM,
// Content-Type header, <meta> prescan of the first 1,024 bytes, UTF-8
// validity, windows-1252 — and the outcome is reported as Decoded.Source.
// charset_unsupported fires only for a declared label the decoder table does
// not know; the library's own NewReader would skip such a label silently, so
// Lookup runs first (RFP §2, ADR-0003).
//
// Extraction is deterministic and uses no model. The body root is the first
// present of <main>, [role=main], the largest <article> (unless it is under a
// quarter of the body's text), <body>. Navigation, footers, asides, forms,
// scripts, styles, hidden elements and body-level headers are dropped; an
// <article>'s own <header> is kept because it holds the title. Relative URLs
// resolve against <base href>, else the page URL. The rules are heuristics;
// the escape hatch when they miss is the raw format, chosen by the caller.
package extract
