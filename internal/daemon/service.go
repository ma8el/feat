package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/paths"
	"github.com/ma8el/feat/internal/store"
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
	store    store.Store
	bus      *Bus
	layout   paths.Layout
	build    Build
	endpoint Endpoint
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
