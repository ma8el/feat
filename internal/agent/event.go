package agent

import (
	"time"

	"github.com/ma8el/feat/internal/domain"
)

// EventKind is what a normalized agent event reports.
//
// The vocabulary separates what a provider observed from what it concluded,
// because the two carry different weight and Feat must never derive the second
// from the first. KindTurnEnded is an observation that a turn finished;
// KindReviewRequested is the agent stating that its work is ready to look at.
// There is deliberately no kind that means both.
type EventKind string

// Agent event kinds.
const (
	// KindSessionStarted reports that the provider's session began. It carries
	// the provider's own session identifier where one exists.
	KindSessionStarted EventKind = "session_started"
	// KindPromptSubmitted reports that the user sent the agent a prompt. In a
	// review state it is the conservative revision signal FR-AGENT-009 asks for.
	KindPromptSubmitted EventKind = "prompt_submitted"
	// KindTurnEnded reports the end of a turn.
	//
	// It means idle after a grace period and it means nothing else. It is never
	// completion, never review, and never a question: a provider that cannot
	// distinguish a finished turn from a waiting one leaves Feat conservative
	// (invariant 13, FR-AGENT-008).
	KindTurnEnded EventKind = "turn_ended"
	// KindNotification reports that the provider asked for the user's
	// attention, such as a permission prompt.
	KindNotification EventKind = "notification"
	// KindSessionEnded reports that the session finished normally.
	KindSessionEnded EventKind = "session_ended"
	// KindSessionFailed reports that the session ended in an error.
	KindSessionFailed EventKind = "session_failed"
	// KindReviewRequested is the explicit semantic event FR-AGENT-008 requires
	// before a task can be reviewed. Only an agent-authored control message
	// produces one.
	KindReviewRequested EventKind = "review_requested"
	// KindCompletionReport is the agent's account of what it did.
	KindCompletionReport EventKind = "completion_report"
	// KindOpenQuestion is the agent reporting that it is blocked on the user.
	KindOpenQuestion EventKind = "open_question"
)

// Valid reports whether the kind is documented.
func (k EventKind) Valid() bool {
	switch k {
	case KindSessionStarted, KindPromptSubmitted, KindTurnEnded, KindNotification,
		KindSessionEnded, KindSessionFailed, KindReviewRequested,
		KindCompletionReport, KindOpenQuestion:
		return true
	default:
		return false
	}
}

// Event is one normalized agent event.
//
// It carries state, never transcript: no prompt text, no assistant message, no
// terminal output, and no file contents. Events reach the task's event log and
// the daemon's event stream, and neither may carry what the agent was working
// on (docs/06-technical-architecture.md).
type Event struct {
	// Kind is what happened.
	Kind EventKind
	// OccurredAt is when the provider reported it.
	OccurredAt time.Time
	// ProviderSessionID is the provider's own session identifier, on the events
	// that carry one.
	ProviderSessionID string
	// Continued reports a session-start that resumed, cleared, or compacted an
	// existing session rather than beginning a new one. The distinction matters
	// because only a genuine start means the agent has begun the task.
	Continued bool
	// NeedsInput reports that the provider said it is blocked on the user, as
	// opposed to merely having finished a turn.
	NeedsInput bool
	// Summary is a short explanation for the task's history. It is written by
	// Feat or taken from a field the agent authored for this purpose, and it is
	// never a fragment of the conversation.
	Summary string
	// Checks are the agent's own account of verification it ran. They are a
	// claim rather than a result, which domain.Check records through its
	// reporter.
	Checks []domain.Check
}

// AgentReported converts the event's checks into review checks attributed to
// the agent.
//
// Attribution is the point: a provider-gated result was enforced and an
// agent-reported one was asserted, and a dashboard that showed them alike would
// tell the user something Feat does not know.
func (e Event) AgentReported() []domain.Check {
	if len(e.Checks) == 0 {
		return nil
	}
	checks := make([]domain.Check, 0, len(e.Checks))
	for _, check := range e.Checks {
		check.Reporter = domain.ReporterAgent
		if !check.Status.Valid() {
			check.Status = domain.CheckUnknown
		}
		checks = append(checks, check)
	}
	return checks
}
