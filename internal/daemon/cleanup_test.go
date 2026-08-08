package daemon

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/reconcile"
	"github.com/ma8el/feat/internal/tmux/tmuxtest"
)

// launched arranges a confirmed task with a terminal, which is the state
// cleanup is about.
func launched(t *testing.T) (*service, *preparation, *tmuxtest.Server) {
	t.Helper()

	arranged := prepared(t)
	server := tmuxtest.New()
	service, _ := withTmux(t, arranged, server)
	if _, err := service.PrepareTerminal(context.Background(), arranged.ref, placeholder(arranged.home)); err != nil {
		t.Fatalf("PrepareTerminal: %v", err)
	}
	return service, arranged, server
}

// planFor resolves what a task owns.
func planFor(t *testing.T, service *service, arranged *preparation) api.CleanupPlan {
	t.Helper()

	plan, err := service.CleanupPlan(context.Background(), arranged.ref.Task)
	if err != nil {
		t.Fatalf("CleanupPlan: %v", err)
	}
	return plan
}

// selectAll builds a selection confirming every warning of the named classes.
func selectAll(plan api.CleanupPlan, classes ...reconcile.Class) api.CleanupSelection {
	selection := api.CleanupSelection{Token: plan.Token}
	for _, class := range classes {
		for _, entry := range plan.Classes {
			if entry.Class == string(class) {
				selection.Classes = append(selection.Classes, api.CleanupChoice{
					Class: entry.Class, ConfirmedWarnings: entry.Warnings,
				})
			}
		}
	}
	return selection
}

// classOf returns one class of a plan.
func classOf(plan api.CleanupPlan, class reconcile.Class) (api.CleanupClass, bool) {
	for _, entry := range plan.Classes {
		if entry.Class == string(class) {
			return entry, true
		}
	}
	return api.CleanupClass{}, false
}

// TestPlanningACleanupRemovesNothing is what makes the plan safe to render on a
// screen a user is reading.
//
// It is checked at the adapters rather than at the outcome: that no Git command
// removed anything and no tmux command ran at all is a stronger statement than
// that a directory happens to still be there.
func TestPlanningACleanupRemovesNothing(t *testing.T) {
	service, arranged, server := launched(t)

	before := len(arranged.fake.vectors())
	plan := planFor(t, service, arranged)

	if len(plan.Classes) == 0 {
		t.Fatal("the plan named nothing, so it cannot be said to have removed nothing")
	}
	for _, vector := range arranged.fake.vectors()[before:] {
		for _, destructive := range []string{"worktree remove", "branch -d", "branch -D", "worktree prune"} {
			if strings.Contains(vector, destructive) {
				t.Errorf("planning ran %q", vector)
			}
		}
	}
	if server.Ran("kill-window") || server.Ran("kill-session") || server.Ran("kill-pane") {
		t.Error("planning removed a tmux object")
	}

	// And the record is untouched: a plan is a question, not a change.
	if task := arranged.reload(t); task.Workflow == domain.WorkflowArchived {
		t.Error("planning archived the task")
	}
}

// TestCleanupRemovesOnlyTheClassesSelected is FR-CLEAN-002, checked on the
// argument vectors.
//
// The worktree class is selected and the branch class is not, so a run that
// removed both would be one that treated a choice as an implication.
func TestCleanupRemovesOnlyTheClassesSelected(t *testing.T) {
	service, arranged, server := launched(t)
	arranged.fake.branches["feat/rate-limit"] = true

	plan := planFor(t, service, arranged)
	worktrees, ok := classOf(plan, reconcile.ClassWorktrees)
	if !ok {
		t.Fatal("the plan named no worktrees")
	}

	before := len(arranged.fake.vectors())
	result, err := service.Cleanup(context.Background(), arranged.ref.Task,
		selectAll(plan, reconcile.ClassWorktrees))
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	var removed, deleted bool
	for _, vector := range arranged.fake.vectors()[before:] {
		if strings.Contains(vector, "worktree remove") {
			removed = true
		}
		if strings.HasPrefix(vector, "branch -") {
			deleted = true
		}
	}
	if !removed {
		t.Error("selecting the worktrees removed none")
	}
	if deleted {
		t.Error("selecting the worktrees also deleted a branch")
	}
	if server.Ran("kill-window") {
		t.Error("selecting the worktrees also removed the task's terminal")
	}
	if len(result.Removed) != len(worktrees.Targets) {
		t.Errorf("reported %d removals, want %d", len(result.Removed), len(worktrees.Targets))
	}
	for _, entry := range result.Removed {
		if entry.Class != string(reconcile.ClassWorktrees) {
			t.Errorf("removed a %s, which was not selected", entry.Class)
		}
	}
}

