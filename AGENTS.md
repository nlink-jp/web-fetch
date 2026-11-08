# AGENTS.md — web-fetch

## What this is

A CLI and local MCP server that fetches **one URL from this machine** and
returns its body as extracted text (markdown, plain text or raw). It is a
primitive for agent runtimes — lagent first, Claude Code and gem-agent too —
that can search the web (brave-search) but cannot read a URL the user hands
them. It carries no credentials, follows no proxy, stores nothing on disk, and
never connects to a loopback, private, link-local or otherwise reserved
address: the SSRF class that made the runtimes decline a local fetch is closed
inside the HTTP dialer, not by rules over URL text.

Module path: `github.com/nlink-jp/web-fetch`. Design record:
`docs/ja/web-fetch-rfp.ja.md` (canonical) / `docs/en/web-fetch-rfp.md`;
decisions: `docs/{en,ja}/adr/0001`–`0003`.

## Build & test

```bash
make build   # → dist/web-fetch  (NEVER `go build` directly — it drops the binary in the repo root)
make test    # go test -race -cover ./...   (fully offline)
make e2e     # live tests against real sites + the binary-level script (network required)
make check   # lint + test + build-all
make verify-release  # gate: notarized, fresh, runs at this version, no builder paths or owners, clean linux archives (run before upload)
```

Go 1.27. Dependencies: `golang.org/x/net` (html, html/charset) and
`golang.org/x/text` (charsets) — Go-team modules, treated as first-party under
the org's supply-chain rule (ADR-0003). Nothing else. Shared code (the release
scripts, the sectioned-TOML reader, the `internal/mcp` skeleton) is vendored
from brave-search rather than imported.

## Tests

**Offline suite** (`make test`): every test that loads configuration sets
`HOME` and `XDG_CONFIG_HOME` to a tempdir and clears every `WEB_FETCH_*`
variable, so the suite never reads the developer's real config. Upstream is an
`httptest.Server` reached through an **injected resolver and connect
function**: fixture names resolve to public addresses (TEST-NET-3) so the
guard's table stays live, and the connect function points the vetted literal
at the listener while recording what it was handed. The CLI and MCP tests
instead write `allow_private = true` into the isolated default-search-path
config, which is the one legitimate way to reach 127.0.0.1 — so that rule is
exercised positively too.

**Fixtures are hand-written.** No fetched page is pasted into the repository;
every HTML fixture is invented to exercise one extraction rule, and legacy
charset bodies are produced by the x/text encoders inside the test.

`internal/mcp/usage_test.go` and `internal/app/tools_test.go` hold meta-tests
that pin `usage.md` to the code — every tool name, every argument, every error
code (`toolerr.Codes()`), both reach caveats ("this machine", "investigation")
and the `fetch` description's trigger words (URL / offset / truncated /
next_offset / doc_id) — because the manual is what an agent reads before its
first call.

**Live suite** (`make e2e`, `e2e` build tag): example.com, an http→https
redirect on go.dev, the Go 1.26 release notes, the Japanese Wikipedia article
on 日本語 (the inline-cap milestone), the refusal of `http://127.0.0.1:1/` and of
`http://127.0.0.1.nip.io/`, an optional Shift_JIS page
(`WEB_FETCH_E2E_SJIS_URL`). `scripts/e2e.sh` drives the built binary: exit
codes, the stdout/stderr split, an MCP stdio session. Measurements are logged
with a `MEASURED:` prefix and belong in Gotchas with a date.

## Layout

