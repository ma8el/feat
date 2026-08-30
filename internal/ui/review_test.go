package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/ma8el/feat/internal/api"
)

// reviewed is what the daemon returns for the fixture task: two repositories
// with different bases, a gated failure and an agent's claim, and the project's
// own commands expanded per repository.
func reviewed() api.ReviewStatus {
	task := liveTask()
	task.Workflow = "verification_failed"

	return api.ReviewStatus{
		Task: task,
		Review: api.Review{
			Summary: "Added the export job and its retry policy.",
			Gated:   true,
			Checks: []api.ReviewCheck{
				{ID: "unit", RepositoryID: "core", Status: "failed", Reporter: "provider",
					Detail: "exited with status 1\n2 failed, 82 passed"},
				{ID: "lint", RepositoryID: "core", Status: "passed", Reporter: "agent", Detail: "clean"},
			},
		},
		Repositories: []api.ReviewRepository{
			{
				RepositoryID: "core", Access: "read_write",
				Branch:       "feat/7f3a1c2e-add-a-scheduled-export-job",
				WorktreePath: "/srv/worktrees/example/7f3a1c2e/core",
				BaseRef:      "origin/main", BaseCommit: "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d",
				HeadCommit:   "0011223344556677889900aabbccddeeff001122",
				ChangedFiles: 7, Insertions: 214, Deletions: 36, Dirty: true, Ahead: 3,
				SummarizedAt: &dashboardOrigin,
			},
			{
				RepositoryID: "schema", Access: "read_only",
				WorktreePath: "/srv/worktrees/example/7f3a1c2e/schema",
				BaseRef:      "origin/main", BaseCommit: "9f8e7d6c5b4a39281706f5e4d3c2b1a09f8e7d6c",
				SummarizedAt: &dashboardOrigin,
			},
		},
		Commands: []api.ReviewCommand{
			{Kind: "diff", RepositoryID: "core", Program: "git",
				Arguments: []string{"diff", "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d"},
				Directory: "/srv/worktrees/example/7f3a1c2e/core"},
			{Kind: "diff", RepositoryID: "schema", Program: "git",
				Arguments: []string{"diff", "9f8e7d6c5b4a39281706f5e4d3c2b1a09f8e7d6c"},
				Directory: "/srv/worktrees/example/7f3a1c2e/schema"},
			{Kind: "status", RepositoryID: "core", Program: "git",
				Arguments: []string{"status", "--short"},
				Directory: "/srv/worktrees/example/7f3a1c2e/core"},
		},
	}
}

// reviewScreen opens the review screen for the fixture task.
func reviewScreen(t *testing.T, backend *fakeBackend) Model {
	t.Helper()

	backend.reviewStatus = reviewed()
	model := dashboard(backend, backend.reviewStatus.Task)
	return press(t, model, "v")
}

// TestTheTaskPanelGroupsChangesByRepository is FR-REV-001 on the panel.
//
// Every repository is there with its own recorded base, and the base is shown
// rather than implied: a review of a long-running task is only meaningful
// against the commit it started from, and a user who cannot see which commit
// that was has to take Feat's word for it.
func TestTheTaskPanelGroupsChangesByRepository(t *testing.T) {
	model := reviewScreen(t, newFakeBackend())
	view := model.taskPanel()

	for field, want := range map[string]string{
		"the first repository":  "core",
		"the second repository": "schema",
		"its own base":          "1a2b3c4d5e6f",
		"the other base":        "9f8e7d6c5b4a",
		"the head commit":       "0011223344556677889900aabbccddeeff001122"[:12],
		"the changed files":     "7 file(s)",
		"the line counts":       "+214 -36",
		"uncommitted work":      "uncommitted",
		"the worktree":          "/srv/worktrees/example/7f3a1c2e/core",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the task panel does not show %s (%q):\n%s", field, want, view)
		}
	}
}