// TestCleanupRefusesAStalePlan is the token doing its job through the daemon.
func TestCleanupRefusesAStalePlan(t *testing.T) {
	service, arranged, _ := launched(t)
	plan := planFor(t, service, arranged)
	selection := selectAll(plan, reconcile.ClassWorktrees)
	selection.Token = "not the token of anything"

	before := len(arranged.fake.vectors())
	_, err := service.Cleanup(context.Background(), arranged.ref.Task, selection)
	if err == nil {
		t.Fatal("a cleanup carrying a token from nowhere removed something")
	}
	for _, vector := range arranged.fake.vectors()[before:] {
		if strings.Contains(vector, "worktree remove") {
			t.Errorf("a refused cleanup still ran %q", vector)
		}
	}
}

// TestCleanupRefusesUnconfirmedDirtyWork is slice 12's eighth acceptance
// criterion at the daemon, where the warning is observed rather than supplied.
//
// The worktree is made dirty after the plan was taken, which is the case the
// re-resolution exists for: the plan the user was shown had no warning, and the
// one that decides has one.
func TestCleanupRefusesUnconfirmedDirtyWork(t *testing.T) {
	service, arranged, _ := launched(t)

	plan := planFor(t, service, arranged)
	worktrees, ok := classOf(plan, reconcile.ClassWorktrees)
	if !ok || len(worktrees.Warnings) != 0 {
		t.Fatalf("the fixture's worktrees are already risky: %+v", worktrees.Warnings)
	}
	selection := selectAll(plan, reconcile.ClassWorktrees)

	// The agent wrote something between the screen and the key press.
	arranged.fake.dirty = true

	before := len(arranged.fake.vectors())
	_, err := service.Cleanup(context.Background(), arranged.ref.Task, selection)
	if err == nil {
		t.Fatal("a worktree that became dirty was removed on a confirmation given before it was")
	}
	if !strings.Contains(err.Error(), "uncommitted") {
		t.Errorf("error = %v, want it to name the uncommitted work", err)
	}
	for _, vector := range arranged.fake.vectors()[before:] {
		if strings.Contains(vector, "worktree remove") {
			t.Errorf("a refused cleanup still ran %q", vector)
		}
	}

	// Asking again shows the warning, and confirming it removes the worktree
	// with --force, because Git refuses a dirty worktree without it.
	fresh := planFor(t, service, arranged)
	risky, _ := classOf(fresh, reconcile.ClassWorktrees)
	if len(risky.Warnings) == 0 {
		t.Fatal("the fresh plan does not warn about the uncommitted work")
	}
	if _, err := service.Cleanup(context.Background(), arranged.ref.Task,
		selectAll(fresh, reconcile.ClassWorktrees)); err != nil {
		t.Fatalf("a confirmed removal of dirty work was refused: %v", err)
	}

	var forced bool
	for _, vector := range arranged.fake.vectors() {
		if strings.Contains(vector, "worktree remove --force") {
			forced = true
		}
	}
	if !forced {
		t.Error("a confirmed dirty worktree was removed without --force, which Git would refuse")
	}
}

// TestCleanupRetainsVolumesThatWereNotChosen is slice 12's sixth acceptance
// criterion at the daemon.
//
// The fixture is a host-execution task with no Compose project at all, so the
// assertion is the narrow one that can be made here: no volume command is ever
// produced by a selection that did not name the class. The adapter-level rule —
// that removal is by name and never through `down --volumes` — is checked in
// internal/execution/compose and internal/runtime/compose.
func TestCleanupRetainsVolumesThatWereNotChosen(t *testing.T) {
	service, arranged, _ := launched(t)
	plan := planFor(t, service, arranged)

	if _, ok := classOf(plan, reconcile.ClassVolumes); ok {
		t.Skip("the fixture has volumes; this test is about a plan that has none")
	}
	selection := selectAll(plan, reconcile.ClassWorktrees)
	selection.Classes = append(selection.Classes, api.CleanupChoice{
		Class:             string(reconcile.ClassVolumes),
		ConfirmedWarnings: []string{reconcile.WarningVolume},
	})

	if _, err := service.Cleanup(context.Background(), arranged.ref.Task, selection); err == nil {
		t.Fatal("a cleanup removed volumes the plan never named")
	}
}

// TestArchivingRefusesToStrandAResource is the rule that keeps an archived task
// from becoming an orphan.
func TestArchivingRefusesToStrandAResource(t *testing.T) {
	service, arranged, _ := launched(t)
	arranged.fake.branches["feat/rate-limit"] = true

	plan := planFor(t, service, arranged)
	if len(plan.Classes) < 2 {
		t.Fatalf("the fixture owns %d classes, and this test needs at least two", len(plan.Classes))
	}

	partial := selectAll(plan, reconcile.ClassWorktrees)
	partial.Archive = true
	if _, err := service.Cleanup(context.Background(), arranged.ref.Task, partial); err == nil {
		t.Fatal("a task was archived while it still owned resources nobody removed")
	}
	if task := arranged.reload(t); task.Workflow == domain.WorkflowArchived {
		t.Error("a refused archive still archived the task")
	}
}

