package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/term"
)

// Process exit codes, part of the CLI contract. Callers and tests rely on them
// to tell a real failure from an unimplemented command.
const (
	// ExitOK reports success.
	ExitOK = 0
	// ExitError reports a command failure.
	ExitError = 1
	// ExitUsage reports an invalid invocation, such as a bad flag or a wrong
	// number of positional arguments.
	ExitUsage = 2
	// ExitNotImplemented reports a command that exists in the v0 command
	// surface but does not do its work yet.
	ExitNotImplemented = 3
	// ExitNotRunning reports that a command needed a daemon and none was
	// running. It is separate from ExitError because an absent daemon is a state
	// a script may want to act on rather than a failed command, the same
	// distinction `systemctl is-active` makes (ADR-027).
	ExitNotRunning = 4
	// ExitInterrupted reports that the process was cancelled by a signal.
	ExitInterrupted = 130
)

// Execute runs the feat command tree and returns the process exit code.
//
// It never calls os.Exit, so a test can drive the whole command surface in
// process.
func Execute(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return execute(ctx, Options{Interactive: interactive()}, args, stdout, stderr)
}

// execute runs the command tree with explicit options.
//
// A test supplies its own path layout and command runner instead of the
// process's, and still goes through the exit-code mapping the real binary uses.
func execute(ctx context.Context, opts Options, args []string, stdout, stderr io.Writer) int {
	root := NewRootCommand(opts)
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)

	err := root.ExecuteContext(ctx)
	if err == nil {
		return ExitOK
	}

	var usage *usageError
	if errors.As(err, &usage) {
		reportf(stderr, "feat: %v\n\n%s\n", usage, usage.cmd.UsageString())
		return ExitUsage
	}

	var notImplemented *NotImplementedError
	if errors.As(err, &notImplemented) {
		reportf(stderr, "feat: %v\n", notImplemented)
		return ExitNotImplemented
	}

	var diagnosis *diagnosisError
	if errors.As(err, &diagnosis) {
		// The report is already on stdout. This adds the one line saying the
		// exit code was deliberate.
		reportf(stderr, "feat: %v\n", diagnosis)
		return ExitError
	}

	var notRunning *NotRunningError
	if errors.As(err, &notRunning) {
		// The command already printed what it observed on stdout, so this adds
		// only what the user can do about it.
		reportf(stderr, "feat: %v\n", notRunning)
		return ExitNotRunning
	}

	if errors.Is(err, context.Canceled) {
		return ExitInterrupted
	}

	reportf(stderr, "feat: %v\n", err)
	return ExitError
}

// reportf writes a diagnostic to the error stream. A failed write is dropped
// because failing to report a failure is not itself actionable.
func reportf(stderr io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(stderr, format, args...)
}

// interactive reports whether the real process streams are attached to a
// terminal. The TUI needs both directions. Without them the root command falls
// back to a plain-text rendering, so `feat` stays usable in pipes and CI.
func interactive() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

// terminalStderr reports whether standard error is a terminal, which decides
// whether the foreground daemon mirrors its log there.
func terminalStderr() bool { return term.IsTerminal(int(os.Stderr.Fd())) }
