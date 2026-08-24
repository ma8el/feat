package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ma8el/feat/internal/agent"
	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/config"
	"github.com/ma8el/feat/internal/control"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/execution/compose"
	"github.com/ma8el/feat/internal/forge"
	"github.com/ma8el/feat/internal/git"
	"github.com/ma8el/feat/internal/notify"
	"github.com/ma8el/feat/internal/paths"
	"github.com/ma8el/feat/internal/project"
	"github.com/ma8el/feat/internal/reconcile"
	"github.com/ma8el/feat/internal/resources"
	"github.com/ma8el/feat/internal/review"
	"github.com/ma8el/feat/internal/runtime"
	"github.com/ma8el/feat/internal/store"
	"github.com/ma8el/feat/internal/tmux"
)

// Build identifies the running binary. It is passed in rather than read from the
// version package, so that a test can pin what health reports.
type Build struct {
	Version   string
	Commit    string
	GoVersion string
	Platform  string
}

// service answers the local API from persistent state.
//
// It is the only component that holds the store: the daemon is the sole reader
// and writer of persistent state (ADR-008), and confining it to one type is what
// makes that checkable rather than aspirational.
type service struct {
	store store.Store
	bus   *Bus
	// Adapters operate on host resources; the service supplies resolved inputs
	// and remains the only persistent-state writer.
	git       *git.Git
	terminals *tmux.Tmux
	agent     agent.Adapter
	// runner executes probe commands for a host-native agent. A devcontainer
	// task probes inside its own container instead, through the execution
	// environment the launch built.
	runner agent.Runner
	// checks runs a completion gate's host checks. A nil value runs them as
	// processes on this host.
	checks review.Runner
	// forges open merge requests, one adapter per forge a repository can
	// declare. Every credentialed provider call is made through one of them, on
	// this host, with the authentication the user already has: the agent
	// environment receives no provider token (ADR-070).
	forges map[domain.ForgeKind]forge.Adapter
	// docker runs the container commands a devcontainer task needs. A nil value
	// drives the real Docker CLI.
	docker compose.Runner
	// runtimeDocker runs the container commands a task's application services
	// need. It is separate from docker because the two adapters are separate
	// (ADR-034), which also lets a test arrange a Docker that refuses to start an
	// application while the agent's own container is perfectly healthy.
	runtimeDocker runtime.Runner
	layout        paths.Layout
	// env is the environment configuration is resolved against: the user who
	// owns the daemon, not whoever sent the request.
	env paths.Environment
	// hostAgent reports that the daemon was started with the opt-in that
	// launches an agent on this host even where the project configures a
	// container. It is read from the daemon's own environment and never from a
	// request: a caller that could move the agent outside its configured
	// boundary would be granting itself a capability (ADR-032).
	hostAgent bool
	build     Build
	endpoint  Endpoint
	now       func() time.Time
	logger    *slog.Logger

	// notifier delivers desktop notifications. It never fails a task.
	notifier notify.Notifier
	// undeliverable is why this build delivers no desktop notification, asked
	// once at startup and empty when it can. It is held so that a notification
	// this platform cannot show is dropped saying so, rather than handed to a
	// notifier that refuses it and logged as a failed delivery.
	undeliverable string
	// notifiable reports that the daemon has finished catching up on what
	// happened while it was stopped. Until it has, changes are recorded without
	// interrupting anybody: a restart that notified for every turn that ended
	// overnight would be reporting the past (ADR-035).
	notifiable atomic.Bool
	// observer samples the machine and the tasks. Sampling is observational and
	// never blocks a request.
	observer *resources.Observer
	// sample is the most recent sample, which the local API serves. It is
	// deliberately not persisted: derived samples are not part of stored state.
	sampleMu sync.Mutex
	sample   resources.Sample
	sampled  bool
	// resourceOverride replaces the projects' configured sampling interval. Only
	// a test sets it, so that a sample can be forced without waiting for one.
	resourceOverride time.Duration
	// runtimeOverride replaces api.RuntimeTimeout as the budget for one manual
	// runtime action. Only a test sets it, so that what a daemon does when a
	// Compose command outlasts its budget can be observed without a test that
	// takes as long as the budget.
	runtimeOverride time.Duration
	// agentOverride replaces api.AgentTimeout as the budget for one launch,
	// resume, or stop. Only a test sets it, for the reason above.
	agentOverride time.Duration

	// idle holds the pending end-of-turn transitions, one per task.
	idle *idleTimers
	// idleNotice holds the pending "has been idle long enough to mention"
	// notifications, one per task. It is separate from idle because the two
	// measure different periods from different moments (ADR-035).
	idleNotice *idleTimers
	// startup holds the pending "has not reported starting" notices, one per
	// task. It is separate from idle because the two mean different things and
	// a task can never be waiting on both.
	startup *idleTimers
	// gate records which tasks have a completion gate running, so that two
	// review requests in quick succession do not run a project's test suite
	// twice at once.
	gate *gates
	// locks serialise the read-modify-write cycles of one task's records, which
	// the background gate made concurrent with everything else (ADR-036).
	locks *taskLocks
	// portMu serialises host port allocation across every task. It is the
	// daemon's rather than a task's on purpose: choosing a free port means
	// reading what every other task holds, and two tasks created at the same
	// moment would otherwise read the same port as free and both write it down.
	// It is held across the record as well as the choice, because a choice
	// nothing has saved is one the next task's read cannot see.
	portMu sync.Mutex
	// reserving runs between a task's ports being chosen and being recorded,
	// which is the window portMu exists to close and the only moment the two
	// halves of an allocation can be observed apart. Only a test sets it: every
	// Docker command a fake can hold up runs after both halves, so a test
	// without this seam cannot enter the window at all — which is how the
	// acceptance criterion came to be checked by two sequential launches.
	reserving func(domain.TaskID)
	// handovers records the terminals a client has been sent to attach to, so
	// that a rendering does not pin a window out from under a client that is
	// still on its way to it. It is separate from locks because a frame must not
	// wait behind a launch or a completion gate: those hold a task's lock for as
	// long as their work takes, and the terminal view is what a user watches
	// while they run.
	handovers *handovers
	// report is the most recent reconciliation pass, which the local API serves
	// and the dashboard shows. It is deliberately not persisted: it describes
	// what was observed at one moment, and a stored copy would be read later as
	// though it still were (ADR-037).
	reportMu sync.Mutex
	report   *reconcile.Report
	// startedRecord is the durable daemon record this run wrote when it claimed
	// the state directory. A clean shutdown rewrites it; a crash leaves it
	// saying the run never ended, which is what makes the flag mean anything.
	startedRecord *domain.DaemonRecord
	// previousRun is what the last daemon to own this state directory left
	// behind, read once when this run claimed it. It is held rather than
	// re-read, because claiming replaced the record on disk with this run's.
	previousRun *domain.DaemonRecord
	// workspaces caches one control workspace per task, because each holds the
	// record of what it has already applied.
	workspaceMu sync.Mutex
	workspaces  map[domain.TaskID]*control.Workspace
	// repeats keeps the polling loops from writing the same failure to the log
	// on every tick. A persistent failure is reported when it appears and when
	// it changes, rather than four times a second for as long as it lasts.
	repeats *repeats
	// pollNow asks the control poller to read immediately.
	pollNow chan struct{}
}

