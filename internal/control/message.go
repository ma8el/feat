package control

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/ma8el/feat/internal/domain"
)

// SchemaVersion is the control-message schema this build understands.
//
// A message declaring another version is rejected rather than interpreted, for
// the reason configuration is: a document Feat half-understands would change
// state the agent did not ask it to change.
const SchemaVersion = 1

// MaxMessageBytes bounds one control message.
//
// The messages are state changes and short reports, not payloads. A bound well
// below the point where reading one is expensive means an oversized document is
// refused by size rather than by the memory it took to find out.
const MaxMessageBytes = 256 << 10

// MessageType is what a control message asks for or reports.
//
// The vocabulary is provider-neutral on purpose. A Claude hook event arrives as
// TypeProviderEvent carrying the provider's own payload, and only the provider
// adapter knows how to read it; this package never learns what a Stop event is.
type MessageType string

// Message types. docs/03-domain-model.md lists the agent-authored ones;
// TypeProviderEvent carries a provider-native event that a hook emitted.
const (
	// TypeProviderEvent is a provider-native event, verbatim, for the provider
	// adapter to parse. It asks for nothing by itself.
	TypeProviderEvent MessageType = "provider_event"
	// TypeReviewRequested is the agent explicitly asking for review. It is the
	// only way a task reaches review_requested: no end-of-turn signal produces
	// one (FR-AGENT-008).
	TypeReviewRequested MessageType = "review_requested"
	// TypeCompletionReport is the agent's account of what it did, including
	// whatever verification it claims to have run.
	TypeCompletionReport MessageType = "completion_report"
	// TypeOpenQuestion is the agent reporting that it is blocked on the user.
	TypeOpenQuestion MessageType = "open_question"
	// TypeRuntimeRequested is the agent asking for application services.
	//
	// It is recognised and recorded, and it does nothing. FR-RUN-009 places
	// agent-requested runtime after v0, and a request stays inert until host
	// validation and explicit user approval in every version
	// (docs/05-security-model.md). Recognising it is what lets Feat say that it
	// refused something, rather than leaving the agent to wonder whether anyone
	// read it.
	TypeRuntimeRequested MessageType = "runtime_requested"
)

// Valid reports whether the type is documented.
func (t MessageType) Valid() bool {
	switch t {
	case TypeProviderEvent, TypeReviewRequested, TypeCompletionReport,
		TypeOpenQuestion, TypeRuntimeRequested:
		return true
	default:
		return false
	}
}

// Capability is what a message type requires before it may take effect.
type Capability string

// Capabilities. Only the ones Feat grants are listed as granted; a message
// requiring anything else is refused by name rather than by silence.
const (
	// CapabilityNone is required by a message that only reports.
	CapabilityNone Capability = ""
	// CapabilityRuntimeControl would be required to act on a runtime request.
	// Nothing grants it in v0.
	CapabilityRuntimeControl Capability = "runtime_control"
)

// Requires returns the capability a message of this type needs.
func (t MessageType) Requires() Capability {
	if t == TypeRuntimeRequested {
		return CapabilityRuntimeControl
	}
	return CapabilityNone
}

// Message is one document exchanged through the control workspace.
//
// It is a wire format, so its fields carry JSON tags and its identifiers are
// strings until they have been validated against the task that owns them.
type Message struct {
	// SchemaVersion is the envelope version. It must be SchemaVersion.
	SchemaVersion int `json:"schema_version"`
	// ID is unique per message. It is what makes replaying an outbox
	// idempotent: an identifier Feat already applied is recognised and skipped.
	ID string `json:"id"`
	// TaskID is the task the message belongs to. A message naming another task
	// is refused rather than applied to the one whose workspace it appeared in.
	TaskID string `json:"task_id"`
	// Type is what the message reports or asks for.
	Type MessageType `json:"type"`
	// OccurredAt is when the agent produced it.
	OccurredAt time.Time `json:"occurred_at"`
	// Payload is the type-specific body, left unparsed here. For a provider
	// event it is the provider's own document, which only its adapter reads.
	Payload json.RawMessage `json:"payload,omitempty"`

	// file is the outbox entry the message was read from. It is not part of the
	// document and is used to explain where a rejected message came from.
	file string
}

// File returns the outbox file the message was read from.
func (m Message) File() string { return m.file }

// idPattern bounds a message identifier. It reaches log lines and the processed
// record, and it is generated rather than typed, so the pattern is deliberately
// narrower than the identifier needs to be.
const maxIDBytes = 128

// Validate reports whether the message is well formed and belongs to the task
// that owns the workspace it appeared in.
//
// Every rule the security model lists for a control message is checked here or
// in the reader that found the file: schema version, task ownership, message
// type, event identity, size, path, and required capability. What is not
// checked here is whether it was already processed, which is a question about
// history rather than about the document.
func (m Message) Validate(owner domain.TaskID) error {
	if m.SchemaVersion != SchemaVersion {
		return &RejectionError{
			Reason: fmt.Sprintf("declares schema version %d, and this build understands %d",
				m.SchemaVersion, SchemaVersion),
		}
	}
	if m.ID == "" {
		return &RejectionError{Reason: "carries no event id, so it could not be recognised if it arrived twice"}
	}
	if len(m.ID) > maxIDBytes {
		return &RejectionError{
			Reason: fmt.Sprintf("has an event id of %d bytes, and the limit is %d", len(m.ID), maxIDBytes),
		}
	}
	if !safeName(m.ID) {
		return &RejectionError{
			Reason: "has an event id that is not a plain name: " + quote(m.ID),
		}
	}
	if m.TaskID != owner.String() {
		return &RejectionError{
			Reason: "names task " + quote(m.TaskID) + ", and it appeared in the control workspace of " + owner.String(),
		}
	}
	if !m.Type.Valid() {
		return &RejectionError{Reason: "has message type " + quote(string(m.Type)) + ", which this build does not know"}
	}
	if m.OccurredAt.IsZero() {
		return &RejectionError{Reason: "carries no occurrence time"}
	}
	return nil
}

// RejectionError explains why a control message was not applied.
//
// It is a distinct type because a rejected message is a normal event rather
// than a failure of the daemon: it is recorded, reported, and never retried.
type RejectionError struct {
	// File is the outbox entry, when the failure is about one.
	File string
	// Reason says what was wrong, phrased to follow "the control message".
	Reason string
}

func (e *RejectionError) Error() string {
	if e.File == "" {
		return "the control message " + e.Reason
	}
	return "the control message in " + e.File + " " + e.Reason
}

// maxQuotedBytes bounds what a refusal repeats back.
//
// Every value quoted here was written by an agent, and a refusal reaches a log
// line, the host-only record of settled messages, and the task's own event log.
// A message may be a quarter of a megabyte, and none of those readers is
// improved by a field that long.
const maxQuotedBytes = 96

func quote(value string) string {
	if len(value) > maxQuotedBytes {
		return fmt.Sprintf("%q…", value[:maxQuotedBytes])
	}
	return fmt.Sprintf("%q", value)
}
