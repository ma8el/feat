package git

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/ma8el/feat/internal/domain"
)

// CleanupRequest asks what a task owns in Git.
//
// It is built from the task's own record, never from a path a caller supplied:
// the resources Feat may remove are the ones it wrote down when it created them
// (FR-CLEAN-001).
type CleanupRequest struct {
	// Project owns the task.
	Project domain.ProjectID
	// Task is the task being cleaned up.
	Task domain.TaskID
	// Root is the fixed directory Feat owns. A target outside it is refused
	// rather than removed.
	Root string
	// Repositories are the task's recorded repository bindings.
	Repositories []CleanupRepository
}

// CleanupRepository is one recorded binding.
type CleanupRepository struct {
	// ID identifies the repository within its project.
	ID domain.RepositoryID
	// HostPath is the user's ordinary checkout, which owns the branch and the
	// worktree registration.
	HostPath string
	// Remote is the remote a pushed branch would be on.
	Remote string
	// WorktreePath is the recorded task worktree.
	WorktreePath string
	// Branch is the recorded task branch, empty for a read-only repository.
	Branch string
	// BaseRef is the ref the base policy named, used to ask whether the branch
	// has been merged.
	BaseRef string
	// BaseCommit is the recorded immutable base commit.
	BaseCommit string
}

// CleanupPlan is the exact set of Git resources one task owns, with what makes
// each of them risky to remove.
//
// It has no execute method: this package produces the inventory and removes
// nothing. Producing it and acting on it are separate steps on purpose: the
// user chooses per class of resource, and the classes are separate here for the
// same reason (FR-CLEAN-002).
type CleanupPlan struct {
	// Project owns the task.
	Project domain.ProjectID
	// Task is the task the plan belongs to.
	Task domain.TaskID
	// Worktrees are the task worktrees, one per recorded binding.
	Worktrees []WorktreeTarget
	// Branches are the task branches, one per read-write binding.
	Branches []BranchTarget
	// Problems are recorded resources the plan refuses to name as targets,
	// which is what an unsafe or unresolvable path becomes.
	Problems []Problem
}

// WorktreeTarget is one worktree a cleanup could remove.
type WorktreeTarget struct {
	// Repository identifies the repository within its project.
	Repository domain.RepositoryID
	// Path is the recorded worktree path.
	Path string
	// HostPath is the checkout the worktree is registered with.
	HostPath string
	// Present reports whether the directory is there now.
	Present bool
	// Registered reports whether Git still knows about it.
	Registered bool
	// Dirty reports uncommitted or untracked changes.
	Dirty bool
	// Locked reports a worktree the user locked, which Git will not remove
	// without being forced.
	Locked bool
	// Warnings are the reasons removing this worktree needs explicit
	// confirmation (FR-CLEAN-003).
	Warnings []string
}

// BranchTarget is one branch a cleanup could delete.
type BranchTarget struct {
	// Repository identifies the repository within its project.
	Repository domain.RepositoryID
	// Name is the branch name.
	Name string
	// HostPath is the checkout that holds the branch.
	HostPath string
	// Present reports whether the branch exists now.
	Present bool
	// Merged reports whether the base ref contains the branch tip.
	Merged bool
	// Unpushed counts commits that exist only here. Without a remote-tracking
	// branch, every commit the task made is unpushed.
	Unpushed int
	// Pushed reports whether a remote-tracking branch exists at all.
	Pushed bool
	// Warnings are the reasons deleting this branch needs explicit
	// confirmation.
	Warnings []string
}

// Risky reports whether the worktree holds work that would be lost.
func (t WorktreeTarget) Risky() bool { return len(t.Warnings) > 0 }

// Risky reports whether the branch holds work that would be lost.
func (t BranchTarget) Risky() bool { return len(t.Warnings) > 0 }

// CleanupPlanFor resolves what a task owns and what removing it would cost.
//
// It observes and never removes. Two rules decide what may appear in it:
//
//   - a target comes from the task's record, so a resource Feat did not write
//     down is never proposed for removal;
//   - a recorded path that is not inside the root Feat owns is refused, not
//     removed. A record can be edited, restored from a backup, or written by an
//     older version, and the moment a path from one of those decides what gets
//     deleted, the record has become an instruction.
func (g *Git) CleanupPlanFor(ctx context.Context, req CleanupRequest) (*CleanupPlan, error) {
	if err := req.Project.Validate(); err != nil {
		return nil, err
	}
	if err := req.Task.Validate(); err != nil {
		return nil, err
	}

	plan := &CleanupPlan{Project: req.Project, Task: req.Task}
	checkouts := make([]string, 0, len(req.Repositories))
	for _, repository := range req.Repositories {
		checkouts = append(checkouts, repository.HostPath)
	}

	for _, repository := range req.Repositories {
		if err := CheckWorktreePath(req.Root, repository.WorktreePath, checkouts); err != nil {
			plan.Problems = append(plan.Problems, Problem{
				Repository: repository.ID,
				Reason: fmt.Sprintf("the recorded worktree %q is not a path Feat may remove: %s",
					repository.WorktreePath, err),
			})
			continue
		}

		worktree, problems := g.worktreeTarget(ctx, repository)
		plan.Worktrees = append(plan.Worktrees, worktree)
		plan.Problems = append(plan.Problems, problems...)

		if repository.Branch == "" {
			continue
		}
		branch, problems := g.branchTarget(ctx, repository)
		plan.Branches = append(plan.Branches, branch)
		plan.Problems = append(plan.Problems, problems...)
	}
	return plan, nil
}

