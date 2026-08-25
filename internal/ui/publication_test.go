package ui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ma8el/feat/internal/api"
)

// publishable is a dashboard on the publication screen of a live task.
func publishable(t *testing.T, backend *fakeBackend) Model {
	t.Helper()

	backend.publicationStatus = api.PublicationStatus{
		Task: api.Task{ID: liveTask().ID, Key: liveTask().Key, Title: liveTask().Title},
		Drafts: []api.PublicationDraft{{
			RepositoryID: "core", Forge: "gitlab", Remote: "origin",
			Branch: "feat/7f3a1c2e-add-a-scheduled-export-job", BaseBranch: "main",
			Commit: "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d",
			Title:  "Export the daily report",
			Body:   "It writes to the configured bucket.",
		}},
	}
	backend.approved = []api.ApprovedPublication{{
		RepositoryID: "core",
		Title:        "Export the daily report",
		Body:         "It writes to the configured bucket.",
		Commit:       "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d",
	}}

	model := dashboard(backend, liveTask())
	model.selected = liveTask().ID
	model.screen = screenTask
	return press(t, model, "P")
}

// TestOpeningPublicationSendsNothing is why it is safe to reach with one key.
//
// What publishing would do is a question. Only what the user approves is ever
// acted on, and approving is three key presses further on.
func TestOpeningPublicationSendsNothing(t *testing.T) {
	backend := newFakeBackend()
	model := publishable(t, backend)

	if model.screen != screenPublication {
		t.Fatalf("P left the dashboard on screen %v", model.screen)
	}
	if len(backend.publicationCalls) != 1 {
		t.Errorf("opening the screen planned %d times, want once", len(backend.publicationCalls))
	}
	if len(backend.publicationRequests) != 0 {
		t.Errorf("opening the screen published %v", backend.publicationRequests)
	}
	if backend.publicationEdits != 0 {
		t.Errorf("opening the screen opened the editor %d times", backend.publicationEdits)
	}

	body := model.publicationBody()
	for _, want := range []string{"core", "gitlab", "main", "Export the daily report"} {
		if !strings.Contains(body, want) {
			t.Errorf("the screen does not show %q:\n%s", want, body)
		}
	}
}

// TestNothingIsSentBeforeTheDraftHasBeenRead is the control ADR-070 rests on.
//
// The agent's words can carry anything it read. A person reading them before
// they are sent is the only control there is, and a title in a table is not
// reading them: what is sent is the document the user had open.
func TestNothingIsSentBeforeTheDraftHasBeenRead(t *testing.T) {
	backend := newFakeBackend()
	model := publishable(t, backend)

	confirming := press(t, model, "enter")
	if confirming.publication.confirming {
		t.Error("the screen asked to publish before the draft had been opened")
	}
	if len(backend.publicationRequests) != 0 {
		t.Fatalf("enter published %v without the draft being read", backend.publicationRequests)
	}
	if !strings.Contains(confirming.status, "open the draft first") {
		t.Errorf("the screen does not say what is missing: %q", confirming.status)
	}

	// Reading it, and then approving.
	read := readDraft(t, model, backend)
	if backend.publicationEdits != 1 {
		t.Fatalf("e opened the editor %d times", backend.publicationEdits)
	}
	if len(backend.publicationRequests) != 0 {
		t.Errorf("reading the draft published %v", backend.publicationRequests)
	}

	asked := press(t, read, "enter")
	if !asked.publication.confirming {
		t.Fatal("enter after reading the draft did not ask the last question")
	}
	if len(backend.publicationRequests) != 0 {
		t.Errorf("the question published %v before it was answered", backend.publicationRequests)
	}
}

