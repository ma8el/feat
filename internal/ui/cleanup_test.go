package ui

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/ma8el/feat/internal/api"
)

// cleanupFixture is a plan with one safe class and one that would lose work.
func cleanupFixture() api.CleanupPlan {
	return api.CleanupPlan{
		TaskID:     liveTask().ID,
		TaskKey:    liveTask().Key,
		ProjectID:  "example",
		Workflow:   "approved",
		Token:      "0f1e2d3c",
		ResolvedAt: time.Date(2026, 8, 15, 14, 9, 3, 0, time.UTC),
		Archivable: true,
		Classes: []api.CleanupClass{
			{
				Class: "terminal", Title: "terminal",
				Targets: []api.CleanupTarget{{Identity: "@3", Detail: "the task's tmux window", Present: true}},
			},
			{
				Class: "worktrees", Title: "worktrees",
				Targets: []api.CleanupTarget{{
					Identity: "/state/feat/worktrees/example/7f3a1c2e/api",
					Detail:   "the task worktree of api",
					Present:  true,
					Warnings: []string{"the worktree has uncommitted or untracked changes"},
				}},
				Warnings: []string{"the worktree has uncommitted or untracked changes"},
			},
		},
	}
}

// openCleanupScreen opens the cleanup screen over the fixture.
func openCleanupScreen(t *testing.T, backend *fakeBackend) Model {
	t.Helper()
	return openCleanupPlan(t, backend, cleanupFixture())
}

// requestCleanup presses enter and answers the resolve it fires.
//
// Enter asks the daemon what the task owns before it asks the user anything, so
// the confirmation appears only once a plan has come back. The plan given here is
// what comes back.
func requestCleanup(t *testing.T, model Model, plan api.CleanupPlan) Model {
	t.Helper()

	updated, cmd := model.Update(key("enter"))
	model = updated.(Model)
	if !model.cleanup.pending {
		t.Fatal("enter did not ask what the task owns before asking the user")
	}
	runCommands(t, cmd)
	updated, _ = model.Update(cleanupPlanMsg{plan: plan})
	return updated.(Model)
}

// openCleanupPlan opens the cleanup screen over a plan.
func openCleanupPlan(t *testing.T, backend *fakeBackend, plan api.CleanupPlan) Model {
	t.Helper()

	backend.cleanupPlan = plan
	model := dashboard(backend, liveTask())

	updated, cmd := model.Update(key("C"))
	model = updated.(Model)
	if model.screen != screenCleanup {
		t.Fatalf("screen = %v, want the cleanup screen", model.screen)
	}
	runCommands(t, cmd)

	updated, _ = model.Update(cleanupPlanMsg{plan: backend.cleanupPlan})
	return updated.(Model)
}

// TestOpeningCleanupResolvesAndRemovesNothing keeps the screen safe to reach
// with one key press.
func TestOpeningCleanupResolvesAndRemovesNothing(t *testing.T) {
	backend := newFakeBackend()
	model := openCleanupScreen(t, backend)

	if len(backend.cleanupCalls) != 1 {
		t.Errorf("opening the screen resolved %d plans, want 1", len(backend.cleanupCalls))
	}
	if len(backend.cleanupSelections) != 0 {
		t.Errorf("opening the screen removed something: %+v", backend.cleanupSelections)
	}

	view := content(model)
	for _, want := range []string{"terminal", "worktrees", "uncommitted"} {
		if !strings.Contains(view, want) {
			t.Errorf("the screen does not show %q:\n%s", want, view)
		}
	}
}

// TestRemovingIsOneConfirmationCarryingWhatItWouldCost is FR-CLEAN-002 and
// FR-CLEAN-003 at the screen.
//
// Pressing enter with nothing selected removes nothing. Selecting asks nothing —
// a tick is a decision being assembled, and the screen already draws what each
// class would cost beside the resources it is true of. The one question is the
// removal's, and it carries the warnings of everything chosen (ADR-061).
func TestRemovingIsOneConfirmationCarryingWhatItWouldCost(t *testing.T) {
	backend := newFakeBackend()
	model := openCleanupScreen(t, backend)

	// Nothing selected.
	updated, _ := model.Update(key("enter"))
	model = updated.(Model)
	if len(backend.cleanupSelections) != 0 {
		t.Fatal("enter removed something with nothing selected")
	}
	if !strings.Contains(model.status, "select") {
		t.Errorf("status = %q, want it to say there is nothing selected", model.status)
	}

	// Selecting the class that would lose work interrupts nothing.
	updated, _ = model.Update(key("down"))
	model = updated.(Model)
	updated, _ = model.Update(key(" "))
	model = updated.(Model)
	if !model.cleanup.chosen["worktrees"] {
		t.Fatal("space did not select the class under the cursor")
	}
	if model.cleanup.executing || strings.Contains(content(model), "[y/N]") {
		t.Errorf("selecting a class put a question on the screen:\n%s", content(model))
	}

	// Enter asks once, naming what will go and what that costs.
	model = requestCleanup(t, model, backend.cleanupPlan)
	if !model.cleanup.executing {
		t.Fatal("enter removed without a confirmation")
	}
	if len(backend.cleanupSelections) != 0 {
		t.Fatal("the confirmation was skipped")
	}
	view := flowed(content(model))
	for _, want := range []string{"Remove the worktrees", "[y/N]", "uncommitted or untracked"} {
		if !strings.Contains(view, want) {
			t.Errorf("the confirmation does not carry %q:\n%s", want, content(model))
		}
	}

	// Anything other than a yes removes nothing and leaves the selection alone,
	// so a mistyped answer does not cost the user their choices.
	updated, cmd := model.Update(key("n"))
	model = updated.(Model)
	runCommands(t, cmd)
	if len(backend.cleanupSelections) != 0 {
		t.Fatal("declining the confirmation removed something anyway")
	}
	if !model.cleanup.chosen["worktrees"] {
		t.Error("declining the confirmation discarded the selection")
	}

	model = requestCleanup(t, model, backend.cleanupPlan)
	updated, cmd = model.Update(key("y"))
	model = updated.(Model)
	runCommands(t, cmd)

	if len(backend.cleanupSelections) != 1 {
		t.Fatalf("the daemon received %d selections, want 1", len(backend.cleanupSelections))
	}
	selection := backend.cleanupSelections[0]
	if selection.Token != "0f1e2d3c" {
		t.Errorf("token = %q, want the token of the plan the user was shown", selection.Token)
	}
	if len(selection.Classes) != 1 || selection.Classes[0].Class != "worktrees" {
		t.Fatalf("classes = %+v, want only the worktrees", selection.Classes)
	}
	// The warnings still go back as the plan's own strings, so the daemon can
	// refuse a confirmation that what is true has overtaken (ADR-037).
	if len(selection.Classes[0].ConfirmedWarnings) != 1 {
		t.Errorf("confirmations = %+v, want the warning the user was shown", selection.Classes[0])
	}
}

