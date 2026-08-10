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
	if !strings.Contains(view, "shift moves the frame") {
		t.Errorf("the key map is not on screen:\n%s", view)
	}

	closed := press(t, opened, "esc")
	if strings.Contains(closed.View(), "shift moves the frame") {
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
// keys and not two, and one column is forty-one lines against the twenty-seven a
// thirty-two-row terminal leaves. Something is cut there, and it is cut from the
// bottom — so what must be at the top is the rule the rest of the map is an
// application of, and the cut must say it happened rather than end mid-list.
func TestTheNarrowestSupportedWidthKeepsTheRule(t *testing.T) {
	model := sized(dashboard(newFakeBackend(), liveTask(), otherTask()), minimumWidth, 32)
	view := press(t, model, "?").View()

	for _, want := range []string{
		"shift moves the frame",  // the rule
		"next and previous task", // and both halves of it
		"move within the view",
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
	for _, open := range []string{"", "v", "R"} {
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
// The dashboard's meaning applies only where the view has none of its own: `C`
// sends work back on the task panel and cleans a task up everywhere else, and `r`
// compares, refreshes, or looks again depending on where it is pressed. A
// fall-through that took precedence would have replaced all three.
func TestAViewKeepsTheKeysItClaims(t *testing.T) {
	backend := newFakeBackend()
	panel := press(t, sized(reviewScreen(t, backend), 120, 32), "C")

	if panel.screen == screenCleanup {
		t.Error("C on the task panel opened cleanup, want the review decision")
	}
	if len(backend.reviewCalls) == 0 || !strings.HasPrefix(
		backend.reviewCalls[len(backend.reviewCalls)-1], string(api.ReviewRequestChanges)) {
		t.Errorf("C on the task panel asked for %v, want a request for changes", backend.reviewCalls)
	}

	// Runtime claims neither, so both land on the dashboard's meaning.
	runtime := press(t, sized(dashboard(newFakeBackend(), liveTask()), 120, 32), "R")
	if cleanup := press(t, runtime, "C"); cleanup.screen != screenCleanup {
		t.Errorf("C on the runtime view left the screen at %v, want cleanup", cleanup.screen)
	}
}

// TestTheTaskActionsReachEveryView checks the rest of what falling through
// restored.
//
// Attaching to the agent is the one a user reaches for most, and the runtime view
// had no answer for it at all: `a` there did nothing, from a screen whose whole
// subject is the selected task.
func TestTheTaskActionsReachEveryView(t *testing.T) {
	for _, open := range []string{"", "v", "R"} {
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

	want := []tab{tabTask, tabRuntime, tabTerminal}
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
//
// Each spelling is checked rather than one standing for the others, because they
// exist for readers who cannot use each other's: a terminal that eats shifted
// arrows leaves only the letter and the control pair (ADR-046).
func TestTheRailIsReachableFromEveryView(t *testing.T) {
	second := otherTask()

	for _, key := range []string{"J", "shift+down", "ctrl+n"} {
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

	if up := press(t, model, "K"); up.cursor != 1 {
		t.Errorf("K from the first task went to %d, want the last", up.cursor)
	}
	down := press(t, press(t, model, "shift+down"), "shift+down")
	if down.cursor != 0 {
		t.Errorf("shift+down past the last task went to %d, want the first", down.cursor)
	}
}

// TestTheTabBarIsReachableFromEveryView is the rail test's other half.
//
// A view with its own keyboard used to swallow everything it did not recognise,
// which is what stopped `tab` at the review tab. The shifted keys are answered
// by the frame before any view sees them, so the cycle closes from wherever the
// user is (ADR-046).
func TestTheTabBarIsReachableFromEveryView(t *testing.T) {
	for _, open := range []string{"", "tab", "v", "R"} {
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
	for _, open := range []string{"", "tab", "v", "R"} {
		for _, plain := range []string{"j", "k", "h", "l", "up", "down", "left", "right"} {
			model := sized(dashboard(newFakeBackend(), liveTask(), otherTask()), 120, 32)
			if open != "" {
				model = press(t, model, open)
			}

			moved := press(t, model, plain)
			if moved.cursor != model.cursor {
				t.Errorf("%q from %q moved the rail: %d became %d",
					plain, open, model.cursor, moved.cursor)
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
		if moved.cursor != 1 {
			t.Errorf("%q left the narrow fallback's cursor at %d, want 1", plain, moved.cursor)
		}
		if got, _ := moved.current(); got.ID != second.ID {
			t.Errorf("%q selected %s, want %s", plain, got.Key, second.Key)
		}
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
	for i, expected := range []tab{tabTask, tabRuntime, tabTerminal} {
		at = press(t, at, "tab")
		if at.activeTab() != expected {
			t.Fatalf("tab %d on a draft landed on %v, want %v", i+1, at.activeTab(), expected)
		}
	}

	// And each of them says so rather than looking like a task with nothing in
	// it: a draft owns no worktree and no services.
	for _, want := range []string{"draft has nothing to compare", "still a draft"} {
		view := sized(dashboard(newFakeBackend(), draft), 120, 32)
		panel := press(t, press(t, view, "tab"), "tab").View()
		if !strings.Contains(press(t, view, "tab").View(), want) &&
			!strings.Contains(panel, want) {
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

	for _, want := range []string{"J K", "H L", "?"} {
		if !strings.Contains(footer, want) {
			t.Errorf("the review footer lost %q to truncation:\n%s", want, footer)
		}
	}
}
