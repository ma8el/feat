package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ma8el/feat/internal/paths"
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
	// ProjectDir is the directory the worktree root gives this task's project,
	// when it generates one. It bounds the tidy-up rather than naming a target:
	// the directories a task was given go with the task, and this one does not,
	// because it belongs to the project and the project's next task is created
	// inside it. Empty means the layout generates no such directory, and the
	// tidy-up stops at Root instead.
	ProjectDir string
	// Checkouts are the ordinary checkouts of the task's repositories, which a
	// worktree must not overlap in either direction.
	Checkouts []string
	// Force removes work that would be lost. It is set only from a confirmation
	// the user gave against a warning they were shown (FR-CLEAN-003).
	Force bool
	// Contained reports that Feat established the branch's tip is already in the
	// ref recorded as its base, so deleting it discards nothing.
	//
	// It is a separate field from Force because it answers a separate question,
	// and only DeleteBranch reads it. Force is about consent to lose work;
	// this is about which question Git is being asked. See ADR-097.
	Contained bool
	// BaseRef is the ref containment was established against. It names the
	// evidence in what a removal reports and in the error if one fails, and
	// decides nothing on its own.
	BaseRef string
}

// BranchDeletion is what deleting one branch did.
//
// It reports the flag as well as the outcome, because a deletion that overrode
// Git's own refusal should leave an account of why it was allowed to. The
// caller records it, and the event log is where the account lives (ADR-097).
type BranchDeletion struct {
	// Deleted reports whether there was a branch to delete.
	Deleted bool
	// Forced reports whether the branch was deleted with `-D`.
	Forced bool
	// Reason is the evidence the force rested on, empty when nothing was forced.
	Reason string
}

// WorktreeRemoval is what removing one worktree did.
//
// The directories are reported rather than left implicit because the caller
// records what a cleanup removed, and a directory Feat deleted belongs in that
// account exactly as the worktree does.
type WorktreeRemoval struct {
	// Removed reports whether the worktree directory was there and is gone.
	Removed bool
	// Directories are the generated directories above it that went with it,
	// innermost first. It is empty when the worktree sat directly in a directory
	// the task does not own, and when something was still in the directory
	// holding it.
	Directories []string
}

// RemoveWorktree removes one task worktree, deregisters it, and takes the empty
// directories Feat created above it for the task.
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
// tidy the branch and the record behind it — and the directories above it are
// pruned in that case too, because they are equally left over.
func (g *Git) RemoveWorktree(ctx context.Context, path string, req RemoveRequest) (WorktreeRemoval, error) {
	if err := CheckWorktreePath(req.Root, path, req.Checkouts); err != nil {
		return WorktreeRemoval{}, fmt.Errorf("refusing to remove %q: %w", path, err)
	}
	if err := checkArgument("worktree path", path); err != nil {
		return WorktreeRemoval{}, err
	}
	if err := checkArgument("checkout path", req.HostPath); err != nil {
		return WorktreeRemoval{}, err
	}

	if _, err := os.Lstat(path); err != nil {
		if os.IsNotExist(err) {
			// Nothing to remove, but Git may still hold the registration of a
			// directory somebody deleted by hand. Pruning is what makes the
			// checkout's own view match, and it removes nothing else.
			if _, pruneErr := g.runner.Run(ctx, req.HostPath, "worktree", "prune"); pruneErr != nil {
				return WorktreeRemoval{}, fmt.Errorf("pruning the worktree registrations of %s: %w",
					req.HostPath, pruneErr)
			}
			return WorktreeRemoval{Directories: pruneGeneratedDirectories(path, req)}, nil
		}
		return WorktreeRemoval{}, fmt.Errorf("examining the worktree %q before removing it: %w", path, err)
	}

	args := []string{"worktree", "remove"}
	if req.Force {
		args = append(args, "--force")
	}
	args = append(args, path)
	if _, err := g.runner.Run(ctx, req.HostPath, args...); err != nil {
		return WorktreeRemoval{}, fmt.Errorf("removing the worktree %q of %s: %w", path, req.HostPath, err)
	}
	return WorktreeRemoval{Removed: true, Directories: pruneGeneratedDirectories(path, req)}, nil
}

