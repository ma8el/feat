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
	// EventExecutionChanged records a change to the agent's execution
	// environment: the identity it was given before it was created, and what was
	// observed of it afterwards.
	//
	// It exists because the environment is created before the session that
	// records it can exist — a session needs the terminal that runs inside the
	// container — so the event log is where the identity of a container is
	// written down first. An interruption between the two therefore still leaves
	// a durable record naming what may exist (ADR-033).
	EventExecutionChanged EventType = "agent_execution_changed"
	// EventReconciled records that startup reconciliation compared desired and
	// observed state for the task.
	EventReconciled EventType = "task_reconciled"
	// EventNotificationSent records that Feat interrupted the user about this
	// task, and why.
	//
	// A desktop notification is gone the moment it is dismissed, so without this
	// there would be no record that Feat asked for somebody's attention. That
	// matters twice: a user who half-saw one can find out what it was, and slice
	// 13 has to measure how many idle notifications turned out to be false.
	//
	// It is deliberately not itself a notifiable change. Recording an event
	// publishes it, and a notification that notified about itself would be a loop
	// at the speed of the event bus.
	EventNotificationSent EventType = "notification_sent"
	// EventCleanedUp records that a cleanup removed one class of the resources a
	// task owned, or failed part way through removing it.
	//
	// It is what makes an archived task explainable. The snapshot keeps what the
	// task was, and this keeps what became of what it owned — including a
	// removal that stopped half way, which is the case a user most needs an
	// account of (FR-CLEAN-001, ADR-037).
	EventCleanedUp EventType = "task_resources_removed"
)

// Valid reports whether the type is documented.
func (t EventType) Valid() bool {
	switch t {
	case EventTaskCreated, EventTaskUpdated, EventWorkflowChanged, EventAttentionChanged,
		EventRepositoryObserved, EventProcessChanged, EventRuntimeChanged,
		EventReviewChanged, EventExecutionChanged, EventReconciled, EventNotificationSent,
		EventCleanedUp:
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

// formatInt renders a count for an error message.
func formatInt(value int) string { return strconv.Itoa(value) }
