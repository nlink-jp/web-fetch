package mcp

import "github.com/nlink-jp/web-fetch/internal/toolerr"

// toolCodes lists the error codes the packages behind the tools can surface,
// so the manual meta-test checks every one has recovery guidance. toolerr is
// the single vocabulary (RFP §2 error table).
func toolCodes() []string {
	return toolerr.Codes()
}