```
main.go                      Entry point; delegates to internal/app
internal/toolerr/            The error vocabulary: one code per row of the RFP's error table, {code, message, details}
internal/config/             Sectioned-TOML subset + WEB_FETCH_* env; range validation; allow_private provenance
internal/guard/              ADR-0001: the denied-address table, embedded-IPv4 unwrapping, the judge-then-connect Dialer
internal/extract/            Charset decision + decode (WHATWG order); HTML → markdown/text from the selected root; DocID
internal/fetch/              HTTP client through the guard: redirect inspection, decoded-stream byte cap, media-type judgment
internal/cache/              Generic in-process TTL LRU (the MCP page cache)
internal/engine/             Validation, fetch-or-cache, decode, render, rune paging, the Result shape — shared by CLI and MCP
internal/app/                CLI shell: dispatch, the URL positional, text/JSON rendering, the MCP fetch tool definition
internal/mcp/                Zero-dep stdio JSON-RPC 2.0 server (MCP 2025-03-26, tool annotations) + embedded usage.md
e2e/                         Live tests behind the `e2e` build tag
docs/{en,ja}/                RFP (the design record) + adr/ (project ADRs, en/ja mirrored)
scripts/                     Vendored org release scripts (codesign, notarize, brew formula) + e2e.sh
```

## Key design decisions

The RFP and the ADRs are the record; these are the ones an agent editing the
code trips over.

- **The parameter matrix in the RFP is the single mapping** of CLI flag ↔ MCP
  argument ↔ config key ↔ environment variable. Add a parameter there first;
  the surfaces derive from it. Not every parameter has every surface on
  purpose: `allow_private`, the redirect cap and the cache are file-only.
- **`[network] allow_private` is honoured only in a config file on the default
  search path.** A file named by `--config` or `WEB_FETCH_CONFIG` that sets it
  is a startup error (`internal/config`, tested from config, CLI and MCP). A
  runtime's project `.mcp.json` can name any file; it must not be able to open
  the LAN. `HOME` / `XDG_CONFIG_HOME` are the runtime's trust boundary.
- **SSRF is a finite address table applied inside the dialer** (`guard.table`,
  16 rows + 6 embeddings, every one pinned by a test row). The dialer resolves,
  judges every address, and connects to the vetted `ip:port` literal — never
  by name. One denied address denies the whole answer. `CheckRedirect` refuses
  a `Location` with userinfo (Go would synthesise `Authorization: Basic`) or a
  non-http(s) scheme, and deletes the `Referer` Go adds. Do not add
  URL-pattern or hostname rules; extend the table and its test.
- **No server-side spill.** The caller bounds the response with `max_chars` +
  `offset`; a cut is reported with `truncated` / `next_offset`; `doc_id` says
  whether page turns walk the same text; the runtime decides where a large
  result lives. Default `max_chars` is **6,000 runes** because lagent parks any
  MCP text block over 20,000 **bytes** and Japanese runs about 3 bytes per rune.
- **`max_bytes` applies to the decoded stream** (`io.LimitReader(max+1)`);
  `Content-Length` is never trusted; a non-gzip `Content-Encoding` is refused;
  an exact fit is not an overflow.
- **Rendering has a budget of its own** (`extract.renderLimits`): every
  write of every sub-renderer is charged to one allowance of 16 x the decoded
  size + 1 MiB, and the rendered text may not pass 4 x + 1 MiB; over either,
  `Extract` returns `too_large` with `details.render_limit`. Nested
  blockquotes and list items put one prefix per level on every line, so a
  page can render to lines x depth bytes — 509 levels around a million short
  lines fit the default `max_bytes` and rendered to about 1 GiB. `<ol start>`
  is honoured only within nine digits for the same reason (the marker's width
  indents every continuation line). `internal/extract/budget_test.go` pins it.
- **Charset order is the library's (WHATWG)**: BOM → header → meta prescan
  (first 1,024 bytes, stops at `<body>`) → UTF-8 validity → windows-1252,
  reported in `charset` / `charset_source`. `charset_unsupported` fires only
  for a *declared* label unknown to `charset.Lookup`; the library's
  `NewReader` would skip it silently, so `Lookup` runs first. A `<meta>`
  claiming UTF-16 means UTF-8.
