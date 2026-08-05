package git

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/domain"
)

// recorder is a journal that remembers what it was told, in order.
type recorder struct {
	created []Created
	// fail makes the journal reject one repository, which is how a test
	// arranges a record that cannot be written.
	fail domain.RepositoryID
	// observe is called for each entry, so that a test can assert what already
	// existed when the record was written.
	observe func(Created)
}

func (r *recorder) Created(_ context.Context, created Created) error {
	if r.observe != nil {
		r.observe(created)
	}
	if created.Repository == r.fail {
		return errors.New("the state directory is full")
	}
	r.created = append(r.created, created)
	return nil
}

func (r *recorder) names() []string {
	names := make([]string, 0, len(r.created))
	for _, created := range r.created {
		names = append(names, created.Repository.String())
	}
	return names
}

// TestEachRepositoryIsRecordedBeforeTheNextIsCreated is the ordering that makes
// an interruption survivable.
//
// If two repositories were created and then recorded together, an interruption
// between them would leave a worktree nothing knows about. Recording each one
// before the next begins bounds what can be unrecorded at any moment to nothing.
func TestEachRepositoryIsRecordedBeforeTheNextIsCreated(t *testing.T) {
	f := twoRepositories(t, t.TempDir())
	adapter := New(f.git)

	plan, err := adapter.Plan(context.Background(), f.req)
	if err != nil {
		t.Fatalf("planning: %v", err)
	}

	var order []string
	journal := &recorder{observe: func(created Created) {
		order = append(order, "recorded "+created.Repository.String())
	}}

	// The fake records every command, so the interleaving of creations and
	// records is checked against the commands themselves.
	result, err := adapter.Apply(context.Background(), plan, journalWatcher{
		journal: journal,
		before: func() {
			order = append(order, "created "+lastWorktreeAdd(f.git))
		},
	})
	if err != nil {
		t.Fatalf("applying: %v", err)
	}

	if len(result.Created) != 2 {
		t.Fatalf("created %d repositories, want 2", len(result.Created))
	}
	want := []string{"created api", "recorded api", "created store", "recorded store"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Errorf("the order was %v, want %v", order, want)
	}
}

// journalWatcher runs a hook before each record, so that a test can observe the
// interleaving without changing the Journal interface for it.
type journalWatcher struct {
	journal Journal
	before  func()
}

func (w journalWatcher) Created(ctx context.Context, created Created) error {
	w.before()
	return w.journal.Created(ctx, created)
}

// lastWorktreeAdd returns the repository identifier of the most recent
// `worktree add`, taken from the path it created.
func lastWorktreeAdd(fake *fakeGit) string {
	fake.mu.Lock()
	defer fake.mu.Unlock()

	for i := len(fake.calls) - 1; i >= 0; i-- {
		call := fake.calls[i]
		if len(call) > 2 && call[0] == "worktree" && call[1] == "add" {
			return filepath.Base(call[len(call)-2])
		}
	}
	return "nothing"
}

