package ui

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

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

	created []api.CreateDraft
	updated []api.UpdateDraft
	planned int
	// launched records every confirmation a screen sent, so a test can assert
	// that what reached the daemon is what the review step displayed.
	launched  []api.Confirmation
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

	// publicationCalls records every plan, publicationRequests every apply, and
	// publicationEdits every time the draft was opened. Their most important
	// assertion is what stays empty: opening the screen sends nothing, and a
	// publication reaches the daemon only after the user has read the draft.
	publicationCalls    []string
	publicationRequests []api.PublishRequest
	publicationEdits    int
	publicationStatus   api.PublicationStatus
	publicationDone     api.PublicationStatus
	publicationErr      error
	publicationApplyErr error
	// approved is what the fake editor returns, and editorErr is a document that
	// could not be read back.
	approved  []api.ApprovedPublication
	editorErr error

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

	// tasksErr is what a read of task state fails with, and daemonStarts counts
	// the daemons the dashboard asked to have started. The counter's most
	// important assertion is that it stays at zero: starting a process is the one
	// thing on this interface that is not a request over a socket, and nothing
	// but a user's yes may reach it.
	tasksErr     error
	daemonStarts int
	daemonErr    error

	// tickets is what each project's tracker command printed, and ticketCalls
	// records every project whose tracker was asked. The counter is what makes
	// "a network call follows a key the user pressed" checkable: it stays at
	// zero until one is.
	tickets     map[string]api.TicketList
	ticketCalls []string
	ticketErr   error

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
	if f.tasksErr != nil {
		return nil, f.tasksErr
	}
	return f.tasks, nil
}

// Tickets answers with what a project's tracker command printed, so that a
// screen's behaviour can be checked without a tracker, an account, or a network.
func (f *fakeBackend) Tickets(_ context.Context, project string) (api.TicketList, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.ticketCalls = append(f.ticketCalls, project)
	if f.ticketErr != nil {
		return api.TicketList{}, f.ticketErr
	}
	list, ok := f.tickets[project]
	if !ok {
		return api.TicketList{}, errors.New("project " + project + " configures no tracker")
	}
	return list, nil
}

