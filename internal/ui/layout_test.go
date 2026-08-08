package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/ma8el/feat/internal/api"
)

// content is what a screen renders, before the three-region layout places it.
//
// Tests of what the dashboard says use it so that a change to where a region
// puts its content cannot fail a test about what the content is. Where the
// layout itself is the subject, the tests below call View.
func content(m Model) string { return m.stackedView() }

// sized gives a model a terminal, which is what decides between the three
// regions and the stacked fallback.
func sized(m Model, width, height int) Model {
	updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return updated.(Model)
}

// otherTask is a second task, in a second project, so that grouping and the
// three-task case have something to group.
func otherTask() api.Task {
	task := liveTask()
	task.ID = "9b02de41-1111-2222-3333-444455556666"
	task.Key = "9b02de41"
	task.ProjectID = "platform"
	task.Title = "Add rate limiting to the public API"
	task.Attention = "none"
	return task
}

// TestNoLineWrapsAtTheSupportedWidth is the slice 13 acceptance criterion.
//
// A task row used to be 158 cells against a terminal of 80 to 160, so three
// tasks read as nine lines of unaligned text. Nothing the dashboard draws may
// now exceed the terminal it is drawn in (ADR-041 evidence 1).
func TestNoLineWrapsAtTheSupportedWidth(t *testing.T) {
	third := otherTask()
	third.ID, third.Key = "7c1a9f30-aaaa-bbbb-cccc-dddddddddddd", "7c1a9f30"
	third.Title = "Emit OpenTelemetry spans"

	for _, width := range []int{96, 120, 160} {
		model := sized(dashboard(newFakeBackend(), liveTask(), otherTask(), third), width, 32)

		for i, line := range strings.Split(model.View(), "\n") {
			if got := ansi.StringWidth(line); got > width {
				t.Errorf("at width %d, line %d is %d cells:\n%s", width, i, got, line)
			}
		}
	}
}

// TestTheFrameKeepsItsRegionsInPlace checks that the rail, the main region, and
// the footer are all on screen at once, which is what a user watching three
// tasks needs and what a screen that replaced every other one could not do.
func TestTheFrameKeepsItsRegionsInPlace(t *testing.T) {
	model := sized(dashboard(newFakeBackend(), liveTask(), otherTask()), 120, 32)
	view := model.View()

	for _, want := range []string{
		"tasks",                  // the rail's heading
		"overview",               // the tab bar
		"7f3a1c2e",               // a task in the rail
		"/srv/worktrees/example", // the footer's worktree
		"type here",              // the footer's hints, for the tab that has focus
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the frame does not carry %q:\n%s", want, view)
		}
	}
}

// TestTheRailGroupsTasksByProject is FR-UI-001's project drill-down, which the
// flat global list did not have.
func TestTheRailGroupsTasksByProject(t *testing.T) {
	model := sized(dashboard(newFakeBackend(), liveTask(), otherTask()), 120, 32)
	view := model.View()

	example := strings.Index(view, "example")
	platform := strings.Index(view, "platform")
	switch {
	case example < 0 || platform < 0:
		t.Fatalf("the rail does not name both projects:\n%s", view)
	case example > platform:
		t.Errorf("projects are not in the order their tasks are:\n%s", view)
	}
}

// TestARailEntryCarriesTheRequiredListFields is FR-UI-002 after ADR-041 moved
// the four fields FR-UI-003 and FR-UI-005 already required.
//
// What must remain is what answers which task to go to next: identity,
// attention, agent state, elapsed time, and the changed-file count.
func TestARailEntryCarriesTheRequiredListFields(t *testing.T) {
	model := sized(dashboard(newFakeBackend(), liveTask()), 120, 32)
	rail := model.railView(28)

	for field, want := range map[string]string{
		"the task key":           "7f3a1c2e",
		"the title":              "Add a scheduled",
		"the attention glyph":    badgeMaybe,
		"the agent state":        "running",
		"the elapsed time":       "1h30m",
		"the changed-file count": "7 files",
	} {
		if !strings.Contains(rail, want) {
			t.Errorf("a rail entry is missing %s (%q):\n%s", field, want, rail)
		}
	}
}

