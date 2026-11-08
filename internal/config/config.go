package config

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Built-in defaults. They are the RFP's parameter matrix; change the matrix
// first, then this table.
const (
	DefaultFormat                = "markdown"
	DefaultMaxChars              = 6000 // keeps a Japanese page's default response under lagent's 20,000-byte inline cap (RFP §7)
	DefaultTimeout               = 20 * time.Second
	DefaultMaxBytes        int64 = 5 << 20
	DefaultMaxRedirects          = 5
	DefaultCacheTTL              = 10 * time.Minute
	DefaultCacheMaxEntries       = 32
	// UserAgentHome is the URL the default User-Agent points at, so a site
	// operator can see who fetched.
	UserAgentHome = "https://github.com/nlink-jp/web-fetch"
)

// Ranges, validated locally. Source: the RFP's parameter matrix.
const (
	MaxCharsMin              = 1000
	MaxCharsMax              = 200000
	TimeoutMin               = time.Second
	TimeoutMax               = 120 * time.Second
	MaxBytesMin        int64 = 64 << 10
	MaxBytesMax        int64 = 50 << 20
	MaxRedirectsMax          = 10
	CacheTTLMax              = time.Hour
	CacheMaxEntriesMax       = 256
)

// Formats lists the accepted output formats, in the order the manual names them.
var Formats = []string{"markdown", "text", "raw"}

// Fetch holds the [fetch] section.
type Fetch struct {
	Format         string
	MaxChars       int
	Timeout        time.Duration
	MaxBytes       int64
	UserAgent      string // "" = DefaultUserAgent(version)
	AcceptLanguage string // "" = the header is not sent
}

// Network holds the [network] section.
type Network struct {
	MaxRedirects int
	AllowPrivate bool
}

// Cache holds the [cache] section (the MCP server's in-process cache only).
type Cache struct {
	TTL        time.Duration // 0 disables the cache
	MaxEntries int
}

// Config holds resolved runtime settings.
type Config struct {
	Fetch   Fetch
	Network Network
	Cache   Cache

	// Path is the config file that was actually read, or "" when none was.
	Path string
	// Explicit reports whether Path was named by --config or WEB_FETCH_CONFIG
	// rather than found on the default search path. It is what decides
	// whether allow_private may be honoured.
	Explicit bool
}

// DefaultUserAgent is the User-Agent sent when [fetch] user_agent is empty.
func DefaultUserAgent(version string) string {
	return "web-fetch/" + version + " (+" + UserAgentHome + ")"
}

// Defaults returns the built-in configuration.
func Defaults() *Config {
	return &Config{
		Fetch:   Fetch{Format: DefaultFormat, MaxChars: DefaultMaxChars, Timeout: DefaultTimeout, MaxBytes: DefaultMaxBytes},
		Network: Network{MaxRedirects: DefaultMaxRedirects},
		Cache:   Cache{TTL: DefaultCacheTTL, MaxEntries: DefaultCacheMaxEntries},
	}
}

// Load resolves configuration. configPath is the --config flag; when it is
// empty WEB_FETCH_CONFIG is consulted, and when that is empty too the first
// existing file on the default search path is read. Environment variables
// override file values. A file named explicitly must exist; a missing default
// file is normal.
func Load(configPath string) (*Config, error) {
	cfg := Defaults()

	explicit := configPath != ""
	if !explicit {
		if v := os.Getenv(EnvConfig); v != "" {
			configPath, explicit = v, true
		}
	}
	if !explicit {
		configPath = FindConfig()
	}
	if configPath != "" {
		f, err := os.Open(configPath)
		switch {
		case err == nil:
			sections, perr := parseTOML(f)
			_ = f.Close()
			if perr != nil {
				return nil, fmt.Errorf("parse config %s: %w", configPath, perr)
			}
			if aerr := applySections(cfg, sections); aerr != nil {
				return nil, fmt.Errorf("config %s: %w", configPath, aerr)
			}
			cfg.Path, cfg.Explicit = configPath, explicit
		case os.IsNotExist(err):
			// An explicit path that does not exist is a mistake worth
			// reporting; a default path that does not exist is normal.
			if explicit {
				return nil, fmt.Errorf("config %s: %w", configPath, err)
			}
		default:
			return nil, fmt.Errorf("open config %s: %w", configPath, err)
		}
	}

	// Provenance: only the operator's own file, found where the operator
	// keeps it, may open the LAN. Checked before the environment is applied
	// so the message names the file that tried.
	if cfg.Network.AllowPrivate && cfg.Explicit {
		return nil, fmt.Errorf("config %s: [network] allow_private is honoured only in a config file on the default search path (%s); a file named by --config or %s cannot loosen the address guard",
			cfg.Path, strings.Join(SearchPaths(), ", "), EnvConfig)
	}

	if err := applyEnv(cfg); err != nil {
		return nil, err
	}
	return cfg, Validate(cfg)
}

