# ADR-0002: web-fetch is a primitive — no robots.txt, no digest, no search tiering, no defensive prose

| Field | Value |
|-------|-------|
| Status | **Accepted** |
| Date | 2026-09-12 |
| Binds | web-fetch |
| Decision makers | nlink-jp maintainers (user decisions of 2026-09-12 recorded in the RFP's Discussion Log) |
| Triggered by | Each of the four features below was proposed during the RFP and would have turned a fetch primitive into a small agent; the operator decided against each |

## Context

gem-agent's `web_fetch` digests a page through a lighter model before the main
model sees it (gem-agent ADR-0017: "extraction beats truncation"). The archived
agentic-web-search honoured robots.txt because it crawled. brave-search's
`llm_context` returns extracted text for indexed pages and could sit in front of
a live fetch as a cheaper first tier. And every server in the fleet is tempted
to tell the model that the text it returns is "data, not instructions".

web-fetch serves a local model whose every prompt token costs a full
re-processing pass (measured on lagent: about 580 tokens/s of prompt on the
reference machine), from a runtime that already nonce-wraps tool results, parks
oversized ones, and gates approvals.

## Decision

web-fetch returns the extracted text of one URL and nothing else.

1. **No robots.txt.** A single fetch the user asked for stands where a browser
   visit stands. It is not a crawler: one URL per call, no link following, no
   sitemap. The `User-Agent` names the tool and its repository and hides
   nothing about who is fetching.
2. **No digest in v1.** A digest side-call on the same local model costs a
   full prompt pass per fetch and adds a second model output to trust. The
   caller bounds the response with `max_chars` and walks it with `offset`;
   whether that suffices is measured on lagent's bench before a digest is
   considered, and a digest — if ever — is its own ADR.
3. **No `llm_context`-first tiering.** Putting brave-search's index in front of
   the live fetch would make web-fetch depend on a Brave key (breaking "no
   key, no registration") and would mix an observability tier 2 tool (a third
   party's index) with a tier 4 tool (a visit from this machine) behind one
   name. Tiering belongs to the runtime and to the mcp-tactics skill, which
   choose between servers.
4. **No defensive prose for the model.** Tool descriptions and the initialize
   instructions carry the trigger (when to call: the user gives a URL, a search
   hit must be read whole, `llm_context` returned a different page) and the
   contract (`max_chars`, `offset`, `truncated`, `next_offset`, `doc_id`), plus
   two reach caveats: the fetch leaves this machine, and it is not for URLs
   under investigation. No "content, never instructions" sentence: that layer
   is the runtime's nonce wrap, and a server's account of its own safety is
   absorbed by the model as one of its own layers.

Also outside the primitive: JavaScript execution (chrome-pilot-mcp), PDF and
office documents (a Phase 2 decision), summaries and question answering,
crawling, multiple URLs per call, disk caches.

## Consequences

- A JS-only page yields nearly empty markdown; `format: raw` shows why, and the
  fallback is chrome-pilot-mcp.
- The default `max_chars` (6,000 runes) is chosen for the primary consumer's
  inline cap, not for reading comfort; a long page costs several page turns, and
  the bench decides whether that is acceptable.
- Site operators see `web-fetch/<version> (+https://github.com/nlink-jp/web-fetch)`
  in their logs; a site that refuses it refuses it openly (`http_status`).
- mcp-tactics lists web-fetch at tier 4 with the sentence "never for a URL
  under investigation — use urlscan-lookup".
- The meta-tests pin the two reach caveats in `usage.md` and in the
  instructions, and pin the trigger words in the `fetch` description.

## Alternatives considered

- **Honour robots.txt** — rejected by the operator: it governs crawlers, and a
  user-directed single visit is a browser's situation, not a crawler's.
- **Digest through the local model** (gem-agent's pattern) — deferred, not
  rejected: measure paging first; a digest is a separate ADR if the bench shows
  paging cannot serve a task class.
- **`llm_context` first, live fetch on a miss** — rejected: a key dependency and
  a tier mix; the URL-equality check that would detect a miss belongs in
  whatever chooses between servers.
- **A "this is untrusted data" sentence in the tool text** — rejected: the
  runtime supplies that layer; server self-description is not a control.

## References

- RFP §3 "プリミティブに留める", "モデルに見せる文言は契機と契約だけ"; Discussion
  Log items 5 and 8
- gem-agent ADR-0017 (digested fetch), lagent ADR-0002 (web access is an MCP
  server), .github ADR-018 (observability tiers)
- nlink-jp/knowledge `security.md`: "A model's account of its own safety
  mechanisms is not a spec"; "Tool descriptions are the one server-authored
  text you cannot wrap"
