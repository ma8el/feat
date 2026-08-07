package resources

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

// HostRunner runs an observation command on the trusted host.
//
// Only the host observes. Nothing here reaches inside a container or an agent's
// environment: what a container is using is a question for the container
// runtime, asked on the host, exactly as every other Docker command Feat runs
// is (docs/05-security-model.md, Docker boundary).
type HostRunner struct {
	// Timeout bounds one command. Zero uses the default.
	Timeout time.Duration
}

var _ Runner = HostRunner{}

// defaultTimeout bounds one observation command.
//
// It is generous because `docker stats` takes between one and two seconds even
// with --no-stream, which is measured rather than assumed (ADR-035). It still
// exists, because a tool that never answers must not hold a sampling loop.
const defaultTimeout = 30 * time.Second

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

	var stdout, stderr bytes.Buffer
	process.Stdout = &stdout
	process.Stderr = &stderr
	// An observation reads nothing. Without this it would inherit the daemon's
	// own standard input.
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