// Validate checks every range the parameter matrix states, so a bad config or
// a bad flag fails locally.
func Validate(cfg *Config) error {
	if err := checkFormat("[fetch] format", cfg.Fetch.Format); err != nil {
		return err
	}
	if err := checkRange("[fetch] max_chars", cfg.Fetch.MaxChars, MaxCharsMin, MaxCharsMax); err != nil {
		return err
	}
	if cfg.Fetch.Timeout < TimeoutMin || cfg.Fetch.Timeout > TimeoutMax {
		return fmt.Errorf("[fetch] timeout must be between %s and %s (got %s)", TimeoutMin, TimeoutMax, cfg.Fetch.Timeout)
	}
	if cfg.Fetch.MaxBytes < MaxBytesMin || cfg.Fetch.MaxBytes > MaxBytesMax {
		return fmt.Errorf("[fetch] max_bytes must be between %s and %s (got %d bytes)", FormatBytes(MaxBytesMin), FormatBytes(MaxBytesMax), cfg.Fetch.MaxBytes)
	}
	if err := checkRange("[network] max_redirects", cfg.Network.MaxRedirects, 0, MaxRedirectsMax); err != nil {
		return err
	}
	if cfg.Cache.TTL < 0 || cfg.Cache.TTL > CacheTTLMax {
		return fmt.Errorf("[cache] ttl must be between 0 and %s (got %s)", CacheTTLMax, cfg.Cache.TTL)
	}
	if err := checkRange("[cache] max_entries", cfg.Cache.MaxEntries, 1, CacheMaxEntriesMax); err != nil {
		return err
	}
	return nil
}

// CheckFormat reports whether v is one of Formats; exported so the engine can
// validate a per-call format with the same words.
func CheckFormat(v string) error { return checkFormat("format", v) }

func checkFormat(name, v string) error {
	if slices.Contains(Formats, v) {
		return nil
	}
	return fmt.Errorf("%s must be one of %s (got %q)", name, strings.Join(Formats, ", "), v)
}

func checkRange(name string, v, lo, hi int) error {
	if v < lo || v > hi {
		return fmt.Errorf("%s must be between %d and %d (got %d)", name, lo, hi, v)
	}
	return nil
}

// knownKeys is the whole vocabulary of the file. A key outside it is a typo,
// and a typo that is silently ignored is the worst kind of configuration bug.
var knownKeys = map[string]map[string]bool{
	"fetch":   {"format": true, "max_chars": true, "timeout": true, "max_bytes": true, "user_agent": true, "accept_language": true},
	"network": {"max_redirects": true, "allow_private": true},
	"cache":   {"ttl": true, "max_entries": true},
}

