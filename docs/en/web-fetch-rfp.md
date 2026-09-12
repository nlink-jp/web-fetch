# RFP: web-fetch

> Generated: 2026-09-12
> Status: Draft (rev.2 — 17 independent-review findings folded in, 2026-09-12)
> The Japanese text (`docs/ja/web-fetch-rfp.ja.md`) is canonical; this is its translation.

## 1. Problem Statement

lagent, the local-LLM agent runtime (lab-series), gained web search through the
brave-search MCP server, but **it has no way to read the body of a URL the user
hands it**. gem-agent's `web_fetch` rests on Vertex AI's URL Context (the page is
fetched provider-side); lagent ADR-0002 declined to reproduce it — "a local fetch
would reach localhost and the LAN from the operator's machine; absent is safer
than reinvented" — and at the same time decided that "a future 'we need web
access' is an MCP server, not a lagent feature".

brave-search's `llm_context` already returns the extracted text of search hits,
so "search, then read the hit" is covered. But today's measurement (below) showed
that **for a URL that is not in the index, llm_context silently returns a
different page rather than "not found"**. What remains is reading a URL the user
supplies — new, deep, or unindexed pages — and no search API fills that.

web-fetch is **a primitive that takes one URL and returns its extracted body as
text**, as a CLI and as an MCP server, with no key and no registration. Because
it fetches from the operator's machine, the SSRF class ADR-0002 feared (reaching
localhost, the LAN, cloud metadata, authenticated pages) is **closed by design**:
the resolved IP addresses are judged against a finite denied domain and pinned
for the connection, every redirect hop is judged again, and the client carries no
cookies and no Authorization.

**User**: the nlink-jp operator. Connected as MCP from both lagent and Claude
Code, and used as a CLI from the shell. **Setup**: `brew install
nlink-jp/tap/web-fetch` plus `claude mcp add` / an entry in lagent's `mcp.json`.
It runs without a config file. Nothing in the setup burden can sink adoption.

### The measurement behind it (2026-09-12, brave-search `context`, 7 calls, $0.035)

| Query | Page returned | Verdict |
|---|---|---|
| bare URL `https://go.dev/doc/go1.26` (max_tokens 2048) | that page only, 19 snippets, 7.5k chars | match |
| `site:go.dev/doc/go1.26` (2048) | that page only, 20 snippets, 7.5k chars | match |
| `site:go.dev Go 1.26 release notes` (4096) | that page, 40 snippets, 14.3k chars + the blog post | match |
| bare URL `https://github.com/nlink-jp/lagent` (published 2 days ago) | `github.com/InternLM/lagent` | **silent substitution** |
| `site:github.com/nlink-jp gem-agent` | `github.com/nlink-jp` (the org page) | mismatch |
| bare URL `https://go.dev/doc/go1.26` (**32768**) | that page only, 42 snippets, 15.0k chars | match (rerun to find the ceiling) |
| bare URL `https://github.com/nlink-jp/gem-agent` (about a month old) | `github.com/nlink-jp` (the org page) | **silent substitution** |

- For an indexed page the snippets come in document order with headings, in
  Markdown-like form — good quality. But even at `max_tokens=32768` the result
  was 42 snippets, about 15k characters, against a real page body of about 27k
  characters: **55%**. Brave selects; it does not return the page.
- Price is $0.005 per call regardless of the token budget.
- A substitution is not an API error. It is detectable by exact comparison of
  the requested URL and the chunk URL, but web-fetch does not build that tier
  (§3 "stay a primitive").
- No snippet text was stored anywhere (Brave ToS); only URLs, counts and sizes.

## 2. Functional Specification

### Commands / API Surface

```
web-fetch <url>                 # extract the body to stdout (default markdown)
web-fetch --json <url>          # structured JSON (the same shape as MCP)
web-fetch mcp                   # run as an MCP server (stdio)
web-fetch version               # print the version (the brew test calls it)
web-fetch --version             # same
```

A first argument starting with `http://` / `https://` is the URL; anything else
is a subcommand. A URL always carries a scheme, so there is no ambiguity and no
`fetch` subcommand.

Two MCP tools: `fetch` and `get_usage`. `get_usage` returns the embedded
`usage.md` (tool reference, response schema, error-recovery table).

### Parameter matrix (the one table for CLI / MCP / config / environment)

CLI flags, MCP arguments, config keys and environment variables are **derived
from this table**; a combination absent here does not exist. Precedence:
**flag > environment > config file > built-in default**.