// StartDaemon records that the dashboard asked for one, and stops failing the
// reads that made it ask unless the test arranged otherwise. A fake that went on
// refusing after a successful start could not tell a dashboard that recovered
// from one that only said it had.
func (f *fakeBackend) StartDaemon(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.daemonStarts++
	if f.daemonErr != nil {
		return f.daemonErr
	}
	f.tasksErr = nil
	return nil
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

func (f *fakeBackend) LaunchDraft(
	_ context.Context, id string, confirmation api.Confirmation,
) (api.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.launchErr != nil {
		return api.Task{}, f.launchErr
	}
	if confirmation.Fingerprint != f.fingerprint {
		return api.Task{}, errors.New("the draft changed after the plan you confirmed was displayed")
	}
	f.launched = append(f.launched, confirmation)
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

// PlanPublication records the request and answers with whatever the test
// arranged.
func (f *fakeBackend) PlanPublication(_ context.Context, id string) (api.PublicationStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.publicationCalls = append(f.publicationCalls, id)

	if f.publicationErr != nil {
		return api.PublicationStatus{}, f.publicationErr
	}
	return f.publicationStatus, nil
}

// ApplyPublication records what reached the daemon, which is the assertion that
// matters: what is sent is what the user approved.
func (f *fakeBackend) ApplyPublication(
	_ context.Context, _ string, request api.PublishRequest,
) (api.PublicationStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.publicationRequests = append(f.publicationRequests, request)

	if f.publicationApplyErr != nil {
		return api.PublicationStatus{}, f.publicationApplyErr
	}
	return f.publicationDone, nil
}

// EditPublication stands in for the editor, returning what the test arranged as
// though the user had typed it.
func (f *fakeBackend) EditPublication(_ api.PublicationStatus) (PublicationEditor, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.publicationEdits++
	return fakePublicationEditor{approved: f.approved, err: f.editorErr}, nil
}

// fakePublicationEditor is a draft that has already been through an editor.
type fakePublicationEditor struct {
	approved []api.ApprovedPublication
	err      error
}

func (fakePublicationEditor) Command() tea.ExecCommand { return noopCommand{} }
func (e fakePublicationEditor) Read() ([]api.ApprovedPublication, error) {
	return e.approved, e.err
}
func (fakePublicationEditor) Close() {}

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

// ComposeFiles answers with a base and an override, which is the case where the
// flow derives more than one value and can propose only one of them.
func (h fakeHost) ComposeFiles(string) []string {
	return []string{
		filepath.Join(h.root, "repo", "compose.yaml"),
		filepath.Join(h.root, "repo", "compose.override.yaml"),
	}
}

func (h fakeHost) ComposeServices(...string) []string { return nil }
func (h fakeHost) Compose(string, string, ...string) wizard.Composition {
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
		// A batch is a list of commands rather than a message: the runtime runs
		// each and delivers what each produces, and a harness that handed the
		// batch itself to Update would silently drop everything in it.
		if batch, batched := message.(tea.BatchMsg); batched {
			for _, next := range batch {
				model = settle(t, model, next)
			}
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
	case "ctrl+t":
		return tea.KeyMsg{Type: tea.KeyCtrlT}
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

// writing opens preparation and answers the source question the way a user who
// types their own brief does.
//
// The cursor opens on "write it here", so the answer is one Enter — which is
// what keeps Enter-Enter the preparation this screen has always had.
func writing(t *testing.T, backend *fakeBackend) prepareModel {
	t.Helper()

	model := newPrepare(backend, prepareStart{project: "example", source: api.Source{Kind: "prompt"}})
	model = settle(t, model, model.Init())
	if model.step != stepSource {
		t.Fatalf("step = %d, want the source question with one project registered: %v",
			model.step, model.err)
	}

	model = drive(t, model, key("enter"))
	if model.step != stepBrief {
		t.Fatalf("step = %d, want the brief: %v", model.step, model.err)
	}
	return model
}

// prepared returns a preparation model that has reached the review screen.
func prepared(t *testing.T, backend *fakeBackend) prepareModel {
	t.Helper()

	model := writing(t, backend)
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

	model := newPrepare(backend, prepareStart{source: api.Source{Kind: "prompt"}})
	model = settle(t, model, model.Init())

	if model.err == nil {
		t.Fatal("preparation with nothing registered reported no error")
	}
	if !strings.Contains(model.err.Error(), "press esc, then p,") {
		t.Errorf("error = %q, want the keys that reach the wizard from here", model.err)
	}
	if view := model.View(newActivity()); !strings.Contains(view, "configure one") {
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
	view := model.View(newActivity())
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
	if len(backend.launched) != 1 || backend.launched[0].Fingerprint != backend.fingerprint {
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

	model := writing(t, backend)
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

	model := newPrepare(backend, prepareStart{
		project: "example",
		brief:   brief,
		source:  api.Source{Kind: "markdown", Reference: "/srv/notes/task.md"},
	})

	if got := model.title.Value(); got != "Retry failed exports" {
		t.Errorf("title = %q, want the brief's first heading", got)
	}
	if got := model.brief.Value(); got != brief {
		t.Errorf("brief = %q, want the imported document unchanged", got)
	}

	model = settle(t, model, model.Init())
	if !strings.Contains(model.View(newActivity()), "task.md") {
		t.Errorf("the brief screen does not say where the brief came from:\n%s", model.View(newActivity()))
	}
}

// TestPreparationNeedsARepository checks that a task cannot be resolved with
// nothing selected.
func TestPreparationNeedsARepository(t *testing.T) {
	backend := newFakeBackend()

	model := writing(t, backend)
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

	model := writing(t, backend)
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

	model := writing(t, backend)
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

	model := writing(t, backend)
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

// ticketList is what a project's tracker printed, with one ticket labelled by a
// merged command and one that is not.
func ticketList() api.TicketList {
	return api.TicketList{
		ReadAt: time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC),
		Tickets: []api.Ticket{
			{
				Reference: "ACME-14",
				Title:     "Reset links expire too quickly",
				Body:      "The link stops working after five minutes.",
				URL:       "https://app.shortcut.com/acme/story/14",
				State:     "Ready for Dev",
				Source:    "shortcut",
			},
			{
				Reference: "#42",
				Title:     "Export the daily report",
				Body:      "Nightly, as CSV.",
				URL:       "https://github.com/acme/planning/issues/42",
				State:     "open",
			},
		},
	}
}

// withTickets arranges a project whose tracker prints the list above.
func withTickets(backend *fakeBackend) {
	backend.tickets = map[string]api.TicketList{"example": ticketList()}
}

// fromTicket answers the source question with the project's tracker, which is
// what runs the command and opens the list.
//
// "from a ticket" is the second answer, so it is one press down from where the
// cursor opens.
func fromTicket(t *testing.T, backend *fakeBackend) prepareModel {
	t.Helper()

	model := newPrepare(backend, prepareStart{project: "example", source: api.Source{Kind: "prompt"}})
	model = settle(t, model, model.Init())
	return drive(t, model, key("down"), key("enter"))
}

// TestTheTrackerIsAskedOnlyWhenTheUserAsks is ADR-031's rule applied to the
// tracker: the command reaches somebody's service over a network, so nothing may
// run it because a screen opened or a field was edited.
//
// The source step does not weaken that. Opening it runs nothing, because the
// question is drawn from three constants; the command runs on the answer.
func TestTheTrackerIsAskedOnlyWhenTheUserAsks(t *testing.T) {
	backend := newFakeBackend()
	withTickets(backend)

	model := newPrepare(backend, prepareStart{project: "example", source: api.Source{Kind: "prompt"}})
	model = settle(t, model, model.Init())
	if model.step != stepSource {
		t.Fatalf("step = %d, want the source question: %v", model.step, model.err)
	}
	if len(backend.ticketCalls) != 0 {
		t.Fatalf("the tracker ran %v before anybody asked for it", backend.ticketCalls)
	}

	model = drive(t, model, key("down"), key("enter"))
	if model.step != stepTickets {
		t.Fatalf("step = %d, want the ticket list: %v", model.step, model.err)
	}
	if len(backend.ticketCalls) != 1 || backend.ticketCalls[0] != "example" {
		t.Errorf("the tracker was asked %v, want the chosen project once", backend.ticketCalls)
	}
}

// TestSelectingATicketComposesTheBriefTheUserConfirms is ADR-070's inbound rule:
// the ticket fills the same editable brief a typed prompt is written in, and it
// is that document — not the ticket — that the confirmation displays and the
// agent receives.
func TestSelectingATicketComposesTheBriefTheUserConfirms(t *testing.T) {
	backend := newFakeBackend()
	withTickets(backend)

	model := fromTicket(t, backend)
	model = drive(t, model, key("enter"))

	if model.step != stepBrief {
		t.Fatalf("step = %d, want the brief after selecting a ticket: %v", model.step, model.err)
	}
	brief := model.brief.Value()
	for _, want := range []string{
		"ACME-14", "Reset links expire too quickly", "Ready for Dev",
		"https://app.shortcut.com/acme/story/14", "shortcut",
		"The link stops working after five minutes.",
	} {
		if !strings.Contains(brief, want) {
			t.Errorf("the composed brief does not carry %q:\n%s", want, brief)
		}
	}
	if !strings.Contains(model.title.Value(), "ACME-14") {
		t.Errorf("title = %q, and it does not name the ticket", model.title.Value())
	}

	// And the source records the ticket, which is what a merge request later
	// names and what a ticket observed again is compared against.
	if model.source.Kind != "ticket" || model.source.Ticket == nil {
		t.Fatalf("source = %+v, want a ticket source carrying the ticket", model.source)
	}
	if model.source.Ticket.Provider != "shortcut" {
		t.Errorf("provider = %q, and a merged command's label is what fills it",
			model.source.Ticket.Provider)
	}
	if !model.source.Ticket.Snapshot.TakenAt.Equal(ticketList().ReadAt) {
		t.Errorf("the snapshot says it was read at %s, want when the list was read at %s",
			model.source.Ticket.Snapshot.TakenAt, ticketList().ReadAt)
	}
}

// TestATicketBriefReachesTheDaemonThroughTheOrdinaryPath checks that a task
// composed from a ticket is created by the same requests a typed prompt is, so
// that the confirmation, the fingerprint, and every other invariant of
// preparation apply to it unchanged.
func TestATicketBriefReachesTheDaemonThroughTheOrdinaryPath(t *testing.T) {
	backend := newFakeBackend()
	withTickets(backend)

	model := fromTicket(t, backend)
	model = drive(t, model, key("enter"), key("ctrl+s"), key("enter"))

	if model.step != stepReview {
		t.Fatalf("step = %d, want the review screen: %v", model.step, model.err)
	}
	if len(backend.created) != 1 {
		t.Fatalf("%d drafts were created, want one", len(backend.created))
	}

	created := backend.created[0]
	if created.Source.Kind != "ticket" || created.Source.Ticket == nil {
		t.Fatalf("the draft's source is %+v, want the ticket it was composed from", created.Source)
	}
	if created.Source.Ticket.Reference != "ACME-14" {
		t.Errorf("reference = %q", created.Source.Ticket.Reference)
	}
	// What was sent is what the screen displayed, which is the whole of the
	// approval (ADR-070).
	if created.Brief != model.brief.Value() {
		t.Errorf("the brief sent is not the one on screen:\nsent:\n%s\nshown:\n%s",
			created.Brief, model.brief.Value())
	}
	if strings.Contains(created.Brief, "\r") {
		t.Error("the brief carries carriage returns")
	}
}

// TestATicketReferenceIsMatchedAgainstWhatTheCommandEmitted is ADR-071's rule
// that Feat never parses a reference: `--ticket` re-runs the command and matches
// what it printed, so a tracker whose keys look like anything at all works.
func TestATicketReferenceIsMatchedAgainstWhatTheCommandEmitted(t *testing.T) {
	backend := newFakeBackend()
	withTickets(backend)

	model := newPrepare(backend, prepareStart{
		project: "example", source: api.Source{Kind: "prompt"}, ticket: "#42",
	})
	model = settle(t, model, model.Init())

	if model.err != nil {
		t.Fatalf("preparing from a ticket: %v", model.err)
	}
	if model.step != stepBrief {
		t.Fatalf("step = %d, want the brief with the ticket already composed", model.step)
	}
	if !strings.Contains(model.brief.Value(), "Export the daily report") {
		t.Errorf("the brief is not the named ticket's:\n%s", model.brief.Value())
	}
	if model.source.Ticket == nil || model.source.Ticket.Reference != "#42" {
		t.Errorf("source = %+v, want the ticket the reference named", model.source)
	}
	if model.source.Ticket.Provider != "" {
		t.Errorf("provider = %q for a ticket the command did not label",
			model.source.Ticket.Provider)
	}
}

// TestATicketReferenceTheCommandDidNotEmitSaysWhatItDid checks the answer to a
// reference that is not in the list. Which tickets are the user's is the
// command's decision, so the answer names what it printed rather than claiming
// the ticket does not exist.
func TestATicketReferenceTheCommandDidNotEmitSaysWhatItDid(t *testing.T) {
	backend := newFakeBackend()
	withTickets(backend)

	model := newPrepare(backend, prepareStart{
		project: "example", source: api.Source{Kind: "prompt"}, ticket: "ACME-99",
	})
	model = settle(t, model, model.Init())

	if model.err == nil {
		t.Fatal("a reference the command did not print was accepted")
	}
	for _, want := range []string{"ACME-99", "ACME-14", "#42"} {
		if !strings.Contains(model.err.Error(), want) {
			t.Errorf("the failure does not say %q: %v", want, model.err)
		}
	}
	// Preparation continues rather than ending: the brief is still there to be
	// written by hand.
	if model.step != stepBrief {
		t.Errorf("step = %d, want the brief", model.step)
	}
	if model.source.Kind == "ticket" {
		t.Error("the source says the task came from a ticket that was never found")
	}
}

// TestAProjectWithNoTrackerSaysSoRatherThanShowingAnEmptyList checks that the
// absence reaches the user as the daemon's own words.
//
// The option is offered by a project that configures no tracker, because the
// sentence that comes back is the most actionable one Feat has on the subject
// and it costs one socket round trip and no network wait. Drawing the option as
// unavailable would need tracker presence on api.Project, which is built from a
// stored snapshot that does not carry it — a flag that would be stale the moment
// a user configured one (ADR-083).
//
// The user stays on the source step, where the other two answers are.
func TestAProjectWithNoTrackerSaysSoRatherThanShowingAnEmptyList(t *testing.T) {
	backend := newFakeBackend()

	model := fromTicket(t, backend)

	if model.step != stepSource {
		t.Fatalf("step = %d, want the source question", model.step)
	}
	if model.err == nil || !strings.Contains(model.err.Error(), "no tracker") {
		t.Errorf("the failure does not say the project configures no tracker: %v", model.err)
	}
	// And the sentence is on the screen the user is standing on, with the
	// answers they can still give.
	view := ansi.Strip(model.View(newActivity()))
	for _, want := range []string{"no tracker", "write it here", "from a Markdown file"} {
		if !strings.Contains(view, want) {
			t.Errorf("the source step does not show %q:\n%s", want, view)
		}
	}
}

// TestAnEmptyTicketListReturnsToTheSourceStep checks the other answer a tracker
// can give. Which tickets are the user's is the command's decision, and none is
// one of them — so it is an answer to the source question rather than a failure,
// and it leaves the user where the other answers are.
func TestAnEmptyTicketListReturnsToTheSourceStep(t *testing.T) {
	backend := newFakeBackend()
	backend.tickets = map[string]api.TicketList{"example": {ReadAt: time.Now()}}

	model := fromTicket(t, backend)

	if model.step != stepSource {
		t.Fatalf("step = %d, want the source question", model.step)
	}
	if model.err == nil || !strings.Contains(model.err.Error(), "no tickets") {
		t.Errorf("error = %v, want one saying the tracker printed none", model.err)
	}
}

// TestBackingOutOfTheTicketListReturnsToTheSourceStep checks that esc answers
// the screen it is pressed on. There is one way into the list, so there is one
// place to go back to and nothing has to remember which.
func TestBackingOutOfTheTicketListReturnsToTheSourceStep(t *testing.T) {
	backend := newFakeBackend()
	withTickets(backend)

	model := fromTicket(t, backend)
	if model.step != stepTickets {
		t.Fatalf("step = %d, want the ticket list: %v", model.step, model.err)
	}

	model = drive(t, model, key("esc"))
	if model.step != stepSource {
		t.Errorf("step = %d, want the source question", model.step)
	}
	if model.source.Kind != "prompt" {
		t.Errorf("source = %q, want no source recorded for a ticket nobody took", model.source.Kind)
	}
}

// TestTheTicketListShowsWhatTheTrackerPrinted checks that the selection shows
// the tracker's own words rather than a vocabulary of Feat's (ADR-071).
func TestTheTicketListShowsWhatTheTrackerPrinted(t *testing.T) {
	backend := newFakeBackend()
	withTickets(backend)

	model := fromTicket(t, backend)

	view := model.View(newActivity())
	for _, want := range []string{"ACME-14", "Ready for Dev", "shortcut", "#42", "open"} {
		if !strings.Contains(view, want) {
			t.Errorf("the ticket list does not show %q:\n%s", want, view)
		}
	}
}

// TestChoosingATicketAfterADraftWasRecordedStartsAgain checks the one way the
// brief and the recorded source could come apart.
//
// A draft records where its brief came from when it is created, and updating one
// replaces its title, brief, and repositories rather than that. So a user who
// resolves a draft, steps back, and then chooses a ticket must get a new draft:
// a task whose brief is the ticket's and whose source says otherwise is a record
// nothing could act on (ADR-071).
func TestChoosingATicketAfterADraftWasRecordedStartsAgain(t *testing.T) {
	backend := newFakeBackend()
	withTickets(backend)

	model := writing(t, backend)
	model.title.SetValue("Add a rate limit")
	model.brief.SetValue("Add a rate limit to the public API.")

	// Forward to the review screen, which is what records the draft, and back.
	model = drive(t, model, key("ctrl+s"), key("enter"))
	if len(backend.created) != 1 {
		t.Fatalf("%d drafts were created, want the one the resolve recorded", len(backend.created))
	}
	recorded := backend.created[0]
	model = drive(t, model, key("esc"), key("esc"), key("esc"))
	if model.step != stepSource {
		t.Fatalf("step = %d, want the source question", model.step)
	}

	model = drive(t, model, key("down"), key("enter"), key("enter"))

	if len(backend.cancelled) != 1 {
		t.Errorf("cancelled %v, want the one draft recorded before the ticket was chosen",
			backend.cancelled)
	}
	if recorded.Source.Kind != "prompt" {
		t.Errorf("the discarded draft's source was %q", recorded.Source.Kind)
	}

	// And the next resolve records a new draft, whose source is the ticket.
	model = drive(t, model, key("ctrl+s"), key("enter"))
	if len(backend.created) != 2 {
		t.Fatalf("%d drafts were created, want a second one for the ticket", len(backend.created))
	}
	if source := backend.created[1].Source; source.Kind != "ticket" || source.Ticket == nil {
		t.Errorf("the second draft's source is %+v, want the ticket", source)
	}
	// And nothing was cancelled twice. Choosing the ticket returns the model
	// that has forgotten the draft it discarded, so the resolve after it has
	// nothing left to cancel and creates rather than updates.
	if len(backend.cancelled) != 1 {
		t.Errorf("cancelled %v, want only the draft the ticket replaced", backend.cancelled)
	}
}

// fromFile answers the source question with a Markdown file, which is the third
// answer and two presses down from where the cursor opens.
func fromFile(t *testing.T, backend *fakeBackend) prepareModel {
	t.Helper()

	model := newPrepare(backend, prepareStart{project: "example", source: api.Source{Kind: "prompt"}})
	model = settle(t, model, model.Init())
	model = drive(t, model, key("down"), key("down"), key("enter"))
	if model.step != stepImport {
		t.Fatalf("step = %d, want the file screen: %v", model.step, model.err)
	}
	return model
}

// writeBrief puts a Markdown document on disk and returns its path.
func writeBrief(t *testing.T, name, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestAFlagAnswersTheSourceQuestionAndSkipsTheStep is what makes the step a
// question rather than an obstacle: --file and --ticket preselect the source
// exactly as --project preselects the project, so neither flag's behaviour
// changes and neither run sees a screen it has already answered (ADR-083).
func TestAFlagAnswersTheSourceQuestionAndSkipsTheStep(t *testing.T) {
	t.Run("--file", func(t *testing.T) {
		backend := newFakeBackend()

		model := newPrepare(backend, prepareStart{
			project: "example",
			brief:   "# Retry failed exports\n\nRetry three times.\n",
			source:  api.Source{Kind: "markdown", Reference: "/srv/notes/task.md"},
		})
		model = settle(t, model, model.Init())

		if model.step != stepBrief {
			t.Fatalf("step = %d, want the brief, with the source already answered", model.step)
		}
		if model.source.Kind != "markdown" {
			t.Errorf("source = %q, want the flag's own", model.source.Kind)
		}
	})

	t.Run("--ticket", func(t *testing.T) {
		backend := newFakeBackend()
		withTickets(backend)

		model := newPrepare(backend, prepareStart{
			project: "example", source: api.Source{Kind: "prompt"}, ticket: "#42",
		})
		model = settle(t, model, model.Init())

		if model.step != stepBrief {
			t.Fatalf("step = %d, want the brief the ticket composed: %v", model.step, model.err)
		}
		if !strings.Contains(model.brief.Value(), "Export the daily report") {
			t.Errorf("the brief is not the named ticket's:\n%s", model.brief.Value())
		}
	})
}

// TestEscFromABriefAFlagFilledLeavesPreparation checks the other half of a flag
// being an answer. There is no answer of the user's own to return to, so esc
// keeps the meaning it had before the step existed: the project step where there
// is one to choose, and out of preparation otherwise.
func TestEscFromABriefAFlagFilledLeavesPreparation(t *testing.T) {
	backend := newFakeBackend()

	model := newPrepare(backend, prepareStart{
		project: "example",
		brief:   "# Retry failed exports\n",
		source:  api.Source{Kind: "markdown", Reference: "/srv/notes/task.md"},
	})
	model = settle(t, model, model.Init())

	updated, cmd := model.Update(key("esc"))
	if updated.step == stepSource {
		t.Fatal("esc from a brief a flag filled went to a question the flag answered")
	}
	runTo[preparedMsg](t, cmd)
}

// TestEachAnswerReachesItsScreen walks the three answers.
func TestEachAnswerReachesItsScreen(t *testing.T) {
	for _, want := range []struct {
		answer  string
		presses []tea.Msg
		step    step
	}{
		{"write it here", []tea.Msg{key("enter")}, stepBrief},
		{"from a ticket", []tea.Msg{key("down"), key("enter")}, stepTickets},
		{"from a Markdown file", []tea.Msg{key("down"), key("down"), key("enter")}, stepImport},
	} {
		t.Run(want.answer, func(t *testing.T) {
			backend := newFakeBackend()
			withTickets(backend)

			model := newPrepare(backend, prepareStart{
				project: "example", source: api.Source{Kind: "prompt"},
			})
			model = settle(t, model, model.Init())
			model = drive(t, model, want.presses...)

			if model.step != want.step {
				t.Errorf("step = %d, want %d for %q: %v", model.step, want.step, want.answer, model.err)
			}
		})
	}
}

// TestTheTrailSaysSourceWhileAnAnswerIsBeingWorkedOut checks that the ticket
// list and the file screen are drawn as part of the step they belong to.
// Choosing a ticket or a file is answering the source question rather than a
// stage of preparation of its own.
func TestTheTrailSaysSourceWhileAnAnswerIsBeingWorkedOut(t *testing.T) {
	for _, current := range []step{stepTickets, stepImport} {
		if got := trailStep(current); got != stepSource {
			t.Errorf("the trail puts step %d at %d, want the source step", current, got)
		}
	}
	for _, current := range []step{stepProject, stepSource, stepBrief, stepRepositories, stepReview} {
		if got := trailStep(current); got != current {
			t.Errorf("the trail puts step %d at %d, want itself", current, got)
		}
	}
}

// TestEveryAnswerResetsTheBrief is the rule the step is shaped by: every source
// converges on the same editor, so the editor is where a brief is reviewed and
// adjusted, and returning to this step can only mean starting over. A user who
// wants to keep what they have never leaves the editor (ADR-083).
//
// The case that makes it a rule rather than a convenience is "write it here"
// clearing a brief a ticket composed: there is no confirmation, and the source
// recorded on the task goes back to being a prompt.
func TestEveryAnswerResetsTheBrief(t *testing.T) {
	backend := newFakeBackend()
	withTickets(backend)

	model := fromTicket(t, backend)
	model = drive(t, model, key("enter"))
	if model.source.Kind != "ticket" {
		t.Fatalf("source = %q, want the ticket the brief was composed from", model.source.Kind)
	}

	// Back to the question, and answer it the other way.
	model = drive(t, model, key("esc"))
	if model.step != stepSource {
		t.Fatalf("step = %d, want the source question", model.step)
	}
	model = drive(t, model, key("enter"))

	if model.step != stepBrief {
		t.Fatalf("step = %d, want the brief: %v", model.step, model.err)
	}
	if got := model.brief.Value(); got != "" {
		t.Errorf("brief = %q, want the composed document cleared", got)
	}
	if got := model.title.Value(); got != "" {
		t.Errorf("title = %q, want the ticket's title cleared", got)
	}
	if model.source.Kind != "prompt" || model.source.Ticket != nil {
		t.Errorf("source = %+v, want a typed prompt with no ticket on it", model.source)
	}
}

// TestChangingTheSourceAfterADraftWasRecordedStartsAgain is the correctness item
// of the whole step, on the path that the ticket list never had.
//
// A draft records where its brief came from when it is created and nothing later
// replaces that: updating one replaces its title, brief, and repositories. So a
// draft recorded before the source changed has to go, or a task could be
// launched whose brief came from a file and whose recorded source says "prompt"
// — a record nothing can act on (ADR-071, ADR-083).
func TestChangingTheSourceAfterADraftWasRecordedStartsAgain(t *testing.T) {
	backend := newFakeBackend()
	path := writeBrief(t, "imported.md", "# Rename the runtime\n\nEverywhere.\n")

	model := writing(t, backend)
	model.title.SetValue("Add a rate limit")
	model.brief.SetValue("Add a rate limit to the public API.")

	// Forward to the review screen, which records the draft, and back to the
	// question.
	model = drive(t, model, key("ctrl+s"), key("enter"))
	if len(backend.created) != 1 {
		t.Fatalf("%d drafts were created, want the one the resolve recorded", len(backend.created))
	}
	if backend.created[0].Source.Kind != "prompt" {
		t.Fatalf("the first draft's source is %q, want the typed prompt", backend.created[0].Source.Kind)
	}
	model = drive(t, model, key("esc"), key("esc"), key("esc"))

	// Import a file, which is a change of source the ticket path could never
	// make.
	model = drive(t, model, key("down"), key("down"), key("enter"))
	model.path.SetValue(path)
	model = drive(t, model, key("enter"))

	if len(backend.cancelled) != 1 {
		t.Errorf("cancelled %v, want the draft recorded before the source changed", backend.cancelled)
	}

	// And the next resolve records a new draft, whose source is the file.
	model = drive(t, model, key("ctrl+s"), key("enter"))
	if len(backend.created) != 2 {
		t.Fatalf("%d drafts were created, want a second one for the imported brief", len(backend.created))
	}
	created := backend.created[1]
	if created.Source.Kind != "markdown" || created.Source.Reference != path {
		t.Errorf("the second draft's source is %+v, want the file it was imported from", created.Source)
	}
	if created.Brief != model.brief.Value() {
		t.Errorf("the brief sent is not the one on screen:\nsent:\n%s\nshown:\n%s",
			created.Brief, model.brief.Value())
	}
	if len(backend.cancelled) != 1 {
		t.Errorf("cancelled %v, want only the draft the import replaced", backend.cancelled)
	}
}

// TestWritingItHereAfterADraftWasRecordedStartsAgain is the same rule on the
// answer that fills the brief with nothing. Any selection replaces the brief
// wholesale, so any selection discards the draft that recorded the old one.
func TestWritingItHereAfterADraftWasRecordedStartsAgain(t *testing.T) {
	backend := newFakeBackend()

	model := writing(t, backend)
	model.title.SetValue("Add a rate limit")
	model.brief.SetValue("Add a rate limit to the public API.")
	model = drive(t, model, key("ctrl+s"), key("enter"))
	if len(backend.created) != 1 {
		t.Fatalf("%d drafts were created, want the one the resolve recorded", len(backend.created))
	}

	model = drive(t, model, key("esc"), key("esc"), key("esc"), key("enter"))

	if len(backend.cancelled) != 1 {
		t.Errorf("cancelled %v, want the draft the answer replaced", backend.cancelled)
	}
	if model.brief.Value() != "" {
		t.Errorf("brief = %q, want it cleared", model.brief.Value())
	}
}

// TestImportingAFileFillsTheBriefTheUserConfirms is ADR-070's inbound rule
// applied to a document: a Markdown file is also text somebody else may have
// written, so it goes into the same editable field a typed prompt is, and it is
// that document the confirmation displays.
//
// The absolute path is recorded on the source, which is what lets the brief
// screen say where its text came from.
func TestImportingAFileFillsTheBriefTheUserConfirms(t *testing.T) {
	backend := newFakeBackend()
	const content = "# Rename the runtime\n\nEverywhere it is called something else.\n"
	path := writeBrief(t, "rename-the-runtime.md", content)

	model := fromFile(t, backend)
	model.path.SetValue(path)
	model = drive(t, model, key("enter"))

	if model.step != stepBrief {
		t.Fatalf("step = %d, want the brief the file filled: %v", model.step, model.err)
	}
	if model.brief.Value() != content {
		t.Errorf("brief = %q, want the file's text unchanged", model.brief.Value())
	}
	if model.title.Value() != "Rename the runtime" {
		t.Errorf("title = %q, want the document's first heading", model.title.Value())
	}
	if model.source.Kind != "markdown" || model.source.Reference != path {
		t.Errorf("source = %+v, want the absolute path it was read from", model.source)
	}
	if view := ansi.Strip(model.View(newActivity())); !strings.Contains(view, "imported from rename-the-runtime.md") {
		t.Errorf("the brief screen does not say where its text came from:\n%s", view)
	}
}

// TestAnImportKeepsATitleThatIsAlreadyThere is the $EDITOR round trip's rule
// rather than the import-at-start one: a title the user typed is theirs, and a
// document guessed a title from does not overwrite it.
func TestAnImportKeepsATitleThatIsAlreadyThere(t *testing.T) {
	backend := newFakeBackend()
	path := writeBrief(t, "notes.md", "# What the file calls itself\n\nBody.\n")

	model := fromFile(t, backend)
	model.title.SetValue("What I called it")
	model.path.SetValue(path)
	model = drive(t, model, key("enter"))

	if model.title.Value() != "What I called it" {
		t.Errorf("title = %q, want the one that was already there", model.title.Value())
	}
}

// TestTheImportScreenRefusesWhatIsNotABrief keeps every refusal on the screen
// the path was typed on, which is the screen the user can correct it on.
//
// A document with no text is refused here rather than at the repository step for
// the same reason: the agent receives the brief exactly as written, and an empty
// one is worth saying so about while the file is still on screen.
func TestTheImportScreenRefusesWhatIsNotABrief(t *testing.T) {
	oversized := writeBrief(t, "huge.md", strings.Repeat("x", 256<<10+1))

	for _, refusal := range []struct {
		name string
		path func(t *testing.T) string
		says string
	}{
		{"a directory", func(t *testing.T) string { return t.TempDir() }, "is a directory"},
		{"over the limit", func(*testing.T) string { return oversized }, "the limit is"},
		{"an empty file", func(t *testing.T) string { return writeBrief(t, "empty.md", "") }, "holds no text"},
		{"whitespace only", func(t *testing.T) string { return writeBrief(t, "blank.md", "\n\n   \n") }, "holds no text"},
		{"nothing at all", func(t *testing.T) string {
			return filepath.Join(t.TempDir(), "missing.md")
		}, "reading the task brief"},
	} {
		t.Run(refusal.name, func(t *testing.T) {
			backend := newFakeBackend()

			model := fromFile(t, backend)
			model.path.SetValue(refusal.path(t))
			model = drive(t, model, key("enter"))

			if model.step != stepImport {
				t.Fatalf("step = %d, want the screen the path was typed on", model.step)
			}
			if model.err == nil || !strings.Contains(model.err.Error(), refusal.says) {
				t.Errorf("error = %v, want one saying %q", model.err, refusal.says)
			}
			if model.brief.Value() != "" {
				t.Errorf("brief = %q, want nothing taken from a file that was refused", model.brief.Value())
			}
			if model.source.Kind != "prompt" {
				t.Errorf("source = %q, want no file recorded", model.source.Kind)
			}
		})
	}
}

// TestTheImportScreenCompletesFromTheProjectsCheckouts checks what tab offers.
//
// The project's own checkouts come first, because a brief written before the
// task usually sits beside the code it is about, and a directory is marked as
// one so that a completed path reads as somewhere to keep typing (ADR-077).
func TestTheImportScreenCompletesFromTheProjectsCheckouts(t *testing.T) {
	backend := newFakeBackend()
	root := t.TempDir()
	checkout := filepath.Join(root, "core")
	if err := os.MkdirAll(filepath.Join(checkout, "docs"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, "task.md"), []byte("Do it."), 0o600); err != nil {
		t.Fatal(err)
	}
	backend.projects[0].Repositories[0].HostPath = checkout

	model := fromFile(t, backend)

	// An empty field takes the first candidate, which is the project's own
	// checkout, marked as the directory it is.
	model = drive(t, model, key("tab"))
	if got, want := model.path.Value(), checkout+string(os.PathSeparator); got != want {
		t.Fatalf("tab put %q in the field, want the project's checkout %q", got, want)
	}

	// And what it names is then what is offered, so a completed directory is
	// somewhere to keep typing.
	for _, want := range []string{
		filepath.Join(checkout, "task.md"),
		filepath.Join(checkout, "docs") + string(os.PathSeparator),
	} {
		if !slices.Contains(model.candidates, want) {
			t.Errorf("%q is not offered; the candidates are %v", want, model.candidates)
		}
	}
}

// TestTheBriefScreenNoLongerOffersTheTicketList checks that there is one door
// into ticket selection rather than two.
//
// A second one, opened from inside the editor, would be a key that wipes the
// document the user is standing in — which is the ambiguity the source step
// exists to remove (ADR-083).
func TestTheBriefScreenNoLongerOffersTheTicketList(t *testing.T) {
	backend := newFakeBackend()
	withTickets(backend)

	model := writing(t, backend)
	model.brief.SetValue("Add a rate limit to the public API.")
	model.focus = 1

	model = drive(t, model, key("ctrl+t"))

	if model.step != stepBrief {
		t.Errorf("step = %d, want to stay on the brief", model.step)
	}
	if len(backend.ticketCalls) != 0 {
		t.Errorf("the tracker ran %v from the brief screen", backend.ticketCalls)
	}
	if view := ansi.Strip(model.View(newActivity())); strings.Contains(view, "ctrl+t") {
		t.Errorf("the brief screen still offers ctrl+t:\n%s", view)
	}
}

// TestEscFromTheBriefReturnsToTheSourceStep is the key's intended meaning rather
// than an oversight: there is no forward path back to the editor that does not
// pass through a selection, and every selection resets, so esc from the brief
// destroys what is in it. It is specified with no guard, because a key whose
// whole purpose is "start over" does not need to ask (ADR-083).
func TestEscFromTheBriefReturnsToTheSourceStep(t *testing.T) {
	backend := newFakeBackend()

	model := writing(t, backend)
	model.title.SetValue("Add a rate limit")
	model.brief.SetValue("Add a rate limit to the public API.")

	model = drive(t, model, key("esc"))
	if model.step != stepSource {
		t.Fatalf("step = %d, want the source question", model.step)
	}

	model = drive(t, model, key("enter"))
	if model.brief.Value() != "" || model.title.Value() != "" {
		t.Errorf("title = %q brief = %q, want both cleared by the answer",
			model.title.Value(), model.brief.Value())
	}
}

// TestEscFromTheSourceStepLeavesPreparationWithOneProject checks the step's own
// esc, which is what the brief step's has always been: the project step where
// there is a project to choose, and out of preparation otherwise.
func TestEscFromTheSourceStepLeavesPreparationWithOneProject(t *testing.T) {
	backend := newFakeBackend()

	model := newPrepare(backend, prepareStart{project: "example", source: api.Source{Kind: "prompt"}})
	model = settle(t, model, model.Init())
	if model.step != stepSource {
		t.Fatalf("step = %d, want the source question", model.step)
	}

	_, cmd := model.Update(key("esc"))
	runTo[preparedMsg](t, cmd)
}

// TestEscFromTheSourceStepReturnsToTheProjectStep is the same key where there is
// more than one project registered.
func TestEscFromTheSourceStepReturnsToTheProjectStep(t *testing.T) {
	backend := newFakeBackend()
	backend.projects = append(backend.projects, api.Project{ID: "other", Name: "Other"})

	model := newPrepare(backend, prepareStart{project: "example", source: api.Source{Kind: "prompt"}})
	model = settle(t, model, model.Init())
	model = drive(t, model, key("esc"))

	if model.step != stepProject {
		t.Errorf("step = %d, want the project step", model.step)
	}
}

// TestBackingOutOfTheFileScreenReturnsToTheSourceStep is the ticket list's rule
// on the other answer: one way in, one way back, and no field to remember which.
func TestBackingOutOfTheFileScreenReturnsToTheSourceStep(t *testing.T) {
	backend := newFakeBackend()

	model := fromFile(t, backend)
	model.path.SetValue("/srv/notes/task.md")

	model = drive(t, model, key("esc"))
	if model.step != stepSource {
		t.Fatalf("step = %d, want the source question", model.step)
	}
	if model.source.Kind != "prompt" || model.source.Reference != "" {
		t.Errorf("source = %+v, want no file recorded for one nobody imported", model.source)
	}

	// And the field does not carry the abandoned path back in.
	model = drive(t, model, key("down"), key("down"), key("enter"))
	if model.path.Value() != "" {
		t.Errorf("path = %q, want the screen opened empty", model.path.Value())
	}
}

// TestPreparingAgainOpensOnTheSourceQuestion checks what `n` in the dashboard
// reaches. A fresh preparation carries no flags, so nothing has answered the
// question, and the import and the ticket list are both one answer away — which
// is the route neither had from the dashboard before.
func TestPreparingAgainOpensOnTheSourceQuestion(t *testing.T) {
	backend := newFakeBackend()

	model := prepared(t, backend)
	fresh := model.restart()
	fresh = settle(t, fresh, fresh.Init())

	if fresh.step != stepSource {
		t.Fatalf("step = %d, want the source question: %v", fresh.step, fresh.err)
	}
	if fresh.brief.Value() != "" || fresh.title.Value() != "" {
		t.Errorf("title = %q brief = %q, want the previous task's answers gone",
			fresh.title.Value(), fresh.brief.Value())
	}
	view := ansi.Strip(fresh.View(newActivity()))
	for _, want := range []string{"from a ticket", "from a Markdown file"} {
		if !strings.Contains(view, want) {
			t.Errorf("the dashboard's preparation does not offer %q:\n%s", want, view)
		}
	}
}
