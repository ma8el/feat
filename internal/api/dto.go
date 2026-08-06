package api

import (
	"time"

	"github.com/ma8el/feat/internal/domain"
)

// Version is the API version this package serves. It appears in the request
// path and in every health response, so a client that talks to an older daemon
// can tell.
const Version = "v1"

// Daemon status values.
const (
	// StatusOK reports a daemon serving requests with everything it needs.
	StatusOK = "ok"
	// StatusDegraded reports a daemon that is serving but could not read part of
	// its state. It is deliberately not an error: a client learns more from a
	// degraded answer than from a failed request.
	StatusDegraded = "degraded"
)

// Health is the response of GET /v1/health.
type Health struct {
	// Status is StatusOK or StatusDegraded.
	Status string `json:"status"`
	// APIVersion is the version of this surface.
	APIVersion string `json:"api_version"`
	// Daemon identifies the process serving the request.
	Daemon Daemon `json:"daemon"`
	// State describes the persistent state the daemon owns.
	State State `json:"state"`
	// Detail explains a degraded status. It is empty when the status is ok.
	Detail string `json:"detail,omitempty"`
}

// Daemon identifies the running daemon process.
type Daemon struct {
	// Version is the build version.
	Version string `json:"version"`
	// Commit is the build commit.
	Commit string `json:"commit"`
	// GoVersion is the toolchain that built it.
	GoVersion string `json:"go_version"`
	// Platform is the operating system and architecture.
	Platform string `json:"platform"`
	// PID is the process identifier, which is also what stops it.
	PID int `json:"pid"`
	// StartedAt is when the process acquired ownership. Clients derive uptime
	// from it rather than reading a second, redundant field.
	StartedAt time.Time `json:"started_at"`
	// Socket is the path the daemon is listening on.
	Socket string `json:"socket"`
	// HostAgent reports that this daemon was started with the opt-in that runs
	// agents on this host even where a project configures a container.
	//
	// It is reported because it changes what a task's isolation actually is, and
	// a boundary that is not there should never have to be inferred from the
	// absence of a message.
	HostAgent bool `json:"host_agent"`
}

// State describes the daemon's persistent state directory.
type State struct {
	// Directory is where snapshots, events, and briefs are stored.
	Directory string `json:"directory"`
	// Projects counts the registered projects.
	Projects int `json:"projects"`
}

// HealthReport is what the daemon knows about itself. The api package wraps it
// into a Health response, because the envelope and its version belong to this
// surface rather than to the daemon.
type HealthReport struct {
	// Daemon identifies the process.
	Daemon Daemon
	// State describes the persistent state.
	State State
	// Degraded explains why the daemon is not fully healthy. An empty value
	// means it is.
	Degraded string
}

// RegisterProject is the body of POST /v1/projects.
//
// It carries an identifier rather than a path or a document: the daemon reads
// the configuration from the directory it resolved, so a client cannot point it
// at another file, and the file that is validated is the one that will be read
// again later.
type RegisterProject struct {
	// ProjectID names the project, and therefore its configuration file.
	ProjectID string `json:"project_id"`
}

// Registration is the response of POST /v1/projects.
type Registration struct {
	// Project is the registered project as it is now recorded.
	Project Project `json:"project"`
	// Created reports whether the project was new. It is false when an already
	// registered project's configuration was re-read.
	Created bool `json:"created"`
}

// RegisteredProject is what the daemon reports after registering a project.
type RegisteredProject struct {
	// Project is the recorded project.
	Project *domain.Project
	// Created reports whether the project was new to Feat.
	Created bool
}

// CreateDraft is the body of POST /v1/task-drafts.
//
// The brief arrives as content rather than as a path. A Markdown import is read
// by the client, so the daemon never opens a file a caller named, which is the
// rule POST /v1/projects follows for the same reason (ADR-028).
type CreateDraft struct {
	// ProjectID names the project the task belongs to.
	ProjectID string `json:"project_id"`
	// Title is the short human-facing name.
	Title string `json:"title"`
	// Brief is the task brief in Markdown. It may be empty while the user is
	// still writing it; launching requires one.
	Brief string `json:"brief"`
	// Source records where the brief came from: a typed prompt or an imported
	// Markdown file.
	Source Source `json:"source"`
}

