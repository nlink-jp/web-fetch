package app

import (
	"context"
	"io"

	"github.com/nlink-jp/web-fetch/internal/config"
	"github.com/nlink-jp/web-fetch/internal/mcp"
)

// runMCP serves the stdio MCP server until stdin closes.
func runMCP(args []string, version string, stdin io.Reader, stdout, stderr io.Writer) int {
	var configPath string
	fs := newFlagSet("mcp", stderr)
	fs.StringVar(&configPath, "config", "", "config file path")
	fs.StringVar(&configPath, "c", "", "config file path (shorthand)")
	positional, code, ok := parseCommand(fs, args, stdout, stderr)
	if !ok {
		return code
	}
	if len(positional) > 0 {
		return fail(stderr, exitError, "mcp takes no arguments (got %q)", positional[0])
	}

	// The config is loaded here so a broken file — or a file that tries to
	// loosen the address guard from the wrong place — fails at startup, where
	// the operator sees it, rather than on the first tool call.
	if _, err := config.Load(configPath); err != nil {
		return fail(stderr, exitError, "%v", err)
	}

	srv := mcp.New(version)
	if err := srv.Serve(context.Background(), stdin, stdout); err != nil {
		return fail(stderr, exitError, "mcp: %v", err)
	}
	return exitOK
}
