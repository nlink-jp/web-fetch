package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// isolate points HOME and XDG_CONFIG_HOME at a fresh directory and clears
// every WEB_FETCH_* variable, so no test can read the developer's real
// config. It returns the default-search-path file the test may create.
func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	xdg := filepath.Join(home, "xdg")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	for _, k := range EnvVars() {
		t.Setenv(k, "")
	}
	return filepath.Join(xdg, "web-fetch", "config.toml")
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultsWithoutAFile(t *testing.T) {
	isolate(t)
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	want := Defaults()
	if cfg.Fetch != want.Fetch || cfg.Network != want.Network || cfg.Cache != want.Cache {
		t.Errorf("defaults changed: got %+v, want %+v", cfg, want)
	}
	if cfg.Path != "" || cfg.Explicit {
		t.Errorf("no file was read, yet Path=%q Explicit=%v", cfg.Path, cfg.Explicit)
	}
	if cfg.Fetch.MaxChars != 6000 || cfg.Fetch.MaxBytes != 5<<20 || cfg.Fetch.Timeout != 20*time.Second {
		t.Errorf("the RFP's defaults drifted: %+v", cfg.Fetch)
	}
}

func TestFileOnTheDefaultSearchPath(t *testing.T) {
	path := isolate(t)
	writeFile(t, path, `
[fetch]
format = "text"
max_chars = 2000
timeout = "5s"
max_bytes = "1MiB"
user_agent = "probe/1"
accept_language = "ja, en;q=0.8"

[network]
max_redirects = 2
allow_private = true

[cache]
ttl = "0"
max_entries = 4
`)
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Path != path || cfg.Explicit {
		t.Errorf("Path=%q Explicit=%v, want the default-path file, not explicit", cfg.Path, cfg.Explicit)
	}
	want := Fetch{Format: "text", MaxChars: 2000, Timeout: 5 * time.Second, MaxBytes: 1 << 20, UserAgent: "probe/1", AcceptLanguage: "ja, en;q=0.8"}
	if cfg.Fetch != want {
		t.Errorf("fetch: got %+v, want %+v", cfg.Fetch, want)
	}
	if cfg.Network != (Network{MaxRedirects: 2, AllowPrivate: true}) {
		t.Errorf("network: %+v", cfg.Network)
	}
	if cfg.Cache != (Cache{TTL: 0, MaxEntries: 4}) {
		t.Errorf("cache: %+v", cfg.Cache)
	}
}

// The provenance rule: allow_private is honoured only where the operator
// keeps the file. A runtime's project configuration can name any file
// through --config or WEB_FETCH_CONFIG, so neither may open the LAN.
func TestAllowPrivateIsRefusedFromAnExplicitFlagPath(t *testing.T) {
	isolate(t)
	other := filepath.Join(t.TempDir(), "project.toml")
	writeFile(t, other, "[network]\nallow_private = true\n")
	_, err := Load(other)
	if err == nil || !strings.Contains(err.Error(), "allow_private") || !strings.Contains(err.Error(), "default search path") {
		t.Fatalf("an explicit file must not loosen the guard; got %v", err)
	}
}

func TestAllowPrivateIsRefusedFromTheEnvConfigPath(t *testing.T) {
	isolate(t)
	other := filepath.Join(t.TempDir(), "env.toml")
	writeFile(t, other, "[network]\nallow_private = true\n")
	t.Setenv(EnvConfig, other)
	_, err := Load("")
	if err == nil || !strings.Contains(err.Error(), EnvConfig) {
		t.Fatalf("WEB_FETCH_CONFIG must not loosen the guard; got %v", err)
	}
}

func TestExplicitFileMayStillTightenOrSetAnythingElse(t *testing.T) {
	isolate(t)
	other := filepath.Join(t.TempDir(), "project.toml")
	writeFile(t, other, "[network]\nallow_private = false\nmax_redirects = 0\n[fetch]\nmax_chars = 1000\n")
	cfg, err := Load(other)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Explicit || cfg.Path != other || cfg.Network.MaxRedirects != 0 || cfg.Fetch.MaxChars != 1000 {
		t.Errorf("explicit file not applied: %+v", cfg)
	}
}

func TestExplicitMissingFileIsAnError(t *testing.T) {
	isolate(t)
	if _, err := Load(filepath.Join(t.TempDir(), "missing.toml")); err == nil {
		t.Fatal("an explicit --config that does not exist must be reported")
	}
	t.Setenv(EnvConfig, filepath.Join(t.TempDir(), "missing-too.toml"))
	if _, err := Load(""); err == nil {
		t.Fatal("a WEB_FETCH_CONFIG that does not exist must be reported")
	}
}

func TestUnknownKeysAndSectionsAreRejected(t *testing.T) {
	for name, body := range map[string]string{
		"unknown key":         "[fetch]\nmax_char = 100\n",
		"unknown section":     "[api]\nkey = \"x\"\n",
		"key outside section": "format = \"text\"\n",
		"bad bool":            "[network]\nallow_private = yes\n",
		"bad int":             "[fetch]\nmax_chars = many\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := isolate(t)
			writeFile(t, path, body)
			if _, err := Load(""); err == nil {
				t.Errorf("%s accepted: %q", name, body)
			}
		})
	}
}

