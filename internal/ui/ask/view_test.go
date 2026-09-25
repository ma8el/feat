package ask

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/ma8el/feat/internal/wizard"
)

// servicesQuestion is the question the defect was reported at, with the flow's
// own text: `Detail` authored as pre-wrapped lines, and a note that is one long
// string with the thing it is about at the end of it.
func servicesQuestion() wizard.Question {
	return wizard.Question{
		ID: "runtime.services", Kind: wizard.KindText,
		Heading: "The application under development",
		Detail: []string{
			"The application under development, run per task. They are separate from",
			"the agent's own environment, and in this version they start only when you",
			"ask for them.",
			"",
			"They start only when you ask for them.",
		},
		Prompt: "Application services",
		Notes: []string{
			"built from this repository rather than mounting it, so Feat builds them " +
				"from the task's worktree and a change shows once the image is built again: web, worker",
		},
	}
}

// contextLines is the context as the terminal would draw it, without styling.
func contextLines(t *testing.T, model Model) []string {
	t.Helper()
	return strings.Split(ansi.Strip(model.Context()), "\n")
}

// TestTheContextFoldsIntoTheWidthItIsGiven is the reported defect. A note about
// which services are built from a repository ran to a hundred and sixty cells
// and was cut where it listed them, and a dialog reads a truncated line as
// exactly as wide as it is allowed, so the box took its full allowance.
func TestTheContextFoldsIntoTheWidthItIsGiven(t *testing.T) {
	const width = 86

	model := opened(t, servicesQuestion())
	model.SetContextWidth(width)

	lines := contextLines(t, model)
	for _, line := range lines {
		if measured := ansi.StringWidth(line); measured > width {
			t.Errorf("a line is %d cells in a %d-cell block: %q", measured, width, line)
		}
	}

	// The whole note, list and all: the end of it was cut, and the end is what
	// the note exists to say.
	context := strings.Join(lines, " ")
	if !strings.Contains(context, "built again: web, worker") {
		t.Errorf("the note is cut before what it is about:\n%s", model.Context())
	}
	if strings.Contains(context, "…") {
		t.Errorf("something is still being cut rather than folded:\n%s", model.Context())
	}
}

// TestAFoldedNoteHangsUnderItself checks the shape of a note that takes more
// than one line. A second line starting in the bullet's column reads as a
// second note, so the wrong count of lines is the wrong count of findings.
func TestAFoldedNoteHangsUnderItself(t *testing.T) {
	model := opened(t, servicesQuestion())
	model.SetContextWidth(60)

	var folded []string
	for _, line := range contextLines(t, model) {
		if strings.HasPrefix(line, "· ") {
			folded = append(folded, line)
		}
	}
	if len(folded) != 1 {
		t.Fatalf("the one note is drawn as %d:\n%s", len(folded), model.Context())
	}

	// The lines under it are indented to where its text starts.
	lines := contextLines(t, model)
	at := 0
	for i, line := range lines {
		if strings.HasPrefix(line, "· ") {
			at = i
		}
	}
	if at+1 >= len(lines) || !strings.HasPrefix(lines[at+1], "  ") {
		t.Errorf("the note's second line does not hang under its first:\n%s", model.Context())
	}
}

// TestTheDetailIsReflowedRatherThanRedrawnAsWritten is the other half of the
// symptom: a paragraph wrapped at about seventy-two cells inside a box three
// quarters of a wide terminal. The flow authors Detail as the lines it would be
// printed as, so rejoining a paragraph and folding it again makes those breaks
// the default rather than the limit.
func TestTheDetailIsReflowedRatherThanRedrawnAsWritten(t *testing.T) {
	question := servicesQuestion()
	widest := 0
	for _, line := range question.Detail {
		widest = max(widest, ansi.StringWidth(line))
	}

	model := opened(t, question)
	model.SetContextWidth(widest + 30)

	var reflowed bool
	for _, line := range contextLines(t, model) {
		if ansi.StringWidth(line) > widest {
			reflowed = true
		}
	}
	if !reflowed {
		t.Errorf("every line still stops at the flow's own break of %d cells:\n%s",
			widest, model.Context())
	}

	// The paragraph break survives the rejoining: the flow's blank line is where
	// one paragraph ends, and the two must not run together.
	context := ansi.Strip(model.Context())
	if !strings.Contains(context, "ask for them.\n\nThey start only when you ask for them.") {
		t.Errorf("the paragraph break was folded away with the line breaks:\n%s", context)
	}
}

// TestTheContextWithoutAWidthDrawsWhatTheFlowWrote is the terminal asker's
// half. That asker never calls Context: it prints the same fields itself, so
// they stay in the scrollback after the widget has exited (ADR-084). A caller
// that was not told a width gets the flow's own lines, unfolded, which is what
// the transcript's rule is measured against.
func TestTheContextWithoutAWidthDrawsWhatTheFlowWrote(t *testing.T) {
	question := servicesQuestion()
	model := opened(t, question)

	context := ansi.Strip(model.Context())
	for _, line := range question.Detail {
		if line == "" {
			continue
		}
		if !strings.Contains(context, line+"\n") {
			t.Errorf("the flow's own line is not drawn as it was written: %q\n%s", line, context)
		}
	}
	if !strings.Contains(context, question.Notes[0]+"\n") {
		t.Errorf("the note was folded for a caller that gave no width:\n%s", context)
	}
}
