package api

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/store/storetest"
)

var update = flag.Bool("update", false, "rewrite golden files")

// fakeService answers the API from fixtures.
//
// The transport is tested against it rather than against a daemon, which is the
// point of the Service interface: a response shape can be pinned without a
// socket, a store, or a lock.
// fixedTime is when every fixture in this file happened, so a golden file pins
// a document rather than a clock.
var fixedTime = time.Date(2026, 8, 7, 9, 44, 22, 0, time.UTC)

type fakeService struct {
	health   HealthReport
	projects []*domain.Project
	tasks    []*domain.Task
	events   chan Event
	// failWith is returned by every read when set.
	failWith error
	// panicWith is raised by every read when set.
	panicWith string
	// unconfigured are projects with no configuration file.
	unconfigured map[domain.ProjectID]bool
	// unregistrable are projects whose configuration does not validate.
	unregistrable map[domain.ProjectID]bool
	// verifications are what each task's agent reported about its own checks.
	verifications map[domain.TaskID]Verification
	// actions records every runtime action the transport passed through, so a
	// test can assert that an endpoint reached the one its path names.
	actions []string
	// resources replaces the fixed sample below, so that a test can arrange a
	// machine that measured nothing while the golden keeps its own.
	resources ResourceReport
	// reconciliation is the last pass, absent until one has been run.
	reconciliation *Reconciliation
	// selections records every cleanup selection the transport passed through,
	// so a test can assert what reached the daemon rather than what was sent.
	selections []CleanupSelection
}

func newFakeService() *fakeService {
	return &fakeService{
		health: HealthReport{
			Daemon: Daemon{
				Version:   "v0.0.0-test",
				Commit:    "0123456",
				GoVersion: "go1.26.0",
				Platform:  "test/arch",
				PID:       4242,
				StartedAt: storetest.Origin,
				Socket:    "/run/feat/feat.sock",
			},
			State: State{Directory: "/state/feat", Projects: 1},
		},
		projects: []*domain.Project{storetest.Project()},
		tasks:    []*domain.Task{storetest.Task()},
		// The fixtures populate every field a payload can carry, so that a
		// mapping the DTO forgets shows up as a zero value rather than as
		// nothing at all.
		verifications: map[domain.TaskID]Verification{
			storetest.TaskID: verificationFixture(),
		},
		unconfigured:  map[domain.ProjectID]bool{},
		unregistrable: map[domain.ProjectID]bool{},
	}
}

func (f *fakeService) check() error {
	if f.panicWith != "" {
		panic(f.panicWith)
	}
	return f.failWith
}

func (f *fakeService) Health(context.Context) (HealthReport, error) {
	if err := f.check(); err != nil {
		return HealthReport{}, err
	}
	return f.health, nil
}

func (f *fakeService) Projects(context.Context) ([]*domain.Project, error) {
	if err := f.check(); err != nil {
		return nil, err
	}
	return f.projects, nil
}

func (f *fakeService) Project(_ context.Context, id domain.ProjectID) (*domain.Project, error) {
	if err := f.check(); err != nil {
		return nil, err
	}
	for _, project := range f.projects {
		if project.ID == id {
			return project, nil
		}
	}
	return nil, fmt.Errorf("%w: no project %s is registered", ErrNotFound, id)
}

// RegisterProject records a project from a fixture, so that the transport can
// be tested without a configuration directory or a store.
func (f *fakeService) RegisterProject(_ context.Context, id domain.ProjectID) (RegisteredProject, error) {
	if err := f.check(); err != nil {
		return RegisteredProject{}, err
	}
	if f.unconfigured[id] {
		return RegisteredProject{}, fmt.Errorf("%w: no configuration for %s", ErrNotFound, id)
	}
	if f.unregistrable[id] {
		return RegisteredProject{}, fmt.Errorf("%w: %s has a problem", ErrInvalid, id)
	}
	for _, project := range f.projects {
		if project.ID == id {
			return RegisteredProject{Project: project, Created: false}, nil
		}
	}
	registered := storetest.Project()
	registered.ID = id
	f.projects = append(f.projects, registered)
	return RegisteredProject{Project: registered, Created: true}, nil
}

