package git

import (
	"context"
	"strings"
	"testing"
)

// TestComparisonMeasuresAgainstTheRecordedBase is FR-REV-001 at the adapter:
// every number a review shows is measured against the commit the task started
// from, and the command that produced it says so.
//
// It is checked on the argument vector as well as on the result, because a
// comparison that happened to produce the right numbers against HEAD~1 would
// pass an assertion about numbers alone.
func TestComparisonMeasuresAgainstTheRecordedBase(t *testing.T) {
	const worktree = "/work/task/api"
	base := commit("beef")
	head := commit("1234")

	fake := newFakeGit()
	fake.add(worktree, &fakeRepository{
		refs:      map[string]string{"HEAD": head},
		dirty:     map[string]string{worktree: " M app.go"},
		changed:   map[string]string{worktree: "app.go\nREADME.md"},
		numstat:   map[string]string{worktree: "12\t3\tapp.go\n0\t7\tREADME.md\n-\t-\tlogo.png"},
		untracked: map[string]string{worktree: "notes.md"},
		counts:    map[string]int{base + "..HEAD": 2},
	})

	comparison, err := New(fake).Compare(context.Background(), ObserveRequest{
		WorktreePath: worktree,
		BaseCommit:   base,
	})
	if err != nil {
		t.Fatalf("comparing: %v", err)
	}

	if comparison.HeadCommit != head {
		t.Errorf("head is %q, want %q", comparison.HeadCommit, head)
	}
	if comparison.Insertions != 12 || comparison.Deletions != 10 {
		t.Errorf("counted +%d -%d, want +12 -10 with the binary file skipped rather than read as zero",
			comparison.Insertions, comparison.Deletions)
	}
	if comparison.Untracked != 1 {
		t.Errorf("counted %d untracked files, want 1", comparison.Untracked)
	}
	if comparison.Observation.ChangedFiles != 3 {
		t.Errorf("counted %d changed files, want the two tracked and the one untracked",
			comparison.Observation.ChangedFiles)
	}
	if !comparison.Observation.Dirty {
		t.Error("the worktree has a modified file and was reported clean")
	}

	var compared bool
	for _, vector := range fake.vectors() {
		if strings.Contains(vector, "--numstat") {
			compared = true
			if !strings.Contains(vector, base) {
				t.Errorf("the line counts were measured by %q, which does not name the recorded base %s",
					vector, base)
			}
		}
	}
	if !compared {
		t.Error("no line count was measured at all")
	}
}

// TestComparisonNeverWritesToTheRepository checks that opening review does not
// disturb the checkout the user may be working in.
//
// Every command carries --no-optional-locks, which is what keeps an observation
// from taking the index lock, and nothing here is a command that writes.
func TestComparisonNeverWritesToTheRepository(t *testing.T) {
	const worktree = "/work/task/api"
	base := commit("beef")

	fake := newFakeGit()
	fake.add(worktree, &fakeRepository{
		refs:   map[string]string{"HEAD": commit("1234")},
		counts: map[string]int{base + "..HEAD": 0},
	})

	if _, err := New(fake).Compare(context.Background(), ObserveRequest{
		WorktreePath: worktree,
		BaseCommit:   base,
	}); err != nil {
		t.Fatalf("comparing: %v", err)
	}

	writing := []string{"add", "commit", "checkout", "stash", "reset", "pull", "merge", "rebase", "update-index"}
	for _, vector := range fake.vectors() {
		for _, subcommand := range writing {
			if strings.HasPrefix(vector, subcommand+" ") || vector == subcommand {
				t.Errorf("comparing ran %q, which changes the repository", vector)
			}
		}
	}
	for _, vector := range fake.vectors() {
		switch {
		case strings.HasPrefix(vector, "--no-optional-locks "):
		case strings.HasPrefix(vector, "rev-parse "), strings.HasPrefix(vector, "rev-list "),
			strings.HasPrefix(vector, "merge-base "):
			// Commands that never take the index lock in the first place.
		default:
			t.Errorf("comparing ran %q without --no-optional-locks", vector)
		}
	}
}

// TestComparisonOfAWorktreeWithNoCommit checks that a task whose agent has not
// committed still compares.
//
// Commits are optional in v0 (FR-GIT-007), so uncommitted work is the ordinary
// case rather than an error, and an absent head is reported as absent.
func TestComparisonOfAWorktreeWithNoCommit(t *testing.T) {
	const worktree = "/work/task/api"
	base := commit("beef")

	fake := newFakeGit()
	fake.add(worktree, &fakeRepository{
		dirty:     map[string]string{worktree: "?? new.go"},
		untracked: map[string]string{worktree: "new.go"},
		counts:    map[string]int{base + "..HEAD": 0},
	})

	comparison, err := New(fake).Compare(context.Background(), ObserveRequest{
		WorktreePath: worktree,
		BaseCommit:   base,
	})
	if err != nil {
		t.Fatalf("comparing a worktree with no commit: %v", err)
	}
	if comparison.HeadCommit != "" {
		t.Errorf("head is %q, want it absent", comparison.HeadCommit)
	}
	if comparison.Untracked != 1 {
		t.Errorf("counted %d untracked files, want the one that is there", comparison.Untracked)
	}
}
