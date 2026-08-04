package store

import (
	"context"

	"github.com/ma8el/feat/internal/domain"
)

// Store is the persistent state of one Feat installation.
//
// The daemon is the only writer (ADR-008), so the interfaces below assume a
// single writer and do not carry a concurrency token. An implementation is
// still required to serialize its own writes, because the daemon serves several
// clients at once.
//
// Nothing here exposes a file, a path, or a query: the file-backed
// implementation must be replaceable by a database-backed one without changing
// a caller (ADR-010).
type Store interface {
	// Projects returns the project repository.
	Projects() ProjectStore
	// Tasks returns the task repository.
	Tasks() TaskStore
	// Events returns the per-task event log.
	Events() EventStore
	// Reviews returns the per-task review repository.
	Reviews() ReviewStore
}

// ProjectStore persists registered projects.
type ProjectStore interface {
	// Save records the project, replacing any earlier snapshot of it.
	Save(ctx context.Context, project *domain.Project) error
	// Load returns the project, or an error matching ErrNotFound.
	Load(ctx context.Context, id domain.ProjectID) (*domain.Project, error)
	// List returns every registered project, ordered by identifier.
	List(ctx context.Context) ([]*domain.Project, error)
}

// TaskStore persists tasks.
//
// Tasks are addressed by project and task identifier together. That mirrors
// task ownership: a task belongs to exactly one project (invariant 1), and a
// caller that has a task identifier without knowing its project has lost the
// relationship rather than found a shortcut.
type TaskStore interface {
	// Save records the task, replacing any earlier snapshot of it.
	Save(ctx context.Context, task *domain.Task) error
	// Load returns the task, or an error matching ErrNotFound.
	Load(ctx context.Context, ref TaskRef) (*domain.Task, error)
	// List returns every task of one project, ordered by identifier.
	List(ctx context.Context, project domain.ProjectID) ([]*domain.Task, error)
}

// EventStore is the append-only history of one task.
type EventStore interface {
	// Append records one event and returns it with the sequence number the log
	// assigned. Sequences start at 1 and increase by one.
	Append(ctx context.Context, ref TaskRef, event domain.Event) (domain.Event, error)
	// Replay returns the task's recorded history in order.
	Replay(ctx context.Context, ref TaskRef) (EventLog, error)
}

// EventLog is a replayed task history.
type EventLog struct {
	// Events are the complete records, in order.
	Events []domain.Event
	// IncompleteFinalRecord reports that the log ended mid-record, which is
	// what a crash during an append leaves behind. The partial record is
	// ignored; the next append discards it (FR-STATE-002).
	IncompleteFinalRecord bool
}

// Last returns the last recorded event.
func (l EventLog) Last() (domain.Event, bool) {
	if len(l.Events) == 0 {
		return domain.Event{}, false
	}
	return l.Events[len(l.Events)-1], true
}

// ReviewStore persists per-task review state, which has to survive a restart
// so that a review in progress is not silently lost (FR-REV-004).
type ReviewStore interface {
	// Save records the review, replacing any earlier snapshot of it. The
	// reference carries the project, which a review knows only through its
	// task.
	Save(ctx context.Context, ref TaskRef, review *domain.Review) error
	// Load returns the review, or an error matching ErrNotFound.
	Load(ctx context.Context, ref TaskRef) (*domain.Review, error)
}

// TaskRef addresses one task.
type TaskRef struct {
	// Project is the owning project.
	Project domain.ProjectID
	// Task is the task within that project.
	Task domain.TaskID
}

// Ref returns the reference that addresses a task.
func Ref(task *domain.Task) TaskRef {
	return TaskRef{Project: task.ProjectID, Task: task.ID}
}

// Validate reports whether both identifiers are well formed. Storage checks
// this before it builds any path, so a malformed identifier cannot reach the
// filesystem.
func (r TaskRef) Validate() error {
	if err := r.Project.Validate(); err != nil {
		return err
	}
	return r.Task.Validate()
}

// String renders the reference as "<project>/<task>" for error messages.
func (r TaskRef) String() string { return r.Project.String() + "/" + r.Task.String() }
