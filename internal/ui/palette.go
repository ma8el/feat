package ui

import "github.com/ma8el/feat/internal/ui/ask"

// The dashboard's colours, read back from internal/ui/ask: `feat project init`
// draws that package's question widget before there is a daemon, and must not
// depend on the dashboard to draw a prompt (ADR-084).
var (
	colourAccent    = ask.ColourAccent
	colourOnAccent  = ask.ColourOnAccent
	colourAttention = ask.ColourAttention
	colourFailure   = ask.ColourFailure
	colourText      = ask.ColourText
	colourMuted     = ask.ColourMuted
	colourRule      = ask.ColourRule
)
