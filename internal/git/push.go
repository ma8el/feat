package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PushRequest asks for one task branch to be pushed to its remote.
type PushRequest struct {
	// Worktree is the task worktree the push runs in. It is the task's own
	// checkout rather than the user's, so a push is scoped to the branch the
	// task owns.
	Worktree string
	// Remote is the remote to push to.
	Remote string
	// Branch is the task branch. It is pushed to a branch of the same name on
	// the remote, which is what the merge request is then opened from.
	Branch string
	// Commit is the exact commit to push.
	//
	// It is pushed by object name rather than by pushing whatever HEAD points
	// at, because a publication records the commit the agent's draft describes
	// and the push has to put that commit on the remote — not whatever the
	// worktree acquired between the plan and the push.
	Commit string
}

// PushReport is what a push did not do.
//
// It is returned whether the push succeeded or failed, because what it names is
// decided before the push runs and is worth knowing either way: a user whose
// pre-push hook scans for secrets needs to be told that Feat's publication is
// the one route out that skips it (ADR-070).
type PushReport struct {
	// Skipped names each thing this push did not run, as a sentence a user
	// reads. It is empty where there was nothing to skip, because a check with
	// nothing to report reports nothing (ADR-028).
	Skipped []string
}

// Push puts one commit on a remote branch.
//
// It runs with hooks, the pager, and an external diff driver disabled, because
// a task's repositories are linked worktrees whose `.git/hooks` and
// `.git/config` the agent can write, and approving a publication must not be
// how a user runs what the agent left there (ADR-050, ADR-070). The settings are
// in the environment of this one process and are never written to the user's
// configuration.
//
// What that costs is reported rather than hidden. A `pre-push` hook is not
// always its author's own convenience — it may be what scans for secrets before
// anything leaves the machine — so where one exists the report names it, and a
// user who depends on it can push by hand instead.
//
// Nothing here forces. A remote that refuses a non-fast-forward is a failure of
// this repository's publication and is recorded as one; overwriting somebody
// else's commits to make a merge request open is not a trade Feat makes.
func (g *Git) Push(ctx context.Context, req PushRequest) (PushReport, error) {
	if req.Worktree == "" {
		return PushReport{}, fmt.Errorf("a push needs the worktree it runs in")
	}
	if err := checkArgument("remote", req.Remote); err != nil {
		return PushReport{}, err
	}
	if err := checkArgument("branch", req.Branch); err != nil {
		return PushReport{}, err
	}
	if !commitPattern.MatchString(req.Commit) {
		return PushReport{}, fmt.Errorf("a push carries a resolved commit, but %q is not one", req.Commit)
	}

	// Resolved first, so that a push which then fails still says what it was not
	// going to run: the two facts are independent and the user needs both.
	report := PushReport{Skipped: g.suppressed(ctx, req.Worktree)}

	// The refspec names the object rather than a local ref, so what lands on the
	// remote is the commit that was planned.
	refspec := req.Commit + ":refs/heads/" + req.Branch
	if _, err := g.runner.RunWith(ctx, req.Worktree, PushEnvironment(), "push", req.Remote, refspec); err != nil {
		return report, err
	}
	return report, nil
}

// SuppressedHooks reports what a push in this worktree would not run.
//
// It is separate from Push because the user is told before they approve rather
// than after Feat has acted: `feat doctor` asks it of a configured repository,
// and a publication asks it of a task's worktree while it is still describing
// what publishing would do.
func (g *Git) SuppressedHooks(ctx context.Context, dir string) ([]string, error) {
	if dir == "" {
		return nil, fmt.Errorf("reporting the hooks a push would skip needs a directory")
	}
	return g.suppressed(ctx, dir), nil
}

// suppressed collects the report.
//
// A question that cannot be answered becomes a line saying so rather than an
// error. Feat is about to push either way, and "there may be a hook here and I
// could not tell" is what the user has to act on; failing the publication over
// it would be a diagnostic refusing an operation it only comments on.
func (g *Git) suppressed(ctx context.Context, dir string) []string {
	var skipped []string

	configured, err := g.runner.Run(ctx, dir, "config", "--get", "core.hooksPath")
	switch code, ran := exitCode(err); {
	case err == nil && configured != "":
		skipped = append(skipped, fmt.Sprintf(
			"core.hooksPath is set to %s, and Feat's push runs no hook from it", configured))
	case err != nil && ran && code == 1:
		// Git's answer "not set", which is the ordinary case and not a failure.
	case err != nil:
		skipped = append(skipped, fmt.Sprintf(
			"whether this repository configures core.hooksPath could not be read (%v), "+
				"and Feat's push runs no hook either way", err))
	}

	hooks, err := g.runner.Run(ctx, dir, "rev-parse", "--git-path", "hooks")
	if err != nil {
		skipped = append(skipped, fmt.Sprintf(
			"this repository's hook directory could not be resolved (%v), so whether it has a pre-push hook "+
				"is unknown; Feat's push runs none either way", err))
		return skipped
	}
	if !filepath.IsAbs(hooks) {
		// `rev-parse --git-path` answers relative to the directory it ran in.
		hooks = filepath.Join(dir, hooks)
	}

	// The filesystem rather than Git: Git has no command that reports which
	// hooks a repository has without running one, and running one is what this
	// exists to avoid. The path came from Git rather than from configuration.
	hook := filepath.Join(hooks, "pre-push")
	info, err := os.Stat(hook)
	switch {
	case err == nil && !info.IsDir():
		// A file named exactly `pre-push` is the hook; Git's own examples are
		// installed as `pre-push.sample` and never run.
		skipped = append(skipped, fmt.Sprintf(
			"%s exists and Feat's push does not run it", hook))
	case err != nil && !os.IsNotExist(err):
		skipped = append(skipped, fmt.Sprintf(
			"whether %s exists could not be read (%v), and Feat's push runs no hook either way", hook, err))
	}
	return skipped
}

// String renders a push report as one line, or an empty string when there was
// nothing to skip.
func (r PushReport) String() string { return strings.Join(r.Skipped, "; ") }