// TestTheConfirmationCollectsTheWarningsOfEverythingChosen keeps a removal of
// several risky classes from putting only one of their costs to the user.
func TestTheConfirmationCollectsTheWarningsOfEverythingChosen(t *testing.T) {
	backend := newFakeBackend()
	plan := cleanupFixture()
	plan.Classes[0].Warnings = []string{"removing a volume discards whatever it holds"}
	plan.Classes[0].Targets[0].Warnings = plan.Classes[0].Warnings

	model := openCleanupPlan(t, backend, plan)
	for _, press := range []string{" ", "down", " "} {
		updated, _ := model.Update(key(press))
		model = updated.(Model)
	}
	model = requestCleanup(t, model, plan)

	view := flowed(content(model))
	for _, want := range []string{"discards whatever it holds", "uncommitted or untracked"} {
		if !strings.Contains(view, want) {
			t.Errorf("the confirmation does not carry %q:\n%s", want, content(model))
		}
	}
}

// TestArchivingIsARowLikeAnyOther is the archive choice reached the way
// everything else on the screen is: down to it, space to tick it.
//
// It had a key of its own, which made it the one checkbox the cursor could not
// land on and a key that did nothing for most of the interaction (ADR-061).
func TestArchivingIsARowLikeAnyOther(t *testing.T) {
	backend := newFakeBackend()
	model := openCleanupScreen(t, backend)

	// It is after the classes, and the cursor reaches it.
	classes := len(model.cleanup.plan.Classes)
	for range classes {
		updated, _ := model.Update(key("down"))
		model = updated.(Model)
	}
	if model.cleanup.cursor != classes {
		t.Fatalf("cursor = %d after %d downs, want the archive row at %d",
			model.cleanup.cursor, classes, classes)
	}
	if updated, _ := model.Update(key("down")); updated.(Model).cleanup.cursor != classes {
		t.Error("the cursor moved past the last row of the screen")
	}
	if !strings.Contains(flowed(content(model)), "> [ ] archive") {
		t.Errorf("the archive row does not show the cursor on it:\n%s", content(model))
	}

	// Space on it says why, while a class is still unselected.
	updated, _ := model.Update(key(" "))
	model = updated.(Model)
	if model.cleanup.archive {
		t.Error("archiving was set while a class was unselected")
	}
	if !strings.Contains(model.status, "every class") {
		t.Errorf("status = %q, want it to say why archiving is not offered", model.status)
	}
	// And the row says it too, so the answer is written where the press happened.
	if !strings.Contains(flowed(content(model)), "select every class") {
		t.Errorf("the screen does not say what archiving is waiting for:\n%s", content(model))
	}

	// Selecting every class makes the same press take.
	for range classes {
		updated, _ = model.Update(key("up"))
		model = updated.(Model)
	}
	for range classes {
		updated, _ = model.Update(key(" "))
		model = updated.(Model)
		updated, _ = model.Update(key("down"))
		model = updated.(Model)
	}
	if !model.cleanup.archivable() {
		t.Fatal("archiving was not offered with every class selected")
	}

	updated, _ = model.Update(key(" "))
	model = updated.(Model)
	if !model.cleanup.archive {
		t.Error("space on the archive row did not set it when it was offered")
	}

	// Deselecting anything takes the archive with it, because it would no
	// longer be removing everything the plan names.
	updated, _ = model.Update(key("up"))
	model = updated.(Model)
	updated, _ = model.Update(key(" "))
	model = updated.(Model)
	if model.cleanup.archive {
		t.Error("deselecting a class left the archive set")
	}
}

// TestTheArchiveRowDoesNotMoveTheInventoryAboveIt is why it is drawn whether or
// not it may be taken.
//
// It sits under the inventory, and the inventory is sized by what the tail
// takes, so a row that appeared when the last class was ticked moved the list
// the user was ticking it in — and moved a cursor stop in and out of existence
// underneath them.
func TestTheArchiveRowDoesNotMoveTheInventoryAboveIt(t *testing.T) {
	backend := newFakeBackend()
	model := openCleanupScreen(t, backend)

	width, _ := model.cleanupInventorySize()
	unselected := drawnLines(model.cleanupTail(width))

	for range len(model.cleanup.plan.Classes) {
		updated, _ := model.Update(key(" "))
		model = updated.(Model)
		updated, _ = model.Update(key("down"))
		model = updated.(Model)
	}
	if !model.cleanup.archivable() {
		t.Fatal("the fixture does not reach an archivable selection")
	}

	if got := drawnLines(model.cleanupTail(width)); got != unselected {
		t.Errorf("the tail is %d lines with everything selected and %d with nothing: "+
			"the inventory above it moves as classes are ticked", got, unselected)
	}
}