// TestTheTaskPanelTellsAClaimFromAnEnforcedResult is FR-AGENT-006 where the user
// reads it.
//
// A result Feat ran and a result the agent asserted are both shown, and they do
// not read alike. Showing them alike would tell the user something Feat does not
// know (FR-AGENT-006).
func TestTheTaskPanelTellsAClaimFromAnEnforcedResult(t *testing.T) {
	model := reviewScreen(t, newFakeBackend())
	view := model.taskPanel()

	if !strings.Contains(view, "Feat ran this") {
		t.Errorf("an enforced result is not marked as one:\n%s", view)
	}
	if !strings.Contains(view, "the agent reported this") {
		t.Errorf("a claimed result is not marked as one:\n%s", view)
	}
	if !strings.Contains(view, "2 failed, 82 passed") {
		t.Errorf("the failing check's own output is not shown:\n%s", view)
	}
	// A result Feat ran must not carry the caveat that belongs to a claim.
	if strings.Contains(view, "reported by the agent, not verified") {
		t.Errorf("an enforced result still carries the unverified caveat:\n%s", view)
	}
}

// TestReviewCommandsRunInTheSelectedRepository is FR-REV-002 and FR-REV-003 at
// the screen: the command that opens is the one for the repository under the
// cursor, in that repository's own worktree.
func TestReviewCommandsRunInTheSelectedRepository(t *testing.T) {
	backend := newFakeBackend()
	model := reviewScreen(t, backend)

	model = press(t, model, "d")
	if len(backend.reviewRan) != 1 {
		t.Fatalf("%d commands were run, want the diff of the selected repository", len(backend.reviewRan))
	}
	if got := backend.reviewRan[0]; got.RepositoryID != "core" || got.Kind != "diff" {
		t.Errorf("the diff opened %+v, want the first repository's", got)
	}

	// Down one, and the same key opens the other repository's diff.
	model = press(t, model, "j")
	model = press(t, model, "d")
	if len(backend.reviewRan) != 2 {
		t.Fatalf("%d commands were run, want one per key press", len(backend.reviewRan))
	}
	if got := backend.reviewRan[1]; got.RepositoryID != "schema" {
		t.Errorf("the diff opened %s after moving the cursor, want schema", got.RepositoryID)
	}
	if got := backend.reviewRan[1].Directory; got != "/srv/worktrees/example/7f3a1c2e/schema" {
		t.Errorf("the diff would run in %q, want the selected repository's worktree", got)
	}
	_ = model
}

// TestAnUnconfiguredEditorFallsBackToTheEnvironment is FR-REV-003's default.
//
// The daemon returns no editor command when the project configures none,
// because $EDITOR belongs to the terminal the user is sitting at rather than to
// the daemon's environment. The screen therefore asks the client for one, on the
// selected repository's worktree.
func TestAnUnconfiguredEditorFallsBackToTheEnvironment(t *testing.T) {
	backend := newFakeBackend()
	model := reviewScreen(t, backend)

	press(t, model, "e")

	if len(backend.reviewRan) != 0 {
		t.Errorf("an editor command was run from the daemon's list: %+v", backend.reviewRan)
	}
	if len(backend.edited) != 1 || backend.edited[0] != "/srv/worktrees/example/7f3a1c2e/core" {
		t.Errorf("the editor opened %v, want the selected repository's worktree", backend.edited)
	}
}

// TestReviewActionsReachTheDaemonAndNothingElse is the fourth acceptance
// criterion at the screen.
//
// Running the checks asks the daemon and does nothing else: no runtime action is
// issued, which is checked by counting what the backend was asked for rather
// than by looking at what a container is doing.
func TestReviewActionsReachTheDaemonAndNothingElse(t *testing.T) {
	backend := newFakeBackend()
	model := reviewScreen(t, backend)

	press(t, model, "V")

	if len(backend.runtimeCalls) != 0 {
		t.Errorf("running the checks reached the runtime: %v", backend.runtimeCalls)
	}
	verified := false
	for _, call := range backend.reviewCalls {
		if strings.HasPrefix(call, string(api.ReviewVerify)+" ") {
			verified = true
		}
	}
	if !verified {
		t.Errorf("running the checks did not reach the daemon: %v", backend.reviewCalls)
	}
}

