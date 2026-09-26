package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/ma8el/feat/internal/domain"
)

// Errors the API translates into responses.
//
// They are declared here rather than in the daemon because the transport owns the
// mapping to status codes, so a service that returns one need not know what a
// status code is. A service wraps them:
//
//	fmt.Errorf("%w: no task %s", api.ErrNotFound, id)
var (
	// ErrNotFound reports a resource that does not exist.
	ErrNotFound = errors.New("not found")
	// ErrInvalid reports a request the daemon refuses to interpret.
	ErrInvalid = errors.New("invalid request")
	// ErrTerminalMissing reports a task whose tagged tmux terminal is not on the
	// machine, which is a recoverable state rather than a missing task.
	//
	// It is defined through ErrNotFound, so every existing errors.Is check keeps
	// its answer and a caller that can offer the recovery can still tell this
	// absence from a task identifier that names nothing.
	ErrTerminalMissing = fmt.Errorf("%w: the task has no live terminal", ErrNotFound)
	// ErrShellMissing reports a task that has not been given a shell pane, which
	// is not a fault: a shell is opened on demand, so most tasks have none for
	// most of their lives. The client says so and names the key that opens one.
	ErrShellMissing = fmt.Errorf("%w: the task has no shell pane", ErrNotFound)
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
	// CodeInternal is returned with 500. It is vague to the client, because the
	// daemon log holds the detail.
	CodeInternal = "internal_error"
	// CodeTerminalMissing is returned with 404 for a task whose tmux terminal is
	// gone. It is the narrower not_found, and the one a client can act on.
	CodeTerminalMissing = "terminal_missing"
	// CodeShellMissing is returned with 404 for a task that has not been given a
	// shell pane, which is an ordinary state rather than a failure.
	CodeShellMissing = "shell_missing"
)

// Coded is an error that carries a daemon error code.
//
// A client implements it so a caller holding only a decoded response can ask the
// same question as one holding the daemon's own error. The alternative is
// matching on message text, which this package reserves the right to change.
type Coded interface{ ErrorCode() string }

// IsTerminalMissing reports whether an error says a task's tmux terminal is not
// there, in process or across the socket.
func IsTerminalMissing(err error) bool {
	return classified(err, ErrTerminalMissing, CodeTerminalMissing)
}

// IsShellMissing reports whether an error says a task has no shell pane yet.
func IsShellMissing(err error) bool {
	return classified(err, ErrShellMissing, CodeShellMissing)
}

// IsInvalid reports whether the daemon refused a request rather than failed one,
// in process or across the socket.
//
// A refusal says what cannot be done from here — a task in a state the action is
// not for, a project that configures no runtime — and the user answers it by
// doing something else. A failure says Feat tried and something broke. A screen
// that renders both alike leaves the user to tell them apart from the wording.
//
// domain.ErrInvalid is classified as this too, so a validation error raised in
// the domain arrives here as the refusal it is.
func IsInvalid(err error) bool {
	return classified(err, ErrInvalid, CodeInvalid) || errors.Is(err, domain.ErrInvalid)
}

// classified answers one of those questions from whichever form the caller has:
// the daemon's own wrapped error, or a client's decoded code.
func classified(err error, sentinel error, code string) bool {
	if errors.Is(err, sentinel) {
		return true
	}
	var coded Coded
	return errors.As(err, &coded) && coded.ErrorCode() == code
}

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
	// Before the general case these are defined through, so the client receives
	// the narrower classification.
	case errors.Is(err, ErrTerminalMissing):
		return http.StatusNotFound, CodeTerminalMissing, err.Error()
	case errors.Is(err, ErrShellMissing):
		return http.StatusNotFound, CodeShellMissing, err.Error()
	case errors.Is(err, ErrNotFound):
		return http.StatusNotFound, CodeNotFound, err.Error()
	case errors.Is(err, ErrInvalid), errors.Is(err, domain.ErrInvalid):
		return http.StatusBadRequest, CodeInvalid, err.Error()
	default:
		// Anything else is unexplained, so the client is told only that and
		// the daemon logs the cause.
		return http.StatusInternalServerError, CodeInternal, "the daemon could not complete the request"
	}
}
