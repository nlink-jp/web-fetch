# web-fetch — MCP reference

web-fetch fetches **one URL from this machine** and returns its body as
extracted text. It is a primitive: it sends no cookies, no Authorization and no
custom headers, follows no proxy, keeps nothing on disk, and never reaches a
loopback, private, link-local or otherwise reserved address (an
`address_not_allowed` error says so). The site sees a visit from this machine's
IP, so this server is **not** for URLs under investigation — those belong to
urlscan-lookup.

The design record is the RFP in `docs/`. This manual is what the code does.

## Tools

### `get_usage`

No arguments. Returns this manual.

The fetch tool arrives with Phase 1 of the RFP; until then this server
registers `get_usage` alone.

## Errors

A tool failure is a result (`isError: true`) whose text is one JSON object:
`{"code": "...", "message": "...", "details": {...}}`. Branch on `code`.

| code | meaning | recovery |
|---|---|---|
| `invalid_argument` | an unknown tool, or an argument that is missing, unknown, mistyped or out of range | fix the call; `message` names the argument, `details.allowed` the accepted values when there is a finite set |
