package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/domain"
)

// envIntegration opts a run in to the tests that use real Git.
const envIntegration = "FEAT_INTEGRATION"

// requireGit skips unless the run is opted in and Git is installed.
//
// A fake runner decides what Git would say, which is enough to pin an argument
// vector and not enough to know that a flag exists, that the output has the
// shape the parser expects, or that a worktree behaves the way this package
// assumes. These tests ask Git itself. Set FEAT_INTEGRATION=1 to run them; CI
// does, on macOS and Linux.
func requireGit(t *testing.T) {
	t.Helper()

	if os.Getenv(envIntegration) == "" {
		t.Skipf("set %s=1 to run the tests that use real Git", envIntegration)
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	// Both this test and the adapter it drives inherit these, so neither reads
	// the developer's own Git configuration, hooks, or templates.
	for name, value := range map[string]string{
		"GIT_CONFIG_GLOBAL":      os.DevNull,
		"GIT_CONFIG_SYSTEM":      os.DevNull,
		"GIT_AUTHOR_NAME":        "Feat Test",
		"GIT_AUTHOR_EMAIL":       "test@example.invalid",
		"GIT_COMMITTER_NAME":     "Feat Test",
		"GIT_COMMITTER_EMAIL":    "test@example.invalid",
		"GIT_TERMINAL_PROMPT":    "0",
		"GIT_ALLOW_PROTOCOL":     "file",
		"GIT_PROTOCOL_FROM_USER": "0",
	} {
		t.Setenv(name, value)
	}
}

// git runs a real Git command in a test.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()

	command := exec.Command("git", args...)
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("`git %s` in %s: %v\n%s", strings.Join(args, " "), dir, err, output)
	}
	return strings.TrimSpace(string(output))
}

