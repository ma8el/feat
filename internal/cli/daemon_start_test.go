package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/ma8el/feat/internal/daemon"
)

// TestAFailedAutoStartReportsWhy pins the message the dashboard leaves behind
// when it could not start the daemon it needs.
//
// Opening the dashboard starts a daemon (ADR-008). When that fails there is
// nothing to show, and what the user gets instead is this error. It used to be
// the generic one — "no feat daemon is running on …; start one with `feat daemon
// start`" — which names the command that had just been run on their behalf and
// failed, and drops the reason: a *daemon.StartupError carries the end of the
// daemon log, which is where a spawn that never began serving says what stopped
// it.
func TestAFailedAutoStartReportsWhy(t *testing.T) {
	socket := "/run/feat/feat.sock"
	cause := &daemon.StartupError{
		Socket:  socket,
		LogFile: "/state/feat/logs/daemon.log",
		LogTail: "runtime directory /run/feat is not writable",
	}

	message := (&NotRunningError{Socket: socket, Cause: cause}).Error()

	if !strings.Contains(message, "runtime directory /run/feat is not writable") {
		t.Errorf("the error does not carry the reason the daemon did not start:\n%s", message)
	}
	if !strings.Contains(message, socket) {
		t.Errorf("the error does not name the socket:\n%s", message)
	}
	if strings.Contains(message, "start one with") {
		t.Errorf("the error still advises the command that just failed:\n%s", message)
	}
}

// TestAFailedAutoStartStaysMatchable keeps the exit-code contract working.
//
// Exit code 4 means "no daemon is running", and a failed start is still that: a
// script that starts one only when it has to must not have to tell the two
// apart. The cause stays reachable through errors.As for a caller that wants it.
func TestAFailedAutoStartStaysMatchable(t *testing.T) {
	cause := &daemon.StartupError{Socket: "/run/feat/feat.sock"}
	err := error(&NotRunningError{Socket: "/run/feat/feat.sock", Cause: cause})

	var notRunning *NotRunningError
	if !errors.As(err, &notRunning) {
		t.Fatal("a failed start is no longer a NotRunningError, so it no longer exits 4")
	}

	var startup *daemon.StartupError
	if !errors.As(err, &startup) {
		t.Error("the failed start is not reachable through the error it was wrapped in")
	}
}

// TestAnAbsentDaemonStillAdvisesTheCommand checks the other half: when nothing
// was tried, the advice is the useful thing to say.
func TestAnAbsentDaemonStillAdvisesTheCommand(t *testing.T) {
	message := (&NotRunningError{Socket: "/run/feat/feat.sock"}).Error()

	if !strings.Contains(message, "start one with `feat daemon start`") {
		t.Errorf("an absent daemon no longer says how to start one:\n%s", message)
	}
}

// TestANonInteractiveRunStartsNothingAndSaysSo covers the summary the root
// command prints in a pipe.
//
// It is the branch that must not spawn, and the one that reports what it
// observed rather than what it did. The interactive branch spawns a process, so
// it is exercised where processes are: the binary lifecycle test in
// internal/daemon.
func TestANonInteractiveRunStartsNothingAndSaysSo(t *testing.T) {
	layout := isolate(t)

	env := &environment{layout: &layout}
	summary := env.describeDaemon(&cobra.Command{})

	if summary.startErr != nil {
		t.Errorf("a non-interactive run tried to start a daemon: %v", summary.startErr)
	}
	if daemon.Answering(layout.Socket) {
		t.Error("a non-interactive run started a daemon")
	}
	if !strings.Contains(summary.line, "no daemon is running") {
		t.Errorf("summary = %q, want it to report the daemon's absence", summary.line)
	}
	if summary.socket != layout.Socket {
		t.Errorf("socket = %q, want %q", summary.socket, layout.Socket)
	}
}