// UpdateDraft is the body of PUT /v1/task-drafts/{draft_id}.
//
// It replaces the draft's editable shape rather than patching it, because that
// is what the preparation screen holds: a whole draft the user edits and saves.
type UpdateDraft struct {
	Title string `json:"title"`
	Brief string `json:"brief"`
	// Repositories is the repository selection, replacing the previous one.
	Repositories []DraftRepository `json:"repositories"`
}

// DraftRepository is one repository's selected part in a task.
type DraftRepository struct {
	RepositoryID string `json:"repository_id"`
	// Access is read_write or read_only. A repository the project configures
	// read-only cannot be selected read-write.
	Access string `json:"access"`
	// Ref is the revision to start from, for a project whose base policy is
	// explicit. Every other policy ignores it.
	Ref string `json:"ref,omitempty"`
}

// LaunchDraft is the body of POST /v1/task-drafts/{draft_id}/launch.
type LaunchDraft struct {
	// Fingerprint is the one the plan the user confirmed carried. A draft that
	// changed since then produces a different fingerprint and the launch is
	// refused, so what is created is what was displayed (ADR-031).
	Fingerprint string `json:"fingerprint"`
}

// DraftRequest is what the daemon needs to record a new draft.
//
// Like RegisteredProject, it is the interface's own shape rather than the wire
// shape: the handler validates every identifier before the daemon sees it, so a
// malformed one never reaches a filesystem path (ADR-027).
type DraftRequest struct {
	// Project owns the task.
	Project domain.ProjectID
	// Title is the short human-facing name.
	Title string
	// Brief is the task brief in Markdown.
	Brief string
	// Source records where the brief came from.
	Source domain.TaskSource
}

// DraftUpdate replaces a draft's editable shape.
type DraftUpdate struct {
	Title string
	Brief string
	// Repositories is the repository selection, replacing the previous one.
	Repositories []DraftSelection
}

// DraftSelection is one repository's selected part in a task.
type DraftSelection struct {
	// Repository identifies the repository within the project.
	Repository domain.RepositoryID
	// Access is the access the task should have to it.
	Access domain.TaskAccess
	// Ref is the revision an explicit base policy reads.
	Ref string
}

// ResolvedDraft is what the daemon reports after resolving a draft.
type ResolvedDraft struct {
	// Task is the draft as it is now recorded.
	Task *domain.Task
	// Notes are what happened while resolving that did not stop the task.
	Notes []string
	// Fingerprint identifies this exact draft.
	Fingerprint string
}

// DraftPlan is the response of POST /v1/task-drafts/{draft_id}/plan.
type DraftPlan struct {
	// Task is the draft as it is now recorded, carrying every resolved base and
	// every proposed branch and worktree path.
	Task Task `json:"task"`
	// Notes are what happened while resolving that the user should know about
	// without it stopping the task, such as a fetch that failed while an older
	// remote-tracking ref was still available.
	Notes []string `json:"notes"`
	// Fingerprint identifies this exact draft, and is what launching carries
	// back.
	Fingerprint string `json:"fingerprint"`
}

// Project is a registered project.
type Project struct {
	ID                string       `json:"id"`
	Name              string       `json:"name"`
	PrimaryRepository string       `json:"primary_repository"`
	Repositories      []Repository `json:"repositories"`
	CreatedAt         time.Time    `json:"created_at"`
	UpdatedAt         time.Time    `json:"updated_at"`
}

// Repository is one repository of a project.
type Repository struct {
	ID string `json:"id"`
	// Name is the display name.
	Name string `json:"name"`
	// HostPath is the ordinary checkout on the host. Paths are the user's own
	// and are not secrets; `feat doctor` prints resolved paths too.
	HostPath string `json:"host_path"`
	// ContainerPath is where the repository is mounted in a devcontainer, empty
	// for host-native projects.
	ContainerPath string `json:"container_path,omitempty"`
	DefaultBranch string `json:"default_branch"`
	Remote        string `json:"remote"`
	DefaultAccess string `json:"default_access"`
}

