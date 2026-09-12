// Package app implements the web-fetch command-line interface: the URL
// positional, the mcp and version commands, and the text / JSON rendering.
// Core logic lives in the engine; this package is the thin I/O shell.
package app

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// Exit codes (RFP §2). A fetch that returned a cut page is still a success.
const (
	exitOK    = 0 // the command completed
	exitFetch = 1 // the fetch failed: network, HTTP, size, a refused address
	exitError = 2 // usage / validation / configuration error
)

// Run dispatches a subcommand and returns a process exit code.
func Run(args []string, version string) int {
	return run(args, version, os.Stdin, os.Stdout, os.Stderr)
}

// run is Run with injected streams, so dispatch, the version banner and the
// command output can be tested without touching the process's own stdio.
//
// The first argument is a subcommand when it names one; otherwise the
// arguments are a fetch as long as one of them is a URL (a URL always carries
// its scheme, so flags may come before it).
func run(args []string, version string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return exitError
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "mcp":
		return runMCP(rest, version, stdin, stdout, stderr)
	case "version", "--version", "-v":
		printVersion(stdout, version)
		return exitOK
	case "help", "-h", "--help":
		usage(stdout)
		return exitOK
	}
	// Anything with a scheme is a fetch attempt, so `ftp://x` is answered by
	// the URL validator (scheme_not_allowed) rather than as an unknown command.
	for _, a := range args {
		if isURL(a) || strings.Contains(a, "://") {
			return runFetch(args, version, stdout, stderr)
		}
	}
	fmt.Fprintf(stderr, "unknown command %q (a URL starts with http:// or https://)\n\n", cmd)
	usage(stderr)
	return exitError
}

// isURL reports whether an argument is the URL positional rather than a
// subcommand or flag: a URL always carries its scheme (RFP §2).
func isURL(arg string) bool {
	lower := strings.ToLower(arg)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

// printVersion is the single source of the version banner. `--version` and the
// `version` subcommand must print byte-identical output: a Homebrew formula's
// `brew test` calls `--version`, while humans type `version`.
func printVersion(w io.Writer, version string) {
	fmt.Fprintln(w, "web-fetch "+version)
	fmt.Fprintln(w, "Fetches one URL from this machine. No credentials, nothing stored on disk.")
}

func usage(w io.Writer) {
	fmt.Fprint(w, `web-fetch — one URL's readable text, as a CLI and a local MCP server

Usage:
  web-fetch [flags] <url>
  web-fetch <command> [flags]

Commands:
  mcp                      Run as a local MCP server (stdio)
  version                  Print the version

Fetch flags (may come before or after the URL):
  -j, --json               JSON output (the same shape the MCP tool returns)
  --format <f>             markdown | text | raw (default markdown)
  --max-chars <n>          Characters returned, 1000-200000 (default 6000)
  --offset <n>             Start position; pass next_offset from a cut result
  --timeout <dur>          Deadline from request start, 1s-120s (default 20s)
  --max-bytes <size>       Most bytes read after decoding, 64KiB-50MiB (default 5MiB)

Shared flags:
  -c, --config <path>      Config file (default: first of
                           $XDG_CONFIG_HOME/web-fetch/config.toml,
                           ~/.config/web-fetch/config.toml,
                           ~/Library/Application Support/web-fetch/config.toml)

Exit codes:
  0  fetched (a cut page is still a success)
  1  the fetch failed (network, HTTP status, size cap, a refused address)
  2  error (invalid input, bad configuration)

A non-HTML body (text, JSON) is returned as is and reported as format raw.
Errors are printed to stderr as one JSON object {code, message, details}.

The fetch runs from this machine with no cookies, no Authorization and no
proxy, and never reaches a loopback, private, link-local or otherwise
reserved address. [network] allow_private = true lifts that, and is honoured
only in a config file on the default search path above — never in a file
named by --config or WEB_FETCH_CONFIG.
`)
}
