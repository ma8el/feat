package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/ma8el/feat/internal/api"
)

// recordingDrafter is a daemon that records what a run asked it to do.
//
// It is what the drafter interface exists for: what creating a task from a
// command line is mostly made of is the order of four requests and what happens
// when one of them fails, and neither needs a socket to check.
type recordingDrafter struct {
	project api.Project

	created   *api.CreateDraft
	updated   *api.UpdateDraft
	planned   bool
	confirmed *api.Confirmation
	cancelled bool

	fingerprint string
	notes       []string

	projectErr error
	createErr  error
	updateErr  error
	planErr    error
	launchErr  error
	cancelErr  error
}

func (d *recordingDrafter) Project(context.Context, string) (api.Project, error) {
	return d.project, d.projectErr
}

func (d *recordingDrafter) CreateDraft(_ context.Context, request api.CreateDraft) (api.Task, error) {
	d.created = &request
	if d.createErr != nil {
		return api.Task{}, d.createErr
	}
	return d.draft(request.Title), nil
}

func (d *recordingDrafter) UpdateDraft(_ context.Context, _ string, request api.UpdateDraft) (api.Task, error) {
	d.updated = &request
	if d.updateErr != nil {
		return api.Task{}, d.updateErr
	}
	return d.draft(request.Title), nil
}

func (d *recordingDrafter) PlanDraft(context.Context, string) (api.DraftPlan, error) {
	d.planned = true
	if d.planErr != nil {
		return api.DraftPlan{}, d.planErr
	}
	resolved := d.draft(d.title())
	resolved.Repositories = []api.TaskRepository{{
		RepositoryID: "core", Access: "read_write",
		BaseCommit:   "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d",
		Branch:       "feat/2c4e6a80-add-a-retry",
		WorktreePath: "/srv/worktrees/app/2c4e6a80/core",
	}}
	return api.DraftPlan{Task: resolved, Notes: d.notes, Fingerprint: d.fingerprint}, nil
}

func (d *recordingDrafter) LaunchDraft(
	_ context.Context, _ string, confirmation api.Confirmation,
) (api.Task, error) {
	d.confirmed = &confirmation
	if d.launchErr != nil {
		return api.Task{}, d.launchErr
	}
	launched := d.draft(d.title())
	launched.Workflow = "preparing"
	launched.Repositories = []api.TaskRepository{{
		RepositoryID: "core", Access: "read_write",
		BaseCommit:   "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d",
		Branch:       "feat/2c4e6a80-add-a-retry",
		WorktreePath: "/srv/worktrees/app/2c4e6a80/core",
	}}
	return launched, nil
}

func (d *recordingDrafter) CancelDraft(context.Context, string) (api.Task, error) {
	d.cancelled = true
	if d.cancelErr != nil {
		return api.Task{}, d.cancelErr
	}
	archived := d.draft(d.title())
	archived.Workflow = "archived"
	return archived, nil
}

func (d *recordingDrafter) draft(title string) api.Task {
	return api.Task{
		ID:        "2c4e6a80-1b3d-4f52-8a7c-9e0d1f2a3b4c",
		Key:       "2c4e6a80",
		ProjectID: "app",
		Title:     title,
		Workflow:  "draft",
	}
}

func (d *recordingDrafter) title() string {
	if d.updated != nil {
		return d.updated.Title
	}
	if d.created != nil {
		return d.created.Title
	}
	return ""
}

// newDrafter is a daemon that answers every request.
func newDrafter() *recordingDrafter {
	return &recordingDrafter{
		fingerprint: "7c1f9a0e2b",
		project: api.Project{
			ID: "app", PrimaryRepository: "core",
			Repositories: []api.Repository{
				{ID: "core", DefaultAccess: "read_write"},
				{ID: "docs", DefaultAccess: "read_only"},
				{ID: "infra", DefaultAccess: "selectable"},
			},
		},
	}
}

// runCreate drives one headless run against a recorded daemon.
func runCreate(t *testing.T, caller drafter, opts implementOptions, document string) (string, error) {
	t.Helper()

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	err := createTask(cmd, caller, opts, document, api.Source{Kind: "prompt"})
	return out.String(), err
}

