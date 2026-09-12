package app

import (
	"errors"
	"flag"
	"fmt"
	"io"
)

// newFlagSet builds a flag set that reports errors on stderr.
func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

// parseInterleaved parses flags that appear anywhere among the positional
// arguments, and returns the positionals.
//
// Go's flag package stops at the first non-flag argument, so a plain Parse
// would read `https://x/ --format text` as a positional followed by ignored
// flags. Writing the URL first is the natural way to type this, and a flag
// that is quietly dropped is worse than one that is rejected — so parse in
// rounds, taking one positional at a time. A bare `--` ends flag parsing for
// everything after it.
func parseInterleaved(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			return nil, err
		}
		rest = fs.Args()
		if len(rest) == 0 {
			return positional, nil
		}
		// fs.Parse consumed a leading "--" itself; if the argument it
		// stopped on was preceded by one, everything left is positional.
		if consumedTerminator(rest, args) {
			return append(positional, rest...), nil
		}
		positional = append(positional, rest[0])
		rest = rest[1:]
	}
}

// consumedTerminator reports whether the round that produced rest ended on a
// "--" terminator: fs.Args() then starts right after it, which is visible as
// the argument before rest[0] in the original list being "--".
func consumedTerminator(rest, args []string) bool {
	idx := len(args) - len(rest) - 1
	return idx >= 0 && args[idx] == "--"
}

// parseCommand parses a command's flags and positionals and applies the
// shared conventions: `-h`/`--help` prints the usage on stdout and succeeds,
// any other parse error is a usage error. ok is false when the caller should
// return code.
func parseCommand(fs *flag.FlagSet, args []string, stdout, stderr io.Writer) (positional []string, code int, ok bool) {
	// The flag package prints its own error before calling Usage; silence
	// both so the error is reported once, in the tool's voice.
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	positional, err := parseInterleaved(fs, args)
	if errors.Is(err, flag.ErrHelp) {
		usage(stdout)
		return nil, exitOK, false
	}
	if err != nil {
		return nil, fail(stderr, exitError, "%v", err), false
	}
	return positional, 0, true
}

// fail prints a one-line error in the tool's voice and returns the code.
func fail(stderr io.Writer, code int, format string, args ...any) int {
	fmt.Fprintf(stderr, "web-fetch: "+format+"\n", args...)
	return code
}
