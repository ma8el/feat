package api

import (
	"fmt"
	"regexp"
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
	Attention string `json:"attention"`
	// Failure is why the task is in `failed`, and null in every other state. It
	// travels with the state because a client that can show one and not the
	// other can only report that something went wrong.
	Failure      *TaskFailure     `json:"failure,omitempty"`
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

// TaskFailure is why a task is in `failed`, in the words of whatever failed.
type TaskFailure struct {
	// Reason is what failed.
	Reason string `json:"reason"`
	// At is when the task entered `failed`.
	At time.Time `json:"at"`
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
// The lifecycle is manual: nothing here starts because a task reached a state,
// and nothing stops because it reached another. What a task owns is named
// explicitly, because a resource a user cannot see is a resource they cannot
// clean up.
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
	Health string `json:"health"`
	// Networks and Volumes are what Compose reports as this project's. Volumes
	// are listed because destroying a runtime retains every one of them, and a
	// retained resource nobody can see is one nobody will remove.
	Networks []string `json:"networks"`
	Volumes  []string `json:"volumes"`
	// Composition, StaticOverrides, EnvFiles, and the two generated paths are
	// the exact inputs this runtime was created from, kept so that a later action
	// reaches the same resources even if the project's configuration has since
	// been edited. They are the user's own paths, which are not secrets; no value
	// from an environment file is ever read, let alone published.
	//
	// Composition is what the application is made of, one entry per repository
	// that brings Compose files. GeneratedIncludePath is the document Feat wrote
	// to join them.
	Composition           []RuntimeSource `json:"composition"`
	GeneratedIncludePath  string          `json:"generated_include_path"`
	StaticOverrides       []string        `json:"static_overrides"`
	EnvFiles              []string        `json:"env_files"`
	GeneratedOverridePath string          `json:"generated_override_path"`
	ObservedAt            time.Time       `json:"observed_at"`
}

// RuntimeSource is one repository's contribution to a task's application.
type RuntimeSource struct {
	// Repository identifies the repository within the project.
	Repository string `json:"repository"`
	// Directory is the checkout its own Compose files' relative paths resolve
	// against.
	Directory string `json:"directory"`
	// Files are that repository's Compose files, in order.
	Files []string `json:"files"`
}

// Port is one published port of one service.
type Port struct {
	Service       string `json:"service"`
	ContainerPort int    `json:"container_port"`
	HostPort      int    `json:"host_port"`
}

// RuntimeService is one observed service of a task's runtime.
type RuntimeService struct {
	Name string `json:"name"`
	// Container is the observed container, empty when the service has none.
	Container string `json:"container,omitempty"`
	// State is what the container runtime called it, and Status its own longer
	// phrasing, kept verbatim so a client quotes the tool rather than
	// paraphrasing it.
	State  string `json:"state,omitempty"`
	Status string `json:"status,omitempty"`
	Health string `json:"health"`
	// ExitCode is the exit status of a service that has stopped.
	ExitCode int `json:"exit_code,omitempty"`
	// Managed reports whether the project's runtime.services names this one. A
	// service that it does not name is one Compose started because a managed
	// service depends on it; it belongs to this task's Compose project all the
	// same, and Feat stops and removes it with the rest.
	Managed bool `json:"managed"`
}

// RuntimeAction is one manual lifecycle action a user asked for.
//
// The five are FR-RUN-005's, and the vocabulary lives here because the endpoint
// path is what names them. Every one of them is an explicit user request: no
// workflow transition, no reconciliation, and no agent reaches any of them.
type RuntimeAction string

// The manual runtime actions.
const (
	// RuntimeCreate brings the containers into existence without starting them.
	RuntimeCreate RuntimeAction = "create"
	// RuntimeStart brings the services up.
	RuntimeStart RuntimeAction = "start"
	// RuntimeStop stops them and keeps their containers.
	RuntimeStop RuntimeAction = "stop"
	// RuntimeObserve reports what exists now, changing nothing.
	RuntimeObserve RuntimeAction = "status"
	// RuntimeDestroy removes the containers and networks the task owns, retaining
	// every volume.
	RuntimeDestroy RuntimeAction = "destroy"
)

// Valid reports whether the action is one Feat performs.
func (a RuntimeAction) Valid() bool {
	switch a {
	case RuntimeCreate, RuntimeStart, RuntimeStop, RuntimeObserve, RuntimeDestroy:
		return true
	default:
		return false
	}
}

// RuntimeTimeout bounds one manual runtime action, from the request arriving to
// the answer being written.
//
// It belongs to the endpoint's contract rather than to the daemon's private
// business, because both ends have to hold the same number. The daemon stops
// waiting for Docker when it runs out; a client that gave up sooner would cancel
// a request the daemon is still serving, and cancelling one kills the
// `docker compose up` it is waiting on part way through.
//
// Minutes, because the work is minutes. The first start of a task's services
// pulls every image the project names and runs every build it defines, while the
// second start of the same task answers in about a second — so the ceiling is
// invisible until the first run of a project nobody has built here yet. Ten
// seconds, which was every request's budget, is what a user met as `Post
// "http://feat/v1/tasks/…/runtime/start": context deadline exceeded` on a start
// that then worked when they tried it again, because by then the images were
// pulled and the containers existed.
const RuntimeTimeout = 15 * time.Minute

// AgentTimeout bounds one request that creates or stops a task's agent
// environment: a launch, a resume, or a stop.
//
// It exists for the reason RuntimeTimeout does, and it was found the same way. A
// launch that had to recreate its container because the project's own Compose
// file had changed took 10.018 seconds and was cancelled by the client at ten,
// while the daemon went on serving it — leaving a container the request that
// created it no longer knew about. The five launches before it took between 0.46
// and 3.13 seconds, so the ceiling is invisible until the day a project's file
// changes.
//
// It is three minutes rather than fifteen because the daemon's own patience for
// a container to come up is three (compose.defaultReadyTimeout), and a budget
// wider than the one the work is bounded by would only postpone the same answer.
// The application runtime's is longer because it pulls and builds a project's
// whole service graph; an agent environment is one service that has usually been
// built already.
const AgentTimeout = 3 * time.Minute

// DestroyRuntime is the body of POST /v1/tasks/{task_id}/runtime/destroy.
//
// It carries the user's confirmation, for the reason a launch carries the
// fingerprint of the plan that was displayed: a request that removes something
// should say that somebody meant it. Volumes are retained whatever it says, and
// removing one is a choice cleanup asks for separately (FR-CLEAN-002).
type DestroyRuntime struct {
	Confirm bool `json:"confirm"`
}

// RuntimeStatus is the response of every runtime action.
type RuntimeStatus struct {
	// Task is the task as it is now recorded, carrying its runtime.
	Task Task `json:"task"`
	// Services are what was observed: one entry per configured service, so that
	// a service with no container is reported as one — a runtime missing half
	// its services is not a runtime that is running — followed by everything
	// else in the task's Compose project, which is everything Compose started
	// because a configured service depends on it.
	Services []RuntimeService `json:"services"`
	// Notes are what a user should know about what they just started, in Feat's
	// terms rather than the container runtime's. They are observations of the
	// running containers and are recomputed by the next action.
	Notes []string `json:"notes"`
}

// RuntimeResult is what the daemon reports after a runtime action.
type RuntimeResult struct {
	// Task is the task as it is now recorded.
	Task *domain.Task
	// Services are the observed services.
	Services []RuntimeService
	// Notes are what the started containers turned out to be.
	Notes []string
}

// ResourceReport is the response of GET /v1/resources.
//
// It is a separate document rather than fields on a task, for two reasons. A
// sample is not persisted (docs/06-technical-architecture.md, storage rules), so
// it is not part of what a task record says about itself; and it has its own
// time and its own failure mode, which a task carrying the figures would have to
// borrow.
//
// The same type is the wire shape and the daemon's, as AttachInfo is: there is
// one representation of an observation nobody stores.
type ResourceReport struct {
	// Machine is what the whole host reported.
	Machine MachineResources `json:"machine"`
	// Tasks are the per-task aggregates. A task appears only while it owns
	// something that can use resources.
	Tasks []TaskResources `json:"tasks"`
	// Notes are what could not be collected, in terms a user can act on. A report
	// with notes is still a report: metrics are observational and one unreadable
	// source never discards the others (FR-UI-005).
	Notes []string `json:"notes"`
	// CollectedAt is when the sample was taken. Sampling runs on its own
	// schedule, so this is deliberately not the time of the request.
	CollectedAt time.Time `json:"collected_at,omitzero"`
	// Sampled reports whether any sample has been taken yet. A daemon that has
	// just started has not, which is different from a machine that reported
	// nothing.
	Sampled bool `json:"sampled"`
}

// MachineResources is what the whole host reported.
//
// Every figure is a pointer, so a value nothing measured is null rather than
// zero: a dashboard printing "0 GiB free" where it had not looked would be
// making a claim, which is the rule ADR-028 established for diagnostics.
type MachineResources struct {
	// Cores is how many processors the machine has.
	Cores int `json:"cores"`
	// Load is the run-queue average, or null when it could not be read.
	//
	// Feat reports load rather than a utilisation percentage because a per-core
	// percentage is not obtainable on macOS without cgo, and one measure on both
	// platforms is worth more than two that look alike and are not (ADR-035).
	Load *LoadAverage `json:"load"`
	// Memory is the machine's memory, or null when it could not be read.
	Memory *Capacity `json:"memory"`
	// Disk is the filesystem holding Feat's state directory, or null when it
	// could not be read.
	Disk *DiskCapacity `json:"disk"`
}

// LoadAverage is the machine's run-queue average over three windows.
type LoadAverage struct {
	One     float64 `json:"one"`
	Five    float64 `json:"five"`
	Fifteen float64 `json:"fifteen"`
}

// Capacity is a total and what is still available, in bytes.
type Capacity struct {
	TotalBytes     uint64 `json:"total_bytes"`
	AvailableBytes uint64 `json:"available_bytes"`
}

// DiskCapacity is a filesystem's capacity and the path it was measured at.
type DiskCapacity struct {
	// Path is the directory the figures describe: Feat's state directory, which
	// is where worktrees, control workspaces, and generated overrides live.
	Path           string `json:"path"`
	TotalBytes     uint64 `json:"total_bytes"`
	AvailableBytes uint64 `json:"available_bytes"`
}

// TaskResources is what one task's containers and processes are using.
type TaskResources struct {
	TaskID string `json:"task_id"`
	// CPUPercent is the total, in percent of one core, or null when nothing
	// measured it. A container using two cores fully reports 200.
	CPUPercent *float64 `json:"cpu_percent"`
	// MemoryBytes is the total resident memory, or null when nothing measured it.
	MemoryBytes *uint64 `json:"memory_bytes"`
	// ContainerBytes and ProcessBytes are the two halves of that total.
	//
	// They are reported apart as well as together because they are not measured
	// against the same thing: on macOS a container's memory is memory inside the
	// container runtime's own virtual machine, so the sum is what this task is
	// using and not a share of the machine's memory above (ADR-035).
	ContainerBytes uint64 `json:"container_bytes"`
	ProcessBytes   uint64 `json:"process_bytes"`
	// Containers are the task's own containers, whatever they run.
	Containers []ContainerResources `json:"containers"`
	// Processes counts the host processes attributed to the task: its terminal
	// panes and everything they started.
	Processes int `json:"processes"`
}

// ContainerResources is one container a task owns.
type ContainerResources struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Kind separates an application service from the agent's own environment.
	// Both belong to the task, and only one of them is what the user is testing.
	Kind        string  `json:"kind"`
	CPUPercent  float64 `json:"cpu_percent"`
	MemoryBytes uint64  `json:"memory_bytes"`
}

