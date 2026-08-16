package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ma8el/feat/internal/config"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/git"
	"github.com/ma8el/feat/internal/paths"
	"github.com/ma8el/feat/internal/store"
)

// prepareFixture is a two-repository project: one the agent may write to, one
// the user chooses per task. The names are generic, because nothing about any
// real project may reach the binary (CLAUDE.md scope rule 3).
const prepareFixture = `version: 1

project:
  id: app
  name: Example Application
  primary_repository: api

repositories:
  api:
    host_path: ~/repos/app/api
    agent:
      container_path: /srv/api
    default_access: read_write
  store:
    host_path: ~/repos/app/store
    agent:
      container_path: /srv/store
    default_access: read_only

git:
  base_policy: remote
  branch_template: "feat/{task_key}-{slug}"

agent:
  execution:
    mode: devcontainer
    compose_files:
      - ~/repos/app/compose.yml
    service: dev
    user: developer
    working_directory: /srv/api
    control_path: /feat
`

// planned is the commit the fake resolves every remote-tracking base to.
const planned = "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d"

// fakeGit answers the Git commands the preparer sends.
//
// It is deliberately simpler than the fake in internal/git: what is under test
// here is the order in which the daemon records and creates, not Git's own
// behaviour, which the opt-in tests in internal/git check against Git itself.
type fakeGit struct {
	mu sync.Mutex
	// calls records every argument vector, in order.
	calls [][]string
	// worktrees are the paths created so far, by repository directory.
	worktrees map[string][]string
	// onAdd runs before a worktree is created, which is where a test can look
	// at what the record already says.
	onAdd func(path string)
	// onDiff runs before a comparison answers, which is where a test can hold
	// a review open and let something else write while it is in flight.
	onDiff func()
	// failAdd makes creating a worktree fail once its path ends in this
	// repository identifier.
	failAdd string
	// resolved is the commit remote-tracking bases resolve to. Changing it
	// between a plan and a launch is what a fetch that picked up new commits
	// would do.
	resolved string
	// head is what a task worktree has checked out, empty when the agent has
	// committed nothing.
	head string
	// changed and numstat are what a comparison against a recorded base finds:
	// the changed file names and the per-file line counts.
	changed string
	numstat string
	// dirty makes every worktree report uncommitted changes, which is what a
	// cleanup has to warn about before it removes one.
	dirty bool
	// branches are the refs the fake has, so a deletion can report whether
	// there was anything to delete.
	branches map[string]bool
	// failRemove makes removing a worktree fail once its path ends in this
	// repository identifier.
	failRemove string
}

func newFakeGit() *fakeGit {
	return &fakeGit{
		worktrees: make(map[string][]string),
		resolved:  planned,
		branches:  make(map[string]bool),
	}
}

// resolveTo moves the commit every remote-tracking base resolves to.
func (f *fakeGit) resolveTo(commit string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resolved = commit
}

func (f *fakeGit) vectors() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	rendered := make([]string, 0, len(f.calls))
	for _, call := range f.calls {
		rendered = append(rendered, strings.Join(call, " "))
	}
	return rendered
}