// TestACompleteInvocationCreatesTheTaskItDescribes is the whole point of the
// headless path: a project and a brief are enough, and what comes back names
// the task that now exists.
func TestACompleteInvocationCreatesTheTaskItDescribes(t *testing.T) {
	caller := newDrafter()

	printed, err := runCreate(t, caller,
		implementOptions{project: "app", brief: "# Add a retry\n\nTo the export job.\n"},
		"# Add a retry\n\nTo the export job.\n")
	if err != nil {
		t.Fatalf("creating a task: %v", err)
	}

	if caller.created == nil {
		t.Fatal("no draft was recorded")
	}
	if caller.created.ProjectID != "app" {
		t.Errorf("project = %q, want app", caller.created.ProjectID)
	}
	// The title is the document's own name, so a caller who passed no title has
	// one derived from words they wrote.
	if caller.created.Title != "Add a retry" {
		t.Errorf("title = %q, want the brief's own heading", caller.created.Title)
	}
	if !caller.planned {
		t.Error("the draft was launched without being resolved")
	}
	if caller.confirmed == nil {
		t.Fatal("the draft was never confirmed")
	}
	if caller.confirmed.Fingerprint != caller.fingerprint {
		t.Errorf("confirmed with %q, want the fingerprint the plan returned", caller.confirmed.Fingerprint)
	}
	if caller.cancelled {
		t.Error("a run that created a task also discarded its draft")
	}

	for _, required := range []string{"created task 2c4e6a80", "core", "1a2b3c4d5e6f", "feat attach 2c4e6a80"} {
		if !strings.Contains(printed, required) {
			t.Errorf("the output does not mention %q:\n%s", required, printed)
		}
	}
}

// TestTheConfirmationIsTheFingerprintTheRunWasJustGiven is ADR-031 kept
// literally rather than by analogy. The value carried back is the plan's own, so
// a draft that moved underneath this run is refused by the daemon rather than
// launched.
func TestTheConfirmationIsTheFingerprintTheRunWasJustGiven(t *testing.T) {
	caller := newDrafter()
	caller.fingerprint = "a-different-digest"

	if _, err := runCreate(t, caller,
		implementOptions{project: "app", brief: "Do the thing."}, "Do the thing."); err != nil {
		t.Fatalf("creating a task: %v", err)
	}
	if caller.confirmed.Fingerprint != "a-different-digest" {
		t.Errorf("confirmed with %q, want the plan's own fingerprint", caller.confirmed.Fingerprint)
	}
}

// TestPlanModeTravelsWithTheConfirmation checks the one decision that is
// deliberately not part of the digest: a value carried in the request that
// confirms cannot have drifted since it was displayed.
func TestPlanModeTravelsWithTheConfirmation(t *testing.T) {
	caller := newDrafter()

	if _, err := runCreate(t, caller,
		implementOptions{project: "app", brief: "Do the thing.", planFirst: true}, "Do the thing."); err != nil {
		t.Fatalf("creating a task: %v", err)
	}
	if !caller.confirmed.PlanFirst {
		t.Error("--plan did not reach the confirmation")
	}
}

// TestADryRunCreatesNothing is what --dry-run promises. The plan is printed and
// the draft it was resolved on is discarded, so a rehearsal leaves no row in
// `feat task list`.
func TestADryRunCreatesNothing(t *testing.T) {
	caller := newDrafter()
	caller.notes = []string{"core: fetch failed, using the remote-tracking ref"}

	printed, err := runCreate(t, caller,
		implementOptions{project: "app", brief: "Do the thing.", dryRun: true}, "Do the thing.")
	if err != nil {
		t.Fatalf("rehearsing a task: %v", err)
	}

	if !caller.planned {
		t.Error("a dry run did not resolve the draft")
	}
	if caller.confirmed != nil {
		t.Error("a dry run launched the task")
	}
	if !caller.cancelled {
		t.Error("a dry run left its draft behind")
	}
	for _, required := range []string{"would create in project app", "nothing was created", "fetch failed"} {
		if !strings.Contains(printed, required) {
			t.Errorf("the output does not mention %q:\n%s", required, printed)
		}
	}
}

