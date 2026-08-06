package git

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrNotFound reports a ref, branch, or commit that does not exist.
//
// Git answers that question with exit code 1 and no output, which is not a
// failure of the command. Callers ask it often — a base ref that is not there
// yet, a branch a collision check is looking for — so it is a sentinel rather
// than a message to parse.
var ErrNotFound = errors.New("not found")

// Git runs Git commands for one machine.
//
// Every command is an argument vector. Nothing in this package builds a command
// string, and nothing is handed to a shell to re-split (CLAUDE.md architectural
// rules).
type Git struct {
	runner Runner
}

// New returns an adapter driving Git through the given runner.
func New(runner Runner) *Git {
	if runner == nil {
		runner = HostRunner{}
	}
	return &Git{runner: runner}
}

// Host returns an adapter driving the real Git executable.
func Host() *Git { return New(HostRunner{}) }

// IsRepository reports whether dir is inside a Git repository.
//
// Only Git's own answer counts as an answer. A command that never ran — no
// executable on the path, no file descriptors left to give it, a timeout — says
// nothing about what is in dir, and reporting it as "not a Git repository" sends
// the user to inspect a checkout that is fine. That is not hypothetical: file
// descriptor exhaustion elsewhere in Feat surfaced here as a working repository
// being declared not to be one.
func (g *Git) IsRepository(ctx context.Context, dir string) error {
	if _, err := g.runner.Run(ctx, dir, "rev-parse", "--git-dir"); err != nil {
		if _, ran := exitCode(err); !ran {
			return fmt.Errorf("cannot tell whether %s is a Git repository: %w", dir, err)
		}
		return fmt.Errorf("%s is not a Git repository: %w", dir, err)
	}
	return nil
}

// Fetch updates the remote-tracking refs of one remote.
//
// It never touches the working tree, the index, or a local branch, which is what
// FR-GIT-001 requires: Feat fetches and never pulls, so a user with uncommitted
// work in their ordinary checkout is unaffected by a task being prepared beside
// it.
//
// The command is deliberately plain. `--prune` would delete remote-tracking refs
// the user still has branches on, `--tags` and `--all` would update refs no base
// policy reads, and each of them is a change to the user's repository that Feat
// was not asked to make.
func (g *Git) Fetch(ctx context.Context, dir, remote string) error {
	if err := checkArgument("remote", remote); err != nil {
		return err
	}
	_, err := g.runner.Run(ctx, dir, "fetch", remote)
	return err
}

// Commit resolves a revision to the full object name of a commit.
//
// The revision may name a branch, a remote-tracking ref, a tag, or HEAD; what is
// returned is always a commit, because that is what Feat records. A revision
// that does not resolve returns an error matching ErrNotFound.
func (g *Git) Commit(ctx context.Context, dir, revision string) (string, error) {
	if err := checkArgument("revision", revision); err != nil {
		return "", err
	}
	output, err := g.runner.Run(ctx, dir, "rev-parse", "--verify", "--quiet", revision+"^{commit}")
	if err != nil {
		if code, ok := exitCode(err); ok && code == 1 {
			return "", fmt.Errorf("%s has no %s: %w", dir, revision, ErrNotFound)
		}
		return "", err
	}
	if !commitPattern.MatchString(output) {
		return "", fmt.Errorf("resolving %s in %s produced %q, which is not a commit", revision, dir, output)
	}
	return output, nil
}

// Exists reports whether a revision resolves in the repository.
func (g *Git) Exists(ctx context.Context, dir, revision string) (bool, error) {
	if _, err := g.Commit(ctx, dir, revision); err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Head returns the ref HEAD points at, such as "refs/heads/main", and reports
// whether HEAD is attached to one at all. A detached HEAD is not an error: it is
// how a read-only task worktree is checked out.
func (g *Git) Head(ctx context.Context, dir string) (string, bool, error) {
	output, err := g.runner.Run(ctx, dir, "symbolic-ref", "--quiet", "HEAD")
	if err != nil {
		if code, ok := exitCode(err); ok && code == 1 {
			return "", false, nil
		}
		return "", false, err
	}
	return output, true, nil
}

// Worktree is one entry of a repository's worktree list.
type Worktree struct {
	// Path is the absolute path of the working tree.
	Path string
	// Head is the commit checked out there, empty for a bare repository.
	Head string
	// Branch is the ref checked out there, empty when HEAD is detached.
	Branch string
	// Bare reports the repository's own bare entry.
	Bare bool
	// Locked reports a worktree Git will not remove without --force.
	Locked bool
	// Prunable reports a registration whose directory is gone.
	Prunable bool
}

// Worktrees lists the working trees registered with a repository, including the
// user's ordinary checkout.
//
// This is how Feat learns what a repository already has: a path Git has
// registered cannot be used for a second worktree, and a branch already checked
// out somewhere cannot be checked out again.
func (g *Git) Worktrees(ctx context.Context, dir string) ([]Worktree, error) {
	output, err := g.runner.Run(ctx, dir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	return parseWorktrees(output), nil
}

// parseWorktrees reads the porcelain worktree list.
//
// The format is one record per working tree, separated by a blank line, with one
// attribute per line. It is a stable, documented format, which is why the
// porcelain form is used rather than the human-readable one.
func parseWorktrees(output string) []Worktree {
	var (
		list    []Worktree
		current *Worktree
	)
	flush := func() {
		if current != nil {
			list = append(list, *current)
			current = nil
		}
	}

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			flush()
			continue
		}
		key, value, _ := strings.Cut(line, " ")
		switch key {
		case "worktree":
			flush()
			current = &Worktree{Path: value}
		case "HEAD":
			if current != nil {
				current.Head = value
			}
		case "branch":
			if current != nil {
				current.Branch = value
			}
		case "bare":
			if current != nil {
				current.Bare = true
			}
		case "locked":
			if current != nil {
				current.Locked = true
			}
		case "prunable":
			if current != nil {
				current.Prunable = true
			}
		}
	}
	flush()
	return list
}