// ReviewAction is one thing a user asks of a task's review.
//
// The vocabulary lives here because the endpoint path is what names them, as it
// does for the runtime actions. Every one is an explicit request: no workflow
// transition and no agent reaches any of them, and none of them touches a
// container, a worktree, or a branch.
type ReviewAction string

// The review actions.
const (
	// ReviewObserve compares every repository against its recorded base and
	// returns what review shows. It observes and records what it observed,
	// which is why it is a POST.
	ReviewObserve ReviewAction = "observe"
	// ReviewApprove records the user's approval. It never stops or destroys a
	// runtime; the offer to stop one is made in words (FR-REV-004, ADR-034).
	ReviewApprove ReviewAction = "approve"
	// ReviewRequestChanges sends the work back for revision.
	ReviewRequestChanges ReviewAction = "changes"
	// ReviewVerify runs the project's configured checks now. It is how a gate
	// interrupted by a restart is run again, and recovery in Feat is an action a
	// user takes rather than something that happens on its own.
	ReviewVerify ReviewAction = "verify"
)

// Valid reports whether the action is one Feat performs.
//
// There is deliberately no action for leaving a review pending. A review nobody
// has decided is already pending, so the action was a way of un-deciding, and it
// moved the review's own copy of the decision without moving the task's — which
// is how a task came to read approved and pending at once (ADR-047).
func (a ReviewAction) Valid() bool {
	switch a {
	case ReviewObserve, ReviewApprove, ReviewRequestChanges, ReviewVerify:
		return true
	default:
		return false
	}
}

