package ui

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ma8el/feat/internal/api"
)

// fakeBackend answers the dashboard from fixtures and records what it was
// asked to do.
//
// The point of the Backend interface is that a screen's behaviour can be
// checked without a socket, a daemon, or tmux. The counters below are what
// makes the strongest assertion available: not that no worktree exists, but
// that nothing was ever asked to create one.
type fakeBackend struct {
	mu sync.Mutex

	projects []api.Project
	tasks    []api.Task

	created   []api.CreateDraft
	updated   []api.UpdateDraft
	planned   int
	launched  []string
	cancelled []string
	attached  []string
	shells    []string
	edited    []string
	// runtimeCalls records every runtime action, so a test can assert that a
	// screen asked for exactly what the user pressed — and, more importantly,
	// that nothing else ever asked for one.
	runtimeCalls []string
	logs         []string

	runtimeStatus api.RuntimeStatus
	runtimeErr    error

	resources   api.ResourceReport
	resourceErr error

	planErr   error
	launchErr error
	// fingerprint is what a plan reports, and what a launch is checked against.
	fingerprint string
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{
		projects: []api.Project{{
			ID:                "example",
			Name:              "Example",
			PrimaryRepository: "core",
			Repositories: []api.Repository{
				{ID: "core", DefaultAccess: "read_write"},
				{ID: "schema", DefaultAccess: "read_only"},
				{ID: "docs", DefaultAccess: "selectable"},
			},
		}},
		fingerprint: "f1e2d3c4",
	}
}

func (f *fakeBackend) Projects(context.Context) ([]api.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.projects, nil
}

func (f *fakeBackend) Tasks(context.Context) ([]api.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tasks, nil
}

// Events ends at once, so a test drives the model itself rather than racing a
// background stream. The dashboard treats an ended stream as a reason to keep
// re-reading, which is exactly the behaviour a model test wants to be able to
// run to completion.
func (f *fakeBackend) Events(context.Context, func(api.Event) error) error {
	return errors.New("this fake publishes no events")
}

// Resources returns whatever the test arranged, including nothing at all: a
// dashboard whose daemon has not sampled yet is a state every screen has to
// render, because it is the state of the first two seconds of every session.
func (f *fakeBackend) Resources(context.Context) (api.ResourceReport, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.resources, f.resourceErr
}

func (f *fakeBackend) CreateDraft(_ context.Context, request api.CreateDraft) (api.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, request)
	return api.Task{
		ID: "2c4e6a80-1b3d-4f52-8a7c-9e0d1f2a3b4c", Key: "2c4e6a80",
		ProjectID: request.ProjectID, Title: request.Title, Brief: request.Brief,
		Source: request.Source, Workflow: "draft",
	}, nil
}

func (f *fakeBackend) UpdateDraft(_ context.Context, id string, request api.UpdateDraft) (api.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updated = append(f.updated, request)
	return api.Task{ID: id, Key: "2c4e6a80", Title: request.Title, Brief: request.Brief, Workflow: "draft"}, nil
}

func (f *fakeBackend) PlanDraft(_ context.Context, id string) (api.DraftPlan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.planned++
	if f.planErr != nil {
		return api.DraftPlan{}, f.planErr
	}

	selection := api.UpdateDraft{}
	if len(f.updated) > 0 {
		selection = f.updated[len(f.updated)-1]
	}
	repositories := make([]api.TaskRepository, 0, len(selection.Repositories))
	for _, selected := range selection.Repositories {
		binding := api.TaskRepository{
			RepositoryID: selected.RepositoryID,
			Access:       selected.Access,
			BaseRef:      "origin/main",
			BaseCommit:   "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d",
			WorktreePath: "/srv/worktrees/example/2c4e6a80/" + selected.RepositoryID,
		}
		if selected.Access == "read_write" {
			binding.Branch = "feat/2c4e6a80-" + selected.RepositoryID
		}
		repositories = append(repositories, binding)
	}

	return api.DraftPlan{
		Task: api.Task{
			ID: id, Key: "2c4e6a80", ProjectID: "example",
			Title: selection.Title, Brief: selection.Brief,
			Workflow: "draft", Repositories: repositories,
		},
		Notes:       []string{"fetching origin failed, so the base is resolved from the last fetched state"},
		Fingerprint: f.fingerprint,
	}, nil
}

