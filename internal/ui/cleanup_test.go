package ui

import (
	"strings"
	"testing"
	"time"

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

// openCleanupScreen opens the cleanup screen over a plan.
func openCleanupScreen(t *testing.T, backend *fakeBackend) Model {
	t.Helper()

	backend.cleanupPlan = cleanupFixture()
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

	view := model.View()
	for _, want := range []string{"terminal", "worktrees", "uncommitted"} {
		if !strings.Contains(view, want) {
			t.Errorf("the screen does not show %q:\n%s", want, view)
		}
	}
}

// TestRemovingNeedsASelectionAndItsConfirmation is FR-CLEAN-002 and
// FR-CLEAN-003 at the screen.
//
// Pressing enter with nothing selected removes nothing; selecting a class whose
// removal would lose work asks again, and declining leaves the class alone.
func TestRemovingNeedsASelectionAndItsConfirmation(t *testing.T) {
	backend := newFakeBackend()
	model := openCleanupScreen(t, backend)

	// Nothing selected.
	updated, _ := model.Update(key("enter"))
	model = updated.(Model)
	if len(backend.cleanupSelections) != 0 {
		t.Fatal("enter removed something with nothing selected")
	}

	// Select the risky class: the screen asks about the warning rather than
	// taking the selection as consent.
	updated, _ = model.Update(key("down"))
	model = updated.(Model)
	updated, _ = model.Update(key(" "))
	model = updated.(Model)
	if model.cleanup.confirming == "" {
		t.Fatal("selecting a class that would lose work asked nothing")
	}
	if !strings.Contains(model.View(), "anyway?") {
		t.Errorf("the confirmation is not on the screen:\n%s", model.View())
	}

	// Declining leaves the class alone.
	updated, _ = model.Update(key("n"))
	model = updated.(Model)
	if model.cleanup.chosen["worktrees"] {
		t.Error("declining the warning left the class selected")
	}

	// Accepting it makes the class removable.
	updated, _ = model.Update(key(" "))
	model = updated.(Model)
	updated, _ = model.Update(key("y"))
	model = updated.(Model)
	if !model.cleanup.accepted["worktrees"] {
		t.Fatal("accepting the warning did not record the confirmation")
	}

	// And removal still asks once more, naming what will go.
	updated, _ = model.Update(key("enter"))
	model = updated.(Model)
	if !model.cleanup.executing {
		t.Fatal("enter removed without a final confirmation")
	}
	if len(backend.cleanupSelections) != 0 {
		t.Fatal("the final confirmation was skipped")
	}

	updated, cmd := model.Update(key("y"))
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
	if len(selection.Classes[0].ConfirmedWarnings) != 1 {
		t.Errorf("confirmations = %+v, want the warning the user accepted", selection.Classes[0])
	}
}

// TestArchivingIsOfferedOnlyWhenEverythingIsSelected keeps the screen from
// offering something the daemon would refuse.
func TestArchivingIsOfferedOnlyWhenEverythingIsSelected(t *testing.T) {
	backend := newFakeBackend()
	model := openCleanupScreen(t, backend)

	updated, _ := model.Update(key(" "))
	model = updated.(Model)
	if model.cleanup.archivable() {
		t.Error("archiving was offered with one class still unselected")
	}
	updated, _ = model.Update(key("A"))
	model = updated.(Model)
	if model.cleanup.archive {
		t.Error("archiving was set while a class was unselected")
	}
	if !strings.Contains(model.status, "every class") {
		t.Errorf("status = %q, want it to say why archiving is not offered", model.status)
	}

	// Selecting the rest, and confirming its warning, makes it available.
	updated, _ = model.Update(key("down"))
	model = updated.(Model)
	updated, _ = model.Update(key(" "))
	model = updated.(Model)
	updated, _ = model.Update(key("y"))
	model = updated.(Model)

	if !model.cleanup.archivable() {
		t.Fatal("archiving was not offered with every class selected")
	}
	updated, _ = model.Update(key("A"))
	model = updated.(Model)
	if !model.cleanup.archive {
		t.Error("archiving was not set when it was offered")
	}

	// Deselecting anything takes the archive with it, because it would no
	// longer be removing everything the plan names.
	updated, _ = model.Update(key(" "))
	model = updated.(Model)
	if model.cleanup.archive {
		t.Error("deselecting a class left the archive set")
	}
}

// TestTheDashboardShowsWhatRecoveryFound is the recovery band.
//
// A pass in which everything matched its record is not news, so the band appears
// only when something needs the user.
func TestTheDashboardShowsWhatRecoveryFound(t *testing.T) {
	backend := newFakeBackend()
	model := dashboard(backend, liveTask())

	if strings.Contains(model.View(), "recovery") {
		t.Error("the recovery band is shown before any pass has run")
	}

	quiet := api.Reconciliation{Ran: true, PreviousRunEndedCleanly: true}
	updated, _ := model.Update(reconciliationMsg{report: quiet})
	if strings.Contains(updated.(Model).View(), "recovery") {
		t.Error("a pass that found nothing produced a recovery band")
	}

	noisy := api.Reconciliation{
		Ran:            true,
		NeedsAttention: true,
		Findings: []api.ReconciliationFinding{{
			Class: "terminal", Status: "missing", TaskKey: "7f3a1c2e",
			Detail: "the recorded tmux terminal is gone",
			Action: "resume it from the task detail",
		}},
	}
	updated, _ = model.Update(reconciliationMsg{report: noisy})
	view := updated.(Model).View()

	for _, want := range []string{"recovery", "missing", "7f3a1c2e", "resume it"} {
		if !strings.Contains(view, want) {
			t.Errorf("the recovery band does not show %q:\n%s", want, view)
		}
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

	// And so does resuming, which is one of the two things that resolves what
	// the band reports.
	_, cmd = model.Update(key("z"))
	runCommands(t, cmd)
	if backend.reconciled != 2 {
		t.Errorf("resuming ran %d passes in total, want 2", backend.reconciled)
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

	view := updated.(Model).View()
	if !strings.Contains(view, looked.Local().Format("15:04:05")) {
		t.Errorf("the band does not say when it looked:\n%s", view)
	}
	if !strings.Contains(view, "look again") {
		t.Errorf("the band does not say how to bring it up to date:\n%s", view)
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
