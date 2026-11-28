# ADR-0004: The MCP protocol layer becomes nlink-jp/mcp — both eras

| Field | Value |
|-------|-------|
| Status | **Accepted** (2026-11-28) |
| Date | 2026-11-28 |
| Binds | web-fetch |
| Decision makers | nlink-jp maintainers |
| Triggered by | Organization ADR-030 (every MCP implementation speaks both eras and takes its protocol layer only from nlink-jp/mcp); issue nlink-jp/web-fetch#1 |

## Context

`internal/mcp/server.go` was a hand-written stdio MCP server speaking the legacy era only (the `initialize`
handshake), so a modern-only client of the 2026-07-28 revision could not reach it. web-fetch is a member of family B
(the `mcp.New(version, tools…)` shape it shares with its twin; nlink-jp/mcp's server-migration §1), whose reference
implementation is shodan-lookup (its ADR-0001). The re-measured peers of 2026-11-27 are in asn-lookup's ADR-0001:
Claude Code 2.1.311, gem-agent and lagent are all dual-era.

## Decision

Follow the common policy (nlink-jp/mcp's `docs/en/server-migration.md`, v0.3.0) as shodan-lookup does:

1. **The shape seen from outside stays (policy §2).** `New(version, tools…)`, `Tools()` and `Serve(ctx, stdin, stdout)` keep their shape. Inside
   `Serve` is a `server.ToolServer`: the tools are registered in `Tools()` order (get_usage last) and each handler calls
   `callTool(ctx, name, args)`. The hand-written read/write loop, handshake and `rpcError` go.
2. **Options (policy §2)**: `Workers` 1, `QueueDepth` 256, a `slog` to stderr (Warn and above). Name, version and
   instructions as today.
3. **Tool failures (policy §3)**: strict decoding through `decodeArgs` and the isError texts stay; argument names are
   now matched exactly.
4. **Behaviour that changes (policy §4)**:
   - An unknown tool is -32602 instead of an isError `invalid_argument` naming the tool.
   - `tools/*` and unknown methods before initialize are -32602 (an unknown method was -32601), and so are
     `arguments` that is not an object.
   - A request with `"id":null` gets -32600 (a `tools/call` with a null id was taken for a notification and not
     answered, and `initialize`, `ping` and `tools/list` were answered with a null id); so does a request without
     `"jsonrpc"`.
   - A batch gets -32600 (it was a parse error); a parse error is answered "parse error" without the decoder's detail. A line over 16 MiB gets -32600 and reading goes on (it ended the
     server with an error).
   - A panic in a tool is answered -32603 by the module and the server goes on (it was an isError `internal_error` result from `callTool`'s own recover); up to 256 calls wait, more get -32603 "server busy".
   - The legacy negotiation (2025-11-25, 2025-06-18, 2024-11-05) is the module's now, and the modern era is added.
5. **Cancellation (policy §5)**: `notifications/cancelled` now cancels the call's ctx. A cancelled call stops its fetch (a test pins that the handler's ctx ends); nothing is stored on disk.
6. **Signals (policy §2)**: the `mcp` subcommand passes `Serve` a ctx cancelled by SIGINT and SIGTERM.
7. **Dependency and version**: `github.com/nlink-jp/mcp` is added; minor (v0.3.0).
8. **Panics (policy §4)**: a tool handler that panics is answered -32603 by the module, which keeps serving; `callTool`'s own recover, which returned an isError `internal_error` result, goes. `internal_error` stays for a result or an error the layer cannot encode.

## Defects found (each fixed in its own fix commit)

- An argument spelt in another case (`{"URL": ...}`) was accepted as the declared one; it is now refused by name.
- SIGINT or SIGTERM ended the MCP server by killing the process; a signal now ends it through its context, so its cleanup runs.
- A result the server could not encode was reported as the caller's argument error; it is now `internal_error`.

## Consequences

- Modern-only clients reach it too; legacy clients are answered as before.
- Protocol-layer updates arrive through the module.
- Tests replay their requests through the module's `client`; tests of protocol details went (the module tests them);
  a test against the three probe clients was added.

## Alternatives considered

| Alternative | Why not |
|---|---|
| Add `server/discover` to the hand-written layer | Leaves the copy of the handshake that ADR-030 §6 forbids |
| Keep `QueueDepth` at the default 16 | The 17th call sent side by side would be refused as "server busy"; before the move it waited |

## References

- Organization ADR-030, ADR-025, ADR-021 §4
- nlink-jp/mcp v0.3.0: `docs/en/server-migration.md`
- shodan-lookup ADR-0001 (family B's reference); asn-lookup ADR-0001 (the re-measured peers)
- Issue nlink-jp/web-fetch#1
