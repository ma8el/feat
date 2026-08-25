package forge

import (
	"context"
	"fmt"
	"strings"

	"github.com/ma8el/feat/internal/domain"
)

// Adapter is one Git forge.
//
// Neither method exposes a forge type: an adapter receives resolved values and
// returns a normalized merge request, so that a second forge changes no caller
// and a future external plugin protocol stays possible (ADR-024).
type Adapter interface {
	// Kind is the forge this adapter publishes to.
	Kind() domain.ForgeKind
	// Open opens one merge request and returns where it can be read.
	//
	// It records nothing: what a task published is written down by the daemon,
	// which is the only writer of persistent state (ADR-008).
	Open(ctx context.Context, req Request) (domain.MergeRequest, error)
}

// Built names the forges this build has an adapter for, in the order the
// roadmap adds them.
//
// It is a declaration rather than a discovery. The daemon composes the registry
// it publishes through, and a guard test holds the two together, so a forge that
// is configurable but not yet built is refused by name and reported by
// `feat doctor` rather than found out at the moment a branch has been pushed and
// the merge request cannot be opened (ADR-070, ADR-074).
var Built = []domain.ForgeKind{domain.ForgeGitLab}

// Available reports whether this build opens merge requests on a forge.
func Available(kind domain.ForgeKind) bool {
	for _, built := range Built {
		if built == kind {
			return true
		}
	}
	return false
}

// Request is one merge request to open.
//
// Every value here is final. The words are the agent's, read and edited by the
// user before anything reached this package; the branches and the remote are
// what the publication planned. An adapter reads no configuration and resolves
// nothing (ADR-029's rule for Git, applied here).
type Request struct {
	// Directory is the task worktree the forge CLI runs in. A forge CLI resolves
	// which project it is talking to from the repository it is run inside, so
	// this is what says which project the request is opened on.
	Directory string
	// Remote is the Git remote the branch was pushed to, and the one the CLI is
	// asked to resolve the project from.
	Remote string
	// SourceBranch is the task branch the request asks to merge.
	SourceBranch string
	// TargetBranch is the branch it asks to merge into.
	TargetBranch string
	// Title is the request's title, as the user approved it.
	Title string
	// Body is the request's description, as the user approved it. It may be
	// empty: a description nobody wrote is better than one Feat invented.
	Body string
}

// Validate reports whether the request can be opened.
//
// The rules are about what survives an argument vector and what a forge needs,
// and they are checked here rather than in each adapter so that every forge
// refuses the same request for the same reason. Nothing here judges the prose:
// a description can say anything, which is exactly why the user reads it before
// it is sent (ADR-070).
func (r Request) Validate() error {
	for _, field := range []struct{ name, value string }{
		{"directory", r.Directory},
		{"remote", r.Remote},
		{"source branch", r.SourceBranch},
		{"target branch", r.TargetBranch},
		{"title", r.Title},
	} {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("a merge request needs a %s", field.name)
		}
	}
	if r.SourceBranch == r.TargetBranch {
		return fmt.Errorf("a merge request cannot merge %s into itself", r.SourceBranch)
	}
	for _, field := range []struct{ name, value string }{
		{"remote", r.Remote},
		{"source branch", r.SourceBranch},
		{"target branch", r.TargetBranch},
		{"title", r.Title},
	} {
		// A value a CLI reads as an option is a flag of somebody else's
		// choosing, which is the rule internal/git states for Git's arguments
		// and holds identically here.
		if strings.HasPrefix(field.value, "-") {
			return fmt.Errorf("a merge request %s must not start with %q, which a command line reads as an "+
				"option, but %q does", field.name, "-", field.value)
		}
	}
	if strings.ContainsAny(r.Title, "\r\n") {
		return fmt.Errorf("a merge request title is one line, and this one spans more")
	}
	for _, field := range []struct{ name, value string }{
		{"title", r.Title},
		{"description", r.Body},
		{"source branch", r.SourceBranch},
		{"target branch", r.TargetBranch},
		{"remote", r.Remote},
	} {
		if strings.ContainsRune(field.value, 0) {
			return fmt.Errorf("a merge request %s must not contain a NUL, which no argument survives",
				field.name)
		}
	}
	return nil
}

// Command is one forge-CLI invocation.
//
// It is an argument vector rather than a string, so nothing is ever handed to a
// shell to re-split (CLAUDE.md architectural rules).
type Command struct {
	// Program is the forge's own command line.
	Program string
	// Arguments are its arguments, each already one vector element.
	Arguments []string
	// Directory is where it runs.
	Directory string
}

// Output is what a forge CLI produced.
type Output struct {
	// Stdout and Stderr are the captured streams.
	Stdout string
	Stderr string
	// ExitCode is the process exit status.
	ExitCode int
}

// Succeeded reports whether the command exited cleanly.
func (o Output) Succeeded() bool { return o.ExitCode == 0 }

// Runner runs one forge CLI on the trusted host.
//
// It is an interface for the reason git.Runner and agent.Runner are: a test can
// arrange an unauthenticated CLI, a protected branch, or a forge that answers
// with something unparseable without needing a forge in that state.
type Runner interface {
	// Run executes the command and returns what it produced. A command that ran
	// and failed is not an error; a command that could not be started is.
	Run(ctx context.Context, command Command) (Output, error)
}

// Error reports a forge CLI that ran and refused.
//
// It carries what the CLI said, because that is the sentence the user would
// have read had they run the command themselves — a protected branch, a project
// that does not exist, a session that is no longer authenticated — and a second
// account of one event helps nobody.
type Error struct {
	// Forge is the forge that refused.
	Forge domain.ForgeKind
	// Program is the CLI that was run.
	Program string
	// ExitCode is what it exited with.
	ExitCode int
	// Detail is what it said, trimmed and bounded.
	Detail string
}

func (e *Error) Error() string {
	message := fmt.Sprintf("%s refused to open the merge request (%s exited %d)",
		e.Forge, e.Program, e.ExitCode)
	if e.Detail != "" {
		message += ": " + e.Detail
	}
	return message
}

// MaxDetail bounds what a forge's own output contributes to a recorded failure.
//
// The reason is recorded on the task and shown on a screen, and a CLI that
// printed a page of advice would otherwise put the page there.
const MaxDetail = 1 << 10

// Detail reduces a CLI's output to the part worth recording.
//
// The last lines are kept rather than the first: a CLI prints its progress and
// then its refusal, so the end is where the reason is.
func Detail(output Output) string {
	text := strings.TrimSpace(output.Stderr)
	if text == "" {
		text = strings.TrimSpace(output.Stdout)
	}
	if len(text) <= MaxDetail {
		return text
	}
	cut := len(text) - MaxDetail
	for cut < len(text) && text[cut]&0xc0 == 0x80 {
		// Cut on a rune boundary, so the result is still valid UTF-8.
		cut++
	}
	return "…" + text[cut:]
}
