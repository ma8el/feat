package ui

import (
	"strings"
	"testing"
	"time"

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

// flowed folds a rendered block into one run of words.
//
// A test about what the dashboard says is not a test about where the region it
// is drawn in wrapped the sentence, and the task panel is wrapped to its region.
func flowed(block string) string { return strings.Join(strings.Fields(ansi.Strip(block)), " ") }

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

// TestNoLineWrapsAtTheSupportedWidth is ADR-041's layout rule.
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
		"task",                   // the tab bar
		"7f3a1c2e",               // a task in the rail
		"/srv/worktrees/example", // the footer's worktree
		"type here",              // the footer's hints, for the tab that has focus
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the frame does not carry %q:\n%s", want, view)
		}
	}
}

// TestTheRailFootKeepsItsOrder pins where the machine's resources sit.
//
// Below the tasks and above the warnings, and at the bottom of the rail whatever
// the task list is doing. Both blocks are about the machine rather than about
// the selected task, and neither is something a user goes looking for: they are
// what the eye finds in the same corner every time, which is what evidence 4 of
// ADR-041 was about.
func TestTheRailFootKeepsItsOrder(t *testing.T) {
	model := sized(withResources(dashboard(newFakeBackend(), liveTask()), sampled(), nil), 120, 32)
	updated, _ := model.Update(reconciliationMsg{report: api.Reconciliation{
		Ran: true, NeedsAttention: true, PreviousRunEndedCleanly: true,
		Findings: []api.ReconciliationFinding{{
			Class: "worktree", Status: "missing", Detail: "not on disk",
		}},
	}})

	rail := ansi.Strip(updated.(Model).railView(29))
	task := strings.Index(rail, "7f3a1c2e")
	machine := strings.Index(rail, "cpu")
	warning := strings.Index(rail, "1 warning")

	switch {
	case task < 0 || machine < 0 || warning < 0:
		t.Fatalf("the rail is missing one of the task, the machine, or the warning:\n%s", rail)
	case machine < task:
		t.Errorf("the machine's resources are above the tasks:\n%s", rail)
	case warning < machine:
		t.Errorf("the machine's resources are below the warnings:\n%s", rail)
	}

	// And the pair ends the rail rather than following the tasks down it.
	lines := strings.Split(strings.TrimRight(rail, "\n"), "\n")
	if got := len(lines); got != 29 {
		t.Errorf("the rail is %d lines of the 29 it was given:\n%s", got, rail)
	}
	if last := lines[len(lines)-1]; !strings.Contains(last, "1 warning") {
		t.Errorf("the rail's last line is %q, want the warning count", last)
	}

	// The three parts are ruled apart rather than spaced apart. They are about
	// three different subjects, and blank space between them read as one list
	// that had stopped.
	rule := strings.Repeat("─", railWidth)
	for _, want := range []struct {
		above string
		line  int
	}{
		{"the resources", lineContaining(lines, "cpu") - 1},
		{"the warnings", lineContaining(lines, "1 warning") - 1},
	} {
		if want.line < 0 {
			t.Errorf("%s are not in the rail:\n%s", want.above, rail)
			continue
		}
		if lines[want.line] != rule {
			t.Errorf("nothing rules off %s: %q", want.above, lines[want.line])
		}
	}
}

