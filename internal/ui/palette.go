package ui

import "github.com/ma8el/feat/internal/ui/ask"

// The dashboard's colours, read back from the package that draws the wizard's
// questions.
//
// They are defined in internal/ui/ask, and the reason is the direction of the
// dependency rather than anything about the colours. `feat project init` draws
// the same question widget the dashboard's dialog draws, before there is a
// daemon and possibly before Feat has run on this machine at all, and a command
// that reached the package that draws the dashboard in order to draw a prompt
// would have the arrow the wrong way round. The widget needs four of the styles
// beside them, the styles need these values, and a palette is chosen as a set —
// so the set moved rather than being split (ADR-084).
//
// Nothing else about them changed: what they are, why each exists, and how they
// were chosen is written where they are defined.
var (
	colourAccent    = ask.ColourAccent
	colourOnAccent  = ask.ColourOnAccent
	colourAttention = ask.ColourAttention
	colourFailure   = ask.ColourFailure
	colourText      = ask.ColourText
	colourMuted     = ask.ColourMuted
	colourRule      = ask.ColourRule
)
