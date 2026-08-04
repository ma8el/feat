package api

import (
	"errors"
	"net/http"

	"github.com/ma8el/feat/internal/domain"
)

// Errors the API translates into responses.
//
// They are declared here rather than in the daemon because the transport owns
// the mapping to status codes, and a service that returns one of them does not
// have to know what a status code is. A service wraps them:
//
//	fmt.Errorf("%w: no task %s", api.ErrNotFound, id)
var (
	// ErrNotFound reports a resource that does not exist.
	ErrNotFound = errors.New("not found")
	// ErrInvalid reports a request the daemon refuses to interpret.
	ErrInvalid = errors.New("invalid request")
)

// Stable error codes. A client may branch on these; the message is for a human
// and may change.
const (
	// CodeNotFound is returned with 404.
	CodeNotFound = "not_found"
	// CodeInvalid is returned with 400.
	CodeInvalid = "invalid_request"
	// CodeNotAllowed is returned with 405.
	CodeNotAllowed = "method_not_allowed"
	// CodeInternal is returned with 500. It is deliberately vague to the
	// client; the daemon log holds the detail.
	CodeInternal = "internal_error"
)

// Error is the body of every failed request, wrapped in an "error" object so
// that a successful payload can never be mistaken for a failure.
type Error struct {
	// Code is the stable machine-readable classification.
	Code string `json:"code"`
	// Message explains the failure to a person.
	Message string `json:"message"`
}

// errorEnvelope wraps an error body.
type errorEnvelope struct {
	Error Error `json:"error"`
}

// classify maps a service error onto a status and a code.
//
// A domain validation error is a client mistake rather than a daemon fault: the
// identifier in the request did not pass the domain's own rules, and the
// domain's message already says which field is wrong.
func classify(err error) (status int, code, message string) {
	switch {
	case errors.Is(err, ErrNotFound):
		return http.StatusNotFound, CodeNotFound, err.Error()
	case errors.Is(err, ErrInvalid), errors.Is(err, domain.ErrInvalid):
		return http.StatusBadRequest, CodeInvalid, err.Error()
	default:
		// Anything else is unexplained, so the client is told only that, and
		// the daemon logs the cause.
		return http.StatusInternalServerError, CodeInternal, "the daemon could not complete the request"
	}
}
