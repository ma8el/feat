package fs

import (
	"context"
	"errors"
	iofs "io/fs"
	"path/filepath"
	"time"

	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/store"
)

// taskDocument is the stored form of a task.
//
// The brief is not here. It is a Markdown document the user wrote and the agent
// reads, so it is stored as Markdown in prompt.md (FR-STATE-001) rather than
// escaped into a JSON string, and this document is the record of everything
// else.
type taskDocument struct {
	SchemaVersion int                      `json:"schema_version"`
	ID            string                   `json:"id"`
	UpdatedAt     time.Time                `json:"updated_at"`
	ProjectID     string                   `json:"project_id"`
	Title         string                   `json:"title"`
	Source        sourceDocument           `json:"source"`
	Workflow      string                   `json:"workflow"`
	Attention     string                   `json:"attention"`
	Failure       *failureDocument         `json:"failure,omitempty"`
	CreatedAt     time.Time                `json:"created_at"`
	Repositories  []taskRepositoryDocument `json:"repositories,omitempty"`
	Session       *sessionDocument         `json:"session,omitempty"`
	Runtime       *runtimeDocument         `json:"runtime,omitempty"`
	Publication   *publicationDocument     `json:"publication,omitempty"`
}

type sourceDocument struct {
	Kind      string          `json:"kind"`
	Reference string          `json:"reference,omitempty"`
	Ticket    *ticketDocument `json:"ticket,omitempty"`
}

// ticketDocument is the ticket a task was composed from.
//
// The snapshot is stored with the reference rather than beside it, because the
// two are read together: what the ticket said when Feat read it is the thing a
// change is compared against, and a reference with no snapshot could not answer
// whether anything changed (ADR-071).
//
// Like the failure below, it is an added optional field at the same schema
// version: a task written by an earlier build has no ticket and decodes to
// none, which is the truth about a brief that came from somewhere else.
type ticketDocument struct {
	Provider        string                 `json:"provider,omitempty"`
	Reference       string                 `json:"reference"`
	URL             string                 `json:"url"`
	Snapshot        ticketSnapshotDocument `json:"snapshot"`
	ChangeAvailable bool                   `json:"change_available"`
}

type ticketSnapshotDocument struct {
	Title   string    `json:"title"`
	Body    string    `json:"body,omitempty"`
	State   string    `json:"state"`
	TakenAt time.Time `json:"taken_at"`
}

// publicationDocument is what publishing a task planned to do and what came of
// it.
//
// The plan is written before anything is attempted and every result before the
// next repository begins, so this document is what an interrupted publication
// is read from: a repository still recorded as planned is one nothing was
// attempted for, and a merge request that exists is always named here
// (ADR-073).
//
// It is an added optional field at the same schema version, on the same rule as
// the ticket above: a task that never published has no publication, which is
// what a document written before this build decodes to.
type publicationDocument struct {
	Repositories []repositoryPublicationDocument `json:"repositories,omitempty"`
	PlannedAt    time.Time                       `json:"planned_at"`
	UpdatedAt    time.Time                       `json:"updated_at"`
}

type repositoryPublicationDocument struct {
	RepositoryID string                `json:"repository_id"`
	Forge        string                `json:"forge"`
	Remote       string                `json:"remote"`
	BaseBranch   string                `json:"base_branch"`
	Commit       string                `json:"commit"`
	State        string                `json:"state"`
	Request      *mergeRequestDocument `json:"request,omitempty"`
	Failure      string                `json:"failure,omitempty"`
	AttemptedAt  *time.Time            `json:"attempted_at,omitempty"`
}

type mergeRequestDocument struct {
	Reference string `json:"reference"`
	URL       string `json:"url"`
}

// failureDocument is why a task is in `failed`.
//
// It is an added optional field at the same schema version, which is what this
// codec's rule allows: a build that adds a field without changing the meaning of
// the existing ones stays readable by the build before it. A snapshot written
// earlier has no failure and decodes to none, which is the truth about a record
// that never held one.
type failureDocument struct {
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
}