// ReviewStatus is the response of every review action.
type ReviewStatus struct {
	// Task is the task as it is now recorded.
	Task Task `json:"task"`
	// Review is the decision, the agent's summary, and the check results.
	Review Review `json:"review"`
	// Repositories are the per-repository comparisons, each against that
	// repository's own recorded base commit (FR-REV-001).
	Repositories []ReviewRepository `json:"repositories"`
	// Commands are the configured external commands, expanded for this task and
	// checked. The client runs them with its own terminal.
	Commands []ReviewCommand `json:"commands"`
	// Notes are what a user should know: a repository that could not be read, a
	// command that could not be expanded, or a count that means less than it
	// looks like.
	Notes []string `json:"notes"`
}

// Review is what is known about a task's work on the wire.
//
// It carries no decision. The user's decision is the task's workflow state,
// which travels beside this in every response that holds one (ADR-047).
type Review struct {
	// Summary is the agent's own account of what it did, which is a claim.
	Summary string `json:"summary,omitempty"`
	// Checks are the results, each attributed to whoever produced it.
	Checks []ReviewCheck `json:"checks"`
	// Gated reports that a completion gate ran at least one of them. It is the
	// difference between a verification and a claim about one.
	Gated bool `json:"gated"`
	// RequestedAt is when the agent asked for review, or null if it has not.
	RequestedAt *time.Time `json:"requested_at"`
}

