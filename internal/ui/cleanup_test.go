package ui

import (
	"errors"
	"fmt"
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
					Identity:   "/state/feat/worktrees/example/7f3a1c2e/api",
					Repository: "api",
					Detail:     "the task worktree of api",
					Present:    true,
					Warnings:   []string{"the worktree has uncommitted or untracked changes"},
				}},
				Warnings: []string{"the worktree has uncommitted or untracked changes"},
			},
		},
	}
}

// openCleanupScreen opens the cleanup dialog over the fixture.
func openCleanupScreen(t *testing.T, backend *fakeBackend) Model {
	t.Helper()
	return openCleanupPlan(t, backend, cleanupFixture())
}

// openCleanupPlan opens the cleanup dialog over a plan.
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

// TestCleanupAsksOneQuestionAndRemovesEverything is ADR-110: one question,
// every risk under it, and a yes removes every class and archives the task.
func TestCleanupAsksOneQuestionAndRemovesEverything(t *testing.T) {
	backend := newFakeBackend()
	model := openCleanupScreen(t, backend)

	if len(backend.cleanupSelections) != 0 {
		t.Fatalf("opening the dialog removed something: %+v", backend.cleanupSelections)
	}
	view := flowed(content(model))
	for _, want := range []string{
		"Clean up and archive task " + liveTask().Key + "? [y/N]",
		"worktrees of api: the worktree has uncommitted or untracked changes (potential data loss)",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the dialog does not carry %q:\n%s", want, content(model))
		}
	}

	_, cmd := model.Update(key("y"))
	runCommands(t, cmd)

	if len(backend.cleanupSelections) != 1 {
		t.Fatalf("the daemon received %d selections, want 1", len(backend.cleanupSelections))
	}
	selection := backend.cleanupSelections[0]
	if selection.Token != "0f1e2d3c" || !selection.Archive {
		t.Errorf("token=%q archive=%v, want the plan's token and an archive", selection.Token, selection.Archive)
	}
	if len(selection.Classes) != 2 || selection.Classes[0].Class != "terminal" || selection.Classes[1].Class != "worktrees" {
		t.Fatalf("classes = %+v, want every class of the plan", selection.Classes)
	}
	// The warnings go back as the plan's own strings, so the daemon can refuse a
	// confirmation that what is true has overtaken (ADR-037).
	if len(selection.Classes[1].ConfirmedWarnings) != 1 {
		t.Errorf("confirmations = %+v, want the warning the user was shown", selection.Classes[1])
	}
}

// TestASafeCleanupAsksOnlyTheQuestion keeps the dialog to one line when nothing
// can be lost.
func TestASafeCleanupAsksOnlyTheQuestion(t *testing.T) {
	plan := cleanupFixture()
	plan.Classes = plan.Classes[:1]
	model := openCleanupPlan(t, newFakeBackend(), plan)

	view := flowed(content(model))
	if !strings.Contains(view, "[y/N]") {
		t.Errorf("the dialog does not ask:\n%s", content(model))
	}
	if strings.Contains(view, "potential data loss") {
		t.Errorf("a safe plan lists a risk:\n%s", content(model))
	}
}

// TestAVolumeIsAlwaysARisk keeps the volumes' standing warning, which no target
// carries, in the question.
func TestAVolumeIsAlwaysARisk(t *testing.T) {
	plan := cleanupFixture()
	plan.Classes = append(plan.Classes, api.CleanupClass{
		Class: "volumes", Title: "volumes",
		Targets:  []api.CleanupTarget{{Identity: "feat-example-7f3a1c2e_db", Present: true}},
		Warnings: []string{"removing a volume discards whatever it holds"},
	})
	model := openCleanupPlan(t, newFakeBackend(), plan)

	if view := flowed(content(model)); !strings.Contains(view, "volumes: removing a volume discards whatever it holds") {
		t.Errorf("the question does not name the volumes:\n%s", content(model))
	}
}

