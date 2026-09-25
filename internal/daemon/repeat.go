package daemon

import "sync"

// repeats suppresses a log line that a polling loop would otherwise write on
// every tick. The control poller runs four times a second and the other pollers
// every few seconds, and what they report when something is wrong is almost
// never momentary, so the same line is true again at the next tick.
//
// Reporting a failure when it appears, and again when it changes, keeps the
// account complete while making the log's size a function of how many distinct
// things went wrong rather than of how long the daemon has been running.
type repeats struct {
	mu sync.Mutex
	// last is the message most recently reported for each subject. A subject is
	// absent when it is healthy, so a failure that comes back after a recovery
	// is reported again.
	last map[string]string
}

func newRepeats() *repeats { return &repeats{last: make(map[string]string)} }

// changed reports whether this subject's failure differs from the one last
// reported for it, and remembers it either way.
func (r *repeats) changed(subject, message string) bool {
	if r == nil {
		return true
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if previous, ok := r.last[subject]; ok && previous == message {
		return false
	}
	r.last[subject] = message
	return true
}

// clear forgets a subject, so its next failure is reported even when it repeats
// the one before. Callers clear on success.
func (r *repeats) clear(subject string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.last, subject)
}