func (f *fakeService) Tasks(context.Context) ([]*domain.Task, error) {
	if err := f.check(); err != nil {
		return nil, err
	}
	return f.tasks, nil
}

func (f *fakeService) Task(_ context.Context, id domain.TaskID) (*domain.Task, error) {
	if err := f.check(); err != nil {
		return nil, err
	}
	for _, task := range f.tasks {
		if task.ID == id {
			return task, nil
		}
	}
	return nil, fmt.Errorf("%w: no task %s in any registered project", ErrNotFound, id)
}

// ResolveTask resolves the way the daemon does, over the fake's own tasks, so
// that the transport is tested against the addressing rule rather than against a
// second implementation of it.
func (f *fakeService) ResolveTask(_ context.Context, ref domain.TaskRef) (domain.TaskID, error) {
	if err := f.check(); err != nil {
		return "", err
	}
	task, found, err := domain.ResolveTask(ref, f.tasks)
	switch {
	case err != nil:
		return "", err
	case !found:
		return "", fmt.Errorf("%w: no task matches %q", ErrNotFound, ref)
	}
	return task.ID, nil
}

func (f *fakeService) Verification(_ context.Context, id domain.TaskID) (Verification, bool, error) {
	reported, ok := f.verifications[id]
	return reported, ok, nil
}

// verificationFixture is a fully populated agent report, so that every field of
// the payload is exercised by the golden files and the mapping check.
func verificationFixture() Verification {
	return Verification{
		Source:     "agent",
		Passed:     3,
		Failed:     1,
		Other:      2,
		Summary:    "Added the endpoint; one integration test still fails.",
		ReportedAt: time.Date(2026, 8, 4, 9, 45, 0, 0, time.UTC),
	}
}

func (f *fakeService) AttachInfo(_ context.Context, id domain.TaskID) (AttachInfo, error) {
	if err := f.check(); err != nil {
		return AttachInfo{}, err
	}
	for _, task := range f.tasks {
		if task.ID == id && task.Session != nil {
			return AttachInfo{
				Socket:  task.Session.Tmux.Socket,
				Session: task.Session.Tmux.Session,
				Window:  task.Session.Tmux.Window,
				Pane:    task.Session.Tmux.Pane,
			}, nil
		}
	}
	return AttachInfo{}, fmt.Errorf("%w: task %s has no agent terminal", ErrNotFound, id)
}

func (f *fakeService) OpenShell(_ context.Context, id domain.TaskID) (AttachInfo, error) {
	if err := f.check(); err != nil {
		return AttachInfo{}, err
	}
	for _, task := range f.tasks {
		if task.ID == id && task.Session != nil {
			// A shell is a second pane in the same window, so only the pane
			// differs from the agent target.
			return AttachInfo{
				Socket:  task.Session.Tmux.Socket,
				Session: task.Session.Tmux.Session,
				Window:  task.Session.Tmux.Window,
				Pane:    "%9",
			}, nil
		}
	}
	return AttachInfo{}, fmt.Errorf("%w: task %s has no terminal to open a shell beside", ErrNotFound, id)
}

// Runtime records the action it was asked for and reports the task unchanged,
// so the transport can be tested without Docker.
func (f *fakeService) Runtime(_ context.Context, id domain.TaskID, action RuntimeAction) (RuntimeResult, error) {
	if err := f.check(); err != nil {
		return RuntimeResult{}, err
	}
	f.actions = append(f.actions, string(action))

	for _, task := range f.tasks {
		if task.ID == id {
			return RuntimeResult{
				Task: task,
				Services: []RuntimeService{
					{Name: "api", Container: "c0ffee", State: "running", Status: "Up 2 seconds",
						Health: "unknown", Managed: true},
					// A service the project does not name, which Compose started
					// because a managed one depends on it. It is in the published
					// body because it is in the task's Compose project, and Feat
					// stops and removes it with the rest.
					{Name: "postgres", Container: "cafe", State: "running", Status: "Up 12 seconds",
						Health: "healthy"},
				},
			}, nil
		}
	}
	return RuntimeResult{}, fmt.Errorf("%w: no task %s", ErrNotFound, id)
}

