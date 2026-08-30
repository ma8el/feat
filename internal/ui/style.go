package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/ma8el/feat/internal/ui/ask"
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

	// The four the question widget draws with are defined beside it, in
	// internal/ui/ask, and read back here for the rest of the dashboard. Two
	// callers of one widget, each free to style it, is the drift that seam
	// exists to make impossible (ADR-084).
	mutedStyle = ask.MutedStyle

	selectedStyle = ask.SelectedStyle

	attentionStyle = ask.AttentionStyle

	titleStyle = ask.TitleStyle

	failureStyle = lipgloss.NewStyle().Bold(true).Foreground(colourFailure)

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

// centreLine indents a line to the middle of a width.
//
// It measures what the terminal will draw rather than counting bytes, as pad
// does and for the same reason: the lines this is given carry styling, and one
// of them carries the indicator's own glyph.
//
// A line that does not fit is returned where it is. The caller cuts it to the
// region afterwards, and an indent added to something already too wide would
// only move the cut further into the sentence.
func centreLine(line string, width int) string {
	indent := (width - ansi.StringWidth(line)) / 2
	if indent <= 0 {
		return line
	}
	return strings.Repeat(" ", indent) + line
}

// tabStop is where a terminal puts a tab: the next multiple of eight.
const tabStop = 8

// plainText is text Feat did not write, made into something Feat can measure.
//
// The dashboard is drawn by cell arithmetic — wrapped to the region, cut to it,
// padded out to the border — and every one of those measurements asks how wide a
// string is. A tab answers zero and the terminal draws it as a jump to the next
// multiple of eight, so a captured `go test` line measured forty-eight cells and
// was drawn as sixty-two: across the border, through the region beside it, and
// down the rest of the frame. A carriage return is worse, because it puts what
// follows back at the terminal's left edge over whatever is already there.
//
// So the tabs are expanded here, where the column they land in is still known,
// and the rest of the C0 controls are dropped: they move the cursor, and nothing
// that moves the cursor may reach a screen laid out by counting cells. Escape
// sequences pass through untouched — the styling is Feat's own, and a rendered
// pane is not drawn through here at all.
func plainText(s string) string {
	if !strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 && r != '\n' || r == 0x7f }) {
		return s
	}

	lines := strings.Split(s, "\n")
	for i, line := range lines {
		var out strings.Builder
		for at := range len(line) {
			switch c := line[at]; {
			case c == '\t':
				// Measured rather than counted, so that a tab after a styled run
				// lands where the terminal will put it.
				width := ansi.StringWidth(out.String())
				out.WriteString(strings.Repeat(" ", tabStop-width%tabStop))
			case c == 0x1b:
				// The introducer of an escape sequence; the bytes after it are all
				// printable and are copied by the branch below.
				out.WriteByte(c)
			case c < 0x20 || c == 0x7f:
			default:
				out.WriteByte(c)
			}
		}
		lines[i] = out.String()
	}
	return strings.Join(lines, "\n")
}

// plainLine is plainText for a value drawn on one line.
//
// A line break in a task's title is the same defect as a tab in a check's
// output — the rail counts the lines it draws — and a title is a user's sentence
// about their own work, which may have come from anywhere.
func plainLine(s string) string {
	return strings.ReplaceAll(plainText(s), "\n", " ")
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

// keyHint renders one key and what it does, for a footer. It is the question
// widget's, because the footer under a question is drawn by both askers.
func keyHint(key, action string) string { return ask.KeyHint(key, action) }

// keyHints joins footer hints.
func keyHints(hints ...string) string { return ask.KeyHints(hints...) }
