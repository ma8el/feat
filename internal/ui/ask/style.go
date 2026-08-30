package ask

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// The styles the question widget draws with.
//
// They are not passed in. A widget whose appearance is a parameter is a widget
// two callers may drift apart, and the whole reason this package exists is that
// the dashboard's dialog and `feat project init` should be one rendering used
// twice rather than two renderings of the same question (ADR-084). The rest of
// the dashboard reads these four back out of `internal/ui`, so a change here is
// a change everywhere the colour means the same thing.
var (
	// MutedStyle is a label, a hint, or a sentence Feat is offering rather than
	// asking.
	MutedStyle = lipgloss.NewStyle().Foreground(ColourMuted)

	// SelectedStyle marks what the cursor is on, and the keys in a footer.
	SelectedStyle = lipgloss.NewStyle().Bold(true).Foreground(ColourAccent)

	// AttentionStyle marks something Feat found out that the user has not been
	// told yet — the bullet in front of a note.
	AttentionStyle = lipgloss.NewStyle().Bold(true).Foreground(ColourAttention)

	// TitleStyle is a region's own heading — the rail's "tasks", the header of a
	// card, the name of the section a question belongs to. It is the accent
	// rather than the text colour, because a header that is only bold reads as
	// the first line of the content under it, which is what the rule beneath it
	// and this colour together stop it from doing.
	TitleStyle = lipgloss.NewStyle().Bold(true).Foreground(ColourAccent)
)

// KeyHint renders one key and what it does, for a footer.
func KeyHint(key, action string) string {
	return SelectedStyle.Render(key) + MutedStyle.Render(" "+action)
}

// KeyHints joins footer hints.
func KeyHints(hints ...string) string {
	return strings.Join(hints, MutedStyle.Render("   "))
}

// Wrap folds text into a width, measuring what the terminal will draw.
//
// It is here for the reason the styles are: the dashboard wraps the paths and
// the errors it draws around a question, the widget wraps the prose the flow
// writes for one, and two wrappers that fold differently would put a paragraph
// and the sentence under it on different measures inside one box (ADR-084). The
// dependency runs one way, so `internal/ui` reads this back as it reads the four
// style tokens back.
//
// The padding lipgloss adds is taken off again. A dialog shrinks its box to the
// widest line it is handed, and a line padded out to the width it wrapped to
// reports itself as exactly as wide as the dialog was allowed — so a wrapped
// paragraph would take three quarters of the terminal to say something half that
// wide, which is the defect wizardModel.wrap was written to avoid and which this
// inherits the fix for.
//
// A width of nothing is text nobody has been told how to fold, and it is
// returned as it came: a caller that has not been given a width leaves this
// alone, which is the same rule SetWidth follows.
func Wrap(text string, width int) string {
	if width < 1 {
		return text
	}

	lines := strings.Split(lipgloss.NewStyle().Width(width).Render(text), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	return strings.Join(lines, "\n")
}
