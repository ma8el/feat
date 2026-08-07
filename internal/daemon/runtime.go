package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/config"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/runtime"
	"github.com/ma8el/feat/internal/runtime/compose"
)

// defaultRuntimeInterval is how often the daemon observes application services.
//
// It is deliberately much longer than the control poller's interval. A control
// message is how a task reports that it is waiting for a person; a container
// that stopped on its own is something a user finds out about when they next
// look at the dashboard, and asking Docker four times a second would cost more
// than the answer is worth.
const defaultRuntimeInterval = 5 * time.Second

// runtimeProvider is the adapter identifier recorded on a task's runtime.
const runtimeProvider = "compose"

// runtimeOverrideName is the generated Compose override of one task's
// application.
const runtimeOverrideName = "compose.override.yaml"

// Generated non-secret variables every managed service receives.
//
// They are generated task metadata rather than anything read from the project's
// environment files, which Feat never opens (docs/05-security-model.md). They
// exist so that an application can tell which task it is serving — in a log
// line, a page footer, or the name of a shared resource it selects.
const (
	varProject  = "FEAT_PROJECT_ID"
	varTask     = "FEAT_TASK_ID"
	varTaskKey  = "FEAT_TASK_KEY"
	varIdentity = "FEAT_RUNTIME_PROJECT"
)

// Runtime performs one manual runtime action for a task.
//
// Every action is a user's explicit request. No workflow transition, no
// reconciliation, and no agent reaches this: v0 starts application services only
// by explicit user action, approval offers to stop them and never does, and a
// request an agent writes is inert until a person approves it (FR-RUN-005,
// FR-RUN-009).
//
// The order is the recoverability rule ADR-029 set for worktrees and ADR-033 for
// containers, applied to application services: the record naming what may exist
// is written before anything is created, so an interruption anywhere leaves a
// record naming a superset of what exists.
func (s *service) Runtime(
	ctx context.Context, id domain.TaskID, action api.RuntimeAction,
) (api.RuntimeResult, error) {
	if !action.Valid() {
		return api.RuntimeResult{}, fmt.Errorf("%w: %q is not a runtime action", api.ErrInvalid, action)
	}

	task, cfg, err := s.runtimeTask(ctx, id)
	if err != nil {
		return api.RuntimeResult{}, err
	}
	services, record, err := s.runtimeFor(ctx, cfg, task, action)
	if err != nil {
		return api.RuntimeResult{}, err
	}

	if action == api.RuntimeObserve {
		// Status creates nothing and validates nothing about the host: a user
		// asking what is running should get an answer even from a machine whose
		// Docker Compose is too old to start anything.
		state, err := services.Observe(ctx)
		if err != nil {
			return api.RuntimeResult{}, err
		}
		return s.recordRuntime(ctx, task, record, state, nil, "status")
	}

	if err := services.Validate(ctx); err != nil {
		return api.RuntimeResult{}, fmt.Errorf("%w: task %s cannot manage its application services: %w",
			api.ErrInvalid, task.ID, err)
	}

	var state runtime.State
	switch action {
	case api.RuntimeCreate:
		state, err = services.Create(ctx)
	case api.RuntimeStart:
		state, err = services.Start(ctx)
	case api.RuntimeStop:
		state, err = services.Stop(ctx)
	case api.RuntimeDestroy:
		state, err = services.Destroy(ctx)
	case api.RuntimeObserve:
		// Answered above, before validation.
	}
	if err != nil {
		// Nothing is undone. A service that started may already have written to a
		// volume or a shared database, and tidying up after a failed start is a
		// destructive act the user did not ask for (ADR-029, ADR-033).
		return api.RuntimeResult{}, fmt.Errorf("%w: %w", api.ErrInvalid, err)
	}

	var notes []string
	if action == api.RuntimeCreate || action == api.RuntimeStart {
		notes = s.inspectRuntime(ctx, task, services, state)
	}
	return s.recordRuntime(ctx, task, record, state, notes, string(action))
}

// RuntimeLogs returns the command that opens a task's normal Compose logs.
//
// The daemon builds it and the client runs it, which is the same division
// `feat attach` uses: the daemon resolves what a task owns, and the caller's own
// terminal is what the output belongs in (FR-RUN-006).
func (s *service) RuntimeLogs(ctx context.Context, id domain.TaskID) (api.RuntimeCommand, error) {
	task, cfg, err := s.runtimeTask(ctx, id)
	if err != nil {
		return api.RuntimeCommand{}, err
	}
	services, _, err := s.runtimeFor(ctx, cfg, task, api.RuntimeObserve)
	if err != nil {
		return api.RuntimeCommand{}, err
	}

	invocation, err := services.Logs(ctx)
	if err != nil {
		return api.RuntimeCommand{}, err
	}
	return api.RuntimeCommand{
		Program:   invocation.Program,
		Arguments: invocation.Arguments,
		Directory: invocation.Directory,
	}, nil
}

