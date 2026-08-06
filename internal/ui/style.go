package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// absent is what a field Feat cannot fill yet renders as.
//
// It is never a zero, a blank, or a plausible-looking value. A dashboard that
// prints "0 files changed" where it has not looked is making a claim, which is
// the rule ADR-028 established for `feat doctor` and ADR-031 carried into the
// task list: a value that was never measured is never displayed as one.
const absent = "—"

var (
	headingStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#1a1a1a", Dark: "#fafafa"})

	mutedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#6c6c6c", Dark: "#8a8a8a"})

	selectedStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#0b5cad", Dark: "#7cc4ff"})

	attentionStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#a35200", Dark: "#ffb454"})

	failureStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#a3161b", Dark: "#ff8189"})

	fieldStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#6c6c6c", Dark: "#8a8a8a"}).
			Width(14)
)

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
	if missing := width - lipgloss.Width(cell); missing > 0 {
		return cell + strings.Repeat(" ", missing)
	}
	return cell
}

// truncate shortens a cell that does not fit, marking that it was shortened.
func truncate(cell string, width int) string {
	if width <= 0 || lipgloss.Width(cell) <= width {
		return cell
	}
	if width == 1 {
		return "…"
	}

	runes := []rune(cell)
	for len(runes) > 0 && lipgloss.Width(string(runes))+1 > width {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "…"
}

// keyHint renders one key and what it does, for a footer.
func keyHint(key, action string) string {
	return selectedStyle.Render(key) + mutedStyle.Render(" "+action)
}

// keyHints joins footer hints.
func keyHints(hints ...string) string {
	return strings.Join(hints, mutedStyle.Render("   "))
}
