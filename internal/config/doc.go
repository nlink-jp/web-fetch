// Package config resolves web-fetch's settings: built-in defaults, then the
// first config file on the default search path (or the one named by --config
// / WEB_FETCH_CONFIG), then the WEB_FETCH_* environment. Every range the RFP's
// parameter matrix states is validated here, so a bad value fails at startup
// where the operator sees it rather than on the first fetch.
//
// One key is provenance-checked. [network] allow_private is honoured only in
// a file found on the default search path; a file reached through --config or
// WEB_FETCH_CONFIG that sets it true is a startup error. A runtime's project
// configuration (an .mcp.json's args or env) can point the server at any file,
// and loosening the address guard is a permission that a project must not be
// able to grant itself (RFP §3, ADR-0001). The environment that locates the
// default search path (HOME, XDG_CONFIG_HOME) is the runtime's trust boundary,
// not this package's.
//
// The sectioned-TOML reader is vendored from brave-search rather than
// imported, matching the series. It reads strictly — a malformed line, a
// repeated section or key, or a key with no value is an error, and a present
// key is applied as written, empty string included — because the file's own
// promise is that a mistake cannot fall back to a default unnoticed.
package config
