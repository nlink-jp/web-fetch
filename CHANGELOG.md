# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
- RFP (rev.2) and project ADRs 0001–0003.