| Item | CLI | MCP argument | config | env | Range / default |
|---|---|---|---|---|---|
| URL | positional | `url` | — | — | `http` / `https` only; userinfo (`user:pass@`) rejected; ports unrestricted (the judgment is address-only, §3); one per call |
| Format | `--format` | `format` | `[fetch] format` | `WEB_FETCH_FORMAT` | `markdown` / `text` / `raw`; default `markdown` |
| Character cap | `--max-chars` | `max_chars` | `[fetch] max_chars` | `WEB_FETCH_MAX_CHARS` | 1,000–200,000 (runes); default **6,000** (rationale in §7) |
| Offset | `--offset` | `offset` | — | — | ≥ 0; rune position in the extracted text; pass `next_offset` after a `truncated` result; at or past `total_chars` → an empty page (`returned_chars: 0`, no `truncated`) |
| Refetch | — | `fresh` | — | — | bool; bypasses the MCP in-process cache; a no-op when `ttl = 0`; the CLI has no cache |
| Deadline | `--timeout` | **none** | `[fetch] timeout` | `WEB_FETCH_TIMEOUT` | 1s–120s; default **20s**, from request start (DNS included) through body; a Go duration (`20s`) or bare seconds (`20`) |
| Byte cap | `--max-bytes` | **none** | `[fetch] max_bytes` | `WEB_FETCH_MAX_BYTES` | 64 KiB–50 MiB; default **5 MiB**; applied to the **decoded** stream (§2 Content-Type); over it → `too_large` |
| User-Agent | — | — | `[fetch] user_agent` | `WEB_FETCH_USER_AGENT` | default `web-fetch/<version> (+https://github.com/nlink-jp/web-fetch)` |
| Accept-Language | — | — | `[fetch] accept_language` | `WEB_FETCH_ACCEPT_LANGUAGE` | default empty (header not sent); e.g. `ja, en;q=0.8` |
| Redirect cap | — | — | `[network] max_redirects` | — | 0–10; default **5** |
| Private networks | — | — | `[network] allow_private` | — | bool; default **false**; **honoured only in a config file found on the default search path**; set in a file reached via `--config` / `WEB_FETCH_CONFIG` it is a startup error (§3) |
| Cache TTL | — | — | `[cache] ttl` | — | 0 (off)–1h; default **10m**; MCP only |
| Cache entries | — | — | `[cache] max_entries` | — | 1–256; default **32**; MCP only |
| Config / output | `--config`, `--json` | — | — | `WEB_FETCH_CONFIG` | |

Deliberately **absent** from the table: arbitrary headers, cookies, Basic auth,
proxy settings (the `HTTP_PROXY` family is ignored — a proxy defeats the address
check), multi-URL batches, robots.txt.

### Input / Output

**Input** is one URL: a positional argument for the CLI, `url` for MCP. stdin is
not read.

**Output** (identical JSON for CLI `--json` and MCP `fetch`, except `meta.cache`):

```json
{
  "url": "https://example.org/a",
  "final_url": "https://example.org/b",
  "status": 200,
  "content_type": "text/html; charset=Shift_JIS",
  "charset": "shift_jis",
  "charset_source": "header",
  "title": "Page title",
  "format": "markdown",
  "doc_id": "sha256:3f2a…",
  "text": "# Heading\n\nBody…",
  "offset": 0,
  "returned_chars": 6000,
  "total_chars": 26946,
  "truncated": true,
  "next_offset": 6000,
  "redirects": ["https://example.org/a"],
  "fetched_at": "2026-09-12T07:28:41Z",
  "meta": { "bytes": 69376, "elapsed_ms": 412, "cache": "miss" }
}
```

- `truncated` and `next_offset` appear **only when the text was cut**; their
  presence is itself the signal of a partial view. `total_chars` and
  `returned_chars` always appear.
- `format` is the format **actually applied**: for `text/*` and JSON it is `raw`
  whatever was requested.
- `doc_id` is the SHA-256 of the decoded body. `offset` is a rune index into a
  deterministic extraction, so page turns that see the same `doc_id` walk the
  same basis; a changed `doc_id` means the page changed (or was refetched via
  `fresh` / TTL expiry) and the caller restarts at 0. A cut may fall inside a
  `[text](url)` or a fence — the caller concatenates pages before reading.
- `charset_source` is one of `bom` / `header` / `meta` / `utf8` / `fallback`;
  `fallback` declares that no label and no valid UTF-8 were found and the WHATWG
  default (windows-1252) was used.
- `redirects` appears only when a redirect happened: the URLs that answered 3xx,
  in order (the first equals `url`). A refused hop is appended last, and the call
  then ends in an error rather than a result.
- `meta.cache` (`hit` / `miss`) is MCP-only; the CLI has none. On a hit,
  `fetched_at` is the original fetch time.
- In `raw` format `title` is empty and `text` is the charset-decoded body as is.
- The whole text is always reachable through `offset` (reachability). **There is
  no server-side spill to files (no workspace_root)** — sizing is the runtime's
  job (lagent / gem-agent park an oversized result in the work directory and
  read it back with `read_file`).