type taskRepositoryDocument struct {
	RepositoryID  string               `json:"repository_id"`
	Access        string               `json:"access"`
	BaseRef       string               `json:"base_ref,omitempty"`
	BaseCommit    string               `json:"base_commit,omitempty"`
	Branch        string               `json:"branch,omitempty"`
	WorktreePath  string               `json:"worktree_path,omitempty"`
	ContainerPath string               `json:"container_path,omitempty"`
	Observation   *observationDocument `json:"observation,omitempty"`
}

type observationDocument struct {
	Dirty        bool      `json:"dirty"`
	Ahead        int       `json:"ahead"`
	Behind       int       `json:"behind"`
	Merged       bool      `json:"merged"`
	ChangedFiles int       `json:"changed_files"`
	ObservedAt   time.Time `json:"observed_at"`
}

type sessionDocument struct {
	Provider          string             `json:"provider"`
	ExecutionMode     string             `json:"execution_mode"`
	Tmux              tmuxDocument       `json:"tmux"`
	ProviderSessionID string             `json:"provider_session_id,omitempty"`
	Process           string             `json:"process"`
	ControlPath       string             `json:"control_path,omitempty"`
	Execution         *executionDocument `json:"execution,omitempty"`
	LastEventSequence uint64             `json:"last_event_sequence"`
	CreatedAt         time.Time          `json:"created_at"`
	LastActivityAt    time.Time          `json:"last_activity_at"`
}

// executionDocument records the environment an agent session runs in.
//
// It is absent for host execution, where the environment is the machine the
// daemon is on and there is nothing to identify. The observed fields are written
// as they were last seen and are never treated as current on the way back in:
// reconciliation asks the environment (ADR-029's rule for worktrees, applied to
// containers).
type executionDocument struct {
	Provider              string    `json:"provider"`
	Identity              string    `json:"identity"`
	Files                 []string  `json:"files,omitempty"`
	GeneratedOverridePath string    `json:"generated_override_path,omitempty"`
	Service               string    `json:"service"`
	User                  string    `json:"user"`
	Container             string    `json:"container,omitempty"`
	Running               bool      `json:"running"`
	Status                string    `json:"status,omitempty"`
	Health                string    `json:"health,omitempty"`
	ObservedAt            time.Time `json:"observed_at,omitzero"`
}

type tmuxDocument struct {
	Socket  string `json:"socket,omitempty"`
	Session string `json:"session,omitempty"`
	Window  string `json:"window,omitempty"`
	Pane    string `json:"pane,omitempty"`
}

type runtimeDocument struct {
	Provider              string                `json:"provider"`
	Identity              string                `json:"identity"`
	Composition           []compositionDocument `json:"composition,omitempty"`
	GeneratedIncludePath  string                `json:"generated_include_path,omitempty"`
	StaticOverrides       []string              `json:"static_overrides,omitempty"`
	GeneratedOverridePath string                `json:"generated_override_path,omitempty"`
	EnvFiles              []string              `json:"env_files,omitempty"`
	Services              []string              `json:"services,omitempty"`
	Provenance            []provenanceDocument  `json:"provenance,omitempty"`
	Allocations           []allocationDocument  `json:"allocations,omitempty"`
	Ports                 []portDocument        `json:"ports,omitempty"`
	Networks              []string              `json:"networks,omitempty"`
	Volumes               []string              `json:"volumes,omitempty"`
	State                 string                `json:"state"`
	Health                string                `json:"health"`
	ObservedAt            *time.Time            `json:"observed_at,omitempty"`
	Generation            uint64                `json:"generation,omitempty"`
}

// compositionDocument is one repository's contribution to a task's application.
type compositionDocument struct {
	Repository string   `json:"repository"`
	Directory  string   `json:"directory"`
	Files      []string `json:"files,omitempty"`
}