// write creates or replaces a file in a working tree.
func write(t *testing.T, dir, name, content string) {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("creating %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// origin creates a bare repository with one commit on main, and returns its
// path. It stands in for a remote without needing a network.
func origin(t *testing.T, dir, name string) string {
	t.Helper()

	bare := filepath.Join(dir, name+".git")
	git(t, dir, "init", "--bare", "--initial-branch=main", bare)

	seed := filepath.Join(dir, name+"-seed")
	git(t, dir, "init", "--initial-branch=main", seed)
	write(t, seed, "README.md", "# "+name+"\n")
	git(t, seed, "add", "README.md")
	git(t, seed, "commit", "-m", "initial commit")
	git(t, seed, "remote", "add", "origin", bare)
	git(t, seed, "push", "origin", "main")

	return bare
}

// checkout clones a bare repository into dir/name and returns the clone's path.
func checkout(t *testing.T, dir, bare, name string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	git(t, dir, "clone", bare, path)
	return path
}

// realFixture is a two-repository project with real Git behind it.
type realFixture struct {
	dir    string
	root   string
	api    string
	store  string
	origin map[string]string
	req    Request
}

// realProject arranges two repositories, each with a remote, and the request
// for a task that takes part in both: one read-write, one read-only.
func realProject(t *testing.T) *realFixture {
	t.Helper()

	dir := t.TempDir()
	remotes := filepath.Join(dir, "remotes")
	if err := os.MkdirAll(remotes, 0o755); err != nil {
		t.Fatalf("creating %s: %v", remotes, err)
	}

	apiOrigin := origin(t, remotes, "api")
	storeOrigin := origin(t, remotes, "store")

	checkouts := filepath.Join(dir, "checkouts")
	if err := os.MkdirAll(checkouts, 0o755); err != nil {
		t.Fatalf("creating %s: %v", checkouts, err)
	}
	api := checkout(t, checkouts, apiOrigin, "api")
	store := checkout(t, checkouts, storeOrigin, "store")

	root := filepath.Join(dir, "state", "worktrees")
	taskRoot := filepath.Join(root, testProject.String(), testTask.String())

	return &realFixture{
		dir:    dir,
		root:   root,
		api:    api,
		store:  store,
		origin: map[string]string{"api": apiOrigin, "store": storeOrigin},
		req: Request{
			Project: testProject,
			Task:    testTask,
			Root:    root,
			Fetch:   true,
			Repositories: []RepositoryRequest{
				{
					ID: "api", HostPath: api, Remote: "origin", DefaultBranch: "main",
					Access: domain.TaskAccessReadWrite, Policy: PolicyRemote,
					Branch: "feat/0f8fad5b-a-title", WorktreePath: filepath.Join(taskRoot, "api"),
					ContainerPath: "/srv/api",
				},
				{
					ID: "store", HostPath: store, Remote: "origin", DefaultBranch: "main",
					Access: domain.TaskAccessReadOnly, Policy: PolicyRemote,
					WorktreePath: filepath.Join(taskRoot, "store"), ContainerPath: "/srv/store",
				},
			},
		},
	}
}

// TestRealDirtyCheckoutIsPreservedAndDoesNotBlockATask is the slice 4
// acceptance criterion that dirty changes in the ordinary checkout are
// preserved and do not block an independent task (FR-GIT-003).
//
// The check is byte-for-byte on the porcelain status, and includes the branch,
// HEAD, and the index, because "preserved" has to mean all of them: a stray
// `git stash`, `git checkout`, or `git pull` would show up in exactly one of
// these and in none of the others.
func TestRealDirtyCheckoutIsPreservedAndDoesNotBlockATask(t *testing.T) {
	requireGit(t)
	f := realProject(t)

	write(t, f.api, "README.md", "# api\n\nuncommitted edit\n")
	write(t, f.api, "staged.txt", "staged\n")
	git(t, f.api, "add", "staged.txt")
	write(t, f.api, "untracked.txt", "untracked\n")

	before := map[string]string{
		"status": git(t, f.api, "status", "--porcelain=v1", "--branch"),
		"head":   git(t, f.api, "rev-parse", "HEAD"),
		"branch": git(t, f.api, "symbolic-ref", "HEAD"),
		"stash":  git(t, f.api, "stash", "list"),
	}

	adapter := Host()
	plan, err := adapter.Plan(context.Background(), f.req)
	if err != nil {
		t.Fatalf("planning a task beside a dirty checkout: %v", err)
	}
	if _, err := adapter.Apply(context.Background(), plan, JournalFunc(
		func(context.Context, Created) error { return nil },
	)); err != nil {
		t.Fatalf("applying: %v", err)
	}

	for name, want := range before {
		if got := git(t, f.api, gitQuery(name)...); got != want {
			t.Errorf("the ordinary checkout's %s changed:\n before: %q\n  after: %q", name, want, got)
		}
	}

	// The task itself is real: the worktree exists and is checked out at the
	// base the plan resolved, unaffected by the uncommitted work next to it.
	worktree := plan.Repositories[0].WorktreePath
	if got := git(t, worktree, "rev-parse", "HEAD"); got != plan.Repositories[0].BaseCommit {
		t.Errorf("the task worktree is at %s, want the resolved base %s", got, plan.Repositories[0].BaseCommit)
	}
	if _, err := os.Stat(filepath.Join(worktree, "untracked.txt")); err == nil {
		t.Error("the task worktree contains the ordinary checkout's untracked file")
	}
}

// gitQuery maps a recorded property to the command that reads it.
func gitQuery(name string) []string {
	switch name {
	case "status":
		return []string{"status", "--porcelain=v1", "--branch"}
	case "head":
		return []string{"rev-parse", "HEAD"}
	case "branch":
		return []string{"symbolic-ref", "HEAD"}
	default:
		return []string{"stash", "list"}
	}
}

// TestRealRemoteBaseUsesTheFetchedCommit is the slice 4 acceptance criterion
// that remote base resolution uses the fetched remote-tracking commit.
//
// The distinction only becomes visible when the three candidate commits differ:
// what this checkout has, what its remote-tracking ref had before the fetch, and
// what the remote actually holds. A colleague pushes, and the task must start
// from what they pushed.
func TestRealRemoteBaseUsesTheFetchedCommit(t *testing.T) {
	requireGit(t)
	f := realProject(t)

	local := git(t, f.api, "rev-parse", "HEAD")
	stale := git(t, f.api, "rev-parse", "refs/remotes/origin/main")

	// A second clone pushes a commit this checkout has never seen.
	colleague := checkout(t, f.dir, f.origin["api"], "colleague")
	write(t, colleague, "feature.txt", "from somebody else\n")
	git(t, colleague, "add", "feature.txt")
	git(t, colleague, "commit", "-m", "a commit this checkout has not seen")
	git(t, colleague, "push", "origin", "main")
	pushed := git(t, colleague, "rev-parse", "HEAD")

	if pushed == local || stale != local {
		t.Fatalf("the fixture does not distinguish the commits: local=%s stale=%s pushed=%s",
			local, stale, pushed)
	}

	plan, err := Host().Plan(context.Background(), f.req)
	if err != nil {
		t.Fatalf("planning: %v", err)
	}

	if got := plan.Repositories[0].BaseCommit; got != pushed {
		t.Errorf("the base is %s, want the fetched remote-tracking commit %s", got, pushed)
	}
	if plan.Repositories[0].BaseRef != "refs/remotes/origin/main" {
		t.Errorf("the recorded base ref is %q", plan.Repositories[0].BaseRef)
	}

	// Fetching updated the remote-tracking ref and nothing else: the user's own
	// branch is where they left it (FR-GIT-001).
	if got := git(t, f.api, "rev-parse", "refs/heads/main"); got != local {
		t.Errorf("the local branch moved to %s, and Feat must never pull", got)
	}
	if got := git(t, f.api, "rev-parse", "HEAD"); got != local {
		t.Errorf("the checkout moved to %s, and Feat must never change it", got)
	}
}

// TestRealTwoRepositoryTaskMapping is the slice 4 acceptance criterion that a
// two-repository task receives the correct branch and worktree mapping, and
// that read-only and read-write selections are recorded correctly.
func TestRealTwoRepositoryTaskMapping(t *testing.T) {
	requireGit(t)
	f := realProject(t)

	adapter := Host()
	plan, err := adapter.Plan(context.Background(), f.req)
	if err != nil {
		t.Fatalf("planning: %v", err)
	}

	var recorded []Created
	result, err := adapter.Apply(context.Background(), plan, JournalFunc(
		func(_ context.Context, created Created) error {
			recorded = append(recorded, created)
			return nil
		}))
	if err != nil {
		t.Fatalf("applying: %v", err)
	}
	if len(result.Created) != 2 || len(recorded) != 2 {
		t.Fatalf("created %d and recorded %d repositories, want 2 of each",
			len(result.Created), len(recorded))
	}

	api, store := plan.Repositories[0], plan.Repositories[1]

	// The read-write repository is on its own branch, and the branch is in the
	// repository it came from rather than anywhere else.
	if got := git(t, api.WorktreePath, "symbolic-ref", "--short", "HEAD"); got != api.Branch {
		t.Errorf("the read-write worktree is on %q, want the task branch %q", got, api.Branch)
	}
	if got := git(t, f.api, "rev-parse", "--verify", "refs/heads/"+api.Branch); got != api.BaseCommit {
		t.Errorf("the task branch starts at %s, want the recorded base %s", got, api.BaseCommit)
	}

	// The read-only repository has a reproducible worktree at the same base and
	// no branch at all, so nothing the agent does can commit to one by accident
	// (FR-GIT-005, invariant 7).
	if _, err := exec.Command("git", "-C", store.WorktreePath, "symbolic-ref", "HEAD").Output(); err == nil {
		t.Error("the read-only worktree is attached to a branch, want a detached HEAD")
	}
	if got := git(t, store.WorktreePath, "rev-parse", "HEAD"); got != store.BaseCommit {
		t.Errorf("the read-only worktree is at %s, want the recorded base %s", got, store.BaseCommit)
	}
	if store.Branch != "" {
		t.Errorf("a branch %q was recorded for a read-only repository", store.Branch)
	}

	// Each repository's worktree is registered with its own checkout, and the
	// two tasks' directories are separate.
	for _, repository := range []struct {
		checkout string
		plan     RepositoryPlan
	}{{f.api, api}, {f.store, store}} {
		listed := git(t, repository.checkout, "worktree", "list", "--porcelain")
		if !strings.Contains(listed, repository.plan.WorktreePath) {
			t.Errorf("%s does not list the task worktree %s:\n%s",
				repository.checkout, repository.plan.WorktreePath, listed)
		}
	}
	if api.WorktreePath == store.WorktreePath {
		t.Fatal("both repositories were given the same worktree")
	}
	if api.ContainerPath == store.ContainerPath {
		t.Error("both repositories were recorded at the same container path")
	}
}

// TestRealFailureHalfwayLeavesNoUnidentifiedWorktree is the slice 4 acceptance
// criterion that a failure halfway through creation leaves a recoverable record
// and no unidentified worktree.
//
// "Unidentified" is checked literally: every directory under the task's
// worktree root and every worktree Git has registered must be one the plan
// names. The plan is what the daemon has already written down at this point, so
// anything outside it would be a resource nothing knows about.
func TestRealFailureHalfwayLeavesNoUnidentifiedWorktree(t *testing.T) {
	requireGit(t)
	f := realProject(t)

	adapter := Host()
	plan, err := adapter.Plan(context.Background(), f.req)
	if err != nil {
		t.Fatalf("planning: %v", err)
	}

	// Something takes the second repository's path between the plan and its
	// application, which is exactly when a half-finished launch happens.
	blocked := plan.Repositories[1].WorktreePath
	if err := os.MkdirAll(filepath.Dir(blocked), 0o755); err != nil {
		t.Fatalf("creating %s: %v", filepath.Dir(blocked), err)
	}
	write(t, filepath.Dir(blocked), filepath.Base(blocked), "in the way\n")

	var recorded []Created
	result, err := adapter.Apply(context.Background(), plan, JournalFunc(
		func(_ context.Context, created Created) error {
			recorded = append(recorded, created)
			return nil
		}))
	if err == nil {
		t.Fatal("applying succeeded although the second worktree could not be created")
	}
	if len(result.Created) != 1 || len(recorded) != 1 {
		t.Fatalf("created %d and recorded %d repositories, want exactly the first",
			len(result.Created), len(recorded))
	}

	// Git reports resolved paths, and on macOS a temporary directory reaches the
	// same place through /private, so both sides are resolved before they are
	// compared.
	named := map[string]bool{}
	for _, repository := range plan.Repositories {
		named[resolvePath(repository.WorktreePath)] = true
	}

	// Nothing under the task root that the plan does not name.
	taskRoot := filepath.Dir(plan.Repositories[0].WorktreePath)
	entries, err := os.ReadDir(taskRoot)
	if err != nil {
		t.Fatalf("reading the task root: %v", err)
	}
	for _, entry := range entries {
		if path := filepath.Join(taskRoot, entry.Name()); !named[resolvePath(path)] {
			t.Errorf("%s exists and the record does not name it", path)
		}
	}

	// Nothing registered with Git that the plan does not name.
	for _, repository := range []string{f.api, f.store} {
		for _, worktree := range parseWorktrees(git(t, repository, "worktree", "list", "--porcelain")) {
			resolved := resolvePath(worktree.Path)
			if resolved == resolvePath(repository) {
				continue // the user's ordinary checkout
			}
			if !named[resolved] {
				t.Errorf("%s has a worktree at %s that the record does not name", repository, worktree.Path)
			}
		}
	}

	// The repository that failed has no half-made branch either.
	if _, err := exec.Command("git", "-C", f.store, "rev-parse", "--verify",
		"refs/heads/"+plan.Repositories[0].Branch).Output(); err == nil {
		t.Error("the failed repository was left with a task branch")
	}

	// What was created is still there: a launch that failed is recovered from,
	// not tidied away.
	if _, err := os.Stat(plan.Repositories[0].WorktreePath); err != nil {
		t.Errorf("the worktree that was created is gone: %v", err)
	}
}

// TestRealCleanupPlanSeesDirtyAndUnmergedWork checks that the inventory a later
// slice acts on reports the risks Git actually knows about, rather than the ones
// a fake was told to report.
func TestRealCleanupPlanSeesDirtyAndUnmergedWork(t *testing.T) {
	requireGit(t)
	f := realProject(t)

	adapter := Host()
	plan, err := adapter.Plan(context.Background(), f.req)
	if err != nil {
		t.Fatalf("planning: %v", err)
	}
	if _, err := adapter.Apply(context.Background(), plan, JournalFunc(
		func(context.Context, Created) error { return nil },
	)); err != nil {
		t.Fatalf("applying: %v", err)
	}

	// The agent committed one change and left another uncommitted, which is the
	// state a task is normally reviewed in (FR-GIT-007).
	worktree := plan.Repositories[0].WorktreePath
	write(t, worktree, "feature.go", "package feature\n")
	git(t, worktree, "add", "feature.go")
	git(t, worktree, "commit", "-m", "the agent's work")
	write(t, worktree, "scratch.txt", "not committed\n")

	cleanup, err := adapter.CleanupPlanFor(context.Background(), CleanupRequest{
		Project: testProject,
		Task:    testTask,
		Root:    f.root,
		Repositories: []CleanupRepository{{
			ID: "api", HostPath: f.api, Remote: "origin",
			WorktreePath: worktree, Branch: plan.Repositories[0].Branch,
			BaseRef: plan.Repositories[0].BaseRef, BaseCommit: plan.Repositories[0].BaseCommit,
		}},
	})
	if err != nil {
		t.Fatalf("planning cleanup: %v", err)
	}
	if len(cleanup.Problems) != 0 {
		t.Fatalf("the cleanup plan has problems: %+v", cleanup.Problems)
	}

	target := cleanup.Worktrees[0]
	if !target.Present || !target.Registered || !target.Dirty {
		t.Errorf("the worktree was reported as %+v", target)
	}
	branch := cleanup.Branches[0]
	if branch.Unpushed != 1 || branch.Merged || branch.Pushed {
		t.Errorf("the branch was reported as %+v, want one unpushed, unmerged, never-pushed commit", branch)
	}

	// The observation the dashboard shows agrees with it.
	observation, err := adapter.Observe(context.Background(), ObserveRequest{
		WorktreePath: worktree,
		BaseRef:      plan.Repositories[0].BaseRef,
		BaseCommit:   plan.Repositories[0].BaseCommit,
	})
	if err != nil {
		t.Fatalf("observing: %v", err)
	}
	if observation.Ahead != 1 || !observation.Dirty || observation.ChangedFiles != 2 {
		t.Errorf("the observation is %+v, want one commit ahead, dirty, and two changed files", observation)
	}
}
