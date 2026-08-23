package daemon

import (
	"sync"

	"github.com/ma8el/feat/internal/domain"
)

// taskLocks serialises the read-modify-write cycles of one task's records.
//
// The daemon is the only process that writes persistent state (ADR-008), and
// storage already makes each write atomic. Neither of those makes a
// load-change-save cycle safe against another one: two goroutines that both read
// a task, change different parts of it, and save produce a file holding one of
// the two changes, and the other is gone without a trace.
//
// That is not hypothetical. It was found by running review end to end: a
// completion gate finishing while the review request that started it was still
// observing the repositories left a task recorded as ready_for_review whose
// review held no checks at all — the state said the checks had passed and the
// record of what passed had been overwritten by a copy loaded a moment earlier
// (ADR-036).
//
// The lock is per task, because that is the unit two writers contend over, and
// it is held across a cycle rather than across an operation: a gate holds it to
// record what it found and releases it while the checks themselves run, which
// take minutes.
type taskLocks struct {
	mu   sync.Mutex
	held map[domain.TaskID]*sync.Mutex
}

func newTaskLocks() *taskLocks { return &taskLocks{held: make(map[domain.TaskID]*sync.Mutex)} }

// lock takes one task's lock and returns the function that releases it.
//
// It returns the release rather than taking an unlock method, so a caller writes
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
