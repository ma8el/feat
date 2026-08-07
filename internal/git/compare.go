package git

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/ma8el/feat/internal/domain"
)

// Comparison is one task repository's work, measured against the commit the
// task started from.
//
// It carries the observation Observe produces plus the numbers review needs, so
// that opening review asks Git once rather than twice for overlapping answers.
type Comparison struct {
	// Observation is dirty, ahead, behind, merged, and the changed-file count.
	Observation domain.GitObservation
	// HeadCommit is what the worktree has checked out, or empty when there is
	// no commit yet. Committing is optional (FR-GIT-007), so a task whose agent
	// has not committed still has work to review.
	HeadCommit string
	// Insertions and Deletions count lines against the recorded base.
	//
	// They cover tracked changes only. Reporting a line count for a file Git has
	// never been told about would mean adding it to the index, and every
	// observation in this package is careful not to write to the repository the
	// user is working in. The count of the files that are missing from these
	// totals is Untracked, so a caller can say so rather than present one number
	// derived from two definitions (ADR-036).
	Insertions int
	Deletions  int
	// Untracked counts files the worktree holds that Git is not tracking. They
	// are included in the observation's changed-file count and excluded from the
	// line counts above.
	Untracked int
}

// Compare reports what one task worktree holds against its recorded base.
//
// The recorded base is the immutable commit resolved when the task was created,
// which is what makes a review of a long-running task mean anything: the branch
// it started from may have moved a dozen times since, and the question review
// asks is what this task changed (FR-REV-001, invariant 8).
func (g *Git) Compare(ctx context.Context, req ObserveRequest) (Comparison, error) {
	observation, err := g.Observe(ctx, req)
	if err != nil {
		return Comparison{}, err
	}
	comparison := Comparison{Observation: observation}

	head, err := g.Commit(ctx, req.WorktreePath, "HEAD")
	switch {
	case errors.Is(err, ErrNotFound):
		// A worktree with no commit at all. Nothing to say, rather than a
		// failure: what the task changed is still measurable.
	case err != nil:
		return Comparison{}, err
	default:
		comparison.HeadCommit = head
	}

	insertions, deletions, err := g.diffStat(ctx, req.WorktreePath, req.BaseCommit)
	if err != nil {
		return Comparison{}, err
	}
	comparison.Insertions, comparison.Deletions = insertions, deletions

	untracked, err := g.untracked(ctx, req.WorktreePath)
	if err != nil {
		return Comparison{}, err
	}
	comparison.Untracked = untracked

	return comparison, nil
}

// diffStat totals the lines a worktree changed against a base commit.
//
// `--numstat` is machine-readable where `--shortstat` is a sentence, and it
// reports a binary file as "-\t-" rather than as a line count, which is skipped
// rather than read as zero: a binary file did change, and claiming it changed no
// lines would be a number nobody measured.
func (g *Git) diffStat(ctx context.Context, worktree, base string) (insertions, deletions int, err error) {
	if !commitPattern.MatchString(base) {
		return 0, 0, fmt.Errorf("a change summary compares against a resolved commit, but %q is not one", base)
	}

	output, err := g.runner.Run(ctx, worktree, "--no-optional-locks", "diff", "--numstat", base, "--")
	if err != nil {
		return 0, 0, err
	}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		added, addedErr := strconv.Atoi(fields[0])
		removed, removedErr := strconv.Atoi(fields[1])
		if addedErr != nil || removedErr != nil {
			continue
		}
		insertions += added
		deletions += removed
	}
	return insertions, deletions, nil
}

// untracked counts the files a worktree holds that Git is not tracking, honouring
// the repository's own ignore rules.
func (g *Git) untracked(ctx context.Context, worktree string) (int, error) {
	output, err := g.runner.Run(ctx, worktree, "--no-optional-locks",
		"ls-files", "--others", "--exclude-standard")
	if err != nil {
		return 0, err
	}
	count := 0
	for _, name := range strings.Split(output, "\n") {
		if strings.TrimSpace(name) != "" {
			count++
		}
	}
	return count, nil
}
