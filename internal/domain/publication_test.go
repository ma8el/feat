package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// testReadOnly is a repository the task binds read-only, which a publication
// can never reach: a read-only binding has no task branch (invariant 7).
const testReadOnly = RepositoryID("docs")

// publishableTask returns a launched task binding two repositories a
// publication may reach and one it may not.
//
// Two, because one merge request per changed repository is only interesting
// with more than one; and the read-only third, because what a publication
// refuses is as much part of the shape as what it records.
func publishableTask(t *testing.T) *Task {
	t.Helper()

	task := draftTask(t)
	setBrief(t, task)
	bindPrimary(t, task)
	bind(t, task, TaskRepository{
		RepositoryID: testSecondary,
		Access:       TaskAccessReadWrite,
		Branch:       "feat/7f3a1c2e-export",
		WorktreePath: "/srv/state/worktrees/example/7f3a1c2e/schema",
	})
	bind(t, task, TaskRepository{
		RepositoryID: testReadOnly,
		Access:       TaskAccessReadOnly,
		WorktreePath: "/srv/state/worktrees/example/7f3a1c2e/docs",
	})

	resolvePrimary(t, task)
	for _, id := range []RepositoryID{testSecondary, testReadOnly} {
		if err := task.ResolveBase(id, "origin/main", otherCommit, origin); err != nil {
			t.Fatalf("resolving the base of %s: %v", id, err)
		}
	}

	if err := task.TransitionTo(WorkflowPreparing, origin); err != nil {
		t.Fatalf("confirming the draft: %v", err)
	}
	if err := task.AttachSession(testSession(t), origin); err != nil {
		t.Fatalf("attaching a session: %v", err)
	}
	if err := task.TransitionTo(WorkflowWorking, origin); err != nil {
		t.Fatalf("starting work: %v", err)
	}
	return task
}

// plan is the pair of entries publishableTask can publish.
func plan() []RepositoryPublication {
	return []RepositoryPublication{
		{
			RepositoryID: testPrimary,
			Forge:        ForgeGitLab,
			Remote:       "origin",
			BaseBranch:   "main",
			Commit:       testCommit,
		},
		{
			RepositoryID: testSecondary,
			Forge:        ForgeGitHub,
			Remote:       "origin",
			BaseBranch:   "main",
			Commit:       otherCommit,
		},
	}
}

// TestAPlanIsRecordedBeforeAnythingIsAttempted is the ordering ADR-073 requires.
//
// A merge request is on somebody else's server and cannot be un-created, so
// every repository a publication could reach is written down before the first
// one is attempted. Anything that reads the record then knows what exists and
// what does not, rather than having to ask the forges.
func TestAPlanIsRecordedBeforeAnythingIsAttempted(t *testing.T) {
	task := publishableTask(t)

	if err := task.PlanPublication(plan(), origin); err != nil {
		t.Fatalf("planning the publication: %v", err)
	}
	if task.Publication == nil {
		t.Fatal("the task records no publication")
	}
	if got := len(task.Publication.Repositories); got != 2 {
		t.Fatalf("the plan holds %d repositories, want both", got)
	}

	for _, entry := range task.Publication.Repositories {
		if entry.State != PublicationPlanned {
			t.Errorf("%s is %s before anything was attempted", entry.RepositoryID, entry.State)
		}
		if entry.Request != nil || entry.Failure != "" || !entry.AttemptedAt.IsZero() {
			t.Errorf("%s carries a result before anything was attempted: %+v", entry.RepositoryID, entry)
		}
	}
	if got := len(task.Publication.Pending()); got != 2 {
		t.Errorf("%d repositories are pending, want both", got)
	}
	if err := task.Validate(); err != nil {
		t.Errorf("a task with a planned publication does not validate: %v", err)
	}
}

