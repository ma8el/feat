package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/ma8el/feat/internal/domain"
)

// Service is what the API needs from the daemon.
//
// Every method is a read: slice 2 serves state and the event stream, and the
// endpoints that change the world arrive with the slices that can create
// something to change. The interface is declared here so that the transport can
// be tested with a fake and the daemon can implement it without a cycle.
type Service interface {
	// Health reports what the daemon knows about itself. It answers even when
	// part of the state is unreadable, saying so in the report.
	Health(ctx context.Context) (HealthReport, error)
	// Projects returns every registered project, ordered by identifier.
	Projects(ctx context.Context) ([]*domain.Project, error)
	// Project returns one project, or an error matching ErrNotFound.
	Project(ctx context.Context, id domain.ProjectID) (*domain.Project, error)
	// Tasks returns every task of every project, ordered by project and task.
	Tasks(ctx context.Context) ([]*domain.Task, error)
	// Task returns one task addressed by task identifier alone, resolving the
	// owning project, or an error matching ErrNotFound (ADR-027).
	Task(ctx context.Context, id domain.TaskID) (*domain.Task, error)
	// Subscribe returns the event stream for one client. The channel is closed
	// when the context ends, or when the subscriber fell too far behind, which
	// the caller reports as a lost stream.
	Subscribe(ctx context.Context) (<-chan Event, error)
}

// Limits applied to every request.
const (
	// maxRequestBody bounds a request body. Slice 2 serves no body at all; the
	// limit is in place before the first endpoint that reads one.
	maxRequestBody = 1 << 20
	// writeTimeout bounds one non-streaming response. The event stream clears
	// its deadline instead, because an idle stream is not a stuck one.
	writeTimeout = 15 * time.Second
	// defaultHeartbeat is how often an idle event stream writes a comment, so
	// that a client and the daemon both notice a connection that has died.
	defaultHeartbeat = 15 * time.Second
)

// Options configures the handler.
type Options struct {
	// Service serves every request. It is required.
	Service Service
	// Logger records requests and unexpected failures. A nil logger discards
	// them.
	Logger *slog.Logger
	// Heartbeat is the event-stream keepalive interval. Zero uses the default;
	// a negative value disables heartbeats, which only a test wants.
	Heartbeat time.Duration
}

// NewHandler builds the HTTP handler for the local API.
func NewHandler(opts Options) http.Handler {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	if opts.Heartbeat == 0 {
		opts.Heartbeat = defaultHeartbeat
	}
	server := &server{service: opts.Service, logger: opts.Logger, heartbeat: opts.Heartbeat}

	mux := http.NewServeMux()
	// The patterns carry no method, so that a wrong method produces the same
	// JSON error shape as everything else rather than net/http's plain text.
	mux.Handle("/v1/health", get(server.health))
	mux.Handle("/v1/events", get(server.events))
	mux.Handle("/v1/projects", get(server.projects))
	mux.Handle("/v1/projects/{project_id}", get(server.project))
	mux.Handle("/v1/tasks", get(server.tasks))
	mux.Handle("/v1/tasks/{task_id}", get(server.task))
	mux.Handle("/", http.HandlerFunc(notFound))

	return server.recoverPanic(server.logRequests(limitBody(mux)))
}

// server holds the handler's dependencies.
type server struct {
	service   Service
	logger    *slog.Logger
	heartbeat time.Duration

	// requests numbers requests so that a log line about a failure can be
	// matched with the request that caused it.
	requests atomic.Uint64
}

func (s *server) health(w http.ResponseWriter, r *http.Request) {
	report, err := s.service.Health(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}

	status := StatusOK
	if report.Degraded != "" {
		status = StatusDegraded
	}
	writeJSON(w, http.StatusOK, Health{
		Status:     status,
		APIVersion: Version,
		Daemon:     report.Daemon,
		State:      report.State,
		Detail:     report.Degraded,
	})
}

func (s *server) projects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.service.Projects(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, newProjects(projects))
}

func (s *server) project(w http.ResponseWriter, r *http.Request) {
	id := domain.ProjectID(r.PathValue("project_id"))
	// The identifier is validated before it reaches the daemon, so a malformed
	// one can never be joined into a path.
	if err := id.Validate(); err != nil {
		s.fail(w, r, err)
		return
	}

	project, err := s.service.Project(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, newProject(project))
}

func (s *server) tasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.service.Tasks(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, newTasks(tasks))
}

