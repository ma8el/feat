package claude

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ma8el/feat/internal/agent"
	"github.com/ma8el/feat/internal/control"
	"github.com/ma8el/feat/internal/domain"
)

// providerPayload is what a generated hook writes: the event name the hook was
// installed for, and Claude's own payload underneath it.
//
// The name is carried separately rather than read from the payload, so that a
// Claude version which renames or drops hook_event_name still produces events
// Feat can attribute. The hook knows which event it is because Feat generated
// one script per event.
type providerPayload struct {
	Hook  string          `json:"hook"`
	Event json.RawMessage `json:"event"`
}

// hookEvent is the part of Claude's hook payload Feat reads.
//
// Everything Claude sends that is not here is deliberately ignored: prompt
// text, the last assistant message, and the transcript path are the
// conversation, and an agent event carries state rather than transcript.
type hookEvent struct {
	SessionID string `json:"session_id"`
	// Source distinguishes a session that began from one that resumed, cleared,
	// or compacted. Only the first means the agent has started the task.
	Source string `json:"source"`
	// Reason is why a session ended.
	Reason string `json:"reason"`
	// NotificationType says what kind of attention Claude asked for.
	NotificationType string `json:"notification_type"`
	// Error is the failure a StopFailure reports.
	Error string `json:"error"`
	// StopHookActive reports that this Stop follows a hook that already blocked
	// one, which is Claude telling a hook not to block again.
	StopHookActive bool `json:"stop_hook_active"`
}

// continuedSources are the SessionStart sources that continue a session rather
// than beginning one.
//
// A user typing /clear or /compact, or resuming after a restart, produces a
// session start. Treating one as a launch would re-run the transition that says
// the agent has begun work, and would do it every time somebody cleared the
// screen.
var continuedSources = map[string]bool{
	"resume":  true,
	"clear":   true,
	"compact": true,
}

// parseHook normalizes one Claude hook event.
//
// An event this build does not model is reported as not applicable rather than
// as an error: a provider may emit more than Feat represents, and a future
// Claude version emitting something new must not turn a task into a failure.
func parseHook(message control.Message) (agent.Event, bool, error) {
	var payload providerPayload
	if err := json.Unmarshal(message.Payload, &payload); err != nil {
		return agent.Event{}, false, &control.RejectionError{
			File:   message.File(),
			Reason: "carries a provider payload this adapter cannot read: " + err.Error(),
		}
	}

	var raw hookEvent
	if len(payload.Event) > 0 {
		// A payload whose body is unreadable still identifies its event, so the
		// state change is kept and only its detail is lost.
		_ = json.Unmarshal(payload.Event, &raw)
	}

	event := agent.Event{OccurredAt: message.OccurredAt, ProviderSessionID: raw.SessionID}
	switch payload.Hook {
	case hookSessionStart:
		event.Kind = agent.KindSessionStarted
		event.Continued = continuedSources[raw.Source]
		event.Summary = "Claude session started"
		if event.Continued {
			event.Summary = "Claude session continued (" + raw.Source + ")"
		}

	case hookUserPromptSubmit:
		event.Kind = agent.KindPromptSubmitted
		// The prompt itself is deliberately not recorded. It is the
		// conversation, and the task history is not a transcript.
		event.Summary = "a prompt was submitted"

	case hookStop:
		// The end of a turn. It becomes idle after the grace period and it
		// never becomes anything else (FR-AGENT-008, invariant 13).
		event.Kind = agent.KindTurnEnded
		event.Summary = "the agent ended its turn"

	case hookStopFailure:
		event.Kind = agent.KindSessionFailed
		event.Summary = "the agent stopped with an error"
		if detail := firstLine(raw.Error); detail != "" {
			event.Summary += ": " + detail
		}

	case hookNotification:
		event.Kind = agent.KindNotification
		event.NeedsInput = true
		event.Summary = "the agent is waiting for you"
		if raw.NotificationType != "" {
			event.Summary += " (" + raw.NotificationType + ")"
		}

	case hookSessionEnd:
		event.Kind = agent.KindSessionEnded
		event.Summary = "the Claude session ended"
		if raw.Reason != "" {
			event.Summary += ": " + raw.Reason
		}

	default:
		return agent.Event{}, false, nil
	}
	return event, true, nil
}

// report is the document the generated helper wraps.
type report struct {
	Summary  string        `json:"summary"`
	Question string        `json:"question"`
	Checks   []checkReport `json:"checks"`
}

