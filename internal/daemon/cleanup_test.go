package daemon

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/reconcile"
	"github.com/ma8el/feat/internal/store"
	"github.com/ma8el/feat/internal/tmux/tmuxtest"
)

// launched arranges a confirmed task with a terminal, which is the state cleanup
// is about.
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
// screen a user is reading. It is checked at the adapters rather than at the
// outcome, because no Git command removing anything and no tmux command running
// at all is stronger than a directory that happens to still be there.
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

	// And the record is untouched: a plan is a question rather than a change.
	if task := arranged.reload(t); task.Workflow == domain.WorkflowArchived {
		t.Error("planning archived the task")
	}
}

// TestCleanupRemovesOnlyTheClassesSelected is FR-CLEAN-002, checked on the
// argument vectors. The worktree class is selected and the branch class is not,
// so a run that removed both would treat a choice as an implication.
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
	// Every target the class named is accounted for, and everything reported
	// belongs to the class that was chosen. The removals are not counted against the
	// targets, because removing the last worktree in a directory the task was given
	// removes that directory too, and it is reported.
	reported := make(map[string]bool, len(result.Removed))
	for _, entry := range result.Removed {
		if entry.Class != string(reconcile.ClassWorktrees) {
			t.Errorf("removed a %s, which was not selected", entry.Class)
		}
		reported[entry.Identity] = true
	}
	for _, target := range worktrees.Targets {
		if !reported[target.Identity] {
			t.Errorf("the worktree %s was removed without being reported", target.Identity)
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

// TestCleanupRefusesUnconfirmedDirtyWork is FR-CLEAN-003 at the daemon, where the
// warning is observed rather than supplied. The worktree is made dirty after the
// plan was taken, which is the case the re-resolution exists for: the plan the
// user was shown had no warning, and the one that decides has one.
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

	// Asking again shows the warning, and confirming it removes the worktree with
	// --force, because Git refuses a dirty worktree without it.
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

// TestCleanupRetainsVolumesThatWereNotChosen is FR-CLEAN-004's retention rule at
// the daemon. The fixture is a host-execution task with no Compose project, so
// this asserts the narrow thing available here: a selection that did not name the
// class produces no volume command. internal/execution/compose and
// internal/runtime/compose check the adapter-level rule that removal is by name
// and never through `down --volumes`.
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

// TestAContainedBranchCleansUpAndArchivesInOnePass is the deadlock ADR-097
// records, end to end.
//
// The fixture is the ordinary state of a checkout that fetches under a remote
// base policy: the recorded base ref contains the task branch, so Feat's plan
// reports nothing at risk, and the checkout's HEAD does not, so `git branch -d`
// refuses. Deriving the flag from the warnings left a branch with no warning to
// confirm and no path to `-D`, so the cleanup failed on every retry and the
// archive refused to leave the branch behind.
func TestAContainedBranchCleansUpAndArchivesInOnePass(t *testing.T) {
	service, arranged, _ := launched(t)
	branch := arranged.reload(t).Repositories[0].Branch
	arranged.fake.branches[branch] = true
	arranged.fake.contained = true
	arranged.fake.headBehind = true

	plan := planFor(t, service, arranged)
	branches, ok := classOf(plan, reconcile.ClassBranches)
	if !ok {
		t.Fatal("the plan named no branches")
	}
	// The premise: the plan says nothing is at risk, so there is nothing for the
	// user to confirm. A fixture that warned would test the other case.
	if len(branches.Warnings) != 0 {
		t.Fatalf("the contained branch carries warnings %v, so this is not the case the test is for",
			branches.Warnings)
	}

	selection := api.CleanupSelection{Token: plan.Token, Archive: true}
	for _, class := range plan.Classes {
		selection.Classes = append(selection.Classes, api.CleanupChoice{
			Class: class.Class, ConfirmedWarnings: class.Warnings,
		})
	}

	result, err := service.Cleanup(context.Background(), arranged.ref.Task, selection)
	if err != nil {
		t.Fatalf("cleaning up a task whose branch its base ref contains: %v", err)
	}
	if !result.Archived {
		t.Fatal("the task was not archived, and no other selection would have removed the branch")
	}

	var deleted bool
	for _, vector := range arranged.fake.vectors() {
		if strings.HasPrefix(vector, "branch -d ") {
			t.Errorf("the deletion ran %q, which Git refuses in a checkout whose HEAD is behind the base ref", vector)
		}
		if strings.HasPrefix(vector, "branch -D ") {
			deleted = true
		}
	}
	if !deleted {
		t.Errorf("no branch deletion ran at all: %v", arranged.fake.vectors())
	}
	if arranged.fake.branches[branch] {
		t.Error("the branch is still there")
	}

	// The force is accounted for rather than silent. It overrode Git's own refusal,
	// so what it rested on is in the report and in the event log.
	var note string
	for _, entry := range result.Removed {
		if entry.Class == string(reconcile.ClassBranches) {
			note = entry.Note
		}
	}
	if !strings.Contains(note, "forced") || !strings.Contains(note, "refs/remotes/origin/main") {
		t.Errorf("the removal reported %q, want it to say it forced and name the ref it established containment against",
			note)
	}
	for _, event := range events(t, service, arranged) {
		if event.Type == domain.EventCleanedUp && event.To == string(reconcile.ClassBranches) {
			if !strings.Contains(event.Detail, "forced") {
				t.Errorf("the event log says %q, want the account of the forced deletion", event.Detail)
			}
		}
	}
}

// TestAnUncontainedBranchIsStillOnlyDeletedOnAConfirmation is the half that must
// not be weakened: a branch holding commits the base ref does not have warns, and
// the confirmation is what produces the force (FR-CLEAN-003).
func TestAnUncontainedBranchIsStillOnlyDeletedOnAConfirmation(t *testing.T) {
	service, arranged, _ := launched(t)
	arranged.fake.branches[arranged.reload(t).Repositories[0].Branch] = true
	arranged.fake.headBehind = true

	plan := planFor(t, service, arranged)
	branches, ok := classOf(plan, reconcile.ClassBranches)
	if !ok || len(branches.Warnings) == 0 {
		t.Fatalf("the plan does not warn about a branch its base ref does not contain: %+v", branches)
	}

	// Selecting it without confirming what it costs is refused, and nothing ran.
	before := len(arranged.fake.vectors())
	unconfirmed := api.CleanupSelection{
		Token:   plan.Token,
		Classes: []api.CleanupChoice{{Class: string(reconcile.ClassBranches)}},
	}
	if _, err := service.Cleanup(context.Background(), arranged.ref.Task, unconfirmed); err == nil {
		t.Fatal("an unmerged branch was deleted without confirming the warning")
	}
	for _, vector := range arranged.fake.vectors()[before:] {
		if strings.HasPrefix(vector, "branch -") {
			t.Errorf("a refused cleanup still ran %q", vector)
		}
	}

	// Confirming it deletes it, and the account names the confirmation rather than
	// a containment nobody established.
	result, err := service.Cleanup(context.Background(), arranged.ref.Task,
		selectAll(plan, reconcile.ClassBranches))
	if err != nil {
		t.Fatalf("a confirmed deletion of an unmerged branch was refused: %v", err)
	}
	for _, entry := range result.Removed {
		if entry.Class == string(reconcile.ClassBranches) && !strings.Contains(entry.Note, "confirmed") {
			t.Errorf("the removal reported %q, want it to rest on the confirmation", entry.Note)
		}
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

// TestArchivingKeepsTheRecordAndTheHistory is what archiving task metadata has to
// mean. Nothing is deleted from the state directory: the snapshot keeps the
// branch and the base the task recorded, and the event log keeps what each class
// removed, so what the task was and what became of what it owned are both still
// answerable after the resources are gone.
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

// TestAPartialCleanupIsRecoverable is the recoverability rule applied to removal:
// a run that failed half way leaves an account of what went and a plan that names
// what is left.
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
// by hand: a user who removed a worktree with Git can still tidy the rest.
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

// alsoPrepared adds a second live task to the fixture project. It keeps the
// project's worktree root in the orphan scan, which lists the roots live tasks
// name, so a directory left under one is reported to whoever is still using the
// project.
func alsoPrepared(t *testing.T, service *service, name string) store.TaskRef {
	t.Helper()

	task, err := domain.NewTask(domain.NewTaskID(), "app", name,
		domain.TaskSource{Kind: domain.SourcePrompt}, service.now())
	if err != nil {
		t.Fatalf("creating a second draft: %v", err)
	}
	if err := task.SetBrief(name+".", service.now()); err != nil {
		t.Fatalf("setting the second brief: %v", err)
	}
	if err := service.store.Tasks().Save(context.Background(), task); err != nil {
		t.Fatalf("saving the second draft: %v", err)
	}
	ref := store.Ref(task)
	if _, err := service.PrepareTask(context.Background(), ref, selection()); err != nil {
		t.Fatalf("preparing the second task: %v", err)
	}
	return ref
}

// TestACleanupLeavesNothingUnderTheWorktreeRootToReport is a residue a real
// dashboard reported: cleaning up a task removed its worktrees and left the
// directory they sat in, so the next recovery pass asked the user to look at an
// empty `…/worktrees/{project_id}/{task_id}`.
//
// The directory is Feat's own, because preparing the task created it. The two
// boundaries are checked with it, because a walk that went further would take
// directories that are not this task's: the project's directory stays while
// another task is in it, and a cleanup never removes the root.
func TestACleanupLeavesNothingUnderTheWorktreeRootToReport(t *testing.T) {
	service, arranged, _ := launched(t)
	live := alsoPrepared(t, service, "Add a health check")

	plan := planFor(t, service, arranged)
	worktrees, ok := classOf(plan, reconcile.ClassWorktrees)
	if !ok || len(worktrees.Targets) == 0 {
		t.Fatal("the plan named no worktrees")
	}
	taskDir := filepath.Dir(worktrees.Targets[0].Identity)
	projectDir := filepath.Dir(taskDir)
	root := filepath.Dir(projectDir)

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
		t.Fatal("the task was not archived, so its directory is still one a task claims")
	}

	if _, err := os.Stat(taskDir); !os.IsNotExist(err) {
		t.Errorf("the cleaned-up task's directory %s is still there: %v", taskDir, err)
	}
	if _, err := os.Stat(projectDir); err != nil {
		t.Errorf("the directory holding another task's worktrees was removed: %v", err)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("the worktree root every task is created in was removed: %v", err)
	}

	var named bool
	for _, entry := range result.Removed {
		if entry.Identity == taskDir && entry.Removed {
			named = true
		}
	}
	if !named {
		t.Errorf("the cleanup removed %s without reporting it", taskDir)
	}

	// And the pass that used to ask about it has nothing to ask.
	for _, finding := range findings(reconciled(t, service), reconcile.ClassWorktrees) {
		if finding.Status == string(reconcile.StatusOrphaned) {
			t.Errorf("a cleanup left %s for the recovery pass to report: %s", finding.Identity, finding.Detail)
		}
	}

	// The task that is still running kept every worktree it had.
	other, err := service.store.Tasks().Load(context.Background(), live)
	if err != nil {
		t.Fatalf("loading the live task: %v", err)
	}
	for _, binding := range other.Repositories {
		if _, err := os.Stat(binding.WorktreePath); err != nil {
			t.Errorf("the live task's worktree %s was removed with the other task's: %v",
				binding.WorktreePath, err)
		}
	}
}

// TestAProjectsOwnDirectoryOutlivesItsLastTask is the second half of the same
// report: an orphaned-worktree finding about a project whose tasks had all been
// cleaned up.
//
// That directory is not a leftover. Feat generates it from the worktree root,
// creates it for the project's first task, and creates every later task inside
// it, so a report that calls it an orphan tells the user to delete a directory
// Feat is going to recreate.
//
// It is therefore neither removed with the last task nor reported afterwards, and
// both halves are checked here. The third check keeps the rule narrow: a stale
// task directory inside it is still reported, which is also what proves the scan
// looked rather than never ran.
func TestAProjectsOwnDirectoryOutlivesItsLastTask(t *testing.T) {
	service, arranged, _ := launched(t)

	plan := planFor(t, service, arranged)
	worktrees, ok := classOf(plan, reconcile.ClassWorktrees)
	if !ok || len(worktrees.Targets) == 0 {
		t.Fatal("the plan named no worktrees")
	}
	taskDir := filepath.Dir(worktrees.Targets[0].Identity)
	projectDir := filepath.Dir(taskDir)

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
		t.Fatal("the task was not archived, so the project still has one")
	}

	// Nothing of the task is left in it, so this is the state the report was about:
	// a project with no task, and a directory of its own.
	if entries, err := os.ReadDir(projectDir); err != nil {
		t.Fatalf("the project's own directory went with its last task: %v", err)
	} else if len(entries) != 0 {
		t.Fatalf("the project's directory still holds %d entries, so it is not the empty one under test", len(entries))
	}
	for _, entry := range result.Removed {
		if entry.Identity == projectDir {
			t.Errorf("the cleanup removed %s, which belongs to the project rather than to the task", projectDir)
		}
	}
	if orphans := orphanedWorktrees(t, service); len(orphans) != 0 {
		t.Errorf("a project between tasks was asked about: %v", orphans)
	}

	// The rule is narrow rather than off. The residue of a cleanup by an older build
	// sits at the same depth as the task directory that has just gone and is still
	// reported, which also proves the scan looked at all.
	stale := filepath.Join(projectDir, "0f8fad5b-d9cb-469f-a165-70867728950e")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatalf("arranging the residue of an older cleanup: %v", err)
	}
	orphans := orphanedWorktrees(t, service)
	if slices.Contains(orphans, projectDir) {
		t.Errorf("the project's own directory %s was reported as an orphan a user should look at", projectDir)
	}
	if !slices.Contains(orphans, stale) {
		t.Errorf("the stale task directory %s was not reported; the orphans were %v", stale, orphans)
	}
}

// orphanedWorktrees runs a pass and returns the directories it wants looked at.
func orphanedWorktrees(t *testing.T, service *service) []string {
	t.Helper()

	var found []string
	for _, finding := range findings(reconciled(t, service), reconcile.ClassWorktrees) {
		if finding.Status == string(reconcile.StatusOrphaned) {
			found = append(found, finding.Identity)
		}
	}
	return found
}

// TestAnEmptyDirectoryLeftByAnOlderCleanupSaysItIsEmpty is what the machines that
// already have this residue are told. Removing them is still the user's, as it is
// for everything reconciliation finds (ADR-037). What changes is that a directory
// holding nothing is named as holding nothing, with the one command that clears
// it, rather than sending somebody to the end of a path to find nothing there.
func TestAnEmptyDirectoryLeftByAnOlderCleanupSaysItIsEmpty(t *testing.T) {
	service, arranged, _ := launched(t)

	task := arranged.reload(t)
	worktree := task.Repositories[0].WorktreePath
	if worktree == "" {
		t.Fatal("the fixture task records no worktree")
	}
	residue := filepath.Join(filepath.Dir(filepath.Dir(worktree)), "0f8fad5b-d9cb-469f-a165-70867728950e")
	if err := os.MkdirAll(residue, 0o755); err != nil {
		t.Fatalf("arranging the residue of an older cleanup: %v", err)
	}

	var found api.ReconciliationFinding
	for _, finding := range findings(reconciled(t, service), reconcile.ClassWorktrees) {
		if finding.Status == string(reconcile.StatusOrphaned) && finding.Identity == residue {
			found = finding
		}
	}
	if found.Identity == "" {
		t.Fatal("a directory no task records was not reported at all")
	}
	if !strings.Contains(found.Detail, "empty") {
		t.Errorf("the finding does not say the directory is empty: %q", found.Detail)
	}
	if !strings.Contains(found.Action, "rmdir") {
		t.Errorf("the action does not name the command that clears it: %q", found.Action)
	}
	if found.Identity != residue {
		t.Errorf("the finding names %q rather than the directory it is about", found.Identity)
	}
	if _, err := os.Stat(residue); err != nil {
		t.Errorf("reconciliation removed the directory it reported: %v", err)
	}
}
