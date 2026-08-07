package review

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/paths"
)

// Kind is which of the configured external commands a request is for.
type Kind string

// The external commands FR-REV-002 and FR-REV-003 ask for. Feat renders no diff
// of its own; it opens the tools the user already has.
const (
	// KindDiff compares one repository against its recorded base commit.
	KindDiff Kind = "diff"
	// KindEditor opens one repository for editing, defaulting to $EDITOR.
	KindEditor Kind = "editor"
	// KindStatus shows one repository's Git status.
	KindStatus Kind = "status"
)

// Valid reports whether the kind is documented.
func (k Kind) Valid() bool {
	switch k {
	case KindDiff, KindEditor, KindStatus:
		return true
	default:
		return false
	}
}

// Command is one external command, expanded and checked.
//
// It is built by New, which is the only way to make one whose fields have been
// examined. The daemon expands the configured template — the placeholder
// vocabulary belongs to internal/config, which validates it (ADR-029) — and
// hands the result here; this package decides whether the expansion may run.
type Command struct {
	// Kind is which configured command this is.
	Kind Kind
	// RepositoryID is the repository the command is about.
	RepositoryID domain.RepositoryID
	// Program is the executable, as the user configured it.
	Program string
	// Arguments are its arguments, each already one vector element.
	Arguments []string
	// Directory is where it runs: the task's own worktree for that repository.
	Directory string
}

// Request is one command to check.
type Request struct {
	// Kind is which configured command this is.
	Kind Kind
	// RepositoryID is the repository the command is about.
	RepositoryID domain.RepositoryID
	// Vector is the expanded argument vector, program first.
	Vector []string
	// Directory is the task worktree the command runs in.
	Directory string
	// Worktrees are every worktree path this task recorded. The directory must
	// be one of them, which is what keeps a command inside the task it belongs
	// to.
	Worktrees []string
}

// New checks an expanded command and returns it, or says why it may not run.
//
// This is slice 11's third acceptance criterion in one place. The rules are
// about what an expansion can turn into rather than about what a template looks
// like, because a template is checked once when the configuration is loaded and
// an expansion happens per task:
//
//   - a placeholder Feat does not expand never reaches here, because expansion
//     fails on one; what reaches here is a vector that may still be empty, may
//     name no program, or may have been left holding a literal brace;
//   - the working directory must be one of this task's own recorded worktrees.
//     Not "inside the worktree root", which would let one task's command run in
//     another task's directory, and not "any absolute path", which is not a
//     rule at all;
//   - that directory must also pass the check every directory Feat acts on
//     passes, so a recorded path that has been edited into a shared system
//     directory is refused rather than used (ADR-029);
//   - no element may carry a NUL or a newline, which nothing that survives an
//     argument vector intact contains.
func New(request Request) (Command, error) {
	if !request.Kind.Valid() {
		return Command{}, fmt.Errorf("%q is not one of the review commands: %s, %s, or %s",
			request.Kind, KindDiff, KindEditor, KindStatus)
	}
	if err := request.RepositoryID.Validate(); err != nil {
		return Command{}, err
	}
	if len(request.Vector) == 0 {
		return Command{}, fmt.Errorf("the %s command of repository %s expands to nothing to run",
			request.Kind, request.RepositoryID)
	}

	program := strings.TrimSpace(request.Vector[0])
	if program == "" {
		return Command{}, fmt.Errorf("the %s command of repository %s names no program",
			request.Kind, request.RepositoryID)
	}
	if strings.ContainsAny(program, "{}") {
		// The program is fixed by configuration and validation refuses a
		// placeholder in it, so a brace here is a template Feat did not expand.
		// Running it would run an executable whose name nobody chose.
		return Command{}, fmt.Errorf(
			"the %s command of repository %s would run the program %q, which still contains a placeholder",
			request.Kind, request.RepositoryID, program)
	}

	for _, argument := range request.Vector {
		if strings.ContainsAny(argument, "\x00\n") {
			return Command{}, fmt.Errorf(
				"the %s command of repository %s carries an argument containing a NUL or a newline",
				request.Kind, request.RepositoryID)
		}
		if strings.ContainsAny(argument, "{}") {
			return Command{}, fmt.Errorf(
				"the %s command of repository %s carries the unexpanded argument %q",
				request.Kind, request.RepositoryID, argument)
		}
	}

	directory, err := checkDirectory(request)
	if err != nil {
		return Command{}, err
	}

	return Command{
		Kind:         request.Kind,
		RepositoryID: request.RepositoryID,
		Program:      program,
		Arguments:    append([]string(nil), request.Vector[1:]...),
		Directory:    directory,
	}, nil
}

// checkDirectory reports where the command may run, or why it may not run
// anywhere.
func checkDirectory(request Request) (string, error) {
	if request.Directory == "" {
		return "", fmt.Errorf("the %s command of repository %s has no directory to run in",
			request.Kind, request.RepositoryID)
	}
	directory := filepath.Clean(request.Directory)
	if !filepath.IsAbs(directory) {
		return "", fmt.Errorf("the %s command of repository %s would run in %q, which is not an absolute path",
			request.Kind, request.RepositoryID, request.Directory)
	}
	if paths.Broad(directory) {
		return "", fmt.Errorf("the %s command of repository %s would run in %s, which is a shared system directory",
			request.Kind, request.RepositoryID, directory)
	}

	for _, worktree := range request.Worktrees {
		if worktree != "" && filepath.Clean(worktree) == directory {
			return directory, nil
		}
	}
	return "", fmt.Errorf(
		"the %s command of repository %s would run in %s, which is not one of this task's worktrees (%s)",
		request.Kind, request.RepositoryID, directory, list(request.Worktrees))
}

// list renders paths for a message, so a refusal says what would have been
// accepted instead.
func list(values []string) string {
	if len(values) == 0 {
		return "the task has none recorded"
	}
	return strings.Join(values, ", ")
}
