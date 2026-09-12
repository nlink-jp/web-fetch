package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/nlink-jp/web-fetch/internal/config"
	"github.com/nlink-jp/web-fetch/internal/engine"
	"github.com/nlink-jp/web-fetch/internal/mcp"
	"github.com/nlink-jp/web-fetch/internal/toolerr"
)

type fetchFlags struct {
	jsonOut  bool
	format   string
	maxChars int
	offset   int
	timeout  string
	maxBytes string
	config   string
}

// runFetch is the URL positional: one fetch, printed as text or JSON.
func runFetch(args []string, version string, stdout, stderr io.Writer) int {
	var f fetchFlags
	fs := newFlagSet("fetch", stderr)
	fs.BoolVar(&f.jsonOut, "json", false, "JSON output")
	fs.BoolVar(&f.jsonOut, "j", false, "JSON output (shorthand)")
	fs.StringVar(&f.format, "format", "", "markdown | text | raw")
	fs.IntVar(&f.maxChars, "max-chars", 0, "characters returned")
	fs.IntVar(&f.offset, "offset", 0, "start position")
	fs.StringVar(&f.timeout, "timeout", "", "deadline, e.g. 20s")
	fs.StringVar(&f.maxBytes, "max-bytes", "", "byte cap after decoding, e.g. 5MiB")
	fs.StringVar(&f.config, "config", "", "config file path")
	fs.StringVar(&f.config, "c", "", "config file path (shorthand)")
	positional, code, ok := parseCommand(fs, args, stdout, stderr)
	if !ok {
		return code
	}
	if len(positional) != 1 {
		return fail(stderr, exitError, "exactly one URL is expected (got %d arguments)", len(positional))
	}

	cfg, err := config.Load(f.config)
	if err != nil {
		return fail(stderr, exitError, "%v", err)
	}
	if f.timeout != "" {
		if cfg.Fetch.Timeout, err = config.ParseDuration(f.timeout); err != nil {
			return fail(stderr, exitError, "--timeout: %v", err)
		}
	}
	if f.maxBytes != "" {
		if cfg.Fetch.MaxBytes, err = config.ParseBytes(f.maxBytes); err != nil {
			return fail(stderr, exitError, "--max-bytes: %v", err)
		}
	}
	if err := config.Validate(cfg); err != nil {
		return fail(stderr, exitError, "%v", err)
	}

	eng := engine.New(cfg, engine.NewClient(cfg, version), nil)
	res, err := eng.Fetch(context.Background(), engine.Request{
		URL: positional[0], Format: f.format, MaxChars: f.maxChars, Offset: f.offset,
	})
	if err != nil {
		return failFetch(stderr, err)
	}
	if f.jsonOut {
		return writeJSON(stdout, stderr, res)
	}
	fmt.Fprintln(stdout, res.Title)
	fmt.Fprintln(stdout, res.FinalURL)
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, res.Text)
	if res.Truncated {
		fmt.Fprintf(stdout, "\n[truncated: %d–%d of %d chars; continue with --offset %d]\n",
			res.Offset, res.NextOffset, res.TotalChars, res.NextOffset)
	}
	return exitOK
}

// failFetch prints the structured error as one JSON line on stderr and maps
// it onto the exit contract: argument and input-URL errors are usage errors
// (2); everything else is a failed fetch (1).
func failFetch(stderr io.Writer, err error) int {
	code := exitFetch
	switch toolerr.Code(err) {
	case toolerr.CodeInvalidArgument:
		code = exitError
	case toolerr.CodeInvalidURL, toolerr.CodeSchemeNotAllowed:
		if toolerr.Detail(err, "hop") == nil {
			code = exitError
		}
	}
	b, merr := json.Marshal(mcp.StructuredError(err))
	if merr != nil {
		return fail(stderr, code, "%v", err)
	}
	fmt.Fprintf(stderr, "%s\n", b)
	return code
}

// writeJSON prints a result as indented JSON, without HTML escaping: the
// payload is page text.
func writeJSON(stdout, stderr io.Writer, v any) int {
	enc := json.NewEncoder(stdout)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fail(stderr, exitError, "%v", err)
	}
	return exitOK
}
