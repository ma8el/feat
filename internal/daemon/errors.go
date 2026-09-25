package daemon

import (
	"errors"
	"fmt"
	"time"

	"github.com/ma8el/feat/internal/paths"
)

// Errors callers match on.
var (
	// ErrNotRunning reports that no daemon owns the runtime directory.
	ErrNotRunning = errors.New("no feat daemon is running")
	// errLockHeld reports that another process holds the ownership lock. It is
	// internal: callers see an *AlreadyRunningError, which can also name the
	// process holding it.
	errLockHeld = errors.New("the ownership lock is held by another process")
)

// AlreadyRunningError reports that a daemon already owns the runtime directory.
// Starting a second daemon is not a failure the user needs to fix, so the message
// names the process that is already there instead of only refusing.
type AlreadyRunningError struct {
	// Endpoint is the record the running daemon wrote, if it could be read.
	Endpoint Endpoint
	// HasEndpoint reports whether the record was readable. A daemon that is
	// starting up holds the lock before it writes the record, and a daemon whose
	// record the system collected holds it without one.
	HasEndpoint bool
	// Answering reports whether the daemon holding the lock is serving on the
	// socket. It is what separates those two: a daemon that answers has finished
	// starting, so its missing record was removed rather than not yet written.
	Answering bool
}

func (e *AlreadyRunningError) Error() string {
	switch {
	case e.HasEndpoint:
		return fmt.Sprintf("a feat daemon is already running: pid %d on %s since %s",
			e.Endpoint.PID, e.Endpoint.Socket, e.Endpoint.StartedAt.Format(time.RFC3339))
	case e.Answering:
		// Naming the wait would send the user to wait for something that
		// finished happening, possibly days ago (ADR-101).
		return "a feat daemon is already running and answering, but its endpoint record has been removed; " +
			"`feat daemon stop` asks the daemon itself, and `feat daemon restart` replaces it"
	default:
		return "a feat daemon is already running: it holds the ownership lock but has not published its endpoint yet"
	}
}

// ForeignSocketError reports a socket that answers requests while the ownership
// lock is free. Feat refuses to start rather than unlink the path, because
// removing a socket something is serving would disconnect its clients.
type ForeignSocketError struct {
	// Socket is the path that answered.
	Socket string
	// Lock is the ownership lock that was free.
	Lock string
}

func (e *ForeignSocketError) Error() string {
	return fmt.Sprintf(
		"something is already serving %s while the ownership lock %s is free: "+
			"refusing to replace it, because removing a live socket would disconnect its clients",
		e.Socket, e.Lock)
}

// UnsafeDirectoryError reports a runtime directory Feat will not use. The
// fallback location can be shared with other users, so ownership cannot be
// established in a directory somebody else owns or anybody else can write to.
type UnsafeDirectoryError struct {
	// Dir is the directory.
	Dir string
	// Reason says what is wrong with it.
	Reason string
}

func (e *UnsafeDirectoryError) Error() string {
	return fmt.Sprintf("runtime directory %s cannot be used: %s; set %s to a directory you own",
		e.Dir, e.Reason, paths.EnvRuntimeOverride)
}

// StartupError reports that a spawned daemon never began serving.
type StartupError struct {
	// Socket is the socket the daemon was expected to listen on.
	Socket string
	// Waited is how long the caller waited.
	Waited time.Duration
	// LogFile is where the daemon's output was sent.
	LogFile string
	// LogTail is the end of that output, which usually says what went wrong.
	LogTail string
	// Err is the underlying failure, if there was one.
	Err error
}

func (e *StartupError) Error() string {
	message := fmt.Sprintf("the daemon did not start listening on %s within %s", e.Socket, e.Waited)
	if e.Err != nil {
		message += ": " + e.Err.Error()
	}
	if e.LogTail != "" {
		return message + "\nthe end of " + e.LogFile + " says:\n" + e.LogTail
	}
	return message + "\nsee " + e.LogFile
}

func (e *StartupError) Unwrap() error { return e.Err }
