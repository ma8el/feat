package git

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
)

// commit builds a recognisable full object name, so that a failed assertion
// says which commit was expected rather than showing forty identical hex
// characters.
func commit(seed string) string {
	if len(seed) > 40 {
		seed = seed[:40]
	}
	digits := strings.Map(func(r rune) rune {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f':
			return r
		default:
			return 'a'
		}
	}, seed)
	return digits + strings.Repeat("0", 40-len(digits))
}

// fakeRepository is what a fake Git knows about one checkout.
//
// It answers the commands this package issues, and nothing else: a command the
// package does not send has no answer here, so a test cannot pass by exercising
// a code path that does not exist.
type fakeRepository struct {
	// refs maps a full ref name to the commit it points at. "HEAD" is included
	// when the checkout has one.
	refs map[string]string
	// head is the ref HEAD is attached to, empty when it is detached.
	head string
	// worktrees are the working trees registered with the repository.
	worktrees []Worktree
	// dirty is the porcelain status output of each worktree path.
	dirty map[string]string
	// changed is the `diff --name-only` output of each worktree path.
	changed map[string]string
	// numstat is the `diff --numstat` output of each worktree path.
	numstat map[string]string
	// untracked is the `ls-files --others` output of each worktree path.
	untracked map[string]string
	// counts answers `rev-list --count`, keyed by the range.
	counts map[string]int
	// ancestors answers `merge-base --is-ancestor`, keyed by "a b".
	ancestors map[string]bool
	// fail makes one subcommand fail, keyed by its first word.
	fail map[string]error
	// missing marks the repository as not being one at all.
	missing bool
}

// fakeGit answers Git commands for a set of checkouts.
type fakeGit struct {
	mu sync.Mutex
	// repositories are the checkouts, keyed by their directory.
	repositories map[string]*fakeRepository
	// calls records every argument vector, in order, so that a test can assert
	// what was run and what was not.
	calls [][]string
	// dirs records the working directory of each call.
	dirs []string
}

func newFakeGit() *fakeGit {
	return &fakeGit{repositories: make(map[string]*fakeRepository)}
}

// add registers a checkout with sensible defaults.
func (f *fakeGit) add(dir string, repository *fakeRepository) *fakeRepository {
	if repository.refs == nil {
		repository.refs = make(map[string]string)
	}
	for _, field := range []*map[string]string{
		&repository.dirty, &repository.changed, &repository.numstat, &repository.untracked,
	} {
		if *field == nil {
			*field = make(map[string]string)
		}
	}
	if repository.counts == nil {
		repository.counts = make(map[string]int)
	}
	if repository.ancestors == nil {
		repository.ancestors = make(map[string]bool)
	}
	if repository.fail == nil {
		repository.fail = make(map[string]error)
	}
	f.repositories[dir] = repository
	return repository
}

// ran reports whether an argument vector with the given prefix was run.
func (f *fakeGit) ran(prefix ...string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, call := range f.calls {
		if len(call) < len(prefix) {
			continue
		}
		if strings.Join(call[:len(prefix)], " ") == strings.Join(prefix, " ") {
			return true
		}
	}
	return false
}

// vectors returns every recorded argument vector as a single string each.
func (f *fakeGit) vectors() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	rendered := make([]string, 0, len(f.calls))
	for _, call := range f.calls {
		rendered = append(rendered, strings.Join(call, " "))
	}
	return rendered
}

// Run answers one Git command.
func (f *fakeGit) Run(_ context.Context, dir string, args ...string) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string(nil), args...))
	f.dirs = append(f.dirs, dir)
	repository, known := f.repositories[dir]
	f.mu.Unlock()

	if !known {
		return "", &ExitError{Args: args, Dir: dir, Code: 128, Stderr: "not a git repository"}
	}

	// The package prefixes read-only commands with a global option. Stripping it
	// here keeps the dispatch below about subcommands.
	if len(args) > 0 && args[0] == "--no-optional-locks" {
		args = args[1:]
	}
	if len(args) == 0 {
		return "", fmt.Errorf("fake git: no subcommand")
	}
	if err, failing := repository.fail[args[0]]; failing {
		return "", err
	}

	switch args[0] {
	case "rev-parse":
		return repository.revParse(args, dir)
	case "symbolic-ref":
		if repository.head == "" {
			return "", &ExitError{Args: args, Dir: dir, Code: 1}
		}
		return repository.head, nil
	case "fetch":
		return "", nil
	case "worktree":
		return f.worktree(repository, args, dir)
	case "status":
		return repository.dirty[dir], nil
	case "diff":
		for _, arg := range args {
			if arg == "--numstat" {
				return repository.numstat[dir], nil
			}
		}
		return repository.changed[dir], nil
	case "ls-files":
		return repository.untracked[dir], nil
	case "rev-list":
		return fmt.Sprint(repository.counts[args[len(args)-1]]), nil
	case "merge-base":
		if repository.ancestors[args[2]+" "+args[3]] {
			return "", nil
		}
		return "", &ExitError{Args: args, Dir: dir, Code: 1}
	default:
		return "", fmt.Errorf("fake git: unexpected command %q", strings.Join(args, " "))
	}
}

