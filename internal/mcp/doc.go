// Package mcp is web-fetch's stdio MCP server, speaking both eras of the
// protocol through nlink-jp/mcp's server.ToolServer (ADR-0004). Its tools
// carry annotations (readOnlyHint, openWorldHint); a client that does not know
// them ignores them.
//
// get_usage is canonical: the mcp-tactics skill deliberately documents no
// parameters, so the embedded manual is what an agent reads before its first
// call. Tool errors are structured JSON ({code, message, details}).
//
// notifications/cancelled cancels the call's ctx, which stops its fetch. The
// server ends at end of input, answering every request that arrived, or on
// SIGINT or SIGTERM.
package mcp