// TestTheInventoryOnTheScreenIsTheInventoryTheCommandPrints is FR-CLEAN-001 at
// the dashboard.
//
// The screen drew each target's identity and nothing else, so a worktree said a
// path, a volume said a name beginning with a Compose project, and a tmux window
// said `@3`. Everything that made those readable — the sentence the plan writes
// for each target, and the project and workflow the removal is happening in —
// was in `feat task cleanup` alone, which is to say a user had to leave the
// dashboard to find out what they were about to remove.
func TestTheInventoryOnTheScreenIsTheInventoryTheCommandPrints(t *testing.T) {
	backend := newFakeBackend()
	plan := cleanupFixture()
	plan.Classes[0].Targets[0].Present = false

	model := openCleanupPlan(t, backend, plan)
	view := flowed(content(model))

	for _, want := range []string{
		// What is being cleaned up: a task still working on something is a
		// different decision from an approved one.
		"project example", "approved",
		// And what each target is, beside what it is called.
		"@3", "the task's tmux window",
		"/state/feat/worktrees/example/7f3a1c2e/api", "the task worktree of api",
		// A target that is already gone still says so.
		"(already gone)",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the inventory does not show %q:\n%s", want, view)
		}
	}
}

// TestAWarningIsDrawnBesideTheTargetItIsTrueOf keeps a class of several
// resources from saying only that one of them would lose work.
//
// The class's warnings are the distinct set of its targets', so a class of three
// worktrees with one dirty one carries a single line saying a worktree has
// uncommitted changes. Under the title that is all it says; beside the worktree
// it is true of, it says which.
func TestAWarningIsDrawnBesideTheTargetItIsTrueOf(t *testing.T) {
	backend := newFakeBackend()
	plan := cleanupFixture()
	plan.Classes[1].Targets = append(plan.Classes[1].Targets, api.CleanupTarget{
		Identity: "/state/feat/worktrees/example/7f3a1c2e/web",
		Detail:   "the task worktree of web",
		Present:  true,
	})

	model := openCleanupPlan(t, backend, plan)
	lines := strings.Split(ansi.Strip(content(model)), "\n")

	dirty := lineWith(t, lines, "/7f3a1c2e/api")
	warning := lineWith(t, lines, "! the worktree has uncommitted")
	clean := lineWith(t, lines, "/7f3a1c2e/web")
	if dirty >= warning || warning >= clean {
		t.Errorf("the warning is not beside the worktree it is true of:\n%s",
			strings.Join(lines, "\n"))
	}

	// The title says it too, because the title is what stays visible when the
	// window is scrolled to the foot of a long class.
	if !strings.Contains(flowed(content(model)), "worktrees (would lose work)") {
		t.Errorf("the class title does not say it would lose work:\n%s", content(model))
	}
}

// lineWith is the index of the one line holding a fragment.
func lineWith(t *testing.T, lines []string, want string) int {
	t.Helper()

	found := -1
	for i, line := range lines {
		if strings.Contains(line, want) {
			if found >= 0 {
				t.Fatalf("%q is on more than one line:\n%s", want, strings.Join(lines, "\n"))
			}
			found = i
		}
	}
	if found < 0 {
		t.Fatalf("%q is on no line:\n%s", want, strings.Join(lines, "\n"))
	}
	return found
}

// longCleanupPlan is an inventory taller than any dialog it is drawn in, which
// is what a task with several repositories owns.
func longCleanupPlan(classes int) api.CleanupPlan {
	plan := api.CleanupPlan{
		TaskID: liveTask().ID, TaskKey: liveTask().Key,
		ProjectID: "example", Workflow: "approved", Token: "0f1e2d3c",
	}
	for i := range classes {
		name := "class" + strconv.Itoa(i)
		plan.Classes = append(plan.Classes, api.CleanupClass{
			Class: name, Title: name,
			Targets: []api.CleanupTarget{
				{Identity: name + "/one", Detail: "the first of " + name, Present: true},
				{Identity: name + "/two", Detail: "the second of " + name, Present: true},
			},
		})
	}
	return plan
}

// TestALongInventoryScrollsRatherThanBeingClipped keeps every class reachable on
// a terminal smaller than the plan.
//
// The overlay cut what did not fit and left a note counting the lines it had
// dropped, and nothing moved the window: a task whose inventory was taller than
// the dialog could be read only by running `feat task cleanup`, and a class the
// cursor was on could be selected without ever having been drawn.
func TestALongInventoryScrollsRatherThanBeingClipped(t *testing.T) {
	backend := newFakeBackend()
	backend.cleanupPlan = longCleanupPlan(6)

	model := sized(dashboard(backend, liveTask()), 90, 20)
	updated, cmd := model.Update(key("C"))
	model = updated.(Model)
	runCommands(t, cmd)
	updated, _ = model.Update(cleanupPlanMsg{plan: backend.cleanupPlan})
	model = updated.(Model)

	view := flowed(content(model))
	if !strings.Contains(view, "class0") {
		t.Fatalf("the first class is not drawn:\n%s", content(model))
	}
	if !strings.Contains(view, "lines below") {
		t.Errorf("the screen does not say there is more of the inventory:\n%s", content(model))
	}
	if strings.Contains(view, "class5") {
		t.Fatalf("the whole inventory fits, so this proves nothing:\n%s", content(model))
	}

	// Moving to the last class brings the window with it.
	for range 5 {
		updated, _ = model.Update(key("down"))
		model = updated.(Model)
	}
	view = flowed(content(model))
	if !strings.Contains(view, "class5") || !strings.Contains(view, "the second of class5") {
		t.Errorf("moving the cursor did not bring the class it is on into view:\n%s", content(model))
	}
	if !strings.Contains(view, "lines above") {
		t.Errorf("the screen does not say what it scrolled past:\n%s", content(model))
	}

	// And the page keys move the window without moving the choice, which is what
	// reaches a class whose own targets are more than the region holds.
	cursor := model.cleanup.cursor
	updated, _ = model.Update(key("pgup"))
	model = updated.(Model)
	if model.cleanup.cursor != cursor {
		t.Errorf("cursor = %d, want the page key to leave the choice where it was", model.cleanup.cursor)
	}
	if model.cleanup.scroll == 0 {
		t.Fatal("pgup left the window at the top of an inventory it was at the bottom of")
	}
	if !strings.Contains(flowed(content(model)), "lines below") {
		t.Errorf("paging up did not move the window:\n%s", content(model))
	}
}

