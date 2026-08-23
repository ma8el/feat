package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/x/ansi"

	"github.com/ma8el/feat/internal/api"
)

// spinning reports whether a rendered block carries a frame of the loading
// indicator.
//
// Any frame, because which one is showing depends on how many ticks a test
// happened to deliver, and that is not what any of these are about.
func spinning(block string) bool {
	stripped := ansi.Strip(block)
	for _, frame := range spinner.MiniDot.Frames {
		if strings.Contains(stripped, frame) {
			return true
		}
	}
	return false
}

// TestOpeningCleanupSaysItIsResolving is the wait a user meets first.
//
// Opening the dialog asks the daemon to walk the task's worktrees, its tmux
// window, and its containers, which takes seconds — and until it comes back
// there is no inventory to draw and nothing else on the screen moves. The line
// that says what is happening carries the indicator, so that the wait reads as
// Feat working rather than as Feat having stopped.
func TestOpeningCleanupSaysItIsResolving(t *testing.T) {
	backend := newFakeBackend()
	backend.cleanupPlan = cleanupFixture()
	model := dashboard(backend, liveTask())

	updated, cmd := model.Update(key("C"))
	model = updated.(Model)

	if !model.activity.running {
		t.Error("the dashboard is waiting for the inventory and says nothing is happening")
	}
	view := content(model)
	if !strings.Contains(flowed(view), "resolving what this task owns") {
		t.Errorf("the screen does not say what it is waiting for:\n%s", view)
	}
	if !spinning(view) {
		t.Errorf("the wait is drawn without an indicator:\n%s", view)
	}

	runCommands(t, cmd)
	updated, _ = model.Update(cleanupPlanMsg{plan: backend.cleanupPlan})
	model = updated.(Model)

	// And it stops. An indicator left running is a dashboard redrawing twelve
	// times a second for the rest of the session, and a claim that Feat is
	// waiting for something it already has.
	if model.activity.running {
		t.Error("the indicator is still running with nothing outstanding")
	}
	if spinning(content(model)) {
		t.Errorf("the resolved inventory is drawn with an indicator on it:\n%s", content(model))
	}
}

// TestARemovalInFlightSaysSoAndTakesTheKeyboard is the longest wait the screen
// has.
//
// The confirmation disappears the moment it is answered, and what replaced it
// was the inventory exactly as it had been: worktrees coming off disk, a tmux
// window being killed, containers and volumes going, and nothing on screen
// saying any of it. Now the line the question was asked on says what is being
// done, and the keys that would resolve the plan again are inert until it
// finishes — a user who thinks nothing is happening presses them.
func TestARemovalInFlightSaysSoAndTakesTheKeyboard(t *testing.T) {
	backend := newFakeBackend()
	// Given a terminal, so that the frame's own footer is drawn as well as the
	// dialog's key map: the two say different things and both are read.
	model := sized(openCleanupScreen(t, backend), 120, 34)

	updated, _ := model.Update(key(" "))
	model = updated.(Model)
	model = requestCleanup(t, model, backend.cleanupPlan)

	updated, cmd := model.Update(key("y"))
	model = updated.(Model)
	runCommands(t, cmd)

	if !model.cleanup.removing || !model.activity.running {
		t.Fatalf("removing=%v running=%v, want a removal the screen is showing",
			model.cleanup.removing, model.activity.running)
	}
	view := content(model)
	if !strings.Contains(flowed(view), "removing what you selected") {
		t.Errorf("the screen does not say a removal is running:\n%s", view)
	}
	if !spinning(view) {
		t.Errorf("the removal is drawn without an indicator:\n%s", view)
	}
	if strings.Contains(flowed(view), "esc back") {
		t.Errorf("the key map offers a way out of a removal it cannot abandon:\n%s", view)
	}

	// Every key but the one that leaves Feat is inert: what is on screen has
	// already been sent, and re-resolving under it would put a second answer
	// against a question nobody asked.
	resolutions := len(backend.cleanupCalls)
	for _, pressed := range []string{"enter", "space", "esc"} {
		updated, cmd = model.Update(key(pressed))
		model = updated.(Model)
		runCommands(t, cmd)
	}
	if len(backend.cleanupCalls) != resolutions {
		t.Errorf("the plan was resolved %d times during the removal, want %d",
			len(backend.cleanupCalls), resolutions)
	}
	if model.screen != screenCleanup {
		t.Error("a key press left the screen that is the account of the removal")
	}
	// And the frame's footer stops offering the escape it would answer at any
	// other moment in the same dialog.
	if footer := flowed(model.View()); strings.Contains(footer, "esc close") {
		t.Errorf("the footer offers a way out of a removal that will not take it:\n%s", model.View())
	}

	updated, _ = model.Update(cleanupDoneMsg{status: api.CleanupStatus{
		Removed: []api.CleanupRemoval{{Class: "worktrees", Identity: "/w", Removed: true}},
	}})
	model = updated.(Model)
	if model.activity.running {
		t.Error("the indicator is still running after the removal finished")
	}
}

