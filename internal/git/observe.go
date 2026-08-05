package git

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ma8el/feat/internal/domain"
)

// ObserveRequest asks what a task worktree looks like now.
type ObserveRequest struct {
	// WorktreePath is the task worktree to observe.
	WorktreePath string
	// BaseRef is the ref the base policy named. It is read as it is now, which
	// is what makes "behind" a useful number.
	BaseRef string
	// BaseCommit is the immutable commit recorded when the task was created.
	// Every comparison that must not move uses this.
	BaseCommit string
	// Now is the observation time. A zero value uses the wall clock.
	Now time.Time
}

// Observe reports the current Git state of one task worktree.
//
// The two reference points are deliberately different, because they answer
// different questions:
//
//   - ahead and the change summary compare against the recorded base commit,
//     which never moves, so they describe the task's own work;
//   - behind and merged compare against the base ref as it is now, so they
//     describe the task's relationship to a branch other people are still
//     pushing to. Measured against the frozen commit, "behind" would always be
//     zero and "merged" would never become true, which are numbers that look
//     like answers without being any.
//
// A base ref that no longer resolves — a branch deleted on the remote, say —
// leaves behind and merged at their zero values rather than failing the
// observation. What Feat knows about the task's own work does not depend on it.
func (g *Git) Observe(ctx context.Context, req ObserveRequest) (domain.GitObservation, error) {
	if req.WorktreePath == "" {
		return domain.GitObservation{}, errors.New("observing a worktree needs its path")
	}
	if !commitPattern.MatchString(req.BaseCommit) {
		return domain.GitObservation{}, fmt.Errorf(
			"observing %s needs its recorded base commit, but %q is not one", req.WorktreePath, req.BaseCommit)
	}

	now := req.Now
	if now.IsZero() {
		now = time.Now()
	}
	observation := domain.GitObservation{ObservedAt: now}

	dirty, err := g.Dirty(ctx, req.WorktreePath)
	if err != nil {
		return domain.GitObservation{}, err
	}
	observation.Dirty = dirty

	changed, err := g.ChangedFiles(ctx, req.WorktreePath, req.BaseCommit)
	if err != nil {
		return domain.GitObservation{}, err
	}
	observation.ChangedFiles = changed

	ahead, err := g.Count(ctx, req.WorktreePath, req.BaseCommit, "HEAD")
	if err != nil {
		return domain.GitObservation{}, err
	}
	observation.Ahead = ahead

	if req.BaseRef == "" {
		return observation, nil
	}
	present, err := g.Exists(ctx, req.WorktreePath, req.BaseRef)
	if err != nil || !present {
		return observation, err
	}

	behind, err := g.Count(ctx, req.WorktreePath, "HEAD", req.BaseRef)
	if err != nil {
		return domain.GitObservation{}, err
	}
	observation.Behind = behind

	merged, err := g.IsAncestor(ctx, req.WorktreePath, "HEAD", req.BaseRef)
	if err != nil {
		return domain.GitObservation{}, err
	}
	observation.Merged = merged

	return observation, nil
}
