package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// protocolVersion is the MCP revision this server speaks. 2025-03-26 is the
// first with tool annotations.
const protocolVersion = "2025-03-26"

// ServerName is what initialize reports.
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

// Server is a stdio JSON-RPC 2.0 MCP server.
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

// Serve reads newline-delimited JSON-RPC messages until stdin closes.
func (s *Server) Serve(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
	if stdin == nil {
		return fmt.Errorf("no input stream: the MCP server reads JSON-RPC from stdin")
	}
	sc := bufio.NewScanner(stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	enc := json.NewEncoder(stdout)

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var req request
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			// A malformed frame gets a parse error with a null id, per the
			// JSON-RPC spec. Staying silent would hang the client.
			if err := enc.Encode(errorResponse(nil, -32700, "parse error: "+err.Error())); err != nil {
				return err
			}
			continue
		}
		resp, ok := s.handle(ctx, &req)
		if !ok {
			continue // a notification: no reply
		}
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	return sc.Err()
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func errorResponse(id json.RawMessage, code int, msg string) response {
	return response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}}
}

func okResponse(id json.RawMessage, result any) response {
	return response{JSONRPC: "2.0", ID: id, Result: result}
}

// handle dispatches one message. The second return value is false for
// notifications, which must not be answered.
func (s *Server) handle(ctx context.Context, req *request) (response, bool) {
	isNotification := len(req.ID) == 0 || string(req.ID) == "null"

	switch req.Method {
	case "initialize":
		return okResponse(req.ID, map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": ServerName, "version": s.Version},
			"instructions":    Instructions,
		}), true

	case "notifications/initialized", "notifications/cancelled":
		// cancelled is received and ignored: a dispatched fetch completes or
		// times out on its own. The spec allows a receiver to ignore it in
		// that case.
		return response{}, false

	case "ping":
		return okResponse(req.ID, map[string]any{}), true

	case "tools/list":
		return okResponse(req.ID, map[string]any{"tools": s.definitions()}), true

	case "tools/call":
		if isNotification {
			return response{}, false
		}
		return s.callTool(ctx, req), true

	default:
		if isNotification {
			return response{}, false
		}
		return errorResponse(req.ID, -32601, "method not found: "+req.Method), true
	}
}

func (s *Server) definitions() []map[string]any {
	out := make([]map[string]any, 0, len(s.tools))
	for _, t := range s.tools {
		def := map[string]any{
			"name":        t.Name,
			"description": t.Description,
			"inputSchema": t.InputSchema,
		}
		if len(t.Annotations) > 0 {
			def["annotations"] = t.Annotations
		}
		out = append(out, def)
	}
	return out
}

type callParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func (s *Server) callTool(ctx context.Context, req *request) (resp response) {
	// A handler that panics must not end the server: it is a long-lived
	// process whose input is whatever page the model asked for, and a page
	// that found a bug in the extractor would otherwise take the fetch tool
	// away until the runtime restarts it (code-audit run-2). The panic
	// becomes a tool error the model can see; the request after it is
	// served as usual.
	defer func() {
		if r := recover(); r != nil {
			resp = okResponse(req.ID, toolError(&internalError{cause: fmt.Sprint(r)}))
		}
	}()
	var p callParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return errorResponse(req.ID, -32602, "invalid params: "+err.Error())
		}
	}
	// get_usage returns the manual itself, which is Markdown rather than a JSON
	// document — wrapping it in JSON would only make it harder to read. It
	// takes no arguments, and says so when handed one.
	if p.Name == ToolGetUsage {
		var none struct{}
		if err := DecodeArgs(p.Arguments, &none); err != nil {
			return okResponse(req.ID, toolError(err))
		}
		return okResponse(req.ID, toolRawText(UsageDoc()))
	}
	tool, ok := s.byName[p.Name]
	if !ok {
		return okResponse(req.ID, toolError(ArgErrorf("unknown tool %q; call tools/list for the available tools", p.Name)))
	}
	result, err := tool.Handle(ctx, p.Arguments)
	if err != nil {
		// Tool failures are results, not protocol errors: the model has to see
		// them and decide what to do, and a JSON-RPC error would be swallowed
		// by the client instead.
		return okResponse(req.ID, toolError(err))
	}
	return okResponse(req.ID, toolText(result))
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
		return toolError(fmt.Errorf("encode result: %w", err))
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
		text = `{"code":"internal_error","message":"could not encode the error"}`
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
	return nil
}

var (
	unknownField = regexp.MustCompile(`unknown field "([^"]+)"`)
	badField     = regexp.MustCompile(`Go struct field [^.]+\.([A-Za-z0-9_]+)`)
)

// CodeInvalidArgument is this layer's own error code — an unknown tool, an
// unknown or mistyped argument. Every other code comes from the packages
// behind the tools, so an agent sees one vocabulary end to end.
const CodeInvalidArgument = "invalid_argument"

// CodeInternalError is this layer's code for a tool handler that panicked:
// a defect in the server, not in the call. The message says so; the cause
// is not shown, because it is the panic value of code that was given the
// page's bytes, and a line of a stack is of no use to a model.
const CodeInternalError = "internal_error"

// internalError is the error a recovered handler panic surfaces as.
type internalError struct{ cause string }

func (e *internalError) Error() string {
	return "the tool failed on this input (a defect in web-fetch, not in the call); the server is still running"
}
func (e *internalError) ErrorCode() string            { return CodeInternalError }
func (e *internalError) ErrorDetails() map[string]any { return nil }

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