// runtimeTask loads a task and the configuration its runtime comes from.
func (s *service) runtimeTask(ctx context.Context, id domain.TaskID) (*domain.Task, *config.Config, error) {
	task, err := s.Task(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if task.Workflow == domain.WorkflowDraft {
		return nil, nil, fmt.Errorf("%w: task %s is still a draft, and nothing has been created for it. "+
			"Confirm it first", api.ErrInvalid, id)
	}

	cfg, err := config.Load(s.layout.ProjectConfigDir(), task.ProjectID.String(), s.configOptions())
	if err != nil {
		return nil, nil, translateConfig(err)
	}
	if !cfg.HasRuntime() {
		return nil, nil, fmt.Errorf("%w: project %s configures no application runtime, so task %s has no "+
			"services to manage. Add a runtime section to %s",
			api.ErrInvalid, task.ProjectID, id, cfg.Path())
	}
	return task, cfg, nil
}

// runtimeFor builds the adapter for a task and makes sure the task's record
// names what the adapter may create.
//
// The record comes first and is saved before the adapter is used, so an
// interruption leaves a task naming a Compose project that may exist rather than
// resources nothing can name.
func (s *service) runtimeFor(
	ctx context.Context, cfg *config.Config, task *domain.Task, action api.RuntimeAction,
) (runtime.Runtime, *domain.RuntimeEnvironment, error) {
	spec, err := s.runtimeSpec(cfg, task)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", api.ErrInvalid, err)
	}

	record, err := s.recordRuntimeInputs(ctx, task, spec, action)
	if err != nil {
		return nil, nil, err
	}
	// What the task already owns wins over what configuration says today. An
	// edited project file must not point a stop or a destroy at a different
	// Compose project than the one that was started.
	spec.Identity = record.Identity
	spec.Files = record.ComposeFiles
	spec.StaticOverrides = record.StaticOverrides
	spec.OverridePath = record.GeneratedOverridePath
	spec.EnvFiles = record.EnvFiles
	spec.Services = record.Services

	services, err := s.runtimes(spec)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", api.ErrInvalid, err)
	}
	return services, record, nil
}

// recordRuntimeInputs attaches or refreshes the task's runtime record.
//
// A task with no runtime gets one, absent and observing nothing. A task whose
// runtime is absent — never created, or destroyed since — has its inputs
// re-resolved from the project's current configuration. A task with resources
// keeps the inputs those resources were created from.
func (s *service) recordRuntimeInputs(
	ctx context.Context, task *domain.Task, spec runtime.Spec, action api.RuntimeAction,
) (*domain.RuntimeEnvironment, error) {
	inputs := domain.RuntimeInputs{
		Provider:              runtimeProvider,
		Identity:              spec.Identity,
		ComposeFiles:          spec.Files,
		StaticOverrides:       spec.StaticOverrides,
		GeneratedOverridePath: spec.OverridePath,
		EnvFiles:              spec.EnvFiles,
		Services:              spec.Services,
		ExternalResources:     externalResources(spec),
	}

	switch {
	case task.Runtime == nil:
		record := domain.NewRuntimeEnvironment(inputs)
		if err := task.AttachRuntime(record, s.now()); err != nil {
			return nil, err
		}
		if err := s.store.Tasks().Save(ctx, task); err != nil {
			return nil, err
		}
		s.record(ctx, task, domain.Event{
			Type: domain.EventRuntimeChanged,
			To:   string(domain.RuntimeAbsent),
			Detail: "the task's application services are Compose project " + record.Identity +
				", recorded before the first " + string(action),
		})
		return record, nil

	case task.Runtime.State == domain.RuntimeAbsent && task.Runtime.Identity != spec.Identity:
		// Nothing exists, so re-resolving cannot orphan anything, and the user
		// has evidently changed something they wanted changed.
		if err := task.Runtime.ReplaceInputs(inputs, s.now()); err != nil {
			return nil, err
		}
		if err := s.store.Tasks().Save(ctx, task); err != nil {
			return nil, err
		}
		s.record(ctx, task, domain.Event{
			Type:   domain.EventRuntimeChanged,
			To:     string(domain.RuntimeAbsent),
			Detail: "the task's application services are now Compose project " + task.Runtime.Identity,
		})
		return task.Runtime, nil

	case task.Runtime.State == domain.RuntimeAbsent:
		// Same identity: pick up an edited file list without an event nobody
		// would learn anything from.
		if err := task.Runtime.ReplaceInputs(inputs, s.now()); err != nil {
			return nil, err
		}
		if err := s.store.Tasks().Save(ctx, task); err != nil {
			return nil, err
		}
		return task.Runtime, nil

	default:
		if task.Runtime.Provider != runtimeProvider {
			return nil, fmt.Errorf("%w: task %s records the runtime provider %q, which this build has no "+
				"adapter for", api.ErrInvalid, task.ID, task.Runtime.Provider)
		}
		return task.Runtime, nil
	}
}

