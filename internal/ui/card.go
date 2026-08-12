package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/charmbracelet/x/ansi"
)

// A card is one of the dashboard's two regions drawn as a panel: a rounded box
// with a header, a rule under the header, and a body (ADR-051).
//
// The rule is the point of it rather than decoration. The rail's heading and the
// tab bar were the first line of their own content, so a user scanning down the
// screen met "tasks" and the first task as though they were two entries of the
// same list. A header separated from what it heads is the thing that stops
// happening.
const (
	// cardBorderWidth is the box rule, one cell on each side.
	cardBorderWidth = 1
	// cardPadding is the gutter between the rule and the content, one cell on
	// each side. Content against a border reads as a table; a gutter is what
	// makes it read as a panel.
	cardPadding = 1
	// cardChrome is what a card spends horizontally, on both sides together.
	cardChrome = 2 * (cardBorderWidth + cardPadding)
	// cardHeaderHeight is the header row and the rule beneath it.
	cardHeaderHeight = 2
	// cardVerticalChrome is the top and bottom rules.
	cardVerticalChrome = 2 * cardBorderWidth
)

// The rounded box, drawn here rather than by lipgloss.
//
// lipgloss can draw a border around a block, and it re-flows what it is given to
// do it: a line wider than the box is wrapped rather than cut, and a rendered
// tmux pane wrapped mid-escape-sequence is a pane whose colours run into the
// border. This draws the box a line at a time, cuts by cell through the same
// escape-aware primitives the overlay uses, and ends every line's styling before
// the border it is about to write — which is the whole reason the splice in
// overlay.go exists too.
const (
	cardTopLeft     = "╭"
	cardTopRight    = "╮"
	cardBottomLeft  = "╰"
	cardBottomRight = "╯"
	cardHorizontal  = "─"
	cardVertical    = "│"
	cardHeaderLeft  = "├"
	cardHeaderRight = "┤"
)

var ruleStyle = lipgloss.NewStyle().Foreground(colourRule)

// card draws a header and a body inside a rounded box of exactly the given
// outer size.
//
// It always returns height lines of width cells, whatever it was given: the
// regions sit side by side and a card that grew with its content would push its
// neighbour's rows out of line. A body longer than the box is cut, which is what
// every caller already does for itself — the task panel scrolls, the rail pins
// its foot, and the pane is captured at the size this leaves it.
func card(header, body string, width, height int, focused bool) string {
	if width < cardChrome+1 {
		width = cardChrome + 1
	}
	if height < cardVerticalChrome+cardHeaderHeight+1 {
		height = cardVerticalChrome + cardHeaderHeight + 1
	}
	inner := width - cardChrome
	rule := ruleStyle
	if focused {
		// The region holding the keyboard says so with its own edge. It is the
		// same question the focused rail entry answers — where do my keystrokes
		// go — asked about the region rather than about the task.
		rule = rule.Foreground(colourAccent)
	}

	rows := make([]string, 0, height)
	rows = append(rows,
		rule.Render(cardTopLeft+strings.Repeat(cardHorizontal, width-2)+cardTopRight),
		cardRow(header, inner, rule),
		rule.Render(cardHeaderLeft+strings.Repeat(cardHorizontal, width-2)+cardHeaderRight),
	)

	lines := strings.Split(body, "\n")
	for range height - cardVerticalChrome - cardHeaderHeight {
		line := ""
		if len(lines) > 0 {
			line, lines = lines[0], lines[1:]
		}
		rows = append(rows, cardRow(line, inner, rule))
	}
	rows = append(rows,
		rule.Render(cardBottomLeft+strings.Repeat(cardHorizontal, width-2)+cardBottomRight))
	return strings.Join(rows, "\n")
}

// cardRow draws one line of content between the box's two verticals.
func cardRow(line string, inner int, rule lipgloss.Style) string {
	gutter := strings.Repeat(" ", cardPadding)
	return rule.Render(cardVertical) + gutter + fit(line, inner) + gutter +
		rule.Render(cardVertical)
}

// fit cuts a line to a width and pads it out to the same width, ending whatever
// styling it left open.
//
// Both halves matter and for the same reason. A line longer than the column
// would be drawn over the border and on across the region beside it; a line
// whose background colour is still set would carry that colour through the
// padding and into the border. Neither is hypothetical: the content here is a
// rendered tmux pane, which sets colours and relies on the terminal clearing to
// end of line, and a capture holds the colour but not the clearing.
func fit(line string, width int) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(line) > width {
		line = ansi.Truncate(line, width, "…")
	}
	if strings.Contains(line, "\x1b") {
		line += ansi.ResetStyle
	}
	if missing := width - ansi.StringWidth(line); missing > 0 {
		line += strings.Repeat(" ", missing)
	}
	return line
}

// cardHeader lays a title out with something right-aligned beside it.
//
// The right-hand side is dropped rather than wrapped or truncated when it does
// not fit. It is always a summary of what is below it — how many tasks are
// waiting, which task the region is about — so a cut-off version of it says
// nothing the content does not, and taking the title's cells to show half of it
// would be a worse trade than leaving it out.
func cardHeader(title, aside string, width int) string {
	if aside == "" {
		return title
	}
	gap := width - ansi.StringWidth(title) - ansi.StringWidth(aside)
	if gap < 2 {
		return title
	}
	return title + strings.Repeat(" ", gap) + aside
}

// joinRows sets two blocks of the same height side by side, with a gap of blank
// cells between them.
//
// The gap is what makes two panels read as two panels. lipgloss can join blocks
// horizontally, and this does not use it for the reason card does not use its
// border: both cards are already exactly as wide as they claim, so joining is
// concatenation, and going through a layout engine would re-measure content that
// has been measured.
func joinRows(left, right string, gap int) string {
	leftLines := strings.Split(left, "\n")
	rightLines := strings.Split(right, "\n")
	if gap < 0 {
		gap = 0
	}

	between := strings.Repeat(" ", gap)
	rows := make([]string, 0, max(len(leftLines), len(rightLines)))
	for i := range max(len(leftLines), len(rightLines)) {
		row := ""
		if i < len(leftLines) {
			row = leftLines[i]
		}
		row += between
		if i < len(rightLines) {
			row += rightLines[i]
		}
		rows = append(rows, row)
	}
	return strings.Join(rows, "\n")
}