// TestAttentionAndAgentStateStaySeparateInTheRail keeps the domain's separation
// where a user actually reads it.
//
// Feat holds process, attention, workflow, and runtime states apart, and a
// composite badge would put them back together. A task that needs the user and a
// task that does not must differ in the glyph while both still say what their
// process is doing.
func TestAttentionAndAgentStateStaySeparateInTheRail(t *testing.T) {
	waiting := liveTask()
	waiting.Attention = "needs_input"

	calm := otherTask()
	calm.Attention = "none"

	rail := sized(dashboard(newFakeBackend(), waiting, calm), 120, 32).railView(28)

	if !strings.Contains(rail, badgeNeedsInput) {
		t.Errorf("a task needing input carries no glyph of its own:\n%s", rail)
	}
	if !strings.Contains(rail, badgeIdle) {
		t.Errorf("a task needing nothing carries no glyph of its own:\n%s", rail)
	}
	// Both entries still report their process state, so the glyph is not
	// standing in for it.
	if count := strings.Count(rail, "running"); count != 2 {
		t.Errorf("agent state appears %d times for two running tasks:\n%s", count, rail)
	}
}

// TestAnOverlayLeavesTheTaskListVisible is what ADR-041 chose a dialog for. A
// full-screen modal took the task list away for the duration, which is the
// complaint the layout was built to answer.
func TestAnOverlayLeavesTheTaskListVisible(t *testing.T) {
	model := sized(dashboard(newFakeBackend(), liveTask(), otherTask()), 120, 32)

	opened := press(t, model, "?")
	view := opened.View()

	if !strings.Contains(view, "9b02de41") {
		t.Errorf("the task list is not visible behind the dialog:\n%s", view)
	}
	if !strings.Contains(view, "prepare a new task") {
		t.Errorf("the key map is not on screen:\n%s", view)
	}

	closed := press(t, opened, "esc")
	if strings.Contains(closed.View(), "prepare a new task") {
		t.Errorf("the dialog did not close:\n%s", closed.View())
	}
}

// TestTheKeyMapAnswersOnlyItsOwnKeys checks that an overlay does not pass keys
// through to a task the user cannot see while reading about how to act on one.
func TestTheKeyMapAnswersOnlyItsOwnKeys(t *testing.T) {
	model := sized(dashboard(newFakeBackend(), liveTask(), otherTask()), 120, 32)
	opened := press(t, model, "?")

	moved := press(t, opened, "down")
	if moved.cursor != opened.cursor {
		t.Errorf("the cursor moved behind the key map: %d became %d", opened.cursor, moved.cursor)
	}
}

// TestANarrowTerminalFallsBackToOneColumn keeps a small terminal usable. A rail
// and a main region inside eighty columns starve each other.
func TestANarrowTerminalFallsBackToOneColumn(t *testing.T) {
	model := sized(dashboard(newFakeBackend(), liveTask()), 80, 24)
	view := model.View()

	if strings.Contains(view, "│") {
		t.Errorf("a narrow terminal drew the divided layout:\n%s", view)
	}
	if !strings.Contains(view, "7f3a1c2e") {
		t.Errorf("the fallback does not list the task:\n%s", view)
	}
}

// TestTheSizeIsNotDecidedBeforeTheTerminalReportsOne checks that a model which
// has had no tea.WindowSizeMsg draws the layout rather than the fallback, so
// that what is drawn does not depend on which message arrived first.
func TestTheSizeIsNotDecidedBeforeTheTerminalReportsOne(t *testing.T) {
	if dashboard(newFakeBackend(), liveTask()).narrow() {
		t.Error("a terminal that has not reported a size was treated as narrow")
	}
}

// TestTheOverviewDropsColumnsThatDoNotFit checks the wide table degrades by
// losing columns rather than by wrapping, and says that it did.
func TestTheOverviewDropsColumnsThatDoNotFit(t *testing.T) {
	model := sized(dashboard(newFakeBackend(), liveTask()), 100, 32)
	body := model.overviewBody(60)

	if !strings.Contains(body, "do not fit this terminal") {
		t.Errorf("the overview drops columns without saying so:\n%s", body)
	}
	for i, line := range strings.Split(body, "\n") {
		if got := ansi.StringWidth(line); got > 60 {
			t.Errorf("line %d of the overview is %d cells, want at most 60: %q", i, got, line)
		}
	}
}