// pruneGeneratedDirectories removes the directories Feat generated above a worktree,
// once nothing is left in them.
//
// Creating a worktree creates them. A worktree path is generated from the
// project and the task — `…/worktrees/{project_id}/{task_id}` by default — and
// Apply calls os.MkdirAll on the parent before Git is ever run, so removing only
// what `git worktree remove` removes leaves a tree of empty directories that
// outlives every resource it was made for. The next reconciliation pass then
// reports them as orphans under the project's worktree root, correctly and
// unhelpfully: the residue of a cleanup the user just confirmed is not something
// they need to be asked to look at. Removal is the mirror of creation, and this
// is the half that was missing.
//
// It is bounded by two directories and by what the filesystem says. It stops at
// the project's own directory, which outlives every task in it — a project with
// no task right now still has one, and the next task is created inside it — and
// at the fixed directory Feat owns, which is shared by every project on the
// machine. Below those, each step removes a directory only if os.Lstat says it
// is a real directory, so a symbolic link is stepped over rather than deleted;
// and emptiness is asked of os.Remove, which refuses a directory holding
// anything, rather than of a listing that could be stale by the time it is acted
// on. The first directory that does not go ends the walk, because a directory
// with something in it is another task's or the user's.
//
// Nothing here fails a removal. The worktree is gone by the time it runs, and a
// directory that could not be removed is left exactly as it was before this
// existed: reconciliation names it, which is the account of it.
func pruneGeneratedDirectories(path string, req RemoveRequest) []string {
	if req.Root == "" || paths.Broad(req.Root) {
		return nil
	}
	root := filepath.Clean(req.Root)

	// The project's directory is honoured only where it really is one of the
	// directories this walk would otherwise pass through. Anything else — a
	// value from a template that resolves elsewhere, or none at all — leaves the
	// root as the only boundary, which is the narrower of the two answers.
	keep := root
	if req.ProjectDir != "" {
		if cleaned := filepath.Clean(req.ProjectDir); paths.Under(root, cleaned) {
			keep = cleaned
		}
	}

	var removed []string
	for dir := filepath.Dir(filepath.Clean(path)); dir != keep && paths.Under(keep, dir); dir = filepath.Dir(dir) {
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() {
			return removed
		}
		if err := os.Remove(dir); err != nil {
			return removed
		}
		removed = append(removed, dir)
	}
	return removed
}

// DeleteBranch deletes one task branch from the checkout that holds it.
//
// The flag follows what Feat established, not what Git would ask. `git branch
// -d` tests whether the branch is contained by the checkout's HEAD or by the
// branch's own upstream; Feat tests whether it is contained by the ref the task
// branched from. A task branch has no upstream, and a local `main` is behind
// `origin/main` on any checkout that has fetched, so under a remote base policy
// the two disagree as a matter of course: Feat calls the branch merged, attaches
// no warning, derives no force from it, and Git then refuses `-d` every time.
// With no warning there is nothing for the user to confirm, so no selection they
// can make reaches `-D` — and the archive refuses to strand the branch. That
// deadlock is what ADR-097 is about.
//
// Git's refusal was a deliberate second gate behind the confirmation rule, and
// what replaces it is Feat's own containment check against the recorded base
// ref. That is the narrower question of the two: it is asked about the ref the
// branch was actually made from rather than about whatever HEAD happens to be,
// and a branch it does not answer for — no base ref, a base ref that is gone, a
// comparison that failed — is not contained here, warns exactly as before, and
// is deleted only from a confirmation given against that warning
// (FR-CLEAN-003).
//
// It reports what it did, including whether it forced and on what evidence.
func (g *Git) DeleteBranch(ctx context.Context, branch string, req RemoveRequest) (BranchDeletion, error) {
	if err := checkArgument("branch", branch); err != nil {
		return BranchDeletion{}, err
	}
	if err := checkArgument("checkout path", req.HostPath); err != nil {
		return BranchDeletion{}, err
	}

	present, err := g.Exists(ctx, req.HostPath, "refs/heads/"+branch)
	if err != nil {
		return BranchDeletion{}, err
	}
	if !present {
		return BranchDeletion{}, nil
	}

	// Containment is stated first where both hold, because it is the answer that
	// makes `-d` the wrong question rather than the answer that permits losing
	// work. A branch can be both contained and carrying commits its remote has
	// never seen, and that one is still forced and still confirmed.
	deletion := BranchDeletion{}
	switch {
	case req.Contained:
		deletion.Forced = true
		deletion.Reason = "the plan established that " + req.containment() + " contains it"
	case req.Force:
		deletion.Forced = true
		deletion.Reason = "the cleanup confirmed the warnings shown against it"
	}

	flag := "-d"
	if deletion.Forced {
		flag = "-D"
	}
	if _, err := g.runner.Run(ctx, req.HostPath, "branch", flag, "--", branch); err != nil {
		// In the plan's terms first. The failure this replaced said only that
		// Git found the branch unmerged, which contradicted the plan the user
		// had just read and made the same attempt look worth repeating.
		why := deletion.Reason
		if !deletion.Forced {
			why = "the plan reported it as neither contained by " + req.containment() +
				" nor carrying a warning the cleanup confirmed"
		}
		return deletion, fmt.Errorf("deleting branch %s in %s, which Feat asked Git to delete with `%s` because %s: %w",
			branch, req.HostPath, flag, why, err)
	}
	deletion.Deleted = true
	return deletion, nil
}

// containment names the ref a branch's containment was established against.
func (r RemoveRequest) containment() string {
	if r.BaseRef == "" {
		return "the ref the task branched from"
	}
	return r.BaseRef
}
