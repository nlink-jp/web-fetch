// Package app implements the web-fetch command-line interface: subcommand
// dispatch plus the mcp and version commands. Core logic lives in the
// packages under internal/; this package is the thin I/O shell around them.
package app

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// Exit codes (RFP §2). A fetch that returned a cut page is still a success;
// 1, the fetch failure, arrives with the fetch command.
const (
	exitOK    = 0 // the command completed
	exitError = 2 // usage / validation / configuration error
)

// Run dispatches a subcommand and returns a process exit code.
func Run(args []string, version string) int {
	return run(args, version, os.Stdin, os.Stdout, os.Stderr)
}

// run is Run with injected streams, so dispatch, the version banner and the
// command output can be tested without touching the process's own stdio.
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
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n", cmd)
		usage(stderr)
		return exitError
	}
}

// isURL reports whether an argument is the URL positional rather than a
// subcommand: a URL always carries its scheme, so the first argument is
// unambiguous (RFP §2).
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
  web-fetch <command> [flags]

Commands:
  mcp                      Run as a local MCP server (stdio)
  version                  Print the version

Flags:
  -c, --config <path>      Config file (default: first of
                           $XDG_CONFIG_HOME/web-fetch/config.toml,
                           ~/.config/web-fetch/config.toml,
                           ~/Library/Application Support/web-fetch/config.toml)

Exit codes:
  0  the command completed
  1  the fetch failed (network, HTTP status, size, a refused address)
  2  error (invalid input, bad configuration)

The fetch runs from this machine with no cookies, no Authorization and no
proxy, and never reaches a loopback, private, link-local or otherwise
reserved address. [network] allow_private = true lifts that, and is honoured
only in a config file on the default search path above — never in a file
named by --config or WEB_FETCH_CONFIG.
`)
}
