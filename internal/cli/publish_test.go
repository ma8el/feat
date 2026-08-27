package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/paths"
)

// plannedDrafts is what a plan offers for two repositories.
func plannedDrafts() []api.PublicationDraft {
	return []api.PublicationDraft{
		{
			RepositoryID: "api", Forge: "gitlab", Remote: "origin",
			Branch: "feat/7f3a1c2e-rate-limit", BaseBranch: "main",
			Commit: "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d",
			Title:  "Add a rate limit to the public API",
			Body:   "It is per token.\n\n## Why\n\nBecause the free tier is being scraped.",
		},
		{
			RepositoryID: "store", Forge: "gitlab", Remote: "origin",
			Branch: "feat/7f3a1c2e-rate-limit", BaseBranch: "main",
			Commit: "2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e",
			Title:  "Add the counter table",
			Body:   "One row per token.",
		},
	}
}

func plannedStatus() api.PublicationStatus {
	return api.PublicationStatus{
		Task:   api.Task{Key: "7f3a1c2e", Title: "Add a rate limit"},
		Drafts: plannedDrafts(),
	}
}

// TestTheDraftDocumentSurvivesBeingReadBack is the round trip the approval
// rests on.
//
// What is sent is what the user had open, so the document has to come back
// carrying the same words — including a description whose own lines begin with
// "#", which are Markdown headings rather than comments (ADR-070).
func TestTheDraftDocumentSurvivesBeingReadBack(t *testing.T) {
	document := publicationDocument(plannedStatus())

	approved, err := readPublicationDocument(document, plannedDrafts())
	if err != nil {
		t.Fatalf("reading the draft back: %v", err)
	}
	if len(approved) != 2 {
		t.Fatalf("the document came back with %d repositories, want two", len(approved))
	}

	for i, want := range plannedDrafts() {
		if approved[i].RepositoryID != want.RepositoryID {
			t.Errorf("repository %d is %q, want %q", i, approved[i].RepositoryID, want.RepositoryID)
		}
		if approved[i].Title != want.Title {
			t.Errorf("title %d is %q, want %q", i, approved[i].Title, want.Title)
		}
		if approved[i].Body != strings.TrimSpace(want.Body) {
			t.Errorf("body %d is %q, want %q", i, approved[i].Body, want.Body)
		}
		// The commit comes from the plan rather than from the document: it is
		// what the words were composed against, and it is not the user's to
		// edit.
		if approved[i].Commit != want.Commit {
			t.Errorf("commit %d is %q, want the one the plan composed against", i, approved[i].Commit)
		}
	}
}

// TestTheHeaderIsCommentsAndTheBodyIsNot is the one structural rule.
//
// Only the lines above the first marker are comments, because a description is
// Markdown and Markdown has headings. A parser that stripped "#" everywhere
// would silently delete the section titles out of somebody's merge request.
func TestTheHeaderIsCommentsAndTheBodyIsNot(t *testing.T) {
	document := publicationDocument(plannedStatus())
	if !strings.Contains(document, "# Publication for 7f3a1c2e") {
		t.Errorf("the document has no header:\n%s", document)
	}
	if !strings.Contains(document, "which are Markdown headings there rather than comments") {
		t.Error("the header does not say that a description's own \"#\" lines are kept")
	}

	approved, err := readPublicationDocument(document, plannedDrafts())
	if err != nil {
		t.Fatalf("reading the draft back: %v", err)
	}
	if !strings.Contains(approved[0].Body, "## Why") {
		t.Errorf("the description lost its heading: %q", approved[0].Body)
	}
	if strings.Contains(approved[0].Body, "Publication for") {
		t.Errorf("the header reached a description: %q", approved[0].Body)
	}
}

