package ui

import (
	"errors"
	"strconv"
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

// document is the whole draft, window or no window.
//
// A test about what the screen says is not a test about how many lines of it
// fit at once: what is on the screen is what TestThePublicationScreenIsDrawn
// and the reading gate are about, and this is for the rest.
func document(m Model) string {
	width, _ := m.publicationRegion()
	lines, _ := m.publicationLines(width)
	return strings.Join(lines, "\n")
}

// TestOpeningPublicationSendsNothing is why it is safe to reach with one key.
//
// What publishing would do is a question. Only what the user approves is ever
// acted on, and approving is two key presses further on.
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
}

// TestThePublicationScreenIsDrawnWhereItIsOpened is the defect this screen
// shipped with: it was drawn in the narrow fallback and nowhere else.
//
// dialogView answered every other overlay and not this one, so on a terminal of
// an ordinary size P changed the screen, asked the daemon for a plan, and drew
// the dashboard it was already drawing — with a footer offering "esc close" over
// a dialog that was never there. What the user could see of the publication was
// the daemon's socket path (ADR-076).
func TestThePublicationScreenIsDrawnWhereItIsOpened(t *testing.T) {
	backend := newFakeBackend()

	for _, size := range []struct {
		name          string
		width, height int
	}{
		{"a dialog over the dashboard", 120, 40},
		{"the narrow fallback", minimumWidth - 1, 30},
	} {
		t.Run(size.name, func(t *testing.T) {
			model := sized(publishable(t, backend), size.width, size.height)
			view := flowed(model.View())

			for _, want := range []string{
				"publish task " + liveTask().Key,   // whose publication this is
				"core",                             // the repository
				"Export the daily report",          // the title that would be sent
				"It writes to the configured buck", // and the description
				"e edit the draft",                 // what to press, and
				"enter publish",                    // what it leads to
			} {
				if !strings.Contains(view, want) {
					t.Errorf("the publication screen does not show %q at %dx%d:\n%s",
						want, size.width, size.height, model.View())
				}
			}
		})
	}
}

// TestNothingIsSentBeforeTheWordsHaveBeenRead is the control ADR-070 rests on.
//
// The agent's words can carry anything it read, and a person reading them before
// they are sent is the only control there is. A draft longer than the window has
// not been read while its end is below the fold, and the screen refuses to
// publish it — reading is scrolling to the end of it, not pressing a key that
// says one did.
func TestNothingIsSentBeforeTheWordsHaveBeenRead(t *testing.T) {
	backend := newFakeBackend()
	model := publishable(t, backend)
	model.publication.status.Drafts[0].Body = longDescription()

	if model.publicationRead() {
		t.Fatal("a draft longer than the screen counts as read before it was scrolled")
	}

	refused := press(t, model, "enter")
	if refused.publication.confirming {
		t.Error("the screen asked to publish words that were still below the fold")
	}
	if len(backend.publicationRequests) != 0 {
		t.Fatalf("enter published %v with the draft half read", backend.publicationRequests)
	}
	if !strings.Contains(refused.status, "read to the end") {
		t.Errorf("the screen does not say what is missing: %q", refused.status)
	}
	if strings.Contains(refused.publicationHints(), "enter publish") {
		t.Errorf("the footer offers a key that will refuse: %q", refused.publicationHints())
	}

	// And reading it is what opens the gate.
	read := readToTheEnd(t, refused)
	if len(backend.publicationRequests) != 0 {
		t.Errorf("reading the draft published %v", backend.publicationRequests)
	}
	asked := press(t, read, "enter")
	if !asked.publication.confirming {
		t.Fatalf("enter after reading the whole draft did not ask the last question: %q", asked.status)
	}
	if len(backend.publicationRequests) != 0 {
		t.Errorf("the question published %v before it was answered", backend.publicationRequests)
	}
}

// TestADraftThatFitsHasBeenReadWhereItIsDrawn is the other half of the gate.
//
// The words are on the screen in full, so they have been read, and publishing
// them takes no trip through an editor: what is displayed is what is sent, and
// this is where it was displayed (ADR-076). The editor is for rewriting them.
func TestADraftThatFitsHasBeenReadWhereItIsDrawn(t *testing.T) {
	backend := newFakeBackend()
	model := press(t, publishable(t, backend), "down")

	if !model.publicationRead() {
		t.Fatalf("a draft drawn in full does not count as read:\n%s", model.publicationBody())
	}

	published := press(t, press(t, model, "enter"), "y")
	if backend.publicationEdits != 0 {
		t.Errorf("publishing what was on the screen opened an editor %d times", backend.publicationEdits)
	}
	if len(backend.publicationRequests) != 1 {
		t.Fatalf("%d publications reached the daemon, want one", len(backend.publicationRequests))
	}

	sent := backend.publicationRequests[0].Repositories
	if len(sent) != 1 {
		t.Fatalf("what was sent is %+v, want the one repository on the screen", sent)
	}
	// Word for word what the screen drew, which is the invariant: a publication
	// composed from anything else would make the reading a formality.
	drawn := document(model)
	if !strings.Contains(drawn, sent[0].Title) || !strings.Contains(drawn, sent[0].Body) {
		t.Errorf("what was sent is %+v, and the screen showed:\n%s", sent[0], drawn)
	}
	if sent[0].Commit != "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d" {
		t.Errorf("the approval was composed against %q", sent[0].Commit)
	}
	if !published.publication.done {
		t.Error("the screen does not know the publication ran")
	}
}

