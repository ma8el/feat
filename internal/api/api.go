package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ma8el/feat/internal/domain"
)

// Service is what the API needs from the daemon.
//
// Most methods are reads. Registration and live-target reconciliation may
// update persistent observations, and both go through the daemon for the reason
// ADR-008 gives: the daemon is the only writer of persistent state. The
// interface is declared here so that the transport can be tested with a fake
// and the daemon can implement it without a cycle.
type Service interface {
	// Health reports what the daemon knows about itself. It answers even when
	// part of the state is unreadable, saying so in the report.
	Health(ctx context.Context) (HealthReport, error)
	// Projects returns every registered project, ordered by identifier.
	Projects(ctx context.Context) ([]*domain.Project, error)
	// Project returns one project, or an error matching ErrNotFound.
	Project(ctx context.Context, id domain.ProjectID) (*domain.Project, error)
	// RegisterProject reads the project's configuration file, validates it, and
	// records the project. It is idempotent: registering a project that is
	// already registered re-reads its configuration and updates the record,
	// which is what a user who edited their YAML expects.
	//
	// It returns an error matching ErrNotFound when no configuration file
	// exists, and one matching ErrInvalid when the file does not validate.
	RegisterProject(ctx context.Context, id domain.ProjectID) (RegisteredProject, error)
	// Tasks returns every task of every project, ordered by project and task.
	Tasks(ctx context.Context) ([]*domain.Task, error)
	// Task returns one task addressed by task identifier alone, resolving the
	// owning project, or an error matching ErrNotFound (ADR-027).
	Task(ctx context.Context, id domain.TaskID) (*domain.Task, error)
	// ResolveTask turns a reference a user typed — a task's short key, its whole
	// identifier, or any prefix of one — into the identifier it names.
	//
	// A reference that names more than one task returns an error matching
	// ErrInvalid rather than one of them, and one that names none returns an
	// error matching ErrNotFound. Both say where a valid value is printed.
	ResolveTask(ctx context.Context, ref domain.TaskRef) (domain.TaskID, error)
	// Verification returns what the agent reported about its own checks, and
	// false when it has reported none.
	//
	// It is separate from the task because it lives in the review aggregate,
	// where docs/03-domain-model.md puts an agent-reported completion summary
	// and agent-reported checks.
	Verification(ctx context.Context, id domain.TaskID) (Verification, bool, error)
	// CreateDraft records a new task draft and creates nothing else. No
	// worktree, branch, terminal, or container exists until the draft is
	// confirmed (FR-TASK-003).
	CreateDraft(ctx context.Context, request DraftRequest) (*domain.Task, error)
	// UpdateDraft replaces a draft's title, brief, and repository selection.
	UpdateDraft(ctx context.Context, id domain.TaskID, request DraftUpdate) (*domain.Task, error)
	// PlanDraft resolves every selected repository's base and proposes the
	// branches and worktree paths the task would receive, creating nothing. The
	// fingerprint it returns is what launching carries back.
	PlanDraft(ctx context.Context, id domain.TaskID) (ResolvedDraft, error)
	// LaunchDraft confirms a draft and creates what the fingerprint describes.
	// A draft that changed since the plan was displayed is refused.
	LaunchDraft(ctx context.Context, id domain.TaskID, fingerprint string) (*domain.Task, error)
	// CancelDraft abandons a draft, archiving the record.
	CancelDraft(ctx context.Context, id domain.TaskID) (*domain.Task, error)
	// TerminalFrame returns one rendered view of a task's pane, after setting
	// the pane to the size the caller will draw it into.
	//
	// It is display and never a source of truth: the daemon reads nothing out of
	// the bytes it returns, and every task state continues to come from provider
	// hooks (ADR-042). A caller that wants a real terminal, with scrollback and a
	// mouse, attaches instead.
	TerminalFrame(ctx context.Context, id domain.TaskID, view TerminalView) (TerminalFrame, error)
	// SendTerminalInput delivers keys or typed text to a task's pane.
	//
	// This is a write to a running agent, so the request is validated rather than
	// trusted, and the caller names a task rather than a pane: resolving which
	// pane belongs to a task is the daemon's, as every other tmux operation is.
	SendTerminalInput(ctx context.Context, id domain.TaskID, input TerminalInput) error
	// AttachInfo resolves a task's live, tagged tmux target. The client uses the
	// returned stable IDs to attach its own terminal to native tmux.
	AttachInfo(ctx context.Context, id domain.TaskID) (AttachInfo, error)
	// OpenShell creates or finds the task's tagged shell pane and returns its
	// target. The daemon builds the command; the request carries an identifier
	// and nothing to execute.
	OpenShell(ctx context.Context, id domain.TaskID) (AttachInfo, error)
	// Runtime performs one manual application-runtime action and reports what it
	// observed. Every action is a user's explicit request: v0 starts application
	// services only when asked, and no workflow transition reaches any of them
	// (FR-RUN-005).
	Runtime(ctx context.Context, id domain.TaskID, action RuntimeAction) (RuntimeResult, error)
	// RuntimeLogs returns the host command that opens the task's normal Compose
	// logs. Feat does not aggregate or persist them (FR-RUN-006).
	RuntimeLogs(ctx context.Context, id domain.TaskID) (RuntimeCommand, error)
	// Review performs one review action and returns what the task's review
	// shows: every repository compared against its own recorded base commit,
	// the check results with who produced each one, and the configured external
	// commands expanded for this task.
	//
	// No action starts, stops, or removes anything. Approval is a statement
	// about the work, and the runtime the user was testing it in is theirs
	// (FR-REV-004, ADR-034).
	Review(ctx context.Context, id domain.TaskID, action ReviewAction) (ReviewResult, error)
	// Reconciliation returns the most recent reconciliation pass without
	// running one, and false when none has run.
	Reconciliation() (Reconciliation, bool)
	// Reconcile compares every persisted task with what the machine has and
	// returns what it found. It repairs, restarts, and adopts nothing: every
	// action a finding suggests is one the user takes (FR-STATE-003).
	Reconcile(ctx context.Context) (Reconciliation, error)
	// CleanupPlan resolves the exact set of resources one task owns, removing
	// nothing. The token it returns is what an execution carries back
	// (FR-CLEAN-001).
	CleanupPlan(ctx context.Context, id domain.TaskID) (CleanupPlan, error)
	// Cleanup removes exactly the classes a selection names, having re-resolved
	// the plan and checked that the confirmations cover what is true now.
	Cleanup(ctx context.Context, id domain.TaskID, selection CleanupSelection) (CleanupResult, error)
	// Resume continues a task's recorded agent session in a new terminal.
	//
	// It is an offered recovery and never an automatic restart: nothing in
	// reconciliation reaches it, and it continues the provider session the task
	// recorded rather than opening an empty one (ADR-032, ADR-037).
	Resume(ctx context.Context, id domain.TaskID) (*domain.Task, error)
	// Stop stops the environment a task's agent session runs in, keeping the
	// containers so that a resume starts the same ones again.
	//
	// It is the only way to stop a task's agent short of removing it, and it is
	// reversible by exactly one act: resuming the session that owns the
	// environment (ADR-057).
	Stop(ctx context.Context, id domain.TaskID) (*domain.Task, error)
	// Resources returns the most recent resource sample.
	//
	// It reads what a background sampler collected rather than taking a sample:
	// asking the container runtime what it is using costs between one and two
	// seconds, and a metric that could slow a request would eventually be a
	// metric that failed one. Sampling is observational and never blocks anything
	// (FR-UI-005, ADR-035).
	Resources(ctx context.Context) (ResourceReport, error)
	// Subscribe returns the event stream for one client. The channel is closed
	// when the context ends, or when the subscriber fell too far behind, which
	// the caller reports as a lost stream.
	Subscribe(ctx context.Context) (<-chan Event, error)
}