// TestTheConfirmationSurvivesATerminalTooSmallForTheInventory is the worst case
// the one-question design has to hold in.
//
// Six risky classes on a terminal at the layout's minimum: the confirmation and
// every warning it collected are more than the region has, and the inventory
// gives up its lines rather than the question giving up its own. A question a
// user cannot read whole is one the answer means nothing about — and the
// inventory it displaced is still counted rather than dropped in silence.
func TestTheConfirmationSurvivesATerminalTooSmallForTheInventory(t *testing.T) {
	backend := newFakeBackend()
	plan := longCleanupPlan(6)
	for i := range plan.Classes {
		plan.Classes[i].Warnings = []string{"removing " + plan.Classes[i].Class + " loses something"}
		plan.Classes[i].Targets[0].Warnings = plan.Classes[i].Warnings
	}
	backend.cleanupPlan = plan

	model := sized(dashboard(backend, liveTask()), 90, 20)
	updated, cmd := model.Update(key("C"))
	model = updated.(Model)
	runCommands(t, cmd)
	updated, _ = model.Update(cleanupPlanMsg{plan: plan})
	model = updated.(Model)

	for range len(plan.Classes) {
		updated, _ = model.Update(key(" "))
		model = updated.(Model)
		updated, _ = model.Update(key("down"))
		model = updated.(Model)
	}
	model = requestCleanup(t, model, plan)

	view := flowed(content(model))
	if !strings.Contains(view, "[y/N]") {
		t.Fatalf("the question did not survive the region:\n%s", content(model))
	}
	for _, class := range plan.Classes {
		if !strings.Contains(view, "removing "+class.Class+" loses something") {
			t.Errorf("the confirmation dropped the warning of %s:\n%s", class.Class, content(model))
		}
	}
	if !strings.Contains(view, "lines above") || !strings.Contains(view, "lines below") {
		t.Errorf("the displaced inventory is not counted:\n%s", content(model))
	}
	// And it stops offering the keys it took. Every key answers the question
	// while it is up, so a note naming pgup is a note about nothing.
	if strings.Contains(view, "pgup to") || strings.Contains(view, "pgdn to") {
		t.Errorf("the scroll note offers a key the confirmation has taken:\n%s", content(model))
	}
	if !strings.Contains(flowed(model.View()), "y remove") {
		t.Errorf("the key map does not answer the question that is up:\n%s", model.View())
	}
}

// TestTheKeyMapSaysWhatEnterActsOnAndFitsSayingIt is the hint line at the width
// it has least of.
//
// Enter takes the whole selection and not the row the cursor is on, and the
// screen has to say which without spending more than a dialog has: this line has
// been truncated before, and a hint cut in half is a key nobody finds.
func TestTheKeyMapSaysWhatEnterActsOnAndFitsSayingIt(t *testing.T) {
	backend := newFakeBackend()
	model := sized(openCleanupScreen(t, backend), minimumWidth, 32)

	hints := ansi.Strip(model.cleanupHints())
	if !strings.Contains(hints, "cleanup selected") {
		t.Errorf("the key map does not say what enter acts on: %q", hints)
	}

	// Every hint intact in the dialog as it is actually drawn, not merely in the
	// line before the box clamps it.
	widest, _ := model.dialogLimits()
	if got := ansi.StringWidth(hints); got > widest-dialogChrome {
		t.Errorf("the key map is %d cells in a dialog of %d: %q",
			got, widest-dialogChrome, hints)
	}
	view := flowed(model.View())
	for _, want := range []string{"space select", "enter cleanup selected", "esc back"} {
		if !strings.Contains(view, want) {
			t.Errorf("%q is not on the screen at %d cells:\n%s", want, minimumWidth, model.View())
		}
	}
}

// TestTheInventorySaysTheMomentItWasTaken is what makes the screen an
// observation rather than a claim about now.
func TestTheInventorySaysTheMomentItWasTaken(t *testing.T) {
	backend := newFakeBackend()
	model := openCleanupScreen(t, backend)

	if !strings.Contains(flowed(content(model)), "resolved "+
		cleanupFixture().ResolvedAt.Local().Format("15:04:05")) {
		t.Errorf("the screen does not say when the inventory was taken:\n%s", content(model))
	}

	// And says so while it is taking another, which enter does before it asks.
	updated, _ := model.Update(key(" "))
	model = updated.(Model)
	updated, _ = model.Update(key("enter"))
	model = updated.(Model)
	if !strings.Contains(flowed(content(model)), "resolving…") {
		t.Errorf("the screen does not say a request is in flight:\n%s", content(model))
	}
}

