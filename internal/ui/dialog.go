package ui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// What a dialog spends on itself. They are named because a body that sizes its
// own content has to subtract them before it counts lines: the cleanup
// inventory scrolls, and the box and its callers read the arithmetic from one
// place.
const (
	// dialogChrome is the horizontal cost: a border and a gutter on each side.
	dialogChrome = 4
	// dialogBorderHeight is the border above and below.
	dialogBorderHeight = 2
	// dialogHeadingHeight is the title and the rule under it.
	dialogHeadingHeight = 2
	// dialogVerticalChrome is both together, which is how many lines fewer than
	// its caller's limit a dialog's body is drawn in.
	dialogVerticalChrome = dialogBorderHeight + dialogHeadingHeight
	// dialogSmallest is the narrowest a dialog is drawn, whatever it was allowed.
	dialogSmallest = 24
)

// dialogBox draws a titled border around an overlay's content. The border is
// what separates the dialog from the dashboard behind it, which is the cost an
// overlay pays for keeping the background visible. The box shrinks to what it
// holds, up to the limit its caller allows, because a dialog sized to the
// terminal covers the task list with blank cells.
func dialogBox(title, body string, limit, tallest int) string {
	if limit < dialogSmallest {
		limit = dialogSmallest
	}
	inner := limit - dialogChrome

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
	return dialogStyle.Width(inner + 2).Render(clampHeight(content, tallest-dialogBorderHeight, inner))
}

// blockWidth is the widest line of a block, in cells.
func blockWidth(block string) int {
	width, _ := blockSize(block)
	return width
}

// documentWidth is the width a scrolling body draws its lines in: the widest
// line of the whole document, up to what the dialog allows. dialogBox shrinks
// the box to the widest line it is handed, and a body that scrolls hands it a
// window, so the box would grow and shrink under somebody reading it.
//
// Padding every drawn line to this is the other half: measuring without padding
// leaves the window as wide as its own widest line, which is what varies. It
// lives here rather than beside any one body, because four bodies scroll.
func documentWidth(lines []string, limit int) int {
	widest := 0
	for _, line := range lines {
		if measured := ansi.StringWidth(line); measured > widest {
			widest = measured
		}
	}
	return min(max(widest, 1), limit)
}

// clampHeight drops the lines of a block that do not fit, saying that it did.
// The note it leaves is truncated to the box's width, because a wrapped note
// would take a second line and put back one of the lines it reports as missing.
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

// dialogStyle is the box an overlay is drawn in. Its border is the accent
// rather than the cards' quiet rule, because an overlay always has the keyboard
// and the frame says that about a region in the same colour (ADR-051).
var dialogStyle = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(colourAccent).
	Padding(0, 1)

// clampBlock truncates every line of a block to a width. A dialog is composited
// over the dashboard by cell, so a line wider than the box would be drawn
// across the task list rather than wrapped inside the border.
func clampBlock(block string, width int) string {
	lines := strings.Split(block, "\n")
	for i, line := range lines {
		lines[i] = truncate(line, width)
	}
	return strings.Join(lines, "\n")
}

// keyColumn is the width of the key column of the map, in cells. It holds the
// widest binding spelled out — `tab / shift+tab`, fifteen cells — and one more
// for the gutter: a truncated key cannot be pressed, and one touching its own
// description cannot be read.
const keyColumn = 16

// keyDescription is what a description may measure for two columns to fit the
// dialog beside the rail, in cells. It is short, which is why every line of the
// map reads as a label rather than a sentence: a wider dialog would cover the
// task keys the overlay was chosen to leave visible.
const keyDescription = 23

// keyGap separates the two columns of the key map.
const keyGap = 4