func (f *fakeBackend) LaunchDraft(_ context.Context, id, fingerprint string) (api.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.launchErr != nil {
		return api.Task{}, f.launchErr
	}
	if fingerprint != f.fingerprint {
		return api.Task{}, errors.New("the draft changed after the plan you confirmed was displayed")
	}
	f.launched = append(f.launched, fingerprint)
	return api.Task{ID: id, Key: "2c4e6a80", Title: "Add a rate limit", Workflow: "preparing"}, nil
}

func (f *fakeBackend) CancelDraft(_ context.Context, id string) (api.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelled = append(f.cancelled, id)
	return api.Task{ID: id, Workflow: "archived"}, nil
}

func (f *fakeBackend) AttachCommand(_ context.Context, id string) (tea.ExecCommand, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attached = append(f.attached, id)
	return noopCommand{}, nil
}

func (f *fakeBackend) ShellCommand(_ context.Context, id string) (tea.ExecCommand, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.shells = append(f.shells, id)
	return noopCommand{}, nil
}

// Runtime records the action and answers with whatever the test arranged, so a
// screen's behaviour is checked without Docker.
func (f *fakeBackend) Runtime(_ context.Context, id string, action api.RuntimeAction) (api.RuntimeStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runtimeCalls = append(f.runtimeCalls, string(action)+" "+id)

	if f.runtimeErr != nil {
		return api.RuntimeStatus{}, f.runtimeErr
	}
	return f.runtimeStatus, nil
}

func (f *fakeBackend) LogsCommand(_ context.Context, id string) (tea.ExecCommand, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logs = append(f.logs, id)
	return noopCommand{}, nil
}

func (f *fakeBackend) EditorCommand(path string) (tea.ExecCommand, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.edited = append(f.edited, path)
	return noopCommand{}, nil
}

// noopCommand stands in for a program that would take over the terminal.
type noopCommand struct{}

func (noopCommand) Run() error            { return nil }
func (noopCommand) SetStdin(_ io.Reader)  {}
func (noopCommand) SetStdout(_ io.Writer) {}
func (noopCommand) SetStderr(_ io.Writer) {}

// drive applies messages to a preparation model, running each returned command
// and feeding the message back, until nothing is left.
//
// Bubble Tea would do this in its event loop; doing it here keeps the test
// deterministic and single-threaded.
func drive(t *testing.T, model prepareModel, messages ...tea.Msg) prepareModel {
	t.Helper()

	for _, message := range messages {
		updated, cmd := model.Update(message)
		model = updated
		model = settle(t, model, cmd)
	}
	return model
}

// settle runs a command and applies whatever it produces, following the chain.
func settle(t *testing.T, model prepareModel, cmd tea.Cmd) prepareModel {
	t.Helper()

	for depth := 0; cmd != nil && depth < 16; depth++ {
		message := cmd()
		if message == nil {
			return model
		}
		// A prepared message ends preparation; the root model handles it.
		if _, done := message.(preparedMsg); done {
			return model
		}
		updated, next := model.Update(message)
		model, cmd = updated, next
	}
	return model
}

func key(name string) tea.KeyMsg {
	switch name {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "ctrl+s":
		return tea.KeyMsg{Type: tea.KeyCtrlS}
	case "ctrl+e":
		return tea.KeyMsg{Type: tea.KeyCtrlE}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "space":
		return tea.KeyMsg{Type: tea.KeySpace}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(name)}
	}
}