// RuntimeLogs returns the command the client would run.
func (f *fakeService) Review(_ context.Context, id domain.TaskID, action ReviewAction) (ReviewResult, error) {
	if err := f.check(); err != nil {
		return ReviewResult{}, err
	}
	f.actions = append(f.actions, string(action))

	for _, task := range f.tasks {
		if task.ID == id {
			return ReviewResult{
				Task:   task,
				Review: storetest.Review(),
				Commands: []ReviewCommand{{
					Kind:         "diff",
					RepositoryID: storetest.PrimaryRepositoryID.String(),
					Program:      "git",
					Arguments:    []string{"diff", storetest.PrimaryBaseCommit},
					Directory:    "/state/feat/worktrees/example/7f3a1c2e/api",
				}},
				Notes: []string{"api has 2 untracked file(s)"},
			}, nil
		}
	}
	return ReviewResult{}, fmt.Errorf("%w: no task %s", ErrNotFound, id)
}

func (f *fakeService) Reconciliation() (Reconciliation, bool) {
	if f.reconciliation == nil {
		return Reconciliation{}, false
	}
	return *f.reconciliation, true
}

func (f *fakeService) Reconcile(_ context.Context) (Reconciliation, error) {
	if err := f.check(); err != nil {
		return Reconciliation{}, err
	}
	f.actions = append(f.actions, "reconcile")

	report := Reconciliation{
		Ran:            true,
		StartedAt:      fixedTime,
		FinishedAt:     fixedTime,
		NeedsAttention: true,
		Findings: []ReconciliationFinding{{
			Class:     "terminal",
			Status:    "missing",
			ProjectID: storetest.ProjectID.String(),
			TaskID:    storetest.TaskID.String(),
			TaskKey:   storetest.TaskID.Key().String(),
			Identity:  "@3",
			Detail:    "the recorded tmux terminal is gone; the session is recorded as stopped and was not restarted",
			Action:    "resume it from the task detail",
		}},
		Problems: []ReconciliationProblem{{
			Class:  "runtime_containers",
			Reason: "the application services could not be observed",
		}},
		PreviousRunEndedCleanly: false,
		PreviousRunStoppedAt:    fixedTime,
	}
	f.reconciliation = &report
	return report, nil
}

func (f *fakeService) CleanupPlan(_ context.Context, id domain.TaskID) (CleanupPlan, error) {
	if err := f.check(); err != nil {
		return CleanupPlan{}, err
	}
	for _, task := range f.tasks {
		if task.ID == id {
			return CleanupPlan{
				TaskID:    task.ID.String(),
				TaskKey:   task.ID.Key().String(),
				ProjectID: task.ProjectID.String(),
				Workflow:  string(task.Workflow),
				Token:     "0f1e2d3c",
				Classes: []CleanupClass{{
					Class: "worktrees",
					Title: "worktrees",
					Targets: []CleanupTarget{{
						Identity:   "/state/feat/worktrees/example/7f3a1c2e/api",
						Repository: storetest.PrimaryRepositoryID.String(),
						Detail:     "the task worktree of api",
						Present:    true,
						Warnings:   []string{"the worktree has uncommitted or untracked changes"},
					}},
					Warnings: []string{"the worktree has uncommitted or untracked changes"},
				}},
				Archivable: true,
				ResolvedAt: fixedTime,
			}, nil
		}
	}
	return CleanupPlan{}, fmt.Errorf("%w: no task %s", ErrNotFound, id)
}

func (f *fakeService) Cleanup(_ context.Context, id domain.TaskID, selection CleanupSelection) (CleanupResult, error) {
	if err := f.check(); err != nil {
		return CleanupResult{}, err
	}
	f.selections = append(f.selections, selection)

	for _, task := range f.tasks {
		if task.ID == id {
			return CleanupResult{
				Task: task,
				Removed: []CleanupRemoval{{
					Class:    "worktrees",
					Identity: "/state/feat/worktrees/example/7f3a1c2e/api",
					Removed:  true,
				}},
				Archived: selection.Archive,
			}, nil
		}
	}
	return CleanupResult{}, fmt.Errorf("%w: no task %s", ErrNotFound, id)
}