// TestADryRunThatCannotDiscardItsDraftSaysSoInsteadOfPrinting keeps the rule
// that standard output carries a document or nothing.
//
// The draft is discarded before anything is printed, so a run that could not
// discard it reports where the draft is rather than printing a plan and then
// failing.
func TestADryRunThatCannotDiscardItsDraftSaysSoInsteadOfPrinting(t *testing.T) {
	caller := newDrafter()
	caller.cancelErr = errors.New("the daemon stopped answering")

	printed, err := runCreate(t, caller,
		implementOptions{project: "app", brief: "Do the thing.", dryRun: true, asJSON: true}, "Do the thing.")
	if err == nil {
		t.Fatal("a dry run that could not discard its draft reported success")
	}
	if printed != "" {
		t.Errorf("a failed run printed this:\n%s", printed)
	}
	if !strings.Contains(err.Error(), "2c4e6a80") {
		t.Errorf("the error does not name the draft that is still there: %v", err)
	}
}

// TestAPlanThatDoesNotHoldLeavesNoTaskBehind.
//
// Planning creates nothing, so the draft owns nothing, and a headless run has no
// screen to go back to and edit it on. Leaving it would put a row in
// `feat task list` for every failed attempt — and the command that removes one
// asks per class of resource and needs a terminal, so a script could not clear
// them up afterwards.
func TestAPlanThatDoesNotHoldLeavesNoTaskBehind(t *testing.T) {
	caller := newDrafter()
	caller.planErr = errors.New("core: no such ref main")

	_, err := runCreate(t, caller,
		implementOptions{project: "app", brief: "Do the thing."}, "Do the thing.")
	if err == nil {
		t.Fatal("a failed plan reported success")
	}
	if caller.confirmed != nil {
		t.Error("a draft that could not be resolved was launched anyway")
	}
	if !caller.cancelled {
		t.Error("a draft that could not be resolved was left in the list")
	}
	for _, required := range []string{"no task was created", "no such ref"} {
		if !strings.Contains(err.Error(), required) {
			t.Errorf("the error does not mention %q: %v", required, err)
		}
	}
}

// TestADraftThatCannotBeDiscardedIsNamed, because then it is there and only the
// user can reach it.
func TestADraftThatCannotBeDiscardedIsNamed(t *testing.T) {
	caller := newDrafter()
	caller.planErr = errors.New("core: no such ref main")
	caller.cancelErr = errors.New("the daemon stopped answering")

	_, err := runCreate(t, caller,
		implementOptions{project: "app", brief: "Do the thing."}, "Do the thing.")
	if err == nil {
		t.Fatal("a failed plan reported success")
	}
	for _, required := range []string{"2c4e6a80", "no such ref", "stopped answering", "--tui"} {
		if !strings.Contains(err.Error(), required) {
			t.Errorf("the error does not mention %q: %v", required, err)
		}
	}
}

// TestALaunchFailureNamesTheTask so that a script that got half a task can find
// it. Nothing is undone: the worktrees a launch created are still there, and the
// record names a superset of what exists (ADR-029).
func TestALaunchFailureNamesTheTask(t *testing.T) {
	caller := newDrafter()
	caller.launchErr = errors.New("tmux: no server running")

	_, err := runCreate(t, caller,
		implementOptions{project: "app", brief: "Do the thing."}, "Do the thing.")
	if err == nil {
		t.Fatal("a failed launch reported success")
	}
	if !strings.Contains(err.Error(), "2c4e6a80") {
		t.Errorf("the error does not name the task: %v", err)
	}
}

