package github

import (
	"context"
	"fmt"
	"regexp"

	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/forge"
)

// Executable is the GitHub command line.
//
// It is a constant rather than a configured value, for the reason the agent's
// executable is: a project that could name the program the daemon runs on its
// owner's behalf would be naming a program, and which forge a repository
// publishes to is already declared by repositories.<id>.forge.kind.
const Executable = "gh"

// Verified is the gh release this adapter's flags and behaviour were checked
// against, as docs/06-technical-architecture.md requires of a provider CLI.
//
// It is recorded rather than enforced, for the reason the GitLab adapter records
// its own: another version is far more likely to work than not, and refusing to
// publish because a user upgraded gh would be Feat inventing a failure.
const Verified = "2.97.0"

// Flags are the gh flags this adapter passes, exported so that the opt-in test
// which asks an installed gh whether it still accepts them has one list to check
// rather than a copy of it.
//
// There is deliberately no confirmation flag among them. gh has no equivalent of
// glab's `--yes`: supplying a title and a body is what makes it non-interactive,
// and without them it refuses — "must provide `--title` and `--body` … when not
// running interactively" — rather than waiting for a terminal the daemon does
// not have. That refusal is worth more than a flag would be, because it fails
// loudly where the other failure mode is a publication that hangs.
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
// The repository is resolved by gh from the one the command runs in, which is
// the task's own worktree: a linked worktree shares its remotes with the
// checkout it came from, so the repository is the one the branch was just pushed
// to. Feat does not derive an owner/repo pair from the remote URL, for the
// reason a forge is declared rather than inferred — a GitHub Enterprise host is
// not something to guess a repository path out of (ADR-071).
//
// `--head` names a branch of that same repository, which is what a task branch
// is: Feat pushes to the repository's own remote, so there is no fork to
// qualify it with.
//
// Two things this deliberately does not do, both because gh does not need them
// and glab did. It passes no confirmation flag, because a title and a body are
// what make gh non-interactive. And it refuses no description: glab reads a
// description of exactly "-" as a request to open an editor, where gh puts that
// special meaning on `--body-file` and treats `--body` as text whatever it says.
// Both were checked against the version above rather than assumed, as was the
// third asymmetry: gh writes no recovery file of its own. glab saves one when a
// creation fails and can load a previous attempt's options back out of it, which
// its adapter is careful never to ask for; gh's `--recover` takes an explicit
// file name and nothing is written without one, so there is nothing here for a
// stale attempt to come back through.
func (a Adapter) Open(ctx context.Context, req forge.Request) (domain.MergeRequest, error) {
	if err := req.Validate(); err != nil {
		return domain.MergeRequest{}, err
	}

	arguments := []string{
		"pr", "create",
		"--head", req.SourceBranch,
		"--base", req.TargetBranch,
		"--title", req.Title,
		// Always passed, empty body included: without it gh refuses for want of
		// a terminal to ask on.
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

// urlPattern matches the pull request URL gh prints when it has created one.
//
// It is anchored on the path GitHub uses for a pull request rather than on a
// host, because GitHub Enterprise is on whatever host the user runs; and it is a
// pattern rather than "the last line", because gh prints progress around it.
var urlPattern = regexp.MustCompile(`https?://[^\s"'<>]+/pull/(\d+)`)

// parse reads the pull request out of what gh printed.
//
// The reference is derived from the URL rather than parsed separately: GitHub's
// own name for a pull request is "#<number>", and the number in the URL is that
// number. Reading one value twice from one string is how the two are guaranteed
// to describe the same request.
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