func (f *fakeGit) Run(_ context.Context, dir string, args ...string) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string(nil), args...))
	f.mu.Unlock()

	// A real command cannot run in a directory that is not there, and neither
	// can this one: a checkout that has been moved away must fail the same way
	// here as it would on the machine.
	if _, err := os.Stat(dir); err != nil {
		return "", fmt.Errorf("chdir %s: no such file or directory", dir)
	}

	if len(args) > 0 && args[0] == "--no-optional-locks" {
		args = args[1:]
	}

	switch {
	case args[0] == "rev-parse" && args[1] == "--git-dir":
		return ".git", nil

	case args[0] == "rev-parse" && strings.HasPrefix(args[len(args)-1], "HEAD"):
		// What a task worktree has checked out. A fake with none answers the
		// way a worktree with no commit does, which is the ordinary case while
		// an agent is working: committing is optional (FR-GIT-007).
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.head == "" {
			return "", &git.ExitError{Args: args, Dir: dir, Code: 1}
		}
		return f.head, nil

	case args[0] == "rev-parse":
		// Remote-tracking bases resolve; nothing else does, so a task branch
		// never collides and a base policy that is not remote is visibly
		// unresolvable — except for a branch the fake has been told exists,
		// which is how a cleanup finds something to delete.
		ref := args[len(args)-1]
		if strings.HasPrefix(ref, "refs/remotes/") {
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.resolved, nil
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.branches[strings.TrimPrefix(ref, "refs/heads/")] {
			return f.resolved, nil
		}
		return "", &git.ExitError{Args: args, Dir: dir, Code: 1}

	case args[0] == "fetch":
		return "", nil

	case args[0] == "worktree" && args[1] == "list":
		f.mu.Lock()
		defer f.mu.Unlock()
		var lines []string
		for _, path := range f.worktrees[dir] {
			lines = append(lines, "worktree "+path, "detached", "")
		}
		return strings.Join(lines, "\n"), nil

	case args[0] == "worktree" && args[1] == "remove":
		path := args[len(args)-1]
		if f.failRemove != "" && filepath.Base(path) == f.failRemove {
			return "", &git.ExitError{
				Args: args, Dir: dir, Code: 128,
				Stderr: "fatal: '" + path + "' contains modified or untracked files, use --force to delete it",
			}
		}
		if err := os.RemoveAll(path); err != nil {
			return "", err
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		remaining := make([]string, 0, len(f.worktrees[dir]))
		for _, existing := range f.worktrees[dir] {
			if existing != path {
				remaining = append(remaining, existing)
			}
		}
		f.worktrees[dir] = remaining
		return "", nil

	case args[0] == "worktree" && args[1] == "prune":
		return "", nil

	case args[0] == "branch":
		name := args[len(args)-1]
		f.mu.Lock()
		defer f.mu.Unlock()
		if !f.branches[name] {
			return "", &git.ExitError{
				Args: args, Dir: dir, Code: 1,
				Stderr: "error: branch '" + name + "' not found",
			}
		}
		delete(f.branches, name)
		return "", nil

	case args[0] == "worktree" && args[1] == "add":
		path := args[len(args)-2]
		if f.onAdd != nil {
			f.onAdd(path)
		}
		if f.failAdd != "" && filepath.Base(path) == f.failAdd {
			return "", &git.ExitError{
				Args: args, Dir: dir, Code: 128,
				Stderr: "fatal: could not create leading directories of '" + path + "'",
			}
		}
		// Real Git creates the directory as part of adding the worktree, and the
		// observation that follows runs in it.
		if err := os.MkdirAll(path, 0o755); err != nil {
			return "", err
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		f.worktrees[dir] = append(f.worktrees[dir], path)
		return "", nil

	case args[0] == "diff":
		f.mu.Lock()
		hold := f.onDiff
		f.mu.Unlock()
		if hold != nil {
			hold()
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, arg := range args {
			if arg == "--numstat" {
				return f.numstat, nil
			}
		}
		return f.changed, nil

	case args[0] == "status":
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.dirty {
			return " M internal/api/handler.go\n?? notes.md", nil
		}
		return "", nil

	case args[0] == "ls-files":
		return "", nil

	case args[0] == "rev-list":
		return "0", nil

	case args[0] == "merge-base":
		return "", &git.ExitError{Args: args, Dir: dir, Code: 1}

	default:
		return "", fmt.Errorf("fake git: unexpected command %q", strings.Join(args, " "))
	}
}

// preparation is a daemon with a registered project and a task draft.
type preparation struct {
	service *service
	fake    *fakeGit
	ref     store.TaskRef
	state   string
	home    string
	env     paths.Environment
}

// arrangeTask registers the fixture project and stores a draft ready to be
// prepared.
func arrangeTask(t *testing.T, fake *fakeGit) *preparation {
	t.Helper()
	return arrangeTaskWith(t, fake, prepareFixture)
}

// arrangeTaskWith is arrangeTask for a project configured differently.
func arrangeTaskWith(t *testing.T, fake *fakeGit, body string) *preparation {
	t.Helper()

	layout := testLayout(t)
	env := configured(t, layout, "app", body)

	// The checkouts the configuration names have to exist, because the fake
	// answers for the directories it is asked about rather than for any
	// directory.
	for _, name := range []string{"api", "store"} {
		// With a .git directory, because a task worktree is only a repository
		// while the main checkout's Git directory is reachable — which is what a
		// containerised task has to mount (ADR-033).
		if err := os.MkdirAll(filepath.Join(env.Home, "repos", "app", name, ".git"), 0o755); err != nil {
			t.Fatalf("creating the checkout %s: %v", name, err)
		}
	}

	now := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	instance, err := New(Options{
		Layout:      layout,
		Environment: env,
		Build:       testBuild,
		Git:         fake,
		Now:         func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	service := instance.service

	if _, err := service.RegisterProject(context.Background(), "app"); err != nil {
		t.Fatalf("registering the project: %v", err)
	}

	task, err := domain.NewTask(domain.NewTaskID(), "app", "Add a rate limit",
		domain.TaskSource{Kind: domain.SourcePrompt}, now)
	if err != nil {
		t.Fatalf("creating the draft: %v", err)
	}
	if err := task.SetBrief("Add a rate limit to the public API.", now); err != nil {
		t.Fatalf("setting the brief: %v", err)
	}
	if err := service.store.Tasks().Save(context.Background(), task); err != nil {
		t.Fatalf("saving the draft: %v", err)
	}

	return &preparation{
		service: service,
		fake:    fake,
		ref:     store.Ref(task),
		state:   layout.State,
		home:    env.Home,
		env:     env,
	}
}

// reload returns the task as it is recorded on disk, which is the only copy a
// restarted daemon would have.
func (p *preparation) reload(t *testing.T) *domain.Task {
	t.Helper()

	task, err := p.service.store.Tasks().Load(context.Background(), p.ref)
	if err != nil {
		t.Fatalf("loading the task: %v", err)
	}
	return task
}

// selection selects both repositories the way the project configures them.
func selection() []Selection {
	return []Selection{
		{Repository: "api", Access: domain.TaskAccessReadWrite},
		{Repository: "store", Access: domain.TaskAccessReadOnly},
	}
}

// TestPreparationRecordsEveryResourceBeforeCreatingIt is the ordering the slice
// 4 acceptance criterion about recoverability rests on.
//
// The record is written first and the worktrees are created afterwards, so that
// no worktree can exist that the record does not name. The assertion is made
// from inside the creation itself: when Git is asked to make a worktree, the
// snapshot on disk must already know about it.
func TestPreparationRecordsEveryResourceBeforeCreatingIt(t *testing.T) {
	fake := newFakeGit()
	arranged := arrangeTask(t, fake)

	var unrecorded []string
	fake.onAdd = func(path string) {
		snapshot, err := os.ReadFile(filepath.Join(arranged.state, "projects", "app",
			"tasks", arranged.ref.Task.String(), "task.json"))
		if err != nil {
			t.Errorf("reading the task snapshot while %s was being created: %v", path, err)
			return
		}
		if !strings.Contains(string(snapshot), path) {
			unrecorded = append(unrecorded, path)
		}
	}

	task, err := arranged.service.PrepareTask(context.Background(), arranged.ref, selection())
	if err != nil {
		t.Fatalf("preparing the task: %v", err)
	}
	if len(unrecorded) > 0 {
		t.Errorf("these worktrees were created before the record named them: %v", unrecorded)
	}

	if task.Workflow != domain.WorkflowPreparing {
		t.Errorf("the prepared task is %s, want preparing", task.Workflow)
	}
	if len(task.Repositories) != 2 {
		t.Fatalf("the task binds %d repositories, want 2", len(task.Repositories))
	}
	for _, binding := range arranged.reload(t).Repositories {
		if binding.Observation == nil {
			t.Errorf("repository %s has no observation, so nothing recorded that its worktree exists",
				binding.RepositoryID)
		}
	}
}

// TestPreparationMapsEachRepositoryToItsOwnResources is the slice 4 acceptance
// criterion that a two-repository task receives the correct branch and worktree
// mapping, and that read-only and read-write selections are recorded correctly.
func TestPreparationMapsEachRepositoryToItsOwnResources(t *testing.T) {
	fake := newFakeGit()
	arranged := arrangeTask(t, fake)

	task, err := arranged.service.PrepareTask(context.Background(), arranged.ref, selection())
	if err != nil {
		t.Fatalf("preparing the task: %v", err)
	}

	key := task.Key().String()
	root := filepath.Join(arranged.state, "worktrees", "app", task.ID.String())

	for _, want := range []domain.TaskRepository{
		{
			RepositoryID: "api",
			Access:       domain.TaskAccessReadWrite,
			BaseRef:      "refs/remotes/origin/main",
			BaseCommit:   planned,
			Branch:       "feat/" + key + "-add-a-rate-limit",
			WorktreePath: filepath.Join(root, "api"),
			// docs/07-configuration-model.md: the mount point comes from the
			// repository's configured container path.
			ContainerPath: "/srv/api",
		},
		{
			RepositoryID: "store",
			Access:       domain.TaskAccessReadOnly,
			BaseRef:      "refs/remotes/origin/main",
			BaseCommit:   planned,
			// A read-only repository has no branch (invariant 7).
			Branch:        "",
			WorktreePath:  filepath.Join(root, "store"),
			ContainerPath: "/srv/store",
		},
	} {
		binding, bound := task.Repository(want.RepositoryID)
		if !bound {
			t.Errorf("the task does not bind repository %s", want.RepositoryID)
			continue
		}
		got := *binding
		got.Observation = nil
		if got != want {
			t.Errorf("repository %s was recorded as\n %+v\nwant\n %+v", want.RepositoryID, got, want)
		}
	}

	// Each worktree was created from its own checkout, so one repository's tree
	// can never be checked out of another's.
	for repository, dir := range map[string]string{
		"api":   filepath.Join(arranged.home, "repos", "app", "api"),
		"store": filepath.Join(arranged.home, "repos", "app", "store"),
	} {
		created := fake.worktrees[dir]
		if len(created) != 1 || filepath.Base(created[0]) != repository {
			t.Errorf("checkout %s created %v, want one worktree for %s", dir, created, repository)
		}
	}
}

// TestAWorktreeRootThatNamesTheRepositoryIsUsedAsWritten checks the other
// reading of git.worktree_root.
//
// The root names the directory holding a task's worktrees. A template that
// already names the repository expands to one directory per repository; one
// that does not gets the repository appended. Both readings have to produce one
// worktree per repository, or the second checkout lands on top of the first.
func TestAWorktreeRootThatNamesTheRepositoryIsUsedAsWritten(t *testing.T) {
	fake := newFakeGit()
	arranged := arrangeTaskWith(t, fake, strings.Replace(prepareFixture,
		`  branch_template: "feat/{task_key}-{slug}"`,
		`  branch_template: "feat/{task_key}-{slug}"`+"\n"+
			`  worktree_root: "~/work/{project_id}/{repository_id}/{task_id}"`, 1))

	task, err := arranged.service.PrepareTask(context.Background(), arranged.ref, selection())
	if err != nil {
		t.Fatalf("preparing the task: %v", err)
	}

	for _, repository := range []domain.RepositoryID{"api", "store"} {
		binding, _ := task.Repository(repository)
		want := filepath.Join(arranged.home, "work", "app", repository.String(), task.ID.String())
		if binding.WorktreePath != want {
			t.Errorf("repository %s was given the worktree %q, want %q",
				repository, binding.WorktreePath, want)
		}
	}
}

// TestPreparationFailureLeavesARecoverableRecord is the slice 4 acceptance
// criterion that a failure halfway through creation leaves a recoverable record
// and no unidentified worktree, checked against the state a restarted daemon
// would read.
func TestPreparationFailureLeavesARecoverableRecord(t *testing.T) {
	fake := newFakeGit()
	fake.failAdd = "store"
	arranged := arrangeTask(t, fake)

	_, err := arranged.service.PrepareTask(context.Background(), arranged.ref, selection())
	if err == nil {
		t.Fatal("preparing succeeded although the second worktree could not be created")
	}
	if !strings.Contains(err.Error(), "store") {
		t.Errorf("the error does not name the repository that failed: %v", err)
	}

	recorded := arranged.reload(t)
	if recorded.Workflow != domain.WorkflowFailed {
		t.Errorf("the task is %s, want failed so that it can be resumed", recorded.Workflow)
	}

	// Both repositories are named, including the one that was never created:
	// the record is what a later reconciliation looks for, and a resource it
	// does not name is a resource nobody can find.
	if len(recorded.Repositories) != 2 {
		t.Fatalf("the record binds %d repositories, want both", len(recorded.Repositories))
	}
	for _, binding := range recorded.Repositories {
		if binding.WorktreePath == "" || binding.BaseCommit == "" {
			t.Errorf("repository %s was recorded without a path or a base: %+v",
				binding.RepositoryID, binding)
		}
	}

	// The observation is what says a worktree exists, and only the repository
	// that was created has one.
	api, _ := recorded.Repository("api")
	store, _ := recorded.Repository("store")
	if api.Observation == nil {
		t.Error("the repository that was created has no observation")
	}
	if store.Observation != nil {
		t.Error("the repository that failed was recorded as observed")
	}

	// Nothing was removed to tidy up: the worktree that exists is still there.
	for _, vector := range fake.vectors() {
		if strings.HasPrefix(vector, "worktree remove") || strings.HasPrefix(vector, "branch -D") {
			t.Errorf("the failed preparation removed something: `git %s`", vector)
		}
	}

	// The history explains what happened.
	log, err := arranged.service.store.Events().Replay(context.Background(), arranged.ref)
	if err != nil {
		t.Fatalf("replaying the task history: %v", err)
	}
	var sawFailure bool
	for _, event := range log.Events {
		if event.Type == domain.EventWorkflowChanged && event.To == string(domain.WorkflowFailed) {
			sawFailure = strings.Contains(event.Detail, "store")
		}
	}
	if !sawFailure {
		t.Errorf("the task history does not record why preparation failed: %+v", log.Events)
	}
}

// TestATaskWithNoBriefIsRefusedBeforeAnyRepositoryIsTouched checks that the
// cheap refusal happens before the visible one: a task that cannot launch
// should not cause a fetch on the user's repositories first.
func TestATaskWithNoBriefIsRefusedBeforeAnyRepositoryIsTouched(t *testing.T) {
	fake := newFakeGit()
	arranged := arrangeTask(t, fake)

	task := arranged.reload(t)
	if err := task.SetBrief("", time.Now()); err != nil {
		t.Fatalf("clearing the brief: %v", err)
	}
	if err := arranged.service.store.Tasks().Save(context.Background(), task); err != nil {
		t.Fatalf("saving the draft: %v", err)
	}

	_, err := arranged.service.PrepareTask(context.Background(), arranged.ref, selection())
	if err == nil {
		t.Fatal("a task with no brief was prepared")
	}
	if !strings.Contains(err.Error(), "no brief") {
		t.Errorf("the error is %v, want one naming the missing brief", err)
	}
	if vectors := fake.vectors(); len(vectors) != 0 {
		t.Errorf("Git ran anyway: %v", vectors)
	}
}

// TestOnlyADraftIsPrepared checks that a task whose resources exist is never
// prepared a second time, which would create a second set of them.
func TestOnlyADraftIsPrepared(t *testing.T) {
	fake := newFakeGit()
	arranged := arrangeTask(t, fake)

	if _, err := arranged.service.PrepareTask(context.Background(), arranged.ref, selection()); err != nil {
		t.Fatalf("preparing the task: %v", err)
	}
	before := len(fake.vectors())

	_, err := arranged.service.PrepareTask(context.Background(), arranged.ref, selection())
	if err == nil {
		t.Fatal("a task that was already prepared was prepared again")
	}
	if !strings.Contains(err.Error(), "only a draft can be prepared") {
		t.Errorf("the error is %v, want one explaining that the task is not a draft", err)
	}
	if len(fake.vectors()) != before {
		t.Error("Git ran for a task that was not a draft")
	}
}

// TestAReadOnlyRepositoryCannotBePromoted checks that a repository a project
// declared read-only does not become writable because one task asked.
func TestAReadOnlyRepositoryCannotBePromoted(t *testing.T) {
	fake := newFakeGit()
	arranged := arrangeTask(t, fake)

	_, err := arranged.service.PrepareTask(context.Background(), arranged.ref, []Selection{
		{Repository: "api", Access: domain.TaskAccessReadWrite},
		{Repository: "store", Access: domain.TaskAccessReadWrite},
	})
	if err == nil {
		t.Fatal("a read-only repository was selected as read-write")
	}
	if !strings.Contains(err.Error(), "store") {
		t.Errorf("the error does not name the repository: %v", err)
	}
	if vectors := fake.vectors(); len(vectors) != 0 {
		t.Errorf("Git ran for a selection that was refused: %v", vectors)
	}
}

// TestAPlanThatCannotBeAppliedLeavesTheTaskADraft checks FR-TASK-003 from the
// other side: a task whose repositories could not be resolved has created
// nothing and is still editable.
func TestAPlanThatCannotBeAppliedLeavesTheTaskADraft(t *testing.T) {
	fake := newFakeGit()
	arranged := arrangeTask(t, fake)

	// The checkout is gone, so the plan cannot resolve it.
	if err := os.RemoveAll(filepath.Join(arranged.home, "repos", "app", "store")); err != nil {
		t.Fatalf("removing the checkout: %v", err)
	}
	fake.onAdd = func(path string) { t.Errorf("a worktree was created at %s", path) }

	_, err := arranged.service.PrepareTask(context.Background(), arranged.ref, selection())
	if err == nil {
		t.Fatal("preparing succeeded with an unresolvable repository")
	}

	recorded := arranged.reload(t)
	if recorded.Workflow != domain.WorkflowDraft {
		t.Errorf("the task is %s, want a draft the user can still change", recorded.Workflow)
	}
	// The selection survives, because it is the user's own answer to which
	// repositories the task is about and they should not have to give it again.
	// What must not survive is a resolved plan: no base commit, no branch, and
	// no worktree path, because none of them was resolved.
	if len(recorded.Repositories) != len(selection()) {
		t.Errorf("the draft records %d repositories, want the %d the user selected",
			len(recorded.Repositories), len(selection()))
	}
	for _, binding := range recorded.Repositories {
		if binding.BaseCommit != "" || binding.Branch != "" || binding.WorktreePath != "" {
			t.Errorf("repository %s recorded %+v from a plan that never resolved",
				binding.RepositoryID, binding)
		}
	}
}

// TestDefaultSelectionLeavesTheChoicesToTheUser checks that a repository the
// configuration marks selectable, stable read-only, or omitted is not included
// on the user's behalf.
func TestDefaultSelectionLeavesTheChoicesToTheUser(t *testing.T) {
	fake := newFakeGit()
	arranged := arrangeTask(t, fake)

	cfg, err := config.Load(arranged.service.layout.ProjectConfigDir(), "app",
		arranged.service.configOptions())
	if err != nil {
		t.Fatalf("loading the configuration: %v", err)
	}

	selected := DefaultSelection(cfg)
	if len(selected) != 2 {
		t.Fatalf("selected %+v, want the read-write and the read-only repository", selected)
	}
	if selected[0].Access != domain.TaskAccessReadWrite || selected[1].Access != domain.TaskAccessReadOnly {
		t.Errorf("the default selection is %+v, want each repository at its configured access", selected)
	}
}

// TestPreparationSurvivesAnUnwritableEventLog checks that history is an
// explanation rather than a precondition: losing it must not fail an operation
// that changed the world.
func TestPreparationSurvivesAnUnwritableEventLog(t *testing.T) {
	fake := newFakeGit()
	arranged := arrangeTask(t, fake)

	// The events file is replaced by a directory, which no append can write to.
	events := filepath.Join(arranged.state, "projects", "app", "tasks",
		arranged.ref.Task.String(), "events.jsonl")
	if err := os.RemoveAll(events); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("removing the event log: %v", err)
	}
	if err := os.MkdirAll(events, 0o755); err != nil {
		t.Fatalf("blocking the event log: %v", err)
	}

	task, err := arranged.service.PrepareTask(context.Background(), arranged.ref, selection())
	if err != nil {
		t.Fatalf("preparing the task failed because its history could not be written: %v", err)
	}
	if task.Workflow != domain.WorkflowPreparing {
		t.Errorf("the task is %s, want preparing", task.Workflow)
	}
}
