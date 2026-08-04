package daemon

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/ma8el/feat/internal/paths"
)

// envSpawned marks a process that was started as a daemon by Spawn.
//
// A daemon never starts a daemon: it is already the thing a client was asking
// for. Refusing that at the boundary bounds the damage when a binary is spawned
// with arguments it does not understand and re-runs the client path instead of
// serving — which, unguarded, is a process that spawns a process that spawns a
// process.
const envSpawned = "FEAT_DAEMON_SPAWNED"

// Waiting for a spawned daemon.
const (
	// defaultStartTimeout bounds how long a client waits for a daemon it
	// started. Acquiring a lock, opening a store, and binding a socket is fast;
	// anything slower is a failure worth reporting.
	defaultStartTimeout = 10 * time.Second
	// pollInterval is how often readiness is checked.
	pollInterval = 20 * time.Millisecond
	// defaultStopTimeout bounds waiting for a daemon to shut down.
	defaultStopTimeout = 10 * time.Second
	// logTailLines is how much of the log an unexplained startup failure
	// quotes.
	logTailLines = 10
)

// SpawnOptions configure starting a background daemon.
type SpawnOptions struct {
	// Executable is the binary to run. An empty value uses the running one, so
	// a client always starts the daemon it was built with.
	Executable string
	// Args make that binary run a daemon in the foreground, such as
	// {"daemon", "run"}. The caller supplies them because the command surface
	// belongs to internal/cli, not here.
	Args []string
	// Env is the child's environment. A nil value passes the current one, which
	// matters: the environment carries the overrides that decided this layout,
	// and a daemon resolving different paths than its client would be a daemon
	// nobody can reach.
	Env []string
	// Timeout bounds waiting for the daemon to answer. Zero uses the default.
	Timeout time.Duration
}

// Spawn starts a daemon in the background and waits until it answers.
//
// The child is detached into its own session, so it survives the client that
// started it — closing the terminal that ran `feat` must not stop the agents it
// is supervising. Its output goes to the daemon log, which is the only place a
// startup failure can be explained afterwards.
//
// Two clients starting a daemon at once is not an error: the loser's child exits
// reporting that a daemon is already running, and both clients then find the
// winner answering on the socket.
func Spawn(ctx context.Context, layout paths.Layout, opts SpawnOptions) (Endpoint, error) {
	if os.Getenv(envSpawned) != "" {
		return Endpoint{}, fmt.Errorf(
			"this process was started as a daemon (%s is set) and will not start another; "+
				"if it is not serving, it was spawned with arguments it does not understand",
			envSpawned)
	}

	executable := opts.Executable
	if executable == "" {
		self, err := os.Executable()
		if err != nil {
			return Endpoint{}, fmt.Errorf("locating this binary to start a daemon: %w", err)
		}
		executable = self
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultStartTimeout
	}

	logFile, err := openLogFile(layout)
	if err != nil {
		return Endpoint{}, err
	}
	defer func() { _ = logFile.Close() }()

	command := exec.Command(executable, opts.Args...)
	command.Env = opts.Env
	if command.Env == nil {
		command.Env = os.Environ()
	}
	command.Env = append(command.Env, envSpawned+"=1")
	// A daemon has no input, and its output belongs in the log rather than in
	// the client's terminal.
	command.Stdin = nil
	command.Stdout = logFile
	command.Stderr = logFile
	detach(command)

	if err := command.Start(); err != nil {
		return Endpoint{}, fmt.Errorf("starting a daemon with %s: %w", executable, err)
	}
	// The child is not this process's to reap: it outlives the client.
	if err := command.Process.Release(); err != nil {
		return Endpoint{}, fmt.Errorf("detaching the daemon process: %w", err)
	}

	endpoint, err := WaitUntilReady(ctx, layout, timeout)
	if err != nil {
		return Endpoint{}, err
	}
	return endpoint, nil
}

// WaitUntilReady waits for a daemon to answer on the socket and publish its
// record.
func WaitUntilReady(ctx context.Context, layout paths.Layout, timeout time.Duration) (Endpoint, error) {
	deadline := time.Now().Add(timeout)

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	var lastErr error
	for {
		if Answering(layout.Socket) {
			endpoint, err := ReadEndpoint(layout)
			if err == nil {
				return endpoint, nil
			}
			// Answering without a published record is the brief moment between
			// binding the socket and writing the file.
			lastErr = err
		}

		if !time.Now().Before(deadline) {
			return Endpoint{}, &StartupError{
				Socket:  layout.Socket,
				Waited:  timeout,
				LogFile: layout.LogFile(),
				LogTail: LogTail(layout.LogFile(), logTailLines),
				Err:     lastErr,
			}
		}

		select {
		case <-ctx.Done():
			return Endpoint{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

// Stop asks a running daemon to shut down and waits for it to stop answering.
//
// It returns the record of the daemon it stopped, so a caller can report which
// process it was.
func Stop(ctx context.Context, layout paths.Layout, timeout time.Duration) (Endpoint, error) {
	if timeout <= 0 {
		timeout = defaultStopTimeout
	}

	endpoint, err := ReadEndpoint(layout)
	if err != nil {
		return Endpoint{}, err
	}
	if !processExists(endpoint.PID) {
		// The record outlived its process. Feat reports that rather than
		// deleting it: the next start reclaims the directory after checking the
		// lock, which is a safer decision than this one would be.
		return endpoint, fmt.Errorf("%w: the endpoint record names pid %d, which is not running; "+
			"the next `feat daemon start` will reclaim the stale socket",
			ErrNotRunning, endpoint.PID)
	}

	if err := signalStop(endpoint.PID); err != nil {
		return endpoint, err
	}

	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		if !Answering(layout.Socket) && !processExists(endpoint.PID) {
			return endpoint, nil
		}
		if !time.Now().Before(deadline) {
			return endpoint, fmt.Errorf("the daemon with pid %d was asked to stop but was still running after %s",
				endpoint.PID, timeout)
		}
		select {
		case <-ctx.Done():
			return endpoint, ctx.Err()
		case <-ticker.C:
		}
	}
}
