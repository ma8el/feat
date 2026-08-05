package git

import (
	"cmp"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/domain"
)

// The identifiers the tests in this package share. The task identifier is a
// real version 4 UUID because the domain validates the form.
const (
	testProject = domain.ProjectID("app")
	testTask    = domain.TaskID("0f8fad5b-d9cb-469f-a165-70867728950e")
)

// remoteBase is the commit the fixtures resolve a remote base policy to.
var remoteBase = commit("beef")

// fixture is a two-repository task: one the agent may write to, one it may only
// read. It is the shape docs/08-v0-scope.md describes for the dogfood project,
// with generic names.
type fixture struct {
	git  *fakeGit
	root string
	req  Request
}

// twoRepositories arranges a fake project whose worktrees live under root.
func twoRepositories(t *testing.T, root string) *fixture {
	t.Helper()

	fake := newFakeGit()
	for _, dir := range []string{"/checkout/api", "/checkout/store"} {
		fake.add(dir, &fakeRepository{
			refs: map[string]string{
				"refs/remotes/origin/main": remoteBase,
				"refs/heads/main":          commit("cafe"),
				"HEAD":                     commit("cafe"),
			},
			head:      "refs/heads/main",
			worktrees: []Worktree{{Path: dir, Head: commit("cafe"), Branch: "refs/heads/main"}},
		})
	}

	return &fixture{
		git:  fake,
		root: root,
		req: Request{
			Project: testProject,
			Task:    testTask,
			Root:    root,
			Fetch:   true,
			Repositories: []RepositoryRequest{
				{
					ID: "api", HostPath: "/checkout/api", Remote: "origin", DefaultBranch: "main",
					Access: domain.TaskAccessReadWrite, Policy: PolicyRemote,
					Branch: "feat/0f8fad5b-a-title", WorktreePath: filepath.Join(root, "api"),
					ContainerPath: "/srv/api",
				},
				{
					ID: "store", HostPath: "/checkout/store", Remote: "origin", DefaultBranch: "main",
					Access: domain.TaskAccessReadOnly, Policy: PolicyRemote,
					WorktreePath: filepath.Join(root, "store"), ContainerPath: "/srv/store",
				},
			},
		},
	}
}

// TestPlanResolvesBasesAndCreatesNothing checks the draft half of task
// preparation: FR-TASK-003 requires resolved bases and proposed names before
// anything exists, and forbids anything existing until the user confirms.
func TestPlanResolvesBasesAndCreatesNothing(t *testing.T) {
	root := filepath.Join(t.TempDir(), "worktrees", "app", testTask.String())
	f := twoRepositories(t, root)

	plan, err := New(f.git).Plan(context.Background(), f.req)
	if err != nil {
		t.Fatalf("planning: %v", err)
	}

	if len(plan.Repositories) != 2 {
		t.Fatalf("planned %d repositories, want 2", len(plan.Repositories))
	}
	for _, repository := range plan.Repositories {
		if repository.BaseCommit != remoteBase {
			t.Errorf("repository %s resolved to %s, want the fetched remote-tracking commit %s",
				repository.ID, repository.BaseCommit, remoteBase)
		}
		if repository.BaseRef != "refs/remotes/origin/main" {
			t.Errorf("repository %s recorded the base ref %q", repository.ID, repository.BaseRef)
		}
	}

	// Read-write gets a branch and read-only must not have one (invariant 7).
	if plan.Repositories[0].Branch != "feat/0f8fad5b-a-title" {
		t.Errorf("the read-write repository was planned with branch %q", plan.Repositories[0].Branch)
	}
	if plan.Repositories[1].Branch != "" {
		t.Errorf("the read-only repository was planned with branch %q", plan.Repositories[1].Branch)
	}

	for _, forbidden := range []string{"worktree add", "branch", "checkout", "commit"} {
		for _, vector := range f.git.vectors() {
			if strings.HasPrefix(vector, forbidden) {
				t.Errorf("planning ran `git %s`, and a plan must create nothing", vector)
			}
		}
	}
	if entries, err := os.ReadDir(filepath.Dir(root)); err == nil && len(entries) > 0 {
		t.Errorf("planning left %d entries under the worktree root", len(entries))
	}
}

