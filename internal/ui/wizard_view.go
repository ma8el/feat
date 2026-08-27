package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/ma8el/feat/internal/wizard"
)

// View renders configuring a project.
func (w wizardModel) View() string {
	var out strings.Builder
	if trail := w.trail(); trail != "" {
		out.WriteString(trail + "\n\n")
	}

	switch w.step {
	case wizardReviewing:
		out.WriteString(w.reviewView())
	case wizardChecking:
		out.WriteString(w.checkView())
	case wizardRegistering:
		out.WriteString(w.registerView())
	case wizardDone:
		out.WriteString(w.doneView())
	default:
		out.WriteString(w.questionView())
	}

	out.WriteString("\n")
	switch {
	case w.err != nil && w.step != wizardDone:
		// Wrapped rather than cut. It is the one string on this screen Feat did
		// not write, it is the reason an answer was refused, and the dialog
		// composites by cell — so a long one would otherwise end in an ellipsis
		// exactly where the reason is.
		out.WriteString("\n" + w.wrap(failureStyle.Render(w.err.Error())) + "\n")
	case w.busy && w.status != "":
		out.WriteString("\n" + mutedStyle.Render(w.status) + "\n")
	default:
		out.WriteString("\n")
	}

	out.WriteString("\n" + w.hints())
	return out.String()
}

// trail shows which part of the configuration is being answered.
//
// The sections are fixed and the questions inside one are not, so this is what
// can be said about progress honestly: a user answering a second repository is
// still in the same part of the file (ADR-063).
func (w wizardModel) trail() string {
	if w.step != wizardAsking || !w.asked {
		// The dialog's own heading already names this screen, and the steps that
		// follow the questions are not sections of the file.
		return ""
	}

	rendered := make([]string, 0, len(wizard.Sections()))
	for _, section := range wizard.Sections() {
		if section == w.question.Section {
			rendered = append(rendered, selectedStyle.Render(string(section)))
			continue
		}
		rendered = append(rendered, mutedStyle.Render(string(section)))
	}
	return strings.Join(rendered, mutedStyle.Render(" › "))
}

// questionView draws one question: what it is about, what has been found out,
// and how to answer it.
func (w wizardModel) questionView() string {
	if !w.asked {
		return mutedStyle.Render("reading what this machine can answer…")
	}

	var out strings.Builder
	if w.question.Heading != "" {
		out.WriteString(titleStyle.Render(w.question.Heading) + "\n")
	}
	for _, line := range w.question.Detail {
		out.WriteString(mutedStyle.Render(line) + "\n")
	}
	if len(w.question.Detail) > 0 {
		out.WriteString("\n")
	}

	// What the last answer established, in the colour of something Feat found
	// rather than something the user typed.
	for _, note := range w.question.Notes {
		out.WriteString(attentionStyle.Render("· ") + mutedStyle.Render(note) + "\n")
	}
	if len(w.question.Notes) > 0 {
		out.WriteString("\n")
	}

	out.WriteString(w.question.Prompt + "\n")
	if w.question.Kind == wizard.KindText {
		out.WriteString("  " + w.field() + "\n")
		if lines := w.below(); len(lines) > 0 {
			out.WriteString("\n")
			for _, line := range lines {
				out.WriteString(mutedStyle.Render("  "+line) + "\n")
			}
		}
		return out.String()
	}
	return out.String() + w.optionsView()
}

// field is the answer being typed, drawn in the width the field was given.
//
// The widget pads its line out to that width from the typed value alone and
// then writes the completion after the padding, so a suggestion made the line as
// wide as the field plus the whole of what it was suggesting. This dialog sizes
// itself to its widest line: the box jumped to its full allowance on the first
// character of a path and crept back a cell per keystroke afterwards, while
// typing something no candidate matches left it perfectly still.
//
// One more cell than the field's width, because the cursor sits after the value
// and that is what the widget draws in every other state too — so the line is
// the same width whether it holds a placeholder, a value, or a value with a
// completion behind it. The completion is shown as far as there is room for it,
// which is all the field ever promised for a value that is too long as well.
func (w wizardModel) field() string {
	if w.input.Width <= 0 {
		return w.input.View()
	}
	return ansi.Truncate(w.input.View(), w.input.Width+1, "")
}

// below is what has to be said about a field that looks emptier than it is.
//
// The key is named only where nothing in the field shows what it would give. A
// question that proposes something has that value under the cursor already and
// the hints carry the rest; a question that offers files and proposes none of
// them — the loop, where an empty answer means "no more" — is a blank field
// whose contents are one key away, and the flow's own sentence must not name a
// key, because the conversation has no such key to name (ADR-077).
func (w wizardModel) below() []string {
	var lines []string
	if w.question.Proposed == "" && len(w.question.Candidates) > 0 {
		lines = append(lines, "press tab to use one of them")
	}
	if w.question.Optional {
		lines = append(lines, "an empty answer is an answer here")
	}
	return lines
}

// optionsView draws the answers a closed question offers.
func (w wizardModel) optionsView() string {
	var out strings.Builder
	for i, option := range w.options() {
		label := optionLabel(w.question.Kind, option)
		if i == w.cursor {
			out.WriteString(selectedStyle.Render("▸ "+label) + "\n")
			continue
		}
		out.WriteString("  " + label + "\n")
	}
	return out.String()
}

