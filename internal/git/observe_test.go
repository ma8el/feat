package git

import (
	"context"
	"testing"
	"time"
)

// TestObservationUsesTwoReferencePoints checks the distinction the domain draws
// between what a task did and where the world moved.
//
// Ahead and the change summary are measured against the recorded base commit,
// which never moves; behind and merged are measured against the base ref as it
// is now. Measured the other way round, "behind" would always be zero and
// "merged" would never become true.
func TestObservationUsesTwoReferencePoints(t *testing.T) {
	const worktree = "/work/task/api"
	base := commit("beef")
	moved := commit("f00d")

	fake := newFakeGit()
	fake.add(worktree, &fakeRepository{
		refs: map[string]string{
			"HEAD":                     commit("1234"),
			"refs/remotes/origin/main": moved,
		},
		dirty:     map[string]string{worktree: " M app.go"},
		changed:   map[string]string{worktree: "app.go\nREADME.md"},
		untracked: map[string]string{worktree: "notes.md"},
		counts: map[string]int{
			base + "..HEAD":                  3,
			"HEAD..refs/remotes/origin/main": 2,
		},
	})

	taken := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	observation, err := New(fake).Observe(context.Background(), ObserveRequest{
		WorktreePath: worktree,
		BaseRef:      "refs/remotes/origin/main",
		BaseCommit:   base,
		Now:          taken,
	})
	if err != nil {
		t.Fatalf("observing: %v", err)
	}

	if !observation.Dirty {
		t.Error("the worktree has a modified file and was reported clean")
	}
	if observation.ChangedFiles != 3 {
		t.Errorf("counted %d changed files, want the two modified and the one untracked",
			observation.ChangedFiles)
	}
	if observation.Ahead != 3 {
		t.Errorf("ahead is %d, want the commits made since the recorded base", observation.Ahead)
	}
	if observation.Behind != 2 {
		t.Errorf("behind is %d, want the commits the base ref gained since", observation.Behind)
	}
	if observation.Merged {
		t.Error("the branch was reported merged although the base ref does not contain it")
	}
	if !observation.ObservedAt.Equal(taken) {
		t.Errorf("the observation is timed %s, want %s", observation.ObservedAt, taken)
	}
}

// TestObservationSurvivesADeletedBaseRef checks that what Feat knows about a
// task's own work does not depend on a branch somebody else deleted on the
// remote.
func TestObservationSurvivesADeletedBaseRef(t *testing.T) {
	const worktree = "/work/task/api"
	base := commit("beef")

	fake := newFakeGit()
	fake.add(worktree, &fakeRepository{
		refs:   map[string]string{"HEAD": commit("1234")},
		counts: map[string]int{base + "..HEAD": 1},
	})

	observation, err := New(fake).Observe(context.Background(), ObserveRequest{
		WorktreePath: worktree,
		BaseRef:      "refs/remotes/origin/main",
		BaseCommit:   base,
	})
	if err != nil {
		t.Fatalf("observing with a base ref that is gone: %v", err)
	}
	if observation.Ahead != 1 {
		t.Errorf("ahead is %d, want the task's own commit", observation.Ahead)
	}
	if observation.Behind != 0 || observation.Merged {
		t.Errorf("behind is %d and merged is %t, want the zero values a missing ref cannot answer",
			observation.Behind, observation.Merged)
	}
}

// TestObservationNeedsAResolvedBase checks that an observation cannot be taken
// against a ref name. Every comparison a task makes for its whole life uses the
// recorded commit, and accepting anything else here would let a moving
// reference in.
func TestObservationNeedsAResolvedBase(t *testing.T) {
	fake := newFakeGit()
	fake.add("/work/task/api", &fakeRepository{})

	if _, err := New(fake).Observe(context.Background(), ObserveRequest{
		WorktreePath: "/work/task/api",
		BaseCommit:   "main",
	}); err == nil {
		t.Fatal("an observation was taken against a ref name rather than a commit")
	}
}
