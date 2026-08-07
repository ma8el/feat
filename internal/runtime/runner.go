package runtime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

// HostRunner runs the container tool on the trusted host.
//
// Only the host runs Docker, ever. The agent's container receives no socket and
// no CLI, and the runtime the user starts from Feat is a host action even though
// the services it starts are the ones the agent's code will be tested against
// (docs/05-security-model.md, Docker boundary).
type HostRunner struct {
	// Timeout bounds one command. Zero uses the default.
	Timeout time.Duration
}

var _ Runner = HostRunner{}

// defaultTimeout bounds one Docker command.
//
// Bringing an application up can pull images or run builds, which is why this is
// generous. It still exists, because a Compose that never answers should produce
// a diagnosis rather than a request that hangs.
const defaultTimeout = 10 * time.Minute

// Look resolves an executable on the daemon's path.
func (r HostRunner) Look(name string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrNotInstalled, name)
	}
	return path, nil
}

// Run executes one command and captures what it produced.
func (r HostRunner) Run(ctx context.Context, invocation Invocation) (Output, error) {
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
	output := Output{Stdout: stdout.String(), Stderr: stderr.String()}

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
