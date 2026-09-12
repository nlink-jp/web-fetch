// Package mcp is a dependency-free stdio JSON-RPC 2.0 MCP server. It is the
// skeleton vendored from brave-search, extended with tool annotations
// (readOnlyHint, openWorldHint — MCP 2025-03-26), which is why the protocol
// revision it reports moved from 2024-11-05. Annotations are additive; a
// client on the older revision ignores them.
//
// get_usage is canonical: the mcp-tactics skill deliberately documents no
// parameters, so the embedded manual is what an agent reads before its first
// call. Tool errors are structured JSON ({code, message, details}).
//
// notifications/cancelled is received and ignored: a fetch that has been
// dispatched completes or times out on its own, and the specification allows
// a receiver to ignore the notification when the request cannot be cancelled.
// A closing stdin is the shutdown signal.
package mcp
