package gitlab

import (
	"context"
	"fmt"
	"regexp"

	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/forge"
)

// editorDescription is the one description glab does not read as a
// description: its own documented shorthand for "open an editor".
const editorDescription = "-"

// Executable is the GitLab command line.
//
// It is a constant rather than a configured value, for the reason the agent's
// executable is: a project that could name the program the daemon runs on its
// owner's behalf would be naming a program, and which forge a repository
// publishes to is already declared by repositories.<id>.forge.kind.
const Executable = "glab"

// Verified is the glab release this adapter's flags and behaviour were checked
// against, as docs/06-technical-architecture.md requires of a provider CLI.
//
// It is recorded rather than enforced. Another version is far more likely to
// work than not, and refusing to publish because a user upgraded glab would be
// Feat inventing a failure; what recording it buys is that a report of odd
// behaviour can be compared with what was actually tried.
const Verified = "1.114.0"

// Flags are the glab flags this adapter passes, exported so that the opt-in
// test which asks an installed glab whether it still accepts them has one list
// to check rather than a copy of it.
//
// TestRealGlabAcceptsTheFlagsThisAdapterPasses reads `glab mr create --help`
// wherever glab is installed and fails if one of these is gone. It needs no
// account and no network, so it runs anywhere glab is.
var Flags = []string{
	"--source-branch",
	"--target-branch",
	"--title",
	"--description",
	"--yes",
}

// Adapter opens merge requests through glab.
type Adapter struct{ runner forge.Runner }

var _ forge.Adapter = Adapter{}

// New returns the GitLab adapter driving the given runner.
func New(runner forge.Runner) Adapter {
	if runner == nil {
		runner = forge.HostRunner{}
	}
	return Adapter{runner: runner}
}

// Kind returns the forge this adapter publishes to.
func (Adapter) Kind() domain.ForgeKind { return domain.ForgeGitLab }

// Open opens one merge request and returns where it can be read.
//
// The project is resolved by glab from the repository the command runs in, which
// is the task's own worktree: a linked worktree shares its remotes with the
// checkout it came from, so the project is the same one the branch was just
// pushed to. Feat does not derive an owner/project pair from the remote URL,
// for the reason a forge is declared rather than inferred — a self-hosted
// instance's URL is not something to guess a project path out of (ADR-071).
//
// `--yes` is what makes this non-interactive: glab otherwise asks whether to
// submit, and a daemon has no terminal to answer on. Nothing else about the
// request is left to a default — the title and the description are always
// passed, so glab never prompts for either.
//
// `--recover` is deliberately never passed. glab writes a recovery file under
// the user's own configuration directory when a creation fails, and its
// documented behaviour is to load the options back out of that file when the
// flag is given. Feat must never take that path: what is sent has to be the
// words the user just read, not the ones a previous attempt left on disk.
// Checked against Verified above: a recovery file whose contents had been
// replaced with nonsense was ignored and overwritten by a run that did not pass
// the flag.
func (a Adapter) Open(ctx context.Context, req forge.Request) (domain.MergeRequest, error) {
	if err := req.Validate(); err != nil {
		return domain.MergeRequest{}, err
	}
	if req.Body == editorDescription {
		// glab documents a description of exactly "-" as "open an editor", and a
		// daemon has no terminal to open one on. The installed version sent it
		// through as text instead, which is the behaviour that would hang if a
		// later one honoured its own documentation — and the description is the
		// one field where a leading hyphen is ordinary prose, so it cannot be
		// refused by the rule that keeps an option out of an argument.
		//
		// It is refused rather than altered. What was displayed is what is sent,
		// so a description Feat quietly changed would be worse than one it
		// declined to send (ADR-070).
		return domain.MergeRequest{}, fmt.Errorf(
			"the description is %q on its own, which %s reads as a request to open an editor rather "+
				"than as the description. Write something beside it in the draft",
			editorDescription, Executable)
	}

	arguments := []string{
		"mr", "create",
		"--source-branch", req.SourceBranch,
		"--target-branch", req.TargetBranch,
		"--title", req.Title,
		// Always passed, empty description included: a missing --description is
		// what sends glab to a prompt.
		"--description", req.Body,
		"--yes",
	}

	output, err := a.runner.Run(ctx, forge.Command{
		Program:   Executable,
		Arguments: arguments,
		Directory: req.Directory,
	})
	if err != nil {
		return domain.MergeRequest{}, fmt.Errorf("opening a GitLab merge request: %w", err)
	}
	if !output.Succeeded() {
		return domain.MergeRequest{}, &forge.Error{
			Forge:    domain.ForgeGitLab,
			Program:  Executable,
			ExitCode: output.ExitCode,
			Detail:   forge.Detail(output),
		}
	}

	request, found := parse(output)
	if !found {
		return domain.MergeRequest{}, &forge.Error{
			Forge:    domain.ForgeGitLab,
			Program:  Executable,
			ExitCode: output.ExitCode,
			Detail: "it reported success without printing the merge request's URL, so Feat cannot " +
				"record what was opened. Look on the forge before publishing again: " + forge.Detail(output),
		}
	}
	return request, nil
}

// urlPattern matches the merge request URL glab prints when it has created one.
//
// It is anchored on the path GitLab uses for a merge request rather than on a
// host, because a self-hosted instance is on whatever host the user runs; and it
// is a pattern rather than "the last line", because glab prints progress around
// it and has changed what it says more than once.
var urlPattern = regexp.MustCompile(`https?://[^\s"'<>]+/-/merge_requests/(\d+)`)

// parse reads the merge request out of what glab printed.
//
// The reference is derived from the URL rather than parsed separately: GitLab's
// own name for a merge request is "!<iid>", and the number in the URL is that
// iid. Reading one value twice from one string is how the two are guaranteed to
// describe the same request.
func parse(output forge.Output) (domain.MergeRequest, bool) {
	for _, stream := range []string{output.Stdout, output.Stderr} {
		match := urlPattern.FindStringSubmatch(stream)
		if match == nil {
			continue
		}
		return domain.MergeRequest{Reference: "!" + match[1], URL: match[0]}, true
	}
	return domain.MergeRequest{}, false
}
