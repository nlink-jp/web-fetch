# web-fetch

Fetch one URL from this machine and get its readable text back — as a command
line tool and as a local MCP server for agent runtimes.

```
web-fetch https://go.dev/doc/go1.26                  # markdown-ish text of the page
web-fetch --format text --max-chars 3000 https://…   # plain text, first 3,000 characters
web-fetch --json https://…                           # the same result as JSON (what MCP returns)
web-fetch https://… --offset 6000                    # the next page of a long document
web-fetch mcp                                        # MCP server over stdio: tools fetch, get_usage
```

## What it does

- Fetches **one URL** (`http` / `https`), follows up to 5 redirects, and
  returns the page's main content as **markdown**, **plain text**, or the
  **raw** decoded body. Flags may come before or after the URL.
- Handles HTML in any declared charset (Shift_JIS and EUC-JP included), plain
  text, and JSON. PDFs, images and other binaries are refused with a clear error.
- Bounds every response: `--max-chars` (default 6,000) and `--offset` page
  through the whole text; a cut result says so (`truncated`, `next_offset`) and
  carries a `doc_id` so you can tell when the page changed between pages. A
  page whose nested quotes or lists would render to many times its own size
  is refused (`too_large`) instead of filling memory.
- Reports what it found: final URL, HTTP status, content type, the charset it
  used and how it decided, the title, total length.

## What it will never do

- **Reach your network.** Loopback, private (RFC 1918), link-local, ULA and
  the other reserved ranges are refused — judged on the addresses a name
  actually resolves to, at connection time, on every redirect hop. Cloud
  metadata endpoints are in that set. An operator who wants LAN access sets
  `allow_private = true` in a config file on the default search path; no
  flag, MCP argument or `--config` file can.
- **Carry credentials.** No cookies, no `Authorization`, no custom headers, no
  `user:pass@` in URLs (rejected, also in redirect targets). Authenticated pages
  are out of reach by construction.
- **Use a proxy.** `HTTP_PROXY` and friends are ignored; a proxy would blind
  the address check.
- **Store anything.** No disk cache. The MCP server keeps a page in memory for
  ten minutes so paging does not refetch; the CLI keeps nothing.
- **Run JavaScript, honour robots.txt, or summarise.** It is a primitive: a
  browser-like single visit that identifies itself in its `User-Agent`.

## Install

```bash
brew install nlink-jp/tap/web-fetch
```

Or take a signed and notarized binary from the
[releases](https://github.com/nlink-jp/web-fetch/releases) — darwin arm64
(zip), linux amd64 / arm64 (tar.gz), windows amd64 (zip) — or build from
source with `make build` (→ `dist/web-fetch`).

## Usage

```
web-fetch [flags] <url>
web-fetch mcp | version

  -j, --json            JSON output (the same shape the MCP tool returns)
  --format <f>          markdown | text | raw (default markdown)
  --max-chars <n>       Characters returned, 1000-200000 (default 6000)
  --offset <n>          Start position; pass next_offset from a cut result
  --timeout <dur>       Deadline from request start, 1s-120s (default 20s)
  --max-bytes <size>    Most bytes read after decoding, 64KiB-50MiB (default 5MiB)
  -c, --config <path>   Config file
```

The text output is the title, the final URL, a blank line and the text; a cut
page ends with `[truncated: 0–6000 of 26946 chars; continue with --offset 6000]`.
A non-HTML body (text, JSON) is printed as is and reported as format `raw`.
When stdout is a terminal, control characters a page put in its title or text
(escape sequences, C1 and bidi controls) are shown as `�`, and `--json`
writes them as `\uXXXX` escapes; to a pipe or a file the text is verbatim.
Errors go to stderr as one JSON object `{code, message, details}`.

## Configuration

Optional. The first existing file of `$XDG_CONFIG_HOME/web-fetch/config.toml`,
`~/.config/web-fetch/config.toml`, `~/Library/Application Support/web-fetch/config.toml`
is read; see [config.example.toml](config.example.toml) for every key and its
default. Precedence: flag > `WEB_FETCH_*` environment variable > file >
built-in default.

`[network] allow_private` is the one key with a rule of its own: it is honoured
only in a file on that default search path. A file named by `--config` or
`WEB_FETCH_CONFIG` that sets it is a startup error. The default search path is
located by `HOME` / `XDG_CONFIG_HOME` as the launching runtime hands them down
(a relative value is ignored), so a server entry that sets those variables
chooses that file: approve a project's MCP configuration only if you trust it.

## MCP server

```bash
claude mcp add web-fetch -- web-fetch mcp
```

Tools: `fetch` (arguments `url`, `format`, `max_chars`, `offset`, `fresh`) and
`get_usage` (the full reference: response schema, error codes and how to
recover from each). Errors are structured JSON (`{code, message, details}`).
The server tells the client what it is for and what it is not: pages are
fetched from this machine's IP address, so it is not the tool for a URL under
investigation — use urlscan-lookup for those.

## Exit codes

| code | meaning |
|---|---|
| 0 | fetched (a cut page is still a success) |
| 1 | the fetch failed: network, HTTP status, size cap, a refused address |
| 2 | usage or configuration error |

## Documentation

- [RFP](docs/en/web-fetch-rfp.md) — the design record: parameter matrix,
  response schema, error table, the SSRF domain, what was decided and why
- ADRs: [0001 the address guard](docs/en/adr/0001-ssrf-closed-in-the-dialer.md),
  [0002 a primitive](docs/en/adr/0002-a-primitive.md),
  [0003 golang.org/x as first-party](docs/en/adr/0003-golang-x-as-first-party.md)
- Japanese: [README.ja.md](README.ja.md), [RFP (ja)](docs/ja/web-fetch-rfp.ja.md)

## License

MIT — see [LICENSE](LICENSE).