func (f *fakeService) Resume(_ context.Context, id domain.TaskID) (*domain.Task, error) {
	if err := f.check(); err != nil {
		return nil, err
	}
	f.actions = append(f.actions, "resume")

	for _, task := range f.tasks {
		if task.ID == id {
			return task, nil
		}
	}
	return nil, fmt.Errorf("%w: no task %s", ErrNotFound, id)
}

func (f *fakeService) RuntimeLogs(_ context.Context, id domain.TaskID) (RuntimeCommand, error) {
	if err := f.check(); err != nil {
		return RuntimeCommand{}, err
	}
	for _, task := range f.tasks {
		if task.ID == id {
			return RuntimeCommand{
				Program: "/usr/local/bin/docker",
				Arguments: []string{
					"compose", "--project-name", "feat-example-7f3a1c2e", "logs", "--follow", "api",
				},
				Directory: "/repos/example/api",
			}, nil
		}
	}
	return RuntimeCommand{}, fmt.Errorf("%w: no task %s", ErrNotFound, id)
}

// Resources returns a fixed sample, so that the transport's own shape is what
// the golden file pins rather than the machine the test happens to run on.
func (f *fakeService) Resources(_ context.Context) (ResourceReport, error) {
	if err := f.check(); err != nil {
		return ResourceReport{}, err
	}
	if f.resources.Sampled || len(f.resources.Notes) > 0 {
		return f.resources, nil
	}
	cpu := 143.5
	memory := uint64(2_684_354_560)
	return ResourceReport{
		Machine: MachineResources{
			Cores:  10,
			Load:   &LoadAverage{One: 3.6, Five: 3.46, Fifteen: 3.76},
			Memory: &Capacity{TotalBytes: 17_179_869_184, AvailableBytes: 4_294_967_296},
			Disk: &DiskCapacity{
				Path: "/state", TotalBytes: 1_000_000_000_000, AvailableBytes: 400_000_000_000,
			},
		},
		Tasks: []TaskResources{{
			TaskID:         "7f3a1c2e-9b4d-4c81-8f2a-1d6b0c5e7a93",
			CPUPercent:     &cpu,
			MemoryBytes:    &memory,
			ContainerBytes: 2_147_483_648,
			ProcessBytes:   536_870_912,
			Containers: []ContainerResources{{
				ID: "f17a8623e8c2", Name: "feat-agent-example-7f3a1c2e-dev", Kind: "agent",
				CPUPercent: 12.5, MemoryBytes: 2_147_483_648,
			}},
			Processes: 7,
		}},
		Notes:       []string{},
		CollectedAt: time.Date(2026, 8, 7, 9, 44, 22, 0, time.UTC),
		Sampled:     true,
	}, nil
}

// CreateDraft records a draft from a fixture, so the transport can be tested
// without a store, a configuration directory, or Git.
func (f *fakeService) CreateDraft(_ context.Context, request DraftRequest) (*domain.Task, error) {
	if err := f.check(); err != nil {
		return nil, err
	}
	draft := storetest.Draft()
	draft.ProjectID = request.Project
	draft.Title = request.Title
	draft.Brief = request.Brief
	draft.Source = request.Source
	f.tasks = append(f.tasks, draft)
	return draft, nil
}

func (f *fakeService) UpdateDraft(ctx context.Context, id domain.TaskID, request DraftUpdate) (*domain.Task, error) {
	draft, err := f.draft(ctx, id)
	if err != nil {
		return nil, err
	}
	draft.Title = request.Title
	draft.Brief = request.Brief
	draft.Repositories = nil
	for _, selected := range request.Repositories {
		draft.Repositories = append(draft.Repositories, domain.TaskRepository{
			RepositoryID: selected.Repository,
			Access:       selected.Access,
			BaseRef:      selected.Ref,
		})
	}
	return draft, nil
}

func (f *fakeService) PlanDraft(ctx context.Context, id domain.TaskID) (ResolvedDraft, error) {
	draft, err := f.draft(ctx, id)
	if err != nil {
		return ResolvedDraft{}, err
	}
	return ResolvedDraft{
		Task:        draft,
		Notes:       []string{"fetching origin failed, so the base is resolved from the last fetched state"},
		Fingerprint: "0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0",
	}, nil
}