// WorktreeSpec describes one worktree to create.
type WorktreeSpec struct {
	// Path is the absolute path to create the worktree at.
	Path string
	// Branch is the new branch to create and check out. An empty branch checks
	// the base commit out detached, which is what a read-only task repository
	// gets: a reproducible tree with no branch to commit to by accident.
	Branch string
	// Commit is the immutable base commit to check out.
	Commit string
}

// AddWorktree creates one task worktree.
//
// Git creates the directory, writes the administrative files under the
// repository's common directory, and checks the tree out — or it fails and
// leaves neither. That atomicity is why Feat never has to undo half of a
// worktree.
func (g *Git) AddWorktree(ctx context.Context, dir string, spec WorktreeSpec) error {
	if err := checkArgument("worktree path", spec.Path); err != nil {
		return err
	}
	if !commitPattern.MatchString(spec.Commit) {
		return fmt.Errorf("a worktree is created at a resolved commit, but %q is not one", spec.Commit)
	}

	args := []string{"worktree", "add"}
	if spec.Branch == "" {
		args = append(args, "--detach")
	} else {
		if err := checkArgument("branch", spec.Branch); err != nil {
			return err
		}
		args = append(args, "-b", spec.Branch)
	}
	args = append(args, spec.Path, spec.Commit)

	_, err := g.runner.Run(ctx, dir, args...)
	return err
}

// Dirty reports whether a working tree has uncommitted or untracked changes.
//
// `--no-optional-locks` keeps the observation from writing to the index, so
// observing a repository never competes with a user working in it.
func (g *Git) Dirty(ctx context.Context, worktree string) (bool, error) {
	output, err := g.runner.Run(ctx, worktree, "--no-optional-locks", "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return output != "", nil
}

// Count returns how many commits are reachable from `to` but not from `from`.
func (g *Git) Count(ctx context.Context, dir, from, to string) (int, error) {
	for _, value := range []string{from, to} {
		if err := checkArgument("revision", value); err != nil {
			return 0, err
		}
	}
	output, err := g.runner.Run(ctx, dir, "rev-list", "--count", from+".."+to)
	if err != nil {
		return 0, err
	}
	count, err := strconv.Atoi(output)
	if err != nil {
		return 0, fmt.Errorf("counting commits in %s between %s and %s: %q is not a number", dir, from, to, output)
	}
	return count, nil
}

// IsAncestor reports whether one commit is reachable from another, which is how
// Git answers "has this branch been merged".
func (g *Git) IsAncestor(ctx context.Context, dir, ancestor, descendant string) (bool, error) {
	for _, value := range []string{ancestor, descendant} {
		if err := checkArgument("revision", value); err != nil {
			return false, err
		}
	}
	if _, err := g.runner.Run(ctx, dir, "merge-base", "--is-ancestor", ancestor, descendant); err != nil {
		// Exit code 1 is the answer "no", not a failure. Anything else is.
		if code, ok := exitCode(err); ok && code == 1 {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// ChangedFiles counts the files in a worktree that differ from a base commit,
// including files that were never added.
//
// A file that is both modified and untracked cannot exist, but a file may appear
// in both lists across repositories, so the names are collected in a set rather
// than counted twice.
func (g *Git) ChangedFiles(ctx context.Context, worktree, base string) (int, error) {
	if !commitPattern.MatchString(base) {
		return 0, fmt.Errorf("a change summary compares against a resolved commit, but %q is not one", base)
	}

	changed := make(map[string]bool)
	tracked, err := g.runner.Run(ctx, worktree, "--no-optional-locks", "diff", "--name-only", base, "--")
	if err != nil {
		return 0, err
	}
	untracked, err := g.runner.Run(ctx, worktree, "--no-optional-locks",
		"ls-files", "--others", "--exclude-standard")
	if err != nil {
		return 0, err
	}
	for _, output := range []string{tracked, untracked} {
		for _, name := range strings.Split(output, "\n") {
			if name = strings.TrimSpace(name); name != "" {
				changed[name] = true
			}
		}
	}
	return len(changed), nil
}

// checkArgument rejects a value that would be read as an option or would not
// survive an argument vector intact.
//
// Remote names, branch names, and refs come from configuration and from task
// preparation. They are not shell input — this package never builds a command
// string — but a value beginning with "-" is still read by Git as an option, and
// `--upload-pack=...` in place of a remote name is a command of the user's
// choosing running on their machine. Validating the value is cheaper and clearer
// than relying on every Git subcommand to honour "--".
func checkArgument(kind, value string) error {
	switch {
	case value == "":
		return fmt.Errorf("a %s must not be empty", kind)
	case strings.HasPrefix(value, "-"):
		return fmt.Errorf("a %s must not start with %q, which Git reads as an option, but %q does", kind, "-", value)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("a %s must not contain a control character, but %q does", kind, value)
		}
	}
	return nil
}
