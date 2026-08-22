package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/config"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/notify"
	"github.com/ma8el/feat/internal/project"
	"github.com/ma8el/feat/internal/runtime"
	"github.com/ma8el/feat/internal/runtime/compose"
	"github.com/ma8el/feat/internal/store"
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

// The generated Compose documents of one task's application.
//
// The include joins the repositories the application is composed of; the
// override is merged last over the result. Both live under the task's own
// runtime directory, are host-only, and are never mounted anywhere.
const (
	runtimeIncludeName  = "compose.include.yaml"
	runtimeOverrideName = "compose.override.yaml"
)

// Generated non-secret variables every managed service receives.
//
// They are generated task metadata rather than anything read from the project's
// environment files, which Feat never opens (docs/05-security-model.md). They
// exist so that an application can tell which task it is serving — in a log
// line, a page footer, or the name of a shared resource it selects.
//
// FEAT_TASK_KEY is the one a project shares an external resource by: it is
// short, unique, safe in a name, and not a secret. Naming a share is all Feat
// does, and it neither knows nor asks what is behind the name — the connection
// string lives in an environment file Feat is forbidden to open (ADR-048).
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

	// One budget for the whole action rather than one per Docker command, so that
	// the ceiling exists as a single number both ends of the request know: the
	// client waits for it, and this stops waiting at it (api.RuntimeTimeout).
	budget := s.runtimeBudget()
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	// One task's records are changed by one goroutine at a time (ADR-036).
	defer s.locks.lock(id)()

	task, cfg, err := s.runtimeTask(ctx, id)
	if err != nil {
		return api.RuntimeResult{}, s.explainRuntime(ctx, id, action, budget, beforeTheAction, err)
	}
	services, record, err := s.runtimeFor(ctx, cfg, task, action)
	if err != nil {
		return api.RuntimeResult{}, s.explainRuntime(ctx, id, action, budget, beforeTheAction, err)
	}

	// What the task's services will run, said before any of them is asked to do
	// anything. It is resolved rather than observed, so a status answers it as
	// well as a start does — and a user who has not started anything yet is the
	// one who can still fix it cheaply. A stop and a destroy are silent about it:
	// they are how a user ends an application, and what its services would have
	// run is no longer the question.
	var provenance []string
	switch action {
	case api.RuntimeCreate, api.RuntimeStart, api.RuntimeObserve:
		provenance = append(provenanceNotes(task, record), reachabilityNotes(cfg, record)...)
	case api.RuntimeStop, api.RuntimeDestroy:
	}

	if action == api.RuntimeObserve {
		// Status creates nothing and validates nothing about the host: a user
		// asking what is running should get an answer even from a machine whose
		// Docker Compose is too old to start anything.
		state, err := services.Observe(ctx)
		if err != nil {
			return api.RuntimeResult{}, s.explainRuntime(ctx, id, action, budget, duringTheAction, err)
		}
		return s.recordRuntime(ctx, task, record, state, provenance, "status")
	}

	if err := services.Validate(ctx); err != nil {
		// Pre-wrapped, so that a genuine validation failure keeps the sentence
		// written for it and only a budget that has gone replaces it.
		return api.RuntimeResult{}, s.explainRuntime(ctx, id, action, budget, beforeTheAction,
			fmt.Errorf("%w: task %s cannot manage its application services: %w",
				api.ErrInvalid, task.ID, err))
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
		return api.RuntimeResult{}, s.explainRuntime(ctx, id, action, budget, duringTheAction, err)
	}

	notes := provenance
	if action == api.RuntimeCreate || action == api.RuntimeStart {
		// Both halves of the same question, in the order they were answered in:
		// what configuration said the services would run, then what the started
		// containers turned out to hold. Configuration cannot see a mount the
		// project's own files add at a path Feat was never told about, and an
		// inspection cannot see a service that has no mount at all.
		notes = append(notes, s.inspectRuntime(ctx, task, services, state)...)
	}
	return s.recordRuntime(ctx, task, record, state, notes, string(action))
}

// runtimeBudget is how long one manual runtime action may take.
func (s *service) runtimeBudget() time.Duration {
	if s.runtimeOverride > 0 {
		return s.runtimeOverride
	}
	return api.RuntimeTimeout
}

