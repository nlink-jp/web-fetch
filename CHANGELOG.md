# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- **A page can no longer forge a markdown link past the scheme allow-list.**
  The renderer judged an href's scheme on the parsed URL but wrote the link
  as `[text](url)` by concatenation, and net/url keeps `( ) [ ]` as written
  in a path or query, so an href of `?)[x](javascript:alert(1)` — or anchor
  text of `a](javascript:b)[c` — ended the real link early and left a second,
  page-authored link whose target never passed the allow-list. Link text now
  escapes `\`, `[` and `]`, and the destination percent-encodes `( ) [ ] < >`
  and the space; a URL with parentheses in its path reads as
  `…/Go_%28language%29`, which resolves to the same resource. Found by the
  code-audit skill's second run over this repository.
- **The config file is read strictly.** The reader searched and sliced each
  line, so text after a value or a `[section]` header was dropped (`[network]
  allow_private = true` on one line read as an empty section), a repeated
  section or key silently won, an unclosed quote was kept as text, and a key
  with an empty value (`timeout =`, `max_chars = ""`) read as "not set" and
  fell back to the default — the opposite of what the example file promises.
  Each of those is a startup error now, with the line number. A key that is
  present is applied as written: `user_agent = ""` and `accept_language = ""`
  keep their documented meaning, `max_chars = ""` is not an integer. A
  backslash inside a `"…"` string is refused (no escapes are implemented;
  `'…'` takes it literally). Found by the code-audit skill's second run over
  this repository.
- **A failed fetch's error line is made inert on a terminal too.** The CLI
  made page text inert on stdout but wrote the error JSON to stderr as
  encoded, and that JSON quotes the remote side: the `Content-Type` it sent
  (`details.content_type`) and the URL its redirects chose
  (`details.final_url`, and the message). `encoding/json` leaves C1 and the
  bidi controls raw, so a site could reorder what the operator read in the
  error line. When stderr is a terminal, the error JSON now gets the same
  `\uXXXX` escapes as `--json` on stdout, and web-fetch's own one-line
  messages are made inert the same way; to a pipe the bytes are unchanged.
  Found by the code-audit skill's second run over this repository.
- **A page's `<meta http-equiv="content-type">` can no longer end the MCP
  server.** The charset prescan lower-cased the attribute with
  `strings.ToLower` and applied the index it found to the original string;
  the attribute is the page's undecoded bytes, and ToLower rewrites each
  invalid byte as three (U+FFFD) and shortens some runes, so a handful of
  `0xFF` bytes before `charset=` put the index past the end and the process
  panicked — the stdio server with it, until the runtime restarted it. The
  search now folds ASCII case only, at offsets of the string it slices. As a
  second layer, a tool handler that panics answers `internal_error` (an
  `isError` result; the next request is served) instead of ending the
  server; the panic value is not shown to the model. Found by the code-audit
  skill's second run over this repository.
- **A `<pre>` of backticks no longer costs the square of its length to
  render.** The markdown fence around a code block must be longer than any
  backtick run inside it; the renderer found that length by growing the
  fence one backtick at a time and searching the whole block again at every
  step, uncharged by the render budget, so a few KiB of gzip carrying a
  megabyte of backticks pinned one core for minutes on every page turn. The
  longest run is now found in one pass, and that pass is charged to the
  budget like a write. Found by the code-audit skill's second run over this
  repository.
- **The Linux archives no longer record the builder's user and group names.**
  macOS `tar` stores each staging file's owner names (`magi:wheel`-style) in
  the archive headers unless told otherwise, so every published `.tar.gz`
  carried the release machine's login. `make package` now passes `--uid 0
  --gid 0 --uname root --gname root`, and `make verify-release` fails any
  Linux archive whose members are not owned by `root:root` (0:0). Found by
  the code-audit skill's first run over this repository.
- **Release binaries no longer embed the builder's home directory.** Every
  `go build` in the Makefile ran without `-trimpath`, so each published
  binary recorded the absolute paths of the checkout and the module cache —
  the release machine's login name and directory layout. `make build` and
  `make build-all` now pass `-trimpath`, and `make verify-release` fails any
  packaged binary (darwin zip and linux archives) that contains the builder's
  `$HOME`. Found by the code-audit skill's first run over this repository.
- **A relative `XDG_CONFIG_HOME` or `HOME` no longer locates the default
  search path.** The XDG Base Directory specification says a relative value
  is invalid and must be ignored; web-fetch joined it as given, so it
  resolved against the working directory — for an MCP server usually the
  project — where a committed `allow_private = true` file could pose as the
  operator's own. Both are now required to be absolute. README stated that a
  project's MCP configuration "can never open the LAN"; it now says what the
  design record says: the `--config` / `WEB_FETCH_CONFIG` file is refused,
  and the environment that locates the default search path is the launching
  runtime's to trust. Found by the code-audit skill's first run over this
  repository.
- **Page text printed to a terminal is made inert.** The CLI wrote a page's
  title and text to stdout as fetched, so escape sequences a page carried
  (screen clears, OSC window titles and hyperlinks, clipboard writes where
  the terminal allows them) and bidi overrides were interpreted by the
  operator's terminal. When stdout is a terminal, C0 (except newline and
  tab), DEL, C1 and the bidi controls in the title, final URL and text are
  now shown as U+FFFD, and `--json` writes them as `\uXXXX` escapes; output
  to a pipe or file is unchanged. Found by the code-audit skill's first run
  over this repository.
- **A page's `<base href="javascript:…">` can no longer put a `javascript:`
  URL into a markdown link.** The renderer judged an href's scheme before
  resolving it against the base, so a fragment- or query-only href (`#top`,
  `?q`) had no scheme to refuse and took the base's. The scheme is now judged
  after resolution, against an allow-list (http, https, mailto, or none when
  there is no base) instead of a deny-list of two, so `vbscript:`, `file:` and
  app schemes render as text as well; a `<base>` outside http(s) is ignored in
  favour of the page URL. Found by the code-audit skill's first run over this
  repository.