// recordRuntime saves what an action observed and reports it.
//
// The event is published only when the state or health actually changed. Every
// poll would otherwise publish, every publication makes a dashboard re-read, and
// a re-read that publishes is the loop slice 6 paid for once already.
func (s *service) recordRuntime(
	ctx context.Context, task *domain.Task, record *domain.RuntimeEnvironment,
	state runtime.State, notes []string, action string,
) (api.RuntimeResult, error) {
	from, fromHealth := record.State, record.Health

	if err := record.Observe(state.Lifecycle, state.Health, s.now()); err != nil {
		return api.RuntimeResult{}, err
	}
	record.ObserveResources(ports(state), state.Networks, state.Volumes, s.now())

	if err := s.store.Tasks().Save(ctx, task); err != nil {
		return api.RuntimeResult{}, err
	}
	if from != record.State || fromHealth != record.Health {
		s.record(ctx, task, domain.Event{
			Type:   domain.EventRuntimeChanged,
			From:   string(from),
			To:     string(record.State),
			Detail: describeRuntime(record, action),
		})
	}
	for _, note := range notes {
		s.logger.WarnContext(ctx, "a task's application services mount something unexpected",
			slog.String("task", task.ID.String()), slog.String("note", note))
	}
	return api.RuntimeResult{Task: task, Services: serviceStates(state), Notes: notes}, nil
}

// describeRuntime says what changed, in the words a user would use.
func describeRuntime(record *domain.RuntimeEnvironment, action string) string {
	detail := "Compose project " + record.Identity + " is " + string(record.State)
	if record.Health != domain.HealthUnknown {
		detail += ", health " + string(record.Health)
	}
	return detail + " after " + action
}

// inspectRuntime asks the started containers what they turned out to mount.
//
// A failure here is logged and does not fail the action: the services are
// running, and a user who has just started them should not be told the start
// failed because a second question could not be asked.
func (s *service) inspectRuntime(
	ctx context.Context, task *domain.Task, services runtime.Runtime, state runtime.State,
) []string {
	report, err := services.Inspect(ctx, state)
	if err != nil {
		s.logger.WarnContext(ctx, "inspecting a task's application containers",
			slog.String("task", task.ID.String()), slog.Any("error", err))
		return nil
	}
	return report.Notes
}