func applySections(cfg *Config, sections map[string]map[string]string) error {
	for sec, kv := range sections {
		if sec == "" {
			if len(kv) > 0 {
				return fmt.Errorf("keys outside a [section] are not allowed")
			}
			continue
		}
		known, ok := knownKeys[sec]
		if !ok {
			return fmt.Errorf("unknown section [%s]", sec)
		}
		for k := range kv {
			if !known[k] {
				return fmt.Errorf("unknown key [%s] %s", sec, k)
			}
		}
	}
	// A key that is present is applied as written, an empty value included:
	// `user_agent = ""` and `accept_language = ""` mean what the example file
	// says they mean (the default agent, no header), and `max_chars = ""` is
	// not an integer. Only an absent key leaves the default in place.
	var err error
	if f := sections["fetch"]; f != nil {
		if v, ok := f["format"]; ok {
			cfg.Fetch.Format = v // "" is refused by Validate
		}
		if v, ok := f["max_chars"]; ok {
			if cfg.Fetch.MaxChars, err = parseInt(v, "[fetch] max_chars"); err != nil {
				return err
			}
		}
		if v, ok := f["timeout"]; ok {
			if cfg.Fetch.Timeout, err = ParseDuration(v); err != nil {
				return fmt.Errorf("[fetch] timeout: %w", err)
			}
		}
		if v, ok := f["max_bytes"]; ok {
			if cfg.Fetch.MaxBytes, err = ParseBytes(v); err != nil {
				return fmt.Errorf("[fetch] max_bytes: %w", err)
			}
		}
		if v, ok := f["user_agent"]; ok {
			cfg.Fetch.UserAgent = v
		}
		if v, ok := f["accept_language"]; ok {
			cfg.Fetch.AcceptLanguage = v
		}
	}
	if n := sections["network"]; n != nil {
		if v, ok := n["max_redirects"]; ok {
			if cfg.Network.MaxRedirects, err = parseInt(v, "[network] max_redirects"); err != nil {
				return err
			}
		}
		if v, ok := n["allow_private"]; ok {
			if cfg.Network.AllowPrivate, err = parseBool(v); err != nil {
				return fmt.Errorf("[network] allow_private: %w", err)
			}
		}
	}
	if c := sections["cache"]; c != nil {
		if v, ok := c["ttl"]; ok {
			if cfg.Cache.TTL, err = parseDurationOrZero(v); err != nil {
				return fmt.Errorf("[cache] ttl: %w", err)
			}
		}
		if v, ok := c["max_entries"]; ok {
			if cfg.Cache.MaxEntries, err = parseInt(v, "[cache] max_entries"); err != nil {
				return err
			}
		}
	}
	return nil
}

// Environment variables. Only the [fetch] keys and the config path have one:
// allow_private, the redirect cap and the cache are file-only by design (RFP
// parameter matrix).
const (
	EnvConfig         = "WEB_FETCH_CONFIG"
	EnvFormat         = "WEB_FETCH_FORMAT"
	EnvMaxChars       = "WEB_FETCH_MAX_CHARS"
	EnvTimeout        = "WEB_FETCH_TIMEOUT"
	EnvMaxBytes       = "WEB_FETCH_MAX_BYTES"
	EnvUserAgent      = "WEB_FETCH_USER_AGENT"
	EnvAcceptLanguage = "WEB_FETCH_ACCEPT_LANGUAGE"
)

// EnvVars lists every variable Load reads, so tests can clear them and the
// manual can enumerate them.
func EnvVars() []string {
	return []string{EnvConfig, EnvFormat, EnvMaxChars, EnvTimeout, EnvMaxBytes, EnvUserAgent, EnvAcceptLanguage}
}

// applyEnv overlays the WEB_FETCH_* variables. An empty variable is an
// unset one here — a shell cannot tell them apart the way a file can.
func applyEnv(cfg *Config) error {
	setString(&cfg.Fetch.Format, os.Getenv(EnvFormat))
	if v := os.Getenv(EnvMaxChars); v != "" {
		n, err := parseInt(v, EnvMaxChars)
		if err != nil {
			return err
		}
		cfg.Fetch.MaxChars = n
	}
	if v := os.Getenv(EnvTimeout); v != "" {
		d, err := ParseDuration(v)
		if err != nil {
			return fmt.Errorf("%s: %w", EnvTimeout, err)
		}
		cfg.Fetch.Timeout = d
	}
	if v := os.Getenv(EnvMaxBytes); v != "" {
		n, err := ParseBytes(v)
		if err != nil {
			return fmt.Errorf("%s: %w", EnvMaxBytes, err)
		}
		cfg.Fetch.MaxBytes = n
	}
	setString(&cfg.Fetch.UserAgent, os.Getenv(EnvUserAgent))
	setString(&cfg.Fetch.AcceptLanguage, os.Getenv(EnvAcceptLanguage))
	return nil
}

