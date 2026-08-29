package daemon

import (
	"sync"

	"github.com/ma8el/feat/internal/tmux"
)

// viewport is the size a client last asked for a terminal at.
//
// It exists so that a task's window can be created at the size it will be drawn
// into rather than at tmux's 80x24, which is what an agent's first output would
// otherwise be wrapped at for good (tmux.sizeBeforeStart). The daemon has no
// other way to know: a launch says which task to start, not how large the
// screen watching it is, and the only place a client's dimensions reach the
// daemon at all is the frame it asks for.
//
// So this is a memory of the last one rather than a fact about the next. It is
// deliberately not persisted and deliberately not per client: the size is a
// hint, the render path corrects it on the first frame whatever it was, and a
// value read from a previous run of the daemon would be a guess about a
// terminal that has since been closed. A daemon that has drawn nothing yet has
// nothing to offer and says so, which leaves creation exactly as it was.
type viewport struct {
	mu   sync.Mutex
	last tmux.Size
}

func newViewport() *viewport { return &viewport{} }

// observe records the size of a frame a client asked for.
func (v *viewport) observe(size tmux.Size) {
	if !size.Known() {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.last = size
}

// size returns the last size a client drew a terminal at, or the zero value if
// none has.
func (v *viewport) size() tmux.Size {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.last
}