// TestFetchFailureIsReportedAndTheTaskContinues checks FR-GIT-001's "when
// network access is available". A laptop on a train can still start a task from
// the last fetched state; what it must not do is leave the user believing the
// base is current.
func TestFetchFailureIsReportedAndTheTaskContinues(t *testing.T) {
	f := twoRepositories(t, filepath.Join(t.TempDir(), "worktrees"))
	f.git.repositories["/checkout/api"].fail["fetch"] = errors.New("could not resolve host: example.invalid")

	plan, err := New(f.git).Plan(context.Background(), f.req)
	if err != nil {
		t.Fatalf("planning: %v", err)
	}
	if len(plan.Repositories) != 2 {
		t.Fatalf("a failed fetch stopped the plan: %+v", plan.Problems)
	}
	if len(plan.Notes) != 1 || plan.Notes[0].Repository != "api" {
		t.Fatalf("notes are %+v, want one about repository api", plan.Notes)
	}
	if !strings.Contains(plan.Notes[0].Summary, "last fetched state") {
		t.Errorf("the note does not say the base may be stale: %q", plan.Notes[0].Summary)
	}
}

// TestMissingRemoteBaseNamesTheRemedy checks that the commonest first-run
// failure — a remote that was never fetched — says what to do about it.
func TestMissingRemoteBaseNamesTheRemedy(t *testing.T) {
	f := twoRepositories(t, filepath.Join(t.TempDir(), "worktrees"))
	delete(f.git.repositories["/checkout/store"].refs, "refs/remotes/origin/main")

	_, err := New(f.git).Plan(context.Background(), f.req)
	if err == nil {
		t.Fatal("planning succeeded without a base commit")
	}
	if !strings.Contains(err.Error(), "git fetch origin") {
		t.Errorf("the error does not name the remedy: %v", err)
	}
}

// TestEveryProblemIsReported checks that a plan reports all of its problems
// rather than the first, for the reason configuration validation does: the user
// is going to fix them by hand.
func TestEveryProblemIsReported(t *testing.T) {
	f := twoRepositories(t, filepath.Join(t.TempDir(), "worktrees"))
	delete(f.git.repositories["/checkout/api"].refs, "refs/remotes/origin/main")
	delete(f.git.repositories["/checkout/store"].refs, "refs/remotes/origin/main")

	plan, err := New(f.git).Plan(context.Background(), f.req)
	if err == nil {
		t.Fatal("planning succeeded with two unresolvable bases")
	}
	if len(plan.Problems) != 2 {
		t.Fatalf("reported %d problems, want 2: %+v", len(plan.Problems), plan.Problems)
	}
	var reported *PlanError
	if !errors.As(err, &reported) {
		t.Fatalf("the error is %T, want a *PlanError listing every problem", err)
	}
	for _, id := range []string{"api", "store"} {
		if !strings.Contains(reported.Error(), id) {
			t.Errorf("the error does not name repository %s: %v", id, reported)
		}
	}
}

// TestCollisionsAreDetectedBeforeAnythingIsCreated covers the three ways a task
// can ask for a resource something already holds. None of them is resolved by
// choosing another name: a branch Feat renamed is a branch the user did not
// agree to and will look for under the name they saw.
func TestCollisionsAreDetectedBeforeAnythingIsCreated(t *testing.T) {
	t.Run("the branch already exists", func(t *testing.T) {
		f := twoRepositories(t, filepath.Join(t.TempDir(), "worktrees"))
		f.git.repositories["/checkout/api"].refs["refs/heads/feat/0f8fad5b-a-title"] = commit("dead")

		_, err := New(f.git).Plan(context.Background(), f.req)
		if err == nil || !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("planning reported %v, want a branch collision", err)
		}
	})

	t.Run("the worktree path is taken", func(t *testing.T) {
		root := t.TempDir()
		f := twoRepositories(t, root)
		if err := os.MkdirAll(filepath.Join(root, "api"), 0o755); err != nil {
			t.Fatalf("arranging the occupied path: %v", err)
		}

		_, err := New(f.git).Plan(context.Background(), f.req)
		if err == nil || !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("planning reported %v, want an occupied worktree path", err)
		}
	})

	t.Run("git already has a worktree there", func(t *testing.T) {
		root := t.TempDir()
		f := twoRepositories(t, root)
		repository := f.git.repositories["/checkout/api"]
		repository.worktrees = append(repository.worktrees, Worktree{Path: filepath.Join(root, "api")})

		_, err := New(f.git).Plan(context.Background(), f.req)
		if err == nil || !strings.Contains(err.Error(), "already has a worktree") {
			t.Fatalf("planning reported %v, want a registered worktree collision", err)
		}
	})
}