// revParse answers `rev-parse --git-dir` and `rev-parse --verify --quiet`.
func (r *fakeRepository) revParse(args []string, dir string) (string, error) {
	if len(args) == 2 && args[1] == "--git-dir" {
		if r.missing {
			return "", &ExitError{Args: args, Dir: dir, Code: 128, Stderr: "not a git repository"}
		}
		return ".git", nil
	}
	revision := strings.TrimSuffix(args[len(args)-1], "^{commit}")
	if resolved, ok := r.refs[revision]; ok {
		return resolved, nil
	}
	return "", &ExitError{Args: args, Dir: dir, Code: 1}
}

// worktree answers `worktree list`, `worktree add`, `worktree remove`, and
// `worktree prune`.
//
// Adding one and removing one have the effects the real command has that this
// package depends on: a worktree is registered with its branch when it is added,
// and its directory is deleted from disk and deregistered when it is removed.
// Removing a dirty one without --force is refused, because that is Git's own
// safety and the confirmation rule in internal/reconcile sits on top of it — a
// fake that removed dirty work regardless would let a caller that forgot to
// force pass here and fail against Git.
func (f *fakeGit) worktree(repository *fakeRepository, args []string, dir string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	switch args[1] {
	case "list":
		var lines []string
		for _, worktree := range repository.worktrees {
			lines = append(lines, "worktree "+worktree.Path)
			if worktree.Head != "" {
				lines = append(lines, "HEAD "+worktree.Head)
			}
			switch {
			case worktree.Bare:
				lines = append(lines, "bare")
			case worktree.Branch != "":
				lines = append(lines, "branch "+worktree.Branch)
			default:
				lines = append(lines, "detached")
			}
			if worktree.Locked {
				lines = append(lines, "locked")
			}
			lines = append(lines, "")
		}
		return strings.Join(lines, "\n"), nil

	case "add":
		created := Worktree{Path: args[len(args)-2], Head: args[len(args)-1]}
		if branch := branchOf(args); branch != "" {
			created.Branch = "refs/heads/" + branch
			repository.refs["refs/heads/"+branch] = args[len(args)-1]
		}
		repository.worktrees = append(repository.worktrees, created)
		// A new worktree is a checkout of the base commit, so the adapter that
		// observes it immediately afterwards finds it registered with the same
		// answers as its own repository.
		f.repositories[created.Path] = repository
		repository.refs["HEAD"] = args[len(args)-1]
		return "", nil

	case "remove":
		path := args[len(args)-1]
		if repository.dirty[path] != "" && !contains(args, "--force") {
			return "", &ExitError{
				Args: args, Dir: dir, Code: 128,
				Stderr: "fatal: '" + path + "' contains modified or untracked files, use --force to delete it",
			}
		}
		if err := os.RemoveAll(path); err != nil {
			return "", err
		}
		remaining := make([]Worktree, 0, len(repository.worktrees))
		for _, worktree := range repository.worktrees {
			if worktree.Path != path {
				remaining = append(remaining, worktree)
			}
		}
		repository.worktrees = remaining
		delete(f.repositories, path)
		return "", nil

	case "prune":
		return "", nil

	default:
		return "", fmt.Errorf("fake git: unexpected worktree command %q", strings.Join(args, " "))
	}
}

// contains reports whether an argument vector carries a flag.
func contains(args []string, flag string) bool {
	for _, arg := range args {
		if arg == flag {
			return true
		}
	}
	return false
}

// branchOf returns the branch a `worktree add` creates, empty when detached.
func branchOf(args []string) string {
	for i, arg := range args {
		if arg == "-b" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}
