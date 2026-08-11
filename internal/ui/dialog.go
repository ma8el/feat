package ui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// dialogBox draws a titled border around an overlay's content.
//
// The border is what separates the dialog from the dashboard behind it. Without
// one the two readings run together, which is the cost an overlay pays for
// keeping the background visible and the reason a modal screen did not need one.
//
// The box shrinks to what it holds, up to the limit its caller allows. A dialog
// sized to the terminal rather than to its content covers the task list with
// blank cells, which is the thing an overlay was chosen to avoid.
func dialogBox(title, body string, limit, tallest int) string {
	if limit < 24 {
		limit = 24
	}
	inner := limit - 4

	heading := titleStyle.Render(truncate(title, inner))
	content := clampBlock(body, inner)
	if fits, _ := blockSize(content); fits < inner {
		// The title is part of what has to fit, so a dialog is never narrower
		// than its own name.
		inner = max(fits, blockWidth(heading))
	}
	// The heading is ruled off from the content, as a card's is: a dialog is the
	// same kind of thing drawn over the dashboard rather than beside it
	// (ADR-051).
	content = heading + "\n" + ruleStyle.Render(strings.Repeat(cardHorizontal, inner)) +
		"\n" + content

	// The border takes a line above and below, so the content gets what is left
	// of the body region. A dialog taller than that would draw over the footer,
	// which is the one part of the frame that never moves.
	return dialogStyle.Width(inner + 2).Render(clampHeight(content, tallest-2, inner))
}

// blockWidth is the widest line of a block, in cells.
func blockWidth(block string) int {
	width, _ := blockSize(block)
	return width
}

// clampHeight drops the lines of a block that do not fit, saying that it did.
//
// The note it leaves is truncated to the box's width, because a note that
// wrapped would take a second line and put back one of the lines it was
// reporting as missing.
func clampHeight(block string, height, width int) string {
	if height < 3 {
		height = 3
	}
	lines := strings.Split(block, "\n")
	if len(lines) <= height {
		return block
	}
	kept := append([]string(nil), lines[:height-1]...)
	note := "… " + strconv.Itoa(len(lines)-height+1) + " more lines than fit here"
	return strings.Join(append(kept, mutedStyle.Render(truncate(note, width))), "\n")
}

// dialogStyle is the box an overlay is drawn in.
//
// Its border is the accent rather than the cards' quiet rule, and that is the
// one place the two differ: an overlay always has the keyboard, and the frame
// already says so about a region by taking the same colour (ADR-051).
var dialogStyle = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(colourAccent).
	Padding(0, 1)

// clampRows drops the lines of a block that a region has no room for.
func clampRows(block string, height int) string {
	if height < 1 {
		height = 1
	}
	lines := strings.Split(block, "\n")
	if len(lines) <= height {
		return block
	}
	return strings.Join(lines[:height], "\n")
}

// clampBlock truncates every line of a block to a width.
//
// A dialog is composited over the dashboard by cell, so a line wider than the
// box would be drawn across the task list rather than wrapped inside the border.
func clampBlock(block string, width int) string {
	lines := strings.Split(block, "\n")
	for i, line := range lines {
		lines[i] = truncate(line, width)
	}
	return strings.Join(lines, "\n")
}

// keyColumn is the width of the key column of the map, in cells.
//
// It holds the widest binding spelled out — `tab / shift+tab`, fifteen cells —
// and one more for the gutter, because a key that is truncated to an ellipsis is
// a key nobody can press and one that touches its own description is one nobody
// can read.
const keyColumn = 16

// keyDescription is what a description may measure for two columns to fit the
// dialog beside the rail, in cells.
//
// It is short, and it is why every line of the map below reads as a label rather
// than a sentence. Two columns inside three quarters of a hundred-and-twenty-cell
// terminal is what there is to spend, and the alternative — a wider dialog — is
// one that covers the task keys the overlay was chosen to leave visible.
const keyDescription = 23

// keyGap separates the two columns of the key map.
const keyGap = 4