// worktreeTarget observes one recorded worktree.
func (g *Git) worktreeTarget(ctx context.Context, repository CleanupRepository) (WorktreeTarget, []Problem) {
	target := WorktreeTarget{
		Repository: repository.ID,
		Path:       repository.WorktreePath,
		HostPath:   repository.HostPath,
	}
	var problems []Problem
	problem := func(format string, args ...any) {
		problems = append(problems, Problem{Repository: repository.ID, Reason: fmt.Sprintf(format, args...)})
	}

	info, err := os.Lstat(repository.WorktreePath)
	switch {
	case err == nil:
		target.Present = info.IsDir()
		if !info.IsDir() {
			problem("%s is not a directory, so it is not a worktree Feat created", repository.WorktreePath)
		}
	case errors.Is(err, os.ErrNotExist):
		// A worktree that is already gone is reported as absent rather than as a
		// problem: cleanup after a manual `git worktree remove` should still be
		// able to tidy the branch and the record.
	default:
		problem("%s cannot be examined: %s", repository.WorktreePath, err)
	}

	worktrees, err := g.Worktrees(ctx, repository.HostPath)
	if err != nil {
		problem("the worktrees of %s cannot be listed: %s", repository.HostPath, err)
		return target, problems
	}
	recorded := resolvePath(repository.WorktreePath)
	for _, worktree := range worktrees {
		// Git reports the resolved path, so a recorded path that reaches the same
		// directory through a symbolic link still matches its registration.
		if resolvePath(worktree.Path) == recorded {
			target.Registered = true
			target.Locked = worktree.Locked
			break
		}
	}
	if target.Locked {
		target.Warnings = append(target.Warnings, "the worktree is locked")
	}

	if !target.Present {
		return target, problems
	}
	dirty, err := g.Dirty(ctx, repository.WorktreePath)
	if err != nil {
		problem("the state of %s cannot be read: %s", repository.WorktreePath, err)
		return target, problems
	}
	target.Dirty = dirty
	if dirty {
		target.Warnings = append(target.Warnings, "the worktree has uncommitted or untracked changes")
	}
	return target, problems
}

// branchTarget observes one recorded branch.
func (g *Git) branchTarget(ctx context.Context, repository CleanupRepository) (BranchTarget, []Problem) {
	target := BranchTarget{
		Repository: repository.ID,
		Name:       repository.Branch,
		HostPath:   repository.HostPath,
	}
	var problems []Problem
	problem := func(format string, args ...any) {
		problems = append(problems, Problem{Repository: repository.ID, Reason: fmt.Sprintf(format, args...)})
	}

	ref := "refs/heads/" + repository.Branch
	present, err := g.Exists(ctx, repository.HostPath, ref)
	if err != nil {
		problem("checking whether branch %s exists in %s failed: %s", repository.Branch, repository.HostPath, err)
		return target, problems
	}
	target.Present = present
	if !present {
		return target, problems
	}

	if repository.BaseRef != "" {
		if contained, err := g.Exists(ctx, repository.HostPath, repository.BaseRef); err == nil && contained {
			merged, err := g.IsAncestor(ctx, repository.HostPath, ref, repository.BaseRef)
			if err != nil {
				problem("checking whether branch %s is merged failed: %s", repository.Branch, err)
			} else {
				target.Merged = merged
			}
		}
	}

	// Unpushed work is counted against the branch's own remote-tracking ref when
	// it has one, and against the recorded base otherwise: a branch that was
	// never pushed has every commit of the task still only on this machine.
	upstream := "refs/remotes/" + repository.Remote + "/" + repository.Branch
	from := repository.BaseCommit
	if repository.Remote != "" {
		if pushed, err := g.Exists(ctx, repository.HostPath, upstream); err == nil && pushed {
			target.Pushed = true
			from = upstream
		}
	}
	if from != "" {
		unpushed, err := g.Count(ctx, repository.HostPath, from, ref)
		if err != nil {
			problem("counting the unpushed commits of branch %s failed: %s", repository.Branch, err)
		} else {
			target.Unpushed = unpushed
		}
	}

	switch {
	case target.Unpushed > 0 && !target.Pushed:
		target.Warnings = append(target.Warnings, fmt.Sprintf(
			"the branch has %d commit%s and was never pushed", target.Unpushed, suffix(target.Unpushed)))
	case target.Unpushed > 0:
		target.Warnings = append(target.Warnings, fmt.Sprintf(
			"the branch has %d unpushed commit%s", target.Unpushed, suffix(target.Unpushed)))
	}
	if !target.Merged {
		target.Warnings = append(target.Warnings, "the branch is not merged into "+repository.BaseRef)
	}
	return target, problems
}

// suffix pluralises a count of commits.
func suffix(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}