**CLI text output** (without `--json`): the title on line 1, `final_url` on line
2, a blank line, the body. When cut, one trailing line:
`[truncated: 0–6000 of 26946 chars; continue with --offset 6000]`.

**Content-Type handling** (v1, user decision):

| Content-Type | Handling |
|---|---|
| `text/html`, `application/xhtml+xml` | extracted (markdown / text) or raw |
| `text/*` (plain, markdown, csv, xml …) | body as is (`format` reported as `raw`, `title` empty) |
| `application/json`, `application/*+json`, `application/xml` | same |
| `application/pdf` | refused, `unsupported_content_type` (PDF extraction is a Phase 2+ decision) |
| anything else (images, binaries, octet-stream) | refused, `unsupported_content_type` |

The header wins; without one, the first 1,024 bytes are sniffed (the same buffer
the charset detection uses).

**Transfer encoding and body size**: `Accept-Encoding` is left to the Transport
(only gzip it asked for itself is decoded transparently). A response naming any
other `Content-Encoding` (br, zstd …) is `unsupported_content_type`
(`details.content_encoding`). `max_bytes` is applied to the **decoded** stream
with `io.LimitReader(max_bytes + 1)`; `Content-Length` is never trusted (it lies,
is missing, or the body is chunked). A compression bomb ends as `too_large` here.

**HTML charset** follows the adopted library `golang.org/x/net/html/charset`
(WHATWG order): BOM → `Content-Type` charset → `<meta charset>` prescan → UTF-8
validity → windows-1252 default. The outcome is reported in `charset` /
`charset_source`. `charset_unsupported` fires only for a **declared label**
(header or meta) absent from the conversion table (`htmlindex`) — the library's
`NewReader` silently skips an unknown label, so `Lookup` runs first. Shift_JIS,
EUC-JP and ISO-2022-JP are included (user decision: supported in v1).

**Extraction rules (`markdown`)** — deterministic, no LLM:

1. The body root is the first present of `<main>`, `[role="main"]`,
   `<article>`, `<body>`. With several `<article>` elements (an index page) the
   one with the **largest text length** is taken, and if that is under a quarter
   of `<body>`'s text length the root falls back to `<body>`.
2. Dropped: `script` / `style` / `noscript` / `template` / `svg` / `iframe` /
   `nav` / `footer` / `aside` / `form`, elements with `hidden`
   (`hidden="until-found"` is kept) and `aria-hidden="true"`. `header` is dropped
   **only when it is a direct child of `<body>` or has `role="banner"`** —
   `<article><header><h1>` is the article's title block. `<details>` content is
   kept even when closed.
3. Headings become `#`, paragraphs are blank-line separated, `ul`/`ol` become
   `-` / `1.`, `a` becomes `[text](absolute URL)`, `img` contributes alt text
   only, `pre`/`code` become fences, `table` becomes pipe rows, `blockquote`
   becomes `>`. Relative URLs resolve against `<base href>` when present,
   otherwise `final_url`.
4. Whitespace is collapsed within lines; three or more blank lines become two.
5. The title is `<title>`, else the first `h1` inside the body root.

`text` is the same without the markup of rule 3. Root selection is a
**heuristic**; the escape hatch when it misses is `raw` (choose a format, do
not change the default).

**Errors** are structured JSON (a text block with `isError: true`; the CLI
prints the same JSON on stderr):

| code | Meaning | details | CLI exit |
|---|---|---|---|
| `invalid_argument` | `max_chars` / `offset` / `format` out of range; an unknown or mistyped MCP argument | `argument`, `allowed` | 2 |
| `invalid_url` | malformed, has userinfo, no host; also a redirect target with userinfo (`details.hop`) | `url`, `hop` | 2 (1 on a redirect) |
| `scheme_not_allowed` | not `http` / `https`; also a scheme change on redirect | `scheme`, `hop` | 2 (1 on a redirect) |
| `address_not_allowed` | resolves into the denied domain (§3); the message says `allow_private` can lift it | `host`, `address`, `class` | 1 |
| `dns_failed` | resolution failed, or **zero addresses** | `host` | 1 |
| `connect_failed` | TCP / TLS failure on every vetted address | `host`, `cause` | 1 |
| `timeout` | deadline exceeded (a stalled body read included) | `timeout` | 1 |
| `too_many_redirects` | over `max_redirects` | `hops` | 1 |
| `http_status` | non-2xx (a 3xx without `Location` included) | `status`, `final_url` | 1 |
| `too_large` | over `max_bytes` after decoding | `max_bytes` | 1 |
| `unsupported_content_type` | a refused row of the table above, or a non-gzip `Content-Encoding` | `content_type`, `content_encoding` | 1 |
| `charset_unsupported` | a declared label absent from the conversion table | `charset` | 1 |

