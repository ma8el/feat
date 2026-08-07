package review

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// HostRunner runs a check on the trusted host.
//
// It exists rather than reusing agent.HostRunner because that one bounds every
// command at twenty seconds, which is right for a probe asking whether a
// provider CLI is authenticated and wrong for a test suite. The bound here is
// the gate's, and the gate applies it through the context rather than the runner
// so that one place decides it (ADR-036).
type HostRunner struct{}

var _ Runner = HostRunner{}

// Run executes the check with its own streams captured.
//
// A check that runs and exits non-zero is not an error: that is the answer the
// gate exists to collect. Only a check that could not be started at all fails,
// and it fails with a message that says which of the two happened, because the
// remedies are different — an absent program is installed, and a failing one is
// fixed.
func (HostRunner) Run(ctx context.Context, check Check) (Output, error) {
	if err := check.validate(); err != nil {
		return Output{}, err
	}

	// An argument vector, never an interpolated shell string (CLAUDE.md).
	// #nosec G204 -- the program and its arguments come from the project's own
	// configuration, whose templates are validated and whose program may never
	// be a placeholder; every element is separate and nothing reaches a shell.
	process := exec.CommandContext(ctx, check.Program, check.Arguments...)
	process.Dir = check.Directory

	var stdout, stderr bytes.Buffer
	process.Stdout = &stdout
	process.Stderr = &stderr
	// A check reads nothing. Without this it would inherit the daemon's own
	// standard input, and a command that decided to prompt would wait for a
	// person who is not there.
	process.Stdin = nil

	err := process.Run()
	output := Output{Stdout: stdout.String(), Stderr: stderr.String()}

	var exit *exec.ExitError
	switch {
	case err == nil:
		return output, nil
	case errors.As(err, &exit):
		output.ExitCode = exit.ExitCode()
		if output.ExitCode < 0 {
			// Killed by a signal, which for a check under a deadline is the
			// bound expiring. The gate reads the context and says so.
			output.ExitCode = 1
		}
		return output, nil
	case errors.Is(err, exec.ErrNotFound):
		return output, fmt.Errorf("%s is not installed on this host", check.Program)
	default:
		return output, fmt.Errorf("running %s: %w", check.Program, err)
	}
}

// validate rejects a check that cannot be run safely.
func (c Check) validate() error {
	if c.ID == "" {
		return errors.New("a check has no identifier")
	}
	if c.Program == "" {
		return fmt.Errorf("check %s names no program", c.ID)
	}
	if strings.HasPrefix(c.Program, "-") {
		return fmt.Errorf("check %s would run the program %q, which begins with %q and would be read as an option",
			c.ID, c.Program, "-")
	}
	for _, value := range append([]string{c.Program}, c.Arguments...) {
		if strings.ContainsAny(value, "\x00\n") {
			return fmt.Errorf("check %s carries an argument containing a NUL or a newline", c.ID)
		}
	}
	if c.Directory == "" {
		return fmt.Errorf("check %s has no directory to run in", c.ID)
	}
	return nil
}