// TestTabMovesTheMainRegion checks the tab bar's key.
func TestTabMovesTheMainRegion(t *testing.T) {
	model := sized(dashboard(newFakeBackend(), liveTask()), 120, 32)

	if model.activeTab() != tabTerminal {
		t.Fatalf("a fresh dashboard opens on %v, want the terminal", model.activeTab())
	}
	if next := press(t, model, "tab"); next.activeTab() != tabOverview {
		t.Errorf("tab moved to %v, want the overview", next.activeTab())
	}
	if back := press(t, model, "shift+tab"); back.activeTab() != tabRuntime {
		t.Errorf("shift+tab moved to %v, want runtime, wrapping at the end", back.activeTab())
	}
}

// TestTheFooterCarriesTheWorktreeAndTheMachine checks the two values that moved
// into the footer: the path a user would otherwise look up and paste, and the
// machine card that used to sit above the task list and push it down the screen.
func TestTheFooterCarriesTheWorktreeAndTheMachine(t *testing.T) {
	model := sized(withResources(dashboard(newFakeBackend(), liveTask()), sampled(), nil), 160, 32)
	footer := model.frameFooter(160)

	if !strings.Contains(footer, "/srv/worktrees/example/7f3a1c2e/core") {
		t.Errorf("the footer does not carry the selected task's worktree:\n%s", footer)
	}
	if !strings.Contains(footer, "load") {
		t.Errorf("the footer does not carry the machine's resources:\n%s", footer)
	}
}

// TestAReadOnlyBindingIsNotOfferedAsAWorktree checks that the footer names a
// directory a user can work in. A read-only repository has no branch and no
// worktree of its own.
func TestAReadOnlyBindingIsNotOfferedAsAWorktree(t *testing.T) {
	task := liveTask()
	task.Repositories[0].Access = "read_only"
	task.Repositories[0].WorktreePath = "/srv/worktrees/example/7f3a1c2e/core"

	note := worktreeNote(task)
	if strings.Contains(note, "/core") {
		t.Errorf("a read-only binding was offered as the working directory: %q", note)
	}
}

// TestADialogNeverReachesTheFooter keeps the one part of the frame that holds
// still. A dialog that covered the footer would take the machine's resources and
// the selected task's worktree off screen exactly when a user is deciding
// something.
func TestADialogNeverReachesTheFooter(t *testing.T) {
	model := sized(withResources(dashboard(newFakeBackend(), liveTask()), sampled(), nil), 120, 26)
	opened := press(t, model, "?")

	lines := strings.Split(opened.View(), "\n")
	footer := lines[len(lines)-footerHeight:]

	for i, line := range footer {
		if strings.ContainsAny(line, "╭╮╰╯│") && i > 0 {
			t.Errorf("the dialog reached footer line %d: %q", i, line)
		}
	}
	if !strings.Contains(strings.Join(footer, "\n"), "load") {
		t.Errorf("the footer lost the machine's resources behind a dialog:\n%s",
			strings.Join(footer, "\n"))
	}
}

// TestADialogTallerThanTheTerminalSaysWhatItDropped checks that content which
// does not fit is reported rather than silently cut.
func TestADialogTallerThanTheTerminalSaysWhatItDropped(t *testing.T) {
	model := sized(dashboard(newFakeBackend(), liveTask()), 120, 20)
	view := press(t, model, "?").View()

	if !strings.Contains(view, "more lines than fit here") {
		t.Errorf("a clipped dialog does not say it was clipped:\n%s", view)
	}
	for i, line := range strings.Split(view, "\n") {
		if got := ansi.StringWidth(line); got > 120 {
			t.Errorf("line %d is %d cells after clipping: %q", i, got, line)
		}
	}
}

// TestTabCyclesPastAViewWithItsOwnKeyboard is the defect found in use: tab
// moved overview → detail → review and then stopped.
//
// Review and runtime answer their own keys and return for everything else, so
// they swallowed the key that was meant to leave them. The cycle has to close,
// including back round to the first view.
func TestTabCyclesPastAViewWithItsOwnKeyboard(t *testing.T) {
	model := sized(dashboard(newFakeBackend(), liveTask()), 120, 32)

	want := []tab{tabOverview, tabDetail, tabReview, tabRuntime, tabTerminal}
	at := model
	for i, expected := range want {
		at = press(t, at, "tab")
		if at.activeTab() != expected {
			t.Fatalf("tab %d landed on %v, want %v", i+1, at.activeTab(), expected)
		}
	}
}