// Task is one unit of agent work.
//
// The brief is included in list responses as well as in a single task, because
// v0 lists the tasks of one machine and splitting the shape would mean two
// mappings and two golden files for no observed cost. Slice 6 may split it if
// the dashboard shows that it should.
type Task struct {
	ID string `json:"id"`
	// Key is the human-facing short identifier derived from the ID.
	Key       string `json:"key"`
	ProjectID string `json:"project_id"`
	Title     string `json:"title"`
	Brief     string `json:"brief"`
	Source    Source `json:"source"`
	// Workflow is the product-level state Feat decides.
	Workflow string `json:"workflow"`
	// Attention records whether the user may need to intervene. It is separate
	// from every other dimension on purpose.
	Attention    string           `json:"attention"`
	Repositories []TaskRepository `json:"repositories"`
	// Session is the task's agent session, or null before it is launched.
	Session *Session `json:"session"`
	// Runtime is the task's application runtime, or null when it has none.
	Runtime *Runtime `json:"runtime"`
	// Verification is what the agent reported about its own checks, or null
	// when it has reported none. It is the agent's claim rather than a result
	// anything enforced, which is why the source is part of it.
	Verification *Verification `json:"verification"`
	CreatedAt    time.Time     `json:"created_at"`
	UpdatedAt    time.Time     `json:"updated_at"`
}

// Verification is a task's check results, attributed to whoever produced them.
//
// The attribution is the point. A provider-gated result was enforced and an
// agent-reported one was asserted, and a dashboard that showed them alike would
// tell the user something Feat does not know.
type Verification struct {
	// Source is who produced the results: "agent" for a result the agent
	// claimed, "provider" for one a completion gate enforced. Only "agent"
	// occurs in v0.1.
	Source string `json:"source"`
	// Passed, Failed, and Other count the results by outcome.
	Passed int `json:"passed"`
	Failed int `json:"failed"`
	Other  int `json:"other"`
	// Summary is the agent's account of what it did.
	Summary string `json:"summary,omitempty"`
	// ReportedAt is when the agent reported them.
	ReportedAt time.Time `json:"reported_at"`
}

// Total counts every reported result.
func (v Verification) Total() int { return v.Passed + v.Failed + v.Other }

// Source records where a task brief came from.
type Source struct {
	Kind      string `json:"kind"`
	Reference string `json:"reference,omitempty"`
}

// TaskRepository binds a task to one repository of its project.
type TaskRepository struct {
	RepositoryID string `json:"repository_id"`
	Access       string `json:"access"`
	BaseRef      string `json:"base_ref,omitempty"`
	// BaseCommit is the immutable recorded base. Review and cleanup compare
	// against it for the lifetime of the task.
	BaseCommit    string `json:"base_commit,omitempty"`
	Branch        string `json:"branch,omitempty"`
	WorktreePath  string `json:"worktree_path,omitempty"`
	ContainerPath string `json:"container_path,omitempty"`
	// Observation is the last observed Git state, or null if never observed.
	Observation *GitObservation `json:"observation"`
}

// GitObservation is what Feat last saw in a task worktree.
type GitObservation struct {
	Dirty        bool      `json:"dirty"`
	Ahead        int       `json:"ahead"`
	Behind       int       `json:"behind"`
	Merged       bool      `json:"merged"`
	ChangedFiles int       `json:"changed_files"`
	ObservedAt   time.Time `json:"observed_at"`
}

// Session is a task's native agent session.
type Session struct {
	Provider      string `json:"provider"`
	ExecutionMode string `json:"execution_mode"`
	Tmux          Tmux   `json:"tmux"`
	// ProviderSessionID is the agent's own identifier when it exposes one.
	ProviderSessionID string `json:"provider_session_id,omitempty"`
	// Process is the observed process state. Idle never means complete.
	Process     string `json:"process"`
	ControlPath string `json:"control_path,omitempty"`
	// Execution is the isolated environment the session runs in, or null when it
	// runs on this host and there is nothing to identify.
	Execution *Execution `json:"execution"`
	// LastEventSequence is the last control event Feat processed.
	LastEventSequence uint64    `json:"last_event_sequence"`
	CreatedAt         time.Time `json:"created_at"`
	LastActivityAt    time.Time `json:"last_activity_at"`
}