// runtimeSpec resolves a task's application runtime from configuration.
//
// It is the only place the two vocabularies meet: everything the adapter
// receives is final here, and the adapter reads no configuration (ADR-034).
func (s *service) runtimeSpec(cfg *config.Config, task *domain.Task) (runtime.Spec, error) {
	section := cfg.Runtime
	if section == nil {
		return runtime.Spec{}, fmt.Errorf("project %s configures no application runtime", task.ProjectID)
	}
	if len(section.ComposeFiles) == 0 {
		return runtime.Spec{}, fmt.Errorf("project %s configures no Compose files for its application runtime",
			task.ProjectID)
	}

	identity, err := config.Expand(section.ProjectNameTemplate, config.Values{
		ProjectID: task.ProjectID.String(),
		TaskID:    task.ID.String(),
		TaskKey:   task.Key().String(),
	})
	if err != nil {
		return runtime.Spec{}, err
	}
	override, err := s.runtimeOverridePath(task)
	if err != nil {
		return runtime.Spec{}, err
	}

	spec := runtime.Spec{
		Project:         task.ProjectID,
		Task:            task.ID,
		Identity:        identity,
		Files:           append([]string(nil), section.ComposeFiles...),
		StaticOverrides: append([]string(nil), section.StaticOverrides...),
		// The first configured file's directory, so that file's own relative
		// sources and build contexts resolve as they do when the user runs
		// Compose by hand (ADR-033, ADR-034).
		Directory:    filepath.Dir(section.ComposeFiles[0]),
		OverridePath: override,
		EnvFiles:     append([]string(nil), section.EnvFiles...),
		Services:     append([]string(nil), section.Services...),
		Mounts:       runtimeMounts(cfg, task),
		Variables: map[string]string{
			varProject:  task.ProjectID.String(),
			varTask:     task.ID.String(),
			varTaskKey:  task.Key().String(),
			varIdentity: identity,
		},
		ForbiddenSources: checkouts(cfg),
	}

	for _, name := range sortedResourceNames(section.ExternalResources) {
		resource := section.ExternalResources[name]
		binding := runtime.ExternalBinding{ID: name, Kind: resource.Type, Variable: resource.SelectorVariable}
		if binding.Variable != "" {
			// The task key: short, unique, safe in a name, and not a secret. Feat
			// names the share and never creates, migrates, or drops anything
			// behind it — the resource is external, and OQ-011 leaves what a
			// project does with the name to the project.
			binding.Selector = task.Key().String()
			spec.Variables[binding.Variable] = binding.Selector
		}
		spec.External = append(spec.External, binding)
	}

	if err := spec.Validate(); err != nil {
		return runtime.Spec{}, err
	}
	return spec, nil
}

// runtimeOverridePath is where a task's generated Compose override is written.
//
// Both identifiers are validated before either reaches a path, so no stored
// value can name a directory outside the runtime root.
func (s *service) runtimeOverridePath(task *domain.Task) (string, error) {
	if err := task.ProjectID.Validate(); err != nil {
		return "", err
	}
	if err := task.ID.Validate(); err != nil {
		return "", err
	}
	return filepath.Join(
		s.layout.RuntimeRoot(), task.ProjectID.String(), task.ID.String(), runtimeOverrideName), nil
}

// runtimeMounts is the code the task's services run.
//
// Each selected repository's task worktree, at the container path its repository
// configures, with the access the task has. Compose merges by target, so this
// replaces whatever the project's own files mounted there — and a container_path
// that disagrees with those files adds a mount instead, which the adapter
// reports after the services are up (ADR-034).
//
// The container path comes from the project's configuration rather than from the
// task's recorded binding, and the difference matters. A binding carries one only
// for devcontainer execution, because that field records where the *agent's* own
// container mounts the worktree and a host-native agent has no container. An
// application runtime has containers whatever the agent does, so a host-execution
// project would otherwise mount nothing and its services would quietly run the
// user's ordinary checkout — with everything Feat recorded still correct.
//
// A repository with no container path is skipped rather than guessed at: a
// project whose services do not carry the code is a valid project.
func runtimeMounts(cfg *config.Config, task *domain.Task) []runtime.Mount {
	var mounts []runtime.Mount

	for _, binding := range task.Repositories {
		repository, known := cfg.Repositories[binding.RepositoryID.String()]
		if !known {
			continue
		}
		if repository.ContainerPath == "" || binding.WorktreePath == "" {
			continue
		}
		access := "read-write"
		if binding.Access == domain.TaskAccessReadOnly {
			access = "read-only"
		}
		mounts = append(mounts, runtime.Mount{
			Source:      binding.WorktreePath,
			Target:      repository.ContainerPath,
			ReadOnly:    binding.Access == domain.TaskAccessReadOnly,
			Description: "the " + binding.RepositoryID.String() + " task worktree, " + access,
		})
	}
	return mounts
}

