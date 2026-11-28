package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"reflect"
	"regexp"
	"strings"

	"github.com/nlink-jp/mcp"
	mcpserver "github.com/nlink-jp/mcp/server"
)

// ServerName is the name the server reports when a client opens it.
const ServerName = "web-fetch"

// Handler answers one tool call. Arguments arrive as raw JSON so each tool
// decodes strictly into its own struct.
type Handler func(ctx context.Context, args json.RawMessage) (any, error)

// Tool is one entry of tools/list plus the handler behind it.
type Tool struct {
	Name        string
	Description string
	InputSchema map[string]any
	// Annotations are the MCP tool annotations (readOnlyHint, destructiveHint,
	// idempotentHint, openWorldHint). Every tool declares them: a fetch is
	// read-only but open-world, and a client that gates on the hints must
	// see them rather than guess.
	Annotations map[string]any
	Handle      Handler
}

// Server serves the tools over stdio. The protocol — both eras, framing,
// versions, cancellation — is nlink-jp/mcp's (organization ADR-030); this
// type holds the tools.
type Server struct {
	Version string
	tools   []Tool
	byName  map[string]Tool
}

// New builds a server over the given tools. get_usage is always present, and
// every tool's schema is closed on the way in.
func New(version string, tools ...Tool) *Server {
	s := &Server{Version: version, byName: map[string]Tool{}}
	all := append([]Tool{}, tools...)
	all = append(all, usageTool())
	for _, t := range all {
		closeSchema(t.InputSchema)
		s.tools = append(s.tools, t)
		s.byName[t.Name] = t
	}
	return s
}

// closeSchema sets additionalProperties:false on a tool's top-level input
// schema, as organization ADR-021 §10 requires. It is the client-side half of
// the strictness DecodeArgs enforces here: the schema stops a mistyped
// argument at a validating client, DecodeArgs stops it at the server.
//
// It is applied in New — the one place every tool passes through — rather than
// written into each schema literal, so a tool added later cannot forget it.
// Only the top level is touched; a nested object that deliberately accepts
// free-form keys keeps whatever it declares.
func closeSchema(schema map[string]any) {
	if schema != nil {
		schema["additionalProperties"] = false
	}
}

// Tools returns the registered tool definitions, in tools/list order.
func (s *Server) Tools() []Tool { return s.tools }

// Serve runs the server until stdin reaches EOF (answering every request that
// arrived), ctx is cancelled, or a write to stdout fails.
func (s *Server) Serve(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
	if stdin == nil {
		return fmt.Errorf("no input stream: the MCP server reads JSON-RPC from stdin")
	}
	ts, err := s.toolServer()
	if err != nil {
		return err
	}
	return ts.Serve(ctx, stdin, stdout)
}

// toolServer registers the tools, in tools/list order, on a fresh protocol
// server; each handler calls callTool. One is built per Serve.
func (s *Server) toolServer() (*mcpserver.ToolServer, error) {
	ts, err := mcpserver.New(mcpserver.Info{
		Server:       mcp.Implementation{Name: ServerName, Version: s.Version},
		Instructions: Instructions,
	}, mcpserver.Options{
		// One call at a time, as before the module: the fetcher's pacing assumes
		// it.
		Workers: 1,
		// Calls sent side by side wait, as they did before the module: the
		// default of 16 would refuse the 17th as "server busy".
		QueueDepth: 256,
		Logger:     slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
	})
	if err != nil {
		return nil, err
	}
	for _, tl := range s.tools {
		schema, err := json.Marshal(tl.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("tool %s: %w", tl.Name, err)
		}
		def := mcpserver.Tool{Name: tl.Name, Description: tl.Description, InputSchema: schema}
		if len(tl.Annotations) > 0 {
			a, err := json.Marshal(tl.Annotations)
			if err != nil {
				return nil, fmt.Errorf("tool %s: %w", tl.Name, err)
			}
			def.Annotations = a
		}
		if err := ts.AddTool(def, s.handle); err != nil {
			return nil, err
		}
	}
	return ts, nil
}

