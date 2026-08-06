package domain

import (
	"strconv"
	"time"
)

// Event is one recorded change in a task's state.
//
// Events are the task's history and, later, the payload of the daemon's event
// stream. They carry state changes only: never terminal output, never file
// contents, and never a secret value.
type Event struct {
	// Sequence orders the events of one task. It is assigned by the event log
	// when the event is appended, starting at 1.
	Sequence uint64
	// ProjectID is the project the task belongs to.
	ProjectID ProjectID
	// TaskID is the task the event belongs to.
	TaskID TaskID
	// RepositoryID names the repository for a repository-scoped event, and is
	// empty otherwise.
	RepositoryID RepositoryID
	// Type is what changed.
	Type EventType
	// From is the previous value, where the event describes a transition.
	From string
	// To is the new value, where the event describes a transition.
	To string
	// Detail is a short human-readable explanation, such as why a lifecycle
	// failed. It must not carry secrets.
	Detail string
	// OccurredAt is when the change happened.
	OccurredAt time.Time
}

// EventType identifies what an event describes.
type EventType string

// Event types. Each names one state dimension, because the dimensions are
// separate: a process going idle and a task becoming ready for review are
// different events, and no reader should have to infer one from the other.
const (
	// EventTaskCreated records a new task record, which begins as a draft.
	// Confirmation is a workflow transition and is recorded as one.
	EventTaskCreated EventType = "task_created"
	// EventTaskUpdated records a change to a draft's editable shape: its title,
	// its brief, its repository selection, or the plan resolved for it. Only a
	// draft can produce one, because a task's shape is frozen when it leaves
	// draft.
	EventTaskUpdated EventType = "task_updated"
	// EventWorkflowChanged records a workflow state transition.
	EventWorkflowChanged EventType = "task_workflow_changed"
	// EventAttentionChanged records an attention state change.
	EventAttentionChanged EventType = "task_attention_changed"
	// EventRepositoryObserved records a new Git observation for one repository.
	EventRepositoryObserved EventType = "task_repository_observed"
	// EventProcessChanged records an agent process state change.
	EventProcessChanged EventType = "agent_process_changed"
	// EventRuntimeChanged records an application runtime state change.
	EventRuntimeChanged EventType = "runtime_state_changed"
	// EventReviewChanged records a review status change.
	EventReviewChanged EventType = "review_state_changed"
	// EventReconciled records that startup reconciliation compared desired and
	// observed state for the task.
	EventReconciled EventType = "task_reconciled"
)

// Valid reports whether the type is documented.
func (t EventType) Valid() bool {
	switch t {
	case EventTaskCreated, EventTaskUpdated, EventWorkflowChanged, EventAttentionChanged,
		EventRepositoryObserved, EventProcessChanged, EventRuntimeChanged,
		EventReviewChanged, EventReconciled:
		return true
	default:
		return false
	}
}

// Validate reports whether the event is internally consistent.
//
// The sequence is not checked here: it belongs to the log the event is appended
// to, which assigns it.
func (e Event) Validate() error {
	if err := e.ProjectID.Validate(); err != nil {
		return err
	}
	if err := e.TaskID.Validate(); err != nil {
		return err
	}
	if e.RepositoryID != "" {
		if err := e.RepositoryID.Validate(); err != nil {
			return err
		}
	}
	if !e.Type.Valid() {
		return &ValidationError{
			Entity: "event",
			ID:     e.TaskID.String(),
			Field:  "type",
			Reason: "must be a documented event type, but is " + quote(string(e.Type)),
		}
	}
	if e.OccurredAt.IsZero() {
		return &ValidationError{Entity: "event", ID: e.TaskID.String(), Field: "occurred_at", Reason: "must be set"}
	}
	return nil
}

// formatUint renders a sequence number for an error message.
func formatUint(value uint64) string { return strconv.FormatUint(value, 10) }
