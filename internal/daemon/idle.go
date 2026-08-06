package daemon

import (
	"sync"
	"time"

	"github.com/ma8el/feat/internal/domain"
)

// Timer schedules work for later.
//
// It is an interface for the same reason the daemon takes its clock as a
// function: a grace period is an acceptance criterion, and a test that proved
// it by sleeping would prove only that the machine was slow enough.
type Timer interface {
	// After runs the function once, after the duration has passed, and returns
	// a stop function. Stopping after the function has run does nothing.
	After(d time.Duration, run func()) (stop func())
}

// wallTimer schedules on the wall clock.
type wallTimer struct{}

// After schedules the function with the standard library.
func (wallTimer) After(d time.Duration, run func()) func() {
	timer := time.AfterFunc(d, run)
	return func() { timer.Stop() }
}

// idleTimers hold at most one pending idle transition per task.
//
// One per task rather than one overall, because two tasks end their turns
// independently and the second must not cancel the first.
type idleTimers struct {
	timer Timer

	mu      sync.Mutex
	pending map[domain.TaskID]func()
}

func newIdleTimers(timer Timer) *idleTimers {
	if timer == nil {
		timer = wallTimer{}
	}
	return &idleTimers{timer: timer, pending: make(map[domain.TaskID]func())}
}

// arm schedules the idle transition for one task, replacing any pending one.
func (t *idleTimers) arm(id domain.TaskID, after time.Duration, run func()) {
	t.mu.Lock()
	if stop, exists := t.pending[id]; exists {
		stop()
	}
	t.mu.Unlock()

	stop := t.timer.After(after, func() {
		t.mu.Lock()
		delete(t.pending, id)
		t.mu.Unlock()
		run()
	})

	t.mu.Lock()
	t.pending[id] = stop
	t.mu.Unlock()
}

// cancel drops a task's pending idle transition.
func (t *idleTimers) cancel(id domain.TaskID) {
	t.mu.Lock()
	stop, exists := t.pending[id]
	delete(t.pending, id)
	t.mu.Unlock()

	if exists {
		stop()
	}
}

// cancelAll drops every pending transition, which shutdown does so that a timer
// cannot fire into a daemon that has stopped.
func (t *idleTimers) cancelAll() {
	t.mu.Lock()
	pending := t.pending
	t.pending = make(map[domain.TaskID]func())
	t.mu.Unlock()

	for _, stop := range pending {
		stop()
	}
}