// ReviewCheck is one check result.
type ReviewCheck struct {
	ID           string `json:"id"`
	RepositoryID string `json:"repository_id,omitempty"`
	// Status is passed, failed, skipped, or unknown. Unknown is a check that
	// did not report — one that could not be started, or that ran out of time —
	// and it is deliberately not a failure.
	Status string `json:"status"`
	// Reporter is "agent" for a claimed result and "provider" for one a gate
	// enforced.
	Reporter string `json:"reporter"`
	// Detail is what the check said about itself: the agent's words, or the
	// tail of what the command printed.
	Detail string     `json:"detail,omitempty"`
	RanAt  *time.Time `json:"ran_at"`
}

// ReviewRepository is one repository's comparison against its recorded base.
type ReviewRepository struct {
	RepositoryID string `json:"repository_id"`
	Access       string `json:"access"`
	Branch       string `json:"branch,omitempty"`
	WorktreePath string `json:"worktree_path"`
	// BaseRef is the ref the base policy named and BaseCommit is what it
	// resolved to. The commit is what every comparison uses, for the whole life
	// of the task (invariant 8).
	BaseRef    string `json:"base_ref"`
	BaseCommit string `json:"base_commit"`
	// HeadCommit is what the worktree has checked out, empty when the agent has
	// not committed. Committing is optional (FR-GIT-007).
	HeadCommit string `json:"head_commit,omitempty"`
	// ChangedFiles counts tracked and untracked files that differ from the
	// base; Insertions and Deletions count tracked lines only.
	ChangedFiles int  `json:"changed_files"`
	Insertions   int  `json:"insertions"`
	Deletions    int  `json:"deletions"`
	Dirty        bool `json:"dirty"`
	// Ahead, Behind, and Merged come from the last observation: ahead of the
	// recorded base, behind the base ref as it is now, and contained in it.
	Ahead        int        `json:"ahead"`
	Behind       int        `json:"behind"`
	Merged       bool       `json:"merged"`
	SummarizedAt *time.Time `json:"summarized_at"`
}