// Where in an action the clock ran out, for the two sentences that differ by it.
const (
	beforeTheAction = false
	duringTheAction = true
)

// explainRuntime says what a failed action failed of.
//
// Two of the reasons are not the project's, and neither says so on its own: a
// Docker that was still working when the budget ran out, and a Docker that was
// cut short because the caller went away. Both arrive here as a Compose command
// that did not finish, and both leave whatever Compose had already created on
// the machine — so the message names the action that says what that is rather
// than an outcome this cannot know. The record is a superset of it either way
// (ADR-029, ADR-033), and the observer corrects the state on its next pass.
//
// Every step of an action reports through this, not only the Compose command
// that does the work. Which step noticed the clock is Feat's business rather
// than the user's: a start whose budget went while the daemon was still asking
// Docker its version failed for the same reason as one whose budget went inside
// `up`, and the first used to arrive as `task X cannot manage its application
// services: context deadline exceeded` — the transport error this budget exists
// to replace (ADR-034). It also arrived that way only sometimes, because which
// step is holding the clock when it runs out depends on how loaded the machine
// is, which is how a user meets the same failure with two different messages.
//
// What genuinely differs is what may exist afterwards, and that is what the
// sentences differ in: an action that had not begun created nothing, and there
// is no half-finished Compose command to warn about.
//
// A caller that went away is logged as well as reported, because there is nobody
// left to report it to: the connection that would have carried the answer is the
// thing that has gone, and a daemon that says nothing about it leaves a user
// with a half-started application and an empty log.
//
// An error that already carries ErrInvalid is returned as it stands. It is a
// message somebody wrote for this user, and wrapping it again would produce a
// sentence that says the request was invalid twice.
func (s *service) explainRuntime(
	ctx context.Context, id domain.TaskID, action api.RuntimeAction,
	budget time.Duration, started bool, err error,
) error {
	left := "Docker was stopped part way through and nothing was removed"
	if !started {
		left = "the action had not started, so nothing was created"
	}

	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		s.logger.WarnContext(ctx, "a task's runtime action ran out of time",
			slog.String("task", id.String()), slog.String("action", string(action)),
			slog.Duration("budget", budget), slog.Bool("started", started), slog.Any("error", err))
		return fmt.Errorf("%w: runtime %s on task %s did not finish within %s. %s; "+
			"`feat runtime status %s` says what exists now",
			api.ErrInvalid, action, id, budget, left, id.Key())

	case errors.Is(ctx.Err(), context.Canceled):
		s.logger.WarnContext(context.WithoutCancel(ctx), "a task's runtime action was cancelled by its caller",
			slog.String("task", id.String()), slog.String("action", string(action)),
			slog.Bool("started", started), slog.Any("error", err))
		return fmt.Errorf("%w: runtime %s on task %s was cancelled before it finished. %s; "+
			"`feat runtime status %s` says what exists now",
			api.ErrInvalid, action, id, left, id.Key())

	case errors.Is(err, api.ErrInvalid):
		return err

	default:
		return fmt.Errorf("%w: %w", api.ErrInvalid, err)
	}
}

// RuntimeLogs returns the command that opens a task's normal Compose logs.
//
// The daemon builds it and the client runs it, which is the same division
// `feat attach` uses: the daemon resolves what a task owns, and the caller's own
// terminal is what the output belongs in (FR-RUN-006).
//
// It takes the task's lock even though it creates nothing, because resolving
// what a task owns goes through runtimeFor, which attaches or refreshes the
// runtime record and saves it. That is a read-modify-write of one task's
// records, and every one of those runs under the task's own lock (ADR-036) —
// asking where the logs are must not overwrite what a start wrote while it was
// being asked.
func (s *service) RuntimeLogs(ctx context.Context, id domain.TaskID) (api.RuntimeCommand, error) {
	defer s.locks.lock(id)()

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
	documents := readComposition(cfg)
	spec, err := s.runtimeSpec(cfg, task, documents)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", api.ErrInvalid, err)
	}

	// One act, under one lock: the record is what holds the allocation, so an
	// allocation nothing has recorded yet would be given to the next task as free
	// while this one was about to bind it.
	record, err := s.reserveAndRecord(
		ctx, task, spec, runtimeNeeds(cfg, documents), cfg.Runtime.Ports(),
		cfg.Runtime.BindAddress, action)
	if err != nil {
		return nil, nil, err
	}
	spec = recordedInputs(spec, record, cfg.Runtime.BindAddress)

	// After the record's own inputs are back in place, so that what is recorded
	// about each service is read from the specification the documents are
	// generated from rather than from one nothing will be written with.
	if err := s.recordProvenance(ctx, task, record, runtimeProvenance(cfg, spec)); err != nil {
		return nil, nil, err
	}

	services, err := s.runtimes(spec)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", api.ErrInvalid, err)
	}
	return services, record, nil
}