**Exit codes** (CLI): 0 = fetched (even if cut), 1 = fetch failed, 2 = usage or
configuration error. `invalid_argument` and an `invalid_url` /
`scheme_not_allowed` of the input URL itself are 2; everything else is 1.

### Configuration

The first of `$XDG_CONFIG_HOME/web-fetch/config.toml`,
`~/.config/web-fetch/config.toml`, `~/Library/Application Support/web-fetch/config.toml`
(the **default search path**). Optional. The sectioned-TOML subset reader is
vendored from brave-search.

```toml
[fetch]
format          = "markdown"
max_chars       = 6000
timeout         = "20s"
max_bytes       = "5MiB"
accept_language = ""          # empty = not sent
# user_agent    = ""          # empty = built-in default

[network]
max_redirects = 5
allow_private = false         # true permits the LAN / localhost. Honoured only in a default-search-path file

[cache]
ttl         = "10m"           # MCP in-process only; 0 disables
max_entries = 32
```

There is no project-level config file. `--config` / `WEB_FETCH_CONFIG` may point
at any file, but a file reached that way setting `allow_private = true` is a
startup error ("`[network] allow_private` is honoured only in a config file on
the default search path").

### External Dependencies

- **External services / API keys: none.**
- Go modules: `golang.org/x/net` (`html`, `html/charset`) and `golang.org/x/text`
  (charsets). Both are `golang.org/x/` modules maintained by the Go team. Most
  Go repositories in the organization carry them transitively, and
  mail-analyzer / mail-analyzer-local / lite-rag require `x/text` directly.
  **web-fetch is the first to import `x/net/html` and `x/text/encoding`
  directly.** Under the supply-chain rule (stdlib → first-party → REST; no
  community tooling) they are treated as first-party (recorded in ADR-0003).
  Nothing else.
- The MCP skeleton, the sectioned-TOML reader and the release scripts are
  vendored from brave-search (not imported).

## 3. Design Decisions

### Why an MCP server and not a lagent built-in tool

lagent ADR-0002 decided that "a future web access is an MCP server", and
brave-search entered by that route. The same route means zero change to lagent
and the same server serves Claude Code and gem-agent. lagent's receiving side
already exists: MCP results are nonce-wrapped as untrusted data, oversized
results are parked in the work directory, and approval is governed by
`[approval.tools]` / `--allow`.

### SSRF is closed by a finite denied domain, not by string rules (→ ADR-0001)

ADR-0002 and gem-agent ADR-0017 declined a local fetch because of SSRF. Written
as URL patterns the defence becomes a rule over an infinite domain. Instead:

1. **The judgment lives in the dialer.** `http.Transport.DialContext` is replaced:
   it resolves the `host:port` it is given, judges **every** returned address
   against the denied domain, and connects to a vetted address **as an `ip:port`
   literal** (there is no path that dials by name; with several addresses the
   dialer tries them in turn itself — Happy Eyeballs is given up by design).
   Check and connect are one operation, so a DNS answer that changes between a
   pre-check and the connection (rebinding) has nothing to exploit — no TOCTOU
   is created. Zero addresses is `dns_failed`. TLS SNI and certificate
   verification still use the URL's host name.
2. **One finite table** (every row pinned by tests):

   | Class | Ranges |
   |---|---|
   | loopback | 127/8, ::1 |
   | private | 10/8, 172.16/12, 192.168/16 |
   | link-local (cloud metadata included) | 169.254/16, fe80::/10 |
   | site-local (deprecated, still routed) | fec0::/10 |
   | ULA | fc00::/7 |
   | unspecified / broadcast / multicast | 0.0.0.0, ::, 255.255.255.255, 224/4, ff00::/8 |
   | shared address space | 100.64/10 |
   | reserved / benchmarking | 0/8, 192.0.0/24, 198.18/15, 240/4 |
   | embedded IPv4, unwrapped and re-judged | IPv4-mapped ::ffff:0:0/96, IPv4-compatible ::/96, NAT64 64:ff9b::/96 and 64:ff9b:1::/48, 6to4 2002::/16 (bits 16–47), Teredo 2001::/32 (last 32 bits inverted) |

   Ports are unrestricted (of `ip:port` only the address is judged — a
   deliberate choice). Numeric spellings (`2130706433`, `0x7f.0.0.1`, `127.1`)
   come back normalised from the resolver and are judged normally, ending as
   `address_not_allowed`; depending on the resolver implementation (system /
   PreferGo) they may instead be `dns_failed` — both are refusals.
3. **Every redirect hop** passes through the same dialer and is judged again.
   `CheckRedirect` additionally inspects `Location`: userinfo → `invalid_url`
   (Go's `http.Client` would otherwise synthesise `Authorization: Basic` from
   it), a non-http(s) scheme → `scheme_not_allowed`, and hops are bounded by
   `max_redirects`.
4. **No credentials**: no cookie jar, no Authorization, no arbitrary headers,
   userinfo rejected in the input URL and in every redirect target. That
   authenticated pages are unreachable is a structural fact.
5. **Proxy environment variables are ignored**: through a proxy the address
   judgment loses its meaning.
6. **`allow_private = true` is honoured only in a config file on the default
   search path.** Set in a file reached via `--config` / `WEB_FETCH_CONFIG` it is
   a startup error — a runtime's project configuration (`.mcp.json` `args` /
   `env`) can point at any file and must not be able to loosen this. Loosening
   is a permission; no flag or MCP argument can change it. **Residual**: `HOME` /
   `XDG_CONFIG_HOME` are environment the runtime hands its child, and web-fetch
   cannot tell them apart from the operator's own. lagent reads `.mcp.json` only
   after the trust prompt, and Claude Code likewise, so the environment is left
   to the runtime's trust boundary (stated in §7, recorded in ADR-0001).