var _ api.Service = (*service)(nil)

// Health reports what the daemon knows about itself.
//
// Unreadable state produces a degraded report rather than a failed request. A
// client asking whether the daemon is alive has learned more from "running, but
// the state directory cannot be listed" than from a 500.
func (s *service) Health(ctx context.Context) (api.HealthReport, error) {
	report := api.HealthReport{
		Daemon: api.Daemon{
			Version:   s.build.Version,
			Commit:    s.build.Commit,
			GoVersion: s.build.GoVersion,
			Platform:  s.build.Platform,
			PID:       s.endpoint.PID,
			StartedAt: s.endpoint.StartedAt,
			Socket:    s.endpoint.Socket,
			HostAgent: s.hostAgent,
		},
		State: api.State{Directory: s.layout.State},
	}

	projects, err := s.store.Projects().List(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "listing projects for health", slog.Any("error", err))
		report.Degraded = "the state directory could not be listed: " + err.Error()
		return report, nil
	}
	report.State.Projects = len(projects)
	return report, nil
}

// Projects returns every registered project.
func (s *service) Projects(ctx context.Context) ([]*domain.Project, error) {
	projects, err := s.store.Projects().List(ctx)
	if err != nil {
		return nil, err
	}
	return projects, nil
}

// Project returns one project.
func (s *service) Project(ctx context.Context, id domain.ProjectID) (*domain.Project, error) {
	project, err := s.store.Projects().Load(ctx, id)
	if err != nil {
		return nil, translate(err, "no project "+id.String()+" is registered")
	}
	return project, nil
}

// RegisterProject reads a project's configuration and records it.
//
// It is the first write the local API carries. The daemon does the reading as
// well as the writing: the request names a project, and the file is the one at
// the documented location under the configuration directory this daemon
// resolved, so a client cannot point the daemon at a file of its choosing.
//
// Registration is idempotent. Re-registering picks up an edited configuration
// and keeps the original registration time, because a user who edits their YAML
// expects the record to follow it. Tasks that are already running are
// unaffected: their configuration was resolved into a launch snapshot when they
// were launched (docs/07-configuration-model.md).
func (s *service) RegisterProject(ctx context.Context, id domain.ProjectID) (api.RegisteredProject, error) {
	cfg, err := config.Load(s.layout.ProjectConfigDir(), id.String(), s.configOptions())
	if err != nil {
		return api.RegisteredProject{}, translateConfig(err)
	}

	existing, err := s.store.Projects().Load(ctx, id)
	switch {
	case err == nil:
		updated, err := project.Update(existing, cfg, s.now())
		if err != nil {
			return api.RegisteredProject{}, fmt.Errorf("%w: %w", api.ErrInvalid, err)
		}
		if err := s.store.Projects().Save(ctx, updated); err != nil {
			return api.RegisteredProject{}, err
		}
		s.logger.InfoContext(ctx, "project updated",
			slog.String("project", id.String()), slog.String("configuration", cfg.Path()))
		return api.RegisteredProject{Project: updated, Created: false}, nil

	case errors.Is(err, store.ErrNotFound):
		registered, err := project.FromConfig(cfg, s.now())
		if err != nil {
			return api.RegisteredProject{}, fmt.Errorf("%w: %w", api.ErrInvalid, err)
		}
		if err := s.store.Projects().Save(ctx, registered); err != nil {
			return api.RegisteredProject{}, err
		}
		s.logger.InfoContext(ctx, "project registered",
			slog.String("project", id.String()), slog.String("configuration", cfg.Path()))
		return api.RegisteredProject{Project: registered, Created: true}, nil

	default:
		return api.RegisteredProject{}, err
	}
}