func (f *fakeService) LaunchDraft(ctx context.Context, id domain.TaskID, fingerprint string) (*domain.Task, error) {
	draft, err := f.draft(ctx, id)
	if err != nil {
		return nil, err
	}
	if fingerprint == "" {
		return nil, fmt.Errorf("%w: task %s changed after the plan you confirmed was displayed", ErrInvalid, id)
	}
	return draft, nil
}

func (f *fakeService) CancelDraft(ctx context.Context, id domain.TaskID) (*domain.Task, error) {
	draft, err := f.draft(ctx, id)
	if err != nil {
		return nil, err
	}
	draft.Workflow = domain.WorkflowArchived
	return draft, nil
}

func (f *fakeService) draft(ctx context.Context, id domain.TaskID) (*domain.Task, error) {
	task, err := f.Task(ctx, id)
	if err != nil {
		return nil, err
	}
	if task.Workflow != domain.WorkflowDraft {
		return nil, fmt.Errorf("%w: task %s is %s, and only a draft can be prepared",
			ErrInvalid, id, task.Workflow)
	}
	return task, nil
}

func (f *fakeService) Subscribe(context.Context) (<-chan Event, error) {
	if err := f.check(); err != nil {
		return nil, err
	}
	if f.events == nil {
		f.events = make(chan Event)
	}
	return f.events, nil
}

// request runs one request against a handler and returns the response.
func request(t *testing.T, handler http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(method, path, nil))
	return recorder
}

// requestBody runs one request that carries a JSON body.
func requestBody(
	t *testing.T, handler http.Handler, method, path, body string,
) *httptest.ResponseRecorder {
	t.Helper()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recorder, request)
	return recorder
}

// TestResponseBodies pins every payload against a golden file.
//
// The wire format is a published surface (ADR-027): renaming a Go field, or
// mapping one differently, has to fail here rather than in a client somebody
// else wrote.
func TestResponseBodies(t *testing.T) {
	handler := NewHandler(Options{Service: newFakeService()})

	tests := []struct {
		name string
		path string
	}{
		{"health", "/v1/health"},
		{"projects", "/v1/projects"},
		{"project", "/v1/projects/" + storetest.ProjectID.String()},
		{"tasks", "/v1/tasks"},
		{"task", "/v1/tasks/" + storetest.TaskID.String()},
		{"resources", "/v1/resources"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := request(t, handler, http.MethodGet, test.path)

			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body: %s", response.Code, response.Body.String())
			}
			if got := response.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
				t.Errorf("content type = %q", got)
			}
			compare(t, test.name+".golden", response.Body.String())
		})
	}
}

func TestAttachInfoResponseUsesStableTmuxIDs(t *testing.T) {
	handler := NewHandler(Options{Service: newFakeService()})
	response := request(t, handler, http.MethodPost,
		"/v1/tasks/"+storetest.TaskID.String()+"/attach-info")

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", response.Code, response.Body.String())
	}
	compare(t, "attach-info.golden", response.Body.String())
}

func TestShellResponseUsesStableTmuxIDs(t *testing.T) {
	handler := NewHandler(Options{Service: newFakeService()})
	response := request(t, handler, http.MethodPost,
		"/v1/tasks/"+storetest.TaskID.String()+"/shell")

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", response.Code, response.Body.String())
	}
	compare(t, "shell.golden", response.Body.String())
}