// keyMap renders every key the dashboard has, which is what the footer stopped
// trying to list.
//
// The footer used to carry twelve hints at once, which is a list nobody reads
// and the first thing to be truncated on a narrow terminal. It now carries what
// is reachable from where the user is, and this is where the rest lives.
//
// It is laid out in two columns wherever they fit, because in one column it does
// not: six sections of keys is forty-one lines, and the dialog on a normal
// terminal has room for twenty-seven. What overflowed was cut from the bottom,
// which is where the sections a reader has not memorised are.
func keyMap(width int) string {
	sections := []struct {
		heading string
		keys    [][2]string
	}{
		// Shifted keys move the frame and plain keys move within the view. It is
		// one rule, and it is the first thing this list says, because a user who
		// has it needs the rest of the sections only once (ADR-046).
		{"moving · shift moves the frame", [][2]string{
			{"J / K", "next and previous task"},
			{"L / H", "next and previous view"},
			{"shift+↓ ↑ → ←", "the same, in arrows"},
			{"ctrl+n / ctrl+p", "where shift is eaten"},
			{"tab / shift+tab", "view, the same way"},
			{"space", "fold this project away"},
		}},
		{"the view · plain keys stay in it", [][2]string{
			{"j k h l  ↓↑←→", "move within the view"},
			{"enter / v", "open the task panel"},
			{"i", "type into the pane"},
			{"ctrl+q", "take the keyboard back"},
			{"w", "agent's pane or shell"},
		}},
		{"the selected task", [][2]string{
			{"a", "attach to the terminal"},
			{"s", "open the task's shell"},
			{"R", "application runtime"},
			{"z", "resume the session"},
			{"x", "cancel a draft"},
		}},
		{"the task panel", [][2]string{
			{"j / k", "select a repository"},
			{"d / e", "diff and editor, there"},
			{"A / C / P", "approve/change/pending"},
			{"V", "run configured checks"},
			{"pgup / pgdn", "scroll the panel"},
			{"r", "compare again"},
		}},
		{"the runtime view", [][2]string{
			{"c / u / t", "create, start, stop"},
			{"o", "follow the Compose logs"},
			{"d", "destroy, after a yes"},
		}},
		{"everything else", [][2]string{
			{"!", "what recovery found"},
			{"n", "prepare a new task"},
			{"C", "clean up a task"},
			{"r", "look again"},
			{"?", "this list"},
			{"q", "quit"},
		}},
	}

	blocks := make([]string, 0, len(sections))
	lines := 0
	for _, section := range sections {
		var out strings.Builder
		out.WriteString(mutedStyle.Render(section.heading))
		for _, key := range section.keys {
			out.WriteString("\n  " + pad(selectedStyle.Render(key[0]), keyColumn) +
				mutedStyle.Render(key[1]))
		}
		blocks = append(blocks, out.String())
		lines += len(section.keys) + 1
	}

	if columns, ok := twoColumnKeys(blocks, lines, width); ok {
		return columns
	}
	return strings.Join(blocks, "\n\n")
}

// twoColumnKeys lays the sections out side by side, and reports whether they fit.
//
// The split is by rendered height rather than by count, so that the two columns
// end at about the same line however the sections are sized. A width that cannot
// hold both is not squeezed: the caller stacks them instead, which is what the
// narrow fallback gets.
func twoColumnKeys(blocks []string, lines, width int) (string, bool) {
	if len(blocks) < 2 {
		return "", false
	}

	split, height := 0, 0
	for i, block := range blocks[:len(blocks)-1] {
		height += len(strings.Split(block, "\n")) + 1
		split = i + 1
		if height*2 >= lines {
			break
		}
	}

	left := strings.Join(blocks[:split], "\n\n")
	right := strings.Join(blocks[split:], "\n\n")
	leftWidth, _ := blockSize(left)
	rightWidth, _ := blockSize(right)
	if leftWidth+keyGap+rightWidth > width {
		return "", false
	}

	// Each line of the left column is padded to the gutter, rather than the block
	// being placed in a fixed-width box: a box wraps a line that overruns it, and
	// a wrapped key line puts a description under a key column it does not belong
	// to.
	padded := strings.Split(left, "\n")
	for i, line := range padded {
		padded[i] = pad(line, leftWidth+keyGap)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, strings.Join(padded, "\n"), right), true
}

// keyMap is a method too, so that the view layer reaches it the same way it
// reaches every other body.
func (m Model) keyMap(width int) string { return keyMap(width) }