- **A page's nested blocks can no longer render to many times its size.**
  Markdown rendering gives every line one prefix per enclosing blockquote or
  list item, so hundreds of nested levels around many short lines — within
  the default `max_bytes` — rendered to about a gigabyte, with far more copying
  on the way, before the engine paged it. Rendering now shares one byte budget
  across every nesting level (16 x the decoded size + 1 MiB of writes, 4 x +
  1 MiB of text) and such a page is refused as `too_large`
  (`details.render_limit`) within a fraction of a second. An `<ol start>`
  value is honoured only within nine digits, because the marker's width
  indents every continuation line. Found by the code-audit skill's first run
  over this repository.
- **`make verify-release` now fails closed.** Its last block chained unzip, the
  packaged binary's `--version` and `spctl` with `&&` and ended the whole chain
  in `|| true`, so a zip that did not unpack or a binary that did not run exited
  0 and the upload proceeded. Each step is now judged on its own, the packaged
  binary's `--version` must contain the tag being released, and only the
  informational `spctl` line may be ignored. Matches the org template
  (CONVENTIONS.md §Code Signing → Verifying a release).
- **The Linux archives no longer carry macOS file metadata.** macOS `tar` wrote
  each bundled file's extended attributes (`com.apple.provenance`, and a Dropbox
  attribute where the tree is synced) into the `.tar.gz` twice: as AppleDouble
  `._` members, which GNU tar extracts as stray `._<name>` files beside the real
  ones, and as `LIBARCHIVE.xattr.*` / `SCHILY.xattr.*` pax headers, which it
  reports as unknown keywords. `make package` now archives with
  `COPYFILE_DISABLE=1 tar --no-xattrs`; each setting stops one of the two.
  Archives already published still carry them; the files themselves are
  unaffected.

### Internal

