package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nlink-jp/web-fetch/internal/config"
)

// isolate keeps every test away from the developer's real config: HOME and
// XDG_CONFIG_HOME point at a fresh directory and the WEB_FETCH_* variables
// are cleared.
func isolate(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	for _, k := range config.EnvVars() {
		t.Setenv(k, "")
	}
}

func runWith(t *testing.T, stdin string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	isolate(t)
	var out, errb bytes.Buffer
	code = run(args, "v9.9.9", strings.NewReader(stdin), &out, &errb)
	return code, out.String(), errb.String()
}

func TestVersionFlagAndSubcommandAreByteIdentical(t *testing.T) {
	c1, o1, _ := runWith(t, "", "version")
	c2, o2, _ := runWith(t, "", "--version")
	c3, o3, _ := runWith(t, "", "-v")
	if c1 != exitOK || c2 != exitOK || c3 != exitOK {
		t.Fatalf("exit codes: %d %d %d", c1, c2, c3)
	}
	if o1 != o2 || o2 != o3 {
		t.Errorf("version banners differ:\n%q\n%q\n%q", o1, o2, o3)
	}
	if !strings.HasPrefix(o1, "web-fetch v9.9.9\n") {
		t.Errorf("banner must start with the name and version: %q", o1)
	}
}

func TestHelpSucceedsOnStdout(t *testing.T) {
	for _, arg := range []string{"help", "-h", "--help"} {
		code, out, errb := runWith(t, "", arg)
		if code != exitOK || !strings.Contains(out, "Usage:") || errb != "" {
			t.Errorf("%s: code=%d stdout=%q stderr=%q", arg, code, out, errb)
		}
	}
}

func TestNoArgumentsAndUnknownCommandAreUsageErrors(t *testing.T) {
	code, out, errb := runWith(t, "")
	if code != exitError || out != "" || !strings.Contains(errb, "Usage:") {
		t.Errorf("no args: code=%d stdout=%q stderr=%q", code, out, errb)
	}
	code, _, errb = runWith(t, "", "frobnicate")
	if code != exitError || !strings.Contains(errb, `unknown command "frobnicate"`) {
		t.Errorf("unknown command: code=%d stderr=%q", code, errb)
	}
}

func TestIsURLRecognisesOnlySchemedArguments(t *testing.T) {
	for arg, want := range map[string]bool{
		"https://example.org/":  true,
		"HTTP://EXAMPLE.ORG":    true,
		"mcp":                   false,
		"example.org":           false,
		"ftp://example.org/":    false,
		"file:///etc/hosts":     false,
		"httpsx://example.org/": false,
	} {
		if got := isURL(arg); got != want {
			t.Errorf("isURL(%q) = %v, want %v", arg, got, want)
		}
	}
}

func TestMCPServesUntilStdinCloses(t *testing.T) {
	session := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"
	code, out, errb := runWith(t, session, "mcp")
	if code != exitOK || errb != "" {
		t.Fatalf("mcp: code=%d stderr=%q", code, errb)
	}
	if !strings.Contains(out, `"protocolVersion"`) || !strings.Contains(out, `"get_usage"`) {
		t.Errorf("mcp session output: %q", out)
	}
}

func TestMCPRefusesArgumentsAndABrokenConfig(t *testing.T) {
	code, _, errb := runWith(t, "", "mcp", "extra")
	if code != exitError || !strings.Contains(errb, "takes no arguments") {
		t.Errorf("mcp extra: code=%d stderr=%q", code, errb)
	}
	bad := filepath.Join(t.TempDir(), "bad.toml")
	if err := os.WriteFile(bad, []byte("[fetch]\nmax_chars = 10\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errb = runWith(t, "", "mcp", "--config", bad)
	if code != exitError || !strings.Contains(errb, "max_chars") {
		t.Errorf("mcp bad config: code=%d stderr=%q", code, errb)
	}
}

// The provenance rule reaches the CLI: a project-supplied file cannot open
// the LAN, and the refusal happens at startup, not on the first call.
func TestMCPRefusesAllowPrivateFromAnExplicitConfig(t *testing.T) {
	loosen := filepath.Join(t.TempDir(), "project.toml")
	if err := os.WriteFile(loosen, []byte("[network]\nallow_private = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errb := runWith(t, `{"jsonrpc":"2.0","id":1,"method":"ping"}`+"\n", "mcp", "-c", loosen)
	if code != exitError || out != "" || !strings.Contains(errb, "allow_private") {
		t.Errorf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
}

func TestMCPHelpFlag(t *testing.T) {
	code, out, _ := runWith(t, "", "mcp", "-h")
	if code != exitOK || !strings.Contains(out, "Usage:") {
		t.Errorf("mcp -h: code=%d stdout=%q", code, out)
	}
}
