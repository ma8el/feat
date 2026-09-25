package daemon

import (
	"sync"
	"time"

	"github.com/ma8el/feat/internal/domain"
)

// handovers records the terminals a client has been sent to attach to, and
// serialises that against rendering them.
//
// A rendering sizes the window it draws and tmux then holds it at that size, so a
// native client attaching afterwards gets a terminal the size of the dashboard's
// main region and dots over the rest of its screen. The size is released as the
// attach target is handed out (AttachInfo, OpenShell).
//
// Releasing it is not enough. Between the release and the client reaching tmux
// there is a window of tens of milliseconds, and a rendering in it asks tmux who
// is attached, is told nobody, and pins the size again. The client stays in that
// state, because Bubble Tea blocks its event loop while the terminal is handed
// over and the dashboard that would notice it is not polling.
//
// So a handed-out attach target is remembered, and a rendering treats the task as
// watched until the client arrives or is judged not to be coming. None of it is
// persistent: a daemon that restarted has no attach in flight.
type handovers struct {
	// mu is held across a whole render as well as across recording a handover, so
	// a poll cannot read "nobody is attached" and then pin the window it was about
	// to be told not to touch. It costs an attaching client one frame.
	mu     sync.Mutex
	handed map[domain.TaskID]time.Time
}

func newHandovers() *handovers { return &handovers{handed: make(map[domain.TaskID]time.Time)} }

// attachGrace is how long a client has to arrive before rendering takes the
// window back. Too short and the defect returns for a slow attach; too long and a
// task whose attach failed draws at the window's size until it expires, which the
// renderer clips and the next frame corrects.
//
// It is not configurable, for the reason startupGrace is not: any value
// comfortably longer than starting a tmux client will do.
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
// needs: only an attach creates one, and the next attach or the first frame after
// it expires removes it.
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