// TestWhatIsSentIsWhatCameBackFromTheEditor checks that the user's words win.
func TestWhatIsSentIsWhatCameBackFromTheEditor(t *testing.T) {
	backend := newFakeBackend()
	model := publishable(t, backend)
	// What the user left in the editor, which is not what the agent wrote.
	backend.approved = []api.ApprovedPublication{{
		RepositoryID: "core",
		Title:        "Export the daily report, hourly",
		Body:         "Rewritten by the person sending it.",
		Commit:       "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d",
	}}

	published := press(t, press(t, readDraft(t, model, backend), "enter"), "y")

	if len(backend.publicationRequests) != 1 {
		t.Fatalf("%d publications reached the daemon, want one", len(backend.publicationRequests))
	}
	sent := backend.publicationRequests[0].Repositories
	if len(sent) != 1 || sent[0].Title != "Export the daily report, hourly" {
		t.Errorf("what was sent is %+v, want the words the editor came back with", sent)
	}
	if sent[0].Body != "Rewritten by the person sending it." {
		t.Errorf("body = %q", sent[0].Body)
	}
	_ = published
}

// TestAnythingButYesLeavesThePublicationUnsent keeps the last question from
// being answered by accident.
func TestAnythingButYesLeavesThePublicationUnsent(t *testing.T) {
	backend := newFakeBackend()
	model := publishable(t, backend)

	asked := press(t, readDraft(t, model, backend), "enter")
	left := press(t, asked, "n")

	if len(backend.publicationRequests) != 0 {
		t.Errorf("a question answered with something other than yes published %v",
			backend.publicationRequests)
	}
	if left.publication.confirming {
		t.Error("the question is still open after it was answered")
	}
	if !strings.Contains(left.status, "nothing was published") {
		t.Errorf("the screen does not say what happened: %q", left.status)
	}
}

// TestAStaleDraftIsRefusedOnTheScreenToo keeps the two clients answering alike.
//
// The document does not offer a stale repository, so what this arranges is a
// document that disagrees with the plan it was written from: the words arrive
// approved anyway. They are refused here rather than sent for the daemon to
// refuse, so the answer to the last question is not spent on something that
// cannot happen.
func TestAStaleDraftIsRefusedOnTheScreenToo(t *testing.T) {
	backend := newFakeBackend()
	model := publishable(t, backend)
	model.publication.status.Drafts[0].Stale = true
	model.publication.status.Drafts[0].DraftCommit = "9999999999999999999999999999999999999999"

	asked := press(t, readDraft(t, model, backend), "enter")
	if asked.publication.confirming {
		t.Error("a stale draft reached the last question")
	}
	if len(backend.publicationRequests) != 0 {
		t.Errorf("a stale draft was published: %v", backend.publicationRequests)
	}
	if !strings.Contains(asked.status, "no longer current") {
		t.Errorf("the screen does not say why: %q", asked.status)
	}
	if !strings.Contains(asked.publicationBody(), "no longer current") {
		t.Errorf("the screen does not mark the stale repository:\n%s", asked.publicationBody())
	}
}

// TestOneStaleDraftLeavesTheOtherRepositoriesPublishable is what a stale draft
// costs and what it does not.
//
// It is one repository's problem: the agent described a commit that is no longer
// current there, and no edit resolves it. Refusing the whole publication for it
// would leave a user waiting on a fresh draft for a repository they were not
// publishing — and the daemon, which asks only about the repositories in the
// request, would have taken the others.
func TestOneStaleDraftLeavesTheOtherRepositoriesPublishable(t *testing.T) {
	backend := newFakeBackend()
	model := publishable(t, backend)
	model.publication.status.Drafts = append(model.publication.status.Drafts, api.PublicationDraft{
		RepositoryID: "schema", Forge: "gitlab", Remote: "origin",
		Branch: "feat/7f3a1c2e-add-a-scheduled-export-job", BaseBranch: "main",
		Commit: "0011223344556677889900aabbccddeeff001122",
		Title:  "Add the export table", Body: "Written before the work finished.",
		Stale: true, DraftCommit: "9999999999999999999999999999999999999999",
	})

	// The document offers core alone, so that is what comes back from the
	// editor: the stale repository was never the user's to approve.
	asked := press(t, readDraft(t, model, backend), "enter")
	if !asked.publication.confirming {
		t.Fatalf("a stale draft in another repository stopped the publication: %q", asked.status)
	}
	// And the one that was left out is on the screen the user answers from,
	// saying why it is not among them.
	if !strings.Contains(asked.publicationBody(), "no longer current") {
		t.Errorf("the screen does not mark the stale repository:\n%s", asked.publicationBody())
	}

	press(t, asked, "y")
	if len(backend.publicationRequests) != 1 {
		t.Fatalf("%d publications reached the daemon, want the one repository that was publishable",
			len(backend.publicationRequests))
	}
	sent := backend.publicationRequests[0].Repositories
	if len(sent) != 1 || sent[0].RepositoryID != "core" {
		t.Errorf("what was sent is %+v, want only core", sent)
	}
}