// TestAnythingButYesRemovesNothing is the default of [y/N].
func TestAnythingButYesRemovesNothing(t *testing.T) {
	for _, pressed := range []string{"n", "enter", "esc", " "} {
		backend := newFakeBackend()
		model := openCleanupScreen(t, backend)

		updated, cmd := model.Update(key(pressed))
		model = updated.(Model)
		runCommands(t, cmd)

		if len(backend.cleanupSelections) != 0 {
			t.Errorf("%q removed something", pressed)
		}
		if model.screen == screenCleanup {
			t.Errorf("%q left the dialog open", pressed)
		}
		if model.status != "nothing was removed" {
			t.Errorf("%q: status = %q, want it to say nothing was removed", pressed, model.status)
		}
	}
}

// TestAPlanWithProblemsAsksNothing keeps a task with a path Feat refuses to
// touch from being archived over it.
func TestAPlanWithProblemsAsksNothing(t *testing.T) {
	plan := cleanupFixture()
	plan.Archivable = false
	plan.Problems = []string{"the recorded worktree /etc is outside the worktree root"}
	backend := newFakeBackend()
	model := openCleanupPlan(t, backend, plan)

	view := flowed(content(model))
	if !strings.Contains(view, "outside the worktree root") {
		t.Errorf("the problem is not on the screen:\n%s", content(model))
	}
	if strings.Contains(view, "[y/N]") {
		t.Errorf("the dialog asks a question it cannot act on:\n%s", content(model))
	}

	_, cmd := model.Update(key("y"))
	runCommands(t, cmd)
	if len(backend.cleanupSelections) != 0 {
		t.Error("y removed something from a plan that cannot be archived")
	}
}

// TestAFinishedCleanupClosesTheDialog closes the dialog once the task is
// archived, because the daemon will not resolve an archived task again.
func TestAFinishedCleanupClosesTheDialog(t *testing.T) {
	backend := newFakeBackend()
	model := openCleanupScreen(t, backend)

	updated, cmd := model.Update(key("y"))
	model = updated.(Model)
	runCommands(t, cmd)

	updated, cmd = model.Update(cleanupDoneMsg{status: api.CleanupStatus{
		Removed:  []api.CleanupRemoval{{Class: "terminal", Identity: "@3", Removed: true}},
		Archived: true,
	}})
	model = updated.(Model)
	runCommands(t, cmd)

	if model.screen == screenCleanup {
		t.Fatalf("the dialog stayed open after the cleanup it was opened for:\n%s", model.View())
	}
	if !strings.Contains(model.status, "removed the terminal") || !strings.Contains(model.status, "archived") {
		t.Errorf("status = %q, want it to say what was removed and that the task is archived", model.status)
	}
}

// TestASummaryCountsWhatWasAlreadyGoneAndSaysWhenATaskIsArchived covers the two
// things a one-line report still has to carry.
func TestASummaryCountsWhatWasAlreadyGoneAndSaysWhenATaskIsArchived(t *testing.T) {
	backend := newFakeBackend()
	model := openCleanupScreen(t, backend)

	for _, want := range []struct {
		status  api.CleanupStatus
		wanted  []string
		refused []string
	}{{
		// A resource that was already gone is not a failure — the user asked for
		// it to be absent and it is — but a cleanup that removed nothing because
		// everything had gone is a different morning from one that removed two.
		status: api.CleanupStatus{Removed: []api.CleanupRemoval{
			{Class: "terminal", Identity: "@3", Removed: true},
			{Class: "worktrees", Identity: "/a", Removed: false},
		}},
		wanted: []string{"removed the terminal", "1 resource was already gone"},
	}, {
		status: api.CleanupStatus{Removed: []api.CleanupRemoval{
			{Class: "terminal", Identity: "@3", Removed: false},
		}},
		wanted:  []string{"1 resource was already gone", "nothing was removed"},
		refused: []string{"removed the"},
	}, {
		status: api.CleanupStatus{
			Removed:  []api.CleanupRemoval{{Class: "terminal", Identity: "@3", Removed: true}},
			Archived: true,
		},
		wanted: []string{"removed the terminal", "stopped tracking it"},
	}} {
		summary := model.cleanup.summary(want.status)
		for _, fragment := range want.wanted {
			if !strings.Contains(summary, fragment) {
				t.Errorf("summary = %q, want it to carry %q", summary, fragment)
			}
		}
		for _, fragment := range want.refused {
			if strings.Contains(summary, fragment) {
				t.Errorf("summary = %q, want it not to carry %q", summary, fragment)
			}
		}
	}
}