// The reader is strict (code-audit run-2, 2026-11-08): it used to search and
// slice, so text after a value or a header was dropped, a repeated section
// or key silently won, and a present empty value read as "not set". Each
// shape is a startup error now, so a mistake cannot fall back to a default
// unnoticed — the promise config.example.toml makes.
func TestMalformedLinesAreRejected(t *testing.T) {
	for name, body := range map[string]string{
		"text after a quoted value":   "[network]\nmax_redirects = \"0\" 5\n",
		"key on the header line":      "[network] allow_private = true\n",
		"text after a header":         "[network] x\nmax_redirects = 1\n",
		"unterminated string":         "[fetch]\nformat = \"text\n",
		"escape in a basic string":    "[fetch]\nuser_agent = \"a\\\"b\"\n",
		"repeated section":            "[cache]\nttl = \"0\"\n[cache]\nttl = \"1h\"\n",
		"repeated key":                "[cache]\nttl = \"0\"\nttl = \"1h\"\n",
		"no value":                    "[fetch]\ntimeout =\n",
		"comment for a value":         "[fetch]\ntimeout = # later\n",
		"empty string for an integer": "[fetch]\nmax_chars = \"\"\n",
		"empty string for a format":   "[fetch]\nformat = \"\"\n",
		"empty string for a duration": "[fetch]\ntimeout = \"\"\n",
		"empty string for a bool":     "[network]\nallow_private = \"\"\n",
		"empty section name":          "[]\nformat = \"text\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := isolate(t)
			writeFile(t, path, body)
			_, err := Load("")
			if err == nil {
				t.Fatalf("accepted: %q", body)
			}
			t.Logf("%v", err)
		})
	}
}

// What strictness must not take away: comments after values and headers,
// a '#' inside quotes, a literal backslash in '...', and the two keys whose
// empty string has a documented meaning.
func TestWellFormedShapesAreRead(t *testing.T) {
	path := isolate(t)
	writeFile(t, path, `
[fetch] # the section
format = "text" # trailing comment
max_chars = 2000 # bare value, comment
user_agent = 'probe\1 #not-a-comment'
accept_language = ""

[network]
max_redirects = 3
`)
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	want := Fetch{Format: "text", MaxChars: 2000, Timeout: DefaultTimeout, MaxBytes: DefaultMaxBytes, UserAgent: "probe\\1 #not-a-comment", AcceptLanguage: ""}
	if cfg.Fetch != want {
		t.Errorf("fetch: got %+v, want %+v", cfg.Fetch, want)
	}
	if cfg.Network.MaxRedirects != 3 {
		t.Errorf("network: %+v", cfg.Network)
	}
	// user_agent = "" is the default agent, as the example file says.
	path = isolate(t)
	writeFile(t, path, "[fetch]\nuser_agent = \"\"\nformat = \"text\"\n")
	cfg, err = Load("")
	if err != nil || cfg.Fetch.UserAgent != "" || cfg.Fetch.Format != "text" {
		t.Errorf("empty user_agent: %v %+v", err, cfg.Fetch)
	}
}

func TestEnvironmentOverridesTheFile(t *testing.T) {
	path := isolate(t)
	writeFile(t, path, "[fetch]\nmax_chars = 2000\nformat = \"text\"\ntimeout = \"5s\"\nmax_bytes = \"1MiB\"\n")
	t.Setenv(EnvMaxChars, "3000")
	t.Setenv(EnvFormat, "raw")
	t.Setenv(EnvTimeout, "7")
	t.Setenv(EnvMaxBytes, "2MiB")
	t.Setenv(EnvUserAgent, "ua/2")
	t.Setenv(EnvAcceptLanguage, "en")
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	want := Fetch{Format: "raw", MaxChars: 3000, Timeout: 7 * time.Second, MaxBytes: 2 << 20, UserAgent: "ua/2", AcceptLanguage: "en"}
	if cfg.Fetch != want {
		t.Errorf("got %+v, want %+v", cfg.Fetch, want)
	}
}

func TestRangesAreValidated(t *testing.T) {
	cases := map[string]func(*Config){
		"max_chars low":     func(c *Config) { c.Fetch.MaxChars = MaxCharsMin - 1 },
		"max_chars high":    func(c *Config) { c.Fetch.MaxChars = MaxCharsMax + 1 },
		"timeout low":       func(c *Config) { c.Fetch.Timeout = 500 * time.Millisecond },
		"timeout high":      func(c *Config) { c.Fetch.Timeout = TimeoutMax + time.Second },
		"max_bytes low":     func(c *Config) { c.Fetch.MaxBytes = MaxBytesMin - 1 },
		"max_bytes high":    func(c *Config) { c.Fetch.MaxBytes = MaxBytesMax + 1 },
		"format":            func(c *Config) { c.Fetch.Format = "html" },
		"max_redirects":     func(c *Config) { c.Network.MaxRedirects = MaxRedirectsMax + 1 },
		"cache ttl":         func(c *Config) { c.Cache.TTL = CacheTTLMax + time.Minute },
		"cache max_entries": func(c *Config) { c.Cache.MaxEntries = 0 },
	}
	for name, mutate := range cases {
		c := Defaults()
		mutate(c)
		if err := Validate(c); err == nil {
			t.Errorf("%s: out-of-range value accepted", name)
		}
	}
	if err := Validate(Defaults()); err != nil {
		t.Errorf("defaults do not validate: %v", err)
	}
}