func (s *server) task(w http.ResponseWriter, r *http.Request) {
	id := domain.TaskID(r.PathValue("task_id"))
	if err := id.Validate(); err != nil {
		s.fail(w, r, err)
		return
	}

	task, err := s.service.Task(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, newTask(task))
}

// fail writes the error response and logs the cause of an unexplained failure,
// which the client is not told.
func (s *server) fail(w http.ResponseWriter, r *http.Request, err error) {
	status, code, message := classify(err)
	if status == http.StatusInternalServerError {
		s.logger.ErrorContext(r.Context(), "request failed",
			slog.String("method", r.Method), slog.String("path", r.URL.Path), slog.Any("error", err))
	}
	writeJSON(w, status, errorEnvelope{Error: Error{Code: code, Message: message}})
}

// get rejects any method other than GET, and answers HEAD as net/http does, by
// running the handler and discarding the body.
func get(handler http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			writeJSON(w, http.StatusMethodNotAllowed, errorEnvelope{Error: Error{
				Code:    CodeNotAllowed,
				Message: r.Method + " is not allowed on " + r.URL.Path + "; this endpoint serves GET",
			}})
			return
		}
		handler(w, r)
	})
}

// notFound answers an unrouted path in the same shape as every other error.
func notFound(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusNotFound, errorEnvelope{Error: Error{
		Code:    CodeNotFound,
		Message: "no endpoint at " + r.URL.Path + "; the local API is versioned under /" + Version,
	}})
}

// limitBody bounds every request body.
func limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
		next.ServeHTTP(w, r)
	})
}

// recoverPanic keeps one failing request from taking the daemon down with it.
// The daemon serves several clients and owns every task's state; a nil map in a
// handler must not end the session of three running agents.
func (s *server) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}
			// http.ErrAbortHandler is the documented way to abort a response,
			// and net/http expects to handle it itself.
			if aborted, ok := recovered.(error); ok && errors.Is(aborted, http.ErrAbortHandler) {
				panic(recovered)
			}
			s.logger.ErrorContext(r.Context(), "handler panicked",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Any("panic", recovered))
			writeJSON(w, http.StatusInternalServerError, errorEnvelope{Error: Error{
				Code:    CodeInternal,
				Message: "the daemon could not complete the request",
			}})
		}()
		next.ServeHTTP(w, r)
	})
}

// logRequests records one line per request.
func (s *server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := s.requests.Add(1)
		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: w}

		next.ServeHTTP(recorder, r)

		s.logger.InfoContext(r.Context(), "request",
			slog.Uint64("request", id),
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", recorder.status()),
			slog.Duration("duration", time.Since(started)))
	})
}

// statusRecorder remembers the status code for the access log. It forwards
// Flush and Unwrap so that the event stream still works through it.
type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.code == 0 {
		r.code = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(data []byte) (int, error) {
	if r.code == 0 {
		r.code = http.StatusOK
	}
	return r.ResponseWriter.Write(data)
}

// Unwrap exposes the underlying writer to http.ResponseController, which is how
// the event stream sets deadlines and flushes.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (r *statusRecorder) status() int {
	if r.code == 0 {
		return http.StatusOK
	}
	return r.code
}

// writeJSON writes one response with a bounded write deadline, so that a client
// that stops reading cannot hold a daemon goroutine forever.
func writeJSON(w http.ResponseWriter, status int, payload any) {
	// Indented output costs a few bytes on a local socket and makes both curl
	// and the golden files readable.
	body, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		// A DTO that cannot be marshalled is a programming error, and the
		// response has not been started yet, so the client still gets a body.
		http.Error(w, `{"error":{"code":"internal_error","message":"the daemon could not encode the response"}}`,
			http.StatusInternalServerError)
		return
	}
	body = append(body, '\n')

	controller := http.NewResponseController(w)
	// A writer without deadline support is fine; the deadline is a safeguard,
	// not a requirement.
	_ = controller.SetWriteDeadline(time.Now().Add(writeTimeout))

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	if _, err := w.Write(body); err != nil {
		// The client went away mid-response. There is nothing left to tell it,
		// and the access log already records the request.
		_ = err
	}
}

// describeStream renders the hello detail once, so the handler and its test do
// not describe the policy differently.
func describeStream() string {
	return fmt.Sprintf("connected to the feat daemon; %s does not support resuming a stream, so read current state after reconnecting", Version)
}