// TestEnterResolvesBeforeItAsks is the freshness the screen has instead of a
// re-resolve key.
//
// `r` was a key a user had to know to press to find out something they could not
// know they needed. The moment freshness is worth anything is the moment consent
// is given, so that is when Feat looks: enter resolves, and the question is put
// against what came back.
func TestEnterResolvesBeforeItAsks(t *testing.T) {
	backend := newFakeBackend()
	model := openCleanupScreen(t, backend)

	updated, _ := model.Update(key(" "))
	model = updated.(Model)

	before := len(backend.cleanupCalls)
	updated, cmd := model.Update(key("enter"))
	model = updated.(Model)
	if model.cleanup.executing {
		t.Fatal("the question went up before the plan it is about came back")
	}
	runCommands(t, cmd)
	if len(backend.cleanupCalls) != before+1 {
		t.Fatalf("enter made %d requests in total, want %d", len(backend.cleanupCalls), before+1)
	}

	// A tick landing while the resolve is in flight would put a class into the
	// question that the plan under it was never checked for.
	updated, _ = model.Update(key("down"))
	model = updated.(Model)
	updated, _ = model.Update(key(" "))
	model = updated.(Model)
	if model.cleanup.chosen["worktrees"] {
		t.Error("a class was selected while the question was being prepared")
	}

	updated, _ = model.Update(cleanupPlanMsg{plan: backend.cleanupPlan})
	model = updated.(Model)
	if !model.cleanup.executing {
		t.Fatal("the plan came back and no question was asked")
	}
}

// TestACostThatMovedIsInTheQuestionItMoved is the case the token cannot see, and
// the likeliest one to happen.
//
// The token covers what a plan would remove and deliberately not what removing it
// would cost, so that an agent writing a file is not reported as a stale plan
// (ADR-037). But an agent writing a file is exactly what changes under an open
// cleanup screen: a worktree that was clean when it was ticked is dirty by the
// time enter is pressed. Resolving on enter is what puts that warning in front of
// the user instead of in the daemon's refusal.
func TestACostThatMovedIsInTheQuestionItMoved(t *testing.T) {
	backend := newFakeBackend()
	model := openCleanupScreen(t, backend)

	// The same resources, one of which has become dirty since the screen opened.
	// The token is unchanged by construction, because nothing was gained or lost.
	dirtied := cleanupFixture()
	dirtied.ResolvedAt = dirtied.ResolvedAt.Add(time.Minute)
	dirtied.Classes[0].Targets[0].Warnings = []string{"the window has a process still running in it"}
	dirtied.Classes[0].Warnings = dirtied.Classes[0].Targets[0].Warnings
	if dirtied.Token != cleanupFixture().Token {
		t.Fatal("the fixture changed the token, so this proves nothing about the other axis")
	}

	updated, _ := model.Update(key(" "))
	model = updated.(Model)
	model = requestCleanup(t, model, dirtied)

	if !model.cleanup.executing {
		t.Fatal("a cost that moved under the same resources stopped the question")
	}
	view := flowed(content(model))
	if !strings.Contains(view, "still running in it") {
		t.Errorf("the warning that appeared is not in the question:\n%s", content(model))
	}
	if !strings.Contains(model.status, "cost has changed since you looked") {
		t.Errorf("status = %q, want it to say the cost moved", model.status)
	}
}

// TestAChangedResourceSetStopsShortOfTheQuestion keeps a confirmation from
// covering something nobody has read.
//
// A gained or lost resource is a different plan, and the confirmation names
// classes rather than targets — so a class that quietly grew a third worktree
// would be confirmed by a user who had seen two. The inventory is replaced and
// the question waits for another enter, which is the same rule FR-CLEAN-001 makes
// about choosing against a summary.
func TestAChangedResourceSetStopsShortOfTheQuestion(t *testing.T) {
	backend := newFakeBackend()
	model := openCleanupScreen(t, backend)

	gained := cleanupFixture()
	gained.Token = "5c4b3a29"
	gained.ResolvedAt = gained.ResolvedAt.Add(time.Minute)
	gained.Classes[1].Targets = append(gained.Classes[1].Targets, api.CleanupTarget{
		Identity: "/state/feat/worktrees/example/7f3a1c2e/web",
		Detail:   "the task worktree of web",
		Present:  true,
	})

	updated, _ := model.Update(key("down"))
	model = updated.(Model)
	updated, _ = model.Update(key(" "))
	model = updated.(Model)
	model = requestCleanup(t, model, gained)

	if model.cleanup.executing {
		t.Fatal("a plan that gained a resource was confirmed without being read")
	}
	if !strings.Contains(model.status, "press enter again") {
		t.Errorf("status = %q, want it to say what to do about the change", model.status)
	}
	view := flowed(content(model))
	if !strings.Contains(view, "/7f3a1c2e/web") {
		t.Errorf("the resource that appeared is not in the inventory:\n%s", content(model))
	}
	if !strings.Contains(view, "resolved "+gained.ResolvedAt.Local().Format("15:04:05")) {
		t.Errorf("the screen still says the old moment:\n%s", content(model))
	}
	// The selection survives, so pressing enter again is one press and not four.
	if !model.cleanup.chosen["worktrees"] {
		t.Error("the selection was discarded by the change it was warned about")
	}
}

