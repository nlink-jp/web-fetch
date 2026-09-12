# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Project scaffold: Makefile (build / build-all / test / lint / check /
  package / verify-release / brew), vendored org release scripts, lint config,
  MIT license.
- `internal/config`: sectioned-TOML config with `WEB_FETCH_*` overrides and
  range validation for every key of the RFP's parameter matrix.
  `[network] allow_private` is honoured only in a file on the default search
  path; a file named by `--config` or `WEB_FETCH_CONFIG` that sets it fails at
  startup.
- `internal/mcp`: stdio JSON-RPC 2.0 MCP server (protocol 2025-03-26) with
  tool annotations, structured `{code, message, details}` errors, JSON results
  without HTML escaping, and the embedded `get_usage` manual pinned to the
  code by meta-tests.
- CLI: `web-fetch mcp` and `web-fetch version` / `--version`.
- RFP (rev.2, Japanese canonical with English mirror) recording the design and
  the independent review that shaped it.
