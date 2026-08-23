package git

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// removalFixture arranges a task at the layout the default configuration
// generates: one directory per project and task, with a worktree per repository
// inside it.
//
// The root is the fixed directory Feat owns, and it is deliberately not the
// parent of the worktrees: the directories between the two are the ones a task
// is given and nothing else uses, which is what makes them removable with it.
func removalFixture(t *testing.T) (*fakeGit, map[string]string, string) {
	t.Helper()

	root := filepath.Join(t.TempDir(), "worktrees")
	taskDir := filepath.Join(root, testProject.String(), testTask.String())
	paths := map[string]string{
		"api":     filepath.Join(taskDir, "api"),
		"store":   filepath.Join(taskDir, "store"),
		"task":    taskDir,
		"project": filepath.Dir(taskDir),
		"root":    root,
	}
	for _, id := range []string{"api", "store"} {
		if err := os.MkdirAll(paths[id], 0o755); err != nil {
			t.Fatalf("arranging %s: %v", paths[id], err)
		}
	}

	fake := newFakeGit()
	repository := &fakeRepository{
		worktrees: []Worktree{
			{Path: "/checkout/api", Branch: "refs/heads/main"},
			{Path: paths["api"], Branch: "refs/heads/feat/0f8fad5b-a-title"},
			{Path: paths["store"]},
		},
	}
	fake.add("/checkout/api", repository)
	fake.add("/checkout/store", repository)
	return fake, paths, root
}

// TestRemovingTheLastWorktreeTakesTheDirectoriesTheTaskWasGiven is the residue
// half of FR-CLEAN-001.
//
// Creating a task creates the directories its worktrees sit in, so cleaning it
// up removes them: what a cleanup leaves behind on the machine is what the next
// recovery pass asks the user about, and a directory that outlived every
// resource it was made for is a question with no answer worth giving.
//
// Both halves are checked here. The first worktree takes nothing above it,
// because the task's other worktree is still there and a directory holding
// something is never removed; the second takes the task's directory and the
// project's, and stops at the root.
func TestRemovingTheLastWorktreeTakesTheDirectoriesTheTaskWasGiven(t *testing.T) {
	fake, paths, root := removalFixture(t)
	adapter := New(fake)
	request := RemoveRequest{
		HostPath:  "/checkout/api",
		Root:      root,
		Checkouts: []string{"/checkout/api", "/checkout/store"},
	}

	first, err := adapter.RemoveWorktree(context.Background(), paths["api"], request)
	if err != nil {
		t.Fatalf("removing the first worktree: %v", err)
	}
	if !first.Removed {
		t.Error("removing a worktree that was there reported that nothing went")
	}
	if len(first.Directories) != 0 {
		t.Errorf("removing one of two worktrees took %v", first.Directories)
	}
	if _, err := os.Stat(paths["task"]); err != nil {
		t.Fatalf("the directory holding the task's other worktree is gone: %v", err)
	}

	second, err := adapter.RemoveWorktree(context.Background(), paths["store"], request)
	if err != nil {
		t.Fatalf("removing the second worktree: %v", err)
	}
	if want := []string{paths["task"], paths["project"]}; !slices.Equal(second.Directories, want) {
		t.Errorf("the removal reported %v, want %v", second.Directories, want)
	}
	for _, name := range []string{"task", "project"} {
		if _, err := os.Stat(paths[name]); !os.IsNotExist(err) {
			t.Errorf("the %s directory is still there: %v", name, err)
		}
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("the worktree root every other task is created in is gone: %v", err)
	}
}