// TestASelectionOutlivedByItsResourcesIsForgotten is the other half of that.
//
// A tick is a choice about a resource, and a resource that has gone takes its
// choice with it: left behind it is a selection the screen cannot draw and the
// daemon would refuse, reported as neither.
func TestASelectionOutlivedByItsResourcesIsForgotten(t *testing.T) {
	backend := newFakeBackend()
	model := openCleanupScreen(t, backend)

	gone := cleanupFixture()
	gone.Token = "5c4b3a29"
	gone.Classes = gone.Classes[:1]

	updated, _ := model.Update(key("down"))
	model = updated.(Model)
	updated, _ = model.Update(key(" "))
	model = updated.(Model)
	model = requestCleanup(t, model, gone)

	if model.cleanup.chosen["worktrees"] {
		t.Error("a class the plan no longer names is still selected")
	}
	if model.cleanup.executing {
		t.Fatal("a question was asked about a selection that is empty")
	}
	if !strings.Contains(model.status, "nothing you selected is still there") {
		t.Errorf("status = %q, want it to say the selection outlived its resources", model.status)
	}
}

// TestOpeningAndCleaningUpAskNoQuestion keeps the confirmation to the key that
// asks for it.
//
// Both resolve plans, and neither is a user pressing enter. A screen that put a
// removal question up because a cleanup had just finished would be asking about
// something nobody requested.
func TestOpeningAndCleaningUpAskNoQuestion(t *testing.T) {
	backend := newFakeBackend()
	model := openCleanupScreen(t, backend)

	if model.cleanup.executing || model.status != "" {
		t.Errorf("opening the screen asked something: executing=%v status=%q",
			model.cleanup.executing, model.status)
	}

	updated, cmd := model.Update(cleanupDoneMsg{status: api.CleanupStatus{}})
	model = updated.(Model)
	runCommands(t, cmd)
	updated, _ = model.Update(cleanupPlanMsg{plan: backend.cleanupPlan})
	model = updated.(Model)

	if model.cleanup.executing {
		t.Error("a finished cleanup put a removal question up")
	}
}

// TestTheDashboardShowsWhatRecoveryFound is the recovery band.
//
// A pass in which everything matched its record is not news, so the band appears
// only when something needs the user.
func TestTheDashboardShowsWhatRecoveryFound(t *testing.T) {
	backend := newFakeBackend()
	model := dashboard(backend, liveTask())

	if strings.Contains(content(model), "recovery") {
		t.Error("the recovery band is shown before any pass has run")
	}

	quiet := api.Reconciliation{Ran: true, PreviousRunEndedCleanly: true}
	updated, _ := model.Update(reconciliationMsg{report: quiet})
	if strings.Contains(content(updated.(Model)), "recovery") {
		t.Error("a pass that found nothing produced a recovery band")
	}

	noisy := api.Reconciliation{
		Ran:            true,
		NeedsAttention: true,
		Findings: []api.ReconciliationFinding{{
			Class: "terminal", Status: "missing",
			TaskID: liveTask().ID, TaskKey: liveTask().Key,
			Detail: "the recorded tmux terminal is gone",
			Action: "resume it from the task panel",
		}},
	}
	updated, _ = model.Update(reconciliationMsg{report: noisy})
	found := updated.(Model)
	found.selected = liveTask().ID

	// A finding that names a task belongs on that task's panel, beside the
	// workflow it contradicts and the keys that act on it.
	panel := found.taskPanel()
	for _, want := range []string{"recovery", "missing", "terminal", "resume it"} {
		if !strings.Contains(panel, want) {
			t.Errorf("the task panel does not show %q:\n%s", want, panel)
		}
	}
}

// TestTheRailCountsWarningsAndTheOverlayHoldsThem is where reconciliation went.
//
// An orphan whose task record is gone has no panel to appear on, and a pass that
// could not ask a question at all is not about any one task. Those would
// otherwise be findings shown nowhere, which is what the removed overview page
// was the only home for. The footer was tried and is too small: a finding is
// three lines, and several of them is a list, not a line.
func TestTheRailCountsWarningsAndTheOverlayHoldsThem(t *testing.T) {
	model := sized(dashboard(newFakeBackend(), liveTask()), 160, 32)

	updated, _ := model.Update(reconciliationMsg{report: api.Reconciliation{
		Ran: true, NeedsAttention: true, PreviousRunEndedCleanly: true,
		Findings: []api.ReconciliationFinding{{
			Class: "container", Status: "orphaned", Identity: "feat-agent-x",
			Detail: "running with no task record",
			Action: "clean it up",
		}},
		Problems: []api.ReconciliationProblem{{Reason: "docker could not be reached"}},
	}})
	found := updated.(Model)

	// The rail says how many and which key, and nothing more: the detail is
	// wider than thirty-two cells and would be truncated into uselessness.
	rail := found.View()
	if !strings.Contains(rail, "2 warnings") || !strings.Contains(rail, "! to see") {
		t.Errorf("the rail does not count the warnings or name the key:\n%s", rail)
	}
	if strings.Contains(rail, "running with no task record") {
		t.Errorf("the rail carries a finding's detail, which does not fit it:\n%s", rail)
	}

	// And the key opens all of it, with the action for each.
	opened := press(t, found, "!")
	view := opened.View()
	for _, want := range []string{
		"orphaned", "feat-agent-x", "running with no task record", "clean it up",
		"unchecked", "docker could not be reached",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the recovery overlay does not carry %q:\n%s", want, view)
		}
	}

	// It is an overlay, so what it is about is still behind it.
	if !strings.Contains(view, liveTask().Key) {
		t.Errorf("the overlay hid the task list:\n%s", view)
	}
	if closed := press(t, opened, "esc"); strings.Contains(closed.View(), "running with no task record") {
		t.Errorf("the overlay did not close:\n%s", closed.View())
	}
}

