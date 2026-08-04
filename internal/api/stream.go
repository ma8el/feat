package api

import (
	"time"

	"github.com/ma8el/feat/internal/domain"
)

// EventKind is what an item on the event stream describes.
type EventKind string

// Event kinds.
//
// Only KindTask carries a domain state change. The other three are statements
// about the stream itself, and they are separate kinds so that a client never
// has to infer the health of its connection from the absence of events.
const (
	// KindHello opens every stream. It states that the daemon is connected and
	// that the client's view of state may be stale, since resume is not
	// supported in v0.1.
	KindHello EventKind = "hello"
	// KindResync answers a client that asked to resume from an event ID. Feat
	// keeps no replay buffer, so the honest answer is to say the position was
	// ignored and current state must be read again.
	KindResync EventKind = "resync"
	// KindTask carries one recorded task event.
	KindTask EventKind = "task_event"
	// KindStreamLost ends a stream whose subscriber fell too far behind. It is
	// the last item the client receives, so that lost events are reported
	// rather than silently missing (ADR-027).
	KindStreamLost EventKind = "stream_lost"
)

// Event is one item on the daemon's event stream.
type Event struct {
	// StreamSequence orders the items of one connection. The opening hello is
	// 0 and recorded events start at 1.
	StreamSequence uint64 `json:"stream_sequence"`
	// Kind is what this item describes.
	Kind EventKind `json:"kind"`
	// Detail explains a stream-level item in one line. It carries no task text
	// and no secret.
	Detail string `json:"detail,omitempty"`
	// TaskEvent is the recorded state change, present when Kind is KindTask.
	TaskEvent *TaskEvent `json:"task_event,omitempty"`
}

// TaskEvent is one recorded change in a task's state.
//
// Events carry state changes only: never terminal output, never file contents,
// and never a secret value.
type TaskEvent struct {
	// Sequence orders the events of one task, assigned by its event log.
	Sequence     uint64 `json:"sequence"`
	ProjectID    string `json:"project_id"`
	TaskID       string `json:"task_id"`
	RepositoryID string `json:"repository_id,omitempty"`
	// Type names the state dimension that changed.
	Type string `json:"type"`
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
	// Detail is a short human-readable explanation.
	Detail     string    `json:"detail,omitempty"`
	OccurredAt time.Time `json:"occurred_at"`
}

// TaskEventOf wraps a recorded domain event for the stream. The stream sequence
// is assigned by whatever publishes it, because it orders one connection rather
// than one task.
func TaskEventOf(event domain.Event) Event {
	return Event{
		Kind: KindTask,
		TaskEvent: &TaskEvent{
			Sequence:     event.Sequence,
			ProjectID:    event.ProjectID.String(),
			TaskID:       event.TaskID.String(),
			RepositoryID: event.RepositoryID.String(),
			Type:         string(event.Type),
			From:         event.From,
			To:           event.To,
			Detail:       event.Detail,
			OccurredAt:   event.OccurredAt,
		},
	}
}