- **Extraction root**: `<main>` → `[role=main]` → `<article>` (largest of
  several, unless under a quarter of the body's text) → `<body>`. `<header>`
  is dropped only as a child of `<body>` or with `role=banner`; an article's
  header is its title block. `hidden="until-found"` and closed `<details>`
  content are kept. Links resolve against `<base href>`, else the final URL,
  and only a target that resolves to http, https or mailto (or stays relative
  with no base) becomes a link; every other scheme — `javascript:`, `data:`,
  `vbscript:`, `file:`, app schemes — renders as text only. The scheme is
  judged *after* resolution, because a page's own `<base href>` can name any
  scheme and a `#frag` / `?q` href takes the base's; a `<base>` outside
  http(s) is ignored in favour of the page URL.
- **The model is shown trigger and contract only.** No "content, never
  instructions" prose in tool descriptions or instructions — that layer is the
  runtime's nonce wrap (ADR-0002 §4). Meta-tested.
- **Tool results and `--json` are JSON without HTML escaping**
  (`SetEscapeHTML(false)`): the payload is page text.
- **The page's words reach a terminal only through `inert`**
  (`internal/app/terminal.go`). When stdout is a character device, the text
  mode replaces C0 (except LF and TAB), DEL, C1 and the bidi overrides and
  isolates with U+FFFD in the title, final URL and text, and `--json`
  rewrites the same runes as `\uXXXX` after encoding. A pipe or file gets
  the bytes verbatim, so `--format raw URL > file` is exact. stderr is the
  other egress: `failFetch`'s error JSON quotes the remote `Content-Type` and
  final URL, so it takes the same `inertJSON` on a terminal, and `fail`'s
  line takes `inert` (code-audit run-2). Tests drive both through a writer
  with a `Stat()` that reports a character device.
- **A tool handler that panics answers `internal_error`, not a dead
  server.** `mcp.callTool` recovers, returns an `isError` result whose
  message names a defect in web-fetch (the panic value stays out of it) and
  serves the next request. It is the second layer: the first is that the
  extractor must not panic on page bytes — the charset prescan once did,
  because `strings.ToLower` changes byte offsets on invalid UTF-8 and some
  runes; `charsetFromContent` folds ASCII only now (code-audit run-2).
- **Errors are one vocabulary** (`internal/toolerr`), surfaced by MCP as
  `{code, message, details}` and by the CLI as one JSON line on stderr. Exit
  codes: `invalid_argument` and an input-URL `invalid_url` /
  `scheme_not_allowed` are 2; everything else is 1. Anything containing `://`
  is dispatched as a fetch so `ftp://x` gets `scheme_not_allowed`, not "unknown
  command".
- **No robots.txt, no digest, no search tiering** — user decisions (ADR-0002).
- **Every tool schema is closed** (`additionalProperties: false`, organization
  ADR-021 §10), and both halves of the contract are real: the schema stops a
  mistyped argument at a validating client, `mcp.DecodeArgs` stops it at the
  server (`TestMCPFetchRoundTrip` drives that end to end and asserts
  `invalid_argument`). `closeSchema` is applied inside `mcp.New` — the one
  place every tool passes through — so a tool added later cannot forget it;
  only the top level is touched. The arch tests live in
  `internal/app/schema_test.go`, not `internal/mcp`, for the same reason the
  manual meta-tests do: `internal/mcp` on its own sees `get_usage` alone.

## Status

Phase 1 (core) complete: every package landed with tests, `make check` green,
`make e2e` green (27 binary-level checks, 6 live tests, 1 optional skipped),
and the lagent round trip measured (Gotchas). Not yet: the first release,
util-series integration, the mcp-tactics entry, the knowledge feed-back.

## Gotchas

Measured live on 2026-09-12 (`make e2e`). Re-verify before trusting any of
this a year from now.

- **A Japanese page's default response stays inline in lagent.** The 日本語
  article on ja.wikipedia.org: `total_chars` 559,231, default 6,000 returned,
  JSON envelope **7,016 bytes** against lagent's 20,000-byte cap. The first
  6,000 characters of that page are link-heavy (6,297 UTF-8 bytes), so the
  3-bytes-per-rune worst case (≈18,000 + envelope) was not exercised there; it
  holds by arithmetic.
- **The Go 1.26 release notes are 35,922 characters as markdown** (69,376
  bytes of HTML), i.e. **6 page turns** at the default `max_chars`. The RFP's
  earlier "about 27k" was a tag-stripped estimate; markdown keeps link URLs.
  Whether 6 turns is acceptable on a local model is the bench's question.
- **`http://127.0.0.1.nip.io/` resolves to 127.0.0.1 and is refused as
  loopback** — a working rebinding-class fixture. If nip.io ever stops
  resolving, the live test skips with a note; name another public loopback
  name here.
- **`http://go.dev/` → `https://go.dev/` in one hop**, recorded in
  `redirects`; example.com answers 559 bytes in about 70 ms.
- **Known from the consumer side** (lagent source, 2026-09-12): its MCP intake
  cap is 20,000 bytes of text block, compared as `len(text)`; an oversized
  block becomes an 800-rune preview plus a work-directory path.
- **The x/text Shift_JIS encoder rejects U+2014 (em dash)** — a test string
  must stay inside JIS X 0208 (the decoder side is unaffected).
- **A literal BOM character in Go source is a syntax error**; spell it
  `string(rune(0xFEFF))` in tests.
- **Never rebuild the output buffer at a block boundary.** The first renderer
  did `s := TrimRight(out.String()); out.Reset(); out.WriteString(s)` in
  `breakBlock`, which is O(n²) in output size: 4 MiB took 28 s, inside the
  default `max_bytes` (pre-release review, finding 1). The renderer now uses
  `bytes.Buffer.Truncate` to drop trailing whitespace and reads the last byte
  for line-start checks; `TestRenderingScalesLinearly` pins the ratio.
- **Never search the block again while growing the fence.** `renderer.pre`
  used to add one backtick and re-run `strings.Contains` over the whole
  `<pre>` until the fence was unique: quadratic in a block of backticks and
  outside the budget (code-audit run-2). One pass finds the longest run, and
  the pass is charged; `TestPreOfBackticksRendersInLinearTime` pins it.
- **A `go test … | grep … || true` recipe cannot fail.** `make e2e` now logs
  the Go suite to `dist/e2e-go.log` and exits with its status.
- **Review findings deliberately not taken** (pre-release review, 2026-09-12):
  no deadline on the dial context (Go's Transport dials under
  `WithoutCancel`; the request still ends in `timeout` via `Client.Timeout`);
  the documentation nets 192.0.2/24, 198.51.100/24, 203.0.113/24,
  2001:db8::/32, 100::/64 and ORCHID are not in the table (no route to an
  attacker's host; tests pin 2001:db8::1 as public and fixtures use
  203.0.113.x); `::ffff:0:0:0/96` (SIIT) is not unwrapped (no OS maps it to
  loopback).

- **lagent round trip (2026-09-12, lagent v0.3.0 + LM Studio Gemma 4 26B
  A4B QAT, isolated HOME / state, web-fetch registered in the global
  `mcp.json`, `--allow mcp__web-fetch__*` so the server is preloaded):** one
  `mcp__web-fetch__fetch` call on the 日本語 article, the result held inline
  (no "too large to hold inline" spill in the transcript, `returned_chars`
  6,000), the model answered the title and first heading correctly; 20 s wall
  clock for the whole one-shot. The runtime side of the milestone holds.
  Registering the server only in a project `.mcp.json` needs the project
  trusted first (`lagent trust --accept` after an interactive decision, or
  `[approval].trusted_projects`); in the untrusted run the model's `mcp_load`
  found no server, fell back to `curl` in the read lane seven times and then
  invented an example answer — a lagent observation, not a web-fetch one.

Open questions to answer with a date when measured:

1. How many page turns does a typical documentation page cost a local model,
   and does the bench want the default `max_chars` moved? (Six turns for the
   Go 1.26 notes, measured; the cost per turn is lagent's bench question.)