// TestDraftResponseBodies pins the task-draft surface.
//
// The four requests are the preparation lifecycle in order: record a draft,
// edit it, resolve it, and confirm it. Each response is a published surface for
// the same reason the rest are (ADR-027).
func TestDraftResponseBodies(t *testing.T) {
	draft := storetest.DraftID.String()

	tests := []struct {
		name   string
		method string
		path   string
		body   string
		status int
	}{
		{
			name: "draft-created", method: http.MethodPost, path: "/v1/task-drafts",
			body: `{"project_id":"example","title":"Add a scheduled export job",` +
				`"brief":"Export the daily report.","source":{"kind":"prompt"}}`,
			status: http.StatusCreated,
		},
		{
			name: "draft-updated", method: http.MethodPut, path: "/v1/task-drafts/" + draft,
			body: `{"title":"Add a scheduled export job","brief":"Export the daily report.",` +
				`"repositories":[{"repository_id":"core","access":"read_write"}]}`,
			status: http.StatusOK,
		},
		{
			name: "draft-plan", method: http.MethodPost, path: "/v1/task-drafts/" + draft + "/plan",
			status: http.StatusOK,
		},
		{
			name: "draft-launched", method: http.MethodPost, path: "/v1/task-drafts/" + draft + "/launch",
			body:   `{"fingerprint":"0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0"}`,
			status: http.StatusOK,
		},
		{
			name: "draft-cancelled", method: http.MethodDelete, path: "/v1/task-drafts/" + draft,
			status: http.StatusOK,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// A handler per case, so one case's fixture mutation cannot decide
			// what the next one records.
			service := newFakeService()
			service.tasks = append(service.tasks, storetest.Draft())
			handler := NewHandler(Options{Service: service})

			response := requestBody(t, handler, test.method, test.path, test.body)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d, body: %s",
					response.Code, test.status, response.Body.String())
			}
			compare(t, test.name+".golden", response.Body.String())
		})
	}
}

// TestLaunchingWithoutTheDisplayedPlanIsRefused is the transport half of the
// slice 6 criterion that confirming launches the snapshot that was displayed.
//
// The daemon compares the fingerprint; what the transport has to get right is
// that a refusal is a request error the user can act on rather than a failure
// they cannot.
func TestLaunchingWithoutTheDisplayedPlanIsRefused(t *testing.T) {
	service := newFakeService()
	service.tasks = append(service.tasks, storetest.Draft())
	handler := NewHandler(Options{Service: service})

	response := requestBody(t, handler, http.MethodPost,
		"/v1/task-drafts/"+storetest.DraftID.String()+"/launch", `{"fingerprint":""}`)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body: %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "changed after the plan") {
		t.Errorf("body = %s, want the reason the launch was refused", response.Body.String())
	}
}

// TestShellTakesNoCommandFromTheCaller checks that the endpoint carries an
// identifier rather than something to execute.
//
// The daemon runs commands on its owner's behalf, so a body naming a program
// must be rejected rather than ignored: a client that asked for something Feat
// did not do should be told.
func TestShellTakesNoCommandFromTheCaller(t *testing.T) {
	handler := NewHandler(Options{Service: newFakeService()})

	response := requestBody(t, handler, http.MethodPost,
		"/v1/tasks/"+storetest.TaskID.String()+"/shell", `{"program":"/bin/sh"}`)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body: %s", response.Code, response.Body.String())
	}
}

// compare checks output against a golden file.
func compare(t *testing.T, name, got string) {
	t.Helper()

	golden := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("creating testdata: %v", err)
		}
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatalf("writing %s: %v", golden, err)
		}
		return
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("reading %s: %v\nRun: go test ./internal/api -update", golden, err)
	}
	if got != string(want) {
		t.Errorf("payload changed.\n\ngot:\n%s\nwant:\n%s\n"+
			"The wire format is a published surface. If this change is intended, run:\n"+
			"\tgo test ./internal/api -update", got, want)
	}
}

