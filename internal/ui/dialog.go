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

	content := headingStyle.Render(truncate(title, inner)) + "\n\n" + clampBlock(body, inner)
	if fits, _ := blockSize(content); fits < inner {
		inner = fits
	}
	// The border takes a line above and below, so the content gets what is left
	// of the body region. A dialog taller than that would draw over the footer,
	// which is the one part of the frame that never moves.
	return dialogStyle.Width(inner + 2).Render(clampHeight(content, tallest-2, inner))
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

var dialogStyle = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(lipgloss.AdaptiveColor{Light: "#0b5cad", Dark: "#7cc4ff"}).
	Padding(0, 1)

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

// keyMap renders every key the dashboard has, which is what the footer stopped
// trying to list.
//
// The footer used to carry twelve hints at once, which is a list nobody reads
// and the first thing to be truncated on a narrow terminal. It now carries what
// is reachable from where the user is, and this is where the rest lives.
func keyMap() string {
	sections := []struct {
		heading string
		keys    [][2]string
	}{
		{"tasks", [][2]string{
			{"shift+↑ ↓", "select a task, from any view"},
			{"ctrl+p / n", "the same, where a terminal eats shifted arrows"},
			{"↑ ↓", "move within the view that has the keyboard"},
			{"tab", "next view"},
			{"shift+tab", "previous view"},
			{"enter / v", "the task panel: what it is, and what to decide"},
		}},
		{"the selected task", [][2]string{
			{"a", "attach to the agent's terminal"},
			{"s", "open the task's shell"},
			{"R", "application runtime"},
			{"z", "resume a recorded session"},
			{"x", "cancel a draft"},
		}},
		{"the task panel", [][2]string{
			{"↑ ↓", "select a repository"},
			{"d / e", "diff and editor, in that repository"},
			{"A / C / P", "approve, request changes, leave pending"},
			{"V", "run the project's checks"},
			{"pgup/pgdn", "scroll the panel"},
			{"r", "compare again"},
		}},
		{"everything else", [][2]string{
			{"!", "what the last recovery pass found"},
			{"n", "prepare a new task"},
			{"C", "clean up a task's resources"},
			{"r", "look again"},
			{"?", "this list"},
			{"q", "quit"},
		}},
	}

	var out strings.Builder
	for i, section := range sections {
		if i > 0 {
			out.WriteString("\n")
		}
		out.WriteString(mutedStyle.Render(section.heading) + "\n")
		for _, key := range section.keys {
			out.WriteString("  " + pad(selectedStyle.Render(key[0]), 12) + mutedStyle.Render(key[1]) + "\n")
		}
	}
	return strings.TrimRight(out.String(), "\n")
}

// keyMap is a method too, so that the view layer reaches it the same way it
// reaches every other body.
func (m Model) keyMap() string { return keyMap() }
