package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/ma8el/feat/internal/api"
)

// briefTab opens the brief tab on the given task.
func briefTab(t *testing.T, task api.Task) Model {
	t.Helper()

	model := sized(dashboard(newFakeBackend(), task), 120, 32)
	model.selected = task.ID
	return press(t, press(t, model, "L"), "L")
}

// TestTheBriefTabOpensForEveryTaskItCanBeOn records ADR-041's rule about tabs,
// on the one this change adds.
//
// A tab that declines to open is a tab the cycle cannot pass, so a task with no
// brief and a task that is no longer listed each open and say so rather than
// leaving the region blank or refusing the key.
func TestTheBriefTabOpensForEveryTaskItCanBeOn(t *testing.T) {
	empty := liveTask()
	empty.Brief = ""

	for _, shape := range []struct {
		what string
		task api.Task
		want string
	}{
		{"a launched task", liveTask(), "Export the daily report"},
		{"a draft", pendingDraft(), "Retry three times before giving up."},
		{"a task whose brief is empty", empty, "this task has no brief"},
	} {
		model := briefTab(t, shape.task)
		if model.screen != screenBrief {
			t.Fatalf("the brief tab did not open for %s: screen is %v", shape.what, model.screen)
		}
		if body := ansi.Strip(content(model)); !strings.Contains(body, shape.want) {
			t.Errorf("the brief tab for %s does not say %q:\n%s", shape.what, shape.want, body)
		}
	}

	// A selection the list does not hold — one a cleanup archived while the tab
	// was open — is a state the tab reports rather than one it refuses.
	gone := briefTab(t, liveTask())
	gone.selected = "a task that went away"
	if body := ansi.Strip(content(gone)); !strings.Contains(body, "no longer listed") {
		t.Errorf("the brief tab does not say the task went away:\n%s", body)
	}
}

// TestTheBriefNamesWhereItCameFrom is the field that moved here with it.
//
// Where a brief came from is a fact about the document rather than about the
// task's state, so it is beside the heading here instead of a seventh field on
// the panel (ADR-086).
func TestTheBriefNamesWhereItCameFrom(t *testing.T) {
	model := briefTab(t, liveTask())

	body := ansi.Strip(content(model))
	if !strings.Contains(body, "markdown · /srv/notes/export.md") {
		t.Errorf("the brief does not name its source:\n%s", body)
	}
	// Beside the heading rather than under it: one line carries both.
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, "brief") && strings.Contains(line, "/srv/notes/export.md") {
			return
		}
	}
	t.Errorf("the source is not on the heading's line:\n%s", body)
}

// TestTheBriefScrollsAndSaysHowMuchIsLeft is the same rule the task panel
// follows: a body clipped in silence reads as a body that is short.
func TestTheBriefScrollsAndSaysHowMuchIsLeft(t *testing.T) {
	task := liveTask()
	task.Brief = strings.Repeat("A paragraph of the brief, long enough to need a scroll.\n\n", 30)

	model := briefTab(t, task)
	width, height := model.mainRegionSize()

	top := model.briefBody(width, height)
	if !strings.Contains(ansi.Strip(top), "lines below") {
		t.Fatalf("a clipped brief does not say what is below it:\n%s", top)
	}

	down := press(t, model, "pgdown")
	if down.brief.scroll != panelPage {
		t.Fatalf("pgdown left the brief at %d, want %d", down.brief.scroll, panelPage)
	}
	if !strings.Contains(ansi.Strip(down.briefBody(width, height)), "lines above") {
		t.Errorf("a scrolled brief does not say what is above it:\n%s", down.briefBody(width, height))
	}

	// And it stops at the end rather than building up an offset that takes as
	// many presses to undo.
	at := down
	for range 40 {
		at = press(t, at, "pgdown")
	}
	settled := at.brief.scroll
	if again := press(t, at, "pgdown"); again.brief.scroll != settled {
		t.Errorf("scrolling past the end of the brief moved from %d to %d", settled, again.brief.scroll)
	}
}