// recordProvenance records where each managed service's code comes from.
//
// It is written before the adapter exists, so a create that is interrupted after
// its first Compose command still leaves a task saying which of its services
// were going to run its work — the ordering rule ADR-029 set for worktrees,
// applied to a state resolved rather than observed.
func (s *service) recordProvenance(
	ctx context.Context, task *domain.Task, record *domain.RuntimeEnvironment,
	provenance []domain.ServiceProvenance,
) error {
	if !record.ResolveProvenance(provenance, s.now()) {
		return nil
	}
	return s.store.Tasks().Save(ctx, task)
}

// recorded applies the inputs a task's runtime already owns to a specification
// freshly resolved from configuration.
//
// What the task owns wins over what configuration says today: an edited project
// file must not point a stop or a destroy at a different Compose project than
// the one that was started.
//
// The mounts and the build contexts are deliberately not among those inputs.
// They are written into the generated override every time that document is
// written, so they follow the configuration in force rather than the
// configuration a runtime was created under — a user who corrects a container
// path should get the corrected mount on their next start. What they may not do
// is name a service this task does not manage, which is what a project file that
// has gained a service since the runtime was created would produce: those are
// dropped here, because refusing to stop or destroy an existing runtime over a
// service it never had is a worse answer than leaving the new service alone
// until the runtime is absent and its inputs are resolved again.
//
// The host ports are on the other side of that line, with the identity and the
// file list. A published port is bound by a running container, so re-resolving
// one from edited configuration would move a task's address out from under the
// containers holding it — and it is what other tasks are kept away from, which
// only works while it is the recorded value.
func recordedInputs(spec runtime.Spec, record *domain.RuntimeEnvironment, bind string) runtime.Spec {
	spec.Identity = record.Identity
	spec.Includes = runtimeIncludesOf(record.Composition)
	spec.IncludePath = record.GeneratedIncludePath
	spec.StaticOverrides = record.StaticOverrides
	spec.OverridePath = record.GeneratedOverridePath
	spec.EnvFiles = record.EnvFiles
	spec.Services = record.Services

	managed := make(map[string]bool, len(spec.Services))
	for _, service := range spec.Services {
		managed[service] = true
	}

	mounts := make([]runtime.Mount, 0, len(spec.Mounts))
	for _, mount := range spec.Mounts {
		services := make([]string, 0, len(mount.Services))
		for _, service := range mount.Services {
			if managed[service] {
				services = append(services, service)
			}
		}
		if len(services) == 0 {
			continue
		}
		mount.Services = services
		mounts = append(mounts, mount)
	}
	spec.Mounts = mounts

	builds := make([]runtime.Build, 0, len(spec.Builds))
	for _, build := range spec.Builds {
		if managed[build.Service] {
			builds = append(builds, build)
		}
	}
	spec.Builds = builds

	// Last, so that the publications and the addresses they generate are
	// resolved against the services this runtime actually manages rather than
	// against the ones configuration names today.
	return withAllocations(spec, record.Allocations, bind)
}

