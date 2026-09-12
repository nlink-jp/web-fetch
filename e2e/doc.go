// Package e2e holds live end-to-end tests that fetch real pages over the
// network. They are guarded by the `e2e` build tag so `go test ./...`
// (offline) never runs them; run them deliberately with:
//
//	make e2e          # or: go test -tags e2e -count=1 ./e2e/...
//
// They use the operator's real configuration (so allow_private stays whatever
// the operator set), assert on live responses, and log every measurement with
// a `MEASURED:` prefix — those lines answer the open questions in AGENTS.md
// and belong there with a date. No fetched page is saved.
//
// This file has no build tag so the package always compiles (and reports "no
// test files" without the tag).
package e2e