// TestThePanelOffersNoDecisionInAnyWorkflowState is what ADR-086 removed, pinned
// so that it does not come back by habit.
//
// Approve was pressed once in fifty-one tasks and request-changes never, nothing
// read the state either produced, and requesting changes recorded a label and
// then told the user to attach and do the real thing. The keys are gone from the
// panel in every state a task can be in, and pressing them asks the daemon for
// nothing.
func TestThePanelOffersNoDecisionInAnyWorkflowState(t *testing.T) {
	for _, workflow := range []string{
		"draft", "preparing", "working", "review_requested", "verifying",
		"ready_for_review", "verification_failed", "failed",
	} {
		t.Run(workflow, func(t *testing.T) {
			backend := newFakeBackend()
			status := reviewed()
			status.Task.Workflow = workflow
			backend.reviewStatus = status

			model := press(t, dashboard(backend, status.Task), "v")
			before := len(backend.reviewCalls)

			for _, key := range []string{"A", "C"} {
				after := press(t, model, key)
				if len(backend.reviewCalls) != before {
					t.Errorf("%s recorded %v", key, backend.reviewCalls[before:])
				}
				if after.review.pending != "" {
					t.Errorf("%s left the panel waiting for %q", key, after.review.pending)
				}
			}

			panel := ansi.Strip(model.taskPanel())
			for _, gone := range []string{"A to approve", "C to send back", "decision"} {
				if strings.Contains(panel, gone) {
					t.Errorf("a %s task is still offered %q:\n%s", workflow, gone, panel)
				}
			}
			if hints := ansi.Strip(taskPanelHints()); strings.Contains(hints, "approve") ||
				strings.Contains(hints, "request changes") {
				t.Errorf("the panel's hints still name a decision: %s", hints)
			}
		})
	}
}

// TestTheWorkflowLineNamesTheExitsThatExist is what the decision field became.
//
// The two transitions that carry the real loop are the ones nobody pressed a
// decision key for: sending work back is the user attaching and typing, and
// finishing is publishing and then cleaning up. So the line under the workflow
// names those, in the states where they are what happens next, and says nothing
// in the states where they are not (ADR-086).
func TestTheWorkflowLineNamesTheExitsThatExist(t *testing.T) {
	const exits = "P to publish · a to attach and revise"

	for workflow, want := range map[string]bool{
		"review_requested":    true,
		"ready_for_review":    true,
		"verification_failed": true,
		// The checks are running, and what happens next is the gate's rather
		// than the user's — which is the state the old decision field named no
		// key in either.
		"verifying": false,
		"working":   false,
		"preparing": false,
		"draft":     false,
		"failed":    false,
		"archived":  false,
	} {
		t.Run(workflow, func(t *testing.T) {
			task := reviewed().Task
			task.Workflow = workflow

			model := dashboard(newFakeBackend(), task)
			model.selected = task.ID
			model.screen = screenTask

			panel := ansi.Strip(model.taskPanel())
			if got := strings.Contains(panel, exits); got != want {
				t.Errorf("a %s task names the exits (%t), want %t:\n%s", workflow, got, want, panel)
			}
		})
	}

	// And where it is drawn, it is under the workflow rather than beside it: the
	// state is what a reader looks for and the exits are what they read next.
	task := reviewed().Task
	task.Workflow = "ready_for_review"
	model := dashboard(newFakeBackend(), task)
	model.selected = task.ID
	model.screen = screenTask

	for _, line := range strings.Split(ansi.Strip(model.taskPanel()), "\n") {
		if !strings.Contains(line, exits) {
			continue
		}
		if strings.TrimRight(line, " ") != strings.Repeat(" ", fieldValueColumn)+exits {
			t.Errorf("the exits are not on a continuation line under the workflow: %q", line)
		}
		return
	}
	t.Errorf("a task ready for review does not name its exits:\n%s", ansi.Strip(model.taskPanel()))
}

