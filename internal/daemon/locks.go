package daemon

import (
	"sync"

	"github.com/ma8el/feat/internal/domain"
)

// taskLocks serialises the read-modify-write cycles of one task's records. The
// daemon is the only writer of persistent state (ADR-008) and storage makes each
// write atomic, but neither makes a load-change-save cycle safe against another:
// two goroutines changing different parts of one task leave only one change.
//
// A completion gate finishing while the review request that started it was still
// observing the repositories left a task recorded as ready_for_review whose
// review held no checks (ADR-036). The lock is per task and held across a cycle
// rather than across an operation, so a gate releases it while its checks run.
type taskLocks struct {
	mu   sync.Mutex
	held map[domain.TaskID]*sync.Mutex
}

func newTaskLocks() *taskLocks { return &taskLocks{held: make(map[domain.TaskID]*sync.Mutex)} }

// lock takes one task's lock and returns the function that releases it. It
// returns the release rather than an unlock method, so a caller writes
// `defer s.locks.lock(id)()` and cannot release somebody else's.
func (l *taskLocks) lock(id domain.TaskID) func() {
	l.mu.Lock()
	entry, ok := l.held[id]
	if !ok {
		entry = &sync.Mutex{}
		l.held[id] = entry
	}
	l.mu.Unlock()

	entry.Lock()
	return entry.Unlock
}