// prepared returns a preparation model that has reached the review screen.
func prepared(t *testing.T, backend *fakeBackend) prepareModel {
	t.Helper()

	model := newPrepare(backend, "example", "", api.Source{Kind: "prompt"})
	model = settle(t, model, model.Init())
	if model.step != stepBrief {
		t.Fatalf("step = %d, want the brief with one project registered", model.step)
	}

	model.title.SetValue("Add a rate limit")
	model.brief.SetValue("Add a rate limit to the public API.")

	model = drive(t, model, key("ctrl+s"))
	if model.step != stepRepositories {
		t.Fatalf("step = %d, want the repository selection: %v", model.step, model.err)
	}

	model = drive(t, model, key("enter"))
	if model.step != stepReview {
		t.Fatalf("step = %d, want the review screen: %v", model.step, model.err)
	}
	return model
}

// TestNothingIsCreatedBeforeTheUserConfirms is FR-TASK-003 at the screen that
// implements it.
//
// Everything up to the review screen may read and resolve; only the
// confirmation may create. The assertion is that no launch was requested, which
// is the only way a worktree, a branch, or a terminal can appear.
func TestNothingIsCreatedBeforeTheUserConfirms(t *testing.T) {
	backend := newFakeBackend()
	model := prepared(t, backend)

	if len(backend.launched) != 0 {
		t.Error("preparation launched a task before the user confirmed it")
	}
	if backend.planned != 1 {
		t.Errorf("the draft was resolved %d times, want once", backend.planned)
	}
	if len(backend.created) != 1 {
		t.Fatalf("the draft was recorded %d times, want once", len(backend.created))
	}

	// The review screen shows what confirming would create, which is what the
	// user is being asked about.
	view := model.View()
	for _, want := range []string{
		"1a2b3c4d5e6f", "origin/main", "feat/2c4e6a80-core",
		"/srv/worktrees/example/2c4e6a80/core", "nothing above exists yet",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the review screen does not show %q:\n%s", want, view)
		}
	}
}

// TestConfirmingCarriesTheDisplayedFingerprint checks that the confirmation the
// user gives is tied to the plan they read (ADR-031).
func TestConfirmingCarriesTheDisplayedFingerprint(t *testing.T) {
	backend := newFakeBackend()
	model := prepared(t, backend)

	_, cmd := model.Update(key("enter"))
	message := runTo[preparedMsg](t, cmd)

	if message.err != nil {
		t.Fatalf("confirming failed: %v", message.err)
	}
	if len(backend.launched) != 1 || backend.launched[0] != backend.fingerprint {
		t.Errorf("launched with %v, want the fingerprint the review screen displayed", backend.launched)
	}
}

// TestCancellingPreparationCreatesNothing is the slice 6 acceptance criterion
// at the screen the user cancels from.
func TestCancellingPreparationCreatesNothing(t *testing.T) {
	backend := newFakeBackend()
	model := prepared(t, backend)

	_, cmd := model.Update(key("ctrl+c"))
	message := runTo[preparedMsg](t, cmd)

	if message.err != nil {
		t.Fatalf("cancelling failed: %v", message.err)
	}
	if message.task != nil {
		t.Error("cancelling reported a launched task")
	}
	if len(backend.launched) != 0 {
		t.Error("cancelling launched a task")
	}
	if len(backend.cancelled) != 1 {
		t.Errorf("the draft was cancelled %d times, want once", len(backend.cancelled))
	}
}