// TestAnInterruptedPublicationNamesWhatItDidNotAttempt is the property the
// ordering buys.
//
// Half a publication is a recorded state rather than one to be undone, so what
// the user is owed is which repositories published and which did not — and the
// ones that did not are named rather than deduced.
func TestAnInterruptedPublicationNamesWhatItDidNotAttempt(t *testing.T) {
	task := publishableTask(t)
	if err := task.PlanPublication(plan(), origin); err != nil {
		t.Fatalf("planning the publication: %v", err)
	}

	opened := MergeRequest{Reference: "!12", URL: "https://forge.example.com/core/-/merge_requests/12"}
	if err := task.RecordPublished(testPrimary, opened, origin.Add(time.Minute)); err != nil {
		t.Fatalf("recording the merge request: %v", err)
	}

	// The interruption: nothing was attempted for the second repository.
	pending := task.Publication.Pending()
	if len(pending) != 1 || pending[0].RepositoryID != testSecondary {
		t.Fatalf("pending = %+v, want only %s", pending, testSecondary)
	}
	published := task.Publication.Published()
	if len(published) != 1 || published[0].RepositoryID != testPrimary {
		t.Fatalf("published = %+v, want only %s", published, testPrimary)
	}
	if published[0].Request == nil || published[0].Request.URL != opened.URL {
		t.Errorf("the record does not name the merge request it opened: %+v", published[0])
	}
	if published[0].AttemptedAt.IsZero() {
		t.Error("a published repository records no time it was attempted")
	}
	if err := task.Validate(); err != nil {
		t.Errorf("an interrupted publication does not validate: %v", err)
	}
}

// TestOneRepositorysFailureLeavesTheOthersRecordable checks that a failure is
// recorded rather than allowed to end the publication.
//
// Where the cause is common the user reads it several times, which costs
// nothing; where it is local to one repository, the others still land
// (ADR-073 evidence 3).
func TestOneRepositorysFailureLeavesTheOthersRecordable(t *testing.T) {
	task := publishableTask(t)
	if err := task.PlanPublication(plan(), origin); err != nil {
		t.Fatalf("planning the publication: %v", err)
	}

	const refused = "remote: You are not allowed to push code to protected branches"
	if err := task.RecordPublicationFailure(testPrimary, refused, origin.Add(time.Minute)); err != nil {
		t.Fatalf("recording the failure: %v", err)
	}
	opened := MergeRequest{Reference: "#4", URL: "https://forge.example.com/schema/pull/4"}
	if err := task.RecordPublished(testSecondary, opened, origin.Add(2*time.Minute)); err != nil {
		t.Fatalf("the repository after the failure could not be recorded: %v", err)
	}

	failed, ok := task.Publication.Repository(testPrimary)
	if !ok {
		t.Fatalf("%s is missing from the publication", testPrimary)
	}
	if failed.State != PublicationFailed || failed.Failure != refused {
		t.Errorf("the failure is recorded as %s %q", failed.State, failed.Failure)
	}
	if failed.Request != nil {
		t.Errorf("a repository that did not publish names a merge request: %+v", failed.Request)
	}
	if err := task.Validate(); err != nil {
		t.Errorf("a partly failed publication does not validate: %v", err)
	}
}

// TestRepublishingSkipsWhatAlreadyPublished checks that a second publication
// cannot forget or replace a merge request the first one opened.
//
// A repository that published is skipped as already published rather than as
// stale, which is what lets a staleness refusal keep its one meaning: that the
// draft describes a commit which is no longer current (ADR-073).
func TestRepublishingSkipsWhatAlreadyPublished(t *testing.T) {
	task := publishableTask(t)
	if err := task.PlanPublication(plan(), origin); err != nil {
		t.Fatalf("planning the publication: %v", err)
	}
	opened := MergeRequest{Reference: "!12", URL: "https://forge.example.com/core/-/merge_requests/12"}
	if err := task.RecordPublished(testPrimary, opened, origin.Add(time.Minute)); err != nil {
		t.Fatalf("recording the merge request: %v", err)
	}
	if err := task.RecordPublicationFailure(testSecondary, "the network is unreachable", origin.Add(2*time.Minute)); err != nil {
		t.Fatalf("recording the failure: %v", err)
	}

	// The user fixes the network and publishes again.
	if err := task.PlanPublication(plan(), origin.Add(time.Hour)); err != nil {
		t.Fatalf("re-planning the publication: %v", err)
	}

	kept, _ := task.Publication.Repository(testPrimary)
	if kept.State != PublicationPublished || kept.Request == nil || kept.Request.URL != opened.URL {
		t.Errorf("re-planning lost the merge request it had opened: %+v", kept)
	}
	retried, _ := task.Publication.Repository(testSecondary)
	if retried.State != PublicationPlanned {
		t.Errorf("the repository that failed came back as %s, want a retry", retried.State)
	}

	// And a second merge request for the same repository is refused rather than
	// written over the first, which nothing would then name.
	err := task.RecordPublished(testPrimary,
		MergeRequest{Reference: "!13", URL: "https://forge.example.com/core/-/merge_requests/13"},
		origin.Add(2*time.Hour))
	if !errors.Is(err, ErrInvariant) {
		t.Fatalf("a second merge request for one repository was accepted: %v", err)
	}
	if !strings.Contains(err.Error(), opened.URL) {
		t.Errorf("the refusal does not name what was already published: %v", err)
	}
}

