package daemon

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ma8el/feat/internal/agent/claude"
	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/config"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/execution/compose/composetest"
	"github.com/ma8el/feat/internal/paths"
	"github.com/ma8el/feat/internal/store"
	"github.com/ma8el/feat/internal/tmux/tmuxtest"
)

// drafting is a daemon with a registered project and fake Git, tmux, and Docker,
// which is everything task preparation and launch drive.
type drafting struct {
	service *service
	git     *fakeGit
	tmux    *tmuxtest.Server
	docker  *composetest.Docker
	layout  paths.Layout
	env     paths.Environment
	now     time.Time
}

// launched creates a draft, resolves it, and launches it, returning the task.
//
// The fixture project runs its agent in a container, so this drives the whole
// devcontainer launch against the fake Docker: the specification, the generated
// override, the start, the probes, and the agent command.
func (d *drafting) launched(t *testing.T, title ...string) *domain.Task {
	t.Helper()

	name := "Add a rate limit"
	if len(title) > 0 {
		name = title[0]
	}
	draft := d.draft(t, name)
	d.selectRepositories(t, draft.ID)
	displayed := d.resolve(t, draft.ID)

	task, err := d.service.LaunchDraft(context.Background(), draft.ID, displayed.Fingerprint)
	if err != nil {
		t.Fatalf("launching %q: %v", name, err)
	}
	return task
}

// config loads the fixture project's configuration as the daemon resolves it.
func (d *drafting) config(t *testing.T) *config.Config {
	t.Helper()

	cfg, err := config.Load(d.layout.ProjectConfigDir(), "app", d.service.configOptions())
	if err != nil {
		t.Fatalf("loading the project configuration: %v", err)
	}
	return cfg
}

