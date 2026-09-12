# ADR-0001: SSRF is closed inside the dialer, as a finite address domain

| Field | Value |
|-------|-------|
| Status | **Accepted** |
| Date | 2026-09-12 |
| Binds | web-fetch |
| Decision makers | nlink-jp maintainers |
| Triggered by | lagent ADR-0002 and gem-agent ADR-0017 declined a local fetch because it would reach localhost, the LAN, cloud metadata and authenticated pages from the operator's machine; web-fetch exists only if that class is closed by construction rather than by rules |

## Context

web-fetch fetches from the operator's machine. A provider-side fetch (Vertex
URL Context, what gem-agent uses) is not available to a local model, so the
SSRF (server-side request forgery) class that the two runtimes named is real
here: a model, or an injected instruction inside a page, asks for
`http://169.254.169.254/` or `http://localhost:1234/` and the tool obliges.

Four facts shape the defence:

1. **A rule over URL text is a rule over an infinite domain.** Hostnames
   resolve to anything; numeric spellings (`2130706433`, `0x7f.0.0.1`, `127.1`)
   are normalised by the resolver, not the parser; a public name can answer with
   a private address. Patterns over the URL cannot enumerate the cases.
2. **A pre-check followed by a dial is a TOCTOU.** The archived
   agentic-web-search resolved the host, judged the answer, then let `net/http`
   resolve again to connect. A DNS answer that changes in between (rebinding)
   defeats the check.
3. **Go's HTTP client adds credentials on its own.** A redirect `Location`
   carrying `user:pass@` makes `http.Client` send `Authorization: Basic`; a
   cookie jar, if set, follows redirects; proxy environment variables reroute
   every connection and blind an address check.
4. **A runtime's project configuration can point the server at any file.**
   lagent and Claude Code start MCP servers from a project's `.mcp.json` with
   arbitrary `args` and `env`. A setting that loosens the guard must not be
   reachable from there.

## Decision

1. **The judgment lives in the dialer.** `http.Transport.DialContext` is
   replaced by `guard.Dialer`, which resolves the `host:port` it is handed,
   judges **every** returned address, and connects to a vetted address **as an
   `ip:port` literal**. There is no code path that dials by name. Check and
   connect are one operation. A name that resolves to zero addresses is
   `dns_failed`. Several vetted addresses are tried in order by the dialer
   itself. TLS SNI and certificate verification keep using the URL's host name,
   because only the TCP dial is replaced.
2. **The denied domain is one finite table**, `guard.table`, pinned row by row
   by tests: loopback, private (RFC 1918), link-local (169.254/16 including
   cloud metadata, fe80::/10), site-local (fec0::/10), unique-local (fc00::/7),
   unspecified, broadcast, multicast, shared address space (100.64/10), and the
   reserved blocks 0/8, 192.0.0/24, 198.18/15, 240/4, 2001:2::/48. An IPv6 address that
   embeds an IPv4 address — IPv4-mapped, IPv4-compatible, NAT64 (64:ff9b::/96
   and 64:ff9b:1::/48), 6to4 (2002::/16) and Teredo (2001::/32, inverted) — is
   unwrapped and the embedded address judged instead. **One denied address
   denies the whole answer.** Ports are not judged: they do not change which
   machine is reached.
3. **Every redirect hop passes the same dialer.** In addition, `CheckRedirect`
   refuses a `Location` that carries userinfo (`invalid_url`, with the hop) or a
   scheme other than http(s) (`scheme_not_allowed`), and caps the hop count
   (`too_many_redirects`).
4. **The client carries no credentials.** No cookie jar, no `Authorization`,
   no caller-supplied headers, and userinfo is rejected in the input URL as
   well as in every redirect target. The `Referer` Go adds on a redirect is
   removed too: only headers this tool chose are sent.
5. **Proxy environment variables are ignored** (`Transport.Proxy` is nil).
6. **`[network] allow_private = true` is honoured only in a config file found
   on the default search path.** A file reached via `--config` or
   `WEB_FETCH_CONFIG` that sets it is a startup error. No flag and no MCP
   argument exists for it. The environment that locates the default search path
   (`HOME`, `XDG_CONFIG_HOME`) is handed down by the runtime; lagent and Claude
   Code read a project's `.mcp.json` only after their trust prompt, so that
   boundary is theirs and is recorded here as the residual.

## Consequences

- Happy Eyeballs is gone: a dual-stack host with one dead address family
  connects after the first attempt fails, not in parallel.
- In a DNS64 environment the NAT64 answer is unwrapped and the inner IPv4
  judged — a public inner address is allowed, a private one refused.
- A local development server is unreachable unless the operator opts in through
  their own global config file. Tests therefore inject the resolver and the
  connect function: names resolve to public fixtures, and the connect function
  is pointed at the `httptest` listener while asserting it received the vetted
  literal.
- Adding a class is a table row plus a test row. A hostname or URL-pattern
  rule is never added; a proposal to add one is a sign the table has a gap.
- Numeric IPv4 spellings end as `address_not_allowed` or `dns_failed`
  depending on the resolver implementation; both are refusals.
- A page that redirects to a private address fails at that hop with the class
  named, after the public hops were fetched — the `redirects` field shows the
  path.

## Alternatives considered

- **Pre-check, then let `net/http` dial** (the archived implementation) —
  rejected: a rebinding window between check and connect.
- **Hostname or URL-pattern deny lists** — rejected: an infinite domain, and
  exactly the rule class the organization's guidance says to replace with a
  bounded one.
- **Stop at a cross-host redirect and demand a second call** — rejected: every
  hop passes the same dialer, so following and reporting `final_url` and
  `redirects` is simpler and no less safe.
- **Restrict ports** — rejected: the address decides which machine is reached;
  a port rule would refuse legitimate public services and protect nothing the
  table does not.
- **`allow_private` as a flag or MCP argument** — rejected: a permission a
  project file or a model could grant itself.
- **A hard-coded allow of loopback for tests** — rejected: a test hook in the
  production surface. Injecting the resolver and connect function keeps the
  table live in tests.

## References

- RFP §3 "SSRF は文字列規則ではなく有限の拒否領域で閉じる", §7 (residuals)
- lagent ADR-0002 (features not reproduced), gem-agent ADR-0017 (web tools)
- nlink-jp/knowledge `security.md`: "Path confinement that checks a resolved
  path and then opens the original is a TOCTOU"; "A constraint derived from
  conversation may only ever tighten — one that can loosen is a permission";
  "Bound an agent's shell with the kernel's cage, not with text rules"
- `internal/guard` (the table, `Classify`, `Dialer`), `internal/config`
  (the provenance check)
