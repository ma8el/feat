package ui

import (
	"strings"
	"testing"
)

// TestTheTaskPanelCarriesBothHalvesOnce is the merge.
//
// Detail and review were two tabs that shared their subject, their header, their
// workflow, their repository list, and their check summary, and neither filled
// the main region on its own (ADR-042). One panel has to carry what FR-UI-003
// requires of task detail and what FR-REV-001 requires of review — and carry the
// shared parts once, which is the reason for merging them.
func TestTheTaskPanelCarriesBothHalvesOnce(t *testing.T) {
	panel := reviewScreen(t, newFakeBackend()).taskPanel()

	for requirement, want := range map[string]string{
		// FR-UI-003: brief, repository/base mapping, tmux target, runtime, checks.
		"the brief":       "Export the daily report",
		"the base commit": "1a2b3c4d5e6f",
		"the branch":      "feat/7f3a1c2e-add-a-scheduled-export-job",
		"the tmux pane":   "%7",
		"the runtime":     "absent",
		"the environment": "feat-agent-example-7f3a1c2e",
		// FR-REV-001 and FR-REV-004: the comparison and the decision.
		"the head commit":  "001122334455",
		"the line counts":  "+214 -36",
		"the decision":     "pending",
		"the check detail": "Feat ran this",
	} {
		if !strings.Contains(panel, want) {
			t.Errorf("the panel is missing %s (%q):\n%s", requirement, want, panel)
		}
	}

	// And the shared parts appear once. Detail and review each listed the
	// repositories with their own subset of the same facts, and each stated the
	// workflow in its header; showing both was the duplication that made two
	// thin tabs look like two different things.
	for what, needle := range map[string]string{
		"the workflow":       "workflow ",
		"a repository":       "/srv/worktrees/example/7f3a1c2e/core",
		"the check summary":  "run by Feat",
		"the task's project": "example · ",
	} {
		if got := strings.Count(panel, needle); got != 1 {
			t.Errorf("%s appears %d times, want once:\n%s", what, got, panel)
		}
	}
}

// TestALabelWiderThanItsColumnKeepsItsLine is a defect the merge made visible.
//
// The label column has a fixed width, and lipgloss wraps rather than overflows:
// "compose project" came out as "compose" and then "project" against the panel's
// left edge, where it read as a heading rather than a label.
func TestALabelWiderThanItsColumnKeepsItsLine(t *testing.T) {
	panel := reviewScreen(t, newFakeBackend()).taskPanel()

	for _, line := range strings.Split(panel, "\n") {
		if strings.HasPrefix(line, "project ") {
			t.Errorf("a wrapped label became its own line:\n%s", panel)
		}
	}
	if !strings.Contains(panel, "compose project") {
		t.Errorf("the compose project label did not survive on one line:\n%s", panel)
	}
}

// TestTheTaskPanelScrollsRatherThanHidingTheBrief keeps what does not fit
// reachable.
//
// The panel is taller than the region on any terminal worth supporting once a
// task has two repositories and a brief. FR-UI-003 requires the brief, and a
// region that clipped it in silence would read as a task that has none.
func TestTheTaskPanelScrollsRatherThanHidingTheBrief(t *testing.T) {
	model := sized(reviewScreen(t, newFakeBackend()), 120, 32)

	width, height := model.mainRegionSize()
	top := model.taskBody(width, height-2)
	if strings.Contains(top, "Export the daily report") {
		t.Skip("the panel fits this region, so there is nothing to scroll to")
	}
	if !strings.Contains(top, "pgup/pgdn") {
		t.Fatalf("a clipped panel does not say it was clipped:\n%s", top)
	}

	// Down until the brief is reached, which must happen within the panel's
	// length rather than never.
	at := model
	for range 20 {
		at = press(t, at, "pgdown")
		if strings.Contains(at.taskBody(width, height-2), "Export the daily report") {
			if back := press(t, at, "pgup"); back.review.scroll >= at.review.scroll {
				t.Errorf("pgup did not move back: %d then %d", at.review.scroll, back.review.scroll)
			}
			return
		}
	}
	t.Errorf("the brief was never reached by scrolling:\n%s", at.taskBody(width, height-2))
}

// TestScrollingStopsAtTheEndOfThePanel keeps a held key from building up an
// offset that takes as many presses to undo.
func TestScrollingStopsAtTheEndOfThePanel(t *testing.T) {
	model := sized(reviewScreen(t, newFakeBackend()), 120, 32)

	at := model
	for range 30 {
		at = press(t, at, "pgdown")
	}
	settled := at.review.scroll

	if again := press(t, at, "pgdown"); again.review.scroll != settled {
		t.Errorf("scrolling past the end moved from %d to %d", settled, again.review.scroll)
	}
	// And one press back is one page back, rather than the many it would take to
	// unwind an unbounded offset.
	if back := press(t, at, "pgup"); back.review.scroll != settled-panelPage {
		t.Errorf("pgup from the end left the offset at %d, want %d", back.review.scroll, settled-panelPage)
	}
}

// TestSOpensTheShellOnTheTaskPanelToo records a key that used to mean two things.
//
// `s` ran the configured status command here and opened the task's shell
// everywhere else. What that command printed was a line or two on the screen the
// TUI had just left, gone before it could be read, and the panel already states
// what it would have said (ADR-045). The command itself is still configured and
// still expanded — `feat review` prints it — so what this test pins is the key,
// not the feature.
func TestSOpensTheShellOnTheTaskPanelToo(t *testing.T) {
	backend := newFakeBackend()
	model := reviewScreen(t, backend)

	press(t, model, "s")

	if len(backend.reviewRan) != 0 {
		t.Errorf("s ran a review command from the task panel: %+v", backend.reviewRan)
	}
	if len(backend.shells) != 1 {
		t.Errorf("s opened %d shells from the task panel, want one", len(backend.shells))
	}
}