// keyMap renders every key the dashboard has, which is more than a footer can
// list. The footer carries what is reachable from where the user is, and this
// is where the rest lives.
//
// It is laid out in two columns wherever they fit, because in one column it
// does not: seven sections render as forty-five lines — thirty-nine of keys and
// headings, and the blank line between each pair — against the twenty-seven the
// dialog has on a normal terminal, and what overflows is cut from the bottom
// where the sections a reader has not memorised are. The split and the fit are
// both decided on that rendered height, separators included.
//
// A description longer than keyDescription breaks this silently: the two
// columns stop fitting the width, the map falls back to the one column that
// does not fit the height, and the last section is eaten
// (TestTheKeyMapStaysInsideItsColumns).
func keyMap(width int) string {
	sections := []struct {
		heading string
		keys    [][2]string
	}{
		// Everything that moves, in one section: the frame's shifted keys, the
		// letters that go straight to a view, and the plain keys that move inside
		// whichever one is open (ADR-046). It is first because the narrow fallback
		// draws one column and cuts from the bottom.
		//
		// The sections after it are the tab bar's own order, so the list is
		// arranged the way the screen it is read over is.
		{"moving", [][2]string{
			{"J / K / L / H", "next/prev task, view"},
			{"shift+↓ ↑ → ←", "the same, in arrows"},
			{"tab / shift+tab", "next/prev view"},
			{"space", "fold or open a project"},
			{"A / T", "terminal, task panel"},
			{"B / R", "brief, runtime panel"},
			{"j k h l  ↓↑←→", "move within the view"},
		}},
		{"the terminal tab", [][2]string{
			{"i", "type into the pane"},
			{"ctrl+q", "take the keyboard back"},
			{"w", "agent's pane or shell"},
		}},
		{"the task tab", [][2]string{
			{"j / k", "select a repository"},
			{"d / e", "diff and editor"},
			{"V", "run configured checks"},
			{"P", "publish changes"},
			{"pgup / pgdn", "scroll the panel"},
		}},
		{"the brief tab", [][2]string{
			{"pgup / pgdn", "scroll the panel"},
		}},
		{"the runtime tab", [][2]string{
			{"c / u", "create, start"},
			{"t / d", "stop, destroy"},
			{"o", "follow the Compose logs"},
			{"pgup / pgdn", "scroll the panel"},
		}},
		{"tasks and projects", [][2]string{
			{"n", "prepare a new task"},
			{"p", "configure a project"},
			{"a", "attach to the terminal"},
			{"s", "open the task's shell"},
			{"z / t", "resume and stop it"},
			{"C", "clean up a task"},
			{"x", "cancel a draft"},
		}},
		{"everything else", [][2]string{
			{"!", "what recovery found"},
			{"S", "start the daemon"},
			{"D", "run doctor"},
			{"r", "refresh"},
			{"?", "this list"},
			{"q", "quit"},
		}},
	}

	blocks := make([]string, 0, len(sections))
	for _, section := range sections {
		var out strings.Builder
		out.WriteString(mutedStyle.Render(section.heading))
		for _, key := range section.keys {
			out.WriteString("\n  " + pad(selectedStyle.Render(key[0]), keyColumn) +
				mutedStyle.Render(key[1]))
		}
		blocks = append(blocks, out.String())
	}

	if columns, ok := twoColumnKeys(blocks, width); ok {
		return columns
	}
	return strings.Join(blocks, "\n\n")
}

// twoColumnKeys lays the sections out side by side, and reports whether they
// fit. The split is by rendered height rather than by count, so the two columns
// end at about the same line however the sections are sized. A width that
// cannot hold both is not squeezed: the caller stacks them instead.
//
// Both sides of that comparison are rendered heights, blank separators
// included. Comparing a rendered height against a count of keys and headings
// leaves the separators out, which stops the walk a section early and gives the
// right column the difference.
func twoColumnKeys(blocks []string, width int) (string, bool) {
	if len(blocks) < 2 {
		return "", false
	}

	total := len(blocks) - 1
	for _, block := range blocks {
		total += len(strings.Split(block, "\n"))
	}

	split, height := 0, 0
	for i, block := range blocks[:len(blocks)-1] {
		height += len(strings.Split(block, "\n")) + 1
		split = i + 1
		if height*2 >= total {
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
