package github

import (
	"context"
	"fmt"
	"regexp"

	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/forge"
)

// Executable is the GitHub command line. It is a constant rather than a
// configured value, for the reason the agent's executable is: a project that
// names the program the daemon runs on its owner's behalf is naming a program,
// and repositories.<id>.forge.kind already says which forge to publish to.
const Executable = "gh"

// Verified is the gh release this adapter's flags and behaviour were checked
// against, as docs/06-technical-architecture.md requires of a provider CLI. It
// is recorded rather than enforced, because refusing to publish when a user
// upgraded gh would be Feat inventing a failure.
const Verified = "2.97.0"

// Flags are the gh flags this adapter passes, exported so the opt-in test that
// asks an installed gh whether it still accepts them reads one list.
//
// None of them is a confirmation flag. gh has no equivalent of glab's `--yes`:
// a title and a body are what make it non-interactive, and without them it
// refuses rather than waiting for a terminal the daemon does not have.
var Flags = []string{
	"--head",
	"--base",
	"--title",
	"--body",
}

// Adapter opens pull requests through gh.
type Adapter struct{ runner forge.Runner }

var _ forge.Adapter = Adapter{}

// New returns the GitHub adapter driving the given runner.
func New(runner forge.Runner) Adapter {
	if runner == nil {
		runner = forge.HostRunner{}
	}
	return Adapter{runner: runner}
}

// Kind returns the forge this adapter publishes to.
func (Adapter) Kind() domain.ForgeKind { return domain.ForgeGitHub }

// Open opens one pull request and returns where it can be read.
//
// gh resolves the repository from the directory it runs in, which is the task's
// worktree, and a linked worktree shares the remotes of the checkout it came
// from. Feat derives no owner/repo pair from the remote URL, because a GitHub
// Enterprise host is not something to guess a repository path out of (ADR-071).
// `--head` names a branch of that same repository, since Feat pushes to the
// repository's own remote and there is no fork to qualify.
//
// Two of glab's hazards do not arise here, both checked against the version
// above. gh reads `--body` as text whatever it says, putting the editor meaning
// on `--body-file`; and gh writes no recovery file unless `--recover` names one,
// so no previous attempt's options can come back through.
func (a Adapter) Open(ctx context.Context, req forge.Request) (domain.MergeRequest, error) {
	if err := req.Validate(); err != nil {
		return domain.MergeRequest{}, err
	}

	arguments := []string{
		"pr", "create",
		"--head", req.SourceBranch,
		"--base", req.TargetBranch,
		"--title", req.Title,
		// Always passed, empty body included. Without it gh refuses for want
		// of a terminal to ask on.
		"--body", req.Body,
	}

	output, err := a.runner.Run(ctx, forge.Command{
		Program:   Executable,
		Arguments: arguments,
		Directory: req.Directory,
	})
	if err != nil {
		return domain.MergeRequest{}, fmt.Errorf("opening a GitHub pull request: %w", err)
	}
	if !output.Succeeded() {
		return domain.MergeRequest{}, &forge.Error{
			Forge:    domain.ForgeGitHub,
			Program:  Executable,
			ExitCode: output.ExitCode,
			Detail:   forge.Detail(output),
		}
	}

	request, found := parse(output)
	if !found {
		return domain.MergeRequest{}, &forge.Error{
			Forge:    domain.ForgeGitHub,
			Program:  Executable,
			ExitCode: output.ExitCode,
			Detail: "it reported success without printing the pull request's URL, so Feat cannot " +
				"record what was opened. Look on the forge before publishing again: " + forge.Detail(output),
		}
	}
	return request, nil
}

// urlPattern matches the pull request URL gh prints when it has created one. It
// anchors on GitHub's pull request path rather than on a host, because GitHub
// Enterprise runs on the user's own; and it is a pattern rather than "the last
// line", because gh prints progress around it.
var urlPattern = regexp.MustCompile(`https?://[^\s"'<>]+/pull/(\d+)`)

// parse reads the pull request out of what gh printed. The reference comes from
// the URL rather than from a second reading, so the two cannot describe
// different requests. GitHub's own name for a pull request is "#<number>".
func parse(output forge.Output) (domain.MergeRequest, bool) {
	for _, stream := range []string{output.Stdout, output.Stderr} {
		match := urlPattern.FindStringSubmatch(stream)
		if match == nil {
			continue
		}
		return domain.MergeRequest{Reference: "#" + match[1], URL: match[0]}, true
	}
	return domain.MergeRequest{}, false
}