### Stay a primitive (→ ADR-0002, user decision)

- **No robots.txt.** A single fetch the user asked for stands where a browser
  visit stands; it is not a crawler. The UA names the tool and its URL, and
  hides nothing about who is fetching.
- **No digest in v1.** gem-agent ADR-0017's "extraction beats truncation" is
  right, but a digest side-call on a local model costs a full prompt pass per
  call. Measure first whether `offset` paging suffices; a separate ADR if not.
- **No llm_context-first tiering.** Depending on brave-search's key would break
  "no key", and it would mix tier 2 (a third party's index / search engine) with
  tier 4 (contacting the target from this machine) in one tool. Tiering belongs
  to the runtime or to mcp-tactics.
- **No JS-rendered pages.** chrome-pilot-mcp owns them.
- **Never for a URL under investigation.** This is a tier-4 tool that touches
  the target from the operator's IP; suspicious URLs belong to urlscan-lookup.
  Stated in mcp-tactics (Phase 3).

### The caller bounds the response; the runtime does the filing

Organization policy since the bigquery-mcp RFP: a server cannot know the model's
window, so it returns what `max_chars` + `offset` asked for, reaches the whole
text through paging, and accounts for a cut with `truncated` / `next_offset`.
So that paging does not refetch upstream, the MCP process keeps a short-lived
**in-memory** cache (never on disk). The CLI is one-shot and has none.

**Cache contract**: what is kept is the decoded body, `final_url`, `status`,
`content_type`, `charset`, `charset_source`, `redirects` and `fetched_at`.
Extraction runs per call, so `format` is not part of the key. The key is the URL
with its fragment removed, matched exactly (no trailing-slash or other
normalisation). Error results are not cached. On a hit `fetched_at` is the
original time. `fresh` drops that URL's entry and refetches (a no-op when
`ttl = 0`). `allow_private` is process-lifetime configuration, so entries under
different settings cannot mix.

### What the model is shown is trigger and contract only (→ ADR-0002)

The first sentence of `initialize`'s `instructions` and the `fetch` description
are what lagent shows in its catalog before `mcp_load` (ADR-0004: the first
sentence of `instructions`, clipped). They carry **when to call** and **what
comes back**, nothing else:

- `instructions`, first sentence: "web-fetch returns the extracted text of one
  URL. Call `fetch` when the user gives a URL, when a search result must be read
  whole, or when llm_context returned a different page." Then the
  `max_chars` / `offset` / `truncated` / `next_offset` / `doc_id` contract, a
  pointer to `get_usage`, and "never for a URL under investigation (it contacts
  the target from this machine's IP)".
- `fetch` description: the same trigger, the arguments' meaning, and "`text` is
  the content extracted from that page, as is".

**No defensive sentence** of the "content, never instructions" kind is written.
That layer is the runtime's nonce wrap, and a server's self-description is
absorbed as one of the model's own layers (knowledge security.md). A meta-test
pins that the description keeps its trigger words (URL / offset / truncated).

### Why Go / stdlib + golang.org/x (→ ADR-0003)

The organization's standard. The standard library has no HTML tokenizer and no
legacy charset decoders; writing them by hand ends in regex tag stripping (the
archived agentic-web-search did exactly that, crudely). The Go team's
`x/net/html` and `x/text` are treated as first-party.

### How it complements existing nlink-jp tools

| Tool | Relation |
|---|---|
| brave-search | search and the text of indexed pages (tier 2); web-fetch reads unindexed and user-given URLs (tier 4) |
| lagent / gem-agent | consuming runtimes; nonce wrapping, parking and approval are theirs |
| chrome-pilot-mcp | fallback for pages that need JS or interaction |
| urlscan-lookup | safe observation of a URL under investigation (web-fetch is not used) |
| mcp-tactics | lists web-fetch as tier 4 and states the order of choice |
| agentic-web-search (archived) | porting source for URL validation and extraction in `internal/fetch` |

