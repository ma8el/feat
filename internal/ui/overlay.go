package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// overlayOn draws top over background with its upper-left corner at (x, y),
// measured in terminal cells, and returns the composite. ADR-041 chose an
// overlay rather than a screen that replaces the dashboard, and lipgloss v1.1.0
// cannot layer one rendering over another. Cuts go through ansi.Cut, which
// neither splits an escape sequence nor counts one as visible width.
func overlayOn(background, top string, x, y int) string {
	if top == "" {
		return background
	}
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}

	backgroundLines := strings.Split(background, "\n")
	topLines := strings.Split(top, "\n")

	// A dialog placed below the last background line still draws, on blank
	// lines, so a caller whose arithmetic is wrong sees the result rather than
	// nothing.
	for len(backgroundLines) < y+len(topLines) {
		backgroundLines = append(backgroundLines, "")
	}

	for i, line := range topLines {
		backgroundLines[y+i] = spliceLine(backgroundLines[y+i], line, x)
	}
	return strings.Join(backgroundLines, "\n")
}

// spliceLine replaces the cells of background from x to the width of top.
func spliceLine(background, top string, x int) string {
	width := ansi.StringWidth(top)
	if width == 0 {
		return background
	}

	var out strings.Builder
	backgroundWidth := ansi.StringWidth(background)

	switch {
	case backgroundWidth >= x:
		out.WriteString(ansi.Cut(background, 0, x))
	default:
		out.WriteString(background)
		out.WriteString(strings.Repeat(" ", x-backgroundWidth))
	}

	// Each side ends whatever style it left open before the other begins. The
	// two conditions are separate: a styled overlay on a blank background wrote
	// no reset after itself, and the colour ran to the edge of the screen. tmux
	// clears to end of line as it draws, and a capture holds the colour but not
	// the clearing. A plain composite gets no reset and stays plain text.
	if strings.Contains(background, "\x1b") {
		out.WriteString(ansi.ResetStyle)
	}
	out.WriteString(top)
	if strings.Contains(top, "\x1b") {
		out.WriteString(ansi.ResetStyle)
	}

	if right := x + width; backgroundWidth > right {
		out.WriteString(ansi.Cut(background, right, backgroundWidth))
	}
	return out.String()
}

// centreOverlay places top in the middle of a region of the given size.
// Vertically it sits slightly above centre, which is where a dialog is read
// from.
func centreOverlay(background, top string, width, height int) string {
	topWidth, topHeight := blockSize(top)

	x := (width - topWidth) / 2
	y := (height - topHeight) / 3
	return overlayOn(background, top, x, y)
}

// terminate ends any style a line left open. A capture holds the colour tmux
// emitted but not the clearing tmux does as it draws, so a line whose
// background was set and never cleared runs that background to the edge of the
// terminal.
func terminate(block string) string {
	lines := strings.Split(block, "\n")
	for i, line := range lines {
		if strings.Contains(line, "\x1b") {
			lines[i] = line + ansi.ResetStyle
		}
	}
	return strings.Join(lines, "\n")
}

// blockSize measures a rendered block in cells.
func blockSize(block string) (width, height int) {
	lines := strings.Split(block, "\n")
	for _, line := range lines {
		if w := ansi.StringWidth(line); w > width {
			width = w
		}
	}
	return width, len(lines)
}