// arrangeDrafting registers the fixture project and returns a daemon whose two
// adapters are fakes, so that what preparation creates can be observed exactly.
func arrangeDrafting(t *testing.T) *drafting {
	t.Helper()

	layout := testLayout(t)
	env := configured(t, layout, "app", prepareFixture)
	for _, name := range []string{"api", "store"} {
		// With a .git directory, because a task worktree is only a repository
		// while the main checkout's Git directory is reachable — which is what a
		// containerised task has to mount (ADR-033).
		if err := os.MkdirAll(filepath.Join(env.Home, "repos", "app", name, ".git"), 0o755); err != nil {
			t.Fatalf("creating the checkout %s: %v", name, err)
		}
	}

	fake := newFakeGit()
	server := tmuxtest.New()
	docker := workingDocker()
	now := time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)

	arranged := &drafting{
		git: fake, tmux: server, docker: docker,
		layout: layout, env: env, now: now,
	}

	instance, err := New(Options{
		Layout:      layout,
		Environment: env,
		Build:       testBuild,
		Git:         fake,
		Tmux:        server,
		// Indirect, so a test can replace the fake after the daemon exists and
		// still have the launch use it.
		Docker: dockerFunc(func() *composetest.Docker { return arranged.docker }),
		Logger: slog.New(slog.DiscardHandler),
		Now:    func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := instance.service.RegisterProject(context.Background(), "app"); err != nil {
		t.Fatalf("registering the project: %v", err)
	}

	arranged.service = instance.service
	return arranged
}

// draft records a draft the way the preparation screen does.
func (d *drafting) draft(t *testing.T, title string) *domain.Task {
	t.Helper()

	task, err := d.service.CreateDraft(context.Background(), api.DraftRequest{
		Project: "app",
		Title:   title,
		Brief:   "Add a rate limit to the public API.",
		Source:  domain.TaskSource{Kind: domain.SourcePrompt},
	})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	return task
}

// selectRepositories makes the ordinary selection: the writable repository and
// the read-only one.
func (d *drafting) selectRepositories(t *testing.T, id domain.TaskID) {
	t.Helper()

	if _, err := d.service.UpdateDraft(context.Background(), id, api.DraftUpdate{
		Title: "Add a rate limit",
		Brief: "Add a rate limit to the public API.",
		Repositories: []api.DraftSelection{
			{Repository: "api", Access: domain.TaskAccessReadWrite},
			{Repository: "store", Access: domain.TaskAccessReadOnly},
		},
	}); err != nil {
		t.Fatalf("UpdateDraft: %v", err)
	}
}

// resolve plans the draft, which is what the review screen displays.
func (d *drafting) resolve(t *testing.T, id domain.TaskID) api.ResolvedDraft {
	t.Helper()

	plan, err := d.service.PlanDraft(context.Background(), id)
	if err != nil {
		t.Fatalf("PlanDraft: %v", err)
	}
	return plan
}

func (d *drafting) reload(t *testing.T, id domain.TaskID) *domain.Task {
	t.Helper()

	task, err := d.service.store.Tasks().Load(context.Background(),
		store.TaskRef{Project: "app", Task: id})
	if err != nil {
		t.Fatalf("loading task %s: %v", id, err)
	}
	return task
}

// worktreeRoot is where the fixture project's task worktrees would go.
func (d *drafting) worktreeRoot() string {
	return filepath.Join(d.layout.State, "worktrees")
}

// TestCancellingADraftCreatesNothing is the slice 6 acceptance criterion that
// cancelling a draft creates no worktrees, tmux windows, or containers.
//
// It is checked at the adapters rather than at the outcome: a Git command that
// creates a worktree and a tmux command that creates a window are the only ways
// either can appear, so a test that no such command ran is stronger than a test
// that no directory exists.
func TestCancellingADraftCreatesNothing(t *testing.T) {
	arranged := arrangeDrafting(t)

	draft := arranged.draft(t, "Add a rate limit")
	arranged.selectRepositories(t, draft.ID)
	arranged.resolve(t, draft.ID)

	cancelled, err := arranged.service.CancelDraft(context.Background(), draft.ID)
	if err != nil {
		t.Fatalf("CancelDraft: %v", err)
	}
	if cancelled.Workflow != domain.WorkflowArchived {
		t.Errorf("workflow = %q, want archived", cancelled.Workflow)
	}

	// Resolving reads the repositories and may fetch. What it must never do is
	// create anything.
	for _, vector := range arranged.git.vectors() {
		if strings.HasPrefix(vector, "worktree add") || strings.HasPrefix(vector, "branch") ||
			strings.HasPrefix(vector, "checkout") {
			t.Errorf("preparing and cancelling a draft ran %q", vector)
		}
	}
	if calls := arranged.tmux.Calls(); len(calls) != 0 {
		t.Errorf("preparing and cancelling a draft ran %d tmux commands: %v", len(calls), calls)
	}

	// And nothing is on disk under the directory Feat would have created in.
	if entries, err := os.ReadDir(arranged.worktreeRoot()); err == nil && len(entries) > 0 {
		t.Errorf("the worktree root holds %d entries after a cancelled draft", len(entries))
	}

	// The record survives as an explanation of what the user started and
	// decided against, and the task list no longer offers it.
	stored := arranged.reload(t, draft.ID)
	if stored.Workflow != domain.WorkflowArchived {
		t.Errorf("stored workflow = %q, want archived", stored.Workflow)
	}
}

// TestConfirmingLaunchesTheDisplayedSnapshot is the slice 6 acceptance
// criterion that confirming launches the previously displayed snapshot.
//
// The world moves between the plan and the confirmation: the fake resolves a
// different commit the second time it is asked, which is what a fetch that
// picked up new commits would do. The task must still start from the commit the
// user read.
func TestConfirmingLaunchesTheDisplayedSnapshot(t *testing.T) {
	arranged := arrangeDrafting(t)

	draft := arranged.draft(t, "Add a rate limit")
	arranged.selectRepositories(t, draft.ID)
	displayed := arranged.resolve(t, draft.ID)

	shown := make(map[domain.RepositoryID]string, len(displayed.Task.Repositories))
	for _, binding := range displayed.Task.Repositories {
		shown[binding.RepositoryID] = binding.BaseCommit
	}
	if len(shown) != 2 {
		t.Fatalf("the plan resolved %d repositories, want 2", len(shown))
	}

	// The remote moves on. Anything that resolved a base again from here would
	// produce a different commit.
	const moved = "aaaabbbbccccddddeeeeffff00001111aaaabbbb"
	arranged.git.resolveTo(moved)

	launched, err := arranged.service.LaunchDraft(context.Background(), draft.ID, displayed.Fingerprint)
	if err != nil {
		t.Fatalf("LaunchDraft: %v", err)
	}

	for _, binding := range launched.Repositories {
		if binding.BaseCommit != shown[binding.RepositoryID] {
			t.Errorf("repository %s started from %s, but %s was displayed",
				binding.RepositoryID, binding.BaseCommit, shown[binding.RepositoryID])
		}
	}

	// And the worktrees were created at the commits that were displayed, not at
	// the ones a fresh resolution would have found.
	for _, vector := range arranged.git.vectors() {
		if strings.HasPrefix(vector, "worktree add") && strings.Contains(vector, moved) {
			t.Errorf("a worktree was created at the moved commit: %q", vector)
		}
	}

	stored := arranged.reload(t, draft.ID)
	if stored.Workflow != domain.WorkflowPreparing {
		t.Errorf("workflow = %q, want preparing: slice 6 opens a task terminal and starts no agent",
			stored.Workflow)
	}
	if stored.Session == nil {
		t.Fatal("the launched task has no terminal")
	}
}

// TestADraftThatChangedAfterTheDisplayedPlanIsRefused is the other half of the
// same criterion.
//
// A user who edits a draft after resolving it has a screen that no longer
// describes what would be created. Feat refuses rather than re-resolving,
// because a plan the user never saw is not one they confirmed.
//
// The edited field is the brief, which is the case the fingerprint exists for:
// the brief is what the agent receives, the repositories still resolve to the
// same commits, and nothing else would notice.
func TestADraftThatChangedAfterTheDisplayedPlanIsRefused(t *testing.T) {
	arranged := arrangeDrafting(t)

	draft := arranged.draft(t, "Add a rate limit")
	arranged.selectRepositories(t, draft.ID)
	displayed := arranged.resolve(t, draft.ID)

	if _, err := arranged.service.UpdateDraft(context.Background(), draft.ID, api.DraftUpdate{
		Title: "Add a rate limit",
		Brief: "Add a rate limit to the public API, and delete the old one.",
		Repositories: []api.DraftSelection{
			{Repository: "api", Access: domain.TaskAccessReadWrite},
			{Repository: "store", Access: domain.TaskAccessReadOnly},
		},
	}); err != nil {
		t.Fatalf("UpdateDraft: %v", err)
	}

	// The plan itself survives an edit that did not change the selection, so
	// the user is not sent back to the network for a change to prose.
	edited := arranged.reload(t, draft.ID)
	for _, binding := range edited.Repositories {
		if binding.BaseCommit == "" {
			t.Errorf("editing the brief discarded the base resolved for %s", binding.RepositoryID)
		}
	}

	before := len(arranged.git.vectors())
	_, err := arranged.service.LaunchDraft(context.Background(), draft.ID, displayed.Fingerprint)
	if err == nil {
		t.Fatal("a draft that changed after its plan was displayed was launched anyway")
	}
	if !errors.Is(err, api.ErrInvalid) {
		t.Errorf("error = %v, want one the user can act on", err)
	}
	if !strings.Contains(err.Error(), "resolve the draft again") {
		t.Errorf("error = %v, want one naming what to do next", err)
	}

	if len(arranged.git.vectors()) != before {
		t.Error("Git ran for a launch that was refused")
	}
	if stored := arranged.reload(t, draft.ID); stored.Workflow != domain.WorkflowDraft {
		t.Errorf("workflow = %q, want a draft the user can still resolve", stored.Workflow)
	}

	// Reading the review screen again is what makes the confirmation valid.
	current := arranged.resolve(t, draft.ID)
	if _, err := arranged.service.LaunchDraft(context.Background(), draft.ID, current.Fingerprint); err != nil {
		t.Fatalf("launching the draft the user has now read: %v", err)
	}
}

// TestChangingTheSelectionDiscardsWhatWasResolvedForIt is the complement: a
// resolution belongs to one repository at one access, and must not outlive
// either.
func TestChangingTheSelectionDiscardsWhatWasResolvedForIt(t *testing.T) {
	arranged := arrangeDrafting(t)

	draft := arranged.draft(t, "Add a rate limit")
	arranged.selectRepositories(t, draft.ID)
	arranged.resolve(t, draft.ID)

	// The read-only repository is taken out and a promotion of nothing is
	// attempted: what remains must be the writable repository alone.
	if _, err := arranged.service.UpdateDraft(context.Background(), draft.ID, api.DraftUpdate{
		Title:        "Add a rate limit",
		Brief:        "Add a rate limit to the public API.",
		Repositories: []api.DraftSelection{{Repository: "api", Access: domain.TaskAccessReadOnly}},
	}); err != nil {
		t.Fatalf("UpdateDraft: %v", err)
	}

	stored := arranged.reload(t, draft.ID)
	if len(stored.Repositories) != 1 {
		t.Fatalf("the draft selects %d repositories, want 1", len(stored.Repositories))
	}
	binding := stored.Repositories[0]
	if binding.Access != domain.TaskAccessReadOnly {
		t.Errorf("access = %q, want the read-only the user chose", binding.Access)
	}
	if binding.BaseCommit != "" || binding.Branch != "" || binding.WorktreePath != "" {
		t.Errorf("repository %s kept %+v, resolved at a different access",
			binding.RepositoryID, binding)
	}
}

// TestSeveralDraftsAndLiveTasksCoexist is the slice 6 acceptance criterion that
// several task drafts and live tasks can coexist.
//
// Three drafts and two launched tasks are held at once, and each keeps its own
// identity: its own branch, its own worktrees, and its own terminal. The v0.1
// goal is three independent tasks running concurrently, so the interesting
// failure is one task's resources being recorded against another.
func TestSeveralDraftsAndLiveTasksCoexist(t *testing.T) {
	arranged := arrangeDrafting(t)
	ctx := context.Background()

	var launched []*domain.Task
	for _, title := range []string{"Add a rate limit", "Cache the report"} {
		draft := arranged.draft(t, title)
		arranged.selectRepositories(t, draft.ID)
		plan := arranged.resolve(t, draft.ID)

		task, err := arranged.service.LaunchDraft(ctx, draft.ID, plan.Fingerprint)
		if err != nil {
			t.Fatalf("launching %q: %v", title, err)
		}
		launched = append(launched, task)
	}

	var drafts []*domain.Task
	for _, title := range []string{"Retry failed exports", "Rotate the API keys", "Split the worker pool"} {
		draft := arranged.draft(t, title)
		arranged.selectRepositories(t, draft.ID)
		arranged.resolve(t, draft.ID)
		drafts = append(drafts, draft)
	}

	tasks, err := arranged.service.Tasks(ctx)
	if err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	if len(tasks) != 5 {
		t.Fatalf("the daemon lists %d tasks, want 5", len(tasks))
	}

	states := make(map[domain.TaskID]domain.WorkflowState, len(tasks))
	for _, task := range tasks {
		states[task.ID] = task.Workflow
	}
	for _, draft := range drafts {
		if states[draft.ID] != domain.WorkflowDraft {
			t.Errorf("draft %s is %q, want draft", draft.Key(), states[draft.ID])
		}
	}
	for _, task := range launched {
		if states[task.ID] != domain.WorkflowPreparing {
			t.Errorf("task %s is %q, want preparing", task.Key(), states[task.ID])
		}
	}

	// Nothing is shared. Two tasks sharing a branch, a worktree, or a terminal
	// is the defect this criterion exists to catch.
	branches := map[string]domain.TaskID{}
	worktrees := map[string]domain.TaskID{}
	panes := map[string]domain.TaskID{}
	for _, task := range tasks {
		for _, binding := range task.Repositories {
			if binding.Branch != "" {
				if other, taken := branches[binding.Branch]; taken {
					t.Errorf("tasks %s and %s share branch %s", other, task.ID, binding.Branch)
				}
				branches[binding.Branch] = task.ID
			}
			if binding.WorktreePath != "" {
				if other, taken := worktrees[binding.WorktreePath]; taken {
					t.Errorf("tasks %s and %s share worktree %s", other, task.ID, binding.WorktreePath)
				}
				worktrees[binding.WorktreePath] = task.ID
			}
		}
		if task.Session == nil {
			continue
		}
		if other, taken := panes[task.Session.Tmux.Pane]; taken {
			t.Errorf("tasks %s and %s share tmux pane %s", other, task.ID, task.Session.Tmux.Pane)
		}
		panes[task.Session.Tmux.Pane] = task.ID
	}

	// Cancelling one draft leaves every other task alone, which is the second
	// half of coexisting.
	if _, err := arranged.service.CancelDraft(ctx, drafts[0].ID); err != nil {
		t.Fatalf("CancelDraft: %v", err)
	}
	for _, task := range append(append([]*domain.Task(nil), launched...), drafts[1:]...) {
		reloaded := arranged.reload(t, task.ID)
		if reloaded.Workflow == domain.WorkflowArchived {
			t.Errorf("cancelling one draft archived task %s as well", task.Key())
		}
	}
}

// TestADraftTakesNoMoreAccessThanTheProjectAllows checks that the preparation
// step cannot promote a repository the project configured read-only.
//
// The Git adapter refuses this too, but a draft that recorded the promotion and
// failed only at launch would have shown the user a selection Feat was never
// going to honour.
func TestADraftTakesNoMoreAccessThanTheProjectAllows(t *testing.T) {
	arranged := arrangeDrafting(t)
	draft := arranged.draft(t, "Add a rate limit")

	_, err := arranged.service.UpdateDraft(context.Background(), draft.ID, api.DraftUpdate{
		Title: "Add a rate limit",
		Brief: "Add a rate limit to the public API.",
		Repositories: []api.DraftSelection{
			{Repository: "store", Access: domain.TaskAccessReadWrite},
		},
	})
	if err == nil {
		t.Fatal("a read-only repository was selected as read-write")
	}
	if !strings.Contains(err.Error(), "store") {
		t.Errorf("error = %v, want one naming the repository", err)
	}

	stored := arranged.reload(t, draft.ID)
	for _, binding := range stored.Repositories {
		if binding.RepositoryID == "store" && binding.Access == domain.TaskAccessReadWrite {
			t.Error("the refused selection was recorded anyway")
		}
	}
}

// TestAnUnresolvedDraftCannotBeLaunched checks that confirmation requires
// something to confirm.
//
// A draft nobody resolved has no base commits and no paths, so launching one
// would have to resolve them — which is exactly the re-planning that would
// break the displayed-snapshot criterion.
func TestAnUnresolvedDraftCannotBeLaunched(t *testing.T) {
	arranged := arrangeDrafting(t)

	draft := arranged.draft(t, "Add a rate limit")
	arranged.selectRepositories(t, draft.ID)

	_, err := arranged.service.LaunchDraft(context.Background(), draft.ID, Fingerprint(arranged.reload(t, draft.ID)))
	if err == nil {
		t.Fatal("an unresolved draft was launched")
	}
	if !strings.Contains(err.Error(), "resolve the draft before confirming it") {
		t.Errorf("error = %v, want one naming what is missing", err)
	}
}

// TestADraftIsRecordedBeforeItIsResolved checks that creating a draft creates a
// record and nothing else (FR-TASK-003).
func TestADraftIsRecordedBeforeItIsResolved(t *testing.T) {
	arranged := arrangeDrafting(t)
	draft := arranged.draft(t, "Add a rate limit")

	if draft.Workflow != domain.WorkflowDraft {
		t.Errorf("workflow = %q, want draft", draft.Workflow)
	}
	if vectors := arranged.git.vectors(); len(vectors) != 0 {
		t.Errorf("recording a draft ran Git: %v", vectors)
	}
	if calls := arranged.tmux.Calls(); len(calls) != 0 {
		t.Errorf("recording a draft ran tmux: %v", calls)
	}

	// The default selection is the project's, so the user starts from what
	// their configuration says rather than from an empty list.
	stored := arranged.reload(t, draft.ID)
	if len(stored.Repositories) != 2 {
		t.Fatalf("the draft selects %d repositories, want the project's 2 defaults", len(stored.Repositories))
	}
	for _, binding := range stored.Repositories {
		if binding.BaseCommit != "" || binding.WorktreePath != "" {
			t.Errorf("repository %s was resolved before the user asked: %+v", binding.RepositoryID, binding)
		}
	}
}

// TestAnOversizedBriefIsRefused checks the bound on a stored, mounted document
// the agent will read.
func TestAnOversizedBriefIsRefused(t *testing.T) {
	arranged := arrangeDrafting(t)

	_, err := arranged.service.CreateDraft(context.Background(), api.DraftRequest{
		Project: "app",
		Title:   "Add a rate limit",
		Brief:   strings.Repeat("x", maxBriefBytes+1),
		Source:  domain.TaskSource{Kind: domain.SourcePrompt},
	})
	if err == nil {
		t.Fatal("an oversized brief was accepted")
	}
	if !errors.Is(err, api.ErrInvalid) {
		t.Errorf("error = %v, want one the caller can act on", err)
	}
}

// TestEveryTaskKeyInAProjectIsDistinct checks ADR-026's collision rule.
//
// The key appears in branch names, worktree paths, and the dashboard, so two
// tasks sharing one would make the user's own shorthand ambiguous.
func TestEveryTaskKeyInAProjectIsDistinct(t *testing.T) {
	arranged := arrangeDrafting(t)

	seen := map[domain.TaskKey]bool{}
	for range 12 {
		draft := arranged.draft(t, "Add a rate limit")
		if seen[draft.Key()] {
			t.Fatalf("task key %s was issued twice", draft.Key())
		}
		seen[draft.Key()] = true
	}
}

// TestTheTaskShellOpensInThePrimaryWorkspace checks FR-TMUX-003: the task's
// terminal starts in the project's primary workspace, in the same execution
// profile as the agent.
//
// For this project that profile is a container, so the primary workspace is the
// container path the primary repository is mounted at, and the pane enters the
// task's own Compose project to get there. A shell on the host would open in a
// directory with the same files and a different everything else — no toolchain,
// no dependencies, and the user's own credentials.
func TestTheTaskShellOpensInThePrimaryWorkspace(t *testing.T) {
	arranged := arrangeDrafting(t)
	task := arranged.launched(t)

	if _, err := arranged.service.OpenShell(context.Background(), task.ID); err != nil {
		t.Fatalf("OpenShell: %v", err)
	}

	primary, ok := task.Repository("api")
	if !ok {
		t.Fatal("the launched task does not bind the primary repository")
	}

	// The pane is created empty and its command replaces the holder shell
	// afterwards (ADR-030), so what the shell runs is the second respawn.
	var shell string
	for _, call := range arranged.tmux.Calls() {
		joined := strings.Join(call, " ")
		if strings.HasPrefix(joined, "respawn-pane") && !strings.Contains(joined, claude.Executable) {
			shell = joined
		}
	}
	if shell == "" {
		t.Fatalf("no shell pane ran a command: %v", arranged.tmux.Calls())
	}

	if !strings.Contains(shell, "--workdir "+primary.ContainerPath) {
		t.Errorf("the shell did not open in the primary workspace %s: %s", primary.ContainerPath, shell)
	}
	if !strings.Contains(shell, "--project-name "+task.Session.Execution.Identity) {
		t.Errorf("the shell did not enter the task's own container: %s", shell)
	}
	if strings.Contains(shell, primary.WorktreePath) {
		t.Errorf("the shell ran against the host worktree path rather than inside the container: %s", shell)
	}
}