// TestADescriptionThatLooksLikeAMarkerStaysDescription is what the widening
// fence is for.
//
// The description is the agent's, and it can carry anything the agent read —
// including a line shaped exactly like a section marker, out of a dependency's
// changelog or an issue body. A fixed marker would let that line become
// structure: the description above it cut short, and the publication either
// refused because a repository now appears twice or sent under a name the user
// never approved. The words stay words (ADR-070).
func TestADescriptionThatLooksLikeAMarkerStaysDescription(t *testing.T) {
	drafts := plannedDrafts()
	drafts[0].Body = "It is per token.\n\n=== store ===\nThe agent wrote this line.\n\n" +
		"=== Overview ===\n\nAnd this one."
	status := plannedStatus()
	status.Drafts = drafts

	document := publicationDocument(status)
	if !strings.Contains(document, "==== api ====") {
		t.Errorf("the marker did not widen around a description that contains one:\n%s", document)
	}
	if !strings.Contains(document, "the first \"====\" marker") {
		t.Errorf("the header names a marker the document does not use:\n%s", document)
	}

	approved, err := readPublicationDocument(document, drafts)
	if err != nil {
		t.Fatalf("reading the draft back: %v", err)
	}
	if len(approved) != 2 {
		t.Fatalf("the document came back with %d repositories, want two: %+v", len(approved), approved)
	}
	if approved[0].Body != strings.TrimSpace(drafts[0].Body) {
		t.Errorf("the description was cut short at a line that looks like a marker:\n%s", approved[0].Body)
	}
	if approved[1].RepositoryID != "store" || approved[1].Title != drafts[1].Title {
		t.Errorf("the real section for store came back as %+v", approved[1])
	}
}

// TestAnInjectedMarkerCannotApproveARepositoryTheDocumentLeftOut is the same
// hazard where it was silent.
//
// A repository that already published is named in the plan and left out of the
// document. A marker for it inside somebody else's description would then be the
// only section it has — an approval, with the agent's own words, that no person
// ever read.
func TestAnInjectedMarkerCannotApproveARepositoryTheDocumentLeftOut(t *testing.T) {
	drafts := plannedDrafts()
	drafts[1].Published = &api.MergeRequest{
		Reference: "!7", URL: "https://gitlab.example.com/app/store/-/merge_requests/7",
	}
	drafts[0].Body = "It is per token.\n\n=== store ===\nWords nobody approved."
	status := plannedStatus()
	status.Drafts = drafts

	approved, err := readPublicationDocument(publicationDocument(status), drafts)
	if err != nil {
		t.Fatalf("reading the draft back: %v", err)
	}
	if len(approved) != 1 || approved[0].RepositoryID != "api" {
		t.Fatalf("the document came back as %+v, want only the repository it offered", approved)
	}
	if !strings.Contains(approved[0].Body, "=== store ===") {
		t.Errorf("the description lost the line: %q", approved[0].Body)
	}
}

// TestASectionWithNoTitleIsRefusedRatherThanPromotingTheLineBelowIt is the
// no-draft case.
//
// The agent writes no draft for every repository, and the plan says so: write a
// title before approving. What stands in the section is then the description
// alone — the agent's first sentence, or the ticket line Feat itself added — and
// a rule that took the first non-empty line would send that as the title of the
// merge request, with the description missing the line it took.
func TestASectionWithNoTitleIsRefusedRatherThanPromotingTheLineBelowIt(t *testing.T) {
	ticket := "Task 7f3a1c2e — https://tracker.example.invalid/stories/482"
	drafts := plannedDrafts()
	drafts[1].Title, drafts[1].Body = "", ticket
	status := plannedStatus()
	status.Drafts = drafts

	document := publicationDocument(status)
	_, err := readPublicationDocument(document, drafts)
	if err == nil {
		t.Fatal("a section with no title was approved")
	}
	if !strings.Contains(err.Error(), "store has no title") {
		t.Errorf("the refusal is %q, and it does not name the repository", err)
	}
	if !strings.Contains(err.Error(), "=== store ===") {
		t.Errorf("the refusal is %q, and it does not say where the title goes", err)
	}

	// And writing it where the document left the slot is all it takes.
	filled := strings.Replace(document, "=== store ===\n\n", "=== store ===\nAdd the counter table\n", 1)
	approved, err := readPublicationDocument(filled, drafts)
	if err != nil {
		t.Fatalf("reading the filled-in draft back: %v", err)
	}
	if len(approved) != 2 || approved[1].Title != "Add the counter table" {
		t.Fatalf("the filled-in draft came back as %+v", approved)
	}
	if approved[1].Body != ticket {
		t.Errorf("the description is %q, want the ticket line the title was written above", approved[1].Body)
	}
}