func setString(dst *string, v string) {
	if v != "" {
		*dst = v
	}
}

func parseInt(v, name string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return 0, fmt.Errorf("%s: %q is not an integer", name, v)
	}
	return n, nil
}

func parseBool(v string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	return false, fmt.Errorf("%q is not true or false", v)
}

// ParseDuration accepts Go durations ("20s", "2m") and bare seconds ("20").
// The value must be positive.
func ParseDuration(v string) (time.Duration, error) {
	d, err := parseDurationOrZero(v)
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, fmt.Errorf("%q is not a positive duration", v)
	}
	return d, nil
}

// parseDurationOrZero is ParseDuration that also accepts zero (the cache's
// "off").
func parseDurationOrZero(v string) (time.Duration, error) {
	v = strings.TrimSpace(v)
	if d, err := time.ParseDuration(v); err == nil {
		if d < 0 {
			return 0, fmt.Errorf("%q is negative", v)
		}
		return d, nil
	}
	s, err := strconv.ParseFloat(v, 64)
	if err != nil || s < 0 {
		return 0, fmt.Errorf("%q is not a duration (e.g. 20s) or a number of seconds", v)
	}
	return time.Duration(s * float64(time.Second)), nil
}

// ParseBytes accepts a byte count as a bare integer or with a binary suffix:
// "5MiB", "64KiB", "1GiB" (case-insensitive). Decimal suffixes (KB, MB) are
// not accepted, so a value can never mean two sizes.
func ParseBytes(v string) (int64, error) {
	s := strings.ToLower(strings.TrimSpace(v))
	mult := int64(1)
	switch {
	case strings.HasSuffix(s, "gib"):
		mult, s = 1<<30, strings.TrimSuffix(s, "gib")
	case strings.HasSuffix(s, "mib"):
		mult, s = 1<<20, strings.TrimSuffix(s, "mib")
	case strings.HasSuffix(s, "kib"):
		mult, s = 1<<10, strings.TrimSuffix(s, "kib")
	}
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%q is not a byte count (an integer, or with a KiB / MiB / GiB suffix)", v)
	}
	if n > (1<<62)/mult {
		return 0, fmt.Errorf("%q is too large", v)
	}
	return n * mult, nil
}

// FormatBytes renders a byte count with the largest exact binary suffix, so
// an error message says "5MiB" for 5242880.
func FormatBytes(n int64) string {
	switch {
	case n >= 1<<30 && n%(1<<30) == 0:
		return strconv.FormatInt(n>>30, 10) + "GiB"
	case n >= 1<<20 && n%(1<<20) == 0:
		return strconv.FormatInt(n>>20, 10) + "MiB"
	case n >= 1<<10 && n%(1<<10) == 0:
		return strconv.FormatInt(n>>10, 10) + "KiB"
	}
	return strconv.FormatInt(n, 10)
}

// SearchPaths lists where a config file is looked for, in order — the default
// search path. macOS operators expect ~/.config too, so it is searched before
// Application Support rather than instead of it.
//
// Only absolute values locate it. The XDG Base Directory specification says
// a relative XDG_CONFIG_HOME is invalid and must be ignored, and a relative
// home directory is no better: either would resolve against the process's
// working directory, which for an MCP server is usually the project, so a
// committed file could pose as the operator's own. The environment that
// names these directories is still the runtime's to trust (doc.go).
func SearchPaths() []string {
	var paths []string
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" && filepath.IsAbs(x) {
		paths = append(paths, filepath.Join(x, "web-fetch", "config.toml"))
	}
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		return paths
	}
	paths = append(paths, filepath.Join(home, ".config", "web-fetch", "config.toml"))
	if runtime.GOOS == "darwin" {
		paths = append(paths, filepath.Join(home, "Library", "Application Support", "web-fetch", "config.toml"))
	}
	return paths
}