// TestFailureHalfwayLeavesARecoverableRecord is the slice 4 acceptance
// criterion that a failure halfway through creation leaves a recoverable record
// and no unidentified worktree.
//
// The adapter's half of it is checked here: what exists is reported, what does
// not is named, and nothing that was created is silently removed. The daemon's
// half — that the record on disk names every worktree that could exist — is
// checked in internal/daemon.
func TestFailureHalfwayLeavesARecoverableRecord(t *testing.T) {
	f := twoRepositories(t, t.TempDir())
	adapter := New(f.git)

	plan, err := adapter.Plan(context.Background(), f.req)
	if err != nil {
		t.Fatalf("planning: %v", err)
	}

	// The world changed between planning and applying, which is exactly when a
	// half-finished creation happens.
	f.git.repositories["/checkout/store"].fail["worktree"] = errors.New("could not create leading directories")

	journal := &recorder{}
	result, err := adapter.Apply(context.Background(), plan, journal)
	if err == nil {
		t.Fatal("applying succeeded although the second repository failed")
	}

	var failure *ApplyError
	if !errors.As(err, &failure) {
		t.Fatalf("the error is %T, want an *ApplyError naming what exists", err)
	}
	if failure.Repository != "store" {
		t.Errorf("the failure names repository %s, want store", failure.Repository)
	}
	if len(failure.Created) != 1 || failure.Created[0] != "api" {
		t.Errorf("the failure reports %v as already prepared, want api", failure.Created)
	}
	if !strings.Contains(failure.Error(), "api") {
		t.Errorf("the message does not say what exists: %v", failure)
	}

	if names := journal.names(); len(names) != 1 || names[0] != "api" {
		t.Errorf("the journal recorded %v, want the one repository that was created", names)
	}
	if len(result.Created) != 1 || result.Remaining[0] != "store" {
		t.Errorf("the result is %+v, want one created and store remaining", result)
	}

	// Nothing is undone. A worktree that exists may already have been written
	// to, and removing it to tidy up a failed launch is a destructive act the
	// user did not ask for.
	if f.git.ran("worktree", "remove") || f.git.ran("branch", "-D") {
		t.Errorf("the failed launch removed something: %v", f.git.vectors())
	}
}

// TestARecordThatCannotBeWrittenStopsTheLaunch checks the other half of the
// ordering rule. If the record fails, continuing would create a second worktree
// the record has no observation for.
func TestARecordThatCannotBeWrittenStopsTheLaunch(t *testing.T) {
	f := twoRepositories(t, t.TempDir())
	adapter := New(f.git)

	plan, err := adapter.Plan(context.Background(), f.req)
	if err != nil {
		t.Fatalf("planning: %v", err)
	}

	_, err = adapter.Apply(context.Background(), plan, &recorder{fail: "api"})
	if err == nil {
		t.Fatal("applying succeeded although the record could not be written")
	}
	if !strings.Contains(err.Error(), "recording the worktree") {
		t.Errorf("the error does not say the record failed: %v", err)
	}
	if count := strings.Count(strings.Join(f.git.vectors(), "\n"), "worktree add"); count != 1 {
		t.Errorf("%d worktrees were created after the record failed, want 1", count)
	}
}

// TestAPlanWithProblemsIsNeverApplied checks that the problems a draft showed
// the user are the ones that stop it, rather than being rediscovered halfway
// through creating resources.
func TestAPlanWithProblemsIsNeverApplied(t *testing.T) {
	f := twoRepositories(t, t.TempDir())
	adapter := New(f.git)

	plan, err := adapter.Plan(context.Background(), f.req)
	if err != nil {
		t.Fatalf("planning: %v", err)
	}
	plan.Problems = append(plan.Problems, Problem{Repository: "api", Reason: "something is wrong"})

	if _, err := adapter.Apply(context.Background(), plan, &recorder{}); err == nil {
		t.Fatal("a plan with problems was applied")
	}
	if f.git.ran("worktree", "add") {
		t.Error("a worktree was created from a plan with problems")
	}
}

// TestAppliedWorktreesAreObserved checks that what the record says about a new
// worktree is an observation rather than an assumption: `git worktree add` runs
// the repository's own hooks, and a hook may leave files behind.
func TestAppliedWorktreesAreObserved(t *testing.T) {
	root := t.TempDir()
	f := twoRepositories(t, root)
	adapter := New(f.git)

	plan, err := adapter.Plan(context.Background(), f.req)
	if err != nil {
		t.Fatalf("planning: %v", err)
	}
	// A checkout hook left a generated file behind in the new worktree.
	f.git.repositories["/checkout/api"].untracked[filepath.Join(root, "api")] = "generated.txt"

	journal := &recorder{}
	if _, err := adapter.Apply(context.Background(), plan, journal); err != nil {
		t.Fatalf("applying: %v", err)
	}

	observation := journal.created[0].Observation
	if observation.ChangedFiles != 1 {
		t.Errorf("the new worktree reported %d changed files, want the one a hook left behind",
			observation.ChangedFiles)
	}
	if observation.ObservedAt.IsZero() {
		t.Error("the observation has no time, so it could never be aged out")
	}
}
