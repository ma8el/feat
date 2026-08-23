package ui

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/config"
	"github.com/ma8el/feat/internal/paths"
	"github.com/ma8el/feat/internal/wizard"
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
	// reviewCalls records every review action, for the same reason: approving a
	// task must reach the daemon exactly once and must reach nothing else.
	reviewCalls []string
	// reviewRan records every external command a screen ran, so a test can
	// assert which repository's tools were opened.
	reviewRan []api.ReviewCommand
	// wizards counts the flows the dashboard asked for, written the projects it
	// asked to write, and registered the ones it asked the daemon to record.
	// Their most important assertion is that they stay empty until the user has
	// answered the screen that offers each.
	wizards     int
	written     []string
	registered  []string
	root        string
	wizardErr   error
	writeErr    error
	registerErr error
	// diagnosed records every project the checks were asked about, and diagnosis
	// is what they answered with.
	diagnosed   []string
	diagnosis   api.Diagnosis
	diagnoseErr error

	// frames records every request for a rendered pane and inputs everything
	// sent to one, so a test can assert what reached the daemon rather than what
	// a key handler meant to send.
	frames []api.TerminalView
	// frameTasks records whose pane each of those requests asked for, in the
	// same order. The view alone cannot answer the question a frame arriving
	// under the wrong name raises, which is which task the dashboard asked
	// about.
	frameTasks []string
	inputs     []api.TerminalInput
	frame      api.TerminalFrame
	frameErr   error
	inputErr   error

	runtimeStatus api.RuntimeStatus
	runtimeErr    error

	reviewStatus api.ReviewStatus
	reviewErr    error

	// cleanupCalls records every plan request and cleanupSelections every
	// execution, so a test can assert that opening the screen removed nothing
	// and that what reached the daemon is what the user selected.
	cleanupCalls      []string
	cleanupSelections []api.CleanupSelection
	cleanupPlan       api.CleanupPlan
	cleanupStatus     api.CleanupStatus
	cleanupPlanErr    error
	cleanupErr        error
	// resumed records every task whose session was resumed. Its most important
	// assertion is that it stays empty.
	resumed []string
	// stopped records every task whose agent environment was stopped, and
	// stopErr is what the daemon answered. Both matter for the same reason
	// resumed does: a key that stops a working agent must reach the daemon only
	// when the user meant it to.
	stopped []string
	stopErr error
	// reconciled counts the passes the dashboard asked the daemon to run, and
	// reconciliation is what a read answers with.
	reconciled     int
	reconciliation api.Reconciliation

	resources   api.ResourceReport
	resourceErr error

	planErr   error
	launchErr error
	// fingerprint is what a plan reports, and what a launch is checked against.
	fingerprint string
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{
		root: filepath.Join(os.TempDir(), "feat-ui-test"),
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

// Review records the action and answers with whatever the test arranged.
func (f *fakeBackend) Review(_ context.Context, id string, action api.ReviewAction) (api.ReviewStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reviewCalls = append(f.reviewCalls, string(action)+" "+id)

	if f.reviewErr != nil {
		return api.ReviewStatus{}, f.reviewErr
	}
	return f.reviewStatus, nil
}

// CleanupPlan records the request and answers with whatever the test arranged.
func (f *fakeBackend) CleanupPlan(_ context.Context, id string) (api.CleanupPlan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cleanupCalls = append(f.cleanupCalls, id)

	if f.cleanupPlanErr != nil {
		return api.CleanupPlan{}, f.cleanupPlanErr
	}
	return f.cleanupPlan, nil
}

// Cleanup records the selection, which is what a test asserts against: what the
// user selected and what reached the daemon are different things, and only one
// of them removes anything.
func (f *fakeBackend) Cleanup(
	_ context.Context, id string, selection api.CleanupSelection,
) (api.CleanupStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cleanupSelections = append(f.cleanupSelections, selection)

	if f.cleanupErr != nil {
		return api.CleanupStatus{}, f.cleanupErr
	}
	_ = id
	return f.cleanupStatus, nil
}

func (f *fakeBackend) Resume(_ context.Context, id string) (api.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resumed = append(f.resumed, id)
	return api.Task{ID: id}, nil
}

func (f *fakeBackend) Stop(_ context.Context, id string) (api.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, id)

	if f.stopErr != nil {
		return api.Task{}, f.stopErr
	}
	return api.Task{ID: id}, nil
}

func (f *fakeBackend) Reconciliation(context.Context) (api.Reconciliation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reconciliation, nil
}

// Reconcile records that a new pass was asked for, which is what a test asserts
// against: reading the last one and looking again are different requests.
func (f *fakeBackend) Reconcile(context.Context) (api.Reconciliation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reconciled++
	return f.reconciliation, nil
}

