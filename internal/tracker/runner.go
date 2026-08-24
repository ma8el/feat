package tracker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Command is a project's tracker command, resolved to an argument vector.
//
// It is an argument vector rather than an interpolated shell string, as every
// user-supplied command Feat runs is (CLAUDE.md architectural rules). Nothing
// in it is a template: a tracker command runs before there is a task, so there
// is no task for a placeholder to name, and configuration refuses one.
type Command struct {
	// Program is the executable to run.
	Program string
	// Arguments are its arguments, each one separate.
	Arguments []string
	// Directory is where it runs.
	//
	// It is explicit because the two processes that run a tracker command — the
	// daemon, and `feat doctor` — would otherwise inherit whichever directory
	// each happened to be started in, and a command that answered one and not
	// the other would be a project that is configured and does not work. The
	// caller passes the user's home directory: there is no task yet, so there
	// is no worktree to run in.
	Directory string
}

// Runner runs a tracker command.
//
// It is an interface so that what a tracker prints can be arranged in a test
// without a tracker, an account, or a network, and so that `feat doctor` and
// the daemon run one the same way.
type Runner interface {
	// Run executes the command and returns what it printed on standard output.
	Run(ctx context.Context, command Command) ([]byte, error)
}

// HostRunner runs a tracker command on the trusted host.
//
// Every credentialed provider call is made here, using the authentication the
// user already has: the agent environment receives no provider token and no
// tracker access at all (ADR-070).
type HostRunner struct{}

var _ Runner = HostRunner{}

// List runs the tracker command and returns the tickets it printed.
//
// A nil runner runs the command on this host.
//
// It bounds nothing itself: how long a tracker may take is the caller's, applied
// through the context, so that one place holds it (ADR-036). The two callers
// hold different numbers for good reasons — the daemon's is half of a contract
// its client waits on, and `feat doctor` bounds every command it runs the same
// way — and neither would survive a second bound here quietly winning.
func List(ctx context.Context, runner Runner, command Command) ([]Ticket, error) {
	if runner == nil {
		runner = HostRunner{}
	}
	if err := command.validate(); err != nil {
		return nil, err
	}

	output, err := runner.Run(ctx, command)
	if err != nil {
		return nil, err
	}
	return Parse(output)
}

// Run executes the tracker command with its own streams captured.
//
// Standard output is bounded, and a command that writes past the bound is
// refused by size rather than read to the end: the reason the bound exists is
// that what it prints becomes a brief.
func (HostRunner) Run(ctx context.Context, command Command) ([]byte, error) {
	if err := command.validate(); err != nil {
		return nil, err
	}

	// An argument vector, never an interpolated shell string (CLAUDE.md).
	// #nosec G204 -- the program and its arguments come from the project's own
	// configuration, which refuses a placeholder anywhere in a tracker command;
	// every element is separate and nothing reaches a shell.
	process := exec.CommandContext(ctx, command.Program, command.Arguments...)
	process.Dir = command.Directory

	stdout := &bounded{limit: MaxOutputBytes}
	// Standard error is bounded too, and far shorter: what a failing command
	// says is read for its first line, and a tool that answers a failure with a
	// megabyte should not be able to spend the daemon's memory on it. It is
	// truncated rather than stopped, because standard error is where a working
	// tool narrates, and a tracker killed for narrating too much would be a
	// tracker that works everywhere except here.
	stderr := &bounded{limit: maxErrorBytes, truncate: true}
	process.Stdout = stdout
	process.Stderr = stderr
	// A tracker command reads nothing. Without this it would inherit the
	// daemon's own standard input, and a command that decided to prompt for a
	// password would wait for a person who is not there.
	process.Stdin = nil

	// The budget is read before the command runs, so that a run which exceeded
	// it can say how long it was given. The caller sets it; a context carrying
	// none means the run was not bounded, and then only cancellation ends it.
	budget, bounded := time.Duration(0), false
	if deadline, ok := ctx.Deadline(); ok {
		budget, bounded = time.Until(deadline), true
	}

	err := process.Run()

	// The size refusal is decided before the exit status, because exceeding the
	// bound is what closed the pipe the command was writing to: the exit status
	// that follows describes Feat's refusal rather than the command's own
	// failure, and reporting that would name the wrong thing.
	if stdout.exceeded {
		return nil, &RejectionError{Oversized: true, Reason: fmt.Sprintf(
			"is larger than %d bytes, which is the limit", MaxOutputBytes)}
	}
	if err != nil {
		return nil, describeFailure(ctx, command, budget, bounded, stderr, stdout, err)
	}
	return stdout.Bytes(), nil
}

