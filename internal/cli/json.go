package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	feat "github.com/ma8el/feat"
)

// jsonFlagName is the flag every command that can print a document offers.
//
// It is a local flag on each of them rather than a persistent one on the root,
// so `feat --help` and the golden command surface say which commands have a
// document to print. A global flag would claim every command does.
const jsonFlagName = "json"

// Printing a document is opt-in because a person at a terminal is the default
// reader. A command that printed JSON by default would answer the rarer
// question (ADR-099).
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
// A failure never appears in the document. Stdout carries the document or
// nothing, the message goes to standard error, and the exit code is the one the
// command would have used anyway. Those codes are already the machine-readable
// error surface — ADR-027 gave an absent daemon its own code so a script need
// not parse output — and an error object on stdout would say the same thing
// twice.
//
// HTML escaping is off because a brief is Markdown a person wrote. Rewriting its
// angle brackets and ampersands as numeric escapes would make the document
// harder to read for a browser that is never going to render it.
func emitJSON(out io.Writer, document any) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(document); err != nil {
		return fmt.Errorf("writing the JSON document: %w", err)
	}
	return nil
}

// newOutputCommand groups what describes --json's documents. It sits beside
// `feat project schema` in the same noun-then-schema form (ADR-102).
func newOutputCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "output",
		Short: "Describe the documents --json prints",
		Long:  `Describe the documents Feat's commands print when given --json.`,
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "schema",
		Short: "Print the JSON Schema for --json output",
		Long: `Print the JSON Schema every document --json prints is described by.

It is schema/feat-output.schema.json from Feat's repository, embedded so an
installation without a checkout still has it. What it promises across releases
is recorded in ADR-102.`,
		Args: checkArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := cmd.OutOrStdout().Write(feat.OutputSchema())
			return err
		},
	})
	return cmd
}