// Execution is the isolated environment one agent session runs in.
//
// It is a separate concept from Runtime even when both are Compose projects:
// this is how the agent runs, and that is the application the user tests.
type Execution struct {
	// Provider identifies the execution adapter, such as the Compose adapter.
	Provider string `json:"provider"`
	// Identity is what makes an action affect one task's environment and no
	// other's, which for the Compose adapter is the project name.
	Identity string `json:"identity"`
	// Service is the service the agent runs in.
	Service string `json:"service"`
	// User is the identity the agent runs as, which is never root.
	User string `json:"user"`
	// Container is the observed container, empty when none was observed.
	Container string `json:"container,omitempty"`
	// Running is whether the environment was observed to be up.
	Running bool `json:"running"`
	// Status is what the environment itself called its state.
	Status string `json:"status,omitempty"`
	// Health is the observed health, which is separate from running.
	Health string `json:"health,omitempty"`
	// ObservedAt is when the last four fields were established.
	ObservedAt time.Time `json:"observed_at,omitzero"`
}

// Tmux locates a session's terminal. These are execution references, never task
// identity (invariant 10).
type Tmux struct {
	Socket  string `json:"socket,omitempty"`
	Session string `json:"session,omitempty"`
	Window  string `json:"window,omitempty"`
	Pane    string `json:"pane,omitempty"`
}

// AttachInfo is the response of POST /v1/tasks/{task_id}/attach-info.
//
// The client invokes native tmux with these stable IDs. No display name or
// numeric index crosses the API because either may change under user config.
type AttachInfo struct {
	Socket  string `json:"socket"`
	Session string `json:"session"`
	Window  string `json:"window"`
	Pane    string `json:"pane"`
}

// Runtime is a task's application runtime.
//
// It carries what the dashboard and the runtime commands need in v0. Slice 9
// extends it when the Compose adapter has more to report; inventing the rest of
// the surface now would publish fields nothing fills.
type Runtime struct {
	Provider string `json:"provider"`
	// Identity is the unique runtime identity, which is what makes an action
	// affect one task's services and no other's.
	Identity string   `json:"identity"`
	Services []string `json:"services"`
	Ports    []Port   `json:"ports"`
	State    string   `json:"state"`
	// Health is separate from State: without a configured health check the
	// honest answer is unknown.
	Health     string    `json:"health"`
	ObservedAt time.Time `json:"observed_at"`
}

// Port is one published port of one service.
type Port struct {
	Service       string `json:"service"`
	ContainerPort int    `json:"container_port"`
	HostPort      int    `json:"host_port"`
}

// newProject maps a project onto the wire.
func newProject(project *domain.Project) Project {
	repositories := make([]Repository, 0, len(project.Repositories))
	for _, repository := range project.Repositories {
		repositories = append(repositories, Repository{
			ID:            repository.ID.String(),
			Name:          repository.Name,
			HostPath:      repository.HostPath,
			ContainerPath: repository.ContainerPath,
			DefaultBranch: repository.DefaultBranch,
			Remote:        repository.Remote,
			DefaultAccess: string(repository.DefaultAccess),
		})
	}
	return Project{
		ID:                project.ID.String(),
		Name:              project.Name,
		PrimaryRepository: project.PrimaryRepository.String(),
		Repositories:      repositories,
		CreatedAt:         project.CreatedAt,
		UpdatedAt:         project.UpdatedAt,
	}
}

// newProjects maps a list of projects, always as a list rather than null, so a
// client can iterate the response without a nil check.
func newProjects(projects []*domain.Project) []Project {
	out := make([]Project, 0, len(projects))
	for _, project := range projects {
		out = append(out, newProject(project))
	}
	return out
}

// NewVerification summarises a review's checks for the dashboard.
//
// A review with no reported checks and no summary produces nothing, so that an
// absent value is genuinely absent rather than a row of zeroes claiming that
// nothing failed.
func NewVerification(review *domain.Review) (Verification, bool) {
	if review == nil || (len(review.Checks) == 0 && review.CompletionSummary == "") {
		return Verification{}, false
	}

	verification := Verification{
		Source:     string(domain.ReporterAgent),
		Summary:    review.CompletionSummary,
		ReportedAt: review.RequestedAt,
	}
	for _, check := range review.Checks {
		// The strictest reporter present decides the label: a single asserted
		// result means the set as a whole was not enforced.
		if check.Reporter == domain.ReporterProvider && verification.Source != string(domain.ReporterAgent) {
			verification.Source = string(domain.ReporterProvider)
		}
		switch check.Status {
		case domain.CheckPassed:
			verification.Passed++
		case domain.CheckFailed:
			verification.Failed++
		default:
			verification.Other++
		}
	}
	return verification, true
}

