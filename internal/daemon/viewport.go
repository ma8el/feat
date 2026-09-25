package daemon

import (
	"sync"

	"github.com/ma8el/feat/internal/tmux"
)

// viewport is the size a client last asked for a terminal at. It lets a task's
// window be created at the size it will be drawn into rather than at tmux's
// 80x24, which an agent's first output would otherwise be wrapped at for good
// (tmux.sizeBeforeStart).
//
// The daemon learns the size from the frames clients ask for, because a launch
// says which task to start and not how large the screen is. It is not persisted
// and not per client: the render path corrects the size on the first frame, and
// a daemon that has drawn nothing offers nothing.
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
