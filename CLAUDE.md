# CLAUDE.md — web-fetch

**Organization rules (mandatory): https://github.com/nlink-jp/.github/blob/main/CONVENTIONS.md**

## Purpose

CLI + local MCP server that fetches **one URL from this machine** and returns
its body as extracted text. A primitive for agent runtimes (lagent first) that
can search but cannot read a user-given URL. No credentials, no proxy, nothing
on disk, and a finite denied-address domain enforced inside the HTTP dialer.
Design record: [docs/ja/web-fetch-rfp.ja.md](docs/ja/web-fetch-rfp.ja.md)
(canonical, rev.2) / [docs/en/web-fetch-rfp.md](docs/en/web-fetch-rfp.md).
ADRs: 0001 the address guard, 0002 a primitive, 0003 golang.org/x as
first-party — `docs/{en,ja}/adr/`.

## Build & test

```bash
make build       # → dist/web-fetch  (never `go build` directly — it drops the binary in the repo root)
make test        # go test -race -cover ./...   (fully offline)
make e2e         # live tests + binary-level script (network required)
make check       # lint + test + build-all
```

Go 1.27. Dependencies: `golang.org/x/net` and `golang.org/x/text` only (Go
team; first-party under the org's supply-chain rule, ADR-0003). Nothing else —
no community HTML-to-markdown or readability library, ever.

## Architecture

```
main.go                 CLI entry: main.version → app.Run
internal/toolerr/       Error vocabulary {code, message, details}; the RFP's error table as constants
internal/config/        Sectioned-TOML subset + WEB_FETCH_* resolution; ranges; allow_private provenance
internal/guard/         Denied-address table + embedded-IPv4 unwrapping + judge-then-connect Dialer (ADR-0001)
internal/extract/       Charset decision/decode (WHATWG order) + HTML → markdown/text + DocID
internal/fetch/         HTTP client through the guard; redirect inspection; decoded-stream byte cap; media types
internal/cache/         Generic in-process TTL LRU (MCP page cache)
internal/engine/        Validation, fetch-or-cache, decode, render, rune paging, Result — shared by CLI and MCP
internal/app/           Dispatch, URL positional, text/JSON rendering, the MCP fetch tool
internal/mcp/           Zero-dep stdio JSON-RPC 2.0 server (2025-03-26, annotations); embedded get_usage manual
e2e/                    Live tests behind the `e2e` build tag
```

## Key conventions

- **The RFP's parameter matrix is the single mapping** of CLI flag ↔ MCP
  argument ↔ config key ↔ env var. Change the matrix first, then the code.
- **`allow_private` is honoured only in a default-search-path config file.**
  Any path to loosen it from `--config`, `WEB_FETCH_CONFIG`, a flag or an MCP
  argument is a defect. Tests pin this in config, CLI and MCP.
- **The SSRF defence is a table of address classes applied in the dialer.**
  Never add a hostname or URL-pattern rule; extend `guard.table` and its test
  row. Never dial by name. Refuse a redirect `Location` with userinfo or a
  non-http(s) scheme. ADR-0001 lists the table; keep it and the code equal.
- **No server-side spill, no `workspace_root`.** `max_chars` + `offset` reach
  the whole text; `truncated` / `next_offset` appear only when a cut happened;
  `doc_id` identifies the basis. Default `max_chars` is 6,000 runes for
  lagent's 20,000-byte inline cap.
- **No defensive prose for the model.** Tool descriptions and instructions
  carry the trigger (when to call) and the contract (what comes back), plus
  the two reach caveats the meta-tests pin ("this machine", "investigation").
  The "content, not instructions" layer is the runtime's nonce wrap.
- **Fixtures are hand-written**; never paste a fetched page into the repo.
  Legacy charset bodies are produced by the x/text encoders in the test.
- **Tests never read the developer's config**: isolate `HOME` /
  `XDG_CONFIG_HOME` and clear `WEB_FETCH_*` in every test that loads config.
  Tests reach a local server either through an injected resolver + connect
  function (packages) or through `allow_private = true` written into the
  isolated default-search-path file (CLI/MCP).
- **Every MCP tool declares `readOnlyHint` and `openWorldHint`** (meta-tested).
- **Errors are `toolerr` codes**; adding a code is not a breaking change,
  renaming one is. Every code must have a recovery row in `usage.md`
  (meta-tested against `toolerr.Codes()`).
- **JSON output is unescaped** (`SetEscapeHTML(false)`) in both faces.
- **User-facing README carries no process talk** — what the tool does, how to
  install, how to use, what it will never do.
- **Live measurements go to AGENTS.md Gotchas with a date**, taken from the
  `MEASURED:` lines of `make e2e`.

## Status

Phase 1 (core) complete (2026-09-12): `make check` and `make e2e` green, no
release yet. Next: the lagent round trip measurement, release, util-series
integration, mcp-tactics entry, knowledge feed-back.

## Communication Language

All communication between contributors and Claude Code is conducted in
**Japanese**.