// failCleanup answers the question with y, lets the removal fail with the
// backend's error, and answers the resolve that failure fires.
func failCleanup(t *testing.T, model Model, backend *fakeBackend) Model {
	t.Helper()

	updated, cmd := model.Update(key("y"))
	model = updated.(Model)
	if !model.cleanup.removing {
		t.Fatal("y did not send the removal")
	}
	runCommands(t, cmd)

	updated, cmd = model.Update(cleanupDoneMsg{err: backend.cleanupErr})
	model = updated.(Model)
	runCommands(t, cmd)
	updated, _ = model.Update(cleanupPlanMsg{plan: backend.cleanupPlan})
	return updated.(Model)
}

// TestAFailedCleanupAsksAgainAboutWhatIsLeft covers a removal that stopped
// halfway and one the daemon refused because a warning appeared after the
// question. Both re-resolve and ask again, with the failure beside the question.
func TestAFailedCleanupAsksAgainAboutWhatIsLeft(t *testing.T) {
	backend := newFakeBackend()
	backend.cleanupErr = errors.New("removing the worktrees of task 7f3a1c2e: the worktree is locked")
	model := openCleanupScreen(t, backend)

	before := len(backend.cleanupCalls)
	model = failCleanup(t, model, backend)

	if model.screen != screenCleanup {
		t.Fatal("a failed cleanup closed the dialog that explains it")
	}
	if len(backend.cleanupCalls) != before+1 {
		t.Errorf("the plan was read %d times, want %d", len(backend.cleanupCalls), before+1)
	}
	view := flowed(content(model))
	for _, want := range []string{"the worktree is locked", "[y/N]"} {
		if !strings.Contains(view, want) {
			t.Errorf("the dialog does not carry %q:\n%s", want, content(model))
		}
	}

	// Asking again clears the account of the attempt it supersedes.
	updated, _ := model.Update(key("y"))
	model = updated.(Model)
	if !model.cleanup.removing {
		t.Fatal("y did not send the second removal")
	}
	if strings.Contains(flowed(content(model)), "the worktree is locked") {
		t.Errorf("the previous failure is drawn over the removal that supersedes it:\n%s", content(model))
	}
}

// TestAResolveThatFailedIsDrawnBesideTheRemovalThatDid keeps the two apart.
func TestAResolveThatFailedIsDrawnBesideTheRemovalThatDid(t *testing.T) {
	backend := newFakeBackend()
	backend.cleanupErr = errors.New("removing the worktrees of task 7f3a1c2e: the worktree is locked")
	model := openCleanupScreen(t, backend)

	updated, cmd := model.Update(key("y"))
	model = updated.(Model)
	runCommands(t, cmd)
	updated, cmd = model.Update(cleanupDoneMsg{err: backend.cleanupErr})
	model = updated.(Model)
	runCommands(t, cmd)
	updated, _ = model.Update(cleanupPlanMsg{err: errors.New("no feat daemon is listening")})
	model = updated.(Model)

	body := flowed(content(model))
	for _, want := range []string{"the worktree is locked", "no feat daemon is listening"} {
		if !strings.Contains(body, want) {
			t.Errorf("the dialog lost %q:\n%s", want, content(model))
		}
	}
	if strings.Contains(body, "[y/N]") {
		t.Errorf("the dialog asks about a plan it could not read:\n%s", content(model))
	}
}

