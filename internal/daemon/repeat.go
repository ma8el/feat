package daemon

import "sync"

// repeats suppresses a log line that a polling loop would otherwise write on
// every tick.
//
// The control poller runs four times a second, and the runtime and resource
// pollers every few seconds. What they report when something is wrong is almost
// never momentary — a task whose control workspace has gone, a container runtime
// that is not running — so the same line is true again at the next tick, and the
// loop writes it for as long as the daemon runs. That is how a log reaches a
// size a user notices: not by saying many things, but by saying one thing
// several times a second.
//
// Reporting a failure when it appears, and again when it changes, keeps the
// account complete while making its size a function of how many distinct things
// went wrong rather than of how long the daemon has been running.
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

// clear forgets a subject, so that its next failure is reported even when it is
// the one reported before. Callers clear on success, which is what makes a
// recurrence news rather than a repeat.
func (r *repeats) clear(subject string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.last, subject)
}
