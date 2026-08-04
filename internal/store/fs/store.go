package fs

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/store"
)

// The state layout from docs/06-technical-architecture.md. Every path segment
// below is either a constant or an identifier the domain has already validated,
// so no user-supplied text is joined into a path unchecked.
const (
	projectsDir = "projects"
	tasksDir    = "tasks"
	projectFile = "project.json"
	taskFile    = "task.json"
	briefFile   = "prompt.md"
	eventsFile  = "events.jsonl"
	reviewFile  = "review.json"
)

// File modes. State belongs to one user: it names their repositories, their
// paths, and their task briefs.
const (
	dirPerm  os.FileMode = 0o700
	filePerm os.FileMode = 0o600
)

// Store is the file-backed implementation of the storage interfaces.
type Store struct {
	root string

	// mu guards the maps below, which are shared by every caller.
	mu sync.Mutex
	// locks serialize the writes to one record. Entries are never removed: one
	// mutex per project and task is bounded by the number of records, and a
	// removable entry would need a second lock to be removed safely.
	locks map[string]*sync.Mutex
	// sequences caches the highest sequence number appended to each event log,
	// so an append does not re-read the log every time. It is rebuilt from the
	// files after a restart.
	sequences map[string]uint64

	// interrupt is a test hook. It is nil in a real process.
	interrupt interruption
}

var _ store.Store = (*Store)(nil)

// Open prepares a store under root, creating the directory if it is missing.
//
// The caller supplies the root rather than resolving it here, so that this
// package stays independent of the user's environment and a test can run
// against a temporary directory.
func Open(root string) (*Store, error) {
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("state directory %q must be an absolute path", root)
	}
	if err := os.MkdirAll(root, dirPerm); err != nil {
		return nil, fmt.Errorf("creating state directory %s: %w", root, err)
	}
	return &Store{
		root:      root,
		locks:     make(map[string]*sync.Mutex),
		sequences: make(map[string]uint64),
	}, nil
}

// Root returns the directory the store writes to.
func (s *Store) Root() string { return s.root }

// Projects returns the project repository.
func (s *Store) Projects() store.ProjectStore { return projectStore{store: s} }

// Tasks returns the task repository.
func (s *Store) Tasks() store.TaskStore { return taskStore{store: s} }

// Events returns the per-task event log.
func (s *Store) Events() store.EventStore { return eventStore{store: s} }

// Reviews returns the per-task review repository.
func (s *Store) Reviews() store.ReviewStore { return reviewStore{store: s} }

// projectDir returns the directory holding one project's state.
func (s *Store) projectDir(id domain.ProjectID) (string, error) {
	if err := id.Validate(); err != nil {
		return "", err
	}
	return filepath.Join(s.root, projectsDir, id.String()), nil
}

// taskDir returns the directory holding one task's state.
func (s *Store) taskDir(ref store.TaskRef) (string, error) {
	if err := ref.Validate(); err != nil {
		return "", err
	}
	return filepath.Join(s.root, projectsDir, ref.Project.String(), tasksDir, ref.Task.String()), nil
}

// lock serializes writes to one record and returns the release function.
func (s *Store) lock(key string) func() {
	s.mu.Lock()
	mutex, ok := s.locks[key]
	if !ok {
		mutex = &sync.Mutex{}
		s.locks[key] = mutex
	}
	s.mu.Unlock()

	mutex.Lock()
	return mutex.Unlock
}

// cachedSequence returns the highest sequence recorded for an event log.
func (s *Store) cachedSequence(key string) (uint64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sequence, ok := s.sequences[key]
	return sequence, ok
}

// recordSequence caches the highest sequence appended to an event log.
func (s *Store) recordSequence(key string, sequence uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sequences[key] = sequence
}
