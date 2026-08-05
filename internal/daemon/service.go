package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/config"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/git"
	"github.com/ma8el/feat/internal/paths"
	"github.com/ma8el/feat/internal/project"
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
	layout    paths.Layout
	// env is the environment configuration is resolved against: the user who
	// owns the daemon, not whoever sent the request.
	env      paths.Environment
	build    Build
	endpoint Endpoint
	now      func() time.Time
	logger   *slog.Logger
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

// Subscribe returns the event stream for one client.
func (s *service) Subscribe(ctx context.Context) (<-chan api.Event, error) {
	return s.bus.Subscribe(ctx), nil
}

// Publish records a state change on the event stream.
//
// Slice 2 has no writer of persistent state, so nothing calls this in
// production yet: the endpoints that change the world arrive with the slices
// that can create something to change. The delivery path is real, and its order
// and backpressure behaviour are tested through it (ADR-027).
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