// TestADocumentIsPrintedWhenOneIsAskedFor checks the shape a script reads, and
// that the table does not come with it.
func TestADocumentIsPrintedWhenOneIsAskedFor(t *testing.T) {
	caller := newDrafter()

	printed, err := runCreate(t, caller,
		implementOptions{project: "app", brief: "Do the thing.", asJSON: true}, "Do the thing.")
	if err != nil {
		t.Fatalf("creating a task: %v", err)
	}

	var task api.Task
	if err := json.Unmarshal([]byte(printed), &task); err != nil {
		t.Fatalf("the document does not parse: %v\n%s", err, printed)
	}
	if task.Key != "2c4e6a80" {
		t.Errorf("key = %q, want the created task's", task.Key)
	}
	if strings.Contains(printed, "attach with") {
		t.Errorf("the document came with the table:\n%s", printed)
	}
}

// TestADryRunPrintsThePlanAsADocument checks the other half: what a caller reads
// to decide is the resolved plan, fingerprint and all.
func TestADryRunPrintsThePlanAsADocument(t *testing.T) {
	caller := newDrafter()

	printed, err := runCreate(t, caller,
		implementOptions{project: "app", brief: "Do the thing.", dryRun: true, asJSON: true}, "Do the thing.")
	if err != nil {
		t.Fatalf("rehearsing a task: %v", err)
	}

	var plan api.DraftPlan
	if err := json.Unmarshal([]byte(printed), &plan); err != nil {
		t.Fatalf("the document does not parse: %v\n%s", err, printed)
	}
	if plan.Fingerprint != caller.fingerprint {
		t.Errorf("fingerprint = %q, want the plan's own", plan.Fingerprint)
	}
	if len(plan.Task.Repositories) != 1 {
		t.Errorf("the plan describes %d repositories, want the one it resolved", len(plan.Task.Repositories))
	}
}

// TestASelectionReplacesTheProjectsDefaults checks --repository end to end: the
// explicit form is taken as given, and a bare identifier takes what the project
// configured.
func TestASelectionReplacesTheProjectsDefaults(t *testing.T) {
	caller := newDrafter()

	if _, err := runCreate(t, caller, implementOptions{
		project:      "app",
		brief:        "Do the thing.",
		repositories: []string{"core", "docs:read_write"},
	}, "Do the thing."); err != nil {
		t.Fatalf("creating a task: %v", err)
	}

	if caller.updated == nil {
		t.Fatal("the selection never reached the draft")
	}
	want := []api.DraftRepository{
		{RepositoryID: "core", Access: "read_write"},
		{RepositoryID: "docs", Access: "read_write"},
	}
	if len(caller.updated.Repositories) != len(want) {
		t.Fatalf("selected %d repositories, want %d", len(caller.updated.Repositories), len(want))
	}
	for i, selected := range caller.updated.Repositories {
		if selected != want[i] {
			t.Errorf("selection %d is %+v, want %+v", i, selected, want[i])
		}
	}
	// The update replaces the draft's whole editable shape, so what it carries
	// back has to be the brief that was just recorded.
	if caller.updated.Brief != "Do the thing." {
		t.Errorf("the update replaced the brief with %q", caller.updated.Brief)
	}
}

// TestNoSelectionLeavesTheProjectsDefaultsAlone: a run that asks for no
// repositories gets what the project configured, which CreateDraft already
// recorded. Sending an update would be replacing a selection with itself.
func TestNoSelectionLeavesTheProjectsDefaultsAlone(t *testing.T) {
	caller := newDrafter()

	if _, err := runCreate(t, caller,
		implementOptions{project: "app", brief: "Do the thing."}, "Do the thing."); err != nil {
		t.Fatalf("creating a task: %v", err)
	}
	if caller.updated != nil {
		t.Errorf("a run that selected nothing replaced the draft's selection with %+v", caller.updated)
	}
}

// TestARepositoryTheProjectLeavesOpenIsRefusedRatherThanChosen.
//
// selectable, stable_read_only, and omitted are not task accesses: each is the
// project saying the choice belongs to the task. Picking one on the caller's
// behalf would be Feat making a decision the configuration deliberately left.
func TestARepositoryTheProjectLeavesOpenIsRefusedRatherThanChosen(t *testing.T) {
	caller := newDrafter()

	_, err := runCreate(t, caller, implementOptions{
		project: "app", brief: "Do the thing.", repositories: []string{"infra"},
	}, "Do the thing.")
	if err == nil {
		t.Fatal("a repository the project leaves open was selected anyway")
	}
	for _, required := range []string{"selectable", "infra:read_write", "infra:read_only"} {
		if !strings.Contains(err.Error(), required) {
			t.Errorf("the error does not mention %q: %v", required, err)
		}
	}
	if caller.created != nil {
		t.Error("a draft was recorded before the selection was understood")
	}
}