// FindConfig returns the first existing search path, or "" when none exists.
func FindConfig() string {
	for _, p := range SearchPaths() {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// parseTOML parses the minimal subset this tool needs: [section] headers and
// key = value lines, where value is an optionally quoted string. Comments start
// with '#'. It intentionally does not support arrays, nested tables, typed
// values or backslash escapes. Vendored from the sibling tools rather than
// imported, matching the series.
//
// It reads strictly, because the file's own promise is that a mistake cannot
// fall back to a default unnoticed (config.example.toml): a line is a header
// or a key = value, with nothing after the value but a comment; a quoted
// string is closed; a section or a key appears once; a key has a value. A
// first version searched and sliced instead, and `[network] allow_private =
// true` on one line read as an empty [network] section (code-audit run-2).
func parseTOML(r io.Reader) (map[string]map[string]string, error) {
	sections := map[string]map[string]string{}
	current := ""
	sections[current] = map[string]string{}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	line := 0
	for sc.Scan() {
		line++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		if strings.HasPrefix(raw, "[") {
			end := strings.IndexByte(raw, ']')
			if end < 0 {
				return nil, fmt.Errorf("line %d: unterminated section header", line)
			}
			name := strings.TrimSpace(raw[1:end])
			if name == "" {
				return nil, fmt.Errorf("line %d: empty section name", line)
			}
			if rest := strings.TrimSpace(raw[end+1:]); rest != "" && rest[0] != '#' {
				return nil, fmt.Errorf("line %d: unexpected text after [%s]: %q (a key goes on its own line)", line, name, rest)
			}
			if _, dup := sections[name]; dup {
				return nil, fmt.Errorf("line %d: section [%s] appears twice", line, name)
			}
			current = name
			sections[current] = map[string]string{}
			continue
		}
		eq := strings.IndexByte(raw, '=')
		if eq < 0 {
			return nil, fmt.Errorf("line %d: expected key = value", line)
		}
		key := strings.TrimSpace(raw[:eq])
		if key == "" {
			return nil, fmt.Errorf("line %d: empty key", line)
		}
		val, err := parseValue(strings.TrimSpace(raw[eq+1:]))
		if err != nil {
			return nil, fmt.Errorf("line %d: %s: %w", line, key, err)
		}
		if _, dup := sections[current][key]; dup {
			return nil, fmt.Errorf("line %d: key %s appears twice in [%s]", line, key, current)
		}
		sections[current][key] = val
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return sections, nil
}

// parseValue reads one value: a "..." or '...' string, which must be closed
// and may be followed only by a comment, or a bare word up to a comment. A
// backslash in a "..." string is refused rather than read as an escape this
// reader does not implement; '...' takes it literally, as TOML does. No value
// at all is an error — `key = ""` is how to write an empty string.
func parseValue(v string) (string, error) {
	if v == "" || v[0] == '#' {
		return "", fmt.Errorf("no value (write key = \"\" for an empty string)")
	}
	if q := v[0]; q == '"' || q == '\'' {
		end := strings.IndexByte(v[1:], q)
		if end < 0 {
			return "", fmt.Errorf("unterminated %c-quoted string", q)
		}
		s := v[1 : 1+end]
		if q == '"' && strings.IndexByte(s, '\\') >= 0 {
			return "", fmt.Errorf("backslash escapes are not supported in a \"...\" string; use '...' for a literal")
		}
		if rest := strings.TrimSpace(v[2+end:]); rest != "" && rest[0] != '#' {
			return "", fmt.Errorf("unexpected text after the string: %q", rest)
		}
		return s, nil
	}
	if hash := strings.IndexByte(v, '#'); hash >= 0 {
		v = strings.TrimSpace(v[:hash])
	}
	return v, nil
}