// TestARemovedSectionIsARepositoryLeftUnpublished is how a user says no to one
// of them.
func TestARemovedSectionIsARepositoryLeftUnpublished(t *testing.T) {
	document := publicationDocument(plannedStatus())
	cut := strings.Index(document, "=== store ===")
	if cut < 0 {
		t.Fatalf("the document has no section for store:\n%s", document)
	}

	approved, err := readPublicationDocument(document[:cut], plannedDrafts())
	if err != nil {
		t.Fatalf("reading the draft back: %v", err)
	}
	if len(approved) != 1 || approved[0].RepositoryID != "api" {
		t.Errorf("the document came back as %+v, want only the section that was left", approved)
	}
}

// TestAnEditedDraftIsWhatIsSent checks that the user's words win.
func TestAnEditedDraftIsWhatIsSent(t *testing.T) {
	edited := "# header, which is comments\n\n" +
		"=== api ===\n" +
		"Rate-limit the public API by token\n" +
		"\n" +
		"The person sending this rewrote it.\n"

	approved, err := readPublicationDocument(edited, plannedDrafts())
	if err != nil {
		t.Fatalf("reading the draft back: %v", err)
	}
	if len(approved) != 1 {
		t.Fatalf("the document came back with %d repositories, want one", len(approved))
	}
	if approved[0].Title != "Rate-limit the public API by token" {
		t.Errorf("title = %q", approved[0].Title)
	}
	if approved[0].Body != "The person sending this rewrote it." {
		t.Errorf("body = %q", approved[0].Body)
	}
}

// TestADocumentThisPublicationDidNotPlanIsRefused keeps an edited file from
// naming somewhere else.
func TestADocumentThisPublicationDidNotPlanIsRefused(t *testing.T) {
	cases := map[string]struct {
		document string
		contains string
	}{
		"a repository the plan never offered": {
			document: "=== elsewhere ===\nA title\n",
			contains: "not one this publication planned",
		},
		"the same repository twice": {
			document: "=== api ===\nOne\n\n=== api ===\nTwo\n",
			contains: "two sections for repository",
		},
		"a section with no title": {
			document: "=== api ===\n\n\n",
			contains: "has no title",
		},
	}

	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := readPublicationDocument(test.document, plannedDrafts())
			if err == nil {
				t.Fatal("the document was accepted")
			}
			if !strings.Contains(err.Error(), test.contains) {
				t.Errorf("the refusal is %q, and it does not say %q", err, test.contains)
			}
		})
	}
}

// TestAnAlreadyPublishedRepositoryIsNotInTheDocument keeps a user from editing
// words that have already been sent.
func TestAnAlreadyPublishedRepositoryIsNotInTheDocument(t *testing.T) {
	status := plannedStatus()
	status.Drafts[1].Published = &api.MergeRequest{
		Reference: "!7", URL: "https://gitlab.example.com/app/store/-/merge_requests/7",
	}

	document := publicationDocument(status)
	if strings.Contains(document, "=== store ===") {
		t.Errorf("a repository that already published is offered for editing:\n%s", document)
	}
	if !strings.Contains(document, "=== api ===") {
		t.Errorf("the repository still to publish is missing:\n%s", document)
	}
}

// fakePublisher answers what the daemon would and records what reached it.
//
// It is what the publisher interface exists for: the document, the editor, and
// the confirmation are most of this command, and none of them needs a socket.
type fakePublisher struct {
	plan     api.PublicationStatus
	result   api.PublicationStatus
	requests []api.PublishRequest
}

func (f *fakePublisher) PlanPublication(context.Context, string) (api.PublicationStatus, error) {
	return f.plan, nil
}

func (f *fakePublisher) ApplyPublication(
	_ context.Context, _ string, request api.PublishRequest,
) (api.PublicationStatus, error) {
	f.requests = append(f.requests, request)
	return f.result, nil
}