// TestAWorktreeRemovedByHandStillLosesItsDirectories is the same tidy-up on the
// path that reaches Git differently.
//
// A worktree somebody removed themselves leaves both a stale registration and
// the directories above it. Cleanup of it succeeds — the user asked for it to be
// absent and it is — and it must leave the machine in the state a cleanup leaves
// it in, or the same orphan is reported for a task that was cleaned up properly.
func TestAWorktreeRemovedByHandStillLosesItsDirectories(t *testing.T) {
	fake, paths, root := removalFixture(t)
	for _, id := range []string{"api", "store"} {
		if err := os.RemoveAll(paths[id]); err != nil {
			t.Fatalf("removing %s by hand: %v", paths[id], err)
		}
	}

	removal, err := New(fake).RemoveWorktree(context.Background(), paths["api"], RemoveRequest{
		HostPath:  "/checkout/api",
		Root:      root,
		Checkouts: []string{"/checkout/api", "/checkout/store"},
	})
	if err != nil {
		t.Fatalf("removing an absent worktree: %v", err)
	}
	if removal.Removed {
		t.Error("removing an absent worktree reported that something went")
	}
	if !fake.ran("worktree", "prune") {
		t.Error("the stale registration was not pruned")
	}
	if want := []string{paths["task"], paths["project"]}; !slices.Equal(removal.Directories, want) {
		t.Errorf("the removal reported %v, want %v", removal.Directories, want)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("the worktree root is gone: %v", err)
	}
}

// TestNothingIsPrunedThatIsNotAnEmptyDirectoryBelowTheRoot checks each rule the
// walk stops on, because every one of them is a directory that would otherwise
// be deleted.
func TestNothingIsPrunedThatIsNotAnEmptyDirectoryBelowTheRoot(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "worktrees")

	// A directory holding anything ends the walk, whatever is in it: another
	// task's worktree, a repository this task no longer records, or a file
	// somebody put there.
	held := filepath.Join(root, "app", "held")
	if err := os.MkdirAll(filepath.Join(held, "api"), 0o755); err != nil {
		t.Fatalf("arranging %s: %v", held, err)
	}
	if err := os.WriteFile(filepath.Join(held, "notes.txt"), []byte("mine\n"), 0o644); err != nil {
		t.Fatalf("arranging a file: %v", err)
	}
	if pruned := pruneGeneratedDirectories(filepath.Join(held, "api"), root); len(pruned) != 0 {
		t.Errorf("a directory with a file in it was pruned: %v", pruned)
	}
	if _, err := os.Stat(held); err != nil {
		t.Errorf("a directory with a file in it is gone: %v", err)
	}

	// A symbolic link is stepped over rather than followed or deleted, so a link
	// planted between the root and a worktree cannot turn a cleanup into a
	// removal somewhere else.
	target := filepath.Join(dir, "elsewhere")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("arranging %s: %v", target, err)
	}
	link := filepath.Join(root, "linked")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("this filesystem has no symbolic links: %v", err)
	}
	if pruned := pruneGeneratedDirectories(filepath.Join(link, "api"), root); len(pruned) != 0 {
		t.Errorf("a symbolic link was pruned: %v", pruned)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Errorf("the symbolic link is gone: %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Errorf("what the symbolic link pointed at is gone: %v", err)
	}

	// A worktree whose parent is the root itself — the layout a template that
	// names the repository directly produces — prunes nothing at all.
	if pruned := pruneGeneratedDirectories(filepath.Join(root, "api"), root); len(pruned) != 0 {
		t.Errorf("the root's own children were pruned: %v", pruned)
	}
	if _, err := os.Stat(root); err != nil {
		t.Errorf("the worktree root is gone: %v", err)
	}

	// And a root that is not a prefix of the path, or is a shared system
	// directory, prunes nothing: neither says which directories a task was
	// given.
	if pruned := pruneGeneratedDirectories(filepath.Join(dir, "outside", "api"), root); len(pruned) != 0 {
		t.Errorf("a path outside the root was pruned: %v", pruned)
	}
	for _, broad := range []string{"", "/", "/tmp"} {
		if pruned := pruneGeneratedDirectories(filepath.Join(dir, "api"), broad); len(pruned) != 0 {
			t.Errorf("a root of %q pruned %v", broad, pruned)
		}
	}
}