// TestAPlanCannotForgetAMergeRequestItOpened checks the record's one hard
// obligation: everything Feat created on somebody else's server is named here.
func TestAPlanCannotForgetAMergeRequestItOpened(t *testing.T) {
	task := publishableTask(t)
	if err := task.PlanPublication(plan(), origin); err != nil {
		t.Fatalf("planning the publication: %v", err)
	}
	opened := MergeRequest{Reference: "!12", URL: "https://forge.example.com/core/-/merge_requests/12"}
	if err := task.RecordPublished(testPrimary, opened, origin.Add(time.Minute)); err != nil {
		t.Fatalf("recording the merge request: %v", err)
	}

	// The second publication covers the other repository alone — the first one
	// has nothing left to change.
	if err := task.PlanPublication(plan()[1:], origin.Add(time.Hour)); err != nil {
		t.Fatalf("re-planning the publication: %v", err)
	}

	kept, ok := task.Publication.Repository(testPrimary)
	if !ok {
		t.Fatalf("re-planning dropped %s, whose merge request is open", testPrimary)
	}
	if kept.Request == nil || kept.Request.URL != opened.URL {
		t.Errorf("the kept entry no longer names the merge request: %+v", kept)
	}
	if first := task.Publication.Repositories[0].RepositoryID; first != testSecondary {
		t.Errorf("the plan is applied starting with %s, want the repository this plan asked for", first)
	}
}

// TestAPlanIsRefusedForWhatCannotBePublished checks what a plan will not
// accept, because each of these would be a record of something that cannot
// exist.
func TestAPlanIsRefusedForWhatCannotBePublished(t *testing.T) {
	readOnly := func(entries []RepositoryPublication) []RepositoryPublication {
		return append(entries, RepositoryPublication{
			RepositoryID: testReadOnly,
			Forge:        ForgeGitLab,
			Remote:       "origin",
			BaseBranch:   "main",
			Commit:       testCommit,
		})
	}

	for name, entries := range map[string][]RepositoryPublication{
		"a repository the task does not bind": {{
			RepositoryID: RepositoryID("absent"),
			Forge:        ForgeGitLab,
			Remote:       "origin",
			BaseBranch:   "main",
			Commit:       testCommit,
		}},
		"a forge Feat does not publish to": {{
			RepositoryID: testPrimary,
			Forge:        ForgeKind("bitbucket"),
			Remote:       "origin",
			BaseBranch:   "main",
			Commit:       testCommit,
		}},
		"no branch to merge into": {{
			RepositoryID: testPrimary,
			Forge:        ForgeGitLab,
			Remote:       "origin",
			Commit:       testCommit,
		}},
		"an abbreviated commit": {{
			RepositoryID: testPrimary,
			Forge:        ForgeGitLab,
			Remote:       "origin",
			BaseBranch:   "main",
			Commit:       testCommit[:8],
		}},
		"one repository twice": {plan()[0], plan()[0]},
		"a result the plan invented": {{
			RepositoryID: testPrimary,
			Forge:        ForgeGitLab,
			Remote:       "origin",
			BaseBranch:   "main",
			Commit:       testCommit,
			State:        PublicationPublished,
			Request:      &MergeRequest{Reference: "!1", URL: "https://forge.example.com/core/-/merge_requests/1"},
		}},
		"a read-only repository": readOnly(nil),
	} {
		t.Run(name, func(t *testing.T) {
			task := publishableTask(t)

			if err := task.PlanPublication(entries, origin); err == nil {
				t.Fatal("the plan was accepted")
			}
			if task.Publication != nil {
				t.Errorf("a refused plan was recorded anyway: %+v", task.Publication)
			}
		})
	}
}