// TestTheWarningCountSitsAtTheFootOfTheRail keeps it where it was last time.
//
// Placed after the tasks it would move whenever one was added, and a marker that
// only appears when something is wrong should at least appear in the same place
// each time it does.
func TestTheWarningCountSitsAtTheFootOfTheRail(t *testing.T) {
	report := api.Reconciliation{
		Ran: true, NeedsAttention: true, PreviousRunEndedCleanly: true,
		Findings: []api.ReconciliationFinding{{
			Class: "worktree", Status: "missing", Detail: "not on disk",
		}},
	}

	for _, tasks := range [][]api.Task{{liveTask()}, {liveTask(), otherTask()}} {
		model := sized(dashboard(newFakeBackend(), tasks...), 110, 26)
		updated, _ := model.Update(reconciliationMsg{report: report})

		rail := strings.Split(updated.(Model).railView(23), "\n")
		if len(rail) != 23 {
			t.Fatalf("with %d tasks the rail is %d rows, want the region's 23", len(tasks), len(rail))
		}
		if !strings.Contains(rail[22], "1 warning") {
			t.Errorf("with %d tasks the marker is not on the last row:\n%s",
				len(tasks), strings.Join(rail, "\n"))
		}
	}
}

// TestLookingAgainKeepsTheOverlayOpen is what its own hint promises.
//
// The key says "look again", and a key that closed the view was the answer
// arriving somewhere the user was no longer looking.
func TestLookingAgainKeepsTheOverlayOpen(t *testing.T) {
	backend := newFakeBackend()
	backend.reconciliation = api.Reconciliation{
		Ran: true, NeedsAttention: true, PreviousRunEndedCleanly: true,
		Findings: []api.ReconciliationFinding{{
			Class: "worktree", Status: "missing", Detail: "not on disk", Action: "clean it up",
		}},
	}
	model := sized(dashboard(backend, liveTask()), 110, 26)

	opened := press(t, model, "!")
	asked, cmd := opened.Update(key("r"))
	looking := asked.(Model)

	if looking.screen != screenRecovery {
		t.Fatalf("looking again left the overlay for %v", looking.screen)
	}
	if !strings.Contains(looking.View(), "looking again") {
		t.Errorf("the overlay does not say a pass is running:\n%s", looking.View())
	}

	runCommands(t, cmd)
	if backend.reconciled != 1 {
		t.Errorf("looking again ran %d passes, want 1", backend.reconciled)
	}

	// And the answer lands in the overlay the user is still reading.
	done, _ := looking.Update(reconciliationMsg{report: backend.reconciliation})
	settled := done.(Model)
	if settled.screen != screenRecovery {
		t.Errorf("the answer closed the overlay it was asked for in")
	}
	if view := settled.View(); strings.Contains(view, "looking again") ||
		!strings.Contains(view, "clean it up") {
		t.Errorf("the overlay did not settle on the new pass:\n%s", view)
	}
}

// TestASingleWarningIsCountedAsOne keeps the rail's count reading as English.
func TestASingleWarningIsCountedAsOne(t *testing.T) {
	model := sized(dashboard(newFakeBackend(), liveTask()), 160, 32)

	updated, _ := model.Update(reconciliationMsg{report: api.Reconciliation{
		Ran: true, NeedsAttention: true, PreviousRunEndedCleanly: true,
		Findings: []api.ReconciliationFinding{{
			Class: "worktree", Status: "missing", Detail: "not on disk",
		}},
	}})

	view := updated.(Model).View()
	if !strings.Contains(view, "1 warning") || strings.Contains(view, "1 warnings") {
		t.Errorf("one finding is not counted as one warning:\n%s", view)
	}
}

// TestTheRecoveryBandCanBeBroughtUpToDate is the defect using the dashboard
// produced.
//
// The band described the pass that ran when the daemon started, and nothing in
// the dashboard could ever run another: the periodic refresh and the refresh key
// both re-read the last one. So a user who resumed a task or cleaned one up went
// on being told about resources they had just dealt with, and the only way to
// clear the band was to restart the daemon.
//
// Reading and looking again stay different requests — a pass asks the container
// runtime about every task, so the two-second refresh must not run one. What
// changes is that the things which resolve a finding now trigger one.
func TestTheRecoveryBandCanBeBroughtUpToDate(t *testing.T) {
	backend := newFakeBackend()
	backend.reconciliation = api.Reconciliation{
		Ran: true, NeedsAttention: true,
		Findings: []api.ReconciliationFinding{{
			Class: "agent_containers", Status: "inconsistent", TaskKey: "7f3a1c2e",
			Detail: "the agent container exists and is Exited (137)",
			Action: "resume the task to start it again, or clean it up",
		}},
	}
	model := dashboard(backend, liveTask())

	// The periodic refresh reads and does not run a pass, because it fires every
	// couple of seconds for as long as the dashboard is open.
	updated, cmd := model.Update(tickMsg{})
	model = updated.(Model)
	runCommands(t, cmd)
	if backend.reconciled != 0 {
		t.Errorf("the periodic refresh ran %d passes; it must only read", backend.reconciled)
	}

	// The refresh key does run one: a user who pressed it wants what is true now.
	updated, cmd = model.Update(key("r"))
	model = updated.(Model)
	runCommands(t, cmd)
	if backend.reconciled != 1 {
		t.Errorf("the refresh key ran %d passes, want 1", backend.reconciled)
	}

	// And so does the same key from the recovery overlay, which is where the
	// staleness is read: the time on the pass is there to be acted on.
	opened, _ := model.Update(key("!"))
	updated, cmd = opened.(Model).Update(key("r"))
	runCommands(t, cmd)
	if backend.reconciled != 2 {
		t.Errorf("looking again from the overlay ran %d passes, want 2", backend.reconciled)
	}
	if updated.(Model).screen != screenRecovery {
		t.Error("looking again closed the overlay the answer was asked for in")
	}

	// And so does resuming, which is one of the two things that resolves what
	// the band reports.
	_, cmd = model.Update(key("z"))
	runCommands(t, cmd)
	if backend.reconciled != 3 {
		t.Errorf("resuming ran %d passes in total, want 3", backend.reconciled)
	}

	// As does finishing a cleanup.
	after := openCleanupScreen(t, backend)
	before := backend.reconciled
	_, cmd = after.Update(cleanupDoneMsg{status: api.CleanupStatus{}})
	runCommands(t, cmd)
	if backend.reconciled != before+1 {
		t.Errorf("a finished cleanup ran %d passes, want %d", backend.reconciled, before+1)
	}
}

