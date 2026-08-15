package daemon

import (
	"sync"
	"time"

	"github.com/ma8el/feat/internal/domain"
)

// handovers records the terminals a client has been sent to attach to, and
// serialises that against rendering them.
//
// A rendering sizes the window it draws and tmux then holds it at that size,
// which is what makes the pane wrap where the dashboard's main region wraps. A
// native client attaching to a window in that state gets a terminal the size of
// the main region and dots over the rest of their screen, so the size is
// released as the attach target is handed out (AttachInfo, OpenShell).
//
// Releasing it is not enough on its own. Between the release and the client
// reaching tmux there is a window of tens of milliseconds — a process has to be
// started, and the dashboard's own poll is running at up to sixteen frames a
// second — and a rendering in that window asks tmux who is attached, is told
// nobody, and pins the size again. The client then arrives to exactly the state
// the release existed to prevent, and stays in it: Bubble Tea blocks its event
// loop while the terminal is handed over, so the dashboard that would have
// noticed the client and released the size again is not polling at all.
//
// So a handed-out attach target is remembered, and a rendering treats the task
// as watched until the client either arrives or is judged not to be coming.
// Nothing here is persistent state: it is a fact about a client that exists for
// a few seconds, and a daemon that restarted has no attach in flight.
type handovers struct {
	// mu is held across a whole render as well as across recording a handover,
	// so that the two cannot interleave. Without that a poll can read "nobody is
	// attached", have the record and the release happen underneath it, and then
	// pin the window it was about to be told not to touch. It costs a client
	// asking to attach the length of one frame.
	mu     sync.Mutex
	handed map[domain.TaskID]time.Time
}

func newHandovers() *handovers { return &handovers{handed: make(map[domain.TaskID]time.Time)} }

// attachGrace is how long a client has to arrive before rendering takes the
// window back.
//
// It bounds a wrong guess in the only direction that matters. Too short and the
// defect returns for a slow attach; too long and a task whose attach failed
// draws at whatever size the window has until it expires, which the renderer
// already clips and which corrects itself on the next frame after that. It is
// not configurable, for the reason startupGrace is not: any value comfortably
// longer than starting a tmux client is as good as any other.
const attachGrace = 5 * time.Second

// hold takes the lock that a handover and a rendering share, and returns the
// function that releases it.
func (h *handovers) hold() func() {
	h.mu.Lock()
	return h.mu.Unlock
}

// take records that a task's terminal has been handed to a client. The caller
// holds the lock.
func (h *handovers) take(id domain.TaskID, now time.Time) {
	h.handed[id] = now
}

// pending reports that a client was sent to this terminal recently enough to
// still be on its way. The caller holds the lock.
//
// The expired entry is dropped as it is read, which is all the housekeeping this
// needs: entries are only created by a user asking to attach, and each one is
// either overwritten by the next attach or removed by the first frame drawn
// after it expired.
func (h *handovers) pending(id domain.TaskID, now time.Time) bool {
	at, found := h.handed[id]
	if !found {
		return false
	}
	if now.Sub(at) >= attachGrace {
		delete(h.handed, id)
		return false
	}
	return true
}