// TestTheBriefAndThePanelKeepSeparatePositions is why the brief has an offset of
// its own.
//
// They are two tabs over one task, and a shared offset would make each of them
// move the other's position every time it was scrolled — so returning to a tab
// would land somewhere nobody left it.
func TestTheBriefAndThePanelKeepSeparatePositions(t *testing.T) {
	task := reviewed().Task
	task.Brief = strings.Repeat("A paragraph of the brief, long enough to need a scroll.\n\n", 30)

	backend := newFakeBackend()
	status := reviewed()
	status.Task = task
	backend.reviewStatus = status

	model := sized(dashboard(backend, task), 120, 32)
	model.selected = task.ID

	// Down the panel, then over to the brief and down that.
	panel := press(t, press(t, model, "L"), "pgdown")
	if panel.review.scroll == 0 {
		t.Fatalf("the panel did not scroll, so there is nothing to keep separate")
	}
	brief := press(t, press(t, panel, "L"), "pgdown")

	if brief.review.scroll != panel.review.scroll {
		t.Errorf("scrolling the brief moved the panel from %d to %d",
			panel.review.scroll, brief.review.scroll)
	}
	if brief.brief.scroll != panelPage {
		t.Errorf("the brief is at %d, want %d", brief.brief.scroll, panelPage)
	}

	// And back. The panel re-opens at the top, because opening it re-reads the
	// comparison for the selected task and discards what it was holding; what
	// matters here is that it does not open at the brief's position, and that
	// leaving the brief did not cost the brief's.
	back := press(t, brief, "H")
	if back.review.scroll == brief.brief.scroll {
		t.Errorf("the panel opened at the brief's position, %d", back.review.scroll)
	}
	if back.brief.scroll != panelPage {
		t.Errorf("leaving the brief moved it to %d, want %d", back.brief.scroll, panelPage)
	}
}

// TestSelectingAnotherTaskOpensItsBriefAtTheTop keeps the offset attached to the
// document it was measured against.
//
// Half way down one task's brief is nowhere in particular in another's, and a
// tab that kept the number would open the next brief in the middle of a
// paragraph.
func TestSelectingAnotherTaskOpensItsBriefAtTheTop(t *testing.T) {
	first := liveTask()
	first.Brief = strings.Repeat("A paragraph of the first task's brief.\n\n", 30)
	second := otherTask()

	model := sized(dashboard(newFakeBackend(), first, second), 120, 32)
	model.selected = first.ID

	scrolled := press(t, press(t, press(t, model, "L"), "L"), "pgdown")
	if scrolled.brief.scroll == 0 {
		t.Fatalf("the brief did not scroll, so there is nothing to reset")
	}

	moved := press(t, scrolled, "J")
	if moved.selected == first.ID {
		t.Fatalf("J did not move the selection")
	}
	if moved.brief.scroll != 0 {
		t.Errorf("another task's brief opened at %d, want the top", moved.brief.scroll)
	}
}

// TestTextFeatDidNotWriteCannotBreakTheBriefTab is the frame defect where it is
// now likeliest.
//
// The brief is a file somebody wrote, and it is nearly the whole of this body: a
// tab is one byte of no display width that the terminal draws as a jump to the
// next multiple of eight, and a carriage return puts the rest of the line back
// at the terminal's left edge, over whatever is already there (ADR-054).
func TestTextFeatDidNotWriteCannotBreakTheBriefTab(t *testing.T) {
	task := liveTask()
	task.Brief = "Progress was reported like this:\r100%\vand then\tsome\bmore.\n" +
		"ok\tgithub.com/ma8el/feat/internal/api\t1.383s"

	model := briefTab(t, task)
	width, _ := model.mainRegionSize()

	body := model.wrappedBrief(width)
	for name, control := range map[string]string{
		"a tab": "\t", "a carriage return": "\r", "a vertical tab": "\v", "a backspace": "\b",
	} {
		if strings.Contains(body, control) {
			t.Errorf("the brief carries %s, which the terminal will not draw as one cell:\n%s", name, body)
		}
	}
	for i, line := range strings.Split(body, "\n") {
		if got := ansi.StringWidth(line); got > width {
			t.Errorf("brief line %d is %d cells against a region of %d: %q", i, got, width, line)
		}
	}
	for i, line := range strings.Split(model.View(), "\n") {
		if got := ansi.StringWidth(line); got > 120 {
			t.Errorf("line %d is %d cells wide against a terminal of 120: %q", i, got, line)
		}
	}
}
