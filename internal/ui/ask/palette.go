package ask

import "github.com/charmbracelet/lipgloss"

// The dashboard's colours, in one place so every view is coloured from the same
// six decisions rather than from whatever hex a screen was written with.
//
// They live one directory below the dashboard because `feat project init` draws
// the question widget before there is a daemon, and a command that imported the
// package that draws the dashboard in order to draw a prompt would be the wrong
// dependency; `internal/ui` reads every one of them back (ADR-084).
//
// The set is held together by chroma rather than by hue: the selection colour
// and the attention colour carry the same amount of colour, and the failure
// colour carries more than either. That ordering is what stops the pair a user
// reads most from being read as loud and quiet (ADR-051, ADR-053). Within a
// weight, lightness separates them — the accent is about a tenth darker than
// the attention colour in both themes — so a pair is never matched on every
// axis but hue, which is the axis a user's colour vision may lose.
//
// Every colour is adaptive, because a terminal's background is the user's and
// not Feat's. Neither set is a tint of the other: a colour that only inverts
// loses its contrast in one of the two places it has to work. The dark orange
// is the product's colour and cannot be one of the light values, because it
// reads at 2:1 on white.
var (
	// ColourAccent marks what the user has chosen — the selected task, the open
	// tab, the region holding the keyboard — and nothing else. A colour that also
	// meant "important" would stop meaning "here". It is blue, nearly opposite
	// the attention colour, and differs in lightness as well as hue, so it does
	// not lean on the one channel a user may not have. A teal accent gives up two
	// fifths of that separation and cannot reach the orange's weight on white
	// (ADR-053).
	ColourAccent = lipgloss.AdaptiveColor{Light: "#1f4e88", Dark: "#53a0ff"}

	// ColourOnAccent is text drawn on the accent, for the one entry that carries
	// it as a background.
	ColourOnAccent = lipgloss.AdaptiveColor{Light: "#ffffff", Dark: "#11151f"}

	// ColourAttention is a task that may be waiting for the user, and the
	// resource bars. It is the colour Feat is recognised by. The light value is
	// not the dark one darkened: orange runs out of sRGB early on white, so this
	// is the most colour the hue has at a lightness that still reads there and
	// the light theme's version is an ochre by arithmetic rather than by choice.
	ColourAttention = lipgloss.AdaptiveColor{Light: "#8a5a00", Dark: "#f5a623"}

	// ColourFailure is something that went wrong. It is the only warm colour that
	// outranks attention, so it is kept for what a user must act on. It carries
	// more colour than attention rather than merely a different hue, because
	// orange and red are neighbours and a user without the red channel has
	// nothing else to tell them apart with (ADR-053).
	ColourFailure = lipgloss.AdaptiveColor{Light: "#a8202a", Dark: "#ff6287"}

	// ColourText is a heading or a value: the strongest neutral the background
	// allows, so that what a user is reading is the highest contrast on screen.
	ColourText = lipgloss.AdaptiveColor{Light: "#1a1d24", Dark: "#e9edf7"}

	// ColourMuted is a label, a hint, or a figure that supports one. It is
	// legible rather than faint, because a figure nobody can read says nothing.
	ColourMuted = lipgloss.AdaptiveColor{Light: "#6b7280", Dark: "#8b93a8"}

	// ColourRule draws every line the layout is made of: the cards' borders, the
	// rules under their headers, and the one above the footer. It is quiet on
	// purpose, because a frame a user notices competes with what it holds.
	ColourRule = lipgloss.AdaptiveColor{Light: "#c9ced9", Dark: "#4a5270"}
)