// runPublish drives the whole command: plan, editor, confirmation, apply.
//
// The editor is a program rather than a person, so the document comes back
// exactly as it was written — which is what a user who reads it and saves does.
//
// The answers are a file rather than a reader, because the editor is handed this
// process's own input the way it is handed a terminal: os/exec passes a file
// descriptor through and copies anything else, and a copy would drain the
// answers into a program that never reads them.
func runPublish(t *testing.T, caller publisher, answer string) (string, error) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "answers")
	if err := os.WriteFile(path, []byte(answer), 0o600); err != nil {
		t.Fatalf("writing the answers: %v", err)
	}
	answers, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening the answers: %v", err)
	}
	defer func() { _ = answers.Close() }()

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(answers)

	err = publish(cmd, caller, &environment{}, "7f3a1c2e")
	return out.String(), err
}

// TestOneStaleDraftLeavesTheOtherRepositoriesPublishable is what a publication
// refuses and what it does not.
//
// A stale draft is one repository's problem: the agent described a commit that
// is no longer current there, and no edit resolves it, because the refusal is
// about the commit rather than about the words. Refusing the whole publication
// for it would leave a user waiting on a fresh draft for a repository they were
// not publishing — while the daemon, which only ever asked about the
// repositories in the request, would have taken the others.
func TestOneStaleDraftLeavesTheOtherRepositoriesPublishable(t *testing.T) {
	plan := plannedStatus()
	plan.Editor = api.EditorCommand{Program: "true"}
	plan.Drafts[1].Stale = true
	plan.Drafts[1].DraftCommit = "9999999999999999999999999999999999999999"

	caller := &fakePublisher{plan: plan}
	printed, err := runPublish(t, caller, "y\n")
	if err != nil {
		t.Fatalf("publishing with one stale draft: %v\n%s", err, printed)
	}

	if len(caller.requests) != 1 {
		t.Fatalf("%d publications reached the daemon, want the one repository that was publishable",
			len(caller.requests))
	}
	sent := caller.requests[0].Repositories
	if len(sent) != 1 || sent[0].RepositoryID != "api" {
		t.Fatalf("what was sent is %+v, want only api", sent)
	}
	// And the one that was left out is still named, with the state that says
	// why: it is not offered for editing, and the plan is where a user learns
	// that rather than from a document that silently has one section.
	if !strings.Contains(printed, "stale draft") {
		t.Errorf("the plan does not say store is stale:\n%s", printed)
	}
}

// TestAStaleSectionIsNotOfferedForEditing is the other half of the same rule.
//
// The words cannot be made publishable by rewriting them, so offering them for
// rewriting would be offering something that cannot happen: a user would edit,
// save, and be refused for a reason their edit could not touch.
func TestAStaleSectionIsNotOfferedForEditing(t *testing.T) {
	status := plannedStatus()
	status.Drafts[1].Stale = true

	document := publicationDocument(status)
	if strings.Contains(document, "=== store ===") {
		t.Errorf("a stale draft is offered for editing:\n%s", document)
	}
	if !strings.Contains(document, "=== api ===") {
		t.Errorf("the repository that can still publish is missing:\n%s", document)
	}

	approved, err := readPublicationDocument(document, status.Drafts)
	if err != nil {
		t.Fatalf("reading the draft back: %v", err)
	}
	if len(approved) != 1 || approved[0].RepositoryID != "api" {
		t.Errorf("the document came back as %+v, want only what it offered", approved)
	}
}

// TestNothingLeftToPublishOpensNoEditorAndBlamesNobody is the empty case.
//
// Every repository has published or has a stale draft, so there is no document:
// opening an editor on nothing and then reporting that the user removed every
// repository would blame them for a state they did not make. The editor here is
// `false`, so running it at all would fail the command.
func TestNothingLeftToPublishOpensNoEditorAndBlamesNobody(t *testing.T) {
	plan := plannedStatus()
	plan.Editor = api.EditorCommand{Program: "false"}
	plan.Drafts[0].Published = &api.MergeRequest{
		Reference: "!7", URL: "https://gitlab.example.com/app/api/-/merge_requests/7",
	}
	plan.Drafts[1].Stale = true

	caller := &fakePublisher{plan: plan}
	printed, err := runPublish(t, caller, "")
	if err != nil {
		t.Fatalf("publishing a task with nothing left: %v\n%s", err, printed)
	}

	if len(caller.requests) != 0 {
		t.Errorf("a task with nothing left to publish sent %+v", caller.requests)
	}
	if !strings.Contains(printed, "nothing to publish") {
		t.Errorf("the command does not say what happened:\n%s", printed)
	}
	if strings.Contains(printed, "was removed from the draft") {
		t.Errorf("the command blames the user for a document it never opened:\n%s", printed)
	}
	// What is there is still reported, which is where the user learns why.
	if !strings.Contains(printed, "merge_requests/7") || !strings.Contains(printed, "stale draft") {
		t.Errorf("the plan does not say what the task has:\n%s", printed)
	}
}

