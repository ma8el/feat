package cli

import (
	"strings"
	"testing"

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