// TestTheRailIsReachableFromEveryView is the second defect found in use: the
// plain arrows belong to whichever view has the keyboard, so from review there
// was no way to change task at all.
func TestTheRailIsReachableFromEveryView(t *testing.T) {
	second := otherTask()

	for _, key := range []string{"shift+down", "ctrl+n"} {
		for _, open := range []string{"", "tab", "v", "R"} {
			model := sized(dashboard(newFakeBackend(), liveTask(), second), 120, 32)
			if open != "" {
				model = press(t, model, open)
			}

			moved := press(t, model, key)
			if moved.cursor != 1 {
				t.Errorf("%q from %q left the cursor at %d, want 1", key, open, moved.cursor)
			}
			if got, _ := moved.current(); got.ID != second.ID {
				t.Errorf("%q from %q selected %s, want %s", key, open, got.Key, second.Key)
			}
		}
	}
}

// TestSelectingATaskWrapsAtBothEnds keeps the rail's own movement closed, as the
// tab cycle is.
func TestSelectingATaskWrapsAtBothEnds(t *testing.T) {
	model := sized(dashboard(newFakeBackend(), liveTask(), otherTask()), 120, 32)

	if up := press(t, model, "shift+up"); up.cursor != 1 {
		t.Errorf("shift+up from the first task went to %d, want the last", up.cursor)
	}
	down := press(t, press(t, model, "shift+down"), "shift+down")
	if down.cursor != 0 {
		t.Errorf("shift+down past the last task went to %d, want the first", down.cursor)
	}
}

// TestChangingTaskBringsTheOpenViewWithIt checks that a view holding one task's
// data does not keep it under another task's name.
func TestChangingTaskBringsTheOpenViewWithIt(t *testing.T) {
	second := otherTask()
	model := sized(dashboard(newFakeBackend(), liveTask(), second), 120, 32)

	review := press(t, model, "v")
	if review.review.task != liveTask().ID {
		t.Fatalf("review opened on %s, want the first task", review.review.task)
	}

	moved := press(t, review, "shift+down")
	if moved.review.task != second.ID {
		t.Errorf("after moving task the review still holds %s, want %s", moved.review.task, second.ID)
	}
}

// TestAViewThatCannotOpenFallsBackRatherThanSticking checks the other way the
// tab cycle could stop: review and runtime both refuse a draft, and a refusal
// that left the screen alone would strand the user where they were.
func TestAViewThatCannotOpenFallsBackRatherThanSticking(t *testing.T) {
	draft := liveTask()
	draft.Workflow = "draft"

	model := sized(dashboard(newFakeBackend(), draft), 120, 32)
	review := press(t, press(t, press(t, model, "tab"), "tab"), "tab")

	if review.activeTab() == tabReview {
		t.Errorf("review opened for a draft task")
	}
	if review.activeTab() != tabDetail {
		t.Errorf("a refused view left the dashboard on %v, want detail", review.activeTab())
	}
}

// TestADialogHoldsTheFrameKeys checks that a dialog is answered before the tab
// or the task underneath it can change, so that what the answer applies to
// cannot move out from under it.
func TestADialogHoldsTheFrameKeys(t *testing.T) {
	model := sized(dashboard(newFakeBackend(), liveTask(), otherTask()), 120, 32)
	opened := press(t, model, "?")

	if moved := press(t, opened, "shift+down"); moved.cursor != opened.cursor {
		t.Errorf("the task changed behind a dialog: %d became %d", opened.cursor, moved.cursor)
	}
	if moved := press(t, opened, "tab"); moved.activeTab() != opened.activeTab() {
		t.Errorf("the view changed behind a dialog: %v became %v", opened.activeTab(), moved.activeTab())
	}
}

// TestTheFrameKeysSurviveTruncation checks that the keys with no other route to
// discovery are the ones the footer keeps.
//
// Review carries eleven hints of its own and the footer is one line, so
// something is always cut. What must not be cut is how to change task or view:
// a view's own keys are visible on the view, and these are not.
func TestTheFrameKeysSurviveTruncation(t *testing.T) {
	model := sized(dashboard(newFakeBackend(), liveTask(), otherTask()), 120, 32)
	footer := press(t, model, "v").frameFooter(120)

	for _, want := range []string{"shift+↑↓", "tab", "?"} {
		if !strings.Contains(footer, want) {
			t.Errorf("the review footer lost %q to truncation:\n%s", want, footer)
		}
	}
}
