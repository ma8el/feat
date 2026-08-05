package git

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestArgumentVectorsAreExact pins the commands this package sends.
//
// A fake runner cannot tell whether a flag exists or whether the output is the
// shape the parser expects; the opt-in tests against real Git do that. What this
// pins is the other half: that a command is an argument vector, that a value
// lands in one element rather than being split, and that no read-only command
// starts writing.
func TestArgumentVectorsAreExact(t *testing.T) {
	base := commit("feedface")

	for _, tc := range []struct {
		name string
		call func(*Git) error
		want string
	}{
		{
			name: "fetch updates one remote and nothing else",
			call: func(g *Git) error { return g.Fetch(context.Background(), "/repo", "origin") },
			want: "fetch origin",
		},
		{
			name: "a base is resolved to a commit, never to a ref",
			call: func(g *Git) error {
				_, err := g.Commit(context.Background(), "/repo", "refs/remotes/origin/main")
				return err
			},
			want: "rev-parse --verify --quiet refs/remotes/origin/main^{commit}",
		},
		{
			name: "a read-write worktree creates its branch",
			call: func(g *Git) error {
				return g.AddWorktree(context.Background(), "/repo", WorktreeSpec{
					Path: "/work/one", Branch: "feat/abc-title", Commit: base,
				})
			},
			want: "worktree add -b feat/abc-title /work/one " + base,
		},
		{
			name: "a read-only worktree is detached and has no branch",
			call: func(g *Git) error {
				return g.AddWorktree(context.Background(), "/repo", WorktreeSpec{
					Path: "/work/two", Commit: base,
				})
			},
			want: "worktree add --detach /work/two " + base,
		},
		{
			name: "observing a working tree takes no optional lock",
			call: func(g *Git) error {
				_, err := g.Dirty(context.Background(), "/repo")
				return err
			},
			want: "--no-optional-locks status --porcelain",
		},
		{
			name: "a change summary compares against the recorded commit",
			call: func(g *Git) error {
				_, err := g.ChangedFiles(context.Background(), "/repo", base)
				return err
			},
			want: "--no-optional-locks diff --name-only " + base + " --",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeGit()
			fake.add("/repo", &fakeRepository{refs: map[string]string{
				"refs/remotes/origin/main": base,
			}})

			if err := tc.call(New(fake)); err != nil {
				t.Fatalf("running the command: %v", err)
			}
			if vectors := fake.vectors(); vectors[0] != tc.want {
				t.Errorf("ran `git %s`, want `git %s`", vectors[0], tc.want)
			}
		})
	}
}

// TestFetchNeverPrunesOrChangesTheCheckout is FR-GIT-001 stated as a test.
//
// The flags below each change something in the user's repository that Feat was
// not asked to change: --prune deletes remote-tracking refs they may still have
// branches on, --all and --tags update refs no base policy reads, and a pull
// would move the branch they have checked out.
func TestFetchNeverPrunesOrChangesTheCheckout(t *testing.T) {
	fake := newFakeGit()
	fake.add("/repo", &fakeRepository{})

	if err := New(fake).Fetch(context.Background(), "/repo", "origin"); err != nil {
		t.Fatalf("fetching: %v", err)
	}

	vector := fake.vectors()[0]
	for _, forbidden := range []string{"--prune", "--all", "--tags", "--force"} {
		if strings.Contains(vector, forbidden) {
			t.Errorf("`git %s` uses %s, which changes refs Feat was not asked to change", vector, forbidden)
		}
	}
	for _, forbidden := range []string{"pull", "merge", "rebase", "checkout", "reset"} {
		if fake.ran(forbidden) {
			t.Errorf("`git %s` ran, and Feat must never mutate the user's ordinary checkout", forbidden)
		}
	}
}

// TestMissingRefIsNotFoundRatherThanFailure checks that the question "does this
// ref exist" is answered rather than reported as trouble. Git says no with exit
// code 1 and no output, which a caller must not have to tell from a real
// failure by reading messages.
func TestMissingRefIsNotFoundRatherThanFailure(t *testing.T) {
	fake := newFakeGit()
	fake.add("/repo", &fakeRepository{})

	_, err := New(fake).Commit(context.Background(), "/repo", "refs/heads/absent")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("resolving an absent ref returned %v, want an error matching ErrNotFound", err)
	}

	exists, err := New(fake).Exists(context.Background(), "/repo", "refs/heads/absent")
	if err != nil || exists {
		t.Fatalf("Exists returned (%t, %v), want (false, nil)", exists, err)
	}
}

// TestOptionLikeArgumentsAreRejected checks that a configured name cannot become
// a Git option.
//
// This package never builds a command string, so there is no shell to escape.
// What remains is that Git reads an argument beginning with "-" as an option,
// and `--upload-pack=...` in place of a remote name is a command of somebody
// else's choosing running on the user's machine.
func TestOptionLikeArgumentsAreRejected(t *testing.T) {
	fake := newFakeGit()
	fake.add("/repo", &fakeRepository{})
	adapter := New(fake)

	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"remote", func() error { return adapter.Fetch(context.Background(), "/repo", "--upload-pack=touch /tmp/x") }},
		{"revision", func() error { _, err := adapter.Commit(context.Background(), "/repo", "--output=/tmp/x"); return err }},
		{"branch", func() error {
			return adapter.AddWorktree(context.Background(), "/repo", WorktreeSpec{
				Path: "/work/one", Branch: "-b--force", Commit: commit("a"),
			})
		}},
		{"control character", func() error { return adapter.Fetch(context.Background(), "/repo", "origin\nrm -rf /") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); err == nil {
				t.Fatal("the value was accepted, and Git would have read it as an option")
			}
			if len(fake.vectors()) != 0 {
				t.Errorf("Git ran anyway: %v", fake.vectors())
			}
		})
	}
}

// TestWorktreeListIsParsed checks the porcelain format this package depends on,
// including the entries a repository has that are not task worktrees.
func TestWorktreeListIsParsed(t *testing.T) {
	parsed := parseWorktrees(strings.Join([]string{
		"worktree /checkout/repo",
		"HEAD " + commit("aaa"),
		"branch refs/heads/main",
		"",
		"worktree /work/task/repo",
		"HEAD " + commit("bbb"),
		"detached",
		"locked",
		"",
		"worktree /work/gone",
		"HEAD " + commit("ccc"),
		"branch refs/heads/feat/gone",
		"prunable gitdir file points to non-existent location",
		"",
	}, "\n"))

	if len(parsed) != 3 {
		t.Fatalf("parsed %d worktrees, want 3: %+v", len(parsed), parsed)
	}
	if parsed[0].Branch != "refs/heads/main" || parsed[0].Path != "/checkout/repo" {
		t.Errorf("the ordinary checkout parsed as %+v", parsed[0])
	}
	if parsed[1].Branch != "" || !parsed[1].Locked {
		t.Errorf("the detached, locked worktree parsed as %+v", parsed[1])
	}
	if !parsed[2].Prunable {
		t.Errorf("the prunable worktree parsed as %+v", parsed[2])
	}
}