// TestAResultIsRefusedWithoutAPlanThatNamesIt keeps the record from naming a
// resource the plan never said would exist.
func TestAResultIsRefusedWithoutAPlanThatNamesIt(t *testing.T) {
	task := publishableTask(t)
	opened := MergeRequest{Reference: "!12", URL: "https://forge.example.com/core/-/merge_requests/12"}

	if err := task.RecordPublished(testPrimary, opened, origin); !errors.Is(err, ErrInvalid) {
		t.Errorf("a result was recorded against a task with no publication: %v", err)
	}

	if err := task.PlanPublication(plan()[:1], origin); err != nil {
		t.Fatalf("planning the publication: %v", err)
	}
	if err := task.RecordPublished(testSecondary, opened, origin); !errors.Is(err, ErrInvalid) {
		t.Errorf("a result was recorded for a repository the plan does not name: %v", err)
	}
	if err := task.RecordPublicationFailure(testPrimary, "", origin); !errors.Is(err, ErrInvalid) {
		t.Errorf("a failure with no reason was recorded: %v", err)
	}
	if err := task.RecordPublished(testPrimary, MergeRequest{Reference: "!12"}, origin); !errors.Is(err, ErrInvalid) {
		t.Errorf("a merge request with no URL was recorded: %v", err)
	}
}

// TestAnInconsistentPublicationDoesNotValidate covers the records storage must
// refuse on the way in and on the way out, since a document edited by hand or
// written by another build reaches the domain the same way.
func TestAnInconsistentPublicationDoesNotValidate(t *testing.T) {
	for name, breakIt := range map[string]func(publication *Publication){
		"published with no merge request": func(p *Publication) {
			p.Repositories[0].State = PublicationPublished
			p.Repositories[0].AttemptedAt = origin
		},
		"published with a merge request nothing can name": func(p *Publication) {
			p.Repositories[0].State = PublicationPublished
			p.Repositories[0].Request = &MergeRequest{URL: "https://forge.example.com/1"}
			p.Repositories[0].AttemptedAt = origin
		},
		"published with a merge request nothing can read": func(p *Publication) {
			p.Repositories[0].State = PublicationPublished
			p.Repositories[0].Request = &MergeRequest{Reference: "!1"}
			p.Repositories[0].AttemptedAt = origin
		},
		"failed with no reason": func(p *Publication) {
			p.Repositories[0].State = PublicationFailed
			p.Repositories[0].AttemptedAt = origin
		},
		"planned with a result": func(p *Publication) {
			p.Repositories[0].Request = &MergeRequest{Reference: "!1", URL: "https://forge.example.com/1"}
		},
		"attempted with no time": func(p *Publication) {
			p.Repositories[0].State = PublicationFailed
			p.Repositories[0].Failure = "the network is unreachable"
		},
		"a state that is not one": func(p *Publication) {
			p.Repositories[0].State = PublicationState("sent")
		},
		"no plan time": func(p *Publication) { p.PlannedAt = time.Time{} },
		"a repository the task does not bind": func(p *Publication) {
			p.Repositories[0].RepositoryID = RepositoryID("absent")
		},
	} {
		t.Run(name, func(t *testing.T) {
			task := publishableTask(t)
			if err := task.PlanPublication(plan(), origin); err != nil {
				t.Fatalf("planning the publication: %v", err)
			}
			breakIt(task.Publication)

			if err := task.Validate(); err == nil {
				t.Errorf("%s validated", name)
			}
		})
	}
}

// TestOnlyDocumentedForgesArePublishedTo pins the vocabulary configuration is
// checked against, so that a project cannot declare a forge no adapter exists
// for.
func TestOnlyDocumentedForgesArePublishedTo(t *testing.T) {
	for _, kind := range []ForgeKind{ForgeGitHub, ForgeGitLab} {
		if !kind.Valid() {
			t.Errorf("%s is not accepted as a forge", kind)
		}
	}
	for _, kind := range []ForgeKind{"", "bitbucket", "GitLab"} {
		if kind.Valid() {
			t.Errorf("%q is accepted as a forge", kind)
		}
	}
}