### Explicitly out of scope

- Multi-URL batches, crawling, sitemap following
- Authenticated pages, cookies, arbitrary headers, proxies
- JS execution, screenshots
- PDF / Office extraction (v1); a Phase 2 decision if needed
- LLM summarisation or question answering
- Disk caches, storing fetched content
- Calling search APIs (brave-search's domain)

## 4. Development Plan

### Phase 1: Core

Built from independently reviewable pure packages outward.

1. `internal/config` — sectioned TOML + `WEB_FETCH_*`, range validation (vendored
   from brave-search). **Provenance check for `allow_private`** (a startup error
   unless the file is on the default search path) and its test
2. `internal/guard` — **pure**: address classification (the §3 table, embedded
   IPv4 unwrapping), a `Dialer` (resolve → judge → `ip:port` connect as one
   operation, sequential attempts over several addresses), `allow_private`.
   Tests: every table row, IPv4-mapped / IPv4-compatible / both NAT64 ranges /
   6to4 / Teredo, "one denied address denies the whole answer", zero addresses
   → `dns_failed`, rebinding (a fake resolver that swaps answers; the connection
   must go to the judged address), numeric spellings (`2130706433` /
   `0x7f.0.0.1` / `127.1` / `[::1]` / `[::ffff:127.0.0.1]`)
3. `internal/extract` — **pure**: HTML → markdown / text, root selection
   (`main` / `role=main` / largest `article` / `body`), conditional `header`
   removal, `<base href>`, absolute URLs, charset detection (`Lookup` →
   `NewReader`) with `charset_source`, `doc_id`. Tests: hand-written fixtures (no
   real pages pasted), meta-only Shift_JIS with no header charset, EUC-JP,
   ISO-2022-JP, an unknown declared label, pages without `main`, several
   `article`s, `hidden` / `until-found` / `details`, `offset ≥ total_chars`
4. `internal/fetch` — the HTTP client (the guard's Dialer, `CheckRedirect`
   userinfo / scheme checks and cap, decoded-stream `max_bytes`,
   `Content-Encoding` judgment, Content-Type judgment, header policy, proxy
   ignored). Tests: `httptest.Server` reached through a fake resolver (loopback
   is denied by default, so tests inject the resolver), a redirect to a denied
   address, a `Location` with userinfo, a 3xx without `Location`, a gzip body
   decoding past `max_bytes` (bomb), a lying `Content-Length`, a response naming
   `br`, `format=markdown` on `text/plain`, a body stalling mid-read → `timeout`
5. `internal/engine` — defaults, validation, paging, response shaping; shared by
   CLI and MCP. Test: the JSON response's byte size (a default response of 6,000
   Japanese runes stays under 20,000 bytes)
6. `internal/app` — the CLI (positional URL / `mcp` / `version`, text and JSON
   rendering, the exit-code table)
7. `internal/mcp` — vendored skeleton + `annotations` (`readOnlyHint: true`,
   `openWorldHint: true`; the skeleton's `protocolVersion` moves 2024-11-05 →
   2025-03-26 — annotations are additive and older clients ignore them; a
   candidate to flow back to brave-search) + embedded `usage.md` + meta-tests
   (every tool name, argument and error code appears in the manual; the
   description carries its trigger words)
8. `internal/cache` — the MCP in-memory TTL cache (LRU, `fresh`, `ttl = 0`,
   fragment-stripped key, errors not kept)
9. `e2e/` (`e2e` build tag) + `scripts/e2e.sh` — one stable public page, the
   refusal of `http://127.0.0.1:1/`, the refusal of `http://127.0.0.1.nip.io/`
   (public DNS answering loopback — the rebinding class), an optional public
   Shift_JIS page (`WEB_FETCH_E2E_SJIS_URL`; the offline fixture already covers
   it). Logged with a `MEASURED:` prefix

**Milestone**: `make check` green, e2e passing, the usage.md meta-test passing,
a real `mcp_load` → `fetch` round trip from lagent, and **a default `fetch` of a
Japanese page rendering inline in lagent rather than being parked** (recorded as
a `MEASURED:` line).

### Phase 2: Features (decided after measurement)