// TestAScreenWithNothingLeftToPublishOffersNoEditor is the empty case.
//
// Every repository has published or has a stale draft, so there is no document
// to open. A key that opens an editor on nothing is a key that appears to do
// something, and the screen says why instead.
func TestAScreenWithNothingLeftToPublishOffersNoEditor(t *testing.T) {
	backend := newFakeBackend()
	model := publishable(t, backend)
	model.publication.status.Drafts[0].Stale = true
	model.publication.status.Drafts[0].DraftCommit = "9999999999999999999999999999999999999999"

	after := press(t, model, "e")
	if backend.publicationEdits != 0 {
		t.Errorf("e opened an editor on a document with no sections (%d times)", backend.publicationEdits)
	}
	if !strings.Contains(after.publicationBody(), "none of these is left to publish") {
		t.Errorf("the screen does not say why nothing can be published:\n%s", after.publicationBody())
	}
	if strings.Contains(after.publicationHints(), "read and edit the draft") {
		t.Errorf("the footer offers a key that does nothing: %q", after.publicationHints())
	}
}

// TestTheRecordIsShownIncludingWhatWasNotAttempted is how a user meets a
// partial publication.
//
// Nothing is rolled back, so the record is the state of the world: what is on
// the forges, what failed, and what this publication never got to.
func TestTheRecordIsShownIncludingWhatWasNotAttempted(t *testing.T) {
	backend := newFakeBackend()
	model := publishable(t, backend)
	model.publication.status.Publication = &api.Publication{Repositories: []api.PublicationRepository{
		{RepositoryID: "core", State: "published", Request: &api.MergeRequest{
			Reference: "!1", URL: "https://gitlab.example.com/app/core/-/merge_requests/1"}},
		{RepositoryID: "schema", State: "failed", Failure: "GitLab: protected branch"},
		{RepositoryID: "docs", State: "planned"},
	}}

	body := model.publicationBody()
	for _, want := range []string{
		"merge_requests/1", "GitLab: protected branch", "not attempted",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the screen does not show %q:\n%s", want, body)
		}
	}
}

// TestLookingAgainAfterAPublicationShowsTheNewPlan is how a partial publication
// is finished.
//
// Nothing is rolled back, so what a failure leaves is a record and repositories
// that are still unpublished. Looking again composes a fresh plan that skips
// what already published (ADR-073), and the screen has to show it: a finished
// view that keeps drawing over a new plan makes the key that asked for it look
// like it did nothing.
func TestLookingAgainAfterAPublicationShowsTheNewPlan(t *testing.T) {
	backend := newFakeBackend()
	model := publishable(t, backend)
	// One repository published and one failed, which is what a user comes back
	// to this screen for.
	backend.publicationDone = api.PublicationStatus{
		Task: model.publication.status.Task,
		Publication: &api.Publication{Repositories: []api.PublicationRepository{
			{RepositoryID: "core", State: "failed", Failure: "GitLab: protected branch"},
		}},
	}
	published := press(t, press(t, readDraft(t, model, backend), "enter"), "y")
	if !published.publication.done {
		t.Fatal("the screen does not know a publication ran")
	}
	if !strings.Contains(published.publicationHints(), "look again") {
		t.Errorf("the finished screen does not offer a way back to a plan: %q", published.publicationHints())
	}

	// Looking again, and the plan that comes back.
	looking := press(t, published, "r")
	if len(backend.publicationCalls) != 2 {
		t.Fatalf("r planned %d times, want a fresh plan", len(backend.publicationCalls))
	}
	again, cmd := looking.Update(publicationPlanMsg{
		task: looking.publication.task, status: backend.publicationStatus,
	})
	replanned := applyCommand(t, again.(Model), cmd)

	if replanned.publication.done {
		t.Error("a fresh plan arrived and the screen still shows the publication that ran")
	}
	body := replanned.publicationBody()
	if !strings.Contains(body, "Export the daily report") {
		t.Errorf("the screen does not show the plan it just asked for:\n%s", body)
	}
	// The draft has to be read again before anything is sent, and the record of
	// what already happened is still there to read.
	if replanned.publication.read || len(replanned.publication.approved) != 0 {
		t.Error("looking again kept an approval from before the publication")
	}
	if !strings.Contains(replanned.publicationHints(), "read and edit the draft") {
		t.Errorf("the screen does not offer the draft again: %q", replanned.publicationHints())
	}
}

