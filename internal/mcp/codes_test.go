package mcp

// toolCodes lists the error codes the packages behind the tools can surface.
// It lives in a test file of this package (rather than importing them) so the
// manual meta-test stays decoupled from the fetch pipeline; each package's own
// test pins the same list to its constants. Empty until the fetch tool lands
// (RFP Phase 1).
func toolCodes() []string {
	return nil
}
