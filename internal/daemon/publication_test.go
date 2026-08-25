package daemon

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/control"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/forge"
)

// publishFixture is a five-repository project whose repositories all publish to
// the same forge.
//
// Five, because ADR-073's question is what happens when the third of five fails:
// with two there is no "the rest still land", and with three there is no
// repository after the failure and before the end.
//
// The names sort into the order the task binds them, so that "the third" is the
// third both in the fixture and in the record.
const publishFixture = `version: 1

project:
  id: app
  name: Example Application
  primary_repository: alpha

repositories:
  alpha:
    host_path: ~/repos/app/alpha
    default_access: read_write
    forge:
      kind: gitlab
  bravo:
    host_path: ~/repos/app/bravo
    default_access: read_write
    forge:
      kind: gitlab
  charlie:
    host_path: ~/repos/app/charlie
    default_access: read_write
    forge:
      kind: gitlab
  delta:
    host_path: ~/repos/app/delta
    default_access: read_write
    forge:
      kind: gitlab
  echo:
    host_path: ~/repos/app/echo
    default_access: read_write
    forge:
      kind: gitlab

git:
  base_policy: remote
  branch_template: "feat/{task_key}-{slug}"

agent:
  execution:
    mode: host

review:
  editor:
    command: ["nvim", "--clean", "{repository_path}"]
`

// head is the commit every task worktree has checked out in these tests, and
// the commit the agent's drafts describe.
const head = "9f8e7d6c5b4a39281706f5e4d3c2b1a09f8e7d6c"

// publicationRepositories are the fixture's repositories, in the order the task
// binds them.
var publicationRepositories = []string{"alpha", "bravo", "charlie", "delta", "echo"}

// fakeForge is a forge that records what it was asked to open.
//
// It stands where glab would, so that the sequencing above it can be checked
// without an account on somebody's GitLab — and so that a refusal from one
// repository can be arranged, which is the case the whole ordering exists for.
type fakeForge struct {
	mu sync.Mutex
	// opened records every request, in the order they were made.
	opened []forge.Request
	// refuse fails the repository of that name, by the worktree the request
	// runs in.
	refuse map[string]error
	// onOpen runs before each request answers, which is where a test can look
	// at what the record already says.
	onOpen func(repository string)
}

func newFakeForge() *fakeForge {
	return &fakeForge{refuse: make(map[string]error)}
}

func (f *fakeForge) Kind() domain.ForgeKind { return domain.ForgeGitLab }

func (f *fakeForge) Open(_ context.Context, request forge.Request) (domain.MergeRequest, error) {
	repository := filepath.Base(request.Directory)

	f.mu.Lock()
	hold := f.onOpen
	failure := f.refuse[repository]
	f.opened = append(f.opened, request)
	f.mu.Unlock()

	if hold != nil {
		hold(repository)
	}
	if failure != nil {
		return domain.MergeRequest{}, failure
	}
	return domain.MergeRequest{
		Reference: "!" + repository,
		URL:       "https://gitlab.example.com/app/" + repository + "/-/merge_requests/1",
	}, nil
}

// requests returns the repositories a publication opened, in order.
func (f *fakeForge) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	names := make([]string, 0, len(f.opened))
	for _, request := range f.opened {
		names = append(names, filepath.Base(request.Directory))
	}
	return names
}

// publication is a launched five-repository task with a fake forge behind it.
type publication struct {
	*session
	forge *fakeForge
}

// launchPublishing arranges the fixture, launches the task, and gives its five
// worktrees a commit to publish.
func launchPublishing(t *testing.T) *publication {
	t.Helper()

	fake := newFakeGit()
	fake.head = head
	// One commit beyond the recorded base, which is what makes a repository
	// worth publishing.
	fake.ahead = 1

	forges := newFakeForge()
	live := launchWith(t, publishFixture, installed(), false, func(options *Options) {
		options.Git = fake
		options.Forges = map[domain.ForgeKind]forge.Adapter{domain.ForgeGitLab: forges}
	})
	live.fake = fake
	return &publication{session: live, forge: forges}
}