// TestAPublicationInFlightSwallowsTheKeyboard keeps one key press from becoming
// two publications.
//
// It is pushing branches and opening merge requests one repository at a time,
// and a second press must not start it again: what it creates is on somebody
// else's server and is not undone.
func TestAPublicationInFlightSwallowsTheKeyboard(t *testing.T) {
	backend := newFakeBackend()
	model := publishable(t, backend)
	model.publication.working, model.publication.publishing = true, true

	after := press(t, model, "enter")
	if len(backend.publicationRequests) != 0 {
		t.Errorf("a key press during a publication started another: %v", backend.publicationRequests)
	}
	if after.screen != screenPublication {
		t.Error("a key press during a publication left the screen")
	}
	if !strings.Contains(after.publicationBody(), "one repository at a time") {
		t.Errorf("the screen does not say what it is doing:\n%s", after.publicationBody())
	}
}

// TestAFailedPlanIsShownRatherThanThrown checks the ordinary error path.
func TestAFailedPlanIsShownRatherThanThrown(t *testing.T) {
	backend := newFakeBackend()
	backend.publicationErr = errors.New("no daemon is answering")

	model := publishable(t, backend)
	if model.publication.err == nil {
		t.Fatal("a failed plan left no error on the screen")
	}
	if !strings.Contains(model.publicationBody(), "no daemon is answering") {
		t.Errorf("the screen does not show the failure:\n%s", model.publicationBody())
	}
}

// TestAResponseForAnotherTaskIsDropped is the rule every screen here follows.
func TestAPublicationResponseForAnotherTaskIsDropped(t *testing.T) {
	backend := newFakeBackend()
	model := publishable(t, backend)

	other := api.PublicationStatus{Task: api.Task{ID: "another", Key: "another"}}
	updated, _ := model.Update(publicationPlanMsg{task: "another", status: other})

	if got := updated.(Model).publication.status.Task.ID; got == "another" {
		t.Error("the screen drew another task's publication under this task's name")
	}
}

// TestEscapeLeavesThePublicationScreen checks the way out.
func TestEscapeLeavesThePublicationScreen(t *testing.T) {
	backend := newFakeBackend()
	model := publishable(t, backend)

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	left := applyCommand(t, updated.(Model), cmd)

	if left.screen == screenPublication {
		t.Error("esc left the dashboard on the publication screen")
	}
	if len(backend.publicationRequests) != 0 {
		t.Errorf("leaving the screen published %v", backend.publicationRequests)
	}
}

// readDraft presses the key that opens the draft and then delivers what the
// editor came back with.
//
// The editor itself is Bubble Tea's to run: a screen hands it the terminal and
// is told afterwards. What this exercises is both halves of that — the request
// for the document, and the words it returned.
func readDraft(t *testing.T, model Model, backend *fakeBackend) Model {
	t.Helper()

	opened := press(t, model, "e")
	updated, cmd := opened.Update(publicationEditedMsg{
		task: opened.publication.task, approved: backend.approved,
	})
	after := applyCommand(t, updated.(Model), cmd)
	if !after.publication.read {
		t.Fatal("the draft came back and the screen does not know it was read")
	}
	return after
}
