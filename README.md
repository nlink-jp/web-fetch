# web-fetch

Fetch one URL from this machine and get its readable text back — as a command
line tool and as a local MCP server for agent runtimes.

> **Status: in development, not yet released.** The `mcp` and `version`
> commands run; the fetch itself lands with the first release. The behaviour
> described below is the specification the code is being built to.

```
web-fetch https://go.dev/doc/go1.26                  # markdown-ish text of the page
web-fetch --format text --max-chars 3000 https://…   # plain text, first 3,000 characters
web-fetch --json https://…                           # the same result as JSON (what MCP returns)
web-fetch mcp                                        # MCP server over stdio: tools fetch, get_usage
```

## What it does

- Fetches **one URL** (`http` / `https`), follows up to 5 redirects, and
  returns the page's main content as **markdown**, **plain text**, or the
  **raw** decoded body.
- Handles HTML in any declared charset (Shift_JIS and EUC-JP included), plain
  text, and JSON. PDFs, images and other binaries are refused with a clear error.
- Bounds every response: `--max-chars` (default 6,000) and `--offset` page
  through the whole text; a cut result says so (`truncated`, `next_offset`) and
  carries a `doc_id` so you can tell when the page changed between pages.
- Reports what it found: final URL, HTTP status, content type, the charset it
  used and how it decided, the title, total length.

## What it will never do

- **Reach your network.** Loopback, private (RFC 1918), link-local, ULA and
  the other reserved ranges are refused — judged on the addresses a name
  actually resolves to, at connection time, on every redirect hop. Cloud
  metadata endpoints are in that set. An operator who wants LAN access sets
  `allow_private = true` in their own config file; nothing else can.
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

From source, until the first release:

```bash
git clone https://github.com/nlink-jp/web-fetch.git
cd web-fetch
make build          # → dist/web-fetch
```

Releases will ship signed and notarized binaries and a Homebrew formula
(`brew install nlink-jp/tap/web-fetch`).

## Configuration

Optional. The first existing file of `$XDG_CONFIG_HOME/web-fetch/config.toml`,
`~/.config/web-fetch/config.toml`, `~/Library/Application Support/web-fetch/config.toml`
is read; see [config.example.toml](config.example.toml) for every key and its
default. Precedence: flag > `WEB_FETCH_*` environment variable > file >
built-in default.

`[network] allow_private` is the one key with a rule of its own: it is honoured
only in a file on that default search path. A file named by `--config` or
`WEB_FETCH_CONFIG` that sets it is a startup error, so a project's MCP
configuration can never open the LAN.

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
- Japanese: [README.ja.md](README.ja.md), [RFP (ja)](docs/ja/web-fetch-rfp.ja.md)

## License

MIT — see [LICENSE](LICENSE).
