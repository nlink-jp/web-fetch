package app

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/nlink-jp/web-fetch/internal/config"
	"github.com/nlink-jp/web-fetch/internal/mcp"
)

// notifyContext is signal.NotifyContext; a test stands in for the signal.
var notifyContext = signal.NotifyContext

// runMCP serves the stdio MCP server until stdin closes, answering every
// request that arrived, or until SIGINT or SIGTERM.
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
	cfg, err := config.Load(configPath)
	if err != nil {
		return fail(stderr, exitError, "%v", err)
	}

	srv := mcp.New(version, tools(cfg, version)...)
	// SIGINT and SIGTERM cancel the server's ctx: it returns at once and its
	// cleanup runs (server-migration §2).
	ctx, stop := notifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := srv.Serve(ctx, stdin, stdout); err != nil {
		return fail(stderr, exitError, "mcp: %v", err)
	}
	return exitOK
}
