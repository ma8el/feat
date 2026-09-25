package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/charmbracelet/x/ansi"
)

// A card is one of the dashboard's two regions drawn as a panel: a rounded box
// with a header, a rule under the header, and a body (ADR-051). The rule is
// what the box is for: without it a heading and the first row under it read as
// two entries of one list.
const (
	// cardBorderWidth is the box rule, one cell on each side.
	cardBorderWidth = 1
	// cardPadding is the gutter between the rule and the content, one cell on
	// each side. Content against a border reads as a table rather than a panel.
	cardPadding = 1
	// cardChrome is what a card spends horizontally, on both sides together.
	cardChrome = 2 * (cardBorderWidth + cardPadding)
	// cardHeaderHeight is the header row and the rule beneath it.
	cardHeaderHeight = 2
	// cardVerticalChrome is the top and bottom rules.
	cardVerticalChrome = 2 * cardBorderWidth
)

// The rounded box, drawn here rather than by lipgloss. lipgloss re-flows what
// it borders, so a line wider than the box is wrapped rather than cut, and a
// tmux pane wrapped mid-escape-sequence runs its colours into the border. This
// draws the box a line at a time and cuts by cell, as overlay.go does.
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
// outer size. It always returns height lines of width cells, because the
// regions sit side by side and a card that grew with its content would push its
// neighbour's rows out of line. A body longer than the box is cut; every caller
// already bounds its own content.
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
		// The region holding the keyboard says so with its own edge, which is
		// the question the focused rail entry answers about a task.
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
// styling it left open. A line longer than the column draws over the border and
// across the region beside it, and a line whose background is still set carries
// that colour into the border. The content is a rendered tmux pane, and a
// capture holds the colour tmux emitted but not the clearing it does as it
// draws.
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

// cardHeader lays a title out with something right-aligned beside it. The aside
// is dropped rather than truncated when it does not fit: it summarises what is
// below it, so half of it says nothing the content does not.
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
// cells between them. The gap is what keeps the two panels apart on screen.
// Both cards are already exactly as wide as they claim, so joining is
// concatenation and a layout engine would re-measure measured content.
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