// TestUnsafeAndBroadPathsAreRejected is the slice 4 acceptance criterion that
// tests cover unsafe and broad path rejection.
//
// Every path below is one Feat would later remove, so each case is a directory
// that must never become a task worktree. The check runs before any Git command,
// so a rejected path is a plan that ran nothing at all.
func TestUnsafeAndBroadPathsAreRejected(t *testing.T) {
	// A root deep enough to be one Feat could own, written without any literal
	// that names a real machine's directories.
	safeRoot := filepath.Join("/state", "feat", "worktrees")

	for _, tc := range []struct {
		name string
		root string
		// path is the proposed worktree. An empty value is the root's own "api"
		// subdirectory, which is what an ordinary task would ask for.
		path     string
		checkout string
		want     string
	}{
		{
			name: "the filesystem root",
			root: "/",
			want: "not a directory Feat may own",
		},
		{
			// Every entry of the shared-directory list is one component deep, so
			// one stands for all of them here and the list itself is checked in
			// internal/paths.
			name: "a directory directly below the filesystem root",
			root: filepath.Join("/", "var"),
			want: "not a directory Feat may own",
		},
		{
			name: "a shared temporary directory",
			root: filepath.Join("/", "tmp"),
			want: "not a directory Feat may own",
		},
		{
			name: "the directory holding every user's home",
			root: filepath.Join("/", "Users"),
			want: "not a directory Feat may own",
		},
		{
			name: "a relative path",
			root: "worktrees",
			want: "not a directory Feat may own",
		},
		{
			name: "a worktree outside the root",
			root: safeRoot, path: filepath.Join("/state", "elsewhere", "api"),
			want: "not inside the worktree root",
		},
		{
			name: "a worktree that escapes the root by traversal",
			root: safeRoot, path: safeRoot + "/../../../../etc",
			want: "not a clean path",
		},
		{
			name: "the root itself",
			root: safeRoot, path: safeRoot,
			want: "not inside the worktree root",
		},
		{
			name: "a worktree inside a checkout",
			root: safeRoot, checkout: safeRoot,
			want: "overlaps the checkout",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.path == "" {
				tc.path = filepath.Join(tc.root, "api")
			}
			// The whole request is checked first, so a broad root fails before any
			// repository is looked at.
			fake := newFakeGit()
			fake.add("/checkout/api", &fakeRepository{refs: map[string]string{"refs/heads/main": remoteBase}})

			// The second repository is what puts a checkout where the case needs
			// one: the overlap rule compares a proposed worktree against the
			// checkouts of every repository in the same request.
			_, err := New(fake).Plan(context.Background(), Request{
				Project: testProject,
				Task:    testTask,
				Root:    tc.root,
				Repositories: []RepositoryRequest{{
					ID: "api", HostPath: "/checkout/api", Remote: "origin", DefaultBranch: "main",
					Access: domain.TaskAccessReadOnly, Policy: PolicyLocal,
					WorktreePath: tc.path,
				}, {
					ID: "other", HostPath: cmp.Or(tc.checkout, "/checkout/other"),
					Remote: "origin", DefaultBranch: "main",
					Access: domain.TaskAccessReadOnly, Policy: PolicyLocal,
					WorktreePath: filepath.Join(tc.root, "other"),
				}},
			})
			if err == nil {
				t.Fatalf("the path %q was accepted as a task worktree", tc.path)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("rejected with %q, want a message containing %q", err, tc.want)
			}
			if fake.ran("worktree", "add") {
				t.Error("a worktree was created for a path that was rejected")
			}
		})
	}
}

// TestOneWorktreePerRepository checks the rule that keeps two repositories of
// one task apart. Sharing a directory would put the second checkout on top of
// the first.
func TestOneWorktreePerRepository(t *testing.T) {
	root := filepath.Join(t.TempDir(), "worktrees")
	f := twoRepositories(t, root)
	f.req.Repositories[1].WorktreePath = f.req.Repositories[0].WorktreePath

	_, err := New(f.git).Plan(context.Background(), f.req)
	if err == nil || !strings.Contains(err.Error(), "share the worktree") {
		t.Fatalf("planning reported %v, want two repositories sharing one worktree", err)
	}
}

// TestReadWriteRepositoriesNeedABranch checks invariant 7 from the adapter's
// side: a read-write repository with no branch would put the agent's commits on
// whatever the worktree happened to check out.
func TestReadWriteRepositoriesNeedABranch(t *testing.T) {
	f := twoRepositories(t, filepath.Join(t.TempDir(), "worktrees"))
	f.req.Repositories[0].Branch = ""
	f.req.Repositories[1].Branch = "feat/should-not-be-here"

	plan, err := New(f.git).Plan(context.Background(), f.req)
	if err == nil {
		t.Fatal("planning accepted a read-write repository without a branch")
	}
	if len(plan.Problems) != 2 {
		t.Fatalf("reported %d problems, want one per repository: %+v", len(plan.Problems), plan.Problems)
	}
}