// newTask maps a task onto the wire.
func newTask(task *domain.Task, verification *Verification) Task {
	repositories := make([]TaskRepository, 0, len(task.Repositories))
	for _, binding := range task.Repositories {
		repositories = append(repositories, TaskRepository{
			RepositoryID:  binding.RepositoryID.String(),
			Access:        string(binding.Access),
			BaseRef:       binding.BaseRef,
			BaseCommit:    binding.BaseCommit,
			Branch:        binding.Branch,
			WorktreePath:  binding.WorktreePath,
			ContainerPath: binding.ContainerPath,
			Observation:   newObservation(binding.Observation),
		})
	}
	return Task{
		ID:        task.ID.String(),
		Key:       task.Key().String(),
		ProjectID: task.ProjectID.String(),
		Title:     task.Title,
		Brief:     task.Brief,
		Source: Source{
			Kind:      string(task.Source.Kind),
			Reference: task.Source.Reference,
		},
		Workflow:     string(task.Workflow),
		Attention:    string(task.Attention),
		Repositories: repositories,
		Session:      newSession(task.Session),
		Runtime:      newRuntime(task.Runtime),
		Verification: verification,
		CreatedAt:    task.CreatedAt,
		UpdatedAt:    task.UpdatedAt,
	}
}

func newTasks(tasks []*domain.Task, verifications map[string]Verification) []Task {
	out := make([]Task, 0, len(tasks))
	for _, task := range tasks {
		var verification *Verification
		if reported, ok := verifications[task.ID.String()]; ok {
			verification = &reported
		}
		out = append(out, newTask(task, verification))
	}
	return out
}

func newObservation(observation *domain.GitObservation) *GitObservation {
	if observation == nil {
		return nil
	}
	return &GitObservation{
		Dirty:        observation.Dirty,
		Ahead:        observation.Ahead,
		Behind:       observation.Behind,
		Merged:       observation.Merged,
		ChangedFiles: observation.ChangedFiles,
		ObservedAt:   observation.ObservedAt,
	}
}

func newSession(session *domain.AgentSession) *Session {
	if session == nil {
		return nil
	}
	return &Session{
		Provider:      session.Provider,
		ExecutionMode: string(session.ExecutionMode),
		Tmux: Tmux{
			Socket:  session.Tmux.Socket,
			Session: session.Tmux.Session,
			Window:  session.Tmux.Window,
			Pane:    session.Tmux.Pane,
		},
		ProviderSessionID: session.ProviderSessionID,
		Process:           string(session.Process),
		ControlPath:       session.ControlPath,
		Execution:         newExecution(session.Execution),
		LastEventSequence: session.LastEventSequence,
		CreatedAt:         session.CreatedAt,
		LastActivityAt:    session.LastActivityAt,
	}
}

// newExecution renders the agent's execution environment.
//
// The identity, service, and user are what Feat asked for; the container and its
// state are what it saw. A client can tell them apart, because a task whose
// container has gone is a different thing from one that was never given a
// container (docs/03-domain-model.md).
func newExecution(environment *domain.ExecutionEnvironment) *Execution {
	if environment == nil {
		return nil
	}
	return &Execution{
		Provider:   environment.Provider,
		Identity:   environment.Identity,
		Service:    environment.Service,
		User:       environment.User,
		Container:  environment.Container,
		Running:    environment.Running,
		Status:     environment.Status,
		Health:     string(environment.Health),
		ObservedAt: environment.ObservedAt,
	}
}

func newRuntime(runtime *domain.RuntimeEnvironment) *Runtime {
	if runtime == nil {
		return nil
	}
	ports := make([]Port, 0, len(runtime.Ports))
	for _, port := range runtime.Ports {
		ports = append(ports, Port{
			Service:       port.Service,
			ContainerPort: port.ContainerPort,
			HostPort:      port.HostPort,
		})
	}
	services := runtime.Services
	if services == nil {
		services = []string{}
	}
	return &Runtime{
		Provider:   runtime.Provider,
		Identity:   runtime.Identity,
		Services:   services,
		Ports:      ports,
		State:      string(runtime.State),
		Health:     string(runtime.Health),
		ObservedAt: runtime.ObservedAt,
	}
}
