package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// NotImplementedError reports that a command belongs to the documented v0
// command surface but is delivered by a later implementation slice.
//
// Placeholder commands return this error rather than printing a success
// message, so that scripts and tests can never mistake an unimplemented
// command for a working one.
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

// notImplemented builds a cobra RunE that reports the slice owning the command.
func notImplemented(slice int, outcome string) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, _ []string) error {
		return &NotImplementedError{
			Command: cmd.CommandPath(),
			Slice:   slice,
			Outcome: outcome,
		}
	}
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
			return &usageError{cmd: cmd, err: err}
		}
		return nil
	}
}