// draft writes the publication draft the agent's helper would write.
func (p *publication) draft(t *testing.T, commit string, repositories ...string) {
	t.Helper()

	entries := make([]string, 0, len(repositories))
	for _, repository := range repositories {
		entries = append(entries, fmt.Sprintf(
			`{"repository":%q,"title":"Add a rate limit to %s","body":"It is per token.","commit":%q}`,
			repository, repository, commit))
	}
	p.emit(t, control.TypePublicationDraft, `{"repositories":[`+strings.Join(entries, ",")+`]}`)
}

// approve renders the request the user's approval sends back.
func (p *publication) approve(t *testing.T, repositories ...string) api.PublishRequest {
	t.Helper()

	plan, err := p.service.PlanPublication(context.Background(), p.ref.Task)
	if err != nil {
		t.Fatalf("planning the publication: %v", err)
	}

	var approved []api.ApprovedPublication
	for _, draft := range plan.Drafts {
		if len(repositories) > 0 && !contains(repositories, draft.RepositoryID) {
			continue
		}
		approved = append(approved, api.ApprovedPublication{
			RepositoryID: draft.RepositoryID,
			Title:        draft.Title,
			Body:         draft.Body,
			Commit:       draft.Commit,
		})
	}
	return api.PublishRequest{Repositories: approved}
}

