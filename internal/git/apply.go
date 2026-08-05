package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ma8el/feat/internal/domain"
)

// worktreeDirMode is the mode of the directories Feat creates above a task
// worktree.
//
// They hold repository working trees, not secrets, and in devcontainer
// execution a second user — the non-root container user — has to traverse them
// to reach the mount. Git creates the worktree itself with the user's umask, so
// a stricter mode here would only make Feat's own directories the one thing in
// the path that cannot be entered.
const worktreeDirMode = 0o755

// Journal records what Apply created, one repository at a time.
//
// It exists so that the writer of persistent state stays the daemon while the
// creation of resources stays here. Apply calls it after each repository and
// before the next one begins, and stops if it fails: a record that is one
// repository behind the world is exactly the situation Feat must never be in.
type Journal interface {
	// Created records one finished repository. Returning an error stops Apply.
	Created(ctx context.Context, created Created) error
}

// JournalFunc adapts a function to the Journal interface.
type JournalFunc func(ctx context.Context, created Created) error

// Created records one finished repository.
func (f JournalFunc) Created(ctx context.Context, created Created) error { return f(ctx, created) }

// Created is one repository whose worktree now exists.
type Created struct {
	// Repository identifies the repository within its project.
	Repository domain.RepositoryID
	// WorktreePath is the worktree that was created.
	WorktreePath string
	// Branch is the branch that was created, empty for a read-only repository.
	Branch string
	// Observation is what the new worktree looked like immediately afterwards.
	// It is an observation rather than an assumption: `git worktree add` runs
	// the repository's own hooks, and a hook may leave files behind.
	Observation domain.GitObservation
}

// Result is what Apply managed to do.
type Result struct {
	// Created are the repositories whose worktrees exist, in the order they
	// were created.
	Created []Created
	// Remaining are the repositories the plan still names and that Apply did
	// not reach, which is empty on success.
	Remaining []domain.RepositoryID
}

// ApplyError reports a task creation that stopped part way through.
//
// It names the repository that failed and the ones that were already created,
// because the user's next question is what exists now. Nothing is undone: a
// worktree that was created may already have been mounted, entered, or written
// to, and removing it to tidy up a failed launch is a destructive act the user
// did not ask for (docs/05-security-model.md, cleanup safety).
type ApplyError struct {
	// Task is the task being prepared.
	Task domain.TaskID
	// Repository is the repository that failed.
	Repository domain.RepositoryID
	// Created are the repositories that were finished before it.
	Created []domain.RepositoryID
	// Err is what went wrong.
	Err error
}

func (e *ApplyError) Error() string {
	message := fmt.Sprintf("preparing repository %s of task %s: %v", e.Repository, e.Task, e.Err)
	if len(e.Created) > 0 {
		message += fmt.Sprintf(" (%d repositor%s already prepared: %s)",
			len(e.Created), plural(len(e.Created)), join(e.Created))
	}
	return message
}

func (e *ApplyError) Unwrap() error { return e.Err }

// Apply creates the worktrees and branches a plan describes.
//
// The order is deliberate and is what makes an interruption recoverable. The
// caller records the plan before calling this, so every path and branch name
// that could exist afterwards is already written down; Apply then creates one
// repository at a time and journals it before starting the next. At every point
// — a crash, a full disk, a killed daemon — the record names a superset of what
// exists, and nothing exists that the record cannot name.
func (g *Git) Apply(ctx context.Context, plan *Plan, journal Journal) (Result, error) {
	var result Result

	if err := plan.Err(); err != nil {
		return result, err
	}
	if len(plan.Repositories) == 0 {
		return result, fmt.Errorf("task %s has no repository to prepare", plan.Task)
	}
	if journal == nil {
		return result, fmt.Errorf("applying the plan of task %s needs a journal to record what it creates", plan.Task)
	}

	for i, repository := range plan.Repositories {
		created, err := g.create(ctx, repository)
		if err != nil {
			result.Remaining = remaining(plan.Repositories[i:])
			return result, &ApplyError{
				Task:       plan.Task,
				Repository: repository.ID,
				Created:    identifiers(result.Created),
				Err:        err,
			}
		}

		if err := journal.Created(ctx, created); err != nil {
			// The worktree exists and the record of it does not. Stopping here
			// keeps that to one repository; continuing would add a second one
			// the record has no observation for.
			result.Created = append(result.Created, created)
			result.Remaining = remaining(plan.Repositories[i+1:])
			return result, &ApplyError{
				Task:       plan.Task,
				Repository: repository.ID,
				Created:    identifiers(result.Created),
				Err:        fmt.Errorf("recording the worktree %s: %w", created.WorktreePath, err),
			}
		}
		result.Created = append(result.Created, created)
	}
	return result, nil
}

// create makes one repository's worktree and observes it.
func (g *Git) create(ctx context.Context, repository RepositoryPlan) (Created, error) {
	parent := filepath.Dir(repository.WorktreePath)
	if err := os.MkdirAll(parent, worktreeDirMode); err != nil {
		return Created{}, fmt.Errorf("creating the directory %s for the task worktree: %w", parent, err)
	}

	if err := g.AddWorktree(ctx, repository.HostPath, WorktreeSpec{
		Path:   repository.WorktreePath,
		Branch: repository.Branch,
		Commit: repository.BaseCommit,
	}); err != nil {
		return Created{}, err
	}

	observation, err := g.Observe(ctx, ObserveRequest{
		WorktreePath: repository.WorktreePath,
		BaseRef:      repository.BaseRef,
		BaseCommit:   repository.BaseCommit,
		Now:          time.Now(),
	})
	if err != nil {
		return Created{}, fmt.Errorf("observing the new worktree %s: %w", repository.WorktreePath, err)
	}

	return Created{
		Repository:   repository.ID,
		WorktreePath: repository.WorktreePath,
		Branch:       repository.Branch,
		Observation:  observation,
	}, nil
}

// remaining lists the repositories a plan still names.
func remaining(plans []RepositoryPlan) []domain.RepositoryID {
	if len(plans) == 0 {
		return nil
	}
	ids := make([]domain.RepositoryID, 0, len(plans))
	for _, plan := range plans {
		ids = append(ids, plan.ID)
	}
	return ids
}

// identifiers lists the repositories that were created.
func identifiers(created []Created) []domain.RepositoryID {
	if len(created) == 0 {
		return nil
	}
	ids := make([]domain.RepositoryID, 0, len(created))
	for _, one := range created {
		ids = append(ids, one.Repository)
	}
	return ids
}

// join renders repository identifiers for an error message.
func join(ids []domain.RepositoryID) string {
	names := make([]string, len(ids))
	for i, id := range ids {
		names[i] = id.String()
	}
	return listOf(names)
}
