package claude_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ma8el/feat/internal/agent"
	"github.com/ma8el/feat/internal/agent/claude"
	"github.com/ma8el/feat/internal/control"
	"github.com/ma8el/feat/internal/domain"
)

// preparePublishing prepares a launch for a project that publishes the named
// repositories.
func preparePublishing(t *testing.T, repositories ...string) (agent.LaunchSpec, *control.Workspace) {
	t.Helper()

	workspace, err := control.Open(t.TempDir(), testProject, testTask, control.Options{})
	if err != nil {
		t.Fatalf("opening a control workspace: %v", err)
	}
	if err := workspace.Create(); err != nil {
		t.Fatalf("creating the control workspace: %v", err)
	}

	task, err := domain.NewTask(testTask, testProject, "Add a health endpoint",
		domain.TaskSource{Kind: domain.SourcePrompt}, time.Now())
	if err != nil {
		t.Fatalf("building a task: %v", err)
	}
	if err := task.SetBrief(testBrief, time.Now()); err != nil {
		t.Fatalf("setting the brief: %v", err)
	}

	spec, err := claude.New().Prepare(context.Background(), agent.PrepareRequest{
		Task: task,
		Workspace: agent.Workspace{
			WorkingDirectory: "/srv/api",
			ControlPath:      "/feat",
		},
		Control:     workspace,
		Environment: agent.Environment{Mode: domain.ExecutionHost},
		Publication: agent.Publication{Repositories: repositories},
	})
	if err != nil {
		t.Fatalf("preparing a launch: %v", err)
	}
	return spec, workspace
}

// TestTheAgentIsAskedForWordsAndNotForAnAction is the instruction half of
// ADR-070.
//
// The agent writes the description, because it is what knows what it did. It
// does not publish: every credentialed provider call is the host's, and the
// words go to a person first. Both halves are in the generated instructions,
// because an agent that believed it had opened a merge request would report
// work it had not done.
func TestTheAgentIsAskedForWordsAndNotForAnAction(t *testing.T) {
	_, workspace := preparePublishing(t, "api", "store")
	body := instructions(t, workspace)

	for _, phrase := range []string{
		"publication_draft",
		"\"repository\": \"api\"",
		"\"commit\"",
		"api, store",
		"git rev-parse HEAD",
		"refused rather than published",
		"You are not publishing anything",
		"Do not run `git push`, `gh`, or `glab` yourself",
	} {
		if !strings.Contains(body, phrase) {
			t.Errorf("the generated instructions do not say %q", phrase)
		}
	}
}

// TestAProjectWithNoForgeIsNeverToldAboutDrafts checks the other half.
//
// A project with no forge configured never sees the type. A document with
// nowhere to go is a document nobody reads, and a helper that accepted one would
// be inviting the agent to write it (ADR-070).
func TestAProjectWithNoForgeIsNeverToldAboutDrafts(t *testing.T) {
	_, workspace := preparePublishing(t)

	if body := instructions(t, workspace); strings.Contains(body, "publication_draft") {
		t.Error("a project that publishes nowhere was told how to write a publication draft")
	}
	helper := reportHelper(t, workspace)
	if strings.Contains(helper, "publication_draft") {
		t.Error("the generated helper accepts a publication draft for a project that publishes nowhere")
	}
}

// TestTheHelperAcceptsAPublicationDraftWhereThereIsSomewhereToPublish checks the
// generated script's own vocabulary.
func TestTheHelperAcceptsAPublicationDraftWhereThereIsSomewhereToPublish(t *testing.T) {
	_, workspace := preparePublishing(t, "api")
	helper := reportHelper(t, workspace)

	if !strings.Contains(helper,
		"review_requested|completion_report|open_question|publication_draft") {
		t.Errorf("the helper does not accept a publication draft:\n%s", helper)
	}
	if !strings.Contains(helper, "the merge request you propose for each repository") {
		t.Error("the helper's usage does not say what a publication draft is")
	}
}

// TestAPublicationDraftIsParsedAndNeverSummarisedIntoTheEventLog is the parsing
// half.
//
// The event carries the draft for the one screen that shows it and a summary
// that names the repositories and nothing else. A title is agent-authored text
// bound for somewhere durable, and the task's history is not where a user reads
// it before approving it.
func TestAPublicationDraftIsParsedAndNeverSummarisedIntoTheEventLog(t *testing.T) {
	message := control.Message{
		SchemaVersion: control.SchemaVersion,
		ID:            "draft",
		TaskID:        testTask,
		Type:          control.TypePublicationDraft,
		OccurredAt:    time.Now(),
		Payload: []byte(`{"repositories":[
			{"repository":"api","title":"Add a health endpoint",
			 "body":"It returns 200.","commit":"1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d"}]}`),
	}

	event, applicable, err := claude.New().ParseEvent(context.Background(), message)
	if err != nil {
		t.Fatalf("parsing a publication draft: %v", err)
	}
	if !applicable {
		t.Fatal("a publication draft was reported as a message this build does not act on")
	}
	if event.Kind != agent.KindPublicationDraft {
		t.Errorf("kind = %q, want %q", event.Kind, agent.KindPublicationDraft)
	}
	if event.Draft == nil {
		t.Fatal("the event carries no draft, and the draft is what the message is for")
	}
	entry, found := event.Draft.Repository("api")
	if !found || entry.Title != "Add a health endpoint" {
		t.Errorf("the parsed draft is %+v", event.Draft)
	}
	if !strings.Contains(event.Summary, "api") {
		t.Errorf("summary = %q, want it to name the repository", event.Summary)
	}
	if strings.Contains(event.Summary, "Add a health endpoint") ||
		strings.Contains(event.Summary, "It returns 200") {
		t.Errorf("summary = %q, and the agent's prose must not reach the event log", event.Summary)
	}
}

// TestADraftThisProtocolRefusesIsTheAgentsMistake checks that a bad payload
// comes back as a rejection, which is settled once rather than retried for ever.
func TestADraftThisProtocolRefusesIsTheAgentsMistake(t *testing.T) {
	message := control.Message{
		SchemaVersion: control.SchemaVersion,
		ID:            "draft",
		TaskID:        testTask,
		Type:          control.TypePublicationDraft,
		OccurredAt:    time.Now(),
		Payload:       []byte(`{"repositories":[{"repository":"api","title":"t","commit":"nope"}]}`),
	}

	_, _, err := claude.New().ParseEvent(context.Background(), message)
	if err == nil {
		t.Fatal("a draft describing no commit was accepted")
	}
	if !strings.Contains(err.Error(), "not a full commit") {
		t.Errorf("the refusal is %q", err)
	}
}

// reportHelper returns the generated helper script.
func reportHelper(t *testing.T, workspace *control.Workspace) string {
	t.Helper()

	body, err := os.ReadFile(filepath.Join(workspace.AgentDir(), "bin", "feat-report"))
	if err != nil {
		t.Fatalf("reading the generated helper: %v", err)
	}
	return string(body)
}
