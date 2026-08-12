package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// TestACardIsExactlyTheSizeItWasGiven is what the two regions depend on.
//
// They are drawn side by side by concatenating rows, so a card that grew with
// its content or shrank without it would put its neighbour's rows out of line
// from that row down.
func TestACardIsExactlyTheSizeItWasGiven(t *testing.T) {
	for _, body := range []string{
		"",
		"one line",
		strings.Repeat("a line of the body\n", 40),
		strings.Repeat("x", 400),
	} {
		drawn := card("a header", body, 40, 12, false)

		lines := strings.Split(drawn, "\n")
		if len(lines) != 12 {
			t.Errorf("a card of 12 rows drew %d for a body of %d bytes", len(lines), len(body))
		}
		for i, line := range lines {
			if got := ansi.StringWidth(line); got != 40 {
				t.Errorf("row %d of the card is %d cells, want 40: %q", i, got, line)
			}
		}
	}
}

// TestACardRulesItsHeaderOffFromItsBody is the complaint the cards answer.
//
// The rail's heading and the tab bar were the first line of their own content,
// so a heading and the first thing under it read as two entries of one list.
func TestACardRulesItsHeaderOffFromItsBody(t *testing.T) {
	lines := strings.Split(ansi.Strip(card("tasks", "the first task", 20, 8, false)), "\n")

	if !strings.HasPrefix(lines[0], cardTopLeft) || !strings.HasSuffix(lines[0], cardTopRight) {
		t.Errorf("a card does not open with a rounded corner: %q", lines[0])
	}
	if !strings.Contains(lines[1], "tasks") {
		t.Errorf("the header is not the first row inside the card: %q", lines[1])
	}
	if !strings.HasPrefix(lines[2], cardHeaderLeft) || !strings.HasSuffix(lines[2], cardHeaderRight) {
		t.Errorf("nothing rules the header off from the body: %q", lines[2])
	}
	if !strings.Contains(lines[3], "the first task") {
		t.Errorf("the body does not start under the rule: %q", lines[3])
	}
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last, cardBottomLeft) || !strings.HasSuffix(last, cardBottomRight) {
		t.Errorf("a card does not close with a rounded corner: %q", last)
	}
}

// TestACardEndsTheStylingOfWhatItHolds keeps a rendered pane inside its border.
//
// A tmux capture carries the colour tmux emitted and not the clearing tmux does
// as it draws, so a line that set a background and never cleared it would run
// that background through the card's gutter, over its border, and on across the
// region beside it.
func TestACardEndsTheStylingOfWhatItHolds(t *testing.T) {
	body := "\x1b[41mred to the end of the line"
	drawn := card("header", body, 30, 6, false)

	row := ""
	for _, line := range strings.Split(drawn, "\n") {
		if strings.Contains(line, "red to the end") {
			row = line
		}
	}
	if row == "" {
		t.Fatalf("the body is not in the card:\n%s", drawn)
	}
	before, _, ok := strings.Cut(row, "red to the end of the line")
	if !ok {
		t.Fatalf("the row does not hold the body: %q", row)
	}
	after := row[len(before)+len("red to the end of the line"):]
	if !strings.Contains(after, ansi.ResetStyle) {
		t.Errorf("a styled line was not ended before the card's border: %q", row)
	}
	// And the border is still drawn: ending the style must not eat the edge.
	if !strings.HasSuffix(ansi.Strip(row), cardVertical) {
		t.Errorf("the card's right border is missing from %q", ansi.Strip(row))
	}
}

// TestACardCutsALineRatherThanWrappingIt is why the box is drawn here rather
// than by lipgloss.
//
// A wrapped line moves every row after it, which for two cards side by side
// means one region's content sliding down against the other's. A pane wrapped
// mid-escape-sequence is worse: what the terminal does with half a sequence is
// set a colour and keep it.
func TestACardCutsALineRatherThanWrappingIt(t *testing.T) {
	drawn := card("header", "a body line that is far too long for this card", 24, 6, false)

	lines := strings.Split(ansi.Strip(drawn), "\n")
	if !strings.Contains(lines[3], "…") {
		t.Errorf("a cut line does not say it was cut: %q", lines[3])
	}
	if strings.Contains(lines[4], "long for this card") {
		t.Errorf("the line was wrapped onto the row below: %q", lines[4])
	}
}

// TestAHeaderDropsItsAsideRatherThanTheTitle records which half of a header
// survives a narrow region.
//
// The aside is always a summary of what is below it — how many tasks are
// waiting, which task the region is about — so half of it says nothing the
// content does not, while half a title says nothing at all.
func TestAHeaderDropsItsAsideRatherThanTheTitle(t *testing.T) {
	if got := cardHeader("tasks", "3 tasks", 12); got != "tasks" {
		t.Errorf("a header with no room for its aside rendered %q, want the title alone", got)
	}
	got := cardHeader("tasks", "3 tasks", 20)
	if !strings.HasPrefix(got, "tasks") || !strings.HasSuffix(got, "3 tasks") {
		t.Errorf("a header that fits rendered %q, want the aside right-aligned", got)
	}
	if width := ansi.StringWidth(got); width != 20 {
		t.Errorf("the aside is %d cells from the left, want it against the right edge at 20", width)
	}
}