// TestAReadOnlyRepositoryCannotBeCycledToReadWrite checks that the selection
// screen offers only what the project's configuration allows.
//
// The daemon refuses a promotion as well, but a screen that offered it would be
// showing the user a choice Feat was never going to honour.
func TestAReadOnlyRepositoryCannotBeCycledToReadWrite(t *testing.T) {
	backend := newFakeBackend()

	model := newPrepare(backend, "example", "", api.Source{Kind: "prompt"})
	model = settle(t, model, model.Init())
	model.title.SetValue("Add a rate limit")
	model.brief.SetValue("Add a rate limit to the public API.")
	model = drive(t, model, key("ctrl+s"))

	var readOnly int
	for i, row := range model.selection {
		if row.repository == "schema" {
			readOnly = i
		}
	}
	model.cursor = readOnly

	// Cycling through every mode must never reach read-write.
	for range 6 {
		model.cycle(1)
		if model.selection[readOnly].access == "read_write" {
			t.Fatal("a repository the project configures read-only was offered as read-write")
		}
	}

	// A selectable repository, by contrast, may be promoted: the project left
	// the choice to the task.
	for i, row := range model.selection {
		if row.repository != "docs" {
			continue
		}
		model.cursor = i
		var promoted bool
		for range 6 {
			model.cycle(1)
			if model.selection[i].access == "read_write" {
				promoted = true
			}
		}
		if !promoted {
			t.Error("a selectable repository could not be selected as read-write")
		}
	}
}

// TestAnImportedBriefSuppliesTheTitle checks the Markdown import path.
func TestAnImportedBriefSuppliesTheTitle(t *testing.T) {
	backend := newFakeBackend()
	brief := "# Retry failed exports\n\nRetry three times before giving up.\n"

	model := newPrepare(backend, "example", brief,
		api.Source{Kind: "markdown", Reference: "/srv/notes/task.md"})

	if got := model.title.Value(); got != "Retry failed exports" {
		t.Errorf("title = %q, want the brief's first heading", got)
	}
	if got := model.brief.Value(); got != brief {
		t.Errorf("brief = %q, want the imported document unchanged", got)
	}

	model = settle(t, model, model.Init())
	if !strings.Contains(model.View(), "task.md") {
		t.Errorf("the brief screen does not say where the brief came from:\n%s", model.View())
	}
}

// TestPreparationNeedsARepository checks that a task cannot be resolved with
// nothing selected.
func TestPreparationNeedsARepository(t *testing.T) {
	backend := newFakeBackend()

	model := newPrepare(backend, "example", "", api.Source{Kind: "prompt"})
	model = settle(t, model, model.Init())
	model.title.SetValue("Add a rate limit")
	model.brief.SetValue("Add a rate limit to the public API.")
	model = drive(t, model, key("ctrl+s"))

	for i := range model.selection {
		model.selection[i].access = "omitted"
	}

	model = drive(t, model, key("enter"))
	if model.step != stepRepositories {
		t.Errorf("step = %d, want to stay on the selection", model.step)
	}
	if model.err == nil || !strings.Contains(model.err.Error(), "at least one repository") {
		t.Errorf("error = %v, want one saying a repository is needed", model.err)
	}
	if backend.planned != 0 {
		t.Error("an empty selection was resolved")
	}
}

// TestAFailedResolutionKeepsTheDraftEditable checks the path a user meets
// first: a branch that already exists, a checkout that has moved, a remote that
// cannot be reached.
//
// Resolving creates nothing, so a draft whose plan does not hold is still a
// draft. The screen has to say what went wrong and leave the user where they
// can change it, rather than stranding them on a review screen with no plan.
func TestAFailedResolutionKeepsTheDraftEditable(t *testing.T) {
	backend := newFakeBackend()
	backend.planErr = errors.New("branch feat/2c4e6a80-core already exists in /srv/repositories/core")

	model := newPrepare(backend, "example", "", api.Source{Kind: "prompt"})
	model = settle(t, model, model.Init())
	model.title.SetValue("Add a rate limit")
	model.brief.SetValue("Add a rate limit to the public API.")
	model = drive(t, model, key("ctrl+s"), key("enter"))

	if model.step != stepRepositories {
		t.Errorf("step = %d, want the selection the user can still change", model.step)
	}
	if model.err == nil || !strings.Contains(model.err.Error(), "already exists") {
		t.Errorf("error = %v, want the reason the plan does not hold", model.err)
	}
	if model.busy {
		t.Error("the screen is still waiting after the resolution failed")
	}
	if len(backend.launched) != 0 {
		t.Error("a draft that could not be resolved was launched")
	}

	// Confirming is unreachable without a plan, so the failure cannot be
	// stepped past.
	model.step = stepReview
	model = drive(t, model, key("enter"))
	if len(backend.launched) != 0 {
		t.Error("a draft with no plan was launched from the review screen")
	}
	if model.err == nil || !strings.Contains(model.err.Error(), "resolve the draft") {
		t.Errorf("error = %v, want one saying the draft must be resolved first", model.err)
	}
}

