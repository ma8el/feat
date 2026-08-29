package ask

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/ma8el/feat/internal/wizard"
)

// Context draws what the question is asked in light of: which part of the file
// this is, what the user needs before deciding, and what the last answer
// established.
//
// It is drawn apart from the question because one of the two askers says it in
// its own shape. `feat project init` announces a section with a heading and a
// blank line and prints the notes above the widget, where they stay in the
// scrollback after the widget has exited; the dialog has no scrollback and
// draws the whole question at once (ADR-084).
func (m Model) Context() string {
	var out strings.Builder
	if m.question.Heading != "" {
		out.WriteString(TitleStyle.Render(m.question.Heading) + "\n")
	}
	for _, line := range m.question.Detail {
		// A paragraph break is a blank line, and styling an empty string writes
		// the colour around nothing.
		if line == "" {
			out.WriteString("\n")
			continue
		}
		out.WriteString(MutedStyle.Render(line) + "\n")
	}
	if len(m.question.Detail) > 0 {
		out.WriteString("\n")
	}

	// What the last answer established, in the colour of something Feat found
	// rather than something the user typed.
	for _, note := range m.question.Notes {
		out.WriteString(AttentionStyle.Render("· ") + MutedStyle.Render(note) + "\n")
	}
	if len(m.question.Notes) > 0 {
		out.WriteString("\n")
	}
	return out.String()
}

// View draws the question and how it is answered: the field with what is in it,
// or the options with the cursor on one.
func (m Model) View() string {
	var out strings.Builder
	out.WriteString(m.question.Prompt + "\n")
	if m.question.Kind == wizard.KindText {
		out.WriteString("  " + m.field() + "\n")
		if lines := m.below(); len(lines) > 0 {
			out.WriteString("\n")
			for _, line := range lines {
				out.WriteString(MutedStyle.Render("  "+line) + "\n")
			}
		}
		return out.String()
	}
	return out.String() + m.optionsView()
}

// Hints are the keys that answer this question, for a footer.
//
// back says whether the caller has a question to go back to. The dashboard
// always has — stepping back out of the first one closes the dialog — and the
// conversation has one at every question but the first, and none at all at the
// offers it asks after the file is written.
func (m Model) Hints(back bool) string {
	hints := []string{KeyHint("enter", "continue")}
	if m.question.Kind == wizard.KindText {
		if len(m.question.Candidates) > 0 {
			// Said only where there is something to complete, because a key that
			// does nothing on most questions is worse than one nobody was told
			// about: the hint is how a user learns that this question has more
			// than the one value in it.
			hints = append(hints, KeyHint("tab", "complete"))
		}
	} else {
		hints = append([]string{KeyHint("↑↓", "select")}, hints...)
	}
	if back {
		hints = append(hints, KeyHint("esc", "back"))
	}
	return KeyHints(append(hints, KeyHint("ctrl+c", "cancel"))...)
}

// field is the answer being typed, drawn in the width the field was given.
//
// The widget pads its line out to that width from the typed value alone and
// then writes the completion after the padding, so a suggestion made the line as
// wide as the field plus the whole of what it was suggesting. The dialog sizes
// itself to its widest line: the box jumped to its full allowance on the first
// character of a path and crept back a cell per keystroke afterwards, while
// typing something no candidate matches left it perfectly still.
//
// One more cell than the field's width, because the cursor sits after the value
// and that is what the widget draws in every other state too — so the line is
// the same width whether it holds a placeholder, a value, or a value with a
// completion behind it. The completion is shown as far as there is room for it,
// which is all the field ever promised for a value that is too long as well.
func (m Model) field() string {
	if m.input.Width <= 0 {
		return m.input.View()
	}
	return ansi.Truncate(m.input.View(), m.input.Width+1, "")
}

// below is what has to be said about a field that looks emptier than it is.
//
// What used to be here as well was the sentence naming the key that fills it.
// That sentence is the flow's now: it was drawn here because the flow was not
// allowed to name a key the command line had not got, and the command line has
// one (ADR-084).
func (m Model) below() []string {
	if m.question.Optional {
		return []string{"an empty answer is an answer here"}
	}
	return nil
}

// optionsView draws the answers a closed question offers.
func (m Model) optionsView() string {
	var out strings.Builder
	for i, option := range m.options() {
		label := Label(m.question.Kind, option)
		if i == m.cursor {
			out.WriteString(SelectedStyle.Render("▸ "+label) + "\n")
			continue
		}
		out.WriteString("  " + label + "\n")
	}
	return out.String()
}
