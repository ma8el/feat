package ui

import "github.com/charmbracelet/lipgloss"

// The dashboard's colours, in one place so that every view is coloured from the
// same six decisions rather than from whatever hex a screen was written with.
//
// They are chosen as a set. The pair a user reads most — the selection colour
// and the attention colour — used to be a pale sky blue and a saturated orange,
// which are two different kinds of colour rather than two members of one family:
// one is washed out and the other shouts, so a rail holding both looked like two
// programs sharing a window. What replaces them is a blue and an amber of the
// same weight, so the eye reads the difference as meaning rather than as a
// change of loudness (ADR-051).
//
// Every colour is adaptive, because a terminal's background is the user's and
// not Feat's. The light values are dark enough to be read on white and the dark
// values are light enough to be read on black; neither set is a tint of the
// other, because a colour that only inverts loses its contrast in one of the two
// places it has to work.
var (
	// colourAccent marks what the user has chosen — the selected task, the open
	// tab, the region holding the keyboard — and nothing else. A colour that also
	// meant "important" would stop meaning "here".
	colourAccent = lipgloss.AdaptiveColor{Light: "#2f6fdb", Dark: "#7aa2f7"}

	// colourOnAccent is text drawn on the accent, for the one entry that carries
	// it as a background.
	colourOnAccent = lipgloss.AdaptiveColor{Light: "#ffffff", Dark: "#11151f"}

	// colourAttention is a task that may be waiting for the user, and the
	// resource bars. Amber rather than orange: it sits at the accent's weight, so
	// the two can be beside each other without either one winning.
	colourAttention = lipgloss.AdaptiveColor{Light: "#8a5a00", Dark: "#e0af68"}

	// colourFailure is something that went wrong. It is the only warm colour that
	// outranks attention, so it is kept for what a user must act on.
	colourFailure = lipgloss.AdaptiveColor{Light: "#a8202a", Dark: "#f7768e"}

	// colourText is a heading or a value: the strongest neutral the background
	// allows, so that what a user is reading is the highest contrast on screen.
	colourText = lipgloss.AdaptiveColor{Light: "#1a1d24", Dark: "#e9edf7"}

	// colourMuted is a label, a hint, or a figure that supports one. It is
	// legible rather than faint — a value nobody can read is a value nobody
	// measured.
	colourMuted = lipgloss.AdaptiveColor{Light: "#6b7280", Dark: "#8b93a8"}

	// colourRule draws every line the layout is made of: the cards' borders, the
	// rules under their headers, and the one above the footer. It is quiet on
	// purpose. A frame is what holds the content apart, and a frame a user
	// notices is one competing with what it holds.
	colourRule = lipgloss.AdaptiveColor{Light: "#c9ced9", Dark: "#4a5270"}
)
