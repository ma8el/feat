package ask

import "github.com/charmbracelet/lipgloss"

// The dashboard's colours, in one place so that every view is coloured from the
// same six decisions rather than from whatever hex a screen was written with.
//
// They live here, one directory below the dashboard, because the question
// widget is drawn by `feat project init` as well — before there is a daemon,
// and possibly before Feat has run on the machine at all — and a command that
// imported the package that draws the dashboard in order to draw a prompt would
// be the wrong dependency in the wrong direction. What the widget needs are the
// four styles beside this file; what those need are these colours; and a
// palette is chosen as a set, so it moved as a set rather than being split
// three and three. `internal/ui` reads every one of them back (ADR-084).
//
// They are chosen as a set, and the set is held together by chroma rather than
// by hue: the selection colour and the attention colour carry the same amount of
// colour, and the failure colour carries more than either. That is the whole
// ordering, and it is what stops the pair a user reads most from being read as
// loud and quiet rather than as two different things (ADR-051, ADR-053).
//
// Lightness is what separates them within a weight, not chroma. The accent is
// about a tenth darker than the attention colour in both themes, so the two are
// told apart by hue and by lightness together — a pair matched on every axis but
// hue is a pair with only one thing left to lose, which is what a user's colour
// vision may be the thing that loses it.
//
// Every colour is adaptive, because a terminal's background is the user's and
// not Feat's. The light values are dark enough to be read on white and the dark
// values are light enough to be read on black; neither set is a tint of the
// other, because a colour that only inverts loses its contrast in one of the two
// places it has to work. The dark orange is the product's colour and cannot be
// one of the light values: it reads at 2:1 on white.
var (
	// ColourAccent marks what the user has chosen — the selected task, the open
	// tab, the region holding the keyboard — and nothing else. A colour that also
	// meant "important" would stop meaning "here".
	//
	// Blue, and nearly opposite the attention colour, which is the pairing that
	// survives colour-vision deficiency best: it differs in lightness as well as
	// hue, so it does not depend on the one channel a user may not have. Cooler
	// hues were measured and rejected — a teal accent gives up two fifths of that
	// separation, and cannot reach the orange's weight on white at all (ADR-053).
	ColourAccent = lipgloss.AdaptiveColor{Light: "#1f4e88", Dark: "#53a0ff"}

	// ColourOnAccent is text drawn on the accent, for the one entry that carries
	// it as a background.
	ColourOnAccent = lipgloss.AdaptiveColor{Light: "#ffffff", Dark: "#11151f"}

	// ColourAttention is a task that may be waiting for the user, and the
	// resource bars. It is the colour Feat is recognised by, and it is the one a
	// working dashboard shows most, which is the same thing said twice.
	//
	// The light value is not the dark one darkened. Orange runs out of sRGB early
	// on white: this is the most colour the hue has at a lightness that still
	// reads there, so the light theme's version is an ochre by arithmetic rather
	// than by choice.
	ColourAttention = lipgloss.AdaptiveColor{Light: "#8a5a00", Dark: "#f5a623"}

	// ColourFailure is something that went wrong. It is the only warm colour that
	// outranks attention, so it is kept for what a user must act on.
	//
	// It carries more colour than attention rather than merely a different hue,
	// because orange and red are neighbours and a user without the red channel has
	// nothing else to tell them apart with. The dark value was lifted when the
	// orange was: at the old weight the two were the palette's closest pair by a
	// wide margin (ADR-053).
	ColourFailure = lipgloss.AdaptiveColor{Light: "#a8202a", Dark: "#ff6287"}

	// ColourText is a heading or a value: the strongest neutral the background
	// allows, so that what a user is reading is the highest contrast on screen.
	ColourText = lipgloss.AdaptiveColor{Light: "#1a1d24", Dark: "#e9edf7"}

	// ColourMuted is a label, a hint, or a figure that supports one. It is
	// legible rather than faint — a value nobody can read is a value nobody
	// measured.
	ColourMuted = lipgloss.AdaptiveColor{Light: "#6b7280", Dark: "#8b93a8"}

	// ColourRule draws every line the layout is made of: the cards' borders, the
	// rules under their headers, and the one above the footer. It is quiet on
	// purpose. A frame is what holds the content apart, and a frame a user
	// notices is one competing with what it holds.
	ColourRule = lipgloss.AdaptiveColor{Light: "#c9ced9", Dark: "#4a5270"}
)
