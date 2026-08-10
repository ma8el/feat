package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// NotImplementedError reports that a command belongs to the documented v0
// command surface but is delivered by a later implementation slice.
//
// Placeholder commands return this error rather than printing a success
// message, so that scripts and tests can never mistake an unimplemented
// command for a working one.
//
// Slice 12 leaves no command using it: every entry in the documented surface now
// does its work. The type stays because the exit-code contract in Execute reads
// it and because a later slice that defers something needs it — reintroducing
// the two-line constructor beside its one caller is less than the cost of a dead
// helper nobody can see the shape of.
type NotImplementedError struct {
	// Command is the full command path, such as "feat daemon start".
	Command string
	// Slice is the implementation slice that delivers the command.
	Slice int
	// Outcome summarises what that slice delivers.
	Outcome string
}

func (e *NotImplementedError) Error() string {
	return fmt.Sprintf(
		"%s is not implemented yet: it is delivered by implementation slice %d (%s). See docs/11-implementation-plan.md",
		e.Command, e.Slice, e.Outcome,
	)
}

// NotRunningError reports that a command needed a running daemon and found
// none.
//
// It carries its own exit code because "nothing is running" is a state rather
// than a failure: a script that starts a daemon only when it has to should not
// have to parse output to find that out.
type NotRunningError struct {
	// Detail explains the situation when there is more to say than that no
	// daemon is running, such as a record left behind by a process that is gone.
	Detail string
	// Socket is where a daemon would be listening.
	Socket string
}

func (e *NotRunningError) Error() string {
	if e.Detail != "" {
		return e.Detail + "; start one with `feat daemon start`"
	}
	return "no feat daemon is running on " + e.Socket + "; start one with `feat daemon start`"
}

// usageError marks an argument or flag error so that Execute can report the
// usage exit code and print the command's usage text.
type usageError struct {
	cmd *cobra.Command
	err error
}

func (e *usageError) Error() string { return e.err.Error() }

func (e *usageError) Unwrap() error { return e.err }

// checkArgs wraps a positional-argument validator so that violations become
// usage errors. cobra.NoArgs also produces the "unknown command" message, so
// wrapping it gives unknown subcommands the usage exit code too.
func checkArgs(validator cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := validator(cmd, args); err != nil {
			return &usageError{cmd: cmd, err: suggest(cmd, args, err)}
		}
		return nil
	}
}

// suggest names the subcommands a rejected word was probably meant to be.
//
// Cobra builds this itself, but only on the path it takes for a command with no
// Args of its own, and every command here has one. So every unknown word was
// answered with "unknown command" and nothing else (ADR-040). It is done here,
// where a rejection already passes through, so that a name that moved and a name
// that was mistyped get the same answer.
func suggest(cmd *cobra.Command, args []string, err error) error {
	if len(args) == 0 || !cmd.HasAvailableSubCommands() {
		return err
	}
	// Cobra applies this default where it does its own suggesting, which is the
	// path that does not run here.
	if cmd.SuggestionsMinimumDistance <= 0 {
		cmd.SuggestionsMinimumDistance = 2
	}
	suggestions := cmd.SuggestionsFor(args[0])
	if len(suggestions) == 0 {
		return err
	}
	return fmt.Errorf("%w\n\nDid you mean this?\n\t%s", err, strings.Join(suggestions, "\n\t"))
}
