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

	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/store/storetest"
)

var update = flag.Bool("update", false, "rewrite golden files")

// fakeService answers the API from fixtures.
//
// The transport is tested against it rather than against a daemon, which is the
// point of the Service interface: a response shape can be pinned without a
// socket, a store, or a lock.
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
		projects:      []*domain.Project{storetest.Project()},
		tasks:         []*domain.Task{storetest.Task()},
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