// TestAFailedRemovalGoesBackToWaitingForTheNewInventory keeps the two waits
// distinct.
//
// A cleanup that failed halfway re-reads the plan, so the screen is waiting
// again — but for a resolution and not for a removal, and the key map and the
// line under the inventory both have to change back with it.
func TestAFailedRemovalGoesBackToWaitingForTheNewInventory(t *testing.T) {
	backend := newFakeBackend()
	model := openCleanupScreen(t, backend)

	updated, _ := model.Update(key(" "))
	model = updated.(Model)
	model = requestCleanup(t, model, backend.cleanupPlan)
	updated, cmd := model.Update(key("y"))
	model = updated.(Model)
	runCommands(t, cmd)

	updated, cmd = model.Update(cleanupDoneMsg{
		err: errors.New("removing the worktrees of task 7f3a1c2e: the worktree is locked"),
	})
	model = updated.(Model)
	runCommands(t, cmd)

	if model.cleanup.removing {
		t.Error("a removal that ended is still reported as running")
	}
	if !model.cleanup.working || !model.activity.running {
		t.Errorf("working=%v running=%v, want the re-read of the inventory shown",
			model.cleanup.working, model.activity.running)
	}
	if flowed := flowed(content(model)); strings.Contains(flowed, "removing what you selected") {
		t.Errorf("the screen still says it is removing:\n%s", content(model))
	}
}

// TestPreparationShowsTheWaitItAsksTheUserToSit is the other half of the brief.
//
// Confirming a draft creates the branches, the worktrees, and the task terminal,
// which is seconds of a screen that has nothing else to draw. The status line it
// already had is what carries the indicator: the words are the screen's and the
// frame in front of them is the dashboard's.
func TestPreparationShowsTheWaitItAsksTheUserToSit(t *testing.T) {
	model := prepared(t, newFakeBackend())

	confirmed, _ := model.Update(key("enter"))
	if !confirmed.busy {
		t.Fatal("confirming a plan did not put the screen into a wait")
	}

	indicator := newActivity()
	indicator.start()
	view := confirmed.View(indicator)
	if !strings.Contains(ansi.Strip(view), "creating worktrees") {
		t.Errorf("the screen does not say what it is waiting for:\n%s", view)
	}
	if !spinning(view) {
		t.Errorf("the wait is drawn without an indicator:\n%s", view)
	}

	// And the keys it offers are the ones that answer. Only the cancel is
	// accepted while a request is in flight, so a key map still naming four is a
	// key map inviting the presses a still screen already provokes.
	hints := flowed(view)
	if !strings.Contains(hints, "ctrl+c cancel") {
		t.Errorf("the waiting screen does not offer the one key that works:\n%s", view)
	}
	if strings.Contains(hints, "confirm and launch") || strings.Contains(hints, "discard draft") {
		t.Errorf("the waiting screen still offers keys it will not answer:\n%s", view)
	}

	// Stopped, the same screen draws the same words and no frame: the indicator
	// never claims a wait that is over.
	if spinning(confirmed.View(newActivity())) {
		t.Errorf("a screen with nothing outstanding drew an indicator:\n%s",
			confirmed.View(newActivity()))
	}
}

// TestTheDashboardAnimatesOnlyWhileItIsWaiting is the rule the indicator is
// started and stopped by.
//
// It is applied after every message rather than by the screens, so what this
// checks is that a screen reporting a wait is enough to start it and that the
// wait ending is enough to stop it — with no call anywhere in between that a
// screen could forget to make.
func TestTheDashboardAnimatesOnlyWhileItIsWaiting(t *testing.T) {
	model := dashboard(newFakeBackend(), liveTask())
	if model.waiting() || model.activity.running {
		t.Fatal("a dashboard with nothing outstanding is animating")
	}

	model.screen = screenPrepare
	model.prepare.busy = true
	updated, _ := model.Update(tickMsg(dashboardNow))
	model = updated.(Model)
	if !model.activity.running {
		t.Error("a screen that reported a wait did not start the indicator")
	}

	model.prepare.busy = false
	updated, _ = model.Update(tickMsg(dashboardNow))
	model = updated.(Model)
	if model.activity.running {
		t.Error("a wait that ended left the indicator running")
	}
}

// TestOneWaitRunsOneChainOfFrames is what keeps the indicator readable and the
// dashboard idle.
//
// Bubble Tea's ticks cannot be recalled, so a spinner is started once and
// stopped by dropping the tick in flight. Two chains would advance the same
// spinner at twice the rate it is meant to be read at and would go on redrawing
// after the wait ended; the first is a decision taken after every message, so
// starting twice has to be nothing.
func TestOneWaitRunsOneChainOfFrames(t *testing.T) {
	indicator := newActivity()

	start := indicator.start()
	if start == nil {
		t.Fatal("starting the indicator produced no first frame")
	}
	if again := indicator.start(); again != nil {
		t.Error("starting a running indicator began a second chain of frames")
	}

	message, ok := start().(spinner.TickMsg)
	if !ok {
		t.Fatal("the indicator's first frame was not a tick")
	}
	advanced, next := indicator.advance(message)
	if next == nil {
		t.Error("a frame delivered while waiting asked for no successor")
	}

	advanced.stop()
	if _, after := advanced.advance(message); after != nil {
		t.Error("a frame delivered after the wait ended asked for another")
	}
	if advanced.mark("resolving…") != "resolving…" {
		t.Errorf("a stopped indicator marked a line: %q", advanced.mark("resolving…"))
	}
}
