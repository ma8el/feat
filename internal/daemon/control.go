package daemon

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/ma8el/feat/internal/control"
	"github.com/ma8el/feat/internal/domain"
)

// defaultPollInterval is how often the daemon reads the control outboxes.
//
// Polling rather than filesystem notification is ADR-032's decision: inotify
// events do not cross a bind mount reliably on every supported platform, and a
// watcher that worked on the host while silently never firing in a container
// would hide the failure in the configuration that matters most. The interval
// is short enough that a dashboard feels live and long enough that an idle
// machine is doing nothing measurable.
const defaultPollInterval = 250 * time.Millisecond

// controlWorkspace returns the control workspace of one task.
//
// Workspaces are cached because each one holds the record of which messages it
// has applied, and two instances of that record would each believe it was
// complete.
func (s *service) controlWorkspace(task *domain.Task) (*control.Workspace, error) {
	s.workspaceMu.Lock()
	defer s.workspaceMu.Unlock()

	if workspace, ok := s.workspaces[task.ID]; ok {
		return workspace, nil
	}
	workspace, err := control.Open(s.layout.ControlRoot(), task.ProjectID, task.ID, control.Options{Now: s.now})
	if err != nil {
		return nil, err
	}
	s.workspaces[task.ID] = workspace
	return workspace, nil
}

// pollControl reads every live task's control workspace once.
//
// A task whose delivery fails is logged and the others are still read: one
// task's damaged workspace must not stop every other task from reporting, which
// is the same rule slice 12 will state for reconciliation generally.
func (s *service) pollControl(ctx context.Context) {
	tasks, err := s.Tasks(ctx)
	if err != nil {
		s.logger.WarnContext(ctx, "listing tasks to read control messages", slog.Any("error", err))
		return
	}

	for _, task := range tasks {
		if !watching(task) {
			continue
		}
		if err := s.deliverControl(ctx, task); err != nil {
			s.logger.WarnContext(ctx, "reading a task's control workspace",
				slog.String("task", task.ID.String()), slog.Any("error", err))
		}
	}
}

// watching reports whether a task's control workspace is worth reading.
//
// A draft has no workspace, and an archived task has nobody left to report to.
// Everything in between may still produce a message: a failed task can be
// resumed, and a task in review can be sent back to work.
func watching(task *domain.Task) bool {
	switch task.Workflow {
	case domain.WorkflowDraft, domain.WorkflowArchived:
		return false
	default:
		return task.Session != nil
	}
}

// watchControl polls until the context ends.
func (s *service) watchControl(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = defaultPollInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.pollControl(ctx)
		case <-s.pollNow:
			s.pollControl(ctx)
		}
	}
}

// nudge asks the poller to read now rather than at the next tick.
//
// A launch is followed within milliseconds by a session-start event, and a user
// watching the dashboard should see the task reach working immediately rather
// than at whatever point in the polling interval they happened to launch.
func (s *service) nudge() {
	select {
	case s.pollNow <- struct{}{}:
	default:
		// A poll is already queued, which does the same job.
	}
}

// controlPoller owns the goroutine that reads control workspaces.
type controlPoller struct {
	once   sync.Once
	cancel context.CancelFunc
	done   chan struct{}
}

// start begins polling in the background.
func (p *controlPoller) start(ctx context.Context, service *service, interval time.Duration) {
	polling, cancel := context.WithCancel(ctx)
	p.cancel = cancel
	p.done = make(chan struct{})

	go func() {
		defer close(p.done)
		service.watchControl(polling, interval)
	}()
}

// stop ends polling and waits for it to finish.
func (p *controlPoller) stop() {
	p.once.Do(func() {
		if p.cancel == nil {
			return
		}
		p.cancel()
		<-p.done
	})
}
