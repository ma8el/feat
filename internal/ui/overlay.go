package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// overlayOn draws top over background with its upper-left corner at (x, y),
// measured in terminal cells, and returns the composite.
//
// It exists because an overlay is what ADR-041 chose instead of a screen that
// replaces the dashboard: a user preparing a task or confirming a cleanup keeps
// the task list they were reading. lipgloss v1.1.0 places content in a
// whitespace box and cannot layer one rendering over another, so the splice is
// here.
//
// Cuts are made by cell rather than by byte through ansi.Cut, which neither
// splits an escape sequence nor counts one as visible width. A background line
// shorter than the overlay's left edge is padded to it, so a dialog is not
// pulled leftwards by a short line behind it.
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
	// lines, rather than being silently dropped: a caller that got the arithmetic
	// wrong should see the result rather than nothing.
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

	// Any style the background left open ends before the overlay begins, so a
	// colour behind the dialog cannot bleed into it. The reset is written only
	// when there is a style to end, so that a plain background composites to
	// plain text and stays readable in a test and in a pipe.
	styled := strings.Contains(background, "\x1b")
	if styled {
		out.WriteString(ansi.ResetStyle)
	}
	out.WriteString(top)
	if styled {
		out.WriteString(ansi.ResetStyle)
	}

	if right := x + width; backgroundWidth > right {
		out.WriteString(ansi.Cut(background, right, backgroundWidth))
	}
	return out.String()
}

// centreOverlay places top in the middle of a region of the given size.
//
// Vertically it sits slightly above centre, which is where a dialog is read
// from rather than exactly halfway down.
func centreOverlay(background, top string, width, height int) string {
	topWidth, topHeight := blockSize(top)

	x := (width - topWidth) / 2
	y := (height - topHeight) / 3
	return overlayOn(background, top, x, y)
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
