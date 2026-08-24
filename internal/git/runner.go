package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Executable is the program this package drives. It is never a shell.
const Executable = "git"

// commandTimeout bounds one Git command.
//
// A fetch over a slow network is the only command here that legitimately takes
// long, so the bound is generous rather than tight. What it prevents is a
// command that will never answer — a credential prompt on a terminal nobody is
// watching, or a remote that accepted the connection and then stopped talking —
// from holding a task launch open forever.
const commandTimeout = 2 * time.Minute

// Runner runs Git.
//
// It is an interface so that planning, applying, and observing can be tested
// against the exact argument vectors they produce, and against failures that are
// difficult to arrange with a real repository. The opt-in tests use HostRunner
// against real repositories, because a fake runner can only confirm the
// assumptions its author already had.
type Runner interface {
	// Run executes one Git command in dir and returns its standard output with
	// surrounding whitespace removed. A command that ran and failed returns an
	// *ExitError.
	Run(ctx context.Context, dir string, args ...string) (string, error)
	// RunWith is Run with extra environment entries, as KEY=VALUE.
	//
	// It exists because two of this package's operations have to be run under
	// settings rather than under flags: a task session's Git runs with autostash
	// turned off, and a host-side push runs with hooks, the pager, and an
	// external diff driver turned off. Both are expressed in Git's own
	// GIT_CONFIG_COUNT form, because the alternative is writing them into the
	// user's configuration file — which a linked worktree shares with the user's
	// own checkout, so a value set to protect one task would outlive it
	// (environment.go, ADR-056, ADR-070).
	//
	// It is on the interface rather than optional so that a runner which cannot
	// carry them cannot be handed a command that needs them. A push that
	// silently ran with the settings it was meant to run without is exactly the
	// failure the settings exist to prevent.
	RunWith(ctx context.Context, dir string, env []string, args ...string) (string, error)
}

// HostRunner runs Git on the machine Feat is running on.
type HostRunner struct {
	// Timeout bounds one command. Zero uses commandTimeout.
	Timeout time.Duration
}

// Run executes one Git command as an argument vector.
//
// The process inherits the user's environment, so credential helpers, SSH
// agents, and Git configuration keep working, with one addition:
// GIT_TERMINAL_PROMPT=0 turns an interactive credential prompt into a failure.
// Feat runs Git with no terminal attached, so a prompt would otherwise hang
// until the timeout and report nothing useful about why.
func (r HostRunner) Run(ctx context.Context, dir string, args ...string) (string, error) {
	return r.RunWith(ctx, dir, nil, args...)
}

// RunWith executes one Git command with extra environment entries.
func (r HostRunner) RunWith(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = commandTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	command := exec.CommandContext(ctx, Executable, args...)
	command.Dir = dir
	command.Env = append(command.Environ(), "GIT_TERMINAL_PROMPT=0")
	command.Env = append(command.Env, env...)

	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	err := command.Run()
	if ctx.Err() != nil {
		return "", &TimeoutError{Args: args, Dir: dir, Timeout: timeout}
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return "", &ExitError{
				Args:   args,
				Dir:    dir,
				Code:   exit.ExitCode(),
				Stderr: strings.TrimSpace(stderr.String()),
			}
		}
		return "", fmt.Errorf("running `git %s`: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// ExitError reports a Git command that ran and failed.
//
// The exit code is part of the error because Git uses it to answer questions
// rather than to report trouble: `merge-base --is-ancestor` exits 1 for "no",
// and `rev-parse --verify --quiet` exits 1 for "no such ref". A caller that
// cannot tell those from a real failure would have to parse messages.
type ExitError struct {
	// Args is the argument vector, without the program name.
	Args []string
	// Dir is the working directory the command ran in.
	Dir string
	// Code is the process exit code.
	Code int
	// Stderr is what Git reported, trimmed.
	Stderr string
}

func (e *ExitError) Error() string {
	message := fmt.Sprintf("`git %s` failed with exit code %d", strings.Join(e.Args, " "), e.Code)
	if e.Dir != "" {
		message += " in " + e.Dir
	}
	if e.Stderr != "" {
		message += ": " + firstLine(e.Stderr)
	}
	return message
}

// TimeoutError reports a Git command that never answered.
type TimeoutError struct {
	// Args is the argument vector, without the program name.
	Args []string
	// Dir is the working directory the command ran in.
	Dir string
	// Timeout is how long it was given.
	Timeout time.Duration
}

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("`git %s` did not answer within %s in %s",
		strings.Join(e.Args, " "), e.Timeout, e.Dir)
}

// exitCode returns the exit code of a failed Git command, and whether the error
// was one.
func exitCode(err error) (int, bool) {
	var exit *ExitError
	if errors.As(err, &exit) {
		return exit.Code, true
	}
	return 0, false
}

// firstLine returns the first non-empty line of a command's error output.
//
// Git's first line says what went wrong; the rest is usually advice addressed to
// a person at a terminal, and Feat has its own advice to give.
func firstLine(output string) string {
	for _, line := range strings.Split(output, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
