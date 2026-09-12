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
`docs/ja/web-fetch-rfp.ja.md` (canonical) / `docs/en/web-fetch-rfp.md`.

## Build & test

```bash
make build   # → dist/web-fetch  (NEVER `go build` directly — it drops the binary in the repo root)
make test    # go test -race -cover ./...   (fully offline)
make check   # lint + test + build-all
make verify-release  # gate: .notarized marker + freshness (run before upload)
```

Go 1.27. Dependencies are limited to `golang.org/x/net` (html, html/charset)
and `golang.org/x/text` (charsets) — Go-team modules, treated as first-party
under the org's supply-chain rule; they arrive with the extract package. Shared
code (the release scripts, the sectioned-TOML reader, the `internal/mcp`
skeleton) is vendored from brave-search rather than imported.

## Tests

**Offline suite** (`make test`): every test sets `HOME` and `XDG_CONFIG_HOME`
to a tempdir and clears every `WEB_FETCH_*` variable, so the suite never reads
the developer's real config. The MCP layer is driven by an in-process JSON-RPC
session over strings. When the fetch pipeline lands, upstream is an
`httptest.Server` reached through an injected resolver — loopback is denied by
default, so a test cannot simply point at 127.0.0.1.

**Fixtures are hand-written.** No real page is pasted into the repository; an
HTML fixture is invented to exercise one rule of the extractor.

`internal/mcp/usage_test.go` holds meta-tests that pin `usage.md` to the code —
every tool name, every argument, every error code and both reach caveats ("this
machine", "investigation") must appear in the manual and in the initialize
instructions, because the manual is what an agent reads before its first call.

**Live suite** (`make e2e`, network required, `e2e` build tag) arrives with
Phase 1: one stable public page, the refusal of `http://127.0.0.1:1/` and of
`http://127.0.0.1.nip.io/` (public DNS answering loopback), an optional
Shift_JIS page. Measurements are logged with a `MEASURED:` prefix and belong in
Gotchas with a date.

## Layout

```
main.go                      Entry point; delegates to internal/app
internal/config/             Sectioned-TOML subset + WEB_FETCH_* env; range validation; allow_private provenance
internal/mcp/                Zero-dep stdio JSON-RPC 2.0 server (MCP 2025-03-26, tool annotations) + embedded usage.md
internal/app/                CLI shell: dispatch, flags, mcp and version commands
docs/{en,ja}/                RFP (the design record); project ADRs under adr/ as they are written
scripts/                     Vendored org release scripts (codesign, notarize, brew formula)
```

Phase 1 adds `internal/guard` (address classification + dialer),
`internal/extract` (HTML → text), `internal/fetch` (HTTP client),
`internal/engine` (defaults, paging, response shaping), `internal/cache`
(MCP in-process TTL cache) and `e2e/`.

## Key design decisions

The RFP is the record; these are the ones an agent editing the code trips over.

- **The parameter matrix in the RFP is the single mapping** of CLI flag ↔ MCP
  argument ↔ config key ↔ environment variable. Add a parameter there first;
  the surfaces derive from it. Not every parameter has every surface on
  purpose: `allow_private`, the redirect cap and the cache are file-only.
- **`[network] allow_private` is honoured only in a config file on the default
  search path.** A file named by `--config` or `WEB_FETCH_CONFIG` that sets it
  is a startup error (`internal/config`, tested). A runtime's project
  `.mcp.json` can name any file; it must not be able to open the LAN. `HOME` /
  `XDG_CONFIG_HOME` are the runtime's trust boundary, not ours.
- **SSRF is a finite address table applied inside the dialer**, with embedded
  IPv4 (mapped, compatible, NAT64, 6to4, Teredo) unwrapped before judgment,
  every redirect hop judged again, `Location` refused when it carries userinfo
  (Go would otherwise synthesise `Authorization: Basic`), and no dialing by
  name. Do not add URL-pattern or hostname rules; extend the table.
- **No server-side spill.** The caller bounds the response with `max_chars` +
  `offset`; a cut is reported with `truncated` / `next_offset`; the runtime
  decides where a large result lives. Default `max_chars` is **6,000 runes**
  because lagent parks any MCP text block over 20,000 **bytes** and Japanese
  runs about 3 bytes per rune.
- **`max_bytes` applies to the decoded stream**; `Content-Length` is never
  trusted; a non-gzip `Content-Encoding` is refused.
- **Charset order is the library's (WHATWG)**: BOM → header → meta → UTF-8
  validity → windows-1252, reported in `charset` / `charset_source`.
  `charset_unsupported` fires only for a *declared* label unknown to
  `htmlindex`.
- **The model is shown trigger and contract only.** No "content, never
  instructions" prose in tool descriptions or instructions — that layer is the
  runtime's nonce wrap. The instructions must keep "this machine" and
  "investigation" (meta-tested).
- **Tool results are JSON without HTML escaping** (`SetEscapeHTML(false)`):
  the payload is page text, and `<` for every `<` inflates it for no one.
- **No robots.txt, no digest, no search tiering** — user decisions; a primitive.

## Status

Scaffold (Phase 2 of the org process) complete; `make check` green. Next:
project ADRs 0001 (SSRF domain), 0002 (a primitive), 0003 (golang.org/x as
first-party) — written **before** the packages they bind — then Phase 1 in the
RFP's order: guard → extract → fetch → engine → app → mcp fetch tool → cache →
e2e. Nothing has been measured live yet.

## Gotchas

None measured yet. Known from the consumer side (lagent source, 2026-09-12):
its MCP intake cap is 20,000 bytes of text block, compared as `len(text)`, and
an oversized block becomes an 800-rune preview plus a work-directory path — the
reason the default `max_chars` is 6,000 and the Phase 1 milestone includes "a
default fetch of a Japanese page renders inline in lagent".

Open questions to answer with a date when measured:

1. Does the default response of a Japanese page stay inline in lagent (JSON
   envelope included)?
2. How many page turns does a typical documentation page cost a local model at
   `max_chars = 6000`, and does the bench want the default moved?
3. Does `127.0.0.1.nip.io` keep resolving to loopback (the e2e rebinding
   fixture)? If not, name another.
