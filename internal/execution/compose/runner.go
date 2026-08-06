package compose

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/ma8el/feat/internal/execution"
)

// Runner executes host commands for the Compose adapter.
//
// It is an interface for the reason git.Runner and tmux.Runner are: a test can
// arrange a Compose that refuses, a container that is not running, or a probe
// that fails, without needing a machine in that state. Opt-in integration tests
// use the real one.
type Runner interface {
	// Run executes the command and returns what it produced. A command that
	// runs and exits non-zero is not an error: "the service is not running" is
	// an answer, and the caller decides what it means. Only a command that
	// could not be started at all fails.
	Run(ctx context.Context, invocation execution.Invocation) (execution.Output, error)
	// Look resolves an executable to an absolute path.
	Look(name string) (string, error)
}

// HostRunner runs Docker on the trusted host.
//
// Only the host runs Docker, ever. The agent's container receives no socket and
// no CLI, and this type is the only place in Feat that starts one
// (docs/05-security-model.md).
type HostRunner struct {
	// Timeout bounds one command. Zero uses the default.
	Timeout time.Duration
}

var _ Runner = HostRunner{}

// defaultTimeout bounds one Docker command.
//
// Bringing a service up can pull an image or run a build, which is why this is
// generous. It still exists, because a Compose that never answers should
// produce a diagnosis rather than a task that hangs for ever.
const defaultTimeout = 10 * time.Minute

// Look resolves an executable on the daemon's path.
func (r HostRunner) Look(name string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrNotInstalled, name)
	}
	return path, nil
}

// Run executes one Docker command and captures what it produced.
func (r HostRunner) Run(ctx context.Context, invocation execution.Invocation) (execution.Output, error) {
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// An argument vector, never an interpolated shell string (CLAUDE.md).
	process := exec.CommandContext(ctx, invocation.Program, invocation.Arguments...)
	process.Dir = invocation.Directory

	var stdout, stderr bytes.Buffer
	process.Stdout = &stdout
	process.Stderr = &stderr
	// A command run this way reads nothing. Without this it would inherit the
	// daemon's own standard input, and a Docker that decided to prompt would
	// wait for a person who is not there.
	process.Stdin = nil

	err := process.Run()
	output := execution.Output{Stdout: stdout.String(), Stderr: stderr.String()}

	var exit *exec.ExitError
	switch {
	case err == nil:
		return output, nil
	case errors.As(err, &exit):
		output.ExitCode = exit.ExitCode()
		return output, nil
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return output, fmt.Errorf("%s did not answer within %s", invocation.Program, timeout)
	case errors.Is(err, exec.ErrNotFound):
		return output, fmt.Errorf("%w: %s", ErrNotInstalled, invocation.Program)
	default:
		return output, fmt.Errorf("running %s: %w", invocation.Program, err)
	}
}

// ErrNotInstalled reports an executable this machine does not have.
var ErrNotInstalled = errors.New("not installed on this host")
