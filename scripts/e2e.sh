#!/usr/bin/env bash
# e2e.sh — binary-level end-to-end checks against real sites.
#
# The Go suite in e2e/ exercises the packages; this exercises the thing a user
# actually runs: the built binary, its exit codes, its stdout/stderr split, and
# the MCP stdio session. Those are contracts a package-level test cannot see.
#
# Network required. Run via `make e2e`, which builds first. Nothing is saved.

set -uo pipefail

BIN="${BIN:-dist/web-fetch}"
[ -x "$BIN" ] || { echo "FAIL: $BIN not built (run make build)"; exit 1; }
BIN="$(cd "$(dirname "$BIN")" && pwd)/$(basename "$BIN")"

pass=0; fail=0
ok()   { printf '  ok   %s\n' "$1"; pass=$((pass+1)); }
bad()  { printf '  FAIL %s\n' "$1"; fail=$((fail+1)); }

# check NAME EXPECTED_EXIT -- command...
check() {
  local name=$1 want=$2; shift 3
  local out; out=$("$@" 2>&1); local got=$?
  if [ "$got" = "$want" ]; then ok "$name"; else
    bad "$name (exit $got, want $want)"
    printf '       %s\n' "$(printf '%s' "$out" | head -3)"
  fi
}

# contains NAME NEEDLE -- command...   (stdout only)
contains() {
  local name=$1 needle=$2; shift 3
  local out; out=$("$@" 2>/dev/null)
  if printf '%s' "$out" | grep -q -- "$needle"; then ok "$name"; else
    bad "$name (no '$needle')"; printf '       %s\n' "$(printf '%s' "$out" | head -3)"
  fi
}

# stderr_contains NAME NEEDLE -- command...
stderr_contains() {
  local name=$1 needle=$2; shift 3
  local err; err=$("$@" 2>&1 >/dev/null)
  if printf '%s' "$err" | grep -q -- "$needle"; then ok "$name"; else
    bad "$name (stderr lacks '$needle')"; printf '       %s\n' "$(printf '%s' "$err" | head -3)"
  fi
}

echo "web-fetch e2e ($BIN)"

echo "exit codes and error JSON (no network for the refusals)"
check           "version exits 0"                    0 -- "$BIN" version
check           "--version exits 0"                  0 -- "$BIN" --version
check           "no arguments is a usage error"      2 -- "$BIN"
check           "unknown command"                    2 -- "$BIN" frobnicate
check           "ftp:// is scheme_not_allowed (2)"   2 -- "$BIN" ftp://files.test/x
stderr_contains "ftp:// error is JSON"               '"scheme_not_allowed"' -- "$BIN" ftp://files.test/x
check           "userinfo is invalid_url (2)"        2 -- "$BIN" http://u:p@example.com/
check           "loopback is refused (1)"            1 -- "$BIN" http://127.0.0.1:1/
stderr_contains "loopback error names the class"     '"address_not_allowed"' -- "$BIN" http://127.0.0.1:1/
check           "max_chars out of range (2)"         2 -- "$BIN" --max-chars 10 https://example.com/
check           "two URLs is a usage error"          2 -- "$BIN" https://a.test/ https://b.test/

if [ "$("$BIN" version)" != "$("$BIN" --version)" ]; then bad "version and --version differ"; else ok "version banners identical"; fi

echo "live fetches"
check    "example.com fetches (0)"                   0 -- "$BIN" https://example.com/
contains "example.com text has the heading"          "Example Domain" -- "$BIN" https://example.com/
contains "--json carries the schema"                 '"charset_source"' -- "$BIN" --json https://example.com/
contains "--format text drops markup"                "Example Domain" -- "$BIN" --format text https://example.com/
contains "flags may follow the URL"                  '"format": "raw"' -- "$BIN" https://example.com/ --json --format raw
check    "a 404 is a failed fetch (1)"               1 -- "$BIN" https://example.com/definitely-not-here-web-fetch-e2e
stderr_contains "404 error is http_status"           '"http_status"' -- "$BIN" https://example.com/definitely-not-here-web-fetch-e2e

echo "MCP stdio session"
session=$(printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' \
  '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"fetch","arguments":{"url":"https://example.com/","max_chars":1000}}}' \
  '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"fetch","arguments":{"url":"http://127.0.0.1:1/"}}}' \
  '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"get_usage","arguments":{}}}')
out=$(printf '%s\n' "$session" | "$BIN" mcp 2>/dev/null); got=$?
if [ "$got" = 0 ]; then ok "mcp exits 0 when stdin closes"; else bad "mcp exit $got"; fi
lines=$(printf '%s\n' "$out" | grep -c .)
if [ "$lines" = 5 ]; then ok "five replies for five requests (notification unanswered)"; else bad "reply count $lines"; fi
printf '%s\n' "$out" | sed -n 1p | grep -q '"protocolVersion":"2025-03-26"' && ok "initialize reports 2025-03-26" || bad "initialize"
printf '%s\n' "$out" | sed -n 2p | grep -q '"openWorldHint":true' && ok "fetch carries openWorldHint" || bad "annotations"
printf '%s\n' "$out" | sed -n 3p | grep -q 'Example Domain' && ok "fetch returns the page" || bad "fetch result"
printf '%s\n' "$out" | sed -n 3p | grep -q '\\"cache\\": \\"miss\\"' && ok "fetch result reports cache miss" || bad "cache field"
printf '%s\n' "$out" | sed -n 4p | grep -q 'address_not_allowed' && ok "loopback refused over MCP" || bad "mcp refusal"
printf '%s\n' "$out" | sed -n 5p | grep -q 'web-fetch — MCP reference' && ok "get_usage returns the manual" || bad "get_usage"

echo
echo "passed $pass, failed $fail"
[ "$fail" = 0 ]
