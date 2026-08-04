package client

import (
	"errors"
	"fmt"
	"net/http"
)

// ErrDaemonNotRunning reports that nothing is listening on the socket.
//
// It is separated from every other transport failure because it is the one a
// client can do something about: start the daemon.
var ErrDaemonNotRunning = errors.New("no feat daemon is listening")

// StatusError reports a response the daemon refused.
type StatusError struct {
	// Status is the HTTP status code.
	Status int
	// Code is the daemon's stable error code, when it sent one.
	Code string
	// Message is the daemon's explanation, when it sent one.
	Message string
	// Path is the request path, so the message names what was asked for.
	Path string
}

func (e *StatusError) Error() string {
	switch {
	case e.Message != "":
		return e.Message
	case e.Code != "":
		return fmt.Sprintf("the daemon refused %s: %s", e.Path, e.Code)
	default:
		return fmt.Sprintf("the daemon refused %s: %s", e.Path, http.StatusText(e.Status))
	}
}

// NotFound reports whether the daemon said the resource does not exist.
func (e *StatusError) NotFound() bool { return e.Status == http.StatusNotFound }