// TestArchivingKeepsTheRecordAndTheHistory is what "archive task metadata so
// Feat can explain what happened later" has to mean.
//
// Nothing is deleted from the state directory: the snapshot keeps the branch and
// the base the task recorded, and the event log keeps what each class removed.
// So both halves of the question — what the task was, and what became of what it
// owned — are still answerable after the resources are gone.
func TestArchivingKeepsTheRecordAndTheHistory(t *testing.T) {
	service, arranged, _ := launched(t)

	before := arranged.reload(t)
	branch := before.Repositories[0].Branch
	base := before.Repositories[0].BaseCommit
	if branch == "" || base == "" {
		t.Fatal("the fixture task recorded no branch or base to keep")
	}

	plan := planFor(t, service, arranged)
	selection := api.CleanupSelection{Token: plan.Token, Archive: true}
	for _, class := range plan.Classes {
		selection.Classes = append(selection.Classes, api.CleanupChoice{
			Class: class.Class, ConfirmedWarnings: class.Warnings,
		})
	}

	result, err := service.Cleanup(context.Background(), arranged.ref.Task, selection)
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if !result.Archived {
		t.Fatal("the task was not archived")
	}

	after := arranged.reload(t)
	if after.Workflow != domain.WorkflowArchived {
		t.Errorf("workflow = %q, want archived", after.Workflow)
	}
	if after.Repositories[0].Branch != branch {
		t.Errorf("branch = %q, want the recorded %q: archiving must not discard what the task was",
			after.Repositories[0].Branch, branch)
	}
	if after.Repositories[0].BaseCommit != base {
		t.Errorf("base = %q, want the recorded %q", after.Repositories[0].BaseCommit, base)
	}

	history := events(t, service, arranged)
	if _, ok := recorded(history, domain.EventCleanedUp); !ok {
		t.Error("nothing in the event log says what the cleanup removed")
	}
	var archived bool
	for _, event := range history {
		if event.Type == domain.EventWorkflowChanged && event.To == string(domain.WorkflowArchived) {
			archived = true
			if !strings.Contains(event.Detail, "removing") {
				t.Errorf("the archive event does not say what was removed: %q", event.Detail)
			}
		}
	}
	if !archived {
		t.Error("the archive is not on the event stream")
	}
}

// TestAPartialCleanupIsRecoverable is the recoverability rule applied to
// removal: a run that failed half way leaves an account of what went and a plan
// that names what is left.
func TestAPartialCleanupIsRecoverable(t *testing.T) {
	service, arranged, _ := launched(t)
	// The second repository's worktree refuses to be removed.
	arranged.fake.failRemove = "store"

	plan := planFor(t, service, arranged)
	_, err := service.Cleanup(context.Background(), arranged.ref.Task,
		selectAll(plan, reconcile.ClassWorktrees))
	if err == nil {
		t.Fatal("a worktree that could not be removed reported success")
	}
	if !strings.Contains(err.Error(), "worktrees") {
		t.Errorf("error = %v, want it to name the class that failed", err)
	}

	event, ok := recorded(events(t, service, arranged), domain.EventCleanedUp)
	if !ok {
		t.Fatal("a failed cleanup left no account of what it had already removed")
	}
	if !strings.Contains(event.Detail, "failed") {
		t.Errorf("event detail = %q, want it to say the removal failed", event.Detail)
	}

	// Asking again names what is left rather than what was there.
	again := planFor(t, service, arranged)
	remaining, ok := classOf(again, reconcile.ClassWorktrees)
	if !ok {
		t.Fatal("the second plan names no worktrees, though one could not be removed")
	}
	for _, target := range remaining.Targets {
		if strings.HasSuffix(target.Identity, "/api") && target.Present {
			t.Error("the worktree that was removed is still reported as present")
		}
	}
}

// TestCleanupOfAlreadyAbsentResourcesSucceeds keeps a partial cleanup finishable
// by hand: a user who removed a worktree with Git should still be able to tidy
// the rest.
func TestCleanupOfAlreadyAbsentResourcesSucceeds(t *testing.T) {
	service, arranged, _ := launched(t)

	plan := planFor(t, service, arranged)
	worktrees, ok := classOf(plan, reconcile.ClassWorktrees)
	if !ok {
		t.Fatal("the plan named no worktrees")
	}
	if err := os.RemoveAll(worktrees.Targets[0].Identity); err != nil {
		t.Fatalf("removing the worktree by hand: %v", err)
	}

	fresh := planFor(t, service, arranged)
	result, err := service.Cleanup(context.Background(), arranged.ref.Task,
		selectAll(fresh, reconcile.ClassWorktrees))
	if err != nil {
		t.Fatalf("cleaning up after a manual removal failed: %v", err)
	}

	var reportedAbsent bool
	for _, entry := range result.Removed {
		if !entry.Removed {
			reportedAbsent = true
		}
	}
	if !reportedAbsent {
		t.Error("a resource that was already gone was reported as removed")
	}
}