- `format: links` (the page's links, for navigation)
- Conditional requests (`If-None-Match`) to revalidate the cache
- PDF text extraction (weighing the cost of an in-house implementation against demand)
- A digest / `focus` argument (once lagent's bench shows a case paging cannot serve)
- Re-selecting the extraction root when `main` is nearly empty

### Phase 3: Release

- README.md / README.ja.md (user-facing information only), AGENTS.md, CLAUDE.md, CHANGELOG
- `docs/{en,ja}/adr/0001` (the finite SSRF domain: in-dialer judgment, embedded
  IPv4, redirect inspection, no credentials, `allow_private` provenance, the
  environment residual), `0002` (a primitive: no robots, no digest, no tiering,
  no defensive prose), `0003` (golang.org/x as first-party),
  `docs/{en,ja}/reference/`
- Independent verification pass (at design freeze and before release; findings triaged)
- Release (signing, notarization, verify-release, tap), util-series submodule,
  org profile, nlink-web-site card
- **mcp-tactics follow-up** (tier-4 entry, "never for a URL under investigation",
  a note on llm_context's silent substitution of unindexed URLs)
- **Knowledge feed-back**: security.md "close SSRF inside the dialer as a finite
  domain (resolve → judge → connect as one operation, unwrap embedded IPv4,
  inspect every hop's `Location`, carry no credentials, bound where a loosening
  setting may come from)"; mcp-server-design.md "hold a document for paging in
  memory, and prove the basis unchanged with a `doc_id`"

Independent review: Phase 1 items 2 (guard) and 3 (extract) are pure packages
reviewable on their own. Phase 3's mcp-tactics and knowledge changes are
separate commits from the tool itself.

## 5. Required API Scopes / Permissions

**None.** No external service keys, OAuth or IAM. The terms of the sites fetched
bind the fetcher (the operator). The tool stores and redistributes nothing (the
MCP in-memory cache is transient).

## 6. Series Placement

Series: **util-series**
Reason: a general data-acquisition primitive — neither an authenticated client
for one service (cli-series) nor an experiment (lab-series). The same placement
as brave-search. lagent (lab-series) is a consumer, not the home.

## 7. External Platform Constraints

- **Target sites**: bot challenges (Cloudflare and kin) surface as `http_status`
  403 / 503. JS-only pages yield a nearly empty markdown → inspect with `raw`,
  then chrome-pilot-mcp if needed. A huge page is refused as `too_large` once
  the decoded stream passes `max_bytes` — raise `--max-bytes` or give up; it is
  never truncated.
- **Charsets**: the order is BOM → header → meta → UTF-8 validity →
  windows-1252 (WHATWG). A declared label outside the conversion table is
  `charset_unsupported`. An undeclared non-UTF-8 page is decoded as
  windows-1252 with `charset_source: fallback` (undeclared Japanese Shift_JIS
  comes out garbled — a page that declares nothing cannot be judged).
- **MCP client response ceiling**: the primary consumer, lagent, parks any MCP
  text block over 20,000 **bytes** in the work directory and shows the model an
  800-rune preview plus a path. The default `max_chars` is derived from that
  ceiling: Japanese runs about 3 bytes per rune, so **6,000 runes (≈ 18,000
  bytes plus the JSON envelope)** is the largest default that keeps the default
  response inline. English pages have three times the headroom, and a model that
  raises `max_chars` can read 16,000 runes within the same ceiling. Every page
  turn makes a local model reprocess the whole conversation, so the default is
  revisited with lagent's bench. Other clients (Claude Desktop etc.) have their
  own ceilings and are not considered here.
- **Local models**: the fetched body is untrusted data the runtime nonce-wraps.
  web-fetch writes no defensive prose of its own (§3).
- **The environment as a trust boundary**: rewriting `HOME` / `XDG_CONFIG_HOME`
  moves the "default search path". web-fetch treats that environment as handed
  down by the runtime on the operator's behalf. lagent and Claude Code read a
  project's `.mcp.json` only after the trust prompt, so this boundary is the
  runtime's.
- **Network**: IPv6-only and DNS64 environments exercise the NAT64 rule. Happy
  Eyeballs is replaced by the dialer's sequential attempts, so a dual-stack host
  with one dead family connects more slowly. Proxy-only environments are
  unsupported (stated in the README as a design constraint).
- **Legal**: rights in fetched content belong to each site. The tool stores
  nothing. The no-robots decision presumes a single operator-directed visit and
  is not to be repurposed for crawling.

---

## Discussion Log

1. **Trigger (2026-09-12)**: the user — "brave-search gave lagent web search; what
   is missing is web fetch. Is there a good approach?" Survey: lagent ADR-0002 had
   declined web_fetch over SSRF and decided "web access is an MCP server";
   gem-agent ADR-0017 uses Vertex URL Context (provider-side fetch + digest) and
   rejected local fetch + readability ("reopens SSRF, needs HTML parsing, no JS,
   still needs a digest"). lagent already nonce-wraps, parks and gates MCP
   results. The archived agentic-web-search still holds an in-house URL
   validator / robots / HTML-to-text (stdlib only).
2. **Options weighed**: (a) brave-search `llm_context` as a fetch substitute,
   (b) a new MCP server `web-fetch`, (c) chrome-pilot-mcp, (d) curl in the write
   lane, (e) a lagent built-in. Recommendation (b); (a) to be measured first,
   (c) as the JS fallback, (d) is not a design, (e) contradicts ADR-0002.
3. **User decision**: "go with web-fetch; measure llm_context first".
4. **Measurement** (table in §1): an indexed URL returns that page's text in
   document order but only 55% of it; an unindexed URL silently returns another
   page → the remaining need is "a URL the user supplies".
5. **User decisions at RFP start**: no robots.txt / stay a primitive (no
   llm_context-first tiering) / no digest in v1.
6. **Q&A**: non-HTML — "text / JSON pass through, PDF and binaries refused";
   charsets — "supported in v1, adopt `golang.org/x/text`"; util-series
   placement, tool names, SSRF design and phase split approved as proposed.
7. **Considered and not taken**: SSRF defence by URL patterns or host deny lists
   (string rules over an infinite domain; judge resolved addresses against a
   finite table instead); stopping at a cross-host redirect and demanding a
   second call (every hop passes the same dialer, so following and reporting
   `final_url` / `redirects` is simpler); server-side parking in a work
   directory (abolished by organization policy); a generic `mcp_call`-style
   proxy (schemas are not rendered, argument discipline drops — lagent
   ADR-0004's rejection reason); a `fetch` subcommand (a URL always has a
   scheme, so the positional form is unambiguous).
8. **Independent review (2026-09-12, 17 findings against rev.1, all adopted)**:
   two HIGH — ① `allow_private` could be loosened through `--config` /
   `WEB_FETCH_CONFIG` → honoured only in a default-search-path file, an error
   elsewhere; the environment (`HOME` / `XDG_CONFIG_HOME`) recorded as the
   runtime's trust boundary in the residuals. ② the default `max_chars` of
   16,000 runes always exceeds lagent's 20,000-byte inline ceiling for Japanese,
   so every default response would be parked → lowered to 6,000, with a
   measured milestone. Ten MEDIUM — five embedded-IPv6 classes, zero-address
   answers and no dialing by name added to the table; a redirect `Location` with
   userinfo would make Go send `Authorization: Basic` → refused in
   `CheckRedirect`; `max_bytes` on the decoded stream and a `Content-Encoding`
   policy; the charset order aligned with the library (WHATWG) plus the firing
   condition of `charset_unsupported` and `charset_source`; §7's "cut" unified
   to `too_large`; tool text limited to trigger and contract, no defensive
   prose; six extraction gaps (conditional `header`, largest `article`,
   `role=main`, `<base href>`, `until-found` / `details`, `doc_id`); twelve test
   cases and named e2e hosts; brave-search is tier 2, not tier 1; the `fetch`
   subcommand mismatch. Five LOW — the dependency-precedent wording, the
   `protocolVersion` revision, nine small specification holes, the cache
   contract, one clause missing in the English mirror. Findings not taken:
   none.

## Post-implementation record (2026-09-12, `make e2e` at the end of Phase 1)

- **§1's "about 27k characters of page body" was a regex tag-stripping
  estimate.** Under web-fetch's markdown extraction the Go 1.26 release notes
  are **35,922 characters** (link URLs are kept), i.e. **6 pages** at the
  default `max_chars` of 6,000. Whether six page turns are acceptable on a
  local model remains lagent's bench question.
- **§7's milestone "a Japanese page's default response stays inline"**: the
  ja.wikipedia.org article on 日本語 (`total_chars` 559,231) yields a default
  response of **7,016 JSON bytes** (cap 20,000). Its first 6,000 characters are
  link-heavy (6,297 bytes), so the 3-bytes-per-rune worst case did not occur in
  this measurement; it holds by arithmetic (18,000 + envelope).
- **The rebinding-class fixture** `http://127.0.0.1.nip.io/` was refused as
  loopback (`address_not_allowed`, no `via`).
- **The lagent round trip (the runtime side of the milestone)**: lagent
  v0.3.0 + LM Studio (Gemma 4 26B A4B QAT), isolated HOME / state, the server
  registered in the global `mcp.json`, preloaded with
  `--allow mcp__web-fetch__*`. One `mcp__web-fetch__fetch` call, the result
  held inline (no spill note in the transcript, `returned_chars` 6,000), the
  model reported the title and first heading correctly, 20 s for the whole
  one-shot.
- `http://go.dev/` → `https://go.dev/`, one hop, recorded in `redirects`.
- Extraction details settled in the implementation (absent from the RFP):
  `javascript:` and `data:` hrefs render as text only; `<meta
  charset="utf-16">` means UTF-8 (WHATWG); the meta prescan stops at `<body>`;
  any argument containing `://` is dispatched as a fetch, so `ftp://x` gets
  `scheme_not_allowed` rather than "unknown command"; runs of three or more
  newlines fold to two (one blank line).
