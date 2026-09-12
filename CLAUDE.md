# CLAUDE.md — web-fetch

**Organization rules (mandatory): https://github.com/nlink-jp/.github/blob/main/CONVENTIONS.md**

## Purpose

CLI + local MCP server that fetches **one URL from this machine** and returns
its body as extracted text. A primitive for agent runtimes (lagent first) that
can search but cannot read a user-given URL. No credentials, no proxy, nothing
on disk, and a finite denied-address domain enforced inside the HTTP dialer.
Design record: [docs/ja/web-fetch-rfp.ja.md](docs/ja/web-fetch-rfp.ja.md)
(canonical, rev.2) / [docs/en/web-fetch-rfp.md](docs/en/web-fetch-rfp.md).

## Build & test

```bash
make build       # → dist/web-fetch  (never `go build` directly — it drops the binary in the repo root)
make test        # go test -race -cover ./...   (fully offline)
make check       # lint + test + build-all
```

Go 1.27. Dependencies: `golang.org/x/net` and `golang.org/x/text` only (Go
team; first-party under the org's supply-chain rule). Nothing else — no
community HTML-to-markdown or readability library, ever.

## Architecture

```
main.go                 CLI entry: main.version → app.Run
internal/config/        Sectioned-TOML subset + WEB_FETCH_* resolution; ranges; allow_private provenance
internal/mcp/           Zero-dep stdio JSON-RPC 2.0 server (2025-03-26, annotations); embedded get_usage manual
internal/app/           Dispatch + mcp / version; the URL positional and text/JSON rendering land in Phase 1
```

Phase 1 adds `internal/guard`, `internal/extract`, `internal/fetch`,
`internal/engine`, `internal/cache`, `e2e/` — in that order, each a pure
package where the RFP says so.

## Key conventions

- **Write the ADR before the package it binds.** ADR-0001 (SSRF domain) before
  `internal/guard`; ADR-0002 (a primitive) before `internal/fetch`; ADR-0003
  (golang.org/x) before the first `go get`. Project ADRs live in
  `docs/{en,ja}/adr/NNNN-slug.md`, en/ja mirrored.
- **The RFP's parameter matrix is the single mapping** of CLI flag ↔ MCP
  argument ↔ config key ↔ env var. Change the matrix first, then the code.
- **`allow_private` is honoured only in a default-search-path config file.**
  Any path to loosen it from `--config`, `WEB_FETCH_CONFIG`, a flag or an MCP
  argument is a defect. The test that pins this is in `internal/config`.
- **The SSRF defence is a table of address classes applied in the dialer.**
  Never add a hostname or URL-pattern rule; extend the table and its test.
  Never dial by name. Refuse a redirect `Location` with userinfo.
- **No server-side spill, no `workspace_root`.** `max_chars` + `offset` reach
  the whole text; `truncated` / `next_offset` appear only when a cut happened.
  Default `max_chars` is 6,000 runes for lagent's 20,000-byte inline cap.
- **No defensive prose for the model.** Tool descriptions and instructions
  carry the trigger (when to call) and the contract (what comes back), plus
  the two reach caveats the meta-test pins ("this machine", "investigation").
  The "content, not instructions" layer is the runtime's nonce wrap.
- **Fixtures are hand-written**; never paste a fetched page into the repo.
- **Tests never read the developer's config**: isolate `HOME` /
  `XDG_CONFIG_HOME` and clear `WEB_FETCH_*` in every test that loads config.
- **Every MCP tool declares `readOnlyHint` and `openWorldHint`** (meta-tested).
- **Errors are `{code, message, details}`**; codes are the RFP's error table.
  Adding a code is not a breaking change; renaming one is.
- **User-facing README carries no process talk** — what the tool does, how to
  install, how to use, what it will never do.

## Status

Scaffold complete (2026-09-12), `make check` green, no release. Phase 1 (core)
has not started; nothing is measured live.

## Communication Language

All communication between contributors and Claude Code is conducted in
**Japanese**.