// TestAnUntitledRepositoryIsRefusedByName keeps a publication from being
// quietly narrower than the screen it was approved from.
//
// A merge request needs a title and the agent wrote none, so this one cannot be
// opened. Publishing the rest and saying nothing would leave the user believing
// they had published every repository they just read.
func TestAnUntitledRepositoryIsRefusedByName(t *testing.T) {
	backend := newFakeBackend()
	model := publishable(t, backend)
	model.publication.status.Drafts[0].Title = ""

	refused := press(t, press(t, model, "down"), "enter")
	if refused.publication.confirming {
		t.Error("a repository with no title reached the last question")
	}
	if !strings.Contains(refused.status, "core") || !strings.Contains(refused.status, "no title") {
		t.Errorf("the refusal does not name the repository: %q", refused.status)
	}
	if !strings.Contains(document(refused), "no title yet") {
		t.Errorf("the screen does not mark the untitled repository:\n%s", document(refused))
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

	edited := editDraft(t, model, backend)
	// On the screen as well as in the request: what is drawn after an edit is
	// the user's own words, because those are the ones that would be sent.
	if drawn := document(edited); !strings.Contains(drawn, "Rewritten by the person sending it.") {
		t.Errorf("the screen still shows the agent's words after an edit:\n%s", drawn)
	}

	press(t, press(t, edited, "enter"), "y")
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
}

// TestARepositoryRemovedInTheEditorIsSaidToBeGone is what an edit that deletes
// a section leaves on the screen.
//
// The plan still names the repository, and the words that would have been sent
// for it are not being sent. A screen that went on drawing the agent's draft
// there would be showing something that is not going to happen.
func TestARepositoryRemovedInTheEditorIsSaidToBeGone(t *testing.T) {
	backend := newFakeBackend()
	model := publishable(t, backend)
	backend.approved = nil

	edited := editDraft(t, model, backend)
	if !strings.Contains(document(edited), "removed from the draft") {
		t.Errorf("the screen does not say the repository was removed:\n%s", document(edited))
	}

	refused := press(t, edited, "enter")
	if refused.publication.confirming {
		t.Error("a draft with every repository removed reached the last question")
	}
	if len(backend.publicationRequests) != 0 {
		t.Errorf("an empty draft published %v", backend.publicationRequests)
	}
}

// TestAnythingButYesLeavesThePublicationUnsent keeps the last question from
// being answered by accident.
func TestAnythingButYesLeavesThePublicationUnsent(t *testing.T) {
	backend := newFakeBackend()
	model := publishable(t, backend)

	asked := press(t, editDraft(t, model, backend), "enter")
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

	asked := press(t, editDraft(t, model, backend), "enter")
	if asked.publication.confirming {
		t.Error("a stale draft reached the last question")
	}
	if len(backend.publicationRequests) != 0 {
		t.Errorf("a stale draft was published: %v", backend.publicationRequests)
	}
	if !strings.Contains(asked.status, "no longer current") {
		t.Errorf("the screen does not say why: %q", asked.status)
	}
	if !strings.Contains(document(asked), "no longer current") {
		t.Errorf("the screen does not mark the stale repository:\n%s", document(asked))
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

	// The stale repository is never the user's to approve, in the document or on
	// the screen: what would be sent is core alone.
	asked := press(t, readToTheEnd(t, model), "enter")
	if !asked.publication.confirming {
		t.Fatalf("a stale draft in another repository stopped the publication: %q", asked.status)
	}
	// And the one that was left out is on the screen the user answers from,
	// saying why it is not among them.
	if !strings.Contains(document(asked), "no longer current") {
		t.Errorf("the screen does not mark the stale repository:\n%s", document(asked))
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
	if strings.Contains(after.publicationHints(), "edit the draft") {
		t.Errorf("the footer offers a key that does nothing: %q", after.publicationHints())
	}
	if strings.Contains(after.publicationHints(), "enter publish") {
		t.Errorf("the footer offers to publish nothing: %q", after.publicationHints())
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
	model.publication.status.Task.Publication = &api.Publication{Repositories: []api.PublicationRepository{
		{RepositoryID: "core", State: "published", Request: &api.MergeRequest{
			Reference: "!1", URL: "https://gitlab.example.com/app/core/-/merge_requests/1"}},
		{RepositoryID: "schema", State: "failed", Failure: "GitLab: protected branch"},
		{RepositoryID: "docs", State: "planned"},
	}}

	drawn := document(model)
	for _, want := range []string{
		"merge_requests/1", "GitLab: protected branch", "not attempted",
	} {
		if !strings.Contains(drawn, want) {
			t.Errorf("the screen does not show %q:\n%s", want, drawn)
		}
	}
}

// TestTheRecordIsNotSomethingToScrollPastToPublish keeps the reading gate about
// the words.
//
// What has to be read is what would be sent. Feat's own account of what this
// task already published is under it, and a user who has read the draft is not
// made to scroll through a list of merge requests that already exist before the
// screen will send the ones that do not.
func TestTheRecordIsNotSomethingToScrollPastToPublish(t *testing.T) {
	backend := newFakeBackend()
	model := publishable(t, backend)

	recorded := make([]api.PublicationRepository, 0, 40)
	for i := 0; i < 40; i++ {
		recorded = append(recorded, api.PublicationRepository{
			RepositoryID: "repository-" + strconv.Itoa(i), State: "published",
			Request: &api.MergeRequest{Reference: "!1", URL: "https://example.com/1"},
		})
	}
	model.publication.status.Task.Publication = &api.Publication{Repositories: recorded}

	asked := press(t, press(t, model, "down"), "enter")
	if !asked.publication.confirming {
		t.Errorf("a long record kept a read draft from being published: %q", asked.status)
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
	// A repository that failed, which is what a user comes back to this screen
	// for. The record travels on the task, where it lives.
	finished := model.publication.status.Task
	finished.Publication = &api.Publication{Repositories: []api.PublicationRepository{
		{RepositoryID: "core", State: "failed", Failure: "GitLab: protected branch"},
	}}
	backend.publicationDone = api.PublicationStatus{Task: finished}
	published := press(t, press(t, editDraft(t, model, backend), "enter"), "y")
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
	// The fresh plan is long enough not to fit, so that what the screen makes of
	// it says whether the last plan's reading carried over.
	fresh := backend.publicationStatus
	fresh.Drafts = []api.PublicationDraft{backend.publicationStatus.Drafts[0]}
	fresh.Drafts[0].Body = longDescription()

	again, cmd := looking.Update(publicationPlanMsg{task: looking.publication.task, status: fresh})
	replanned := applyCommand(t, again.(Model), cmd)

	if replanned.publication.done {
		t.Error("a fresh plan arrived and the screen still shows the publication that ran")
	}
	if !strings.Contains(document(replanned), "Export the daily report") {
		t.Errorf("the screen does not show the plan it just asked for:\n%s", document(replanned))
	}
	// Fresh words are read again before anything is sent, and the record of what
	// already happened is still there to read.
	if replanned.publication.edited || len(replanned.publication.approved) != 0 {
		t.Error("looking again kept an approval from before the publication")
	}
	if replanned.publicationRead() {
		t.Error("looking again counted a publication ago's reading against the new words")
	}
	if !strings.Contains(replanned.publicationHints(), "edit the draft") {
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
	if !strings.Contains(flowed(after.publicationHints()), "publishing") {
		t.Errorf("the footer offers a way out of a publication that will not be abandoned: %q",
			after.publicationHints())
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

// TestAPublicationResponseForAnotherTaskIsDropped is the rule every screen here
// follows.
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

// editDraft presses the key that opens the draft in an editor and then delivers
// what it came back with.
//
// The editor itself is Bubble Tea's to run: a screen hands it the terminal and
// is told afterwards. What this exercises is both halves of that — the request
// for the document, and the words it returned.
func editDraft(t *testing.T, model Model, backend *fakeBackend) Model {
	t.Helper()

	opened := press(t, model, "e")
	updated, cmd := opened.Update(publicationEditedMsg{
		task: opened.publication.task, approved: backend.approved,
	})
	after := applyCommand(t, updated.(Model), cmd)
	if !after.publication.edited {
		t.Fatal("the draft came back and the screen does not know it was edited")
	}
	return after
}

// readToTheEnd scrolls the draft until every word of it has been on the screen,
// which is what the gate asks of a user.
func readToTheEnd(t *testing.T, model Model) Model {
	t.Helper()

	for page := 0; page < 64; page++ {
		if model.publicationRead() {
			return model
		}
		model = press(t, model, "pgdown")
	}
	t.Fatalf("the draft never counted as read:\n%s", model.publicationBody())
	return model
}

// longDescription is a description that does not fit any window this screen is
// drawn in, so that reading it is something a user has to do.
func longDescription() string {
	lines := make([]string, 0, 60)
	for i := 0; i < 60; i++ {
		lines = append(lines, "It writes to the configured bucket, paragraph "+strconv.Itoa(i)+".")
	}
	return strings.Join(lines, "\n\n")
}