// TestOpeningReviewObservesRatherThanDecides checks that arriving at the screen
// changes nothing about the review: it compares, and the decision is a key the
// user presses.
func TestOpeningReviewObservesRatherThanDecides(t *testing.T) {
	backend := newFakeBackend()
	reviewScreen(t, backend)

	if len(backend.reviewCalls) != 1 {
		t.Fatalf("opening review made %d requests, want one: %v", len(backend.reviewCalls), backend.reviewCalls)
	}
	if !strings.HasPrefix(backend.reviewCalls[0], string(api.ReviewObserve)+" ") {
		t.Errorf("opening review asked for %q, want an observation", backend.reviewCalls[0])
	}
}

// TestATaskReadyForReviewKeepsItsRunningServices is FR-RUN-005's rule that Feat
// never stops a task's services on its own, exercised on the panel a user reads
// when the work is ready.
//
// It used to be an offer in words — "press t to stop them" — shown after
// approving, which went with the approval (ADR-086). What it was protecting is
// this: reading the panel issues no runtime action, and the services are still
// running afterwards (docs/02-user-workflows.md §7).
func TestATaskReadyForReviewKeepsItsRunningServices(t *testing.T) {
	backend := newFakeBackend()
	status := reviewed()
	status.Task.Workflow = "ready_for_review"
	status.Task.Runtime = runningRuntime()
	backend.reviewStatus = status

	model := dashboard(backend, status.Task)
	model = press(t, model, "v")

	view := ansi.Strip(content(model))
	if !strings.Contains(view, "running") {
		t.Errorf("the panel does not say the task's services are running:\n%s", view)
	}
	if len(backend.runtimeCalls) != 0 {
		t.Errorf("reading the panel acted on the runtime: %v", backend.runtimeCalls)
	}
}

// taskEvent is one item of the daemon's stream, about one task.
func taskEvent(id, kind string) api.Event {
	return api.Event{
		Kind:      api.KindTask,
		TaskEvent: &api.TaskEvent{Sequence: 1, TaskID: id, Type: kind},
	}
}

// TestAGateLandingRefreshesTheChecksOnTheOpenPanel is the defect found in use.
//
// A gate is background work: `V` records that the checks are running and returns,
// and the results land minutes later. The dashboard answered every event by
// re-reading the task list, which carries the workflow and the check counts but
// not the results — so the panel that asked for the run went on showing the
// previous run's failures under a workflow that had moved past them, until the
// user left the tab and came back.
func TestAGateLandingRefreshesTheChecksOnTheOpenPanel(t *testing.T) {
	backend := newFakeBackend()
	failed := reviewed()
	failed.Task.Workflow = "verification_failed"
	backend.reviewStatus = failed

	model := press(t, dashboard(backend, failed.Task), "v")
	if !strings.Contains(ansi.Strip(model.taskPanel()), "exited with status 1") {
		t.Fatalf("the panel does not start from the failed run:\n%s", ansi.Strip(model.taskPanel()))
	}

	// What the daemon would answer once the gate it started has landed.
	passed := reviewed()
	passed.Task.Workflow = "ready_for_review"
	passed.Review.Checks = []api.ReviewCheck{
		{ID: "unit", RepositoryID: "core", Status: "passed", Reporter: "provider", Detail: "82 passed"},
	}
	backend.reviewStatus = passed

	updated, cmd := model.Update(eventMsg{event: taskEvent(failed.Task.ID, "review_state_changed")})
	model = applyCommand(t, updated.(Model), cmd)

	panel := ansi.Strip(model.taskPanel())
	if strings.Contains(panel, "exited with status 1") {
		t.Errorf("the panel still shows the run that the gate replaced:\n%s", panel)
	}
	if !strings.Contains(panel, "82 passed") {
		t.Errorf("the panel does not show what the gate found:\n%s", panel)
	}
}