// TestEditingTheBriefHandsTheCurrentTextToTheEditor checks the $EDITOR handoff.
//
// Reading the file back and removing it happen in the callback Bubble Tea runs
// once the editor exits, which a model test cannot invoke. What it can check is
// the half that decides whether the handoff is useful at all: the editor is
// given a Markdown file holding what the user has written so far, rather than an
// empty one that would discard it.
func TestEditingTheBriefHandsTheCurrentTextToTheEditor(t *testing.T) {
	backend := newFakeBackend()
	const draft = "a first draft\n\n- with a list\n"

	model := newPrepare(backend, "example", "", api.Source{Kind: "prompt"})
	model = settle(t, model, model.Init())
	model.brief.SetValue(draft)
	model.focus = 1

	model = drive(t, model, key("ctrl+e"))

	if len(backend.edited) != 1 {
		t.Fatalf("the editor was asked for %d files, want one", len(backend.edited))
	}
	path := backend.edited[0]
	defer func() { _ = os.Remove(path) }()

	if !strings.HasSuffix(path, ".md") {
		t.Errorf("the editor was given %q, want a Markdown file", path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading what the editor was given: %v", err)
	}
	if string(content) != draft {
		t.Errorf("the editor was given %q, want the brief so far %q", content, draft)
	}

	// And the round trip lands back in the brief.
	model = drive(t, model, editedMsg{brief: "the edited brief"})
	if got := model.brief.Value(); got != "the edited brief" {
		t.Errorf("brief = %q, want what came back from the editor", got)
	}
}

// TestABriefIsRequiredBeforeSelectingRepositories checks that the agent is
// never launched with nothing to do.
func TestABriefIsRequiredBeforeSelectingRepositories(t *testing.T) {
	backend := newFakeBackend()

	model := newPrepare(backend, "example", "", api.Source{Kind: "prompt"})
	model = settle(t, model, model.Init())
	model.title.SetValue("Add a rate limit")

	model = drive(t, model, key("ctrl+s"))
	if model.step != stepBrief {
		t.Errorf("step = %d, want to stay on the brief", model.step)
	}
	if model.err == nil || !strings.Contains(model.err.Error(), "brief") {
		t.Errorf("error = %v, want one naming the missing brief", model.err)
	}
}

// runTo runs a command chain until it produces a message of the wanted type.
func runTo[T tea.Msg](t *testing.T, cmd tea.Cmd) T {
	t.Helper()

	var zero T
	for depth := 0; cmd != nil && depth < 8; depth++ {
		message := cmd()
		if wanted, ok := message.(T); ok {
			return wanted
		}
		// A command that returned another command is followed; anything else
		// means the chain ended without producing what was wanted.
		next, chained := message.(tea.Cmd)
		if !chained {
			break
		}
		cmd = next
	}
	t.Fatalf("no %T was produced", zero)
	return zero
}

// TestDurationsAreReadable pins the elapsed-time rendering the task row uses.
func TestDurationsAreReadable(t *testing.T) {
	tests := []struct {
		span time.Duration
		want string
	}{
		{30 * time.Second, "30s"},
		{90 * time.Second, "1m"},
		{2 * time.Hour, "2h0m"},
		{26 * time.Hour, "1d2h"},
		{-time.Second, "0s"},
	}
	for _, test := range tests {
		if got := duration(test.span); got != test.want {
			t.Errorf("duration(%s) = %q, want %q", test.span, got, test.want)
		}
	}
}