// configOptions returns the settings configuration is resolved against.
//
// The daemon's own environment supplies them, so that a resolved "~" is the
// user who owns the daemon rather than whoever asked.
func (s *service) configOptions() config.Options {
	return config.Options{Env: s.env, StateDir: s.layout.State}
}

// translateConfig maps a configuration failure onto the API vocabulary.
//
// A missing file and an invalid one are different states with different
// remedies: one is a file to write, and the other is a file to fix.
func translateConfig(err error) error {
	if errors.Is(err, config.ErrNotFound) {
		return fmt.Errorf("%w: %w", api.ErrNotFound, err)
	}
	var invalid *config.Error
	if errors.As(err, &invalid) {
		return fmt.Errorf("%w: %w", api.ErrInvalid, invalid)
	}
	return err
}

// Tasks returns every task of every project, in project order.
func (s *service) Tasks(ctx context.Context) ([]*domain.Task, error) {
	projects, err := s.store.Projects().List(ctx)
	if err != nil {
		return nil, err
	}

	var tasks []*domain.Task
	for _, project := range projects {
		owned, err := s.store.Tasks().List(ctx, project.ID)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, owned...)
	}
	return tasks, nil
}

// Task returns one task addressed by task identifier alone.
//
// Storage addresses a task by project and task together, because a caller that
// holds only a task identifier has lost the relationship rather than found a
// shortcut (ADR-026). The command surface, though, addresses a task by itself:
// the user types `feat attach <task>`. Resolving one to the other is the
// daemon's job, and this is where it happens (ADR-027).
func (s *service) Task(ctx context.Context, id domain.TaskID) (*domain.Task, error) {
	projects, err := s.store.Projects().List(ctx)
	if err != nil {
		return nil, err
	}

	for _, project := range projects {
		task, err := s.store.Tasks().Load(ctx, store.TaskRef{Project: project.ID, Task: id})
		if err == nil {
			return task, nil
		}
		if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("%w: no task %s in any registered project", api.ErrNotFound, id)
}

// ResolveTask turns what a user typed into the task it names.
//
// It is the other half of the addressing rule Task carries. ADR-027 decided that
// the local API addresses a task by task identifier and the daemon resolves the
// owning project; this decides which identifier a user had to be able to see for
// that to be usable. Lists show the eight-character key, so the key names a task,
// and so does any prefix of an identifier — including the whole thing, which the
// caller has already tried before asking.
//
// Both failures name where a valid value comes from. Explaining the format of an
// identifier to somebody who cannot see one is what the previous rejection did.
func (s *service) ResolveTask(ctx context.Context, ref domain.TaskRef) (domain.TaskID, error) {
	tasks, err := s.Tasks(ctx)
	if err != nil {
		return "", err
	}

	task, found, err := domain.ResolveTask(ref, tasks)
	switch {
	case err != nil:
		var ambiguous *domain.AmbiguousTaskError
		if errors.As(err, &ambiguous) {
			return "", fmt.Errorf("%w; name one of them, or the whole identifier", err)
		}
		return "", fmt.Errorf("%w; %s", err, listTasksHint)
	case !found:
		return "", fmt.Errorf("%w: no task matches %q; %s", api.ErrNotFound, ref, listTasksHint)
	}
	return task.ID, nil
}

// listTasksHint names where a task's own identifiers are printed. Both are: the
// list prints the key, and the detail prints the identifier the key abbreviates.
const listTasksHint = "`feat task list` shows every task Feat knows about, " +
	"and the dashboard's task detail shows the whole identifier"

// Subscribe returns the event stream for one client.
func (s *service) Subscribe(ctx context.Context) (<-chan api.Event, error) {
	return s.bus.Subscribe(ctx), nil
}

// Publish records a state change on the event stream.
//
// The delivery path's order and backpressure behaviour are tested through it
// (ADR-027).
func (s *service) Publish(event domain.Event) uint64 {
	return s.bus.Publish(api.TaskEventOf(event))
}

// translate maps a storage error onto the API vocabulary. Anything that is not
// a missing record keeps its own message, because a corrupt snapshot and an
// absent one need different actions from the user.
func translate(err error, missing string) error {
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("%w: %s", api.ErrNotFound, missing)
	}
	return err
}