// TestAnUnknownRepositoryIsRefusedBeforeADraftExists, so that a typo does not
// leave a record behind.
func TestAnUnknownRepositoryIsRefusedBeforeADraftExists(t *testing.T) {
	caller := newDrafter()

	_, err := runCreate(t, caller, implementOptions{
		project: "app", brief: "Do the thing.", repositories: []string{"nope"},
	}, "Do the thing.")
	if err == nil {
		t.Fatal("an unknown repository was selected")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Errorf("the error does not name the repository: %v", err)
	}
	if caller.created != nil {
		t.Error("a draft was recorded for a repository the project does not have")
	}
}

// TestAnAccessThatIsNotOneIsRefusedWithTheTwoThatAre.
func TestAnAccessThatIsNotOneIsRefusedWithTheTwoThatAre(t *testing.T) {
	caller := newDrafter()

	_, err := runCreate(t, caller, implementOptions{
		project: "app", brief: "Do the thing.", repositories: []string{"core:write"},
	}, "Do the thing.")
	if err == nil {
		t.Fatal("an access that is not one was accepted")
	}
	for _, required := range []string{"read_write", "read_only"} {
		if !strings.Contains(err.Error(), required) {
			t.Errorf("the error does not name %q: %v", required, err)
		}
	}
}

// TestWhatOneInvocationMeans pins the rule the command is built on: an
// invocation that says what the task is creates it, and one that does not opens
// the screen with whatever it was given.
func TestWhatOneInvocationMeans(t *testing.T) {
	for _, test := range []struct {
		name     string
		opts     implementOptions
		complete bool
	}{
		{name: "project and brief", opts: implementOptions{project: "app", brief: "do it"}, complete: true},
		{name: "project and file", opts: implementOptions{project: "app", file: "brief.md"}, complete: true},
		{name: "project alone", opts: implementOptions{project: "app"}},
		{name: "brief alone", opts: implementOptions{brief: "do it"}},
		{name: "nothing", opts: implementOptions{}},
		// A ticket is a document somebody else may have written, and what the
		// user approves is the brief composed from it (ADR-070). It never
		// completes an invocation.
		{name: "project and ticket", opts: implementOptions{project: "app", ticket: "ABC-1"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := test.opts.complete(); got != test.complete {
				t.Errorf("complete() = %v, want %v", got, test.complete)
			}
		})
	}
}

// TestAnInvocationThatCannotMeanWhatItSaysIsRefused covers the combinations
// where carrying on would discard or ignore something the user asked for.
func TestAnInvocationThatCannotMeanWhatItSaysIsRefused(t *testing.T) {
	for _, test := range []struct {
		name        string
		opts        implementOptions
		interactive bool
		want        string
	}{
		{
			name: "two brief sources",
			opts: implementOptions{project: "app", brief: "do it", file: "brief.md"},
			want: "one source",
		},
		{
			name: "a ticket and a brief",
			opts: implementOptions{project: "app", brief: "do it", ticket: "ABC-1"},
			want: "one source",
		},
		{
			name:        "a rehearsal of the screen",
			opts:        implementOptions{project: "app", brief: "do it", tui: true, dryRun: true},
			interactive: true,
			want:        "pass one",
		},
		{
			name:        "the screen without a terminal",
			opts:        implementOptions{project: "app", brief: "do it", tui: true},
			interactive: false,
			want:        "no terminal to open it in",
		},
		{
			name:        "a document from a run that prints none",
			opts:        implementOptions{project: "app", asJSON: true},
			interactive: true,
			want:        "--json describes a document this run does not print",
		},
		{
			name:        "a rehearsal of nothing",
			opts:        implementOptions{project: "app", dryRun: true},
			interactive: true,
			want:        "--dry-run needs the task",
		},
		{
			name:        "a selection for no task",
			opts:        implementOptions{project: "app", repositories: []string{"core"}},
			interactive: true,
			want:        "--repository selects repositories",
		},
		{
			name:        "standard input and the screen",
			opts:        implementOptions{file: "-"},
			interactive: true,
			want:        "reads your keys from",
		},
		{
			name: "a malformed project",
			opts: implementOptions{project: "../elsewhere", brief: "do it"},
			want: "project",
		},
		{
			name:        "no terminal and nothing to create",
			opts:        implementOptions{},
			interactive: false,
			want:        "needs a terminal to compose a task",
		},
		{
			name:        "a ticket without a terminal",
			opts:        implementOptions{project: "app", ticket: "ABC-1"},
			interactive: false,
			want:        "the brief Feat composed from the ticket",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := test.opts.check(test.interactive)
			if err == nil {
				t.Fatalf("the invocation was accepted")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Errorf("error = %q, want it to mention %q", err, test.want)
			}
		})
	}
}

