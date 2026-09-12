# web-fetch — MCP reference

web-fetch fetches **one URL from this machine** and returns its body as
extracted text. It is a primitive: it sends no cookies, no Authorization, no
Referer and no custom headers, follows no proxy, keeps nothing on disk, and never connects to
a loopback, private, link-local or otherwise reserved address (an
`address_not_allowed` error says so). The site sees a visit from this machine's
IP address, so this server is **not** for URLs under investigation — those
belong to urlscan-lookup.

The design record is the RFP in `docs/`. This manual is what the code does.

## When to call `fetch`

- The user gives a URL and wants what is on it.
- A search result (brave-search `web_search`) must be read whole, not as a
  snippet.
- brave-search `llm_context` answered with a page other than the one asked for
  (it substitutes silently for URLs outside its index).

Not for: pages that need JavaScript or interaction (chrome-pilot), PDFs and
other binaries (refused), anything under investigation (urlscan-lookup).

## Tools

### `fetch`

| argument | type | meaning |
|---|---|---|
| `url` | string, required | The http or https URL, exactly one. `user:password@` is refused, in the input and in any redirect. |
| `format` | string | `markdown` (default from config) — headings, lists, links as `[text](absolute URL)`, code fences, pipe tables; `text` — the same without markup; `raw` — the decoded body as is. A non-HTML body (text, JSON, XML) is always raw, whatever was asked. |
| `max_chars` | integer | Characters of text to return, 1000–200000 (default from config, 6000). |
| `offset` | integer | Character position to start from (default 0). Pass `next_offset` from a truncated result to read on. |
| `fresh` | boolean | Refetch instead of reusing the page this server process cached (default false; the cache lives 10 minutes by default, in memory only). |

Response:

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
  "doc_id": "sha256:…",
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

- `truncated` and `next_offset` are present **only when text remains**. Read
  the rest with `offset: next_offset`; concatenate pages before reading, since
  a cut can fall inside a link or a code fence.
- `doc_id` is the hash of the decoded body. The same `doc_id` across page turns
  means the same page; a different one means it changed (or was refetched) —
  start again at offset 0.
- `format` is the format actually applied; `title` is empty for raw.
- `charset_source` is one of `bom`, `header`, `meta`, `utf8` (no declaration,
  valid UTF-8) or `fallback` (no declaration, decoded as windows-1252 — a page
  that declares nothing cannot be judged).
- `redirects` lists the URLs that answered 3xx, in order, only when a redirect
  happened. `final_url` is where the body came from.
- `meta.cache` is `hit` or `miss`. On a hit `fetched_at` is the original time.

### `get_usage`

No arguments. Returns this manual.

## Errors

A tool failure is a result (`isError: true`) whose text is one JSON object:
`{"code": "...", "message": "...", "details": {...}}`. Branch on `code`.

| code | meaning | recovery |
|---|---|---|
| `invalid_argument` | an unknown tool, or an argument that is missing, unknown, mistyped or out of range | fix the call; `details.argument` names it, `details.allowed` the accepted values or range |
| `invalid_url` | malformed, no host, or `user:password@` — in the input, or in a redirect target (`details.hop`) | correct the URL; a redirect into credentials is never followed, stop there |
| `scheme_not_allowed` | not http / https — in the input or a redirect target (`details.hop`) | only http and https are fetched; there is no other scheme to try |
| `address_not_allowed` | the host resolves to a loopback, private, link-local, unique-local, multicast, shared or reserved address (`details.class`, `details.address`; `details.via` names an IPv6 embedding when one was unwrapped) | this server does not reach such addresses; do not retry with a different spelling of the same host. An operator may set `[network] allow_private = true` in the global config |
| `dns_failed` | the host did not resolve, or resolved to no addresses | check the host name; retry later if it is a transient resolver failure |
| `connect_failed` | TCP or TLS failed on every vetted address (`details.cause`) | the site is down or refuses; retry later, or try the http/https counterpart if a certificate failed |
| `timeout` | the deadline passed (DNS, connect, headers or a stalled body) | retry once; a page that always stalls is not readable here |
| `too_many_redirects` | more than the configured hops (`details.hops`) | the site loops; stop |
| `http_status` | a non-2xx answer (`details.status`, `details.final_url`), a 3xx without Location included | 403/503 is usually a bot challenge — the page needs a browser (chrome-pilot); 404 means the URL is wrong |
| `too_large` | the decoded body passed the byte cap (`details.max_bytes`) | nothing is truncated: the page is larger than this server reads. Only an operator can raise `max_bytes` |
| `unsupported_content_type` | PDF, image, binary or an unknown media type (`details.content_type`), or a non-gzip `Content-Encoding` (`details.content_encoding`) | this server extracts HTML, text and JSON only |
| `charset_unsupported` | the page declares a charset the decoder table does not know (`details.charset`) | try `format: raw` only if the bytes might be ASCII; otherwise the page is not readable here |

## Reach

Every fetch leaves this machine as a plain GET with a User-Agent that names
this tool. Nothing is stored beyond the in-memory cache of this process.