// TestBothClientsGetTheSameDraftFile is the machinery the two share.
//
// The terminal and the dashboard differ only in who runs the editor, so the
// file, its permissions, the parser, and the cleanup are one constructor's:
// two copies of this would be two ways for the clients to disagree about what
// the user approved, and they had already come apart over who removes the
// directory.
func TestBothClientsGetTheSameDraftFile(t *testing.T) {
	plan := plannedStatus()
	plan.Editor = api.EditorCommand{Program: "true"}

	draft, err := newPublicationDraft(plan, &environment{})
	if err != nil {
		t.Fatalf("writing the publication draft: %v", err)
	}

	info, err := os.Stat(draft.path)
	if err != nil {
		t.Fatalf("the draft was not written: %v", err)
	}
	// The description of somebody's change, in a file only they can read.
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("the draft is mode %o, want 0600", mode)
	}
	if !strings.Contains(draft.path, plan.Task.Key) {
		t.Errorf("the draft is at %q, and its name does not say which task it is for", draft.path)
	}

	// Unedited, which is a user who read it and saved: what comes back is what
	// the plan composed.
	approved, err := draft.Read()
	if err != nil {
		t.Fatalf("reading the draft back: %v", err)
	}
	if len(approved) != 2 || approved[0].Title != plannedDrafts()[0].Title {
		t.Errorf("the draft came back as %+v", approved)
	}

	draft.Close()
	if _, err := os.Stat(draft.directory); !os.IsNotExist(err) {
		t.Errorf("closing the draft left the description on disk: %v", err)
	}
}

// TestTheEditorKeepsItsConfiguredFlags is why the daemon leaves the document
// slot off.
//
// `code -w` has to stay `code -w`, or the editor returns before the user has
// typed anything and the draft is approved unread.
func TestTheEditorKeepsItsConfiguredFlags(t *testing.T) {
	command, err := publicationEditor(
		api.EditorCommand{Program: "code", Arguments: []string{"-w"}},
		&environment{}, "/tmp/publication.md")
	if err != nil {
		t.Fatalf("building the editor command: %v", err)
	}
	if command.Path == "" || len(command.Args) != 3 {
		t.Fatalf("the editor command is %v", command.Args)
	}
	if command.Args[1] != "-w" || command.Args[2] != "/tmp/publication.md" {
		t.Errorf("the editor command is %v, want its flags kept and the draft appended", command.Args)
	}
}

// TestAnUnconfiguredEditorFallsBackToTheEnvironment is FR-REV-003's default,
// which only the client can resolve.
func TestAnUnconfiguredEditorFallsBackToTheEnvironment(t *testing.T) {
	env := &environment{process: &paths.Environment{
		Home:   "/tmp/feat-test-home",
		Getenv: func(name string) string { return map[string]string{"VISUAL": "emacsclient -f"}[name] },
	}}

	command, err := publicationEditor(api.EditorCommand{}, env, "/tmp/publication.md")
	if err != nil {
		t.Fatalf("building the editor command: %v", err)
	}
	if len(command.Args) != 3 || command.Args[1] != "-f" {
		t.Errorf("the editor command is %v, want $VISUAL with its own arguments", command.Args)
	}
}

// TestAnEditorThatWouldBeReadAsAnOptionIsRefused keeps a configured value from
// becoming a flag of the process that runs it.
func TestAnEditorThatWouldBeReadAsAnOptionIsRefused(t *testing.T) {
	_, err := publicationEditor(api.EditorCommand{Program: "--wait"}, &environment{}, "/tmp/x.md")
	if err == nil || !strings.Contains(err.Error(), "read as an option") {
		t.Errorf("an editor that is an option answered %v", err)
	}
}
