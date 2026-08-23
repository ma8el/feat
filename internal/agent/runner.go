package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// probeTimeout bounds one validation probe.
//
// A provider CLI's authentication check may reach the network, and a task
// launch must not wait indefinitely on a host with no route to it. The bound is
// long enough for a slow answer and short enough that a user sees a diagnosis
// rather than a hang.
const probeTimeout = 20 * time.Second

// HostRunner executes probe commands on the trusted host.
//
// It is the environment for host-native execution; a devcontainer supplies a
// runner that executes the same commands inside the configured container, which
// is why nothing here is specific to a provider or to a check.
type HostRunner struct{}

var _ Runner = HostRunner{}

// Run executes the command and captures what it produced.
//
// A command that runs and exits non-zero is not an error: "glab is not
// authenticated" is an answer, and the caller decides what it means. Only a
// command that could not be started at all fails.
func (HostRunner) Run(ctx context.Context, command Command) (Output, error) {
	if err := command.validate(); err != nil {
		return Output{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	// An argument vector, never an interpolated shell string (CLAUDE.md).
	process := exec.CommandContext(ctx, command.Program, command.Arguments...)
	process.Dir = command.Directory

	var stdout, stderr bytes.Buffer
	process.Stdout = &stdout
	process.Stderr = &stderr
	// A probe reads nothing. Without this it would inherit the daemon's own
	// standard input, and a CLI that decided to prompt would wait for a person
	// who is not there.
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
		return output, fmt.Errorf("%s did not answer within %s", command.Program, probeTimeout)
	case errors.Is(err, exec.ErrNotFound):
		return output, fmt.Errorf("%w: %s", ErrNotInstalled, command.Program)
	default:
		return output, fmt.Errorf("running %s: %w", command.Program, err)
	}
}

// ErrNotInstalled reports an executable the environment does not have.
//
// It is a distinct error because the remedy is distinct: an absent CLI is
// installed, while an unauthenticated one is logged in to.
var ErrNotInstalled = errors.New("not installed in the agent environment")

// validate rejects a command that could be read as something other than itself.
func (c Command) validate() error {
	if c.Program == "" {
		return fmt.Errorf("a probe command must name a program")
	}
	if strings.HasPrefix(c.Program, "-") {
		return fmt.Errorf("a probe program must not begin with %q, but %q does", "-", c.Program)
	}
	for _, value := range append([]string{c.Program}, c.Arguments...) {
		if strings.ContainsAny(value, "\x00\n\r") {
			return fmt.Errorf("a probe argument must not contain a NUL or newline, but %q does", value)
		}
	}
	return nil
}
