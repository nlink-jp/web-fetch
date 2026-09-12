package mcp

import _ "embed"

//go:embed usage.md
var usageDoc string

// UsageDoc returns the embedded manual — what get_usage answers with, and the
// canonical reference for this server's tools. The mcp-tactics skill documents
// no parameters by design, so this is what an agent reads before its first call.
func UsageDoc() string { return usageDoc }

// ToolGetUsage is the one tool every server in the fleet has.
const ToolGetUsage = "get_usage"

func usageTool() Tool {
	return Tool{
		Name:        ToolGetUsage,
		Description: "The full reference for this server: tools, arguments, the response schema and the error-recovery table. Call this first.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		Annotations: map[string]any{"readOnlyHint": true, "idempotentHint": true, "openWorldHint": false},
	}
}

// Instructions is what initialize hands the client. lagent shows a clipped
// first sentence of it in its tool catalog before the server is loaded, so the
// first sentence says what the server is for; the rest is the contract a model
// needs if it never calls get_usage. Trigger and contract only — no defensive
// prose (ADR-0002 §4).
const Instructions = `web-fetch returns the extracted text of one URL, fetched from this machine ` +
	`(your IP address, no credentials, nothing stored on disk). Call fetch when the user gives a URL, ` +
	`when a search result must be read whole, or when llm_context answered with a different page. ` +
	`A result carries max_chars characters of text from offset; truncated with next_offset means more ` +
	`remains, and an unchanged doc_id means the same page. Call get_usage for the full reference: ` +
	`arguments, the response schema and the error-recovery table. Never point it at a URL under ` +
	`investigation: the site sees a visit from here.`
