package git

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestEveryBasePolicyResolvesItsOwnRef checks the four policies
// docs/07-configuration-model.md documents. Each reads a different ref, and
// each records the commit rather than the ref, because a ref moves and a task's
// base must not (invariant 8).
func TestEveryBasePolicyResolvesItsOwnRef(t *testing.T) {
	var (
		remote  = commit("beef")
		local   = commit("cafe")
		current = commit("dad0")
		tagged  = commit("fee1")
	)

	arrange := func() *fakeGit {
		fake := newFakeGit()
		fake.add("/checkout/api", &fakeRepository{
			refs: map[string]string{
				"refs/remotes/origin/main": remote,
				"refs/heads/main":          local,
				"HEAD":                     current,
				"refs/heads/topic":         current,
				"refs/tags/v1.0.0":         tagged,
			},
			head: "refs/heads/topic",
		})
		return fake
	}

	for _, tc := range []struct {
		name    string
		request BaseRequest
		wantRef string
		want    string
	}{
		{
			name:    "remote reads the remote-tracking branch",
			request: BaseRequest{Policy: PolicyRemote, Remote: "origin", DefaultBranch: "main"},
			wantRef: "refs/remotes/origin/main", want: remote,
		},
		{
			name:    "local reads the local default branch",
			request: BaseRequest{Policy: PolicyLocal, DefaultBranch: "main"},
			wantRef: "refs/heads/main", want: local,
		},
		{
			name:    "current reads the checkout, and records the branch it is on",
			request: BaseRequest{Policy: PolicyCurrent},
			wantRef: "refs/heads/topic", want: current,
		},
		{
			name:    "explicit reads what the user named",
			request: BaseRequest{Policy: PolicyExplicit, Ref: "refs/tags/v1.0.0"},
			wantRef: "refs/tags/v1.0.0", want: tagged,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := arrange()
			tc.request.Dir = "/checkout/api"

			base, err := New(fake).ResolveBase(context.Background(), tc.request)
			if err != nil {
				t.Fatalf("resolving: %v", err)
			}
			if base.Ref != tc.wantRef {
				t.Errorf("recorded the ref %q, want %q", base.Ref, tc.wantRef)
			}
			if base.Commit != tc.want {
				t.Errorf("resolved to %s, want %s", base.Commit, tc.want)
			}
			if fake.ran("fetch") {
				t.Error("resolving a base fetched, and fetching is the caller's decision to make and to report")
			}
		})
	}
}

// TestADetachedCheckoutStillHasACurrentBase checks the case where there is no
// branch name to record: what matters is the commit, and HEAD is the only
// honest name for where it came from.
func TestADetachedCheckoutStillHasACurrentBase(t *testing.T) {
	fake := newFakeGit()
	fake.add("/checkout/api", &fakeRepository{refs: map[string]string{"HEAD": commit("dad0")}})

	base, err := New(fake).ResolveBase(context.Background(), BaseRequest{
		Dir: "/checkout/api", Policy: PolicyCurrent,
	})
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	if base.Ref != "HEAD" || base.Commit != commit("dad0") {
		t.Errorf("resolved to %+v, want the checkout's commit named HEAD", base)
	}
}

// TestAMissingBaseNamesTheRemedyForItsPolicy checks that the message says what
// to do, which differs by policy: an unfetched remote and a misconfigured
// default branch are fixed in different places.
func TestAMissingBaseNamesTheRemedyForItsPolicy(t *testing.T) {
	for _, tc := range []struct {
		request BaseRequest
		want    string
	}{
		{BaseRequest{Policy: PolicyRemote, Remote: "upstream", DefaultBranch: "trunk"}, "git fetch upstream"},
		{BaseRequest{Policy: PolicyLocal, DefaultBranch: "trunk"}, "configured default branch"},
		{BaseRequest{Policy: PolicyExplicit, Ref: "refs/heads/absent"}, "name a ref that exists"},
	} {
		fake := newFakeGit()
		fake.add("/checkout/api", &fakeRepository{})
		tc.request.Dir = "/checkout/api"

		_, err := New(fake).ResolveBase(context.Background(), tc.request)
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("the %s policy reported %v, want an error matching ErrNotFound", tc.request.Policy, err)
		}
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("the %s policy reported %v, want a message containing %q", tc.request.Policy, err, tc.want)
		}
	}
}

// TestOnlyTheRemotePolicyFetches checks that Feat does not reach the network for
// an answer that cannot depend on it.
func TestOnlyTheRemotePolicyFetches(t *testing.T) {
	for _, policy := range []BasePolicy{PolicyLocal, PolicyCurrent, PolicyExplicit} {
		f := twoRepositories(t, t.TempDir())
		for i := range f.req.Repositories {
			f.req.Repositories[i].Policy = policy
			f.req.Repositories[i].Ref = "refs/heads/main"
		}

		if _, err := New(f.git).Plan(context.Background(), f.req); err != nil {
			t.Fatalf("planning with the %s policy: %v", policy, err)
		}
		if f.git.ran("fetch") {
			t.Errorf("the %s policy fetched, and a fetch cannot change what it reads", policy)
		}
	}
}

// TestAnUnknownPolicyIsRejected checks that a policy this build does not
// implement fails rather than resolving something arbitrary.
func TestAnUnknownPolicyIsRejected(t *testing.T) {
	fake := newFakeGit()
	fake.add("/checkout/api", &fakeRepository{})

	if _, err := New(fake).ResolveBase(context.Background(), BaseRequest{
		Dir: "/checkout/api", Policy: BasePolicy("whatever-is-newest"),
	}); err == nil {
		t.Fatal("an undocumented base policy resolved")
	}
}
