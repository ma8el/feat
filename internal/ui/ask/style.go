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