func (f *fakeBackend) ReviewCommand(command api.ReviewCommand) (tea.ExecCommand, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reviewRan = append(f.reviewRan, command)
	return noopCommand{}, nil
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

// NewWizard builds a flow over a machine with one repository on it, so that the
// dialog can be driven end to end without Git, a configuration directory, or a
// file.
//
// The questions themselves are internal/wizard's and are tested there. What a
// test here asserts is what the dialog does with them.
func (f *fakeBackend) NewWizard() (*wizard.Wizard, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.wizards++

	if f.wizardErr != nil {
		return nil, f.wizardErr
	}
	return wizard.New(wizard.Options{
		Host:      fakeHost{root: f.root},
		ConfigDir: filepath.Join(f.root, "config"),
		Resolve: config.Options{
			Env:      paths.Environment{Getenv: func(string) string { return "" }, Home: f.root},
			StateDir: filepath.Join(f.root, "state"),
		},
	})
}

// WriteProject records that the file was asked for rather than writing one: the
// exclusive create belongs to internal/wizard, and what a screen test can assert
// is that nothing asked for it until the user confirmed.
func (f *fakeBackend) WriteProject(flow *wizard.Wizard) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.written = append(f.written, flow.ID())

	if f.writeErr != nil {
		return "", f.writeErr
	}
	return filepath.Join(f.root, "config", flow.ID()+".yaml"), nil
}

// Diagnose records that the checks were asked for and answers with whatever the
// test arranged. Its most important assertion is that it stays at zero: the
// checks shell out to Git, Compose, and the container runtime, so nothing but a
// user may start one.
func (f *fakeBackend) Diagnose(_ context.Context, id string) (api.Diagnosis, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.diagnosed = append(f.diagnosed, id)

	if f.diagnoseErr != nil {
		return api.Diagnosis{}, f.diagnoseErr
	}
	if f.diagnosis.Environment == "" {
		f.diagnosis.Environment = "this terminal"
	}
	return f.diagnosis, nil
}

// RegisterProject records what the dashboard asked the daemon to register.
func (f *fakeBackend) RegisterProject(_ context.Context, id string) (api.Registration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.registered = append(f.registered, id)

	if f.registerErr != nil {
		return api.Registration{}, f.registerErr
	}
	return api.Registration{Created: true, Project: api.Project{ID: id, Name: id}}, nil
}

// fakeHost is a machine with one repository on it, at root/repo.
type fakeHost struct{ root string }

func (h fakeHost) Inspect(_ context.Context, path string) (wizard.Checkout, error) {
	if path != filepath.Join(h.root, "repo") {
		return wizard.Checkout{}, errors.New(path + " is not a Git repository")
	}
	return wizard.Checkout{Root: path, Remote: "origin", DefaultBranch: "main"}, nil
}

func (h fakeHost) ComposeFiles(string) []string       { return nil }
func (h fakeHost) ComposeServices(...string) []string { return nil }
func (h fakeHost) Compose(string, ...string) wizard.Composition {
	return wizard.Composition{}
}
func (h fakeHost) Exists(string) bool       { return true }
func (h fakeHost) WorkingDirectory() string { return filepath.Join(h.root, "repo") }
func (h fakeHost) Absolute(value string) (string, error) {
	if filepath.IsAbs(value) {
		return filepath.Clean(value), nil
	}
	return filepath.Join(h.root, value), nil
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

// TestAFirstRunIsPointedAtTheWizard checks what preparation says on a machine
// where nothing is configured yet.
//
// It is the error a new user is most likely to meet, and the step that clears it
// is writing a configuration: naming `feat project add` names a registration
// with nothing to register (ADR-062). What it names instead is the key that
// opens the wizard, because that is the shortest route from where the user is
// standing when they read it (ADR-063).
func TestAFirstRunIsPointedAtTheWizard(t *testing.T) {
	backend := newFakeBackend()
	backend.projects = nil

	model := newPrepare(backend, "", "", api.Source{Kind: "prompt"})
	model = settle(t, model, model.Init())

	if model.err == nil {
		t.Fatal("preparation with nothing registered reported no error")
	}
	if !strings.Contains(model.err.Error(), "press esc, then p,") {
		t.Errorf("error = %q, want the keys that reach the wizard from here", model.err)
	}
	if view := model.View(); !strings.Contains(view, "configure one") {
		t.Errorf("the screen does not show the way out of a first run:\n%s", view)
	}
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

// TestCancellingPreparationCreatesNothing is FR-TASK-003 at the screen the user
// cancels from.
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

// TerminalFrame answers with whatever the test arranged, recording the size it
// was asked for.
func (f *fakeBackend) TerminalFrame(_ context.Context, id string, view api.TerminalView) (api.TerminalFrame, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.frames = append(f.frames, view)
	f.frameTasks = append(f.frameTasks, id)
	if f.frameErr != nil {
		return api.TerminalFrame{}, f.frameErr
	}
	frame := f.frame
	if len(frame.Panes) == 0 {
		frame.Panes = []api.TerminalPane{{
			Pane: "%11", Width: view.Width, Height: view.Height, Active: true,
			Content: []string{"\x1b[32mready\x1b[m", "> "},
		}}
	}
	frame.Width, frame.Height = view.Width, view.Height
	_ = id
	return frame, nil
}

// SendTerminalInput records what a focused pane was sent.
func (f *fakeBackend) SendTerminalInput(_ context.Context, _ string, input api.TerminalInput) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.inputs = append(f.inputs, input)
	return f.inputErr
}

// terminalInputs returns what was sent, copied under the lock.
func (f *fakeBackend) terminalInputs() []api.TerminalInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]api.TerminalInput(nil), f.inputs...)
}

// terminalViews returns the sizes frames were asked for.
func (f *fakeBackend) terminalViews() []api.TerminalView {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]api.TerminalView(nil), f.frames...)
}