// provenanceDocument is where one managed service's code comes from.
type provenanceDocument struct {
	Service      string   `json:"service"`
	Repositories []string `json:"repositories,omitempty"`
	Mounted      []string `json:"mounted,omitempty"`
	Built        []string `json:"built,omitempty"`
}

type portDocument struct {
	Service       string `json:"service"`
	ContainerPort int    `json:"container_port"`
	HostPort      int    `json:"host_port"`
	// HostIP is the address the container runtime reported the port bound on,
	// omitted when it reported none.
	HostIP string `json:"host_ip,omitempty"`
}

// allocationDocument is one host port Feat reserved for one service.
//
// It is stored apart from the observed publications because it is held: it is
// what keeps a second task off this port, and what a destroy gives back.
type allocationDocument struct {
	Service       string `json:"service"`
	ContainerPort int    `json:"container_port"`
	HostPort      int    `json:"host_port"`
	Protocol      string `json:"protocol"`
	HostIP        string `json:"host_ip,omitempty"`
}

type taskStore struct{ store *Store }

// Save records the task.
//
// The brief is written before the snapshot, so that a crash between the two
// leaves a task whose brief is older than its snapshot rather than a snapshot
// that refers to a brief which was never written.
func (t taskStore) Save(ctx context.Context, task *domain.Task) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if task == nil {
		return errors.New("saving a task requires a task")
	}
	if err := task.Validate(); err != nil {
		return err
	}
	dir, err := t.store.taskDir(store.Ref(task))
	if err != nil {
		return err
	}

	defer t.store.lock("task:" + store.Ref(task).String())()

	if err := t.store.replaceFile(filepath.Join(dir, briefFile), []byte(task.Brief)); err != nil {
		return err
	}
	return t.store.writeSnapshot(taskCodec, filepath.Join(dir, taskFile), encodeTask(task))
}

// Load returns one task.
func (t taskStore) Load(ctx context.Context, ref store.TaskRef) (*domain.Task, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dir, err := t.store.taskDir(ref)
	if err != nil {
		return nil, err
	}
	return t.store.loadTask(ref, dir)
}