// handle runs one tools/call: callTool builds the result, and it is copied
// into the protocol's shape. The protocol layer has already refused an unknown
// tool and arguments that are not an object, and answers a panic with -32603.
func (s *Server) handle(ctx context.Context, req *mcpserver.Request) (*mcpserver.Result, error) {
	res := s.callTool(ctx, req.Name, req.Arguments)
	out := &mcpserver.Result{IsError: res["isError"] == true}
	// toolText, toolRawText and toolError build content as []map[string]any;
	// anything else is a programming error, not an empty success.
	blocks, ok := res["content"].([]map[string]any)
	if !ok {
		return nil, fmt.Errorf("tool %s: result content is %T", req.Name, res["content"])
	}
	for _, b := range blocks {
		raw, err := json.Marshal(b)
		if err != nil {
			return nil, err
		}
		out.Content = append(out.Content, raw)
	}
	return out, nil
}

// callTool runs one tool by name and wraps its result as tools/call content.
func (s *Server) callTool(ctx context.Context, name string, args json.RawMessage) map[string]any {
	// get_usage returns the manual itself, which is Markdown rather than a JSON
	// document — wrapping it in JSON would only make it harder to read. It
	// takes no arguments, and says so when handed one.
	if name == ToolGetUsage {
		var none struct{}
		if err := DecodeArgs(args, &none); err != nil {
			return toolError(err)
		}
		return toolRawText(UsageDoc())
	}
	tool, ok := s.byName[name]
	if !ok {
		// Not reached: the protocol layer refuses a tool it did not register
		// (-32602), so an unknown tool never reaches the model as a result.
		return toolError(fmt.Errorf("unknown tool: %s", name))
	}
	result, err := tool.Handle(ctx, args)
	if err != nil {
		// Tool failures are results, not protocol errors: the model has to see
		// them and decide what to do, and a JSON-RPC error would be swallowed
		// by the client instead.
		return toolError(err)
	}
	return toolText(result)
}

// toolText wraps a payload as MCP text content. Structured data is returned as
// JSON text because that is what the content protocol carries. HTML escaping
// is off: the payload is page text, and `<` for every `<` would inflate a
// markdown body for no reader's benefit.
func toolText(v any) map[string]any {
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return toolError(&encodeError{err: err})
	}
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": strings.TrimRight(buf.String(), "\n")}},
	}
}

func toolRawText(s string) map[string]any {
	return map[string]any{"content": []map[string]any{{"type": "text", "text": s}}}
}

func toolError(err error) map[string]any {
	b, mErr := json.Marshal(StructuredError(err))
	text := string(b)
	if mErr != nil {
		text = `{"code":"` + CodeInternalError + `","message":"could not encode the error"}`
	}
	return map[string]any{
		"isError": true,
		"content": []map[string]any{{"type": "text", "text": text}},
	}
}

// DecodeArgs decodes tool arguments strictly: a misspelled argument is a
// mistake worth reporting, not one to ignore silently.
func DecodeArgs(raw json.RawMessage, into any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		e := &ArgError{Msg: err.Error()}
		// encoding/json names the field in prose: `json: unknown field "x"`,
		// `json: cannot unmarshal string into Go struct field a.count of
		// type int`. Lift it into details.argument so the caller can branch.
		if m := unknownField.FindStringSubmatch(err.Error()); m != nil {
			e.Argument = m[1]
		} else if m := badField.FindStringSubmatch(err.Error()); m != nil {
			e.Argument = m[1]
		}
		return e
	}
	// encoding/json matches names case-insensitively, so a name in another
	// case would reach the declared argument. The schema names each one
	// exactly; refuse any other spelling by name, as an undeclared one is.
	var names map[string]json.RawMessage
	if json.Unmarshal(raw, &names) == nil {
		declared := argNames(into)
		for n := range names {
			if !declared(n) {
				return &ArgError{Msg: fmt.Sprintf("json: unknown field %q", n), Argument: n}
			}
		}
	}
	return nil
}