// maxErrorBytes bounds what a failing tracker command may write to standard
// error before Feat stops reading it. Only the first line is reported.
const maxErrorBytes = 8 << 10

// describeFailure says which kind of failure this was, because the remedies are
// different: an absent program is installed, a command that exceeded its bound
// is made faster or narrower, and one that ran and disagreed is fixed where it
// says.
func describeFailure(
	ctx context.Context, command Command, budget time.Duration, timed bool,
	stderr, stdout *bounded, err error,
) error {
	if ctx.Err() != nil {
		if !timed {
			return fmt.Errorf("the tracker command %s was cancelled before it answered", command.Program)
		}
		return fmt.Errorf("the tracker command %s did not answer within %s",
			command.Program, budget.Round(time.Millisecond))
	}
	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Errorf("the tracker command %s is not installed on this host", command.Program)
	}
	// Standard error first and then standard output, because which of the two
	// carries the reason is the failing tool's choice and not ours.
	if detail := firstLine(stderr.String(), stdout.String()); detail != "" {
		return fmt.Errorf("the tracker command %s failed: %s", command.Program, detail)
	}
	return fmt.Errorf("running the tracker command %s: %w", command.Program, err)
}

// validate rejects a command that cannot be run safely.
func (c Command) validate() error {
	if c.Program == "" {
		return errors.New("the project's tracker names no command to run")
	}
	if strings.HasPrefix(c.Program, "-") {
		return fmt.Errorf("the tracker command would run the program %q, which begins with %q "+
			"and would be read as an option", c.Program, "-")
	}
	for _, argument := range append([]string{c.Program}, c.Arguments...) {
		if strings.ContainsAny(argument, "\x00\n") {
			return errors.New("the tracker command carries an argument containing a NUL or a newline")
		}
	}
	if c.Directory == "" {
		return errors.New("the tracker command has no directory to run in")
	}
	return nil
}

// bounded is a writer that stops at a limit and remembers that it did.
//
// It stops rather than truncating silently, so that output past the bound is
// refused by size. Closing the pipe is what ends the command: the process is
// signalled on its next write rather than being read to the end.
type bounded struct {
	limit int
	// truncate keeps accepting after the limit and discards what is past it,
	// for a stream whose size is not the point.
	truncate bool
	buffer   bytes.Buffer
	exceeded bool
}

// errTooLarge ends the copy from the command's pipe. It never reaches a caller:
// Run reports the refusal from the exceeded flag, which says which stream it
// was and how large the limit is.
var errTooLarge = errors.New("the command printed more than the limit")

func (b *bounded) Write(p []byte) (int, error) {
	room := b.limit - b.buffer.Len()
	if len(p) <= room {
		return b.buffer.Write(p)
	}
	if room > 0 {
		_, _ = b.buffer.Write(p[:room])
	}
	b.exceeded = true
	if b.truncate {
		return len(p), nil
	}
	return 0, errTooLarge
}

func (b *bounded) Bytes() []byte  { return b.buffer.Bytes() }
func (b *bounded) String() string { return b.buffer.String() }

// firstLine returns the first non-empty line of a command's output, taking the
// streams in the order given so that a caller can say which one to prefer.
func firstLine(outputs ...string) string {
	for _, output := range outputs {
		for line := range strings.SplitSeq(output, "\n") {
			if trimmed := strings.TrimSpace(line); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}