func TestErrorResponses(t *testing.T) {
	handler := NewHandler(Options{Service: newFakeService()})

	tests := []struct {
		name   string
		method string
		path   string
		status int
		code   string
	}{
		{"unknown path", http.MethodGet, "/v1/nothing", http.StatusNotFound, CodeNotFound},
		{"unversioned path", http.MethodGet, "/health", http.StatusNotFound, CodeNotFound},
		{"missing project", http.MethodGet, "/v1/projects/absent", http.StatusNotFound, CodeNotFound},
		{
			"malformed project id", http.MethodGet, "/v1/projects/Not%20An%20Id",
			http.StatusBadRequest, CodeInvalid,
		},
		{
			"malformed task id", http.MethodGet, "/v1/tasks/not-a-uuid",
			http.StatusBadRequest, CodeInvalid,
		},
		{
			"malformed attach task id", http.MethodPost, "/v1/tasks/not-a-uuid/attach-info",
			http.StatusBadRequest, CodeInvalid,
		},
		{"wrong method", http.MethodPost, "/v1/health", http.StatusMethodNotAllowed, CodeNotAllowed},
		{"wrong method on a list", http.MethodDelete, "/v1/tasks", http.StatusMethodNotAllowed, CodeNotAllowed},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := request(t, handler, test.method, test.path)

			if response.Code != test.status {
				t.Errorf("status = %d, want %d, body: %s", response.Code, test.status, response.Body.String())
			}

			var envelope struct {
				Error Error `json:"error"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("the error body is not JSON: %v\n%s", err, response.Body.String())
			}
			if envelope.Error.Code != test.code {
				t.Errorf("code = %q, want %q", envelope.Error.Code, test.code)
			}
			if envelope.Error.Message == "" {
				t.Error("the error carries no message")
			}
		})
	}
}

// TestEveryTaskEndpointTakesTheKeyTheListsPrint checks the slice 13 acceptance
// criterion at the transport, where it applies to the whole command surface at
// once.
//
// The endpoints below are what `feat attach`, `feat review`, `feat runtime`, and
// `feat task cleanup` call, and every one of them took an identifier no list printed.
// They come through one resolution point, so the check is that each of them
// reached the daemon with the identifier the key abbreviates.
func TestEveryTaskEndpointTakesTheKeyTheListsPrint(t *testing.T) {
	key := storetest.TaskID.Key().String()

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{"task", http.MethodGet, "/v1/tasks/" + key},
		{"attach", http.MethodPost, "/v1/tasks/" + key + "/attach-info"},
		{"shell", http.MethodPost, "/v1/tasks/" + key + "/shell"},
		{"runtime", http.MethodPost, "/v1/tasks/" + key + "/runtime/status"},
		{"runtime logs", http.MethodPost, "/v1/tasks/" + key + "/runtime/logs-info"},
		{"review", http.MethodPost, "/v1/tasks/" + key + "/review/observe"},
		{"cleanup plan", http.MethodPost, "/v1/tasks/" + key + "/cleanup/plan"},
		{"resume", http.MethodPost, "/v1/tasks/" + key + "/resume"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := NewHandler(Options{Service: newFakeService()})

			response := requestBody(t, handler, test.method, test.path, "{}")

			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body: %s", response.Code, response.Body.String())
			}
		})
	}

	// A prefix shorter than the key works for the same reason, and so does the
	// whole identifier, which is the path the dashboard takes.
	handler := NewHandler(Options{Service: newFakeService()})
	for _, ref := range []string{"7f3a", storetest.TaskID.String()} {
		response := request(t, handler, http.MethodGet, "/v1/tasks/"+ref)
		if response.Code != http.StatusOK {
			t.Errorf("%q: status = %d, body: %s", ref, response.Code, response.Body.String())
		}
	}
}

// TestAnAmbiguousTaskReferenceIsRefusedRatherThanChosen checks that the transport
// carries the refusal rather than one of the candidates, and that what it carries
// is actable on.
func TestAnAmbiguousTaskReferenceIsRefusedRatherThanChosen(t *testing.T) {
	service := newFakeService()
	second, err := domain.NewTask(
		"7fb90cd4-1e2f-4a3b-8c4d-5e6f7a8b9c0d", storetest.ProjectID, "another task",
		domain.TaskSource{Kind: domain.SourcePrompt}, fixedTime)
	if err != nil {
		t.Fatalf("creating the second task: %v", err)
	}
	service.tasks = append(service.tasks, second)
	handler := NewHandler(Options{Service: service})

	response := request(t, handler, http.MethodGet, "/v1/tasks/7f")

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body: %s", response.Code, response.Body.String())
	}
	var envelope struct {
		Error Error `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("the error body is not JSON: %v\n%s", err, response.Body.String())
	}
	if envelope.Error.Code != CodeInvalid {
		t.Errorf("code = %q, want %q", envelope.Error.Code, CodeInvalid)
	}
	for _, want := range []string{
		storetest.TaskID.Key().String(), second.Key().String(), storetest.ProjectID.String(),
	} {
		if !contains(envelope.Error.Message, want) {
			t.Errorf("the refusal does not name %q: %s", want, envelope.Error.Message)
		}
	}
}

// TestATaskReferenceThatNamesNothingIsNotFound keeps the two failures apart. A
// reference nothing answers to is a missing task, not a malformed request, and a
// client that branches on the code should be able to tell them apart.
func TestATaskReferenceThatNamesNothingIsNotFound(t *testing.T) {
	handler := NewHandler(Options{Service: newFakeService()})

	response := request(t, handler, http.MethodGet, "/v1/tasks/deadbeef")

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body: %s", response.Code, response.Body.String())
	}
}