// reviewView draws the composed configuration, which is what is being confirmed.
//
// It is the whole file and it is already validated: what the user is looking at
// is a configuration Feat accepts, and confirming writes exactly these bytes
// (ADR-062).
func (w wizardModel) reviewView() string {
	var out strings.Builder
	out.WriteString(w.wrap(fieldStyle.Render("file")+w.review.Path) + "\n\n")

	lines := strings.Split(strings.TrimRight(string(w.review.Text), "\n"), "\n")
	height := w.reviewHeight()
	first := min(w.scroll, max(0, len(lines)-1))
	last := min(len(lines), first+height)

	// Every line is drawn in the width of the file's widest, so that scrolling
	// through it does not resize the dialog around it. The measure is the whole
	// file rather than the window, because the window is what changes.
	block := reviewBlock(lines, max(1, w.width-2))
	for _, line := range lines[first:last] {
		out.WriteString(mutedStyle.Render(pad(line, block)) + "\n")
	}
	if last < len(lines) {
		out.WriteString(mutedStyle.Render("… " + count(len(lines)-last, "more line", "more lines")))
		out.WriteString("\n")
	}

	out.WriteString("\n" + mutedStyle.Render("nothing has been written yet") + "\n")
	return out.String()
}

// reviewBlock is the width the configuration is drawn in: its own widest line,
// up to what the dialog allows.
func reviewBlock(lines []string, limit int) int {
	widest := 0
	for _, line := range lines {
		if measured := ansi.StringWidth(line); measured > widest {
			widest = measured
		}
	}
	return min(max(widest, 1), limit)
}

// checkView draws what this machine says about the project that now exists.
func (w wizardModel) checkView() string {
	var out strings.Builder
	out.WriteString(w.wrap(fieldStyle.Render("wrote")+w.path) + "\n\n")
	out.WriteString(w.check.body(max(20, w.width), w.reviewHeight()))
	return out.String()
}

// registerView offers the daemon the file that now exists.
func (w wizardModel) registerView() string {
	var out strings.Builder
	out.WriteString(w.wrap(fieldStyle.Render("wrote")+w.path) + "\n\n")
	out.WriteString(w.wrap(mutedStyle.Render(
		"registering tells the daemon the project exists. The file stays where it is either way.")) + "\n\n")
	out.WriteString("Register it now?\n")

	for i, option := range []string{"yes", "no"} {
		if i == w.cursor {
			out.WriteString(selectedStyle.Render("▸ "+option) + "\n")
			continue
		}
		out.WriteString("  " + option + "\n")
	}
	return out.String()
}

// doneView is what happened, which is the one screen that is read rather than
// answered.
func (w wizardModel) doneView() string {
	var out strings.Builder
	out.WriteString(w.wrap(fieldStyle.Render("wrote")+w.path) + "\n")
	out.WriteString(w.wrap(fieldStyle.Render("daemon")+w.registration()) + "\n\n")

	if w.project == "" {
		out.WriteString(mutedStyle.Render(
			"register it with `feat project add "+w.flow.ID()+"` when you are ready") + "\n")
	}
	// What the checks found, said again at the end: the findings were read on
	// the way past, and this is the screen a user leaves on.
	switch {
	case !w.check.ran:
		out.WriteString(mutedStyle.Render(
			"run `feat doctor` to check this project against the host") + "\n")
	case w.check.report.Failed():
		out.WriteString(attentionStyle.Render(
			"the checks found errors; fix them and press D to check again") + "\n")
	default:
		out.WriteString(mutedStyle.Render("the checks found nothing that stops it working") + "\n")
	}
	return out.String()
}

// wrap folds a line that has to be read in full into the dialog's width.
//
// A path and the reason an answer was refused are the two things on this screen
// that a user acts on, and the box composites by cell: without this they end in
// an ellipsis, and a path that is cut is a path nobody can check.
//
// The padding lipgloss adds is taken back off. dialogBox shrinks its box to the
// widest line it is handed, and a line padded out to the width it wrapped to
// reports itself as exactly as wide as the dialog was allowed — so the wizard
// took three quarters of the terminal from the review step onwards, to show a
// file whose lines are half that. What decides the width now is the widest thing
// actually on the screen, which is what lets this dialog have as much room as it
// needs and no more.
func (w wizardModel) wrap(text string) string {
	wrapped := lipgloss.NewStyle().Width(max(20, w.width)).Render(text)

	lines := strings.Split(wrapped, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	return strings.Join(lines, "\n")
}

func (w wizardModel) hints() string {
	switch w.step {
	case wizardReviewing:
		return keyHints(
			keyHint("enter", "write it"),
			keyHint("↑↓ pgup/pgdn", "read"),
			keyHint("esc", "back"),
			keyHint("ctrl+c", "cancel"),
		)
	case wizardChecking:
		return keyHints(
			keyHint("↑↓ pgup/pgdn", "read"),
			keyHint("r", "check again"),
			keyHint("enter", "continue"),
		)
	case wizardRegistering:
		return keyHints(keyHint("↑↓", "select"), keyHint("enter", "answer"), keyHint("esc", "skip"))
	case wizardDone:
		return keyHints(keyHint("enter", "close"))
	}

	if w.question.Kind == wizard.KindText {
		hints := []string{keyHint("enter", "continue")}
		if len(w.question.Candidates) > 0 {
			// Said only where there is something to complete, because a key that
			// does nothing on most questions is worse than one nobody was told
			// about: the hint is how a user learns that this question has more
			// than the one value in it.
			hints = append(hints, keyHint("tab", "complete"))
		}
		return keyHints(append(hints, keyHint("esc", "back"), keyHint("ctrl+c", "cancel"))...)
	}
	return keyHints(
		keyHint("↑↓", "select"),
		keyHint("enter", "continue"),
		keyHint("esc", "back"),
		keyHint("ctrl+c", "cancel"),
	)
}