// sortedResourceNames orders the configured external resources, so that a
// generated document and a recorded list are the same every time.
func sortedResourceNames(resources map[string]config.ExternalResource) []string {
	names := make([]string, 0, len(resources))
	for name := range resources {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// externalResources records what the runtime references and never owns.
func externalResources(spec runtime.Spec) []domain.ExternalResource {
	resources := make([]domain.ExternalResource, 0, len(spec.External))
	for _, binding := range spec.External {
		resources = append(resources, domain.ExternalResource{
			ID:   binding.ID,
			Kind: binding.Kind,
			// Always external. The lifecycle is recorded explicitly rather than
			// implied, so that a cleanup plan can prove it excluded the resource
			// rather than merely not mentioning it (FR-RUN-008).
			Lifecycle: domain.LifecycleExternal,
			Selector:  binding.Selector,
		})
	}
	return resources
}

// ports maps observed publications onto the domain's.
func ports(state runtime.State) []domain.PortAssignment {
	if len(state.Ports) == 0 {
		return nil
	}
	return append([]domain.PortAssignment(nil), state.Ports...)
}

// serviceStates maps observed services onto the API's.
func serviceStates(state runtime.State) []api.RuntimeService {
	services := make([]api.RuntimeService, 0, len(state.Services))
	for _, service := range state.Services {
		services = append(services, api.RuntimeService{
			Name:      service.Name,
			Container: service.Container,
			State:     service.State,
			Status:    service.Status,
			Health:    string(service.Health),
			ExitCode:  service.ExitCode,
			Managed:   service.Managed,
		})
	}
	return services
}

// runtimes builds the runtime adapter for one task.
//
// It is a method rather than a package function so that a test can drive a whole
// lifecycle against a fake Docker: whether one task's action can disturb another
// should not depend on the tester having a container runtime (ADR-030's
// reasoning for the tmux fake).
func (s *service) runtimes(spec runtime.Spec) (runtime.Runtime, error) {
	return compose.New(spec, compose.Options{Runner: s.runtimeDocker})
}

// pollRuntimes observes every task that owns a runtime.
//
// Only tasks with a runtime record are asked, only `ps` is run for a runtime
// with nothing in it, and nothing is written or published unless what was
// observed differs from what was recorded. A task whose observation fails is
// logged and the others are still read, for the reason the control poller gives.
func (s *service) pollRuntimes(ctx context.Context) {
	tasks, err := s.Tasks(ctx)
	if err != nil {
		s.logger.WarnContext(ctx, "listing tasks to observe their runtimes", slog.Any("error", err))
		return
	}

	for _, task := range tasks {
		if task.Runtime == nil || task.Workflow == domain.WorkflowArchived {
			continue
		}
		if err := s.observeRuntime(ctx, task); err != nil {
			s.logger.WarnContext(ctx, "observing a task's application services",
				slog.String("task", task.ID.String()), slog.Any("error", err))
		}
	}
}

// observeRuntime reads one task's services and records a change.
func (s *service) observeRuntime(ctx context.Context, task *domain.Task) error {
	cfg, err := config.Load(s.layout.ProjectConfigDir(), task.ProjectID.String(), s.configOptions())
	if err != nil {
		return err
	}
	if !cfg.HasRuntime() {
		// The project's runtime section was removed while the task's services
		// exist. Nothing is done about it: what a task owns is the user's to
		// remove, and slice 12 is where an orphan is reported and acted on.
		return nil
	}

	spec, err := s.runtimeSpec(cfg, task)
	if err != nil {
		return err
	}
	spec.Identity = task.Runtime.Identity
	spec.Files = task.Runtime.ComposeFiles
	spec.StaticOverrides = task.Runtime.StaticOverrides
	spec.OverridePath = task.Runtime.GeneratedOverridePath
	spec.EnvFiles = task.Runtime.EnvFiles
	spec.Services = task.Runtime.Services

	services, err := s.runtimes(spec)
	if err != nil {
		return err
	}
	state, err := services.Observe(ctx)
	if err != nil {
		return err
	}

	if state.Lifecycle == task.Runtime.State && state.Health == task.Runtime.Health {
		// Nothing changed. Saving would rewrite the snapshot and publishing would
		// make every dashboard re-read, several times a minute, for ever.
		return nil
	}
	_, err = s.recordRuntime(ctx, task, task.Runtime, state, nil, "an observation")
	return err
}

// runtimePoller owns the goroutine that observes application services.
type runtimePoller struct {
	once   sync.Once
	cancel context.CancelFunc
	done   chan struct{}
}

// start begins observing in the background.
func (p *runtimePoller) start(ctx context.Context, service *service, interval time.Duration) {
	polling, cancel := context.WithCancel(ctx)
	p.cancel = cancel
	p.done = make(chan struct{})

	go func() {
		defer close(p.done)
		service.watchRuntimes(polling, interval)
	}()
}

// stop ends observing and waits for it to finish.
func (p *runtimePoller) stop() {
	p.once.Do(func() {
		if p.cancel == nil {
			return
		}
		p.cancel()
		<-p.done
	})
}

// watchRuntimes observes until the context ends.
func (s *service) watchRuntimes(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = defaultRuntimeInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.pollRuntimes(ctx)
		}
	}
}