- The `unsupported_content_type` message quotes the declared `Content-Type`
  (`… is "application/pdf", which web-fetch does not extract`), as the
  `Content-Encoding` and charset labels already were: the header is the
  remote site's text, and the quote marks say so inside a sentence in
  web-fetch's voice. A body with no declared type says `declares no media
  type and sniffs as "…"`. `details.content_type` is unchanged. Raised by the
  code-audit skill's first run over this repository as a point to validate;
  the runtime's nonce wrap covers error results too (lagent wraps every tool
  result), so this is hardening, not a fix.
- `make verify-release` also judges each Linux archive: no AppleDouble or other
  macOS metadata members — listed with `--options 'tar:!mac-ext'`, because a
  plain macOS listing folds `._` members away — no extended attributes as pax
  headers, and exactly the canonical binary, `README.md` and `LICENSE`, compared
  in the C locale.
- The Linux-archive check in `make verify-release` reads each archive's pax
  headers with Python's `tarfile` instead of grepping the decompressed stream,
  which also matched file text that names the keywords (a bundled CHANGELOG,
  for one).

## [0.1.1] - 2026-09-21

### Fixed

- Both MCP tool schemas now set `additionalProperties: false`, as
  organization ADR-021 §10 requires. A validating client refuses a mistyped
  argument instead of forwarding it; `mcp.DecodeArgs` already decoded
  strictly, so the two halves of the contract now agree. Applied inside
  `mcp.New`, the one place every tool passes through, so a tool added later
  cannot omit it.

### Added

- `TestEveryToolSchemaIsClosed` and `TestClosedSchemaSurvivesTheWire` — the
  arch assertion over the real tool set, checked both on the Go value and in
  the `tools/list` JSON a client actually validates against.

## [0.1.0] - 2026-09-12

First release.

### Added

- `web-fetch <url>`: fetch one http/https URL from this machine and print its
  body as markdown (default), plain text or the raw decoded body; `--json`
  prints the same result the MCP tool returns; `--max-chars` / `--offset` page
  through the whole text, and a cut result says so.
- MCP server (`web-fetch mcp`, stdio, protocol 2025-03-26) with the tools
  `fetch` (arguments `url`, `format`, `max_chars`, `offset`, `fresh`) and
  `get_usage`; tool annotations; structured `{code, message, details}` errors;
  an in-process TTL cache so paging does not refetch.
- The address guard (ADR-0001): a finite table of denied classes judged inside
  the HTTP dialer on the addresses a name actually resolves to, embedded IPv4
  (mapped, compatible, NAT64, 6to4, Teredo) unwrapped, every redirect hop
  judged again, redirect targets with `user:password@` or a non-http(s) scheme
  refused, no cookies, no Authorization, proxy environment ignored.
- `[network] allow_private` is honoured only in a config file on the default
  search path; a file named by `--config` or `WEB_FETCH_CONFIG` that sets it
  fails at startup.
- Charset handling in WHATWG order (BOM, header, meta prescan, UTF-8 validity,
  windows-1252), reported as `charset` / `charset_source`; Shift_JIS, EUC-JP
  and ISO-2022-JP included; a declared unknown label is `charset_unsupported`.
- Extraction: body root `<main>` → `[role=main]` → largest `<article>` →
  `<body>`; navigation, footers, asides, forms, scripts, hidden elements and
  body-level headers dropped; `<base href>` honoured; `doc_id` (SHA-256 of the
  decoded body) so page turns can tell the page changed.
- Byte cap on the decoded stream (`too_large`, never truncation); non-gzip
  `Content-Encoding` refused; PDFs and binaries refused
  (`unsupported_content_type`).
- Configuration (`config.toml`, `WEB_FETCH_*`), Makefile, vendored org release
  scripts, live e2e suite (`make e2e`) and binary-level script.
- RFP (rev.2) and project ADRs 0001–0003; two independent reviews folded in
  before this release (the second made the renderer linear in output size,
  renders nested tables once, limits IPv6 zone handling to IP literals, drops
  the `Referer` Go adds on redirects, refuses `image/svg+xml`, names the
  argument in MCP `invalid_argument` errors, and keeps the Go suite's status
  in `make e2e`).
