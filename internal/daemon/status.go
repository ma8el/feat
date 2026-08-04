package daemon

import (
	"errors"
	iofs "io/fs"
	"os"
	"strconv"

	"github.com/ma8el/feat/internal/paths"
)

// Status is what the runtime directory says about a daemon.
//
// The three observations are kept apart because their combinations are what a
// diagnosis is made of: a socket with nothing answering is stale, a record whose
// process is gone explains why, and a socket that answers without a record is a
// daemon in the middle of starting.
type Status struct {
	// Endpoint is the published record, valid when HasEndpoint is true.
	Endpoint Endpoint
	// HasEndpoint reports whether a record was readable.
	HasEndpoint bool
	// EndpointError explains an unreadable record that is not simply absent.
	EndpointError error
	// SocketPresent reports whether the socket path exists.
	SocketPresent bool
	// Answering reports whether something accepts connections on the socket.
	Answering bool
	// ProcessAlive reports whether the recorded process identifier is live. It
	// is false when there is no record.
	ProcessAlive bool
}

// Running reports whether a daemon is serving requests.
func (s Status) Running() bool { return s.Answering }

// StaleSocket reports a socket file left behind by a daemon that is gone. The
// next start reclaims it (ADR-027).
func (s Status) StaleSocket() bool { return s.SocketPresent && !s.Answering }

// Diagnose explains the status in one line, in the terms the user can act on.
func (s Status) Diagnose() string {
	switch {
	case s.Answering:
		return "a daemon is running"
	case s.StaleSocket() && s.HasEndpoint && !s.ProcessAlive:
		return "no daemon is running: pid " + strconv.Itoa(s.Endpoint.PID) +
			" left a socket behind, which the next start reclaims"
	case s.StaleSocket():
		return "no daemon is running: a socket exists but nothing answers on it, " +
			"which the next start reclaims"
	case s.HasEndpoint && !s.ProcessAlive:
		return "no daemon is running: pid " + strconv.Itoa(s.Endpoint.PID) + " is recorded but is not running"
	case s.EndpointError != nil:
		return "no daemon is running: " + s.EndpointError.Error()
	default:
		return "no daemon is running"
	}
}

// Inspect reports what the runtime directory says, without claiming anything in
// it. It is safe to call while another daemon is running.
func Inspect(layout paths.Layout) Status {
	status := Status{Answering: Answering(layout.Socket)}

	if _, err := os.Lstat(layout.Socket); err == nil {
		status.SocketPresent = true
	}

	endpoint, err := ReadEndpoint(layout)
	switch {
	case err == nil:
		status.Endpoint = endpoint
		status.HasEndpoint = true
		status.ProcessAlive = processExists(endpoint.PID)
	case errors.Is(err, ErrNotRunning), errors.Is(err, iofs.ErrNotExist):
		// No record at all, which is the ordinary case when nothing is running.
	default:
		status.EndpointError = err
	}
	return status
}
