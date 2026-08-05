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
	Runtime   *Runtime  `json:"runtime"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

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
	// LastEventSequence is the last control event Feat processed.
	LastEventSequence uint64    `json:"last_event_sequence"`
	CreatedAt         time.Time `json:"created_at"`
	LastActivityAt    time.Time `json:"last_activity_at"`
}

// Tmux locates a session's terminal. These are execution references, never task
// identity (invariant 10).
type Tmux struct {
	Socket  string `json:"socket,omitempty"`
	Session string `json:"session,omitempty"`
	Window  string `json:"window,omitempty"`
	Pane    string `json:"pane,omitempty"`
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

// newTask maps a task onto the wire.
func newTask(task *domain.Task) Task {
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
		CreatedAt:    task.CreatedAt,
		UpdatedAt:    task.UpdatedAt,
	}
}

func newTasks(tasks []*domain.Task) []Task {
	out := make([]Task, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, newTask(task))
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
		LastEventSequence: session.LastEventSequence,
		CreatedAt:         session.CreatedAt,
		LastActivityAt:    session.LastActivityAt,
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
