package ask

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
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
//
// Everything here is folded into the width the caller gave, and a caller that
// gave none gets the flow's own lines. See SetContextWidth for what that fixes.
func (m Model) Context() string {
	var out strings.Builder
	if m.question.Heading != "" {
		out.WriteString(styled(TitleStyle, m.question.Heading, m.context) + "\n")
	}

	for i, paragraph := range m.paragraphs() {
		if i > 0 {
			// A paragraph break is a blank line, and styling an empty string
			// writes the colour around nothing.
			out.WriteString("\n")
		}
		if m.context < 1 {
			// No width to fold to, so the flow's breaks are the breaks. This is
			// what the asker with no width would get, and it is what the flow
			// authored the lines for.
			for _, line := range paragraph {
				out.WriteString(MutedStyle.Render(line) + "\n")
			}
			continue
		}
		out.WriteString(styled(MutedStyle, strings.Join(paragraph, " "), m.context) + "\n")
	}
	if len(m.question.Detail) > 0 {
		out.WriteString("\n")
	}

	// What the last answer established, in the colour of something Feat found
	// rather than something the user typed.
	//
	// The bullet is the width of its own indent, so a note that folds hangs under
	// its first line rather than under the mark: a second line starting in the
	// bullet's column reads as a second note, and these are the lines that carry
	// what Feat found out about the answer before this one.
	for _, note := range m.question.Notes {
		out.WriteString(AttentionStyle.Render("· ") +
			indentAfterFirst(styled(MutedStyle, note, m.context-noteMarker), noteMarker) + "\n")
	}
	if len(m.question.Notes) > 0 {
		out.WriteString("\n")
	}
	return out.String()
}

// noteMarker is the width of the bullet a note is written after.
const noteMarker = 2

// styled folds text into a width and colours each of its lines.
//
// Each line rather than the block. A style applied to several lines at once pads
// them all out to the widest, and a padded line reports itself as exactly as
// wide as the fold — so the box would shrink to the width it was allowed rather
// than to the width it needs, which is the measurement this fold was written to
// give back. It is the same reason Wrap takes lipgloss's own padding off again.
func styled(style lipgloss.Style, text string, width int) string {
	lines := strings.Split(Wrap(text, width), "\n")
	for i, line := range lines {
		if line == "" {
			// Styling an empty string writes the colour around nothing.
			continue
		}
		lines[i] = style.Render(line)
	}
	return strings.Join(lines, "\n")
}

// paragraphs are a question's detail, grouped as the flow's blank lines group
// it.
//
// The flow authors `Detail` as the lines it would be printed as, which is what
// `feat project init` prints and what the rule over a section is measured
// against (internal/cli's ruleWidth). Grouping rather than joining is what lets
// both readings out of one place: a caller with a width joins a group and folds
// it again, and a caller without one draws the lines it was given.
func (m Model) paragraphs() [][]string {
	var out [][]string
	var current []string
	for _, line := range m.question.Detail {
		if line == "" {
			if len(current) > 0 {
				out = append(out, current)
				current = nil
			}
			continue
		}
		current = append(current, line)
	}
	if len(current) > 0 {
		out = append(out, current)
	}
	return out
}

// indentAfterFirst puts every line of a block but the first under the first.
func indentAfterFirst(block string, cells int) string {
	lines := strings.Split(block, "\n")
	for i := 1; i < len(lines); i++ {
		lines[i] = strings.Repeat(" ", cells) + lines[i]
	}
	return strings.Join(lines, "\n")
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