// publish approves what the plan offers for the named repositories, or for all
// of them, and applies it.
func (p *publication) publish(t *testing.T, repositories ...string) *domain.Task {
	t.Helper()

	result, err := p.service.ApplyPublication(context.Background(), p.ref.Task, p.approve(t, repositories...))
	if err != nil {
		t.Fatalf("publishing: %v", err)
	}
	return result.Task
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// states returns the recorded state of each repository, in plan order.
func states(task *domain.Task) map[string]domain.PublicationState {
	found := make(map[string]domain.PublicationState)
	if task.Publication == nil {
		return found
	}
	for _, entry := range task.Publication.Repositories {
		found[entry.RepositoryID.String()] = entry.State
	}
	return found
}

// TestAPublicationRecordsItsWholePlanBeforeItAttemptsAnything is ADR-073's
// ordering, and it is the reason an interruption is recoverable.
//
// The record is written first and the repositories are applied afterwards, so
// that no merge request can exist that the record cannot name. The assertion is
// made from inside the first request: when the forge is asked to open one, the
// record on disk must already name every repository this publication will
// attempt.
func TestAPublicationRecordsItsWholePlanBeforeItAttemptsAnything(t *testing.T) {
	live := launchPublishing(t)
	live.draft(t, head, publicationRepositories...)

	var unplanned []string
	live.forge.onOpen = func(string) {
		task, err := live.service.store.Tasks().Load(context.Background(), live.ref)
		if err != nil {
			t.Errorf("reading the task while a merge request was being opened: %v", err)
			return
		}
		if task.Publication == nil {
			unplanned = append(unplanned, "the record has no publication at all")
			return
		}
		for _, name := range publicationRepositories {
			if _, found := task.Publication.Repository(domain.RepositoryID(name)); !found {
				unplanned = append(unplanned, name)
			}
		}
	}

	task := live.publish(t)
	if len(unplanned) > 0 {
		t.Errorf("a merge request was opened before the record named %v", unplanned)
	}
	if task.Publication == nil {
		t.Fatal("nothing was recorded")
	}
	if got := len(task.Publication.Published()); got != len(publicationRepositories) {
		t.Errorf("%d repositories published, want %d", got, len(publicationRepositories))
	}
}

// TestAFailureAtTheThirdOfFiveLeavesTheOthersPublished is ADR-073's central
// promise.
//
// A failure on one repository does not abort the others. Where the cause is
// common the user reads it several times, which costs nothing; where it is local
// to one repository the rest land and the user has one thing to fix rather than
// an unknown number still unattempted. Nothing is rolled back: the two that
// published stay published.
func TestAFailureAtTheThirdOfFiveLeavesTheOthersPublished(t *testing.T) {
	live := launchPublishing(t)
	live.draft(t, head, publicationRepositories...)
	live.forge.refuse["charlie"] = &forge.Error{
		Forge: domain.ForgeGitLab, Program: "glab", ExitCode: 1,
		Detail: "GitLab: You are not allowed to push code to protected branches on this project.",
	}

	task := live.publish(t)

	recorded := states(task)
	for _, name := range []string{"alpha", "bravo", "delta", "echo"} {
		if recorded[name] != domain.PublicationPublished {
			t.Errorf("repository %s is %q, want it published", name, recorded[name])
		}
	}
	if recorded["charlie"] != domain.PublicationFailed {
		t.Errorf("repository charlie is %q, want it failed", recorded["charlie"])
	}

	entry, _ := task.Publication.Repository("charlie")
	if !strings.Contains(entry.Failure, "protected branches") {
		t.Errorf("the failure is %q, want the words the forge used", entry.Failure)
	}
	if entry.Request != nil {
		t.Errorf("a failed repository records a merge request: %+v", entry.Request)
	}

	// The two after it were still attempted, which is the whole point: stopping
	// early would turn one round trip into as many as there are repositories.
	if got := live.forge.requests(); len(got) != 5 {
		t.Errorf("the forge was asked %v, want every repository attempted", got)
	}
	// And the push happened for every one of them, including the one whose
	// merge request was refused: the branch is on the remote and the request is
	// not, which is a state the failure names.
	if got := len(live.fake.pushes()); got != 5 {
		t.Errorf("%d branches were pushed, want 5", got)
	}
}

// TestAnInterruptedPublicationIsRecoverableFromItsRecord is what the plan is
// recorded first for.
//
// The publication stops after two repositories, the way a daemon that was killed
// would. What is left is not deduced from the forges: the record names the three
// it had not attempted, a restarted daemon reads the same record off disk, and
// publishing again finishes exactly those.
func TestAnInterruptedPublicationIsRecoverableFromItsRecord(t *testing.T) {
	live := launchPublishing(t)
	live.draft(t, head, publicationRepositories...)

	// Interrupted: the third repository's request never answers, and the
	// process ends. What survives is what was written down.
	interrupted := errors.New("the daemon stopped")
	live.forge.refuse["charlie"] = interrupted
	live.forge.onOpen = func(repository string) {
		if repository == "charlie" {
			panic("interrupted")
		}
	}

	func() {
		defer func() {
			if recovered := recover(); recovered != "interrupted" {
				t.Errorf("the publication did not stop where the test stopped it: %v", recovered)
			}
		}()
		_, _ = live.service.ApplyPublication(context.Background(), live.ref.Task, live.approve(t))
	}()

	// A second daemon over the same state directory, which is what a restart is.
	restarted := restart(t, live.session)
	restarted.forges = map[domain.ForgeKind]forge.Adapter{domain.ForgeGitLab: live.forge}
	live.forge.onOpen = nil
	delete(live.forge.refuse, "charlie")

	task, err := restarted.Task(context.Background(), live.ref.Task)
	if err != nil {
		t.Fatalf("loading the task after a restart: %v", err)
	}
	if task.Publication == nil {
		t.Fatal("the interrupted publication left no record")
	}

	pending := task.Publication.Pending()
	if len(pending) != 3 {
		t.Fatalf("the record names %d repositories as unattempted, want three: %+v", len(pending), pending)
	}
	for i, name := range []string{"charlie", "delta", "echo"} {
		if pending[i].RepositoryID.String() != name {
			t.Errorf("the record's %d unattempted repository is %s, want %s",
				i, pending[i].RepositoryID, name)
		}
	}
	if got := len(task.Publication.Published()); got != 2 {
		t.Errorf("%d repositories are recorded as published, want the two that finished", got)
	}

	// Publishing again finishes what was left. The two that published are kept
	// and skipped; nothing opens a second merge request for them.
	before := len(live.forge.requests())
	plan, err := restarted.PlanPublication(context.Background(), live.ref.Task)
	if err != nil {
		t.Fatalf("planning the publication again: %v", err)
	}
	var approved []api.ApprovedPublication
	for _, draft := range plan.Drafts {
		approved = append(approved, api.ApprovedPublication{
			RepositoryID: draft.RepositoryID, Title: draft.Title, Body: draft.Body, Commit: draft.Commit,
		})
	}
	result, err := restarted.ApplyPublication(context.Background(), live.ref.Task,
		api.PublishRequest{Repositories: approved})
	if err != nil {
		t.Fatalf("publishing again: %v", err)
	}

	if got := len(result.Task.Publication.Published()); got != 5 {
		t.Errorf("%d repositories published after the second run, want all five", got)
	}
	attempted := live.forge.requests()[before:]
	if len(attempted) != 3 {
		t.Errorf("the second run asked the forge for %v, want only what was unattempted", attempted)
	}
}

// TestRepublishingSkipsWhatAlreadyPublishedAndNeverCallsItStale keeps ADR-073's
// two reasons apart.
//
// A refusal says the agent's draft describes a commit that is no longer current.
// It must never say merely that this ran before, because a user who reads
// "stale" goes looking for work that moved, and a repository that already
// published has nothing wrong with it at all.
func TestRepublishingSkipsWhatAlreadyPublishedAndNeverCallsItStale(t *testing.T) {
	live := launchPublishing(t)
	live.draft(t, head, publicationRepositories...)
	live.publish(t)

	before := len(live.forge.requests())

	// The branch has moved on since, which would make a fresh publication of
	// these repositories stale. It does not make an already published one stale:
	// it is not attempted at all.
	live.fake.head = "0011223344556677889900112233445566778899"

	result, err := live.service.ApplyPublication(context.Background(), live.ref.Task,
		api.PublishRequest{Repositories: []api.ApprovedPublication{{
			RepositoryID: "alpha", Title: "Add a rate limit to alpha", Commit: head,
		}}})
	if err != nil {
		t.Fatalf("publishing again: %v", err)
	}
	if got := live.forge.requests(); len(got) != before {
		t.Errorf("a repository that already published was attempted again: %v", got)
	}

	entry, found := result.Task.Publication.Repository("alpha")
	if !found || entry.State != domain.PublicationPublished {
		t.Fatalf("the record of repository alpha is %+v", entry)
	}
	if entry.Request == nil || entry.Request.URL == "" {
		t.Error("re-publishing lost the merge request that was already open")
	}

	joined := strings.Join(result.Notes, " ")
	if !strings.Contains(joined, "already published") {
		t.Errorf("the notes do not say it was already published: %v", result.Notes)
	}
	if strings.Contains(strings.ToLower(joined), "stale") {
		t.Errorf("a repository that already published was reported as stale: %v", result.Notes)
	}
}

// TestADraftDescribingAnotherCommitIsRefusedBeforeAnythingIsSent is ADR-070's
// staleness refusal.
//
// The draft is the agent's account of what it did, and an account written before
// the work finished describes something else. It is reported and never resolved,
// for the reason a confirmation fingerprint is: re-composing it silently would
// publish words nobody read.
func TestADraftDescribingAnotherCommitIsRefusedBeforeAnythingIsSent(t *testing.T) {
	live := launchPublishing(t)
	// The draft describes the commit that was current when the agent wrote it.
	live.draft(t, head, publicationRepositories...)
	approved := live.approve(t)

	// And then the agent committed again.
	live.fake.head = "0011223344556677889900112233445566778899"

	_, err := live.service.ApplyPublication(context.Background(), live.ref.Task, approved)
	if err == nil {
		t.Fatal("a stale draft was published")
	}
	if !errors.Is(err, api.ErrInvalid) {
		t.Errorf("the refusal is %v, want an invalid request", err)
	}
	if !strings.Contains(err.Error(), "Nothing was pushed and nothing was opened") {
		t.Errorf("the refusal does not say what was not done: %q", err)
	}

	if got := live.forge.requests(); len(got) != 0 {
		t.Errorf("a refused publication opened %v", got)
	}
	if got := live.fake.pushes(); len(got) != 0 {
		t.Errorf("a refused publication pushed %v", got)
	}
	task := live.task(t)
	if task.Publication != nil {
		t.Errorf("a refused publication recorded a plan: %+v", task.Publication)
	}
}

// TestThePlanShowsAStaleDraftBeforeTheUserApproves is the other half.
//
// A refusal at the moment of approval is right and late. The plan is what the
// user reads, so it names the repository, the commit the draft describes, and
// the commit the branch is at.
func TestThePlanShowsAStaleDraftBeforeTheUserApproves(t *testing.T) {
	live := launchPublishing(t)
	live.draft(t, "1111111111111111111111111111111111111111", "alpha")

	plan, err := live.service.PlanPublication(context.Background(), live.ref.Task)
	if err != nil {
		t.Fatalf("planning the publication: %v", err)
	}

	var found bool
	for _, draft := range plan.Drafts {
		if draft.RepositoryID != "alpha" {
			continue
		}
		found = true
		if !draft.Stale {
			t.Error("a draft describing another commit is not marked stale")
		}
		if draft.DraftCommit != "1111111111111111111111111111111111111111" {
			t.Errorf("the plan does not carry the commit the draft describes: %q", draft.DraftCommit)
		}
		if draft.Commit != head {
			t.Errorf("the plan does not carry the commit the branch is at: %q", draft.Commit)
		}
	}
	if !found {
		t.Fatal("the plan has no row for repository alpha")
	}
	if !strings.Contains(strings.Join(plan.Notes, " "), "refused as") {
		t.Errorf("the notes do not say what will happen: %v", plan.Notes)
	}
}

// TestWhatIsSentIsWhatTheUserApproved is the control ADR-070 rests on.
//
// The agent's words go through a person before they reach a forge. What the
// daemon composes the request from is the approved text, not the agent's message
// — otherwise reading one document and sending another would make the approval a
// formality.
func TestWhatIsSentIsWhatTheUserApproved(t *testing.T) {
	live := launchPublishing(t)
	live.draft(t, head, "alpha")

	edited := api.PublishRequest{Repositories: []api.ApprovedPublication{{
		RepositoryID: "alpha",
		Title:        "Rate-limit the public API by token",
		Body:         "The agent's description, rewritten by the person sending it.",
		Commit:       head,
	}}}
	if _, err := live.service.ApplyPublication(context.Background(), live.ref.Task, edited); err != nil {
		t.Fatalf("publishing: %v", err)
	}

	if len(live.forge.opened) != 1 {
		t.Fatalf("the forge was asked %d times, want once", len(live.forge.opened))
	}
	request := live.forge.opened[0]
	if request.Title != edited.Repositories[0].Title {
		t.Errorf("title = %q, want the words the user approved", request.Title)
	}
	if request.Body != edited.Repositories[0].Body {
		t.Errorf("body = %q, want the words the user approved", request.Body)
	}
	if request.TargetBranch != "main" {
		t.Errorf("target branch = %q, want what Feat knows rather than what the agent said",
			request.TargetBranch)
	}
	if request.SourceBranch == "" || !strings.HasPrefix(request.SourceBranch, "feat/") {
		t.Errorf("source branch = %q, want the task's own branch", request.SourceBranch)
	}
}

// TestTheAgentsDraftIsReadBackFromItsOwnMessage checks where the words come
// from.
//
// The draft is written by the agent when it requests review and read again when
// the user asks to publish, which may be after a restart. It lives in the
// control workspace, which is the account of what the agent sent, so the
// read-back is a question about that account rather than a copy Feat kept.
func TestTheAgentsDraftIsReadBackFromItsOwnMessage(t *testing.T) {
	live := launchPublishing(t)
	live.draft(t, head, "alpha", "bravo")

	restarted := restart(t, live.session)
	plan, err := restarted.PlanPublication(context.Background(), live.ref.Task)
	if err != nil {
		t.Fatalf("planning the publication after a restart: %v", err)
	}

	titles := make(map[string]string, len(plan.Drafts))
	for _, draft := range plan.Drafts {
		titles[draft.RepositoryID] = draft.Title
	}
	if titles["alpha"] != "Add a rate limit to alpha" {
		t.Errorf("the draft for alpha reads %q", titles["alpha"])
	}
	if titles["bravo"] != "Add a rate limit to bravo" {
		t.Errorf("the draft for bravo reads %q", titles["bravo"])
	}
	// A repository the agent never drafted still appears, with the words left to
	// the user: publishing work the agent did not describe is allowed.
	if _, found := titles["charlie"]; !found {
		t.Error("a repository the agent wrote no draft for is missing from the plan")
	}
	if titles["charlie"] != "" {
		t.Errorf("a repository the agent wrote no draft for has the title %q", titles["charlie"])
	}
	if !strings.Contains(strings.Join(plan.Notes, " "), "wrote no draft for charlie") {
		t.Errorf("the notes do not say which repositories have no draft: %v", plan.Notes)
	}
}

// TestAPublicationDraftChangesNoState checks that a draft is an account.
//
// It asks for nothing, so it moves no workflow state, sets no attention, and
// reaches no forge. What it does is appear in the task's history, and the
// history carries the repositories rather than the prose.
func TestAPublicationDraftChangesNoState(t *testing.T) {
	live := launchPublishing(t)
	before := live.task(t)

	live.draft(t, head, "alpha")

	after := live.task(t)
	if after.Workflow != before.Workflow {
		t.Errorf("a publication draft moved the workflow from %s to %s", before.Workflow, after.Workflow)
	}
	if after.Attention != before.Attention {
		t.Errorf("a publication draft moved attention from %s to %s", before.Attention, after.Attention)
	}
	if after.Publication != nil {
		t.Errorf("a publication draft recorded a publication: %+v", after.Publication)
	}
	if got := live.forge.requests(); len(got) != 0 {
		t.Errorf("a publication draft reached a forge: %v", got)
	}

	var said string
	for _, event := range history(t, live.session) {
		if event.Type == domain.EventPublicationChanged {
			said = event.Detail
		}
	}
	if said == "" {
		t.Fatal("the task's history says nothing about the draft the agent wrote")
	}
	if !strings.Contains(said, "alpha") {
		t.Errorf("the history entry %q does not name the repository", said)
	}
	if strings.Contains(said, "It is per token") {
		t.Errorf("the agent's prose reached the task's history: %q", said)
	}
}

// TestAPushRunsWithoutTheAgentsHooks is ADR-070's reason for disabling them,
// checked where the publication runs it.
func TestAPushRunsWithoutTheAgentsHooks(t *testing.T) {
	live := launchPublishing(t)
	live.draft(t, head, "alpha")
	live.publish(t, "alpha")

	environments := live.fake.pushEnvironments()
	if len(environments) != 1 {
		t.Fatalf("%d pushes ran, want one", len(environments))
	}
	if !strings.Contains(strings.Join(environments[0], " "), "core.hooksPath") {
		t.Errorf("the push ran with the agent's hooks enabled: %v", environments[0])
	}
}

// TestARepositoryWithNoForgeIsANoteRatherThanAFailure keeps one repository's
// configuration from stopping the rest.
func TestARepositoryWithNoForgeIsANoteRatherThanAFailure(t *testing.T) {
	fixture := strings.Replace(publishFixture, `  bravo:
    host_path: ~/repos/app/bravo
    default_access: read_write
    forge:
      kind: gitlab
`, `  bravo:
    host_path: ~/repos/app/bravo
    default_access: read_write
`, 1)

	fake := newFakeGit()
	fake.head = head
	fake.ahead = 1
	forges := newFakeForge()
	live := launchWith(t, fixture, installed(), false, func(options *Options) {
		options.Git = fake
		options.Forges = map[domain.ForgeKind]forge.Adapter{domain.ForgeGitLab: forges}
	})
	live.fake = fake

	plan, err := live.service.PlanPublication(context.Background(), live.ref.Task)
	if err != nil {
		t.Fatalf("planning the publication: %v", err)
	}
	for _, draft := range plan.Drafts {
		if draft.RepositoryID == "bravo" {
			t.Error("a repository that configures no forge was offered for publication")
		}
	}
	if len(plan.Drafts) != 4 {
		t.Errorf("%d repositories were offered, want the four that configure a forge", len(plan.Drafts))
	}
	if !strings.Contains(strings.Join(plan.Notes, " "), "repositories.bravo.forge.kind") {
		t.Errorf("the notes do not say what to set: %v", plan.Notes)
	}

	// And publishing it anyway is refused by name rather than attempted.
	_, err = live.service.ApplyPublication(context.Background(), live.ref.Task,
		api.PublishRequest{Repositories: []api.ApprovedPublication{{
			RepositoryID: "bravo", Title: "t", Commit: head,
		}}})
	if err == nil || !strings.Contains(err.Error(), "configures no forge") {
		t.Errorf("publishing a repository with no forge answered %v", err)
	}
}

// TestTheEditorKeepsItsFlagsAndIsGivenTheDraft checks what the client is handed.
//
// The configured editor command names an editor and the thing it opens. For a
// publication the thing it opens is the draft, so the flags are kept and the
// argument that named a repository is not: `nvim --clean` has to stay
// `nvim --clean`, or the editor behaves differently from everywhere else.
func TestTheEditorKeepsItsFlagsAndIsGivenTheDraft(t *testing.T) {
	live := launchPublishing(t)

	plan, err := live.service.PlanPublication(context.Background(), live.ref.Task)
	if err != nil {
		t.Fatalf("planning the publication: %v", err)
	}
	if plan.Editor.Program != "nvim" {
		t.Errorf("editor program = %q", plan.Editor.Program)
	}
	if len(plan.Editor.Arguments) != 1 || plan.Editor.Arguments[0] != "--clean" {
		t.Errorf("editor arguments = %v, want the configured flags and no repository", plan.Editor.Arguments)
	}
}

// TestPublishingADraftIsRefused keeps a task with nothing created out of a
// publication.
func TestPublishingADraftIsRefused(t *testing.T) {
	arranged := arrangeTaskWith(t, newFakeGit(), publishFixture)

	_, err := arranged.service.PlanPublication(context.Background(), arranged.ref.Task)
	if err == nil || !strings.Contains(err.Error(), "still a draft") {
		t.Errorf("planning the publication of a draft answered %v", err)
	}
}

// TestAPublicationWithNothingToPublishSaysSo checks the empty request.
func TestAPublicationWithNothingToPublishSaysSo(t *testing.T) {
	live := launchPublishing(t)

	_, err := live.service.ApplyPublication(context.Background(), live.ref.Task, api.PublishRequest{})
	if err == nil || !strings.Contains(err.Error(), "names none") {
		t.Errorf("publishing nothing answered %v", err)
	}
}

// TestARepositoryNamedTwiceIsRefused keeps one publication from opening two
// merge requests for one repository.
func TestARepositoryNamedTwiceIsRefused(t *testing.T) {
	live := launchPublishing(t)

	_, err := live.service.ApplyPublication(context.Background(), live.ref.Task,
		api.PublishRequest{Repositories: []api.ApprovedPublication{
			{RepositoryID: "alpha", Title: "t", Commit: head},
			{RepositoryID: "alpha", Title: "u", Commit: head},
		}})
	if err == nil || !strings.Contains(err.Error(), "named twice") {
		t.Errorf("publishing one repository twice answered %v", err)
	}
	if got := live.forge.requests(); len(got) != 0 {
		t.Errorf("a refused publication opened %v", got)
	}
}

// TestTheDraftNamesTheTicketTheTaskCameFrom is the one thing Feat adds to the
// agent's prose.
//
// It is added to the draft rather than to the request, so the user reads it and
// can delete it: what is sent is what was displayed. The agent is not asked for
// it, because the brief it was given is the composed brief rather than the
// ticket it came from (ADR-070).
func TestTheDraftNamesTheTicketTheTaskCameFrom(t *testing.T) {
	live := launchPublishing(t)
	live.draft(t, head, "alpha")

	task := live.task(t)
	task.Source = domain.TaskSource{
		Kind: domain.SourceTicket,
		Ticket: &domain.ExternalTaskReference{
			Provider:  "tracker",
			Reference: "PROJ-482",
			URL:       "https://tracker.example.invalid/stories/482",
			Snapshot: domain.TicketSnapshot{
				Title:   "Add a rate limit",
				Body:    "The free tier is being scraped.\n",
				State:   "in progress",
				TakenAt: reconcileTime,
			},
		},
	}
	if err := live.service.store.Tasks().Save(context.Background(), task); err != nil {
		t.Fatalf("recording the ticket the task came from: %v", err)
	}

	plan, err := live.service.PlanPublication(context.Background(), live.ref.Task)
	if err != nil {
		t.Fatalf("planning the publication: %v", err)
	}
	for _, draft := range plan.Drafts {
		if !strings.Contains(draft.Body, "https://tracker.example.invalid/stories/482") {
			t.Errorf("the draft for %s does not name the ticket: %q", draft.RepositoryID, draft.Body)
		}
		if !strings.Contains(draft.Body, live.task(t).Key().String()) {
			t.Errorf("the draft for %s does not name the task: %q", draft.RepositoryID, draft.Body)
		}
	}
	// The agent's own words are still there, and the ticket is after them.
	for _, draft := range plan.Drafts {
		if draft.RepositoryID != "alpha" {
			continue
		}
		if !strings.HasPrefix(draft.Body, "It is per token.") {
			t.Errorf("the agent's description was replaced rather than added to: %q", draft.Body)
		}
	}
}

// TestATaskWithNoTicketGetsNoTicketLine keeps a task from a prompt clean.
func TestATaskWithNoTicketGetsNoTicketLine(t *testing.T) {
	live := launchPublishing(t)
	live.draft(t, head, "alpha")

	plan, err := live.service.PlanPublication(context.Background(), live.ref.Task)
	if err != nil {
		t.Fatalf("planning the publication: %v", err)
	}
	for _, draft := range plan.Drafts {
		if strings.Contains(draft.Body, "Task ") {
			t.Errorf("a task from a prompt has a ticket line: %q", draft.Body)
		}
	}
}

// TestRepublishingWithNoWordsForWhatAlreadyPublishedIsFine keeps a client from
// having to invent a title for something that is already on the forge.
//
// The words that were sent are on the forge, and the record keeps the entry as
// it was recorded. A repository that already published is not asked for a title,
// not asked whether its words are current, and not attempted.
func TestRepublishingWithNoWordsForWhatAlreadyPublishedIsFine(t *testing.T) {
	live := launchPublishing(t)
	live.draft(t, head, "alpha")
	live.publish(t, "alpha")

	before := len(live.forge.requests())
	live.draft(t, head, "bravo")

	result, err := live.service.ApplyPublication(context.Background(), live.ref.Task,
		api.PublishRequest{Repositories: []api.ApprovedPublication{
			// No title and no commit: what was sent for it is on the forge.
			{RepositoryID: "alpha"},
			{RepositoryID: "bravo", Title: "Add a rate limit to bravo", Commit: head},
		}})
	if err != nil {
		t.Fatalf("publishing again: %v", err)
	}

	if got := live.forge.requests(); len(got) != before+1 {
		t.Errorf("the forge was asked %v, want only the repository that had not published", got)
	}
	if states(result.Task)["bravo"] != domain.PublicationPublished {
		t.Errorf("bravo is %q, want it published", states(result.Task)["bravo"])
	}
	entry, _ := result.Task.Publication.Repository("alpha")
	if entry.Request == nil || entry.State != domain.PublicationPublished {
		t.Errorf("the record of alpha is %+v, want the merge request it already had", entry)
	}
}

// TestTheForgesThisBuildPublishesToAreTheOnesItDeclares holds the registry and
// the declaration together.
//
// `feat doctor` reads forge.Built to tell a user that a configured forge is one
// this build has no adapter for, and the daemon publishes through the registry
// it composes here. Two lists of one fact can disagree, and the way they would
// disagree is the worst one available: doctor reporting a project as publishable
// that a publication then refuses, or the reverse (ADR-074).
func TestTheForgesThisBuildPublishesToAreTheOnesItDeclares(t *testing.T) {
	registry := forges(Options{})

	for _, kind := range forge.Built {
		if _, built := registry[kind]; !built {
			t.Errorf("forge.Built names %s and the daemon publishes through no adapter for it", kind)
		}
	}
	for kind := range registry {
		if !forge.Available(kind) {
			t.Errorf("the daemon publishes to %s and forge.Built does not name it, "+
				"so `feat doctor` will tell a user it is not built", kind)
		}
	}
	if len(registry) != len(forge.Built) {
		t.Errorf("the daemon has %d adapters and forge.Built names %d", len(registry), len(forge.Built))
	}
}
