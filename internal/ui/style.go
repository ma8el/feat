package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// absent is what a field Feat cannot fill yet renders as.
//
// It is never a zero, a blank, or a plausible-looking value. A dashboard that
// prints "0 files changed" where it has not looked is making a claim, which is
// the rule ADR-028 established for `feat doctor` and ADR-031 carried into the
// task list: a value that was never measured is never displayed as one.
const absent = "—"

var (
	headingStyle = lipgloss.NewStyle().Bold(true).Foreground(colourText)

	mutedStyle = lipgloss.NewStyle().Foreground(colourMuted)

	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(colourAccent)

	attentionStyle = lipgloss.NewStyle().Bold(true).Foreground(colourAttention)

	failureStyle = lipgloss.NewStyle().Bold(true).Foreground(colourFailure)

	// titleStyle is a region's own heading — the rail's "tasks", the header of a
	// card. It is the accent rather than the text colour, because a header that
	// is only bold reads as the first line of the content under it, which is what
	// the rule beneath it and this colour together stop it from doing.
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(colourAccent)

	// focusedEntryStyle marks the task whose terminal is taking the keyboard.
	// A background rather than a colour, because it has to be legible at a
	// glance next to four other entries and it is answering "where are my
	// keystrokes going".
	focusedEntryStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(colourOnAccent).
				Background(colourAccent)

	// activeTabStyle is the tab the main region is drawing.
	//
	// It carries the accent as a background for the same reason the focused entry
	// does: the tab bar is the header of the region beside the rail, and a header
	// whose selected item differs only in shade is one a user has to compare
	// rather than see.
	activeTabStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colourOnAccent).
			Background(colourAccent).
			Padding(0, 1)

	// tabStyle is a tab the main region is not drawing. It keeps the active tab's
	// padding so that moving between tabs does not move the bar.
	tabStyle = lipgloss.NewStyle().Foreground(colourMuted).Padding(0, 1)

	// barStyle is the used part of a resource bar and the number on it. The
	// attention colour, because a bar is a measure rather than a summons and what
	// tells the two apart is the shape: a badge is a glyph beside a task and this
	// is a block that fills a column. Bold is left to the attention styles, so
	// that a bar never shouts and an overloaded machine's number still can.
	barStyle = lipgloss.NewStyle().Foreground(colourAttention)

	fieldStyle = lipgloss.NewStyle().Foreground(colourMuted).Width(fieldWidth)
)

// fieldWidth is the label column of the task panel, in cells.
const fieldWidth = 14

// column is one column of a rendered table.
type column struct {
	// title is the heading.
	title string
	// width is the fixed width, or zero for a column that takes what is left.
	width int
}

// renderTable lays out rows under headings, padding each cell to its column.
//
// Cells are padded by display width rather than by byte count, so a title
// holding a wide character does not push the columns after it out of line.
func renderTable(columns []column, rows [][]string) string {
	var out strings.Builder

	heading := make([]string, len(columns))
	for i, column := range columns {
		heading[i] = pad(column.title, column.width)
	}
	out.WriteString(mutedStyle.Render(strings.TrimRight(strings.Join(heading, "  "), " ")))

	for _, row := range rows {
		cells := make([]string, 0, len(row))
		for i, cell := range row {
			width := 0
			if i < len(columns) {
				width = columns[i].width
			}
			cells = append(cells, pad(cell, width))
		}
		out.WriteString("\n" + strings.TrimRight(strings.Join(cells, "  "), " "))
	}
	return out.String()
}

// pad widens a cell to a column, measuring what the terminal will show.
func pad(cell string, width int) string {
	if width <= 0 {
		return cell
	}
	cell = truncate(cell, width)
	if missing := width - ansi.StringWidth(cell); missing > 0 {
		return cell + strings.Repeat(" ", missing)
	}
	return cell
}

// truncate shortens a cell that does not fit, marking that it was shortened.
//
// It cuts by cell rather than by rune, through the same escape-aware primitive
// the overlay and the cards use. A styled cell — a tab drawn on a background, a
// line of a rendered pane — cut by rune loses half an escape sequence, and what
// the terminal does with the remaining half is set a colour and keep it.
func truncate(cell string, width int) string {
	if width <= 0 || ansi.StringWidth(cell) <= width {
		return cell
	}
	if width == 1 {
		return "…"
	}
	return ansi.Truncate(cell, width, "…")
}

// keyHint renders one key and what it does, for a footer.
func keyHint(key, action string) string {
	return selectedStyle.Render(key) + mutedStyle.Render(" "+action)
}

// keyHints joins footer hints.
func keyHints(hints ...string) string {
	return strings.Join(hints, mutedStyle.Render("   "))
}