// The kinds of external command review opens (FR-REV-002, FR-REV-003).
//
// They are the wire values of internal/review's own kinds, named here because a
// client reads them off a response rather than importing that package.
const (
	ReviewCommandKindDiff   = "diff"
	ReviewCommandKindEditor = "editor"
	ReviewCommandKindStatus = "status"
)

// ReviewCommand is one expanded external command.
//
// It is a command rather than output, as RuntimeCommand is: Feat renders no
// diff of its own, and the client runs the user's own tools with the user's own
// terminal (FR-REV-002, ADR-006).
type ReviewCommand struct {
	// Kind is diff, editor, or status.
	Kind         string   `json:"kind"`
	RepositoryID string   `json:"repository_id"`
	Program      string   `json:"program"`
	Arguments    []string `json:"arguments"`
	// Directory is the task worktree the command runs in, which is checked
	// before it is returned and checked again before it is run.
	Directory string `json:"directory"`
}

// ReviewResult is what the daemon reports after a review action.
type ReviewResult struct {
	// Task is the task as it is now recorded.
	Task *domain.Task
	// Review is its review aggregate.
	Review *domain.Review
	// Commands are the expanded external commands.
	Commands []ReviewCommand
	// Notes are what could not be done, in terms a user can act on.
	Notes []string
}