// TestTheRecoveryBandSaysWhenItLooked keeps a stale band visibly stale.
func TestTheRecoveryBandSaysWhenItLooked(t *testing.T) {
	backend := newFakeBackend()
	model := dashboard(backend, liveTask())

	looked := time.Date(2026, 8, 7, 21, 25, 24, 0, time.UTC)
	updated, _ := model.Update(reconciliationMsg{report: api.Reconciliation{
		Ran: true, NeedsAttention: true, FinishedAt: looked,
		Findings: []api.ReconciliationFinding{{
			Class: "terminal", Status: "missing", Detail: "the recorded tmux terminal is gone",
		}},
	}})

	view := press(t, sized(updated.(Model), 200, 32), "!").View()
	if !strings.Contains(view, looked.Local().Format("15:04:05")) {
		t.Errorf("recovery does not say when it looked:\n%s", view)
	}
	if !strings.Contains(view, "look again") {
		t.Errorf("recovery does not say how to bring it up to date:\n%s", view)
	}
}

// TestResumingIsAKeyTheUserPresses is what keeps recovery an offer.
//
// The assertion that matters is the one about everything else: no automatic
// path, no refresh, and no event reaches a resume.
func TestResumingIsAKeyTheUserPresses(t *testing.T) {
	backend := newFakeBackend()
	model := dashboard(backend, liveTask())

	// Everything the dashboard does on its own.
	for _, message := range []any{
		tickMsg{},
		eventMsg{event: api.Event{Kind: api.KindTask}},
		reconciliationMsg{report: api.Reconciliation{Ran: true, NeedsAttention: true}},
	} {
		updated, cmd := model.Update(message)
		model = updated.(Model)
		runCommands(t, cmd)
	}
	if len(backend.resumed) != 0 {
		t.Fatalf("a resume happened without anybody asking: %v", backend.resumed)
	}

	_, cmd := model.Update(key("z"))
	runCommands(t, cmd)

	if len(backend.resumed) != 1 {
		t.Fatalf("pressing the resume key resumed %d sessions, want 1", len(backend.resumed))
	}
}

// TestStoppingAWorkingAgentIsAskedAboutFirst covers the one key on this
// dashboard that interrupts a turn.
//
// A stop is reversible and destroys nothing, so it does not get cleanup's
// per-class confirmation. What it does get is a question when there is something
// to interrupt: the agent of the fixture task is running, and a key is easier to
// hit by accident than a typed command.
func TestStoppingAWorkingAgentIsAskedAboutFirst(t *testing.T) {
	backend := newFakeBackend()
	model := dashboard(backend, liveTask())

	updated, cmd := model.Update(key("t"))
	model = updated.(Model)
	runCommands(t, cmd)

	if len(backend.stopped) != 0 {
		t.Fatalf("a working agent was stopped before anybody answered: %v", backend.stopped)
	}
	if view := sized(model, 200, 32).View(); !strings.Contains(view, "y to confirm") {
		t.Errorf("the dashboard does not say how to answer:\n%s", view)
	}

	// Anything other than a yes leaves the agent alone.
	updated, cmd = model.Update(key("n"))
	model = updated.(Model)
	runCommands(t, cmd)
	if len(backend.stopped) != 0 {
		t.Fatalf("declining the question stopped the agent anyway: %v", backend.stopped)
	}

	updated, cmd = model.Update(key("t"))
	model = updated.(Model)
	runCommands(t, cmd)
	_, cmd = model.Update(key("y"))
	runCommands(t, cmd)

	if len(backend.stopped) != 1 {
		t.Fatalf("confirming stopped %d agents, want 1", len(backend.stopped))
	}
}

// TestStoppingAnIdleAgentAsksNothing is the other half of that rule.
//
// A question with an obvious answer teaches people to answer without reading it,
// and an agent that is not mid-turn has nothing a stop would interrupt.
func TestStoppingAnIdleAgentAsksNothing(t *testing.T) {
	task := liveTask()
	task.Session.Process = "idle"

	backend := newFakeBackend()
	model := dashboard(backend, task)

	_, cmd := model.Update(key("t"))
	runCommands(t, cmd)

	if len(backend.stopped) != 1 {
		t.Fatalf("stopping an idle agent stopped %d, want 1 and no question", len(backend.stopped))
	}
}

// TestStoppingAHostNativeAgentIsRefusedBeforeTheDaemon keeps the dashboard from
// asking the daemon something it can answer itself.
func TestStoppingAHostNativeAgentIsRefusedBeforeTheDaemon(t *testing.T) {
	task := liveTask()
	task.Session.ExecutionMode = "host"
	task.Session.Execution = nil

	backend := newFakeBackend()
	model := dashboard(backend, task)

	updated, cmd := model.Update(key("t"))
	model = updated.(Model)
	runCommands(t, cmd)

	if len(backend.stopped) != 0 {
		t.Fatalf("a host-native agent was sent to the daemon to be stopped: %v", backend.stopped)
	}
	if view := sized(model, 200, 32).View(); !strings.Contains(view, "no container to stop") {
		t.Errorf("the dashboard does not say why nothing happened:\n%s", view)
	}
}