// checkReport is one check the agent claims to have run.
type checkReport struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Detail string `json:"detail"`
	// Repository names the repository a check belongs to, for a project whose
	// checks are per repository.
	Repository string `json:"repository"`
}

// parseReport normalizes an agent-authored review request or completion report.
//
// This is the only path to semantic completion. It exists because the agent
// said so, which is exactly what FR-AGENT-008 requires and what no hook event
// can substitute for.
func parseReport(message control.Message, kind agent.EventKind) (agent.Event, bool, error) {
	body, err := decodeReport(message)
	if err != nil {
		return agent.Event{}, false, err
	}

	event := agent.Event{
		Kind:       kind,
		OccurredAt: message.OccurredAt,
		Summary:    firstLine(body.Summary),
	}
	if event.Summary == "" {
		event.Summary = "the agent reported its work without a summary"
	}

	for _, reported := range body.Checks {
		check, err := reported.check(message)
		if err != nil {
			return agent.Event{}, false, err
		}
		event.Checks = append(event.Checks, check)
	}
	return event, true, nil
}

// parseQuestion normalizes an agent-authored open question.
func parseQuestion(message control.Message) (agent.Event, bool, error) {
	body, err := decodeReport(message)
	if err != nil {
		return agent.Event{}, false, err
	}

	question := firstLine(body.Question)
	if question == "" {
		question = firstLine(body.Summary)
	}
	if question == "" {
		question = "the agent asked a question without saying what it was"
	}
	return agent.Event{
		Kind:       agent.KindOpenQuestion,
		OccurredAt: message.OccurredAt,
		NeedsInput: true,
		Summary:    question,
	}, true, nil
}

func decodeReport(message control.Message) (report, error) {
	var body report
	if len(message.Payload) == 0 {
		return body, nil
	}
	if err := json.Unmarshal(message.Payload, &body); err != nil {
		return body, &control.RejectionError{
			File:   message.File(),
			Reason: "carries a body this adapter cannot read: " + err.Error(),
		}
	}
	return body, nil
}

// check converts one reported check, refusing a status Feat does not know.
//
// A status is not guessed. An agent that reports "mostly passed" is telling
// Feat something Feat cannot record, and recording it as passed would turn the
// agent's ambiguity into Feat's claim.
func (c checkReport) check(message control.Message) (domain.Check, error) {
	if c.ID == "" {
		return domain.Check{}, &control.RejectionError{
			File:   message.File(),
			Reason: "reports a check with no id",
		}
	}
	status := domain.CheckStatus(c.Status)
	if c.Status == "" {
		status = domain.CheckUnknown
	}
	if !status.Valid() {
		return domain.Check{}, &control.RejectionError{
			File: message.File(),
			Reason: fmt.Sprintf("reports check %q with status %q, and a check is passed, failed, skipped, or unknown",
				c.ID, c.Status),
		}
	}
	check := domain.Check{
		ID:     c.ID,
		Status: status,
		Detail: firstLine(c.Detail),
		// Attribution is the point of recording it at all: this result was
		// asserted by the agent, not enforced by anything.
		Reporter: domain.ReporterAgent,
		RanAt:    message.OccurredAt,
	}
	if c.Repository != "" {
		id := domain.RepositoryID(c.Repository)
		if err := id.Validate(); err != nil {
			return domain.Check{}, &control.RejectionError{
				File:   message.File(),
				Reason: fmt.Sprintf("reports check %q against repository %q, which is not a repository identifier", c.ID, c.Repository),
			}
		}
		check.RepositoryID = id
	}
	return check, nil
}

// maxSummaryBytes bounds a summary that reaches the dashboard and the event
// stream.
const maxSummaryBytes = 240

// firstLine reduces agent-written text to one bounded line.
//
// Summaries reach the dashboard, the task history, and the event stream. A
// multi-line or unbounded one would turn a state record into a place the agent
// can write whatever it likes, and a terminal into something it can redraw.
func firstLine(text string) string {
	line := text
	if index := strings.IndexAny(line, "\r\n"); index >= 0 {
		line = line[:index]
	}
	line = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, line)
	line = strings.TrimSpace(line)

	if len(line) > maxSummaryBytes {
		// Cut on a rune boundary, so the result is still valid UTF-8.
		cut := maxSummaryBytes
		for cut > 0 && !isBoundary(line, cut) {
			cut--
		}
		line = strings.TrimSpace(line[:cut]) + "…"
	}
	return line
}

func isBoundary(text string, index int) bool {
	return index >= len(text) || text[index]&0xc0 != 0x80
}