// RuntimeCommand is the response of POST
// /v1/tasks/{task_id}/runtime/logs-info.
//
// It is a command rather than output: FR-RUN-006 asks for normal Compose logs,
// and the client runs this with its own terminal exactly as it runs native tmux
// for attach. The client checks what it is given before running it.
type RuntimeCommand struct {
	Program   string   `json:"program"`
	Arguments []string `json:"arguments"`
	Directory string   `json:"directory"`
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
	if len(review.Checks) > 0 {
		// The weakest reporter present decides the label: a set is enforced only
		// if every result in it was, because one asserted result is enough to
		// make "verified" a claim rather than a fact.
		//
		// This condition was unreachable until slice 11, when a gate first
		// produced a provider result: it started from "agent" and only ever
		// tested whether it was not "agent" (ADR-036 evidence 1).
		verification.Source = string(domain.ReporterProvider)
	}
	for _, check := range review.Checks {
		if check.Reporter != domain.ReporterProvider {
			verification.Source = string(domain.ReporterAgent)
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

// NewReviewStatus renders what a review action produced.
//
// The per-repository rows are built from the task and the review together,
// because the two hold different halves of one answer: the binding says what the
// task started from and where its worktree is, and the review says what was
// found there. Joining them here rather than storing one inside the other is the
// same choice ADR-026 made for the domain and the stored documents.
func NewReviewStatus(result ReviewResult) ReviewStatus {
	verification, ok := NewVerification(result.Review)
	var reported *Verification
	if ok {
		reported = &verification
	}

	status := ReviewStatus{
		Task:         newTask(result.Task, reported),
		Review:       newReview(result.Review),
		Repositories: newReviewRepositories(result.Task, result.Review),
		Commands:     result.Commands,
		Notes:        result.Notes,
	}
	if status.Commands == nil {
		status.Commands = []ReviewCommand{}
	}
	if status.Notes == nil {
		status.Notes = []string{}
	}
	return status
}

func newReview(review *domain.Review) Review {
	if review == nil {
		return Review{Checks: []ReviewCheck{}}
	}

	checks := make([]ReviewCheck, 0, len(review.Checks))
	for _, check := range review.Checks {
		checks = append(checks, ReviewCheck{
			ID:           check.ID,
			RepositoryID: check.RepositoryID.String(),
			Status:       string(check.Status),
			Reporter:     string(check.Reporter),
			Detail:       check.Detail,
			RanAt:        moment(check.RanAt),
		})
	}
	return Review{
		Summary:     review.CompletionSummary,
		Checks:      checks,
		Gated:       review.Gated(),
		RequestedAt: moment(review.RequestedAt),
	}
}

// newReviewRepositories renders one row per repository the task holds.
//
// A repository with no summary yet is still a row: it is part of the task, and
// leaving it out would make a review of three repositories look like a review of
// two.
func newReviewRepositories(task *domain.Task, review *domain.Review) []ReviewRepository {
	rows := make([]ReviewRepository, 0, len(task.Repositories))
	for _, binding := range task.Repositories {
		row := ReviewRepository{
			RepositoryID: binding.RepositoryID.String(),
			Access:       string(binding.Access),
			Branch:       binding.Branch,
			WorktreePath: binding.WorktreePath,
			BaseRef:      binding.BaseRef,
			BaseCommit:   binding.BaseCommit,
		}
		if binding.Observation != nil {
			row.Ahead = binding.Observation.Ahead
			row.Behind = binding.Observation.Behind
			row.Merged = binding.Observation.Merged
			row.ChangedFiles = binding.Observation.ChangedFiles
			row.Dirty = binding.Observation.Dirty
		}
		if review != nil {
			if change, found := review.Repository(binding.RepositoryID); found {
				row.HeadCommit = change.HeadCommit
				row.ChangedFiles = change.ChangedFiles
				row.Insertions = change.Insertions
				row.Deletions = change.Deletions
				row.Dirty = change.Dirty
				row.SummarizedAt = moment(change.SummarizedAt)
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// moment renders a time that may not have happened as null rather than as the
// zero time, which reads like a date in 1970.
func moment(at time.Time) *time.Time {
	if at.IsZero() {
		return nil
	}
	return &at
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
		Failure:      newTaskFailure(task.Failure),
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

func newTaskFailure(failure *domain.TaskFailure) *TaskFailure {
	if failure == nil {
		return nil
	}
	return &TaskFailure{Reason: failure.Reason, At: failure.At}
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
	return &Runtime{
		Provider:              runtime.Provider,
		Identity:              runtime.Identity,
		Services:              list(runtime.Services),
		Ports:                 ports,
		State:                 string(runtime.State),
		Health:                string(runtime.Health),
		Networks:              list(runtime.Networks),
		Volumes:               list(runtime.Volumes),
		Composition:           newComposition(runtime.Composition),
		GeneratedIncludePath:  runtime.GeneratedIncludePath,
		StaticOverrides:       list(runtime.StaticOverrides),
		EnvFiles:              list(runtime.EnvFiles),
		GeneratedOverridePath: runtime.GeneratedOverridePath,
		ObservedAt:            runtime.ObservedAt,
	}
}

// newComposition renders what a runtime is composed of.
func newComposition(sources []domain.RuntimeSource) []RuntimeSource {
	rendered := make([]RuntimeSource, 0, len(sources))
	for _, source := range sources {
		rendered = append(rendered, RuntimeSource{
			Repository: source.Repository,
			Directory:  source.Directory,
			Files:      list(source.Files),
		})
	}
	return rendered
}

// list renders a string slice as a list rather than null, so a client can
// iterate the response without a nil check.
func list(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// Terminal input and rendering bounds.
//
// Every one of these is a limit on what a client may ask the daemon to do to an
// agent's terminal. Sending keys is a write, so the request is bounded rather
// than trusted, which is the rule the control workspace already follows for
// every message it accepts.
const (
	// MaxTerminalText is the most typed text one request may paste. It is
	// generous enough for a brief pasted into a prompt and far short of what
	// would make a tmux buffer a memory question.
	MaxTerminalText = 32 << 10
	// MaxTerminalKeys is the most key names one request may send.
	MaxTerminalKeys = 32
	// MaxTerminalKeyName bounds one tmux key name, the longest of which are
	// modifier-prefixed function keys.
	MaxTerminalKeyName = 16
	// MaxTerminalSize bounds a requested pane size in cells.
	MaxTerminalSize = 1000
)

// terminalKeyName is the shape of a tmux key name: Enter, Escape, Up, F12,
// C-c, M-x, S-Up.
//
// A name is matched rather than passed through, so that a value arriving over
// the socket cannot become anything tmux would read as a flag or a second
// argument. The adapter also passes keys after a terminator; this is the check
// that does not depend on that one.
var terminalKeyName = regexp.MustCompile(`^[A-Za-z0-9]+(-[A-Za-z0-9]+)*$`)

// TerminalView asks for one rendered frame of a task's pane.
//
// The size is the region the caller will draw into. The daemon sets the pane to
// it before capturing, because a program wraps its own output and would
// otherwise wrap at a width the display does not have.
type TerminalView struct {
	Width  int `json:"width"`
	Height int `json:"height"`
	// Shell asks for the task's shell pane rather than the agent's.
	Shell bool `json:"shell,omitempty"`
}

// Validate reports whether the requested size is one the daemon will set.
func (v TerminalView) Validate() error {
	if v.Width <= 0 || v.Height <= 0 {
		return fmt.Errorf("%w: a terminal view needs a positive width and height, but got %dx%d",
			ErrInvalid, v.Width, v.Height)
	}
	if v.Width > MaxTerminalSize || v.Height > MaxTerminalSize {
		return fmt.Errorf("%w: a terminal view of %dx%d is larger than the %d cells Feat will set",
			ErrInvalid, v.Width, v.Height, MaxTerminalSize)
	}
	return nil
}

// TerminalFrame is one pane as tmux has already rendered it.
//
// Content carries the escape sequences tmux emitted. Feat draws them and reads
// nothing out of them but their width: no task, agent, attention, or workflow
// state is derived from a terminal's contents, which remains what provider hooks
// report (ADR-042).
type TerminalFrame struct {
	// Width and Height are the window's size in cells: the rectangle the panes
	// below tile between them.
	Width  int `json:"width"`
	Height int `json:"height"`
	// Panes are every pane of the task's window, each with the place it occupies.
	//
	// A window rather than a pane, because a pane is not what a user sees. A task
	// window holds the agent and, once one exists, a shell beside it, and drawing
	// one of them into a region sized for both leaves half of it empty.
	Panes []TerminalPane `json:"panes"`
}

// TerminalPane is one pane of a window, with the place it occupies in it.
type TerminalPane struct {
	Pane    string   `json:"pane"`
	Left    int      `json:"left"`
	Top     int      `json:"top"`
	Width   int      `json:"width"`
	Height  int      `json:"height"`
	CursorX int      `json:"cursor_x"`
	CursorY int      `json:"cursor_y"`
	Content []string `json:"content"`
	// Active reports the pane tmux would send a key to.
	Active bool `json:"active,omitempty"`
	// Dead reports a pane whose program has exited and which tmux is retaining,
	// which is a terminal to explain rather than one to keep redrawing.
	Dead bool `json:"dead,omitempty"`
}

// TerminalInput is what a user typed into a focused pane.
//
// Keys and text are separate because tmux delivers them differently: a key name
// goes through send-keys, and text goes through a bracketed paste so that the
// application reading it cannot take a trailing newline as a submission the user
// did not make.
type TerminalInput struct {
	Keys []string `json:"keys,omitempty"`
	Text string   `json:"text,omitempty"`
	// Paste asks for the text to arrive as a paste rather than as typing.
	//
	// The difference is visible to the program receiving it: an application that
	// has enabled bracketed paste mode is told which one this was, and may insert
	// a paste without running what a typed character runs. A keystroke is
	// therefore not a paste, and sending one as a paste made ordinary keys
	// behave oddly.
	Paste bool `json:"paste,omitempty"`
	// Shell directs the input at the task's shell pane rather than the agent's.
	Shell bool `json:"shell,omitempty"`
}

// Validate reports whether this is input the daemon will deliver.
func (i TerminalInput) Validate() error {
	if len(i.Keys) == 0 && i.Text == "" {
		return fmt.Errorf("%w: terminal input carries neither keys nor text", ErrInvalid)
	}
	if len(i.Keys) > MaxTerminalKeys {
		return fmt.Errorf("%w: %d keys in one request is more than the %d Feat sends",
			ErrInvalid, len(i.Keys), MaxTerminalKeys)
	}
	for _, key := range i.Keys {
		if len(key) > MaxTerminalKeyName || !terminalKeyName.MatchString(key) {
			return fmt.Errorf("%w: %q is not a tmux key name", ErrInvalid, key)
		}
	}
	if len(i.Text) > MaxTerminalText {
		return fmt.Errorf("%w: %d bytes of text is more than the %d Feat pastes at once",
			ErrInvalid, len(i.Text), MaxTerminalText)
	}
	return nil
}
