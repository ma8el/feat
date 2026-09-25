package ask

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// The styles the question widget draws with. They are not passed in: an
// appearance that is a parameter is one two callers can drift apart on, and
// this package exists so the dashboard's dialog and `feat project init` are one
// rendering used twice (ADR-084). `internal/ui` reads these four back, so a
// change here is a change everywhere the colour means the same thing.
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
	// the first line of the content under it.
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

// Wrap folds text into a width, measuring what the terminal will draw. It is
// here for the reason the styles are: two wrappers that folded differently
// would put a paragraph and the sentence under it on different measures inside
// one box, and `internal/ui` reads this back (ADR-084).
//
// The padding lipgloss adds is taken off again. A dialog shrinks its box to the
// widest line it is handed, and a line padded out to the width it wrapped to
// reports itself as exactly as wide as the dialog was allowed, so a wrapped
// paragraph would take three quarters of the terminal to say something half
// that wide.
//
// A width of nothing is returned as it came: a caller that has not been given a
// width leaves this alone, which is the rule SetWidth follows.
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