// TestTheDialogSaysWhatWentWrongRatherThanThatSomethingDid strips the wire's
// classification, because the cause is at the end of the daemon's sentence. The
// worktree path keeps the task identifier, since rewriting it names no real path.
func TestTheDialogSaysWhatWentWrongRatherThanThatSomethingDid(t *testing.T) {
	backend := newFakeBackend()
	task := liveTask()
	worktree := "/state/feat/worktrees/example/" + task.ID + "/api"
	backend.cleanupErr = fmt.Errorf(
		"%w: removing the worktrees of task %s: removing the worktree %q of /srv/repos/api: "+
			"`git worktree remove --force %s` failed with exit code 128 in /srv/repos/api: "+
			"fatal: cannot remove a locked working tree, lock reason: held by a demo",
		api.ErrInvalid, task.ID, worktree, worktree)

	model := failCleanup(t, sized(openCleanupScreen(t, backend), 120, 32), backend)

	body := flowed(content(model))
	if !strings.Contains(body, "cannot remove a locked working tree") {
		t.Errorf("the end of the message did not survive:\n%s", content(model))
	}
	if strings.Contains(body, api.ErrInvalid.Error()) {
		t.Errorf("the wire's classification is still leading the line:\n%s", content(model))
	}
	if !strings.Contains(body, worktree) {
		t.Errorf("the worktree path was rewritten:\n%s", content(model))
	}
}

// TestAPlanThatCouldNotBeReadSaysWhyTheSameWay shortens a failed read the way it
// shortens a failed removal.
func TestAPlanThatCouldNotBeReadSaysWhyTheSameWay(t *testing.T) {
	backend := newFakeBackend()
	task := liveTask()
	backend.cleanupPlanErr = fmt.Errorf(
		"%w: task %s cannot be resolved: reading the worktrees of repository api: "+
			"git worktree list: fatal: not a git repository: '/srv/repos/api'",
		api.ErrInvalid, task.ID)

	model := sized(dashboard(backend, task), 120, 32)
	updated, cmd := model.Update(key("C"))
	model = updated.(Model)
	runCommands(t, cmd)
	updated, _ = model.Update(cleanupPlanMsg{err: backend.cleanupPlanErr})
	model = updated.(Model)

	body := flowed(content(model))
	if !strings.Contains(body, "not a git repository") {
		t.Errorf("the end of the message is cut:\n%s", content(model))
	}
	if strings.Contains(body, api.ErrInvalid.Error()) {
		t.Errorf("the wire's classification is still leading the line:\n%s", content(model))
	}
}

// TestTheDashboardShowsWhatRecoveryFound is the recovery band. A pass in which
// everything matched its record is not news, so the band appears only when
// something needs the user.
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
// An orphan whose task record is gone has no panel to appear on, and a pass
// that could not ask a question at all is not about any one task, so both would
// otherwise be findings shown nowhere. The footer is too small: a finding is
// three lines, and several of them is a list rather than a line.
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
// Placed after the tasks it would move whenever one was added, and a marker
// that appears only when something is wrong should appear in the same place
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

// TestLookingAgainKeepsTheOverlayOpen is what its own hint promises. The key
// says "refresh", and a key that closed the view would land the answer
// somewhere the user was no longer looking.
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
// produced. The band described the pass that ran when the daemon started and
// nothing in the dashboard could run another, because the periodic refresh and
// the refresh key both re-read the last one: a user who resumed a task or
// cleaned one up went on being told about resources they had just dealt with,
// and only restarting the daemon cleared it.
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
	if !strings.Contains(view, "r to refresh") {
		t.Errorf("recovery does not say how to bring it up to date:\n%s", view)
	}
}

// TestResumingIsAKeyTheUserPresses is what keeps recovery an offer. The
// assertion that matters is the one about everything else: no automatic path,
// no refresh, and no event reaches a resume.
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
// dashboard that interrupts a turn. A stop is reversible and destroys nothing,
// so it does not get cleanup's per-class confirmation; what it gets is a
// question when there is something to interrupt, because a key is easier to hit
// by accident than a typed command.
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

// TestStoppingAnIdleAgentAsksNothing is the other half of that rule. A question
// with an obvious answer teaches people to answer without reading it, and an
// agent that is not mid-turn has nothing a stop would interrupt.
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
