package git

import (
	"context"
	"fmt"
	"os"
)

// RemoveRequest is one worktree or branch a confirmed cleanup may remove.
//
// It carries the same fields the plan resolved rather than a bare path, because
// every removal is re-checked here immediately before it happens. The plan may
// have been produced seconds ago or minutes ago, and a record can be edited
// between the two.
type RemoveRequest struct {
	// HostPath is the ordinary checkout that owns the registration or the
	// branch. Every command runs there.
	HostPath string
	// Root is the fixed directory Feat owns. A worktree outside it is refused.
	Root string
	// Checkouts are the ordinary checkouts of the task's repositories, which a
	// worktree must not overlap in either direction.
	Checkouts []string
	// Force removes work that would be lost. It is set only from a confirmation
	// the user gave against a warning they were shown (FR-CLEAN-003).
	Force bool
}

// RemoveWorktree removes one task worktree and deregisters it.
//
// The path is checked against the same rule that decided it was creatable, and
// the check runs here rather than only in the plan: this function is what
// actually deletes a directory, and a record that has been edited, restored from
// a backup, or written by an older version must not be able to point it
// somewhere else. That is the rule docs/06-technical-architecture.md states for
// cleanup, applied at the moment it matters.
//
// It reports whether anything was removed. A worktree that is already gone is a
// success: a user who ran `git worktree remove` by hand should still be able to
// tidy the branch and the record behind it.
func (g *Git) RemoveWorktree(ctx context.Context, path string, req RemoveRequest) (bool, error) {
	if err := CheckWorktreePath(req.Root, path, req.Checkouts); err != nil {
		return false, fmt.Errorf("refusing to remove %q: %w", path, err)
	}
	if err := checkArgument("worktree path", path); err != nil {
		return false, err
	}
	if err := checkArgument("checkout path", req.HostPath); err != nil {
		return false, err
	}

	if _, err := os.Lstat(path); err != nil {
		if os.IsNotExist(err) {
			// Nothing to remove, but Git may still hold the registration of a
			// directory somebody deleted by hand. Pruning is what makes the
			// checkout's own view match, and it removes nothing else.
			if _, pruneErr := g.runner.Run(ctx, req.HostPath, "worktree", "prune"); pruneErr != nil {
				return false, fmt.Errorf("pruning the worktree registrations of %s: %w", req.HostPath, pruneErr)
			}
			return false, nil
		}
		return false, fmt.Errorf("examining the worktree %q before removing it: %w", path, err)
	}

	args := []string{"worktree", "remove"}
	if req.Force {
		args = append(args, "--force")
	}
	args = append(args, path)
	if _, err := g.runner.Run(ctx, req.HostPath, args...); err != nil {
		return false, fmt.Errorf("removing the worktree %q of %s: %w", path, req.HostPath, err)
	}
	return true, nil
}

// DeleteBranch deletes one task branch from the checkout that holds it.
//
// Without Force this is `git branch -d`, which Git itself refuses for a branch
// that is not merged — so an unconfirmed deletion of unmerged work is refused
// twice: once by the confirmation rule in internal/reconcile, and once by Git.
// The second refusal is the one that still holds if the first is ever wrong.
//
// It reports whether anything was deleted.
func (g *Git) DeleteBranch(ctx context.Context, branch string, req RemoveRequest) (bool, error) {
	if err := checkArgument("branch", branch); err != nil {
		return false, err
	}
	if err := checkArgument("checkout path", req.HostPath); err != nil {
		return false, err
	}

	present, err := g.Exists(ctx, req.HostPath, "refs/heads/"+branch)
	if err != nil {
		return false, err
	}
	if !present {
		return false, nil
	}

	flag := "-d"
	if req.Force {
		flag = "-D"
	}
	if _, err := g.runner.Run(ctx, req.HostPath, "branch", flag, "--", branch); err != nil {
		return false, fmt.Errorf("deleting branch %s in %s: %w", branch, req.HostPath, err)
	}
	return true, nil
}
