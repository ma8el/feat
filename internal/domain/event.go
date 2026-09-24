package domain

import (
	"strconv"
	"time"
)

// Event is one recorded change in a task's state. It is the task's history and
// the payload of the daemon's event stream, and it carries state changes only:
// never terminal output, never file contents, never a secret value.
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

// Event types. Each names one state dimension, because the dimensions stay
// separate: a process going idle and a task becoming ready for review are
// different events, and no reader should have to infer one from the other.
const (
	// EventTaskCreated records a new task record, which begins as a draft.
	// Confirmation is a workflow transition and is recorded as one.
	EventTaskCreated EventType = "task_created"
	// EventTaskUpdated records a change to a draft's editable shape: its title,
	// brief, repository selection, or resolved plan. Only a draft produces one,
	// because a task's shape freezes when it leaves draft.
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
	// EventReviewChanged records a change to what is known about a task's work:
	// the agent's own report, or the results a completion gate produced. It
	// carries no from and no to; the user's decision is a workflow transition
	// and is recorded as one (ADR-047).
	EventReviewChanged EventType = "review_state_changed"
	// EventExecutionChanged records a change to the agent's execution
	// environment: the identity assigned before creation, and what was observed
	// afterwards. The environment precedes the session that records it, so an
	// interruption between the two still leaves a record of what may exist
	// (ADR-033).
	EventExecutionChanged EventType = "agent_execution_changed"
	// EventReconciled records that startup reconciliation compared desired and
	// observed state for the task.
	EventReconciled EventType = "task_reconciled"
	// EventNotificationSent records that Feat interrupted the user about this
	// task, and why. A dismissed desktop notification leaves nothing behind, so
	// this is the only record that Feat asked for attention (ADR-039). It is
	// never itself notifiable: recording an event publishes it, and notifying
	// about a notification would loop.
	EventNotificationSent EventType = "notification_sent"
	// EventControlRefused records that a message the agent wrote was refused,
	// and why. The refusal belongs on the task it happened to, so a user can see
	// what the agent asked for without reading the daemon's log.
	EventControlRefused EventType = "control_message_refused"
	// EventPublicationChanged records that a publication planned, published, or
	// failed for one of a task's repositories. A publication applies one
	// repository at a time and does not roll back, so a partial one needs an
	// account of what happened and when (ADR-073). It carries no from and no to,
	// because a task has no publication state of its own.
	EventPublicationChanged EventType = "task_publication_changed"
	// EventCleanedUp records that a cleanup removed one class of the resources a
	// task owned, or failed part way through removing it. The snapshot keeps
	// what the task was; this keeps what became of what it owned (FR-CLEAN-001,
	// ADR-037).
	EventCleanedUp EventType = "task_resources_removed"
)

// Valid reports whether the type is documented.
func (t EventType) Valid() bool {
	switch t {
	case EventTaskCreated, EventTaskUpdated, EventWorkflowChanged, EventAttentionChanged,
		EventRepositoryObserved, EventProcessChanged, EventRuntimeChanged,
		EventReviewChanged, EventExecutionChanged, EventReconciled, EventNotificationSent,
		EventControlRefused, EventPublicationChanged, EventCleanedUp:
		return true
	default:
		return false
	}
}

// Validate reports whether the event is internally consistent. The sequence is
// not checked here, because the log that appends the event assigns it.
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
