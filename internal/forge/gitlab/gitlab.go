package gitlab

import (
	"context"
	"fmt"
	"regexp"

	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/forge"
)

// editorDescription is the one description glab does not read as a description.
// It is glab's own documented shorthand for "open an editor".
const editorDescription = "-"

// Executable is the GitLab command line. It is a constant rather than a
// configured value, for the reason the agent's executable is: a project that
// names the program the daemon runs on its owner's behalf is naming a program,
// and repositories.<id>.forge.kind already says which forge to publish to.
const Executable = "glab"

// Verified is the glab release this adapter's flags and behaviour were checked
// against, as docs/06-technical-architecture.md requires of a provider CLI. It
// is recorded rather than enforced, so a report of odd behaviour can be compared
// with what was tried (ADR-074).
const Verified = "1.114.0"

// Flags are the glab flags this adapter passes, exported so the opt-in test that
// asks an installed glab whether it still accepts them reads one list.
// TestRealGlabAcceptsTheFlagsThisAdapterPasses needs no account and no network,
// so it runs anywhere glab is installed.
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
// glab resolves the project from the repository it runs in, which is the task's
// worktree, and a linked worktree shares the remotes of the checkout it came
// from. Feat derives no owner/project pair from the remote URL, because a
// self-hosted instance's URL is not something to guess a project path out of
// (ADR-071).
//
// `--yes` makes this non-interactive, and the title and description are always
// passed, so glab never prompts. `--recover` is never passed, because it loads
// the options a previous attempt left on disk and what is sent has to be the
// words the user just read (ADR-074).
func (a Adapter) Open(ctx context.Context, req forge.Request) (domain.MergeRequest, error) {
	if err := req.Validate(); err != nil {
		return domain.MergeRequest{}, err
	}
	if req.Body == editorDescription {
		// glab documents a description of exactly "-" as "open an editor", and
		// a daemon has no terminal to open one on (ADR-074). The description is
		// the one field where a leading hyphen is ordinary prose, so the rule
		// that keeps an option out of an argument cannot refuse it.
		//
		// It is refused rather than altered, because what was displayed is what
		// is sent (ADR-070).
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
		// Always passed, empty description included. A missing --description
		// is what sends glab to a prompt.
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
// It anchors on GitLab's merge request path rather than on a host, because a
// self-hosted instance runs on the user's own; and it is a pattern rather than
// "the last line", because glab prints progress around it.
var urlPattern = regexp.MustCompile(`https?://[^\s"'<>]+/-/merge_requests/(\d+)`)

// parse reads the merge request out of what glab printed. The reference comes
// from the URL rather than from a second reading, so the two cannot describe
// different requests. GitLab's own name for a merge request is "!<iid>".
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
