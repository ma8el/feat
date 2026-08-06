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
	CreatedAt     time.Time                `json:"created_at"`
	Repositories  []taskRepositoryDocument `json:"repositories,omitempty"`
	Session       *sessionDocument         `json:"session,omitempty"`
	Runtime       *runtimeDocument         `json:"runtime,omitempty"`
}

type sourceDocument struct {
	Kind      string `json:"kind"`
	Reference string `json:"reference,omitempty"`
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
	Provider              string                     `json:"provider"`
	Identity              string                     `json:"identity"`
	ComposeFiles          []string                   `json:"compose_files,omitempty"`
	StaticOverrides       []string                   `json:"static_overrides,omitempty"`
	GeneratedOverridePath string                     `json:"generated_override_path,omitempty"`
	EnvFiles              []string                   `json:"env_files,omitempty"`
	Services              []string                   `json:"services,omitempty"`
	Ports                 []portDocument             `json:"ports,omitempty"`
	Networks              []string                   `json:"networks,omitempty"`
	Volumes               []string                   `json:"volumes,omitempty"`
	State                 string                     `json:"state"`
	Health                string                     `json:"health"`
	ExternalResources     []externalResourceDocument `json:"external_resources,omitempty"`
	ObservedAt            *time.Time                 `json:"observed_at,omitempty"`
}

type portDocument struct {
	Service       string `json:"service"`
	ContainerPort int    `json:"container_port"`
	HostPort      int    `json:"host_port"`
}

type externalResourceDocument struct {
	ID        string `json:"id"`
	Kind      string `json:"kind,omitempty"`
	Lifecycle string `json:"lifecycle"`
	Selector  string `json:"selector,omitempty"`
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
		Source:        sourceDocument{Kind: string(task.Source.Kind), Reference: task.Source.Reference},
		Workflow:      string(task.Workflow),
		Attention:     string(task.Attention),
		CreatedAt:     task.CreatedAt.UTC(),
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
	return document
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
		ComposeFiles:          runtime.ComposeFiles,
		StaticOverrides:       runtime.StaticOverrides,
		GeneratedOverridePath: runtime.GeneratedOverridePath,
		EnvFiles:              runtime.EnvFiles,
		Services:              runtime.Services,
		Networks:              runtime.Networks,
		Volumes:               runtime.Volumes,
		State:                 string(runtime.State),
		Health:                string(runtime.Health),
		ObservedAt:            optionalTime(runtime.ObservedAt),
	}
	for _, port := range runtime.Ports {
		document.Ports = append(document.Ports, portDocument{
			Service:       port.Service,
			ContainerPort: port.ContainerPort,
			HostPort:      port.HostPort,
		})
	}
	for _, resource := range runtime.ExternalResources {
		document.ExternalResources = append(document.ExternalResources, externalResourceDocument{
			ID:        resource.ID,
			Kind:      resource.Kind,
			Lifecycle: string(resource.Lifecycle),
			Selector:  resource.Selector,
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
		},
		Workflow:  domain.WorkflowState(document.Workflow),
		Attention: domain.AttentionState(document.Attention),
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
	return task
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
		ComposeFiles:          document.ComposeFiles,
		StaticOverrides:       document.StaticOverrides,
		GeneratedOverridePath: document.GeneratedOverridePath,
		EnvFiles:              document.EnvFiles,
		Services:              document.Services,
		Networks:              document.Networks,
		Volumes:               document.Volumes,
		State:                 domain.RuntimeState(document.State),
		Health:                domain.HealthState(document.Health),
		ObservedAt:            timeValue(document.ObservedAt),
	}
	for _, port := range document.Ports {
		runtime.Ports = append(runtime.Ports, domain.PortAssignment{
			Service:       port.Service,
			ContainerPort: port.ContainerPort,
			HostPort:      port.HostPort,
		})
	}
	for _, resource := range document.ExternalResources {
		runtime.ExternalResources = append(runtime.ExternalResources, domain.ExternalResource{
			ID:        resource.ID,
			Kind:      resource.Kind,
			Lifecycle: domain.ResourceLifecycle(resource.Lifecycle),
			Selector:  resource.Selector,
		})
	}
	return runtime
}