// TestAnInvocationThatMeansSomethingIsAccepted is the other half, so that the
// refusals above cannot quietly grow to cover a run somebody relies on.
func TestAnInvocationThatMeansSomethingIsAccepted(t *testing.T) {
	for _, test := range []struct {
		name        string
		opts        implementOptions
		interactive bool
	}{
		{name: "the screen, as it always was", interactive: true},
		{name: "the screen with a project", opts: implementOptions{project: "app"}, interactive: true},
		{
			name:        "the screen with a ticket",
			opts:        implementOptions{project: "app", ticket: "ABC-1"},
			interactive: true,
		},
		{name: "a brief with no terminal", opts: implementOptions{project: "app", brief: "do it"}},
		{name: "a piped brief", opts: implementOptions{project: "app", file: "-"}},
		{
			name: "a rehearsal with a document",
			opts: implementOptions{project: "app", brief: "do it", dryRun: true, asJSON: true},
		},
		{
			name:        "the screen, asked for explicitly",
			opts:        implementOptions{project: "app", brief: "do it", tui: true},
			interactive: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.opts.check(test.interactive); err != nil {
				t.Errorf("the invocation was refused: %v", err)
			}
		})
	}
}

// TestABriefFromAFlagOrAPipeIsRecordedAsOne: a brief the caller typed and one
// they piped are both text they supplied, and only a file has a path worth
// recording as where the brief came from.
func TestABriefFromAFlagOrAPipeIsRecordedAsOne(t *testing.T) {
	document, source, err := implementOptions{brief: "Do the thing."}.readBrief(strings.NewReader(""))
	if err != nil {
		t.Fatalf("reading a brief from a flag: %v", err)
	}
	if document != "Do the thing." || source.Kind != "prompt" || source.Reference != "" {
		t.Errorf("a brief from a flag is %q from %+v", document, source)
	}

	document, source, err = implementOptions{file: "-"}.readBrief(strings.NewReader("# Piped\n\nDo it.\n"))
	if err != nil {
		t.Fatalf("reading a brief from a pipe: %v", err)
	}
	if document != "# Piped\n\nDo it.\n" || source.Kind != "prompt" || source.Reference != "" {
		t.Errorf("a piped brief is %q from %+v", document, source)
	}
}

// TestAnEmptyPipeIsRefusedWhereTheUserCanSeeIt.
//
// The daemon refuses a task with no brief before it creates anything, and so
// does the screen. A pipe that held nothing is the same mistake made where
// there is no screen to say so.
func TestAnEmptyPipeIsRefusedWhereTheUserCanSeeIt(t *testing.T) {
	_, _, err := implementOptions{file: "-"}.readBrief(strings.NewReader("   \n\n"))
	if err == nil {
		t.Fatal("an empty pipe was accepted as a task brief")
	}
	if !strings.Contains(err.Error(), "standard input") {
		t.Errorf("the error does not say where the brief was read from: %v", err)
	}
}