var (
	unknownField = regexp.MustCompile(`unknown field "([^"]+)"`)
	badField     = regexp.MustCompile(`Go struct field [^.]+\.([A-Za-z0-9_]+)`)
)

// CodeInvalidArgument is this layer's own error code — an unknown or
// mistyped argument. Every other code comes from the packages
// behind the tools, so an agent sees one vocabulary end to end.
const CodeInvalidArgument = "invalid_argument"

// CodeInternalError is this layer's code for a result or an error it could
// not encode: a defect in the server, not in the call. A tool handler that panics is
// answered -32603 by the protocol layer (nlink-jp/mcp), which keeps serving.
const CodeInternalError = "internal_error"

// ArgError is an argument-validation failure. Argument, when known, names the
// offending argument and surfaces as details.argument.
type ArgError struct {
	Msg      string
	Argument string
}

func (e *ArgError) Error() string { return e.Msg }

// ArgErrorf builds an ArgError.
func ArgErrorf(format string, args ...any) error {
	return &ArgError{Msg: fmt.Sprintf(format, args...)}
}

// Coded is implemented by errors that carry a stable code and optional
// machine-readable details.
type Coded interface {
	error
	ErrorCode() string
	ErrorDetails() map[string]any
}

// StructuredError maps an error onto the {code, message, details} object an
// agent sees.
func StructuredError(err error) map[string]any {
	var ae *ArgError
	if errors.As(err, &ae) {
		out := map[string]any{"code": CodeInvalidArgument, "message": ae.Msg}
		if ae.Argument != "" {
			out["details"] = map[string]any{"argument": ae.Argument}
		}
		return out
	}
	var c Coded
	if errors.As(err, &c) {
		out := map[string]any{"code": c.ErrorCode(), "message": c.Error()}
		if d := c.ErrorDetails(); len(d) > 0 {
			out["details"] = d
		}
		return out
	}
	return map[string]any{"code": CodeInvalidArgument, "message": err.Error()}
}

// argNames reports whether a name is an argument the decode target declares,
// spelt as declared. A field with an explicit json name accepts that name
// exactly; a field without one is matched as encoding/json matches it (any
// case), since no spelling is declared for it; an embedded struct's fields
// count as the target's own; "-" fields are not arguments. A target that is
// not a struct (a map, say) declares no names, so every name passes here and
// is left to the decoder.
func argNames(into any) func(string) bool {
	t := reflect.TypeOf(into)
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct {
		return func(string) bool { return true }
	}
	exact := map[string]bool{}
	var loose []string
	var walk func(reflect.Type)
	walk = func(t reflect.Type) {
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			tag := f.Tag.Get("json")
			if tag == "-" {
				continue
			}
			name, _, _ := strings.Cut(tag, ",")
			if f.Anonymous && name == "" {
				ft := f.Type
				for ft.Kind() == reflect.Pointer {
					ft = ft.Elem()
				}
				if ft.Kind() == reflect.Struct {
					walk(ft)
					continue
				}
			}
			if !f.IsExported() {
				continue
			}
			if name == "" {
				loose = append(loose, f.Name)
			} else {
				exact[name] = true
			}
		}
	}
	walk(t)
	return func(n string) bool {
		if exact[n] {
			return true
		}
		for _, l := range loose {
			if strings.EqualFold(l, n) {
				return true
			}
		}
		return false
	}
}

// encodeError is a result this layer could not encode: a defect in the
// server, not in the call, so it is not reported as an argument error.
type encodeError struct{ err error }

func (e *encodeError) Error() string                { return "encode result: " + e.err.Error() }
func (e *encodeError) ErrorCode() string            { return CodeInternalError }
func (e *encodeError) ErrorDetails() map[string]any { return nil }