// List returns every task of one project, ordered by identifier.
//
// A directory without a task snapshot is skipped. That is what an interrupted
// creation leaves behind, and reporting it as a task would invent one; naming
// it as an orphaned resource belongs to reconciliation.
func (t taskStore) List(ctx context.Context, project domain.ProjectID) ([]*domain.Task, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	projectDir, err := t.store.projectDir(project)
	if err != nil {
		return nil, err
	}
	entries, err := listDir(filepath.Join(projectDir, tasksDir))
	if err != nil {
		return nil, err
	}

	tasks := make([]*domain.Task, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		ref := store.TaskRef{Project: project, Task: domain.TaskID(entry.Name())}
		if ref.Validate() != nil {
			continue
		}
		task, err := t.store.loadTask(ref, filepath.Join(projectDir, tasksDir, entry.Name()))
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, nil
}

func (s *Store) loadTask(ref store.TaskRef, dir string) (*domain.Task, error) {
	path := filepath.Join(dir, taskFile)

	var document taskDocument
	if err := s.readSnapshot(taskCodec, "task", ref.String(), path, &document); err != nil {
		return nil, err
	}

	briefPath := filepath.Join(dir, briefFile)
	brief, err := readFile(briefPath)
	if errors.Is(err, iofs.ErrNotExist) {
		return nil, corrupt("task", ref.String(), briefPath, errors.New("the task snapshot exists but its brief is missing"))
	}
	if err != nil {
		return nil, err
	}

	task := decodeTask(document, string(brief))
	if err := task.Validate(); err != nil {
		return nil, corrupt("task", ref.String(), path, err)
	}
	if task.ProjectID != ref.Project || task.ID != ref.Task {
		return nil, corrupt("task", ref.String(), path,
			errors.New("the snapshot records task "+task.ProjectID.String()+"/"+task.ID.String()))
	}
	return task, nil
}

func encodeTask(task *domain.Task) taskDocument {
	document := taskDocument{
		SchemaVersion: taskSchemaVersion,
		ID:            task.ID.String(),
		UpdatedAt:     task.UpdatedAt.UTC(),
		ProjectID:     task.ProjectID.String(),
		Title:         task.Title,
		Source: sourceDocument{
			Kind:      string(task.Source.Kind),
			Reference: task.Source.Reference,
			Ticket:    encodeTicket(task.Source.Ticket),
		},
		Workflow:  string(task.Workflow),
		Attention: string(task.Attention),
		Failure:   encodeFailure(task.Failure),
		CreatedAt: task.CreatedAt.UTC(),
	}
	for _, binding := range task.Repositories {
		document.Repositories = append(document.Repositories, taskRepositoryDocument{
			RepositoryID:  binding.RepositoryID.String(),
			Access:        string(binding.Access),
			BaseRef:       binding.BaseRef,
			BaseCommit:    binding.BaseCommit,
			Branch:        binding.Branch,
			WorktreePath:  binding.WorktreePath,
			ContainerPath: binding.ContainerPath,
			Observation:   encodeObservation(binding.Observation),
		})
	}
	if task.Session != nil {
		document.Session = &sessionDocument{
			Provider:      task.Session.Provider,
			ExecutionMode: string(task.Session.ExecutionMode),
			Tmux: tmuxDocument{
				Socket:  task.Session.Tmux.Socket,
				Session: task.Session.Tmux.Session,
				Window:  task.Session.Tmux.Window,
				Pane:    task.Session.Tmux.Pane,
			},
			ProviderSessionID: task.Session.ProviderSessionID,
			Process:           string(task.Session.Process),
			ControlPath:       task.Session.ControlPath,
			Execution:         encodeExecution(task.Session.Execution),
			LastEventSequence: task.Session.LastEventSequence,
			CreatedAt:         task.Session.CreatedAt.UTC(),
			LastActivityAt:    task.Session.LastActivityAt.UTC(),
		}
	}
	if task.Runtime != nil {
		document.Runtime = encodeRuntime(task.Runtime)
	}
	if task.Publication != nil {
		document.Publication = encodePublication(task.Publication)
	}
	return document
}

// encodeTicket records the ticket a task was composed from.
func encodeTicket(ticket *domain.ExternalTaskReference) *ticketDocument {
	if ticket == nil {
		return nil
	}
	return &ticketDocument{
		Provider:  ticket.Provider,
		Reference: ticket.Reference,
		URL:       ticket.URL,
		Snapshot: ticketSnapshotDocument{
			Title:   ticket.Snapshot.Title,
			Body:    ticket.Snapshot.Body,
			State:   ticket.Snapshot.State,
			TakenAt: ticket.Snapshot.TakenAt.UTC(),
		},
		ChangeAvailable: ticket.ChangeAvailable,
	}
}

// decodeTicket reads the ticket a task was composed from.
func decodeTicket(document *ticketDocument) *domain.ExternalTaskReference {
	if document == nil {
		return nil
	}
	return &domain.ExternalTaskReference{
		Provider:  document.Provider,
		Reference: document.Reference,
		URL:       document.URL,
		Snapshot: domain.TicketSnapshot{
			Title:   document.Snapshot.Title,
			Body:    document.Snapshot.Body,
			State:   document.Snapshot.State,
			TakenAt: document.Snapshot.TakenAt.UTC(),
		},
		ChangeAvailable: document.ChangeAvailable,
	}
}

// encodePublication records what publishing the task planned and what came of
// it.
func encodePublication(publication *domain.Publication) *publicationDocument {
	document := &publicationDocument{
		PlannedAt: publication.PlannedAt.UTC(),
		UpdatedAt: publication.UpdatedAt.UTC(),
	}
	for _, entry := range publication.Repositories {
		repository := repositoryPublicationDocument{
			RepositoryID: entry.RepositoryID.String(),
			Forge:        string(entry.Forge),
			Remote:       entry.Remote,
			BaseBranch:   entry.BaseBranch,
			Commit:       entry.Commit,
			State:        string(entry.State),
			Failure:      entry.Failure,
			AttemptedAt:  optionalTime(entry.AttemptedAt),
		}
		if entry.Request != nil {
			repository.Request = &mergeRequestDocument{
				Reference: entry.Request.Reference,
				URL:       entry.Request.URL,
			}
		}
		document.Repositories = append(document.Repositories, repository)
	}
	return document
}

// decodePublication reads what publishing the task planned and what came of it.
func decodePublication(document *publicationDocument) *domain.Publication {
	publication := &domain.Publication{
		PlannedAt: document.PlannedAt.UTC(),
		UpdatedAt: document.UpdatedAt.UTC(),
	}
	for _, entry := range document.Repositories {
		repository := domain.RepositoryPublication{
			RepositoryID: domain.RepositoryID(entry.RepositoryID),
			Forge:        domain.ForgeKind(entry.Forge),
			Remote:       entry.Remote,
			BaseBranch:   entry.BaseBranch,
			Commit:       entry.Commit,
			State:        domain.PublicationState(entry.State),
			Failure:      entry.Failure,
			AttemptedAt:  timeValue(entry.AttemptedAt),
		}
		if entry.Request != nil {
			repository.Request = &domain.MergeRequest{
				Reference: entry.Request.Reference,
				URL:       entry.Request.URL,
			}
		}
		publication.Repositories = append(publication.Repositories, repository)
	}
	return publication
}

func encodeFailure(failure *domain.TaskFailure) *failureDocument {
	if failure == nil {
		return nil
	}
	return &failureDocument{Reason: failure.Reason, At: failure.At.UTC()}
}

func decodeFailure(document *failureDocument) *domain.TaskFailure {
	if document == nil {
		return nil
	}
	return &domain.TaskFailure{Reason: document.Reason, At: document.At.UTC()}
}

func encodeObservation(observation *domain.GitObservation) *observationDocument {
	if observation == nil {
		return nil
	}
	return &observationDocument{
		Dirty:        observation.Dirty,
		Ahead:        observation.Ahead,
		Behind:       observation.Behind,
		Merged:       observation.Merged,
		ChangedFiles: observation.ChangedFiles,
		ObservedAt:   observation.ObservedAt.UTC(),
	}
}

func encodeRuntime(runtime *domain.RuntimeEnvironment) *runtimeDocument {
	document := &runtimeDocument{
		Provider:              runtime.Provider,
		Identity:              runtime.Identity,
		Composition:           encodeComposition(runtime.Composition),
		GeneratedIncludePath:  runtime.GeneratedIncludePath,
		StaticOverrides:       runtime.StaticOverrides,
		GeneratedOverridePath: runtime.GeneratedOverridePath,
		EnvFiles:              runtime.EnvFiles,
		Services:              runtime.Services,
		Provenance:            encodeProvenance(runtime.Provenance),
		Networks:              runtime.Networks,
		Volumes:               runtime.Volumes,
		State:                 string(runtime.State),
		Health:                string(runtime.Health),
		ObservedAt:            optionalTime(runtime.ObservedAt),
		Generation:            runtime.Generation,
	}
	for _, allocation := range runtime.Allocations {
		document.Allocations = append(document.Allocations, allocationDocument{
			Service:       allocation.Service,
			ContainerPort: allocation.ContainerPort,
			HostPort:      allocation.HostPort,
			Protocol:      allocation.Protocol,
			HostIP:        allocation.HostIP,
		})
	}
	for _, port := range runtime.Ports {
		document.Ports = append(document.Ports, portDocument{
			Service:       port.Service,
			ContainerPort: port.ContainerPort,
			HostPort:      port.HostPort,
			HostIP:        port.HostIP,
		})
	}
	return document
}

func decodeTask(document taskDocument, brief string) *domain.Task {
	task := &domain.Task{
		ID:        domain.TaskID(document.ID),
		ProjectID: domain.ProjectID(document.ProjectID),
		Title:     document.Title,
		Brief:     brief,
		Source: domain.TaskSource{
			Kind:      domain.SourceKind(document.Source.Kind),
			Reference: document.Source.Reference,
			Ticket:    decodeTicket(document.Source.Ticket),
		},
		Workflow:  domain.WorkflowState(document.Workflow),
		Attention: domain.AttentionState(document.Attention),
		Failure:   decodeFailure(document.Failure),
		CreatedAt: document.CreatedAt.UTC(),
		UpdatedAt: document.UpdatedAt.UTC(),
	}
	for _, binding := range document.Repositories {
		task.Repositories = append(task.Repositories, domain.TaskRepository{
			RepositoryID:  domain.RepositoryID(binding.RepositoryID),
			Access:        domain.TaskAccess(binding.Access),
			BaseRef:       binding.BaseRef,
			BaseCommit:    binding.BaseCommit,
			Branch:        binding.Branch,
			WorktreePath:  binding.WorktreePath,
			ContainerPath: binding.ContainerPath,
			Observation:   decodeObservation(binding.Observation),
		})
	}
	if document.Session != nil {
		task.Session = &domain.AgentSession{
			Provider:      document.Session.Provider,
			ExecutionMode: domain.ExecutionMode(document.Session.ExecutionMode),
			Tmux: domain.TmuxTarget{
				Socket:  document.Session.Tmux.Socket,
				Session: document.Session.Tmux.Session,
				Window:  document.Session.Tmux.Window,
				Pane:    document.Session.Tmux.Pane,
			},
			ProviderSessionID: document.Session.ProviderSessionID,
			Process:           domain.ProcessState(document.Session.Process),
			ControlPath:       document.Session.ControlPath,
			Execution:         decodeExecution(document.Session.Execution),
			LastEventSequence: document.Session.LastEventSequence,
			CreatedAt:         document.Session.CreatedAt.UTC(),
			LastActivityAt:    document.Session.LastActivityAt.UTC(),
		}
	}
	if document.Runtime != nil {
		task.Runtime = decodeRuntime(document.Runtime)
	}
	if document.Publication != nil {
		task.Publication = decodePublication(document.Publication)
	}
	return task
}

// encodeComposition records what a task's application is composed of.
func encodeComposition(sources []domain.RuntimeSource) []compositionDocument {
	if len(sources) == 0 {
		return nil
	}
	documents := make([]compositionDocument, 0, len(sources))
	for _, source := range sources {
		documents = append(documents, compositionDocument{
			Repository: source.Repository,
			Directory:  source.Directory,
			Files:      append([]string(nil), source.Files...),
		})
	}
	return documents
}

// decodeComposition reads what a task's application is composed of.
func decodeComposition(documents []compositionDocument) []domain.RuntimeSource {
	if len(documents) == 0 {
		return nil
	}
	sources := make([]domain.RuntimeSource, 0, len(documents))
	for _, document := range documents {
		sources = append(sources, domain.RuntimeSource{
			Repository: document.Repository,
			Directory:  document.Directory,
			Files:      append([]string(nil), document.Files...),
		})
	}
	return sources
}

// encodeProvenance records where each managed service's code comes from.
func encodeProvenance(provenance []domain.ServiceProvenance) []provenanceDocument {
	if len(provenance) == 0 {
		return nil
	}
	documents := make([]provenanceDocument, 0, len(provenance))
	for _, entry := range provenance {
		documents = append(documents, provenanceDocument{
			Service:      entry.Service,
			Repositories: append([]string(nil), entry.Repositories...),
			Mounted:      append([]string(nil), entry.Mounted...),
			Built:        append([]string(nil), entry.Built...),
		})
	}
	return documents
}

// decodeProvenance reads where each managed service's code comes from.
func decodeProvenance(documents []provenanceDocument) []domain.ServiceProvenance {
	if len(documents) == 0 {
		return nil
	}
	provenance := make([]domain.ServiceProvenance, 0, len(documents))
	for _, document := range documents {
		provenance = append(provenance, domain.ServiceProvenance{
			Service:      document.Service,
			Repositories: append([]string(nil), document.Repositories...),
			Mounted:      append([]string(nil), document.Mounted...),
			Built:        append([]string(nil), document.Built...),
		})
	}
	return provenance
}

// encodeExecution records the environment an agent session runs in.
func encodeExecution(environment *domain.ExecutionEnvironment) *executionDocument {
	if environment == nil {
		return nil
	}
	return &executionDocument{
		Provider:              environment.Provider,
		Identity:              environment.Identity,
		Files:                 append([]string(nil), environment.Files...),
		GeneratedOverridePath: environment.GeneratedOverridePath,
		Service:               environment.Service,
		User:                  environment.User,
		Container:             environment.Container,
		Running:               environment.Running,
		Status:                environment.Status,
		Health:                string(environment.Health),
		ObservedAt:            environment.ObservedAt.UTC(),
	}
}

func decodeExecution(document *executionDocument) *domain.ExecutionEnvironment {
	if document == nil {
		return nil
	}
	return &domain.ExecutionEnvironment{
		Provider:              document.Provider,
		Identity:              document.Identity,
		Files:                 append([]string(nil), document.Files...),
		GeneratedOverridePath: document.GeneratedOverridePath,
		Service:               document.Service,
		User:                  document.User,
		Container:             document.Container,
		Running:               document.Running,
		Status:                document.Status,
		Health:                domain.HealthState(document.Health),
		ObservedAt:            document.ObservedAt.UTC(),
	}
}

func decodeObservation(document *observationDocument) *domain.GitObservation {
	if document == nil {
		return nil
	}
	return &domain.GitObservation{
		Dirty:        document.Dirty,
		Ahead:        document.Ahead,
		Behind:       document.Behind,
		Merged:       document.Merged,
		ChangedFiles: document.ChangedFiles,
		ObservedAt:   document.ObservedAt.UTC(),
	}
}

func decodeRuntime(document *runtimeDocument) *domain.RuntimeEnvironment {
	runtime := &domain.RuntimeEnvironment{
		Provider:              document.Provider,
		Identity:              document.Identity,
		Composition:           decodeComposition(document.Composition),
		GeneratedIncludePath:  document.GeneratedIncludePath,
		StaticOverrides:       document.StaticOverrides,
		GeneratedOverridePath: document.GeneratedOverridePath,
		EnvFiles:              document.EnvFiles,
		Services:              document.Services,
		Provenance:            decodeProvenance(document.Provenance),
		Networks:              document.Networks,
		Volumes:               document.Volumes,
		State:                 domain.RuntimeState(document.State),
		Health:                domain.HealthState(document.Health),
		ObservedAt:            timeValue(document.ObservedAt),
		Generation:            document.Generation,
	}
	for _, allocation := range document.Allocations {
		runtime.Allocations = append(runtime.Allocations, domain.PortAllocation{
			Service:       allocation.Service,
			ContainerPort: allocation.ContainerPort,
			HostPort:      allocation.HostPort,
			Protocol:      allocation.Protocol,
			HostIP:        allocation.HostIP,
		})
	}
	for _, port := range document.Ports {
		runtime.Ports = append(runtime.Ports, domain.PortAssignment{
			Service:       port.Service,
			ContainerPort: port.ContainerPort,
			HostPort:      port.HostPort,
			HostIP:        port.HostIP,
		})
	}
	return runtime
}
