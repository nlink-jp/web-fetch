package toolerr

import (
	"errors"
	"fmt"
	"testing"
)

func TestCodesAreDistinctAndComplete(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range Codes() {
		if seen[c] {
			t.Errorf("duplicate code %q", c)
		}
		seen[c] = true
	}
	if len(seen) != 12 {
		t.Errorf("the RFP's error table has 12 rows; Codes() lists %d", len(seen))
	}
}

func TestWrappedErrorsKeepCodeAndDetails(t *testing.T) {
	base := Newf(CodeHTTPStatus, "server answered %d", 404).With("status", 404).With("final_url", "https://x/")
	wrapped := fmt.Errorf("fetch: %w", base)
	if Code(wrapped) != CodeHTTPStatus || !Has(wrapped, CodeHTTPStatus) {
		t.Errorf("code lost through wrapping: %q", Code(wrapped))
	}
	if Detail(wrapped, "status") != 404 || Detail(wrapped, "missing") != nil {
		t.Errorf("details: %v", base.Details)
	}
	if !errors.Is(wrapped, New(CodeHTTPStatus, "")) || errors.Is(wrapped, New(CodeTimeout, "")) {
		t.Error("errors.Is must compare codes")
	}
	if Code(errors.New("plain")) != "" || Detail(errors.New("plain"), "x") != nil {
		t.Error("a plain error has no code and no details")
	}
	if base.Error() != "server answered 404" || base.ErrorCode() != CodeHTTPStatus || base.ErrorDetails()["status"] != 404 {
		t.Errorf("Coded surface: %v", base)
	}
}
