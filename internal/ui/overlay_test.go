package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// TestOverlayKeepsTheBackgroundAroundIt is the property ADR-041 chose an overlay
// for: what the user was reading stays on screen beside the dialog.
func TestOverlayKeepsTheBackgroundAroundIt(t *testing.T) {
	background := strings.Join([]string{
		"aaaaaaaaaaaaaaaaaaaa",
		"bbbbbbbbbbbbbbbbbbbb",
		"cccccccccccccccccccc",
		"dddddddddddddddddddd",
	}, "\n")

	composite := overlayOn(background, "XXXX\nYYYY", 8, 1)

	want := strings.Join([]string{
		"aaaaaaaaaaaaaaaaaaaa",
		"bbbbbbbbXXXXbbbbbbbb",
		"ccccccccYYYYcccccccc",
		"dddddddddddddddddddd",
	}, "\n")
	if composite != want {
		t.Errorf("overlay composited to\n%s\nwant\n%s", composite, want)
	}
}

// TestOverlayPadsAShortBackgroundLine checks that a dialog is not pulled left by
// a line that ends before it.
func TestOverlayPadsAShortBackgroundLine(t *testing.T) {
	composite := overlayOn("ab\n", "XX", 6, 0)

	line, _, _ := strings.Cut(composite, "\n")
	if line != "ab    XX" {
		t.Errorf("short line composited to %q, want %q", line, "ab    XX")
	}
}

// TestOverlayDrawsBelowTheLastLine checks that a dialog placed past the end of
// the background appears rather than disappearing. A caller whose arithmetic is
// wrong should be able to see that it is.
func TestOverlayDrawsBelowTheLastLine(t *testing.T) {
	composite := overlayOn("only", "XX", 0, 2)

	lines := strings.Split(composite, "\n")
	if len(lines) != 3 {
		t.Fatalf("composited to %d lines, want 3: %q", len(lines), composite)
	}
	if lines[2] != "XX" {
		t.Errorf("last line is %q, want %q", lines[2], "XX")
	}
}

// TestOverlayDoesNotSplitAnEscapeSequence is why this cuts by cell rather than
// by byte. A background rendered with colour must survive having a dialog put on
// top of it, and a cut through the middle of an escape sequence would emit the
// rest of it as text.
func TestOverlayDoesNotSplitAnEscapeSequence(t *testing.T) {
	// Written as escape sequences rather than rendered through lipgloss, which
	// drops colour when nothing is attached to a terminal — so a styled
	// background is exactly what a test would otherwise never see, and this is
	// the case the cell-wise cut exists for.
	background := "\x1b[31maaaaaaaaaaaaaaaaaaaa\x1b[m"

	composite := overlayOn(background, "XXXX", 8, 0)

	if width := ansi.StringWidth(composite); width != 20 {
		t.Errorf("composite is %d cells wide, want 20: %q", width, composite)
	}
	if got := ansi.Strip(composite); got != "aaaaaaaaXXXXaaaaaaaa" {
		t.Errorf("composite reads as %q, want %q", got, "aaaaaaaaXXXXaaaaaaaa")
	}
}

// TestOverlayLeavesAPlainBackgroundPlain keeps the composite readable where
// nothing was styled, which is what a test and a piped run both see.
func TestOverlayLeavesAPlainBackgroundPlain(t *testing.T) {
	composite := overlayOn("aaaaaaaa", "XX", 3, 0)

	if strings.Contains(composite, "\x1b") {
		t.Errorf("plain background composited to styled output: %q", composite)
	}
}

// TestOverlayPreservesWideCharacterWidth checks the cell arithmetic against
// characters that are two cells wide, which a byte-oriented splice would
// misplace.
func TestOverlayPreservesWideCharacterWidth(t *testing.T) {
	background := "日本語日本語日本語日" // ten characters, twenty cells

	composite := overlayOn(background, "XXXX", 8, 0)

	if width := ansi.StringWidth(composite); width != 20 {
		t.Errorf("composite is %d cells wide, want 20: %q", width, composite)
	}
}

// TestCentreOverlayPlacesADialogInsideItsRegion checks that a centred dialog is
// wholly within the region rather than hanging off an edge.
func TestCentreOverlayPlacesADialogInsideItsRegion(t *testing.T) {
	background := strings.TrimSuffix(strings.Repeat(strings.Repeat("·", 40)+"\n", 12), "\n")
	dialog := "┌────────┐\n│ dialog │\n└────────┘"

	composite := centreOverlay(background, dialog, 40, 12)

	lines := strings.Split(composite, "\n")
	if len(lines) != 12 {
		t.Fatalf("centred dialog changed the region to %d lines, want 12", len(lines))
	}
	for i, line := range lines {
		if width := ansi.StringWidth(line); width != 40 {
			t.Errorf("line %d is %d cells wide, want 40: %q", i, width, line)
		}
	}
	if !strings.Contains(composite, "│ dialog │") {
		t.Errorf("centred dialog is not in the composite:\n%s", composite)
	}
}

func TestBlockSizeMeasuresTheWidestLine(t *testing.T) {
	width, height := blockSize("ab\nabcd\nabc")

	if width != 4 || height != 3 {
		t.Errorf("measured %dx%d, want 4x3", width, height)
	}
}
