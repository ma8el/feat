package git

import (
	"context"
	"errors"
	"fmt"
	"regexp"
)

// commitPattern matches a full Git object name. Feat records resolved commits
// rather than refs, so an abbreviated name is not accepted.
var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// BasePolicy decides which commit a task starts from.
//
// The values are the ones docs/07-configuration-model.md documents. They are
// declared here as well as in internal/config because this package is the one
// that acts on them, and an adapter that had to be handed a configuration type
// would know about YAML for no reason.
type BasePolicy string

// Base policies.
const (
	// PolicyRemote resolves the configured remote-tracking branch, after a
	// fetch. It is the recommended default: it starts a task from what the team
	// has, not from what this checkout happens to have.
	PolicyRemote BasePolicy = "remote"
	// PolicyLocal resolves the local default branch.
	PolicyLocal BasePolicy = "local"
	// PolicyCurrent resolves the ordinary checkout's current commit.
	PolicyCurrent BasePolicy = "current"
	// PolicyExplicit resolves a ref supplied during task preparation.
	PolicyExplicit BasePolicy = "explicit"
)

// Valid reports whether the policy is one of the documented ones.
func (p BasePolicy) Valid() bool {
	switch p {
	case PolicyRemote, PolicyLocal, PolicyCurrent, PolicyExplicit:
		return true
	default:
		return false
	}
}

// Base is a resolved starting point for a task repository.
type Base struct {
	// Ref is what the policy named, kept so that a task can explain where its
	// base came from.
	Ref string
	// Commit is the full object name the ref resolved to. This is what Feat
	// records and what every later comparison uses: a ref moves, and a task's
	// base must not (invariant 8).
	Commit string
}

// BaseRequest asks for one repository's base.
type BaseRequest struct {
	// Dir is the ordinary checkout the base is resolved in.
	Dir string
	// Policy decides which ref is read.
	Policy BasePolicy
	// Remote is the remote whose tracking branch PolicyRemote reads.
	Remote string
	// DefaultBranch is the branch PolicyRemote and PolicyLocal read.
	DefaultBranch string
	// Ref is the revision PolicyExplicit reads.
	Ref string
}

// ResolveBase resolves a base policy to an immutable commit.
//
// It never fetches. Fetching is a change to the user's repository and belongs
// to the caller that decided to make it, which also has somewhere to record that
// it failed; resolution then reads whatever the refs say now. That separation is
// what lets a base resolve offline from the last fetched state, with the staleness
// reported rather than hidden.
func (g *Git) ResolveBase(ctx context.Context, req BaseRequest) (Base, error) {
	if !req.Policy.Valid() {
		return Base{}, fmt.Errorf("%q is not a base policy", string(req.Policy))
	}

	ref, err := req.ref()
	if err != nil {
		return Base{}, err
	}

	if req.Policy == PolicyCurrent {
		// The current checkout's commit is the base, and HEAD is only how it is
		// named. A detached HEAD keeps the literal name, because there is no
		// better one to record.
		if head, attached, err := g.Head(ctx, req.Dir); err == nil && attached {
			ref = head
		} else if err != nil {
			return Base{}, err
		}
	}

	commit, err := g.Commit(ctx, req.Dir, ref)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Base{}, &MissingBaseError{Dir: req.Dir, Policy: req.Policy, Ref: ref, Remote: req.Remote}
		}
		return Base{}, err
	}
	return Base{Ref: ref, Commit: commit}, nil
}

// ref returns the revision the policy reads.
func (r BaseRequest) ref() (string, error) {
	switch r.Policy {
	case PolicyRemote:
		if err := checkArgument("remote", r.Remote); err != nil {
			return "", err
		}
		if err := checkArgument("default branch", r.DefaultBranch); err != nil {
			return "", err
		}
		return "refs/remotes/" + r.Remote + "/" + r.DefaultBranch, nil
	case PolicyLocal:
		if err := checkArgument("default branch", r.DefaultBranch); err != nil {
			return "", err
		}
		return "refs/heads/" + r.DefaultBranch, nil
	case PolicyCurrent:
		return "HEAD", nil
	case PolicyExplicit:
		if err := checkArgument("base ref", r.Ref); err != nil {
			return "", err
		}
		return r.Ref, nil
	default:
		return "", fmt.Errorf("%q is not a base policy", string(r.Policy))
	}
}

// MissingBaseError reports a base a policy could not resolve.
//
// It carries the policy and the ref because the remedy differs: a missing
// remote-tracking ref usually means the remote was never fetched, and a missing
// local branch usually means the configured default branch has another name.
type MissingBaseError struct {
	// Dir is the checkout the ref was looked for in.
	Dir string
	// Policy is the base policy that named it.
	Policy BasePolicy
	// Ref is the revision that did not resolve.
	Ref string
	// Remote is the configured remote, for the remote policy.
	Remote string
}

func (e *MissingBaseError) Error() string {
	message := fmt.Sprintf("%s has no %s, which the %q base policy resolves", e.Dir, e.Ref, string(e.Policy))
	switch e.Policy {
	case PolicyRemote:
		message += fmt.Sprintf(": run `git fetch %s` there, or correct the configured remote and default branch", e.Remote)
	case PolicyLocal:
		message += ": correct the configured default branch"
	case PolicyExplicit:
		message += ": name a ref that exists in that repository"
	case PolicyCurrent:
		message += ": the checkout has no commit yet"
	}
	return message
}

// Unwrap makes a missing base match ErrNotFound, so a caller can ask whether a
// base is absent without knowing which policy asked for it.
func (e *MissingBaseError) Unwrap() error { return ErrNotFound }