// TestMethodNotAllowedNamesTheAllowedMethod keeps the 405 useful rather than
// merely correct.
func TestMethodNotAllowedNamesTheAllowedMethod(t *testing.T) {
	handler := NewHandler(Options{Service: newFakeService()})

	response := request(t, handler, http.MethodPost, "/v1/tasks")

	if got := response.Header().Get("Allow"); got != "GET, HEAD" {
		t.Errorf("Allow = %q, want %q", got, "GET, HEAD")
	}
}

// TestServiceFailureIsNotExplainedToTheClient checks that an unexplained failure
// becomes a plain 500. The daemon's log holds the cause; a client cannot act on
// it and should not be handed internals.
func TestServiceFailureIsNotExplainedToTheClient(t *testing.T) {
	service := newFakeService()
	service.failWith = errors.New("the disk is on fire in /srv/secret-project")
	handler := NewHandler(Options{Service: service})

	response := request(t, handler, http.MethodGet, "/v1/projects")

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", response.Code)
	}
	if body := response.Body.String(); contains(body, "secret-project") {
		t.Errorf("the response repeats internal detail:\n%s", body)
	}
}

// TestPanicDoesNotEscapeTheHandler covers the property that keeps one bad
// request from ending the sessions of every running task: the daemon serves
// several clients and owns all task state.
func TestPanicDoesNotEscapeTheHandler(t *testing.T) {
	service := newFakeService()
	service.panicWith = "a nil map in a handler"
	handler := NewHandler(Options{Service: service})

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("the panic escaped the handler: %v", recovered)
		}
	}()

	response := request(t, handler, http.MethodGet, "/v1/health")

	if response.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", response.Code)
	}
	if !contains(response.Body.String(), CodeInternal) {
		t.Errorf("body does not carry the internal error code:\n%s", response.Body.String())
	}
}

// TestDegradedHealthIsReportedNotFailed pins the difference between "the daemon
// is broken" and "the daemon cannot read something".
func TestDegradedHealthIsReportedNotFailed(t *testing.T) {
	service := newFakeService()
	service.health.Degraded = "the state directory could not be listed"
	handler := NewHandler(Options{Service: service})

	response := request(t, handler, http.MethodGet, "/v1/health")

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}

	var health Health
	if err := json.Unmarshal(response.Body.Bytes(), &health); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if health.Status != StatusDegraded {
		t.Errorf("status = %q, want %q", health.Status, StatusDegraded)
	}
	if health.Detail != service.health.Degraded {
		t.Errorf("detail = %q, want %q", health.Detail, service.health.Degraded)
	}
}

// TestTaskPayloadCarriesNoUnmappedField checks the mapping rather than the
// fields somebody remembered: the fixtures populate every domain field, so a
// field the DTO forgets shows up as a zero value here.
func TestTaskPayloadCarriesNoUnmappedField(t *testing.T) {
	handler := NewHandler(Options{Service: newFakeService()})

	response := request(t, handler, http.MethodGet, "/v1/tasks/"+storetest.TaskID.String())

	var task Task
	if err := json.Unmarshal(response.Body.Bytes(), &task); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if unpopulated := storetest.UnpopulatedFields(task); len(unpopulated) > 0 {
		t.Errorf("the task payload leaves fields empty that the fixture populates: %v", unpopulated)
	}
}

func TestHeadRequestsAreAnswered(t *testing.T) {
	handler := NewHandler(Options{Service: newFakeService()})

	response := request(t, handler, http.MethodHead, "/v1/health")

	if response.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", response.Code)
	}
}

func contains(haystack, needle string) bool { return strings.Contains(haystack, needle) }
