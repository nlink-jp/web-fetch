# ADR-0003: golang.org/x/net and golang.org/x/text are first-party dependencies

| Field | Value |
|-------|-------|
| Status | **Accepted** |
| Date | 2026-09-12 |
| Binds | web-fetch |
| Decision makers | nlink-jp maintainers |
| Triggered by | The standard library has no HTML tokenizer and no legacy charset decoders; the organization's supply-chain rule admits stdlib, vendor first-party SDKs and direct REST, and refuses community tooling — `golang.org/x/` has to be placed |

## Context

Extracting a page's text needs an HTML tokenizer and parser; reading a Japanese
page needs Shift_JIS, EUC-JP and ISO-2022-JP decoders. Neither is in the Go
standard library. The archived agentic-web-search avoided the dependency with
regular expressions over the markup and an 8,000-byte cut; the result was crude
enough that the RFP names it as the thing not to repeat.

The organization's rule (stdlib → vendor first-party → direct REST; never
community tooling, re-implement instead) was written for API clients. It does
not name `golang.org/x/`, the Go team's own repositories, which sit between the
standard library and the ecosystem: same authors and review process as the
standard library, released outside its compatibility promise. Most Go
repositories in the organization already carry `x/net` and `x/text`
transitively through Google's SDKs; mail-analyzer, mail-analyzer-local and
lite-rag require `x/text` directly. No repository imports `x/net/html` or
`x/text/encoding` directly yet.

## Decision

1. `golang.org/x/net` (`html`, `html/atom`, `html/charset`) and
   `golang.org/x/text` (`encoding`, `encoding/htmlindex` and the decoders it
   pulls) are **first-party dependencies** for web-fetch, under the same
   standing as a vendor SDK in the supply-chain rule: written and maintained by
   the Go team, distributed from the Go module mirror with checksum database
   verification, pinned in `go.sum`.
2. These are the **only** non-standard-library dependencies. No HTML-to-markdown
   converter, no readability port, no charset detector from the ecosystem; the
   extraction rules are written here (`internal/extract`) against the parser's
   DOM.
3. Versions are pinned and bumped deliberately (`go get golang.org/x/net@latest`
   in its own commit), with `govulncheck` in the release routine. A bump that
   changes `charset.DetermineEncoding`'s order or `html.Parse`'s tree shape is
   caught by the extract fixtures.

## Consequences

- `go.mod` gains two `require` lines, and `go.sum` their checksums. The
  brave-search "no `require` block" statement does not carry over; AGENTS.md and
  CLAUDE.md name the two modules and the reason.
- The charset order web-fetch reports (`charset_source`) is the library's
  WHATWG order (BOM → header → meta → UTF-8 validity → windows-1252); the RFP
  was corrected to match rather than the other way round.
- Any future request for a third dependency starts by asking whether it is
  standard library, `golang.org/x/`, or something to write here.
- The classification is recorded so the next tool needing `x/` does not
  re-argue it — but it binds web-fetch only; the organization rule is unchanged.

## Alternatives considered

- **Regular expressions over the markup** (the archived code) — rejected:
  unsound on real pages (attributes containing `>`, scripts containing markup,
  entities), and it cannot select a body root.
- **A community HTML-to-markdown library** — rejected by the supply-chain rule.
- **A hand-written tokenizer** — rejected: re-implementing `x/net/html`'s
  WHATWG-conformant tokenizer is weeks of work to reach lower quality than the
  Go team's.
- **UTF-8 only, legacy charsets in Phase 2** — rejected by the operator:
  Japanese sites still serve Shift_JIS, and the decoder is the same `x/text`
  module the parser's charset helper already needs.

## References

- RFP §2 "External Dependencies", §3 "なぜ Go / stdlib + golang.org/x か"
- Organization rule: dependency choice stdlib → first-party SDK → REST; no
  community tooling (recorded in the maintainers' feedback log, 2026-09-05)
- nlink-jp/knowledge `build-and-packaging.md` on pinning and checksum
  verification of fetched artifacts
