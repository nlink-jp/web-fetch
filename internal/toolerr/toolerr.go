// Package toolerr is the error vocabulary of web-fetch: one code per row of
// the RFP's error table, carried as {code, message, details} so a CLI can map
// it to an exit code and an MCP client can branch on it without parsing prose.
// Every package behind the tools returns *Error; the MCP layer's own
// invalid_argument shares its spelling with CodeInvalidArgument.
package toolerr

import (
	"errors"
	"fmt"
)

// Codes. The strings are the contract; adding one is not a breaking change,
// renaming one is.
const (
	CodeInvalidArgument        = "invalid_argument"         // an argument out of range, unknown or mistyped
	CodeInvalidURL             = "invalid_url"              // malformed, has userinfo, no host — in the input or a redirect target (details.hop)
	CodeSchemeNotAllowed       = "scheme_not_allowed"       // not http / https — in the input or a redirect target (details.hop)
	CodeAddressNotAllowed      = "address_not_allowed"      // resolves into the denied address domain (ADR-0001)
	CodeDNSFailed              = "dns_failed"               // resolution failed or answered with no addresses
	CodeConnectFailed          = "connect_failed"           // TCP / TLS failed on every vetted address
	CodeTimeout                = "timeout"                  // the deadline passed, a stalled body read included
	CodeTooManyRedirects       = "too_many_redirects"       // over [network] max_redirects
	CodeHTTPStatus             = "http_status"              // a non-2xx response, a 3xx without Location included
	CodeTooLarge               = "too_large"                // the decoded body passed [fetch] max_bytes, or its rendering passed the render budget
	CodeUnsupportedContentType = "unsupported_content_type" // a refused media type, or a non-gzip Content-Encoding
	CodeCharsetUnsupported     = "charset_unsupported"      // a declared charset label unknown to the decoder table
)

// Codes lists every code this package defines, for the manual meta-test.
func Codes() []string {
	return []string{
		CodeInvalidArgument, CodeInvalidURL, CodeSchemeNotAllowed, CodeAddressNotAllowed,
		CodeDNSFailed, CodeConnectFailed, CodeTimeout, CodeTooManyRedirects, CodeHTTPStatus,
		CodeTooLarge, CodeUnsupportedContentType, CodeCharsetUnsupported,
	}
}

// Error is a structured failure.
type Error struct {
	Code    string
	Message string
	Details map[string]any
}

func (e *Error) Error() string { return e.Message }

// ErrorCode implements the MCP layer's Coded interface.
func (e *Error) ErrorCode() string { return e.Code }

// ErrorDetails implements the MCP layer's Coded interface.
func (e *Error) ErrorDetails() map[string]any { return e.Details }

// Is reports whether target is an *Error with the same code, so
// errors.Is(err, toolerr.New(CodeTimeout, "")) works as a sentinel check.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code
}

// New builds an error.
func New(code, message string) *Error {
	return &Error{Code: code, Message: message}
}

// Newf builds an error with a formatted message.
func Newf(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// With adds one detail and returns the error, for chaining.
func (e *Error) With(key string, value any) *Error {
	if e.Details == nil {
		e.Details = map[string]any{}
	}
	e.Details[key] = value
	return e
}

// Code returns err's code, or "" when err is not (or does not wrap) an *Error.
func Code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// Has reports whether err carries the given code.
func Has(err error, code string) bool { return Code(err) == code }

// Detail returns one detail of err, or nil.
func Detail(err error, key string) any {
	var e *Error
	if errors.As(err, &e) && e.Details != nil {
		return e.Details[key]
	}
	return nil
}
