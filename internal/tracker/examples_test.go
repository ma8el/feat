package tracker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// examplesDir holds the worked ticket commands Feat ships, one per tracker,
// each beside the document it printed.
const examplesDir = "../../docs/examples/tickets"

// TestTheWorkedExamplesPrintWhatFeatAccepts validates every shipped example
// against the published shape, with the code `feat doctor` uses.
//
// It is the reason the examples are worth shipping at all: the file a new user
// copies cannot drift from what Feat accepts, which is what
// docs/examples/project.yaml gets from being checked against the configuration
// schema (ADR-071).
func TestTheWorkedExamplesPrintWhatFeatAccepts(t *testing.T) {
	for _, example := range examples(t) {
		t.Run(filepath.Base(example.output), func(t *testing.T) {
			body, err := os.ReadFile(example.output)
			if err != nil {
				t.Fatalf("reading %s: %v", example.output, err)
			}

			tickets, err := Parse(body)
			if err != nil {
				t.Fatalf("%s does not print what Feat accepts: %v", example.command, err)
			}
			if len(tickets) == 0 {
				t.Fatalf("%s prints no ticket, and an example that shows nothing "+
					"shows nothing about the mapping", example.output)
			}

			// A worked example has to work as an example: a user reading it is
			// looking for what each field turns into, and a ticket with no
			// description is one of the two cases the mapping has to get right.
			var described, empty int
			for _, ticket := range tickets {
				if ticket.Body == "" {
					empty++
					continue
				}
				described++
			}
			if described == 0 || empty == 0 {
				t.Errorf("%s prints %d tickets with a description and %d without; "+
					"an example should show both, because an empty description is a ticket",
					example.output, described, empty)
			}
		})
	}
}

// TestEveryWorkedExampleIsValidated is what stops a new example from skipping
// the check above: a command with nothing recorded beside it is a mapping
// nobody has checked, and a document with no command is one nothing produced.
func TestEveryWorkedExampleIsValidated(t *testing.T) {
	found := examples(t)
	if len(found) == 0 {
		t.Fatalf("no worked ticket commands in %s", examplesDir)
	}

	for _, example := range found {
		if _, err := os.Stat(example.output); err != nil {
			t.Errorf("%s has no recorded output beside it, so nothing validates its mapping\n"+
				"\tRecord what it printed as %s.", example.command, filepath.Base(example.output))
		}
	}

	recorded, err := filepath.Glob(filepath.Join(examplesDir, "*.output.json"))
	if err != nil {
		t.Fatalf("listing the recorded output: %v", err)
	}
	for _, output := range recorded {
		command := strings.TrimSuffix(output, ".output.json") + ".sh"
		if _, err := os.Stat(command); err != nil {
			t.Errorf("%s has no command beside it, so nothing says what printed it", output)
		}
	}
}

// TestEveryWorkedExampleIsRunnable checks the two things about a shipped script
// that a reader cannot check by reading it: that it can be executed at all, and
// that it says what it needs before it needs it.
func TestEveryWorkedExampleIsRunnable(t *testing.T) {
	for _, example := range examples(t) {
		t.Run(filepath.Base(example.command), func(t *testing.T) {
			info, err := os.Stat(example.command)
			if err != nil {
				t.Fatalf("reading %s: %v", example.command, err)
			}
			if info.Mode().Perm()&0o111 == 0 {
				t.Errorf("%s is not executable, and a tracker command is run rather than sourced",
					example.command)
			}

			body, err := os.ReadFile(example.command)
			if err != nil {
				t.Fatalf("reading %s: %v", example.command, err)
			}
			script := string(body)
			if !strings.HasPrefix(script, "#!") {
				t.Errorf("%s has no interpreter line", example.command)
			}
			// What a command needs is what a user finds out either by reading
			// the top of the file or by watching `feat doctor` fail.
			if !strings.Contains(script, "Needs:") {
				t.Errorf("%s does not say what it needs installed or authenticated", example.command)
			}
		})
	}
}

// example is one worked command and the document it printed.
type example struct {
	command string
	output  string
}

func examples(t *testing.T) []example {
	t.Helper()

	commands, err := filepath.Glob(filepath.Join(examplesDir, "*.sh"))
	if err != nil {
		t.Fatalf("listing the worked commands: %v", err)
	}
	found := make([]example, 0, len(commands))
	for _, command := range commands {
		found = append(found, example{
			command: command,
			output:  strings.TrimSuffix(command, ".sh") + ".output.json",
		})
	}
	return found
}
