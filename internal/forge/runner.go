package forge

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

// commandTimeout bounds one forge command.
//
// Opening a merge request crosses a network to somebody else's server, so the
// bound is generous rather than tight. What it prevents is a request that will
// never answer — a proxy that accepted the connection and stopped talking, or a
// CLI that decided to prompt — from holding a publication open for ever while
// the repositories after it wait.
const commandTimeout = 2 * time.Minute

// HostRunner runs a forge CLI on the machine Feat is running on.
//
// It runs on the trusted host with the user's own environment, which is where
// the credential already is and the only place Feat makes a credentialed
// provider call. Nothing here adds a token, and nothing here passes one to an
// agent: the agent environment receives no provider credential at all
// (ADR-070).
type HostRunner struct {
	// Timeout bounds one command. Zero uses commandTimeout.
	Timeout time.Duration
}

var _ Runner = HostRunner{}

// Run executes one forge command as an argument vector.
//
// A command that ran and refused is not an error: a protected branch and an
// unauthenticated session are answers, and the adapter turns them into a
// recorded failure for one repository rather than into a failure of the
// publication. A command that could not be started is an error, because nothing
// was established about the forge at all.
func (r HostRunner) Run(ctx context.Context, command Command) (Output, error) {
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = commandTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// #nosec G204 -- the program is a constant in a forge adapter and every
	// argument is one vector element; nothing reaches a shell.
	process := exec.CommandContext(ctx, command.Program, command.Arguments...)
	process.Dir = command.Directory
	// The environment is left as this process's, which is the user's: the
	// authentication they already have on this host is what makes the call. Feat
	// adds nothing to it — a forge CLI that is not logged in says so rather than
	// being handed a credential from somewhere Feat invented — and it passes
	// nothing to an agent, whose environment receives no provider token at all
	// (ADR-070).

	var stdout, stderr bytes.Buffer
	process.Stdout = &stdout
	process.Stderr = &stderr

	err := process.Run()
	output := Output{Stdout: stdout.String(), Stderr: stderr.String()}
	if ctx.Err() != nil {
		return output, fmt.Errorf("`%s` did not answer within %s", command.Program, timeout)
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			output.ExitCode = exit.ExitCode()
			return output, nil
		}
		return output, fmt.Errorf("running `%s`: %w", command.Program, err)
	}
	return output, nil
}