// lineContaining is which line of a rendered block holds a string.
func lineContaining(lines []string, want string) int {
	for i, line := range lines {
		if strings.Contains(line, want) {
			return i
		}
	}
	return -1
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

// TestTheRailKeepsProjectsInAFixedOrder is ADR-041's evidence 4, one level up
// from the row it was found on.
//
// Projects appeared in the order their first task did, and the list is sorted
// newest-first, so launching a task lifted its whole project over every other
// one. The header a user's eye had learned moved down the rail on the day they
// were busy enough to start something, which is the day its position mattered.
// Ordering by the project's own name means no task can move a project.
func TestTheRailKeepsProjectsInAFixedOrder(t *testing.T) {
	// Newer than anything in "example", which is what used to lift "platform"
	// above it however long both had been registered.
	launched := otherTask()
	launched.ID, launched.Key = "1a2b3c4d-5e6f-7a8b-9c0d-1e2f3a4b5c6d", "1a2b3c4d"
	launched.Title = "Cache the rate limit buckets"
	launched.CreatedAt = dashboardOrigin.Add(2 * time.Hour)
	launched.UpdatedAt = launched.CreatedAt

	model := sized(dashboard(newFakeBackend(), liveTask(), otherTask(), launched), 120, 32)

	var order []string
	for _, group := range groupByProject(model.tasks) {
		order = append(order, group.project)
	}
	if len(order) != 2 || order[0] != "example" || order[1] != "platform" {
		t.Errorf("the newest task reordered the rail's projects: %v", order)
	}

	// Drawn in that order too, and not only grouped in it: the rail, the movement
	// keys, and the narrow fallback all read this one function, so an order it
	// returns that the rail did not draw would move the cursor invisibly.
	rail := ansi.Strip(model.railView(32))
	example, platform := strings.Index(rail, "example"), strings.Index(rail, "platform")
	if example < 0 || platform < 0 || example > platform {
		t.Errorf("the rail draws its projects in another order:\n%s", rail)
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
	// A section heading rather than a hint's wording. This test is about whether
	// the overlay opens over the list and closes again, and pinning it to a phrase
	// that is being edited for its own reasons fails it for something it is not
	// about — which is what "where shift is eaten" did when that line went.
	if !strings.Contains(view, "everything else") {
		t.Errorf("the key map is not on screen:\n%s", view)
	}

	closed := press(t, opened, "esc")
	if strings.Contains(closed.View(), "everything else") {
		t.Errorf("the dialog did not close:\n%s", closed.View())
	}
}

// TestTheKeyMapFitsTheDialogItIsDrawnIn is what the two columns are for.
//
// Six sections of keys is forty-one lines in one column and the dialog on a
// hundred-and-twenty by thirty-two terminal holds twenty-seven, so the map was
// cut from the bottom — where the sections a reader has not memorised are. It
// had been overflowing before ADR-046 added a section; this checks the whole of
// it arrives at every width the three-region layout supports.
func TestTheKeyMapFitsTheDialogItIsDrawnIn(t *testing.T) {
	for _, width := range []int{120, 160} {
		model := sized(dashboard(newFakeBackend(), liveTask(), otherTask()), width, 32)
		view := press(t, model, "?").View()

		if strings.Contains(view, "more lines than fit here") {
			t.Errorf("the key map is cut off at %d cells:\n%s", width, view)
		}
		// The last section is the one an overflow eats first, so it stands for
		// the rest arriving.
		for _, want := range []string{"this list", "follow the Compose logs", "cancel a draft"} {
			if !strings.Contains(view, want) {
				t.Errorf("the key map at %d cells lost %q:\n%s", width, want, view)
			}
		}
	}
}

// TestTheNarrowestSupportedWidthKeepsTheRule records what does not fit, and what
// is protected when something has to go.
//
// At ninety-six cells the dialog is seventy-two wide, which holds one column of
// keys and not two, and one column is thirty-nine lines against the twenty-seven
// a thirty-two-row terminal leaves. Something is cut there, and it is cut from
// the bottom — so what must be at the top is everything that moves, which is the
// section the rest of the map is read against, and the cut must say it happened
// rather than end mid-list.
func TestTheNarrowestSupportedWidthKeepsTheRule(t *testing.T) {
	model := sized(dashboard(newFakeBackend(), liveTask(), otherTask()), minimumWidth, 32)
	view := press(t, model, "?").View()

	for _, want := range []string{
		"next/prev task, view",     // moving the frame
		"move within the view",     // and moving inside one
		"terminal, task panel",     // and going straight to one
		"more lines than fit here", // said, rather than silently ending
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the key map at the narrowest supported width lost %q:\n%s", want, view)
		}
	}
}

// TestTheKeyMapStaysInsideItsColumns pins the measurements the two columns
// depend on.
//
// A description that outgrows the column pushes the right-hand column off the
// dialog, and the map silently falls back to the single column that did not fit.
// The constraint is cheap to break by writing a longer sentence, so it is
// checked rather than commented.
func TestTheKeyMapStaysInsideItsColumns(t *testing.T) {
	// A width of zero cannot hold two columns, so the map renders one and every
	// line is a single entry. It is cut by cell rather than at a separator: one
	// binding holds a double space of its own.
	for _, line := range strings.Split(ansi.Strip(keyMap(0)), "\n") {
		if !strings.HasPrefix(line, "  ") {
			// A heading, which is measured against the block rather than the
			// column, and which the block is wide enough for by construction.
			continue
		}
		key := strings.TrimSpace(ansi.Cut(line, 2, 2+keyColumn))
		description := strings.TrimSpace(ansi.Cut(line, 2+keyColumn, ansi.StringWidth(line)))

		if got := ansi.StringWidth(key); got >= keyColumn {
			t.Errorf("the binding %q is %d cells, and the column is %d", key, got, keyColumn)
		}
		if got := ansi.StringWidth(description); got > keyDescription {
			t.Errorf("%q describes %q in %d cells, and the column allows %d",
				description, key, got, keyDescription)
		}
	}
}

// TestTheOverlaysOpenFromEveryView is the defect the footer was advertising.
//
// The task panel and runtime answered their own keys and returned for the rest,
// so `?` and `!` reached neither — while the frame's hints, which those views
// draw, went on offering `? keys`. A view now falls through to the dashboard for
// everything it does not claim.
func TestTheOverlaysOpenFromEveryView(t *testing.T) {
	for _, open := range []string{"", "T", "R"} {
		for _, overlay := range []struct {
			key  string
			want screen
		}{{"?", screenKeys}, {"!", screenRecovery}} {
			model := sized(dashboard(newFakeBackend(), liveTask(), otherTask()), 120, 32)
			if open != "" {
				model = press(t, model, open)
			}

			opened := press(t, model, overlay.key)
			if opened.screen != overlay.want {
				t.Errorf("%q from %q left the screen at %v, want %v",
					overlay.key, open, opened.screen, overlay.want)
			}
			// And the view underneath is the one that comes back, rather than
			// whatever the dashboard opened on.
			if closed := press(t, opened, "esc"); closed.activeTab() != model.activeTab() {
				t.Errorf("closing %q from %q returned to %v, want %v",
					overlay.key, open, closed.activeTab(), model.activeTab())
			}
		}
	}
}

// TestAViewKeepsTheKeysItClaims is the other half of falling through.
//
// The dashboard's meaning applies only where the view has none of its own: `r`
// compares, refreshes, or looks again depending on where it is pressed. A
// fall-through that took precedence would have replaced all three.
func TestAViewKeepsTheKeysItClaims(t *testing.T) {
	backend := newFakeBackend()
	panel := press(t, sized(reviewScreen(t, backend), 120, 32), "r")

	if len(backend.reviewCalls) == 0 || !strings.HasPrefix(
		backend.reviewCalls[len(backend.reviewCalls)-1], string(api.ReviewObserve)) {
		t.Errorf("r on the task panel asked for %v, want a fresh comparison", backend.reviewCalls)
	}
	if panel.screen != screenTask {
		t.Errorf("r on the task panel left the screen at %v", panel.screen)
	}
}

// TestEveryViewHasOneKeyAndTheSameKind pins the shape of the frame's view keys.
//
// Two of the four views had a key and two did not, and the two that had one did
// not agree on what a key for a view looked like: `R` opened the runtime, `v` and
// `enter` both opened the task panel, and the brief and the terminal could only
// be cycled to with `L`, `H`, or `tab`. So the shifted letter now names the view
// it opens, in all four cases and from all four views — they are the frame's keys,
// like the pair that steps between them, so no view can swallow the key that
// leaves it.
func TestEveryViewHasOneKeyAndTheSameKind(t *testing.T) {
	for _, from := range []string{"", "T", "B", "R"} {
		for key, want := range map[string]screen{
			"A": screenTerminal,
			"T": screenTask,
			"B": screenBrief,
			"R": screenRuntime,
		} {
			model := sized(dashboard(newFakeBackend(), liveTask()), 120, 32)
			if from != "" {
				model = press(t, model, from)
			}
			if opened := press(t, model, key); opened.screen != want {
				t.Errorf("%q from %q left the screen at %v, want %v", key, from, opened.screen, want)
			}
		}
	}
}

// TestEnterAndVNoLongerOpenTheTaskPanel is the other half of the same change.
//
// They were the inconsistency: one view reached by two keys, neither of which
// was the shifted letter every other view now uses. `enter` in particular means
// "confirm" in every dialog the dashboard has, and the frame was the one place it
// did not.
func TestEnterAndVNoLongerOpenTheTaskPanel(t *testing.T) {
	for _, gone := range []string{"enter", "v"} {
		model := sized(dashboard(newFakeBackend(), liveTask()), 120, 32)
		if after := press(t, model, gone); after.screen != screenTerminal {
			t.Errorf("%q still moved the frame to %v", gone, after.screen)
		}
	}
}

// TestEscMovesNothingBetweenViews is the reported defect (ADR-089).
//
// The panel and the brief closed onto the terminal and runtime closed onto the
// panel, so held down from the runtime tab `esc` walked backwards through three
// of the four views and never through the brief. It read as a back button with a
// view missing from it. The frame's keys are the way between views now, and this
// checks all four tabs at once because the complaint was about the pattern
// rather than about any one of them.
//
// A real Escape rather than the three runes press sends, since the point is the
// key a terminal delivers. What esc still does is close an overlay, which
// TestTheOverlaysOpenFromEveryView checks from these same views.
func TestEscMovesNothingBetweenViews(t *testing.T) {
	for key, from := range map[string]screen{
		"A": screenTerminal,
		"T": screenTask,
		"B": screenBrief,
		"R": screenRuntime,
	} {
		backend := newFakeBackend()
		model := press(t, sized(dashboard(backend, liveTask(), otherTask()), 120, 32), key)
		if model.screen != from {
			t.Fatalf("%q opened %v, want %v", key, model.screen, from)
		}

		after := pressTyped(t, model, "esc")
		if after.screen != from {
			t.Errorf("esc on %v moved the screen to %v", from, after.screen)
		}
		if after.activeTab() != model.activeTab() {
			t.Errorf("esc on %v moved the tab bar to %v", from, after.activeTab())
		}
		// And no footer offers it, because a hint naming a key nothing answers is
		// the same defect from the other side.
		if hints := flowed(after.hints()); strings.Contains(hints, "esc") {
			t.Errorf("the footer on %v still offers esc: %s", from, hints)
		}
	}
}

// TestCleanupIsTheOnlyMeaningOfC records the end of an overload.
//
// `C` sent work back on the task panel and cleaned a task up everywhere else,
// which is one key with two meanings and one of them destructive. Requesting
// changes was never used in fifty-one tasks and is gone, so the key means one
// thing wherever it is pressed (ADR-086).
func TestCleanupIsTheOnlyMeaningOfC(t *testing.T) {
	backend := newFakeBackend()

	for what, model := range map[string]Model{
		"the task panel":   sized(reviewScreen(t, backend), 120, 32),
		"the runtime view": press(t, sized(dashboard(newFakeBackend(), liveTask()), 120, 32), "R"),
	} {
		if cleanup := press(t, model, "C"); cleanup.screen != screenCleanup {
			t.Errorf("C on %s left the screen at %v, want cleanup", what, cleanup.screen)
		}
	}
	for _, call := range backend.reviewCalls {
		if strings.HasPrefix(call, "changes") {
			t.Errorf("C still asked the daemon for a decision: %v", backend.reviewCalls)
		}
	}
}

// TestTheTaskActionsReachEveryView checks the rest of what falling through
// restored.
//
// Attaching to the agent is the one a user reaches for most, and the runtime view
// had no answer for it at all: `a` there did nothing, from a screen whose whole
// subject is the selected task.
func TestTheTaskActionsReachEveryView(t *testing.T) {
	for _, open := range []string{"", "T", "R"} {
		backend := newFakeBackend()
		model := sized(dashboard(backend, liveTask()), 120, 32)
		if open != "" {
			model = press(t, model, open)
		}

		press(t, model, "a")
		if len(backend.attached) != 1 {
			t.Errorf("a from %q attached to %v, want the selected task", open, backend.attached)
		}
		if prepared := press(t, model, "n"); prepared.screen != screenPrepare {
			t.Errorf("n from %q left the screen at %v, want preparation", open, prepared.screen)
		}
	}
}

// TestPreparationClosesOntoTheTabItOpenedOver is the second half of the report
// behind ADR-089.
//
// `esc` was removed from the tabs and then closed `prepare a new task` onto the
// terminal anyway, which is the same surprise arriving by another road: a user
// who opened the dialog from the task panel and changed their mind was moved to
// a view they had not asked for, in answer to a key that had just undone the
// only thing they had. Preparation remembered the tab on the way in and went
// home regardless; every other overlay already returned to what was underneath.
func TestPreparationClosesOntoTheTabItOpenedOver(t *testing.T) {
	for key, want := range map[string]screen{
		"A": screenTerminal,
		"T": screenTask,
		"B": screenBrief,
		"R": screenRuntime,
	} {
		backend := newFakeBackend()
		model := press(t, sized(dashboard(backend, liveTask(), otherTask()), 120, 32), key)

		opened := press(t, model, "n")
		if opened.screen != screenPrepare {
			t.Fatalf("n from %q left the screen at %v, want preparation", key, opened.screen)
		}

		// The cancellation preparation reports for `esc` at its first step, which
		// is what the dialog closes with.
		updated, cmd := opened.Update(preparedMsg{})
		closed := applyCommand(t, updated.(Model), cmd)

		if closed.screen != want {
			t.Errorf("cancelling preparation from %q left the screen at %v, want %v",
				key, closed.screen, want)
		}
		if closed.activeTab() != model.activeTab() {
			t.Errorf("cancelling preparation from %q left the tab bar on %v, want %v",
				key, closed.activeTab(), model.activeTab())
		}
	}
}

// TestALaunchOpensTheTerminalOfTheTaskItCreated is the exception the rule keeps.
//
// A launch is a result rather than a movement: the terminal draws the pane of
// the task that was just created, which is what the user asked preparation for
// and is not the tab they left. It goes through the tab's own opener, because
// the selection moves with it — a panel or a runtime view assigned rather than
// opened would draw the new task's name over the previous task's answer.
func TestALaunchOpensTheTerminalOfTheTaskItCreated(t *testing.T) {
	backend := newFakeBackend()
	first := liveTask()
	launched := newerTask()
	model := press(t, sized(dashboard(backend, first), 120, 32), "R")

	updated, cmd := press(t, model, "n").Update(preparedMsg{task: &launched})
	closed := applyCommand(t, updated.(Model), cmd)

	if closed.screen != screenTerminal {
		t.Errorf("a launch from the runtime tab left the screen at %v, want the terminal",
			closed.screen)
	}
	if closed.selected != launched.ID {
		t.Errorf("the selection is %s, want the launched task %s", closed.selected, launched.Key)
	}
}

// TestTheKeyMapAnswersOnlyItsOwnKeys checks that an overlay does not pass keys
// through to a task the user cannot see while reading about how to act on one.
func TestTheKeyMapAnswersOnlyItsOwnKeys(t *testing.T) {
	model := sized(dashboard(newFakeBackend(), liveTask(), otherTask()), 120, 32)
	opened := press(t, model, "?")

	moved := press(t, opened, "down")
	if moved.selected != opened.selected {
		t.Errorf("the selection moved behind the key map: %s became %s",
			opened.selected, moved.selected)
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

// TestEveryTabIsAboutTheSelectedTask is ADR-043.
//
// The overview was the one that was not: a wide cross-task table that never fitted
// the supported width and said the same things the rail and the panel say.
func TestEveryTabIsAboutTheSelectedTask(t *testing.T) {
	model := sized(dashboard(newFakeBackend(), liveTask(), otherTask()), 120, 32)
	bar := model.tabBar(120)

	if strings.Contains(bar, "overview") {
		t.Errorf("the tab bar still offers the overview: %q", bar)
	}
	for _, want := range []string{"terminal", "task", "runtime"} {
		if !strings.Contains(bar, want) {
			t.Errorf("the tab bar is missing %q: %q", want, bar)
		}
	}

	// And nothing draws the eleven-column row it held. Every line of the frame
	// fits, which the table could not manage at any supported width.
	for i, line := range strings.Split(model.View(), "\n") {
		if got := ansi.StringWidth(line); got > 120 {
			t.Errorf("line %d is %d cells: %q", i, got, line)
		}
	}
}

// TestTabMovesTheMainRegion checks the tab bar's key.
func TestTabMovesTheMainRegion(t *testing.T) {
	model := sized(dashboard(newFakeBackend(), liveTask()), 120, 32)

	if model.activeTab() != tabTerminal {
		t.Fatalf("a fresh dashboard opens on %v, want the terminal", model.activeTab())
	}
	if next := press(t, model, "tab"); next.activeTab() != tabTask {
		t.Errorf("tab moved to %v, want the task panel", next.activeTab())
	}
	if back := press(t, model, "shift+tab"); back.activeTab() != tabRuntime {
		t.Errorf("shift+tab moved to %v, want runtime, wrapping at the end", back.activeTab())
	}
}

// TestTheFooterCarriesTheWorktree checks the value that moved into the footer:
// the path a user would otherwise look up and paste.
//
// The machine's figures were beside it until they moved to the foot of the rail,
// where a bar can say what a number cannot. What the footer keeps of them is the
// sentence explaining an absent figure, which is tested with the figure it
// explains.
func TestTheFooterCarriesTheWorktree(t *testing.T) {
	model := sized(withResources(dashboard(newFakeBackend(), liveTask()), sampled(), nil), 160, 32)
	footer := model.frameFooter(160)

	if !strings.Contains(footer, "/srv/worktrees/example/7f3a1c2e/core") {
		t.Errorf("the footer does not carry the selected task's worktree:\n%s", footer)
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
// still. A dialog that covered the footer would take the selected task's
// worktree and the keys off screen exactly when a user is deciding something.
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
	if !strings.Contains(strings.Join(footer, "\n"), "/srv/worktrees/example/7f3a1c2e/core") {
		t.Errorf("the footer lost the selected task's worktree behind a dialog:\n%s",
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
// moved through the tabs and then stopped at the first one with its own keys.
//
// The task panel and runtime answer their own keys and return for everything
// else, so they swallowed the key that was meant to leave them. The cycle has to
// close, including back round to the first view.
func TestTabCyclesPastAViewWithItsOwnKeyboard(t *testing.T) {
	model := sized(dashboard(newFakeBackend(), liveTask()), 120, 32)

	at := model
	for i, expected := range []tab{tabTask, tabBrief, tabRuntime, tabTerminal} {
		at = press(t, at, "tab")
		if at.activeTab() != expected {
			t.Fatalf("tab %d landed on %v, want %v", i+1, at.activeTab(), expected)
		}
	}

	// And backwards, which is the same rule and a different key: a tab that
	// swallowed the one would swallow the other.
	back := model
	for i, expected := range []tab{tabRuntime, tabBrief, tabTask, tabTerminal} {
		back = press(t, back, "shift+tab")
		if back.activeTab() != expected {
			t.Fatalf("shift+tab %d landed on %v, want %v", i+1, back.activeTab(), expected)
		}
	}
}

// TestTheRailIsReachableFromEveryView is the second defect found in use: the
// plain arrows belong to whichever view has the keyboard, so from review there
// was no way to change task at all.
//
// Each spelling is checked rather than one standing for the others, because they
// exist for readers who cannot use each other's: a terminal that eats shifted
// arrows leaves only the letter and the control pair (ADR-046).
func TestTheRailIsReachableFromEveryView(t *testing.T) {
	second := otherTask()

	for _, key := range []string{"J", "shift+down", "ctrl+n"} {
		for _, open := range []string{"", "tab", "T", "R"} {
			model := sized(dashboard(newFakeBackend(), liveTask(), second), 120, 32)
			if open != "" {
				model = press(t, model, open)
			}

			moved := press(t, model, key)
			if got, _ := moved.subject(); got.ID != second.ID {
				t.Errorf("%q from %q selected %s, want %s", key, open, got.Key, second.Key)
			}
		}
	}
}

// TestSelectingATaskWrapsAtBothEnds keeps the rail's own movement closed, as the
// tab cycle is.
func TestSelectingATaskWrapsAtBothEnds(t *testing.T) {
	model := sized(dashboard(newFakeBackend(), liveTask(), otherTask()), 120, 32)

	if up := press(t, model, "K"); up.selected != otherTask().ID {
		t.Errorf("K from the first task selected %s, want the last", up.selected)
	}
	down := press(t, press(t, model, "shift+down"), "shift+down")
	if down.selected != liveTask().ID {
		t.Errorf("shift+down past the last task selected %s, want the first", down.selected)
	}
}

// TestTheTabBarIsReachableFromEveryView is the rail test's other half.
//
// A view with its own keyboard used to swallow everything it did not recognise,
// which is what stopped `tab` at the review tab. The shifted keys are answered
// by the frame before any view sees them, so the cycle closes from wherever the
// user is (ADR-046).
func TestTheTabBarIsReachableFromEveryView(t *testing.T) {
	for _, open := range []string{"", "tab", "T", "R"} {
		model := sized(dashboard(newFakeBackend(), liveTask()), 120, 32)
		if open != "" {
			model = press(t, model, open)
		}
		at := model.activeTab()

		if next := press(t, model, "L"); next.activeTab() != nextTab(at, 1) {
			t.Errorf("L from %q landed on %v, want %v", open, next.activeTab(), nextTab(at, 1))
		}
		if back := press(t, model, "H"); back.activeTab() != nextTab(at, -1) {
			t.Errorf("H from %q landed on %v, want %v", open, back.activeTab(), nextTab(at, -1))
		}
		if next := press(t, model, "shift+right"); next.activeTab() != nextTab(at, 1) {
			t.Errorf("shift+right from %q landed on %v, want %v",
				open, next.activeTab(), nextTab(at, 1))
		}
		if back := press(t, model, "shift+left"); back.activeTab() != nextTab(at, -1) {
			t.Errorf("shift+left from %q landed on %v, want %v",
				open, back.activeTab(), nextTab(at, -1))
		}
	}
}

// TestThePlainKeysNeverMoveTheFrame is the defect this was all for.
//
// The plain arrows moved the rail on the terminal tab and a repository on the
// task panel, so one key meant two things and which one depended on a tab the
// user was not thinking about. They now move within the main region only, and on
// the terminal tab — where an unfocused pane has no cursor of its own — they move
// nothing at all rather than reaching past it to the rail.
func TestThePlainKeysNeverMoveTheFrame(t *testing.T) {
	for _, open := range []string{"", "tab", "T", "R"} {
		for _, plain := range []string{"j", "k", "h", "l", "up", "down", "left", "right"} {
			model := sized(dashboard(newFakeBackend(), liveTask(), otherTask()), 120, 32)
			if open != "" {
				model = press(t, model, open)
			}

			moved := press(t, model, plain)
			if moved.selected != model.selected {
				t.Errorf("%q from %q moved the rail: %s became %s",
					plain, open, model.selected, moved.selected)
			}
			if moved.activeTab() != model.activeTab() {
				t.Errorf("%q from %q moved the tab: %v became %v",
					plain, open, model.activeTab(), moved.activeTab())
			}
		}
	}
}

// TestThePlainKeysMoveWithinTheView is the same rule from the other side: a view
// that has something to move through still moves it.
func TestThePlainKeysMoveWithinTheView(t *testing.T) {
	panel := sized(reviewScreen(t, newFakeBackend()), 120, 32)
	if len(panel.review.status.Repositories) < 2 {
		t.Fatalf("the panel opened on %d repositories, want two to move between",
			len(panel.review.status.Repositories))
	}

	down := press(t, panel, "j")
	if down.review.cursor != 1 {
		t.Errorf("j left the repository cursor at %d, want 1", down.review.cursor)
	}
	if up := press(t, down, "k"); up.review.cursor != 0 {
		t.Errorf("k left the repository cursor at %d, want 0", up.review.cursor)
	}
}

// TestTheNarrowFallbackKeepsThePlainArrows checks the one place the rule reads
// differently because the layout does.
//
// Below the layout's minimum there is no rail: the task list is what the single
// column draws, so it is the main region, and moving within it is what the plain
// keys mean everywhere else.
func TestTheNarrowFallbackKeepsThePlainArrows(t *testing.T) {
	second := otherTask()
	model := sized(dashboard(newFakeBackend(), liveTask(), second), 80, 24)

	if !model.narrow() {
		t.Fatal("an eighty-column terminal was not treated as narrow")
	}
	for _, plain := range []string{"down", "j"} {
		moved := press(t, model, plain)
		if got, _ := moved.subject(); got.ID != second.ID {
			t.Errorf("%q selected %s, want %s", plain, got.Key, second.Key)
		}
	}
}

// TestChangingTaskBringsTheOpenViewWithIt checks that a view holding one task's
// data does not keep it under another task's name.
func TestChangingTaskBringsTheOpenViewWithIt(t *testing.T) {
	second := otherTask()
	model := sized(dashboard(newFakeBackend(), liveTask(), second), 120, 32)

	review := press(t, model, "T")
	if review.review.task != liveTask().ID {
		t.Fatalf("review opened on %s, want the first task", review.review.task)
	}

	moved := press(t, review, "shift+down")
	if moved.review.task != second.ID {
		t.Errorf("after moving task the review still holds %s, want %s", moved.review.task, second.ID)
	}
}

// TestTheTabCycleClosesForADraftToo checks the other way the cycle could stop.
//
// The task panel and runtime both used to refuse a draft, and a tab that
// declines to open is a tab the cycle cannot pass: a user whose only task was a
// draft could reach neither the tab after it nor the one before. They open now
// and say what a draft does not have yet.
func TestTheTabCycleClosesForADraftToo(t *testing.T) {
	draft := liveTask()
	draft.Workflow = "draft"

	model := sized(dashboard(newFakeBackend(), draft), 120, 32)
	at := model
	for i, expected := range []tab{tabTask, tabBrief, tabRuntime, tabTerminal} {
		at = press(t, at, "tab")
		if at.activeTab() != expected {
			t.Fatalf("tab %d on a draft landed on %v, want %v", i+1, at.activeTab(), expected)
		}
	}

	// And each of them says so rather than looking like a task with nothing in
	// it: a draft owns no worktree and no services.
	for _, want := range []string{"draft has nothing to compare", "still a draft"} {
		view := sized(dashboard(newFakeBackend(), draft), 120, 32)
		task := press(t, view, "tab").View()
		runtime := press(t, press(t, press(t, view, "tab"), "tab"), "tab").View()
		if !strings.Contains(task, want) && !strings.Contains(runtime, want) {
			t.Errorf("no view said %q for a draft", want)
		}
	}
}

// TestADialogHoldsTheFrameKeys checks that a dialog is answered before the tab
// or the task underneath it can change, so that what the answer applies to
// cannot move out from under it.
func TestADialogHoldsTheFrameKeys(t *testing.T) {
	model := sized(dashboard(newFakeBackend(), liveTask(), otherTask()), 120, 32)
	opened := press(t, model, "?")

	if moved := press(t, opened, "shift+down"); moved.selected != opened.selected {
		t.Errorf("the task changed behind a dialog: %s became %s",
			opened.selected, moved.selected)
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
	footer := press(t, model, "T").frameFooter(120)

	for _, want := range []string{"J K", "H L", "?"} {
		if !strings.Contains(footer, want) {
			t.Errorf("the review footer lost %q to truncation:\n%s", want, footer)
		}
	}
}

// TestTheRegionsAreCardsWithRuledHeaders is what ADR-051 changed about the
// frame.
//
// The rail's heading and the tab bar were the first line of their own content,
// which is what made a heading read as the first entry of the list under it.
// Each region is now a box with a header of its own and a rule between that
// header and what it heads.
func TestTheRegionsAreCardsWithRuledHeaders(t *testing.T) {
	model := sized(dashboard(newFakeBackend(), liveTask(), otherTask()), 120, 32)
	lines := strings.Split(ansi.Strip(model.View()), "\n")

	if !strings.HasPrefix(lines[0], cardTopLeft) || !strings.HasSuffix(lines[0], cardTopRight) {
		t.Errorf("the frame does not open with two rounded boxes: %q", lines[0])
	}
	if !strings.Contains(lines[1], "tasks") || !strings.Contains(lines[1], "terminal") {
		t.Errorf("the two headers are not on the frame's second row: %q", lines[1])
	}
	if strings.Count(lines[2], cardHeaderLeft) != 2 || strings.Count(lines[2], cardHeaderRight) != 2 {
		t.Errorf("both headers are not ruled off from their content: %q", lines[2])
	}
	// And the two boxes are separated rather than sharing an edge, so that they
	// read as two regions.
	if !strings.Contains(lines[0], cardTopRight+strings.Repeat(" ", regionGap)+cardTopLeft) {
		t.Errorf("the two cards are not set apart: %q", lines[0])
	}
}

// TestTheFooterIsRuledOffFromTheRegions is the other half of the same rule. The
// footer is the part of the frame that holds still while the regions change, and
// a line is what says so.
func TestTheFooterIsRuledOffFromTheRegions(t *testing.T) {
	model := sized(dashboard(newFakeBackend(), liveTask()), 120, 32)
	lines := strings.Split(ansi.Strip(model.View()), "\n")

	if got := len(lines); got != 32 {
		t.Fatalf("the frame is %d lines of the terminal's 32:\n%s", got, model.View())
	}
	rule := lines[len(lines)-footerHeight]
	if rule != strings.Repeat(cardHorizontal, 120) {
		t.Errorf("the footer is not ruled off from the regions above it: %q", rule)
	}
	// The rest of the footer is still there, under the rule.
	if !strings.Contains(strings.Join(lines[len(lines)-footerHeight:], "\n"), "J K") {
		t.Errorf("the footer lost its hints:\n%s", strings.Join(lines[len(lines)-footerHeight:], "\n"))
	}
}

// TestTheMainRegionNamesTheTaskItIsAbout checks the header's other half.
//
// Every tab is a view of the selected task, and the rail answers which one by
// moving a marker the eye has to go back to. The main region is where the eye
// already is.
func TestTheMainRegionNamesTheTaskItIsAbout(t *testing.T) {
	model := sized(dashboard(newFakeBackend(), liveTask(), otherTask()), 120, 32)

	header := ansi.Strip(model.mainHeader(80))
	if !strings.Contains(header, liveTask().Key) {
		t.Errorf("the main region's header does not name the selected task: %q", header)
	}

	moved := press(t, model, "J")
	if header := ansi.Strip(moved.mainHeader(80)); !strings.Contains(header, otherTask().Key) {
		t.Errorf("the header did not follow the selection: %q", header)
	}
}

// TestAProjectFoldsAwayAndKeepsSayingWhatItHolds is the marker's promise.
//
// Every project header has drawn a fold marker since the rail was written, on a
// rail where nothing could be folded. Folding one now hides its tasks, and the
// header goes on reporting how many there are and whether any of them wants the
// user — a fold that could hide the one task that stopped would make the rail
// unsafe to fold at all.
func TestAProjectFoldsAwayAndKeepsSayingWhatItHolds(t *testing.T) {
	waiting := liveTask()
	waiting.Attention = "needs_input"
	// Short enough that the rail draws it whole, so that its absence is the
	// entry's absence rather than a truncation.
	waiting.Title = "Export job"

	model := sized(dashboard(newFakeBackend(), waiting, otherTask()), 120, 32)
	if !strings.Contains(ansi.Strip(model.View()), waiting.Key) {
		t.Fatalf("the task is not in the rail before folding:\n%s", model.View())
	}

	folded := press(t, model, " ")
	view := strings.Join(railLines(ansi.Strip(folded.View())), "\n")

	// The entry is gone, which is what folding is for. The key itself survives on
	// the header, because the fold is holding the selection (ADR-052) and that is
	// where it is now reported — once, on the one line the project has left.
	if strings.Contains(view, waiting.Title) {
		t.Errorf("a folded project still lists its tasks:\n%s", view)
	}
	if got := strings.Count(view, waiting.Key); got != 1 {
		t.Errorf("the folded task's key is drawn %d times, want once, on the header:\n%s",
			got, view)
	}
	header := headerLine(strings.Split(view, "\n"), waiting.ProjectID)
	if header == "" {
		t.Fatalf("the folded project is not named at all:\n%s", view)
	}
	if !strings.Contains(header, glyphFolded) {
		t.Errorf("the folded project's marker still points down: %q", header)
	}
	if !strings.Contains(header, badgeNeedsInput) {
		t.Errorf("a folded project hid a task that needs the user: %q", header)
	}
	if !strings.Contains(header, "1") {
		t.Errorf("the folded project does not say how many tasks it holds: %q", header)
	}

	// And the other project is untouched: folding is one project at a time.
	if !strings.Contains(view, otherTask().Key) {
		t.Errorf("folding one project took another's tasks with it:\n%s", view)
	}

	if again := press(t, folded, " "); !strings.Contains(ansi.Strip(again.View()), waiting.Key) {
		t.Errorf("the project did not unfold:\n%s", again.View())
	}
}

// railLines is the rail's half of a rendered frame, so that a test about the
// task list is not answered by the main region beside it or by the footer under
// it — both of which name tasks of their own.
func railLines(view string) []string {
	lines := strings.Split(view, "\n")
	if len(lines) > footerHeight {
		lines = lines[:len(lines)-footerHeight]
	}
	rail := make([]string, 0, len(lines))
	for _, line := range lines {
		rail = append(rail, ansi.Cut(line, 0, railWidth+cardChrome))
	}
	return rail
}

// headerLine is the rail line naming a project.
func headerLine(lines []string, project string) string {
	for _, line := range lines {
		if strings.Contains(line, project) {
			return line
		}
	}
	return ""
}

// TestFoldingKeepsTheSelectionOnTheProjectItFolded is the other half of what
// makes space one control rather than two.
//
// Folding used to move the cursor to the next task the rail still listed, which
// took the user's selection away as the price of reading less about other
// projects — and left the fold with no cursor position to press space on again
// (ADR-052).
func TestFoldingKeepsTheSelectionOnTheProjectItFolded(t *testing.T) {
	first := liveTask()
	model := sized(dashboard(newFakeBackend(), first, otherTask()), 120, 32)

	folded := press(t, model, " ")
	current, ok := folded.subject()
	if !ok {
		t.Fatal("folding left no task selected at all")
	}
	if current.ID != first.ID {
		t.Errorf("folding moved the selection to %s, want the task it was on, %s",
			current.Key, first.Key)
	}
	// The open view is left alone too: nothing was selected by folding, so
	// nothing it was showing moves.
	if folded.selected != model.selected {
		t.Errorf("folding changed the open view to %q, want %q", folded.selected, model.selected)
	}

	// And the header holding it says so, because the entry that used to is gone.
	view := railLines(ansi.Strip(folded.View()))
	if header := headerLine(view, first.ProjectID); !strings.Contains(header, first.Key) {
		t.Errorf("the fold holding the selection does not name it: %q", header)
	}
}

// TestAFoldedProjectIsOneCursorStop is the reported defect: a project could be
// folded and then never opened again.
//
// Folded projects were stepped over entirely, so no key put the cursor back on
// one, and space acts on the project the cursor is in. A fold is now a single
// stop — one for the whole project, not one per hidden task — which is both what
// makes it reachable and what keeps it cheap to move past.
func TestAFoldedProjectIsOneCursorStop(t *testing.T) {
	first, sibling, other := liveTask(), siblingTask(), otherTask()
	model := sized(dashboard(newFakeBackend(), first, sibling, other), 120, 32)

	folded := press(t, model, " ")
	// Moving off the fold leaves the project rather than landing on the task
	// hidden beside the selected one.
	down := press(t, folded, "J")
	if got, _ := down.subject(); got.ID != other.ID {
		t.Fatalf("J from a folded project selected %s, want the next project's task %s",
			got.Key, other.Key)
	}

	// And moving back returns to the fold, which is the position that was missing.
	back := press(t, down, "K")
	got, ok := back.subject()
	if !ok || got.ProjectID != first.ProjectID {
		t.Fatalf("K did not return to the folded project: %+v", got)
	}
	if got.ID != first.ID {
		t.Errorf("returning to the fold selected %s, want the task it was holding, %s",
			got.Key, first.Key)
	}

	opened := press(t, back, " ")
	view := strings.Join(railLines(ansi.Strip(opened.View())), "\n")
	for _, task := range []api.Task{first, sibling} {
		if !strings.Contains(view, task.Key) {
			t.Errorf("the project did not open again: %s is still hidden:\n%s", task.Key, view)
		}
	}
}

// siblingTask is a second task in the first task's project, so that a fold holds
// more than one and stepping over it can be told from stepping through it.
func siblingTask() api.Task {
	task := liveTask()
	task.ID = "8a11bc22-1111-2222-3333-444455556666"
	task.Key = "8a11bc22"
	task.Title = "Back-fill last quarter's exports"
	task.Attention = "none"
	return task
}

// TestFoldingEveryProjectKeepsTheSelection is the end of the same rule: there is
// nowhere to move the cursor to, so it stays and its project header says where
// it is.
func TestFoldingEveryProjectKeepsTheSelection(t *testing.T) {
	model := sized(dashboard(newFakeBackend(), liveTask(), otherTask()), 120, 32)

	// One project at a time, moving between them: space folds where the cursor
	// is, and the cursor no longer leaves the project it folded.
	folded := press(t, press(t, press(t, model, " "), "J"), " ")
	if _, ok := folded.subject(); !ok {
		t.Fatal("folding every project left no task selected")
	}
	view := strings.Join(railLines(ansi.Strip(folded.View())), "\n")
	// No entries: the elapsed time is the second line of one, and nothing else
	// in the rail carries it.
	if strings.Contains(view, "1h30m") {
		t.Errorf("a folded rail still lists task entries:\n%s", view)
	}
	for _, project := range []string{liveTask().ProjectID, otherTask().ProjectID} {
		if !strings.Contains(view, project) {
			t.Errorf("the rail lost the project %q entirely:\n%s", project, view)
		}
	}
	// And the fold holding the selection names it, rather than leaving the
	// difference to the header's colour.
	selected, _ := folded.subject()
	if !strings.Contains(headerLine(strings.Split(view, "\n"), selected.ProjectID), selected.Key) {
		t.Errorf("the fold holding the selected task does not name it:\n%s", view)
	}
}

// TestFoldingDoesNotReachBackIntoTheModelItCameFrom checks that the fold is
// carried by value like the rest of the model.
//
// Bubble Tea copies the model on every message. A map shared between the copies
// would make folding a project change models that were already returned, which
// is the sort of thing that stays invisible until something replays a key.
func TestFoldingDoesNotReachBackIntoTheModelItCameFrom(t *testing.T) {
	model := sized(dashboard(newFakeBackend(), liveTask(), otherTask()), 120, 32)

	press(t, model, " ")
	if !strings.Contains(ansi.Strip(model.View()), liveTask().Key) {
		t.Errorf("folding a copy of the model folded the original:\n%s", model.View())
	}
}

// TestTheRailSaysWhenTheListDoesNotFit keeps the machine's figures where they
// are read from.
//
// The rail's foot is read by position — the same corner every time the eye drops
// to it — and a task list longer than the region used to push it off the bottom.
// The list is cut instead, and says so, and names the key that makes room.
func TestTheRailSaysWhenTheListDoesNotFit(t *testing.T) {
	tasks := make([]api.Task, 0, 8)
	for i := range 8 {
		task := liveTask()
		task.ID = strings.Repeat(string(rune('a'+i)), 8) + "-0000-0000-0000-000000000000"
		task.Key = strings.Repeat(string(rune('a'+i)), 8)
		tasks = append(tasks, task)
	}

	model := sized(withResources(dashboard(newFakeBackend(), tasks...), sampled(), nil), 120, 24)
	rail := strings.Join(railLines(ansi.Strip(model.View())), "\n")

	if !strings.Contains(rail, "more lines") {
		t.Errorf("a rail that could not draw every task did not say so:\n%s", rail)
	}
	if !strings.Contains(rail, "space folds") {
		t.Errorf("the rail does not name the key that makes room:\n%s", rail)
	}
	for _, figure := range []string{"cpu", "memory", "disk"} {
		if !strings.Contains(rail, figure) {
			t.Errorf("the task list pushed %q off the rail:\n%s", figure, rail)
		}
	}
}

// TestAResumeIsNeverOfferedWithoutItsStop keeps the two halves of one lifecycle
// on screen together.
//
// The footer names what is reachable from where the user is standing rather than
// every key, so it is a judgement each view makes — but resume and stop are one
// pair, and a view that named only the resume shipped that way: `t` was bound,
// worked, and appeared in the `?` overlay, and no footer said it existed.
// Reported by the maintainer on the first run of the new commands.
//
// The rule is symmetry rather than presence. A view is free to name neither.
func TestAResumeIsNeverOfferedWithoutItsStop(t *testing.T) {
	backend := newFakeBackend()

	for name, screen := range map[string]func() Model{
		"the whole dashboard": func() Model {
			return sized(dashboard(backend, liveTask()), 200, 40)
		},
		"a terminal too narrow for three regions": func() Model {
			return sized(dashboard(backend, liveTask()), 70, 30)
		},
		"the task panel": func() Model {
			return press(t, sized(dashboard(backend, liveTask()), 200, 40), "T")
		},
	} {
		view := screen().View()
		resume := strings.Contains(view, "z resume")
		stop := strings.Contains(view, "t stop")
		if resume != stop {
			t.Errorf("%s offers resume=%v and stop=%v; they are one pair and the footer "+
				"names both or neither:\n%s", name, resume, stop, view)
		}
	}
}