// Limits applied to every request.
const (
	// maxRequestBody bounds a request body. The limit was in place before the
	// first endpoint that read one.
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
	mux.Handle("/v1/projects", route(map[string]http.HandlerFunc{
		http.MethodGet:  server.projects,
		http.MethodPost: server.registerProject,
	}))
	mux.Handle("/v1/projects/{project_id}", get(server.project))
	mux.Handle("/v1/resources", get(server.resources))
	mux.Handle("/v1/tasks", get(server.tasks))
	mux.Handle("/v1/tasks/{task_id}", get(server.task))
	mux.Handle("/v1/tasks/{task_id}/attach-info", route(map[string]http.HandlerFunc{
		http.MethodPost: server.attachInfo,
	}))
	mux.Handle("/v1/tasks/{task_id}/shell", route(map[string]http.HandlerFunc{
		http.MethodPost: server.shell,
	}))
	// A frame is a POST because asking for one sets the pane's size, which
	// changes something; the input endpoint is separate because sending keys to
	// an agent is a different capability from looking at one.
	mux.Handle("/v1/tasks/{task_id}/terminal", route(map[string]http.HandlerFunc{
		http.MethodPost: server.terminalFrame,
	}))
	mux.Handle("/v1/tasks/{task_id}/terminal/input", route(map[string]http.HandlerFunc{
		http.MethodPost: server.terminalInput,
	}))
	// One endpoint per manual action, because each is a separate thing a user
	// asks for and the path is what names it. Destroy is the only one carrying a
	// body, and what it carries is the confirmation.
	mux.Handle("/v1/tasks/{task_id}/runtime/{action}", route(map[string]http.HandlerFunc{
		http.MethodPost: server.runtime,
	}))
	mux.Handle("/v1/tasks/{task_id}/runtime/logs-info", route(map[string]http.HandlerFunc{
		http.MethodPost: server.runtimeLogs,
	}))
	// The review actions follow the same rule as the runtime's: an action Feat
	// does not perform is a path that does not exist, rather than a request the
	// daemon has to interpret. Observing is a POST because it observes and
	// records what it observed.
	mux.Handle("/v1/tasks/{task_id}/review/{action}", route(map[string]http.HandlerFunc{
		http.MethodPost: server.review,
	}))
	// The two cleanup endpoints are plan and execute rather than one request,
	// for the reason preparation is plan and launch: what is removed has to be
	// what the user read, and a plan token is how the second request proves it
	// (FR-CLEAN-001, ADR-037).
	mux.Handle("/v1/tasks/{task_id}/cleanup/plan", route(map[string]http.HandlerFunc{
		http.MethodPost: server.cleanupPlan,
	}))
	mux.Handle("/v1/tasks/{task_id}/cleanup/execute", route(map[string]http.HandlerFunc{
		http.MethodPost: server.cleanupExecute,
	}))
	// Resuming a dead agent session is its own path, because it is its own thing
	// a user asks for. Nothing else reaches it: reconciliation reports that a
	// session can be resumed and never resumes one.
	mux.Handle("/v1/tasks/{task_id}/resume", route(map[string]http.HandlerFunc{
		http.MethodPost: server.resume,
	}))
	// Stopping one is the inverse and is a sibling rather than an action under a
	// noun of its own: there is no third verb for an agent environment, because
	// coming back is always a resume of the session that owns it (ADR-057).
	mux.Handle("/v1/tasks/{task_id}/stop", route(map[string]http.HandlerFunc{
		http.MethodPost: server.stop,
	}))
	// Reading the last pass and running a new one are the same resource: GET
	// answers with what was found, and POST looks again. It is the shape runtime
	// status uses — an observation is a POST because it records what it
	// observed.
	mux.Handle("/v1/reconciliation", route(map[string]http.HandlerFunc{
		http.MethodGet:  server.reconciliation,
		http.MethodPost: server.reconcile,
	}))
	mux.Handle("/v1/task-drafts", route(map[string]http.HandlerFunc{
		http.MethodPost: server.createDraft,
	}))
	mux.Handle("/v1/task-drafts/{draft_id}", route(map[string]http.HandlerFunc{
		http.MethodPut:    server.updateDraft,
		http.MethodDelete: server.cancelDraft,
	}))
	mux.Handle("/v1/task-drafts/{draft_id}/plan", route(map[string]http.HandlerFunc{
		http.MethodPost: server.planDraft,
	}))
	mux.Handle("/v1/task-drafts/{draft_id}/launch", route(map[string]http.HandlerFunc{
		http.MethodPost: server.launchDraft,
	}))
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

// registerProject records a project from its configuration file.
//
// The request carries the project identifier and nothing else. The daemon reads
// the file from the configuration directory it resolved for itself, so no
// caller-supplied filesystem path crosses the socket, and the file Feat
// validates is the one at the documented location rather than one a client
// pointed it at (docs/05-security-model.md, local daemon API).
func (s *server) registerProject(w http.ResponseWriter, r *http.Request) {
	var request RegisterProject
	if err := decodeBody(r, &request); err != nil {
		s.fail(w, r, err)
		return
	}

	id := domain.ProjectID(request.ProjectID)
	if err := id.Validate(); err != nil {
		s.fail(w, r, err)
		return
	}

	registered, err := s.service.RegisterProject(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	// 201 for a project Feat did not have, 200 for one it re-read. A user who
	// runs the command twice should be able to tell which happened.
	status := http.StatusOK
	if registered.Created {
		status = http.StatusCreated
	}
	writeJSON(w, status, Registration{
		Project: newProject(registered.Project),
		Created: registered.Created,
	})
}

// resources returns the most recent resource sample.
//
// It is a GET because it changes nothing, and it changes nothing because it
// reads a sample somebody else took. A metric that a request collected would be
// a metric a request could be slowed or failed by, and metrics are the one thing
// in Feat that must never do either (FR-UI-005).
func (s *server) resources(w http.ResponseWriter, r *http.Request) {
	report, err := s.service.Resources(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if report.Tasks == nil {
		report.Tasks = []TaskResources{}
	}
	if report.Notes == nil {
		report.Notes = []string{}
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *server) tasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.service.Tasks(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// One lookup per task rather than one for the list: the review lives in its
	// own aggregate, and the number of tasks a user runs at once is small by
	// construction. A task whose verification cannot be read is rendered without
	// one, because a dashboard that failed entirely over a check summary would
	// be worse than a dashboard missing a column.
	verifications := make(map[string]Verification, len(tasks))
	for _, task := range tasks {
		reported, ok, err := s.service.Verification(r.Context(), task.ID)
		if err != nil {
			s.logger.WarnContext(r.Context(), "reading a task's reported verification",
				slog.String("task", task.ID.String()), slog.Any("error", err))
			continue
		}
		if ok {
			verifications[task.ID.String()] = reported
		}
	}
	writeJSON(w, http.StatusOK, newTasks(tasks, verifications))
}

func (s *server) task(w http.ResponseWriter, r *http.Request) {
	id, ok := s.taskID(w, r, "task_id")
	if !ok {
		return
	}

	task, err := s.service.Task(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	var verification *Verification
	if reported, ok, err := s.service.Verification(r.Context(), id); err != nil {
		s.logger.WarnContext(r.Context(), "reading a task's reported verification",
			slog.String("task", id.String()), slog.Any("error", err))
	} else if ok {
		verification = &reported
	}
	writeJSON(w, http.StatusOK, newTask(task, verification))
}

// terminalFrame renders one view of a task's pane.
func (s *server) terminalFrame(w http.ResponseWriter, r *http.Request) {
	id, ok := s.taskID(w, r, "task_id")
	if !ok {
		return
	}
	var view TerminalView
	if err := decodeBody(r, &view); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := view.Validate(); err != nil {
		s.fail(w, r, err)
		return
	}

	frame, err := s.service.TerminalFrame(r.Context(), id, view)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, frame)
}

// terminalInput delivers what a user typed to a task's pane.
func (s *server) terminalInput(w http.ResponseWriter, r *http.Request) {
	id, ok := s.taskID(w, r, "task_id")
	if !ok {
		return
	}
	var input TerminalInput
	if err := decodeBody(r, &input); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := input.Validate(); err != nil {
		s.fail(w, r, err)
		return
	}

	if err := s.service.SendTerminalInput(r.Context(), id, input); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) attachInfo(w http.ResponseWriter, r *http.Request) {
	id, ok := s.taskID(w, r, "task_id")
	if !ok {
		return
	}
	if err := decodeEmptyBody(r); err != nil {
		s.fail(w, r, err)
		return
	}

	info, err := s.service.AttachInfo(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// shell opens or finds the task's shell pane.
//
// The request carries a task identifier and nothing else. The daemon decides
// which program runs and where, because a caller-supplied command would be a
// command the daemon runs on its owner's behalf.
func (s *server) shell(w http.ResponseWriter, r *http.Request) {
	id, ok := s.taskID(w, r, "task_id")
	if !ok {
		return
	}
	if err := decodeEmptyBody(r); err != nil {
		s.fail(w, r, err)
		return
	}

	info, err := s.service.OpenShell(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// cleanupPlan resolves what a task owns without removing any of it.
func (s *server) cleanupPlan(w http.ResponseWriter, r *http.Request) {
	id, ok := s.taskID(w, r, "task_id")
	if !ok {
		return
	}
	if err := decodeEmptyBody(r); err != nil {
		s.fail(w, r, err)
		return
	}

	plan, err := s.service.CleanupPlan(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if plan.Classes == nil {
		plan.Classes = []CleanupClass{}
	}
	writeJSON(w, http.StatusOK, plan)
}

// cleanupExecute removes exactly the classes a selection names.
//
// The body carries a plan token, the classes chosen, and the warnings the user
// accepted — identifiers and confirmations, never a path. A request with no
// token is refused here rather than reaching the resolver, because "which plan
// was this" is the one question a destructive request must not leave open.
func (s *server) cleanupExecute(w http.ResponseWriter, r *http.Request) {
	id, ok := s.taskID(w, r, "task_id")
	if !ok {
		return
	}

	var selection CleanupSelection
	if err := decodeBody(r, &selection); err != nil && !errors.Is(err, io.EOF) {
		s.fail(w, r, err)
		return
	}
	if selection.Token == "" {
		s.fail(w, r, fmt.Errorf("%w: a cleanup must carry the token of the plan it was shown. "+
			"Ask for the plan first; nothing was removed", ErrInvalid))
		return
	}

	result, err := s.service.Cleanup(r.Context(), id, selection)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, NewCleanupStatus(result))
}

// resume continues a task's recorded agent session.
func (s *server) resume(w http.ResponseWriter, r *http.Request) {
	id, ok := s.taskID(w, r, "task_id")
	if !ok {
		return
	}
	if err := decodeEmptyBody(r); err != nil {
		s.fail(w, r, err)
		return
	}

	task, err := s.service.Resume(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, newTask(task, nil))
}

// stop stops the environment a task's agent session runs in.
func (s *server) stop(w http.ResponseWriter, r *http.Request) {
	id, ok := s.taskID(w, r, "task_id")
	if !ok {
		return
	}
	if err := decodeEmptyBody(r); err != nil {
		s.fail(w, r, err)
		return
	}

	task, err := s.service.Stop(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, newTask(task, nil))
}

// reconciliation returns the most recent pass without running one.
func (s *server) reconciliation(w http.ResponseWriter, r *http.Request) {
	report, ok := s.service.Reconciliation()
	if !ok {
		writeJSON(w, http.StatusOK, emptyReconciliation())
		return
	}
	writeJSON(w, http.StatusOK, fillReconciliation(report))
}

// reconcile looks again and returns what it found.
func (s *server) reconcile(w http.ResponseWriter, r *http.Request) {
	if err := decodeEmptyBody(r); err != nil {
		s.fail(w, r, err)
		return
	}
	report, err := s.service.Reconcile(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, fillReconciliation(report))
}

// emptyReconciliation is what a daemon that has not run a pass reports.
func emptyReconciliation() Reconciliation {
	return Reconciliation{
		Findings: []ReconciliationFinding{},
		Problems: []ReconciliationProblem{},
	}
}

// fillReconciliation makes the collections explicit, so a client reads an empty
// list rather than a null.
func fillReconciliation(report Reconciliation) Reconciliation {
	report.Ran = true
	if report.Findings == nil {
		report.Findings = []ReconciliationFinding{}
	}
	if report.Problems == nil {
		report.Problems = []ReconciliationProblem{}
	}
	return report
}

// runtime performs one manual application-runtime action.
//
// The action is a path segment rather than a field, so an unknown one is a 404
// on an endpoint that does not exist rather than a request that reached the
// daemon carrying an instruction it had to interpret. Every action but destroy
// takes no body at all: what a task's services are is the daemon's to resolve
// from the project's configuration, never the caller's to supply.
func (s *server) runtime(w http.ResponseWriter, r *http.Request) {
	id, ok := s.taskID(w, r, "task_id")
	if !ok {
		return
	}

	action := RuntimeAction(r.PathValue("action"))
	if !action.Valid() {
		writeJSON(w, http.StatusNotFound, errorEnvelope{Error: Error{
			Code: CodeNotFound,
			Message: "no runtime action " + strconv.Quote(string(action)) + "; the actions are create, " +
				"start, stop, status, destroy, and logs-info",
		}})
		return
	}

	if action == RuntimeDestroy {
		var request DestroyRuntime
		// An absent body is a request that confirms nothing, which is refused
		// below with the message that explains what confirmation means here. A
		// body that is present and malformed is still an error: a client that
		// tried to say something should be told it was not understood.
		if err := decodeBody(r, &request); err != nil && !errors.Is(err, io.EOF) {
			s.fail(w, r, err)
			return
		}
		if !request.Confirm {
			// A request that removes something says that somebody meant it, which
			// is the rule ADR-031 applied to launching a task: what is created is
			// what was displayed, and what is removed is what was confirmed.
			s.fail(w, r, fmt.Errorf("%w: destroying a task's application services needs an explicit "+
				"confirmation. Volumes are retained either way", ErrInvalid))
			return
		}
	} else if err := decodeEmptyBody(r); err != nil {
		s.fail(w, r, err)
		return
	}

	result, err := s.service.Runtime(r.Context(), id, action)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	services := result.Services
	if services == nil {
		services = []RuntimeService{}
	}
	notes := result.Notes
	if notes == nil {
		notes = []string{}
	}
	writeJSON(w, http.StatusOK, RuntimeStatus{
		Task:     newTask(result.Task, nil),
		Services: services,
		Notes:    notes,
	})
}

// review performs one review action and returns what the task's review shows.
func (s *server) review(w http.ResponseWriter, r *http.Request) {
	id, ok := s.taskID(w, r, "task_id")
	if !ok {
		return
	}

	action := ReviewAction(r.PathValue("action"))
	if !action.Valid() {
		writeJSON(w, http.StatusNotFound, errorEnvelope{Error: Error{
			Code: CodeNotFound,
			Message: "no review action " + strconv.Quote(string(action)) + "; the actions are observe, " +
				"approve, changes, and verify",
		}})
		return
	}
	// No review action carries a body. The decision is in the path, and a
	// request that could name a command or a path would be a caller deciding
	// what the daemon runs (ADR-034's rule for the shell endpoint).
	if err := decodeEmptyBody(r); err != nil {
		s.fail(w, r, err)
		return
	}

	result, err := s.service.Review(r.Context(), id, action)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, NewReviewStatus(result))
}

// runtimeLogs returns the command that opens the task's normal Compose logs.
func (s *server) runtimeLogs(w http.ResponseWriter, r *http.Request) {
	id, ok := s.taskID(w, r, "task_id")
	if !ok {
		return
	}
	if err := decodeEmptyBody(r); err != nil {
		s.fail(w, r, err)
		return
	}

	command, err := s.service.RuntimeLogs(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, command)
}

// createDraft records a new task draft.
//
// Nothing on the host is created here. FR-TASK-003 puts every worktree, branch,
// terminal, and container behind the confirmation that launch carries.
func (s *server) createDraft(w http.ResponseWriter, r *http.Request) {
	var request CreateDraft
	if err := decodeBody(r, &request); err != nil {
		s.fail(w, r, err)
		return
	}

	project := domain.ProjectID(request.ProjectID)
	if err := project.Validate(); err != nil {
		s.fail(w, r, err)
		return
	}

	draft, err := s.service.CreateDraft(r.Context(), DraftRequest{
		Project: project,
		Title:   request.Title,
		Brief:   request.Brief,
		Source: domain.TaskSource{
			Kind:      domain.SourceKind(request.Source.Kind),
			Reference: request.Source.Reference,
		},
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, newTask(draft, nil))
}

func (s *server) updateDraft(w http.ResponseWriter, r *http.Request) {
	id, ok := s.taskID(w, r, "draft_id")
	if !ok {
		return
	}
	var request UpdateDraft
	if err := decodeBody(r, &request); err != nil {
		s.fail(w, r, err)
		return
	}

	selection := make([]DraftSelection, 0, len(request.Repositories))
	for _, selected := range request.Repositories {
		repository := domain.RepositoryID(selected.RepositoryID)
		if err := repository.Validate(); err != nil {
			s.fail(w, r, err)
			return
		}
		selection = append(selection, DraftSelection{
			Repository: repository,
			Access:     domain.TaskAccess(selected.Access),
			Ref:        selected.Ref,
		})
	}

	draft, err := s.service.UpdateDraft(r.Context(), id, DraftUpdate{
		Title:        request.Title,
		Brief:        request.Brief,
		Repositories: selection,
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, newTask(draft, nil))
}

// planDraft resolves the draft's bases and proposes its branches and paths.
//
// It is a request of its own because it fetches: a network call against the
// user's repositories should follow a key they pressed rather than a field they
// edited (ADR-031).
func (s *server) planDraft(w http.ResponseWriter, r *http.Request) {
	id, ok := s.taskID(w, r, "draft_id")
	if !ok {
		return
	}

	resolved, err := s.service.PlanDraft(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	notes := resolved.Notes
	if notes == nil {
		// Always a list rather than null, so a client can iterate the response
		// without a nil check.
		notes = []string{}
	}
	writeJSON(w, http.StatusOK, DraftPlan{
		Task:        newTask(resolved.Task, nil),
		Notes:       notes,
		Fingerprint: resolved.Fingerprint,
	})
}

func (s *server) launchDraft(w http.ResponseWriter, r *http.Request) {
	id, ok := s.taskID(w, r, "draft_id")
	if !ok {
		return
	}
	var request LaunchDraft
	if err := decodeBody(r, &request); err != nil {
		s.fail(w, r, err)
		return
	}

	task, err := s.service.LaunchDraft(r.Context(), id, request.Fingerprint)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// A task that has just launched has reported nothing yet.
	writeJSON(w, http.StatusOK, newTask(task, nil))
}

func (s *server) cancelDraft(w http.ResponseWriter, r *http.Request) {
	id, ok := s.taskID(w, r, "draft_id")
	if !ok {
		return
	}

	draft, err := s.service.CancelDraft(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, newTask(draft, nil))
}

// taskID resolves the task a request names.
//
// Every endpoint that takes a task comes through here, drafts included, so there
// is one place where what a user typed becomes a task identifier. A whole
// identifier is used as it stands: it names one task by construction, so
// resolving it would be reading every task to learn what the caller already
// knew, which is the path the dashboard takes on every request it makes.
//
// Anything shorter is resolved by the daemon, and what comes back is an
// identifier it read out of storage rather than anything a caller composed. That
// is a stronger guarantee than the validation it replaces: no value from a
// request reaches a filesystem path at all.
func (s *server) taskID(w http.ResponseWriter, r *http.Request, name string) (domain.TaskID, bool) {
	ref := domain.TaskRef(r.PathValue(name))
	if id, exact := ref.Exact(); exact {
		return id, true
	}

	id, err := s.service.ResolveTask(r.Context(), ref)
	if err != nil {
		s.fail(w, r, err)
		return "", false
	}
	return id, true
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

// get serves one handler on GET, and on HEAD as net/http does, by running the
// handler and discarding the body.
func get(handler http.HandlerFunc) http.Handler {
	return route(map[string]http.HandlerFunc{http.MethodGet: handler})
}

// route dispatches by method.
//
// A method the endpoint does not serve is answered in the same JSON shape as
// every other failure, with the methods it does serve, rather than with
// net/http's plain text.
func route(handlers map[string]http.HandlerFunc) http.Handler {
	allowed := make([]string, 0, len(handlers)+1)
	for method := range handlers {
		allowed = append(allowed, method)
	}
	if _, ok := handlers[http.MethodGet]; ok {
		allowed = append(allowed, http.MethodHead)
	}
	sort.Strings(allowed)
	allow := strings.Join(allowed, ", ")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.Method
		if method == http.MethodHead {
			method = http.MethodGet
		}
		handler, ok := handlers[method]
		if !ok {
			w.Header().Set("Allow", allow)
			writeJSON(w, http.StatusMethodNotAllowed, errorEnvelope{Error: Error{
				Code:    CodeNotAllowed,
				Message: r.Method + " is not allowed on " + r.URL.Path + "; this endpoint serves " + allow,
			}})
			return
		}
		handler(w, r)
	})
}

// decodeBody reads a JSON request body strictly.
//
// A field the daemon does not know is an error rather than a value silently
// ignored, for the same reason the YAML decoder rejects one: a client that
// asked for something Feat did not do should be told, not left to assume.
func decodeBody(r *http.Request, payload any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(payload); err != nil {
		return fmt.Errorf("%w: the request body is not the expected JSON: %w", ErrInvalid, err)
	}
	// A second document in the body means the client sent something other than
	// the one object this endpoint accepts.
	if decoder.More() {
		return fmt.Errorf("%w: the request body carries more than one JSON document", ErrInvalid)
	}
	return nil
}

// decodeEmptyBody accepts a request that carries no instructions.
//
// An endpoint that takes only an identifier must not silently ignore a body
// asking for something else. The daemon runs commands on its owner's behalf, so
// a caller that sent a program to run should be told that Feat does not take
// one rather than left believing it did.
func decodeEmptyBody(r *http.Request) error {
	var empty struct{}

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&empty); err != nil {
		// No body at all means the same thing as an empty one, so `curl -X
		// POST` works as well as the client does.
		if errors.Is(err, io.EOF) {
			return nil
		}
		return fmt.Errorf("%w: this endpoint takes no request body: %w", ErrInvalid, err)
	}
	if decoder.More() {
		return fmt.Errorf("%w: the request body carries more than one JSON document", ErrInvalid)
	}
	return nil
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
//
// At debug level, because every dashboard refresh and every CLI invocation is a
// request: an access log kept at the default level is the one thing here whose
// volume is set by how long the daemon has been running rather than by anything
// happening.
//
// What the default level keeps is the part that reports a problem: fail logs a
// request the daemon could not explain, and recoverPanic logs one that panicked.
// A request rejected as malformed is not logged at either level, because the
// client was told exactly why in the response it got back.
func (s *server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := s.requests.Add(1)
		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: w}

		next.ServeHTTP(recorder, r)

		s.logger.DebugContext(r.Context(), "request",
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
