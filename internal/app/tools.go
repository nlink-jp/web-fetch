package app

import (
	"context"
	"encoding/json"

	"github.com/nlink-jp/web-fetch/internal/config"
	"github.com/nlink-jp/web-fetch/internal/engine"
	"github.com/nlink-jp/web-fetch/internal/mcp"
)

// ToolFetch is the one tool beside get_usage. The embedded manual references
// it and a meta-test pins the two together.
const ToolFetch = "fetch"

// tools builds the tool set the MCP server exposes over one engine with the
// in-process cache.
func tools(cfg *config.Config, version string) []mcp.Tool {
	eng := engine.New(cfg, engine.NewClient(cfg, version), engine.NewCache(cfg))
	return []mcp.Tool{fetchTool(eng)}
}

type fetchArgs struct {
	URL      string `json:"url"`
	Format   string `json:"format"`
	MaxChars int    `json:"max_chars"`
	Offset   int    `json:"offset"`
	Fresh    bool   `json:"fresh"`
}

// fetchTool's description carries the trigger (when to call) and the
// contract (what comes back) — nothing else (ADR-0002 §4).
func fetchTool(eng *engine.Engine) mcp.Tool {
	return mcp.Tool{
		Name: ToolFetch,
		Description: "Fetch one URL from this machine and return its body as extracted text. " +
			"Call it when the user gives a URL, when a search result must be read whole, or when llm_context " +
			"answered with a different page than asked. `text` is the page's content, extracted as markdown by default " +
			"(plain text and JSON bodies come back as is, reported as format raw), starting at `offset` and at most " +
			"`max_chars` characters long; when `truncated` is present, more remains — pass `next_offset` back as `offset`, " +
			"and an unchanged `doc_id` means the same page. Never for a URL under investigation: the site sees this machine's IP.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url":       map[string]any{"type": "string", "description": "The http or https URL to fetch, exactly one. user:password@ is refused."},
				"format":    map[string]any{"type": "string", "enum": config.Formats, "description": "markdown (default from config), text (no markup), or raw (the decoded body as is)."},
				"max_chars": map[string]any{"type": "integer", "description": "Characters of text to return, 1000-200000 (default from config, 6000)."},
				"offset":    map[string]any{"type": "integer", "description": "Character position to start from (default 0). Use next_offset from a truncated result."},
				"fresh":     map[string]any{"type": "boolean", "description": "Refetch instead of reusing the page cached in this server process (default false)."},
			},
			"required": []string{"url"},
		},
		Annotations: map[string]any{
			"readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": true,
		},
		Handle: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var a fetchArgs
			if err := mcp.DecodeArgs(raw, &a); err != nil {
				return nil, err
			}
			if a.URL == "" {
				return nil, mcp.ArgErrorf("url is required")
			}
			return eng.Fetch(ctx, engine.Request{URL: a.URL, Format: a.Format, MaxChars: a.MaxChars, Offset: a.Offset, Fresh: a.Fresh})
		},
	}
}
