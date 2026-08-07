package ui

import (
	"strings"
	"testing"

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
			Status:  "pending",
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

// TestTheReviewScreenGroupsChangesByRepository is FR-REV-001 on the screen.
//
// Every repository is there with its own recorded base, and the base is shown
// rather than implied: a review of a long-running task is only meaningful
// against the commit it started from, and a user who cannot see which commit
// that was has to take Feat's word for it.
func TestTheReviewScreenGroupsChangesByRepository(t *testing.T) {
	model := reviewScreen(t, newFakeBackend())
	view := model.View()

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
			t.Errorf("the review screen does not show %s (%q):\n%s", field, want, view)
		}
	}
}

// TestTheReviewScreenTellsAClaimFromAnEnforcedResult is slice 11's fifth
// acceptance criterion where the user reads it.
//
// A result Feat ran and a result the agent asserted are both shown, and they do
// not read alike. Showing them alike would tell the user something Feat does not
// know (FR-AGENT-006).
func TestTheReviewScreenTellsAClaimFromAnEnforcedResult(t *testing.T) {
	model := reviewScreen(t, newFakeBackend())
	view := model.View()

	if !strings.Contains(view, "Feat ran this") {
		t.Errorf("an enforced result is not marked as one:\n%s", view)
	}
	if !strings.Contains(view, "the agent reported this") {
		t.Errorf("a claimed result is not marked as one:\n%s", view)
	}
	if !strings.Contains(view, "2 failed, 82 passed") {
		t.Errorf("the failing check's own output is not shown:\n%s", view)
	}
	// The strings slice 8 left behind, which named a slice rather than a state.
	if strings.Contains(view, "slice 11") {
		t.Errorf("the screen still names the slice that was going to deliver it:\n%s", view)
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

// TestReviewDecisionsReachTheDaemonAndNothingElse is the fourth acceptance
// criterion at the screen.
//
// Approving asks the daemon to record a decision and does nothing else: no
// runtime action is issued, which is checked by counting what the backend was
// asked for rather than by looking at what a container is doing.
func TestReviewDecisionsReachTheDaemonAndNothingElse(t *testing.T) {
	backend := newFakeBackend()
	model := reviewScreen(t, backend)

	press(t, model, "A")

	if len(backend.runtimeCalls) != 0 {
		t.Errorf("approving reached the runtime: %v", backend.runtimeCalls)
	}
	approved := false
	for _, call := range backend.reviewCalls {
		if strings.HasPrefix(call, string(api.ReviewApprove)+" ") {
			approved = true
		}
	}
	if !approved {
		t.Errorf("approving did not reach the daemon: %v", backend.reviewCalls)
	}
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

// TestAnApprovedTaskWithRunningServicesIsOfferedTheStop is slice 9's fifth
// acceptance criterion, exercised for the first time by the approval this slice
// delivers.
//
// The offer is made in words on the screen the user is on when they approve, and
// Feat never acts on it (docs/02-user-workflows.md §7).
func TestAnApprovedTaskWithRunningServicesIsOfferedTheStop(t *testing.T) {
	backend := newFakeBackend()
	status := reviewed()
	status.Task.Workflow = "approved"
	status.Task.Runtime = runningRuntime()
	backend.reviewStatus = status

	model := dashboard(backend, status.Task)
	model = press(t, model, "v")

	view := model.View()
	if !strings.Contains(view, "press t to stop") {
		t.Errorf("an approved task with running services is not offered the stop:\n%s", view)
	}
	if len(backend.runtimeCalls) != 0 {
		t.Errorf("the offer was acted on: %v", backend.runtimeCalls)
	}
}
