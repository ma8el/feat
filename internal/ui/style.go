package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/ui/ask"
)

// absent is what a field Feat cannot fill yet renders as. It is never a zero, a
// blank, or a plausible-looking value: a dashboard that prints "0 files
// changed" where it has not looked is making a claim (ADR-028, carried into the
// task list by ADR-031).
const absent = "—"

var (
	headingStyle = lipgloss.NewStyle().Bold(true).Foreground(colourText)

	// The four the question widget draws with are defined beside it, in
	// internal/ui/ask, and read back here for the rest of the dashboard. Two
	// callers each free to style one widget would drift apart (ADR-084).
	mutedStyle = ask.MutedStyle

	selectedStyle = ask.SelectedStyle

	attentionStyle = ask.AttentionStyle

	titleStyle = ask.TitleStyle

	failureStyle = lipgloss.NewStyle().Bold(true).Foreground(colourFailure)

	// focusedEntryStyle marks the task whose terminal is taking the keyboard. It
	// is a background rather than a colour, because it has to be legible at a
	// glance beside four other entries.
	focusedEntryStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(colourOnAccent).
				Background(colourAccent)

	// activeTabStyle is the tab the main region is drawing. It carries the accent
	// as a background for the reason the focused entry does: a header whose
	// selected item differs only in shade has to be compared rather than seen.
	activeTabStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colourOnAccent).
			Background(colourAccent).
			Padding(0, 1)

	// tabStyle is a tab the main region is not drawing. It keeps the active tab's
	// padding so that moving between tabs does not move the bar.
	tabStyle = lipgloss.NewStyle().Foreground(colourMuted).Padding(0, 1)

	// barStyle is the used part of a resource bar and the number on it. It takes
	// the attention colour, and shape is what separates a measure from a summons:
	// a badge is a glyph beside a task and this is a block filling a column. Bold
	// is left to the attention styles, so an overloaded machine's number can
	// still stand out.
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

// centreLine indents a line to the middle of a width. It measures what the
// terminal will draw rather than counting bytes, because the lines it is given
// carry styling. A line that does not fit is returned where it is: the caller
// cuts it to the region, and an indent would move that cut further into the
// sentence.
func centreLine(line string, width int) string {
	indent := (width - ansi.StringWidth(line)) / 2
	if indent <= 0 {
		return line
	}
	return strings.Repeat(" ", indent) + line
}

// tabStop is where a terminal puts a tab: the next multiple of eight.
const tabStop = 8

// plainText is text Feat did not write, made into something Feat can measure. A
// tab measures zero cells and the terminal draws it as a jump to the next
// multiple of eight, so a captured `go test` line measured forty-eight cells
// and was drawn as sixty-two, across the border and into the region beside it.
// A carriage return is worse, because it puts what follows back at the
// terminal's left edge.
//
// The tabs are expanded here, where the column they land in is still known, and
// the rest of the C0 controls are dropped: nothing that moves the cursor may
// reach a screen laid out by counting cells. Escape sequences pass through
// untouched, because the styling is Feat's own.
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

// plainLine is plainText for a value drawn on one line. The rail counts the
// lines it draws, so a line break in a task's title — a user's own sentence,
// which may have come from anywhere — is the same defect as a tab in a check's
// output.
func plainLine(s string) string {
	return strings.ReplaceAll(plainText(s), "\n", " ")
}

// truncate shortens a cell that does not fit, marking that it was shortened. It
// cuts by cell rather than by rune, through the escape-aware primitive the
// overlay and the cards use: a styled cell cut by rune loses half an escape
// sequence, and the terminal keeps the colour the remaining half set.
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

// readHint is the keys that move a document under its window. One wording,
// because five overlays answer the same four keys and each named only the
// arrows and the page keys. The letters lead, as they do in the dashboard's key
// map, where the same pair is written `j k h l  ↓↑←→`.
func readHint() string { return keyHint("j k ↑↓ pgup/pgdn", "read") }

// daemonNote renders what came back from a request the way the screen it lands
// on should read it. A refusal is not a failure: "Checks can only run for a
// task whose agent has asked for review" says what can be done from here, so it
// is drawn as the amber note both screens use for something that needs the
// user. A genuine failure keeps the failure colour.
//
// The wire prefix goes, because it classifies the response for a caller rather
// than telling a person anything, and the task's identifier is shortened to the
// key the header above already names it by. The identifier is replaced by value
// rather than by pattern, so nothing that merely looks like one is rewritten.
//
// It returns one unwrapped note. Both tabs that draw one re-flow their region
// as a whole, and a note folded here as well would break twice.
func daemonNote(err error, task api.Task) string {
	message := daemonMessage(err, task)
	if !api.IsInvalid(err) {
		return failureStyle.Render(message)
	}
	return attentionStyle.Render("note") + " " + message
}

// daemonMessage is what the daemon said with what it said for a caller taken
// off, for a screen that has already decided how to draw it. It is separate
// from daemonNote because not every screen wants that colouring: a cleanup that
// stopped partway through a removal is a failure whatever the wire classified
// it as, and the daemon wraps it as a refusal only so its message survives the
// response.
//
// A caller with no task to name asks for the prefix alone. The identifier is
// replaced for a screen whose messages are about the task; where they are about
// the task's resources it is part of a worktree path, and replacing it would
// name a path that is not on disk.
func daemonMessage(err error, task api.Task) string {
	message := err.Error()
	if task.ID != "" && task.Key != "" {
		message = strings.ReplaceAll(message, task.ID, task.Key)
	}
	return strings.TrimPrefix(message, api.ErrInvalid.Error()+": ")
}

// wrapNote folds a note to the region it is drawn in, where the caller knows
// the width. Its callers are the cleanup dialog's, which draws into a box of
// its own width rather than through a body re-flowed as a whole, so a long
// sentence would otherwise be cut at the box's edge — and the end of it is the
// path.
func wrapNote(message string, width int) string {
	if width <= 0 {
		return message
	}
	return ansi.Wrap(message, width, "")
}
