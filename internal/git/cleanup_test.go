package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/domain"
)

// cleanupFixture arranges a finished task: two worktrees on disk, one branch,
// and a Git that knows about both.
func cleanupFixture(t *testing.T) (*fakeGit, CleanupRequest, string) {
	t.Helper()

	root := t.TempDir()
	api := filepath.Join(root, "api")
	store := filepath.Join(root, "store")
	for _, dir := range []string{api, store} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("arranging %s: %v", dir, err)
		}
	}

	branch := "feat/0f8fad5b-a-title"
	fake := newFakeGit()
	repository := &fakeRepository{
		refs: map[string]string{
			"refs/heads/" + branch:     commit("1234"),
			"refs/remotes/origin/main": commit("beef"),
			"refs/heads/main":          commit("beef"),
		},
		worktrees: []Worktree{
			{Path: "/checkout/api", Branch: "refs/heads/main"},
			{Path: api, Branch: "refs/heads/" + branch},
			{Path: store},
		},
		counts: map[string]int{commit("beef") + "..refs/heads/" + branch: 2},
	}
	fake.add("/checkout/api", repository)
	fake.add("/checkout/store", repository)
	fake.add(api, repository)
	fake.add(store, repository)

	return fake, CleanupRequest{
		Project: testProject,
		Task:    testTask,
		Root:    root,
		Repositories: []CleanupRepository{
			{
				ID: "api", HostPath: "/checkout/api", Remote: "origin",
				WorktreePath: api, Branch: branch,
				BaseRef: "refs/remotes/origin/main", BaseCommit: commit("beef"),
			},
			{
				ID: "store", HostPath: "/checkout/store", Remote: "origin",
				WorktreePath: store,
				BaseRef:      "refs/remotes/origin/main", BaseCommit: commit("beef"),
			},
		},
	}, root
}

// TestCleanupPlanSeparatesResourcesAndRemovesNothing checks FR-CLEAN-001 and
// FR-CLEAN-002: the exact task-owned resources are enumerated, worktrees and
// branches are separate choices, and producing the inventory changes nothing.
func TestCleanupPlanSeparatesResourcesAndRemovesNothing(t *testing.T) {
	fake, request, _ := cleanupFixture(t)

	plan, err := New(fake).CleanupPlanFor(context.Background(), request)
	if err != nil {
		t.Fatalf("planning cleanup: %v", err)
	}

	if len(plan.Worktrees) != 2 {
		t.Fatalf("planned %d worktrees, want one per selected repository", len(plan.Worktrees))
	}
	// A read-only repository has no branch, so cleanup must not offer one.
	if len(plan.Branches) != 1 || plan.Branches[0].Repository != "api" {
		t.Fatalf("planned %d branches, want only the read-write repository's: %+v",
			len(plan.Branches), plan.Branches)
	}
	for _, worktree := range plan.Worktrees {
		if !worktree.Present || !worktree.Registered {
			t.Errorf("worktree %s was reported present=%t registered=%t",
				worktree.Repository, worktree.Present, worktree.Registered)
		}
	}

	for _, vector := range fake.vectors() {
		for _, destructive := range []string{"worktree remove", "worktree prune", "branch -d", "branch -D", "clean"} {
			if strings.HasPrefix(vector, destructive) {
				t.Errorf("planning cleanup ran `git %s`, and a plan must remove nothing", vector)
			}
		}
	}
}

// TestDirtyAndUnmergedWorkIsWarnedAbout checks FR-CLEAN-003. The warnings are
// what a confirmation prompt is built from, so a target that would lose work
// must never be reported as ordinary.
func TestDirtyAndUnmergedWorkIsWarnedAbout(t *testing.T) {
	fake, request, root := cleanupFixture(t)
	fake.repositories["/checkout/api"].dirty[filepath.Join(root, "api")] = " M app.go"

	plan, err := New(fake).CleanupPlanFor(context.Background(), request)
	if err != nil {
		t.Fatalf("planning cleanup: %v", err)
	}

	worktree := plan.Worktrees[0]
	if !worktree.Dirty || !worktree.Risky() {
		t.Fatalf("the dirty worktree was reported as %+v", worktree)
	}
	if !strings.Contains(strings.Join(worktree.Warnings, " "), "uncommitted") {
		t.Errorf("the warnings do not mention uncommitted work: %v", worktree.Warnings)
	}

	branch := plan.Branches[0]
	if branch.Merged {
		t.Error("the branch was reported merged although the base does not contain it")
	}
	if branch.Unpushed != 2 || branch.Pushed {
		t.Errorf("the branch reports %d unpushed commits and pushed=%t, want 2 and false",
			branch.Unpushed, branch.Pushed)
	}
	joined := strings.Join(branch.Warnings, " ")
	if !strings.Contains(joined, "never pushed") || !strings.Contains(joined, "not merged") {
		t.Errorf("the branch warnings are %v, want both the unpushed and the unmerged one", branch.Warnings)
	}
}

// TestRecordedPathsOutsideTheRootAreRefused is the cleanup half of the
// unsafe-path criterion.
//
// A record can be edited, restored from a backup, or written by an older
// version. The moment a path from one of those decides what gets deleted, the
// record has stopped being a record and become an instruction.
func TestRecordedPathsOutsideTheRootAreRefused(t *testing.T) {
	fake, request, _ := cleanupFixture(t)
	request.Repositories[0].WorktreePath = "/etc"

	plan, err := New(fake).CleanupPlanFor(context.Background(), request)
	if err != nil {
		t.Fatalf("planning cleanup: %v", err)
	}

	for _, worktree := range plan.Worktrees {
		if worktree.Path == "/etc" {
			t.Fatal("a path outside the worktree root became a cleanup target")
		}
	}
	if len(plan.Problems) != 1 || plan.Problems[0].Repository != "api" {
		t.Fatalf("problems are %+v, want one naming the refused repository", plan.Problems)
	}
	if !strings.Contains(plan.Problems[0].Reason, "not a path Feat may remove") {
		t.Errorf("the problem does not say why the path was refused: %q", plan.Problems[0].Reason)
	}
}

// TestAnAlreadyRemovedWorktreeIsNotAProblem checks that a user who removed a
// worktree by hand can still clean the rest of the task up.
func TestAnAlreadyRemovedWorktreeIsNotAProblem(t *testing.T) {
	fake, request, root := cleanupFixture(t)
	if err := os.RemoveAll(filepath.Join(root, "store")); err != nil {
		t.Fatalf("removing the worktree: %v", err)
	}

	plan, err := New(fake).CleanupPlanFor(context.Background(), request)
	if err != nil {
		t.Fatalf("planning cleanup: %v", err)
	}
	if len(plan.Problems) != 0 {
		t.Fatalf("problems are %+v, want none for a worktree that is simply gone", plan.Problems)
	}

	var target WorktreeTarget
	for _, worktree := range plan.Worktrees {
		if worktree.Repository == domain.RepositoryID("store") {
			target = worktree
		}
	}
	if target.Present {
		t.Error("a worktree that no longer exists was reported present")
	}
}