// recordRuntimeInputs attaches or refreshes the task's runtime record.
//
// A task with no runtime gets one, absent and observing nothing. A task whose
// runtime is absent — never created, or destroyed since — has its inputs
// re-resolved from the project's current configuration. A task with resources
// keeps the inputs those resources were created from.
func (s *service) recordRuntimeInputs(
	ctx context.Context, task *domain.Task, spec runtime.Spec,
	allocations []domain.PortAllocation, action api.RuntimeAction,
) (*domain.RuntimeEnvironment, error) {
	inputs := domain.RuntimeInputs{
		Provider:              runtimeProvider,
		Identity:              spec.Identity,
		Composition:           runtimeComposition(spec.Includes),
		GeneratedIncludePath:  spec.IncludePath,
		StaticOverrides:       spec.StaticOverrides,
		GeneratedOverridePath: spec.OverridePath,
		EnvFiles:              spec.EnvFiles,
		Services:              spec.Services,
		Allocations:           allocations,
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
	// A runtime with nothing in it holds no host port. It is the release half of
	// the allocation: a destroy gives its ports back, and so does a runtime that
	// was removed by other means, because what makes an allocation worth keeping
	// is a container bound to it.
	record.ReleasePorts(s.now())

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
		if from != record.State {
			// Only a state that changed, and only one worth interrupting for. A
			// stop is deliberately not one: v0 stops services when a user asks, so
			// a stop is something they just did rather than news (FR-RUN-005).
			if condition, ok := notify.ForRuntime(record.State); ok {
				s.notifyTask(ctx, task, condition, 0)
			}
		}
	}
	for _, note := range notes {
		s.logger.WarnContext(ctx, "a task's application services will not show all of its work",
			slog.String("task", task.ID.String()), slog.String("note", note))
	}
	return api.RuntimeResult{Task: task, Services: serviceStates(state), Notes: notes}, nil
}

// provenanceNotes say which managed services will not show the task's work.
//
// Two different things go wrong. A service that receives neither a worktree nor
// a build context runs the user's ordinary checkout, and nothing about it will
// ever change with the task; each of those is named on its own, because each is
// a different repository's container path to fix. A service that builds the code
// into its image runs the task's work and goes on running the copy it was built
// from, which matters because the agent that changed the code is confined to a
// devcontainer with no Docker and can rebuild nothing (ADR-065 evidence 9);
// those are named together, because they all need the same one command.
//
// A service that mounts a worktree *and* builds from it is still named. Whether
// the mount makes the running code current depends on whether the image reads
// the path it is mounted at, which Feat cannot know: an application server
// reloading from its mounted source is current, and a web server serving the
// files its build produced is not — and the reference project has one of each
// (ADR-065 evidence 15). Saying that a change needs a rebuild, and that a mount
// is current wherever the image reads it, is true of both; suppressing it
// whenever a mount exists was true of only one.
func provenanceNotes(task *domain.Task, record *domain.RuntimeEnvironment) []string {
	var notes []string
	var built []string
	mounted := false

	for _, entry := range record.Provenance {
		if !entry.RunsTaskCode() {
			notes = append(notes, fmt.Sprintf(
				"the %s service runs neither a task worktree nor a build context inside one, so it runs "+
					"whatever the project's own Compose files give it — the ordinary checkout rather than "+
					"this task's work. Set %s to the path those files mount that repository at, or point "+
					"the service's build context at it",
				entry.Service, containerPathOf(entry.Repositories)))
			continue
		}
		if len(entry.Built) > 0 {
			built = append(built, entry.Service)
			mounted = mounted || len(entry.Mounted) > 0
		}
	}

	if len(built) > 0 {
		note := fmt.Sprintf(
			"%s build this task's code into their images, so a change the task makes appears in them "+
				"only once the images are built again: `feat runtime create %s` rebuilds them",
			names(built), task.Key())
		if mounted {
			note += ". A worktree one of them also mounts is current the moment it is written, wherever " +
				"the image reads it"
		}
		notes = append(notes, note)
	}
	return notes
}

// containerPathOf names the configuration field a service's own repositories
// would answer with.
func containerPathOf(repositories []string) string {
	if len(repositories) == 0 {
		return "the runtime container path of the repository whose code it runs"
	}
	fields := make([]string, 0, len(repositories))
	for _, repository := range repositories {
		fields = append(fields, "repositories."+repository+".runtime.container_path")
	}
	return names(fields)
}