// TestOnlyAChangeToThisPanelsReviewCostsAnObservation keeps the refresh narrow.
//
// An observation walks every repository with Git, which is seconds on a task
// holding three of them, and an agent's hooks produce events several times a
// turn. Answering all of them would put a Git walk behind every keystroke the
// agent makes.
func TestOnlyAChangeToThisPanelsReviewCostsAnObservation(t *testing.T) {
	for what, arrange := range map[string]struct {
		event   api.Event
		prepare func(Model) Model
	}{
		"an event about another task": {
			event: taskEvent(otherTask().ID, "review_state_changed"),
		},
		"a state this panel does not draw": {
			event: taskEvent(liveTask().ID, "agent_process_changed"),
		},
		"a stream item that is not a task event": {
			event: api.Event{Kind: api.KindHello},
		},
		"an observation already in flight": {
			event: taskEvent(liveTask().ID, "review_state_changed"),
			prepare: func(m Model) Model {
				m.review.observing = true
				return m
			},
		},
	} {
		t.Run(what, func(t *testing.T) {
			backend := newFakeBackend()
			model := reviewScreen(t, backend)
			if arrange.prepare != nil {
				model = arrange.prepare(model)
			}
			before := len(backend.reviewCalls)

			updated, cmd := model.Update(eventMsg{event: arrange.event})
			applyCommand(t, updated.(Model), cmd)

			if len(backend.reviewCalls) != before {
				t.Errorf("%s asked for %v", what, backend.reviewCalls[before:])
			}
		})
	}

	// And the panel has to be the open one: an event about the selected task
	// while the user is reading its terminal costs nothing either.
	backend := newFakeBackend()
	elsewhere := press(t, reviewScreen(t, backend), "esc")
	if elsewhere.screen == screenTask {
		t.Fatalf("esc left the task panel open")
	}
	before := len(backend.reviewCalls)

	updated, cmd := elsewhere.Update(eventMsg{event: taskEvent(liveTask().ID, "review_state_changed")})
	applyCommand(t, updated.(Model), cmd)

	if len(backend.reviewCalls) != before {
		t.Errorf("an event reached a panel nobody has open: %v", backend.reviewCalls[before:])
	}
}

// TestChecksThatAreRunningAreNotReportedAsResults is the other half of the same
// defect, in the window before anything has landed.
//
// A gate records nothing until it finishes, so what is stored while it runs is
// the run before it. Reporting that as this task's checks tells a user who has
// just pressed V that the run they started has already failed.
func TestChecksThatAreRunningAreNotReportedAsResults(t *testing.T) {
	backend := newFakeBackend()
	running := reviewed()
	running.Task.Workflow = "verifying"
	backend.reviewStatus = running

	model := press(t, dashboard(backend, running.Task), "v")

	panel := ansi.Strip(model.taskPanel())
	if !strings.Contains(panel, "checks        running") {
		t.Errorf("a task whose checks are running does not say so:\n%s", panel)
	}
	if strings.Contains(panel, "1 failed") {
		t.Errorf("the previous run's count is reported as this task's checks:\n%s", panel)
	}
	// The results themselves are kept and dated rather than hidden: the last
	// thing known is worth reading, and it is not what is happening.
	if !strings.Contains(panel, "from the run before this one") {
		t.Errorf("the results shown are not dated:\n%s", panel)
	}
	if !strings.Contains(panel, "exited with status 1") {
		t.Errorf("the previous run's results were dropped rather than dated:\n%s", panel)
	}
}

// TestAnObservationLandingDoesNotEndAWaitForACheckRun keeps the indicator
// honest now that two requests can be outstanding at once.
//
// A gate landing while the user waits for one is exactly that: the event fires an
// observation, and the response to it must not stop the indicator the check run
// is still holding up.
func TestAnObservationLandingDoesNotEndAWaitForACheckRun(t *testing.T) {
	backend := newFakeBackend()
	model := reviewScreen(t, backend)

	waiting, _ := model.Update(key("V"))
	model = waiting.(Model)
	if model.review.pending != api.ReviewVerify {
		t.Fatalf("V left the panel waiting for %q", model.review.pending)
	}

	landed, _ := model.Update(reviewMsg{
		task: model.review.task, action: api.ReviewObserve, status: reviewed(),
	})
	model = landed.(Model)

	if model.review.pending != api.ReviewVerify {
		t.Errorf("an observation cleared the wait for the check run: pending is %q", model.review.pending)
	}
	if !model.waiting() {
		t.Error("the panel stopped waiting while the check run was still outstanding")
	}
}