func TestParseBytes(t *testing.T) {
	good := map[string]int64{"5MiB": 5 << 20, "64KiB": 64 << 10, "1GiB": 1 << 30, "1048576": 1 << 20, "5mib": 5 << 20, " 2 MiB ": 2 << 20}
	for in, want := range good {
		got, err := ParseBytes(in)
		if err != nil || got != want {
			t.Errorf("ParseBytes(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"5MB", "5KB", "abc", "-1", "", "1.5MiB"} {
		if _, err := ParseBytes(in); err == nil {
			t.Errorf("ParseBytes(%q) accepted", in)
		}
	}
	for n, want := range map[int64]string{5 << 20: "5MiB", 64 << 10: "64KiB", 1 << 30: "1GiB", 1500: "1500"} {
		if got := FormatBytes(n); got != want {
			t.Errorf("FormatBytes(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestParseDuration(t *testing.T) {
	for in, want := range map[string]time.Duration{"20s": 20 * time.Second, "20": 20 * time.Second, "1.5": 1500 * time.Millisecond, "2m": 2 * time.Minute} {
		got, err := ParseDuration(in)
		if err != nil || got != want {
			t.Errorf("ParseDuration(%q) = %s, %v; want %s", in, got, err, want)
		}
	}
	for _, in := range []string{"0", "-1s", "soon", ""} {
		if _, err := ParseDuration(in); err == nil {
			t.Errorf("ParseDuration(%q) accepted", in)
		}
	}
	if d, err := parseDurationOrZero("0"); err != nil || d != 0 {
		t.Errorf("the cache's 0 must parse: %s, %v", d, err)
	}
}

func TestSearchPathsPreferXDGThenHome(t *testing.T) {
	isolate(t)
	paths := SearchPaths()
	if len(paths) < 2 {
		t.Fatalf("too few search paths: %v", paths)
	}
	if !strings.HasPrefix(paths[0], os.Getenv("XDG_CONFIG_HOME")) {
		t.Errorf("first path is not under XDG_CONFIG_HOME: %s", paths[0])
	}
	if !strings.Contains(paths[1], filepath.Join(".config", "web-fetch")) {
		t.Errorf("second path is not ~/.config/web-fetch: %s", paths[1])
	}
}

// A relative XDG_CONFIG_HOME is invalid under the XDG Base Directory
// specification and is ignored; a relative HOME yields no search path at all.
// Either would otherwise resolve against the working directory, where a
// project's committed file could pose as the operator's own (code-audit
// run-1, 2026-11-08).
func TestRelativeSearchRootsAreIgnored(t *testing.T) {
	isolate(t)
	t.Setenv("XDG_CONFIG_HOME", "rel/xdg")
	for _, p := range SearchPaths() {
		if !filepath.IsAbs(p) {
			t.Errorf("relative search path %q", p)
		}
		if strings.Contains(p, "rel/xdg") {
			t.Errorf("relative XDG_CONFIG_HOME was used: %q", p)
		}
	}
	if paths := SearchPaths(); len(paths) < 1 || !strings.Contains(paths[0], filepath.Join(".config", "web-fetch")) {
		t.Errorf("with a relative XDG_CONFIG_HOME the search starts at ~/.config: %v", paths)
	}

	// The relative value must not reach a file either: a committed
	// rel/xdg/web-fetch/config.toml with allow_private = true in the
	// working directory stays unread.
	work := t.TempDir()
	t.Chdir(work)
	writeFile(t, filepath.Join(work, "rel", "xdg", "web-fetch", "config.toml"), "[network]\nallow_private = true\n")
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Network.AllowPrivate || cfg.Path != "" {
		t.Errorf("a file under a relative XDG_CONFIG_HOME was honoured: path=%q allow_private=%v", cfg.Path, cfg.Network.AllowPrivate)
	}

	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "relhome")
	if paths := SearchPaths(); len(paths) != 0 {
		t.Errorf("a relative HOME yields search paths: %v", paths)
	}
}

func TestDefaultUserAgentNamesTheToolAndItsHome(t *testing.T) {
	ua := DefaultUserAgent("v1.2.3")
	if !strings.HasPrefix(ua, "web-fetch/v1.2.3") || !strings.Contains(ua, UserAgentHome) {
		t.Errorf("UA does not identify the tool: %q", ua)
	}
}