// names joins values the way a sentence would.
func names(values []string) string {
	switch len(values) {
	case 0:
		return ""
	case 1:
		return values[0]
	default:
		return strings.Join(values[:len(values)-1], ", ") + " and " + values[len(values)-1]
	}
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
func (s *service) runtimeSpec(
	cfg *config.Config, task *domain.Task, documents composeDocuments,
) (runtime.Spec, error) {
	section := cfg.Runtime
	if section == nil {
		return runtime.Spec{}, fmt.Errorf("project %s configures no application runtime", task.ProjectID)
	}
	includes := runtimeIncludes(cfg)
	if len(includes) == 0 {
		return runtime.Spec{}, fmt.Errorf(
			"project %s configures an application runtime that no repository brings Compose files to",
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
	directory, err := s.runtimeDirectory(task)
	if err != nil {
		return runtime.Spec{}, err
	}

	spec := runtime.Spec{
		Project:         task.ProjectID,
		Task:            task.ID,
		Identity:        identity,
		Includes:        includes,
		IncludePath:     filepath.Join(directory, runtimeIncludeName),
		StaticOverrides: append([]string(nil), section.StaticOverrides...),
		// Feat's own directory, holding the documents it generates. Every path
		// in them is absolute and each include entry names the directory its own
		// repository's relative paths resolve against, so no repository's file is
		// ever read against another repository's directory (ADR-065 evidence 2).
		Directory:    directory,
		OverridePath: filepath.Join(directory, runtimeOverrideName),
		EnvFiles:     append([]string(nil), section.EnvFiles...),
		Services:     cfg.RuntimeServices(),
		Mounts:       runtimeMounts(cfg, task),
		Builds:       runtimeBuilds(cfg, task, documents),
		Variables: map[string]string{
			varProject:  task.ProjectID.String(),
			varTask:     task.ID.String(),
			varTaskKey:  task.Key().String(),
			varIdentity: identity,
		},
		ForbiddenSources: checkouts(cfg, task),
	}

	if err := spec.Validate(); err != nil {
		return runtime.Spec{}, err
	}
	return spec, nil
}

// runtimeIncludes is what the project's application is composed of.
//
// One entry per repository that brings Compose files, each carrying that
// repository's own checkout as its project directory. A repository that
// contributes a container path and no files is not an include: it has nothing
// to join, and its worktree still reaches the services through the mounts.
func runtimeIncludes(cfg *config.Config) []runtime.Include {
	var includes []runtime.Include
	for _, contribution := range cfg.RuntimeComposition() {
		if len(contribution.ComposeFiles) == 0 {
			continue
		}
		includes = append(includes, runtime.Include{
			Repository: contribution.RepositoryID,
			Directory:  contribution.Directory,
			Files:      contribution.ComposeFiles,
		})
	}
	return includes
}

// runtimeDirectory is where a task's generated Compose documents are written,
// and the Compose project directory of every command Feat runs for it.
//
// Both identifiers are validated before either reaches a path, so no stored
// value can name a directory outside the runtime root.
func (s *service) runtimeDirectory(task *domain.Task) (string, error) {
	if err := task.ProjectID.Validate(); err != nil {
		return "", err
	}
	if err := task.ID.Validate(); err != nil {
		return "", err
	}
	return filepath.Join(s.layout.RuntimeRoot(), task.ProjectID.String(), task.ID.String()), nil
}

// runtimeComposition maps a specification's includes onto the record's.
func runtimeComposition(includes []runtime.Include) []domain.RuntimeSource {
	sources := make([]domain.RuntimeSource, 0, len(includes))
	for _, include := range includes {
		sources = append(sources, domain.RuntimeSource{
			Repository: include.Repository,
			Directory:  include.Directory,
			Files:      append([]string(nil), include.Files...),
		})
	}
	return sources
}

// runtimeIncludesOf maps a record's composition back onto a specification's.
func runtimeIncludesOf(sources []domain.RuntimeSource) []runtime.Include {
	includes := make([]runtime.Include, 0, len(sources))
	for _, source := range sources {
		includes = append(includes, runtime.Include{
			Repository: source.Repository,
			Directory:  source.Directory,
			Files:      append([]string(nil), source.Files...),
		})
	}
	return includes
}

// runtimeMounts is the code the task's services run.
//
// Each selected repository's task worktree, at the container path its repository
// configures, with the access the task has. Compose merges by target, so this
// replaces whatever the project's own files mounted there — and a container_path
// that disagrees with those files adds a mount instead, which the adapter
// reports after the services are up (ADR-034).
//
// The path is the repository's own runtime container path, which is a different
// field from the one the agent's container uses and answers a different
// question: where an application's services expect their source is a fact about
// that application's Compose files, and where the agent's devcontainer mounts a
// worktree is the user's free choice (ADR-065 evidence 5). Reading the agent's
// answer here is what left a host-execution project mounting nothing at all,
// because that field carries a value only when there is a devcontainer.
//
// A repository with no runtime container path is skipped rather than guessed at.
// A project whose services bake that repository's code is a valid project and
// wants no mount at all: runtimeBuilds is where its code comes from. What such a
// repository leaves behind is a service this resolution cannot reach, which the
// task records against that service rather than losing (ADR-065 evidence 1).
func runtimeMounts(cfg *config.Config, task *domain.Task) []runtime.Mount {
	var mounts []runtime.Mount

	for _, binding := range task.Repositories {
		repository, known := cfg.Repositories[binding.RepositoryID.String()]
		if !known || repository.Runtime == nil {
			continue
		}
		if repository.Runtime.ContainerPath == "" || binding.WorktreePath == "" {
			continue
		}
		if len(repository.Runtime.Services) == 0 {
			// Configuration refuses this, so reaching it means the two rule sets
			// disagree. Skipping is the safe half of the disagreement: a mount
			// belonging to no service would fail the specification's own check
			// with a message about a task rather than about a file.
			continue
		}
		access := "read-write"
		if binding.Access == domain.TaskAccessReadOnly {
			access = "read-only"
		}
		mounts = append(mounts, runtime.Mount{
			Services:    append([]string(nil), repository.Runtime.Services...),
			Repository:  binding.RepositoryID.String(),
			Source:      binding.WorktreePath,
			Target:      repository.Runtime.ContainerPath,
			ReadOnly:    binding.Access == domain.TaskAccessReadOnly,
			Description: "the " + binding.RepositoryID.String() + " task worktree, " + access,
		})
	}
	return mounts
}

// runtimeBuilds is the code the task's services bake into their images.
//
// A mount is not the only way a repository's code reaches a service. A service
// whose image copies the repository in has no mount to replace, so the container
// path decides nothing about it and only its build context does: without this,
// such a service runs the user's ordinary checkout whatever the configuration
// says, and ADR-034's post-start inspection cannot report it because the note
// looks at mounts and there is no mount (ADR-065 evidence 4).
//
// The build contexts are read out of the project's own Compose files, which is
// the one place they are stated. The reading is structural and takes the context
// and nothing else: no `environment` value, no `build.args` entry, and no
// `env_file` is opened, and a context containing a "${...}" is left unread
// rather than interpolated. `docker compose config` would answer the same
// question by rendering the whole project including the values of its
// environment files, which Feat must never read (ADR-034 evidence 5) — reading
// the document resolves nothing and stays allowed (ADR-065).
//
// A context is redirected when it lies in the checkout of a repository this task
// selected, at the same place inside that repository's worktree. A context
// somewhere else is left alone: it is not this task's code, and pointing it at a
// worktree would be Feat deciding what a project builds.
func runtimeBuilds(cfg *config.Config, task *domain.Task, documents composeDocuments) []runtime.Build {
	worktrees := taskWorktrees(cfg, task)
	if len(worktrees) == 0 {
		return nil
	}
	managed := make(map[string]bool)
	for _, service := range cfg.RuntimeServices() {
		managed[service] = true
	}

	// Later contributions win, which is the order Compose merges the include
	// document in: a service two repositories both define builds from the
	// context of the file that was read last.
	contexts := make(map[string]runtime.Build)
	var ordered []string
	for _, contribution := range cfg.RuntimeComposition() {
		for _, service := range documents[contribution.RepositoryID].Services {
			if !managed[service.Name] || service.BuildContext == "" {
				continue
			}
			build, redirected := redirectBuild(service.Name, service.BuildContext, worktrees)
			if !redirected {
				continue
			}
			if _, seen := contexts[service.Name]; !seen {
				ordered = append(ordered, service.Name)
			}
			contexts[service.Name] = build
		}
	}

	builds := make([]runtime.Build, 0, len(ordered))
	for _, service := range ordered {
		builds = append(builds, contexts[service])
	}
	return builds
}

// composeDocuments is what each contributing repository's own Compose files
// say about themselves, keyed by repository.
//
// It is read once per runtime action and passed to everything that needs it,
// because two questions are answered from it — where a service's image is built
// from, and which ports it publishes — and reading the same files twice for one
// action would be two answers that could disagree with each other.
type composeDocuments map[string]project.Composition

// readComposition reads every contributing repository's Compose files.
//
// Structurally, against each repository's own checkout, which is the project
// directory its include entry carries: a relative path in those files means
// what it would mean to a user standing in that repository. It resolves no
// interpolation and opens no environment file (ADR-065).
func readComposition(cfg *config.Config) composeDocuments {
	documents := make(composeDocuments)
	for _, contribution := range cfg.RuntimeComposition() {
		if len(contribution.ComposeFiles) == 0 {
			continue
		}
		documents[contribution.RepositoryID] = project.ComposeComposition(
			contribution.Directory, contribution.ComposeFiles...)
	}
	return documents
}

// worktree is one repository this task holds, with the checkout its Compose
// files' paths were written against.
type worktree struct {
	repository string
	checkout   string
	path       string
}

// taskWorktrees are the repositories the task selected that have both a checkout
// and a worktree, in the order the task binds them.
func taskWorktrees(cfg *config.Config, task *domain.Task) []worktree {
	var held []worktree
	for _, binding := range task.Repositories {
		repository, known := cfg.Repositories[binding.RepositoryID.String()]
		if !known || repository.HostPath == "" || binding.WorktreePath == "" {
			continue
		}
		held = append(held, worktree{
			repository: binding.RepositoryID.String(),
			checkout:   filepath.Clean(repository.HostPath),
			path:       binding.WorktreePath,
		})
	}
	return held
}

// redirectBuild points one build context at the task's own copy of it.
//
// The deepest matching checkout wins, so a project holding one repository inside
// another redirects a context at the repository it is really in rather than at
// the one that happens to contain both.
func redirectBuild(service, context string, worktrees []worktree) (runtime.Build, bool) {
	context = filepath.Clean(context)

	var found worktree
	for _, candidate := range worktrees {
		if !within(candidate.checkout, context) {
			continue
		}
		if found.checkout == "" || len(candidate.checkout) > len(found.checkout) {
			found = candidate
		}
	}
	if found.checkout == "" {
		return runtime.Build{}, false
	}

	relative, err := filepath.Rel(found.checkout, context)
	if err != nil {
		return runtime.Build{}, false
	}
	where := "the " + found.repository + " task worktree"
	if relative != "." {
		// A repository holding several applications names the one this service
		// builds, so the generated document says which directory it is.
		where += ", " + filepath.ToSlash(relative)
	}
	return runtime.Build{
		Service:     service,
		Repository:  found.repository,
		Context:     filepath.Join(found.path, relative),
		Description: where + ", which this service's image is built from",
	}, true
}

// within reports whether a path is a directory or is inside it.
func within(outer, inner string) bool {
	return inner == outer || strings.HasPrefix(inner, outer+string(filepath.Separator))
}

// runtimeProvenance says where each managed service's code comes from.
//
// It is resolved from the specification the generated documents are written
// from, so what the task records and what Compose is given cannot disagree, and
// it is resolved before anything is created rather than inspected out of the
// containers afterwards (ADR-065).
func runtimeProvenance(cfg *config.Config, spec runtime.Spec) []domain.ServiceProvenance {
	owners := make(map[string][]string)
	for _, contribution := range cfg.RuntimeComposition() {
		for _, service := range contribution.Services {
			owners[service] = append(owners[service], contribution.RepositoryID)
		}
	}

	provenance := make([]domain.ServiceProvenance, 0, len(spec.Services))
	for _, service := range spec.Services {
		entry := domain.ServiceProvenance{
			Service:      service,
			Repositories: append([]string(nil), owners[service]...),
		}
		for _, mount := range spec.MountsFor(service) {
			if mount.Repository != "" {
				entry.Mounted = append(entry.Mounted, mount.Repository)
			}
		}
		if build, redirected := spec.BuildFor(service); redirected {
			entry.Built = append(entry.Built, build.Repository)
		}
		provenance = append(provenance, entry)
	}
	return provenance
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
		if _, err := s.observeRuntime(ctx, task); err != nil && !errors.Is(err, ErrRuntimeUnconfigured) {
			s.logger.WarnContext(ctx, "observing a task's application services",
				slog.String("task", task.ID.String()), slog.Any("error", err))
		}
	}
}

// ErrRuntimeUnconfigured reports that a task's services exist while its project
// no longer configures a runtime at all.
//
// It is a distinct error because it is not a failure: the task's Compose project
// is still there and is still the task's, and what has gone is the configuration
// that would say how to address it. Reconciliation reports it as an orphan,
// which is what the note in the observer promised the slice that owns recovery
// would do.
var ErrRuntimeUnconfigured = errors.New("the project no longer configures an application runtime")

// observeRuntime reads one task's services, records a change, and returns what
// it saw.
//
// The read-change-write cycle runs under the task's own lock, because a poll
// that started from a copy loaded outside it would overwrite whatever a request
// wrote in between — the defect ADR-036 evidence 9 records, in the one place
// that reaches a task's records on a timer.
//
// The lock is taken after Docker has answered rather than before it, because a
// create or a start holds it for as long as its images take to build, and a
// poller waiting behind one would leave every other task's runtime state
// unobserved for minutes. What that costs is an answer that may have been
// overtaken while it was being given, which stillCurrent is what refuses.
func (s *service) observeRuntime(ctx context.Context, task *domain.Task) (domain.RuntimeState, error) {
	cfg, err := config.Load(s.layout.ProjectConfigDir(), task.ProjectID.String(), s.configOptions())
	if err != nil {
		return "", err
	}
	if !cfg.HasRuntime() {
		return "", ErrRuntimeUnconfigured
	}

	spec, err := s.runtimeSpec(cfg, task, readComposition(cfg))
	if err != nil {
		return "", err
	}
	services, err := s.runtimes(recordedInputs(spec, task.Runtime, cfg.Runtime.BindAddress))
	if err != nil {
		return "", err
	}
	state, err := services.Observe(ctx)
	if err != nil {
		return "", err
	}

	defer s.locks.lock(task.ID)()
	current, err := s.store.Tasks().Load(ctx, store.Ref(task))
	if err != nil {
		return state.Lifecycle, err
	}
	if current.Runtime == nil {
		return state.Lifecycle, nil
	}
	if !stillCurrent(task.Runtime, current.Runtime) {
		// Somebody acted on this task while the question was being asked, so this
		// answer is about a moment that has passed. The next poll asks again
		// against what exists now.
		return state.Lifecycle, nil
	}
	if state.Lifecycle == current.Runtime.State && state.Health == current.Runtime.Health {
		// Nothing changed. Saving would rewrite the snapshot and publishing would
		// make every dashboard re-read, several times a minute, for ever.
		return state.Lifecycle, nil
	}
	_, err = s.recordRuntime(ctx, current, current.Runtime, state, nil, "an observation")
	return state.Lifecycle, err
}

// stillCurrent reports whether a runtime record is still the one an observation
// was taken against.
//
// The poller lists the tasks outside any lock and asks Docker about each of
// them, which takes long enough for a create or a start to finish in between —
// and what it then holds is an answer about the world as it was before that
// action. Writing it down puts the task back to what it was, and the state
// alone would survive that, because the next poll corrects it. The allocated
// host ports would not: a runtime recorded as absent releases them, so a stale
// observation applied after a create gave a task's ports away while its
// containers were bound to them.
//
// Found by running three tasks of the reference project (ADR-065 evidence 16).
//
// It asks how many times the record has been written rather than whether it
// still looks the same, because a record can be changed back into its own
// shape. A destroy and the create after it leave the identity, the state, the
// health and even the port numbers as they were — the allocator releases 21000
// and then hands back the lowest free port, which is 21000 — while the
// containers holding them are new ones. Comparing the shape passes that pair
// and releases live ports; comparing how often the record has been written
// cannot (G3-05).
func stillCurrent(before, current *domain.RuntimeEnvironment) bool {
	return before != nil && current != nil && before.Generation == current.Generation
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
