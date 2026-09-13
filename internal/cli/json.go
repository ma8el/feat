package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// jsonFlagName is the flag every command that can print a document offers.
//
// It is a local flag on each of them rather than a persistent one on the root,
// so that `feat --help` and the golden command surface say which commands have
// a document to print. A global flag would claim every command does.
const jsonFlagName = "json"

// Printing a document is opt-in, and a person at a terminal is the default
// reader: the table is for them, and a command that printed JSON by default
// would be answering the rarer question (ADR-099).
const jsonFlagUsage = "print the result as a JSON document instead of a table"

// addJSONFlag offers the flag on one command.
func addJSONFlag(cmd *cobra.Command) {
	cmd.Flags().Bool(jsonFlagName, false, jsonFlagUsage)
}

// wantsJSON reports whether this run should print a document.
//
// A command that does not offer the flag answers false rather than failing.
// `feat runtime status` has it and the three sibling actions built by the same
// constructor do not, so the branch this guards is compiled into all four.
func wantsJSON(cmd *cobra.Command) bool {
	asked, err := cmd.Flags().GetBool(jsonFlagName)
	return err == nil && asked
}

// emitJSON writes one document and nothing else.
//
// What a failure looks like in the document is that it does not appear in one:
// stdout carries the document or nothing, the message goes to standard error,
// and the exit code is the one the command would have used anyway. Those codes
// are already the machine-readable error surface — ADR-027 gave an absent daemon
// its own code so that a script would not have to parse output — and an error
// object on stdout would be a second surface saying the same thing.
//
// HTML escaping is off because a brief is Markdown a person wrote, and
// rewriting its angle brackets and ampersands as numeric escapes would make the
// document harder to read for the benefit of a browser that is not going to
// render it.
func emitJSON(out io.Writer, document any) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(document); err != nil {
		return fmt.Errorf("writing the JSON document: %w", err)
	}
	return nil
}
