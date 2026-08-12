package notify

import (
	"context"
	"strings"
	"time"

	"github.com/ma8el/feat/internal/domain"
)

// Condition is why Feat would interrupt the user.
//
// Each one is a change the user would want to act on. Nothing here is a
// convenience: a notification that arrives when nothing needs the user teaches
// them to dismiss the ones that do.
type Condition string

// The notifiable conditions of docs/06-technical-architecture.md.
const (
	// ConditionIdle is an agent that has been quiet long enough to be worth
	// mentioning. It is the only condition that is a duration rather than a
	// change, which is why it is armed rather than mapped from a state.
	ConditionIdle Condition = "idle"
	// ConditionReviewRequested is an agent that explicitly asked for review.
	ConditionReviewRequested Condition = "review_requested"
	// ConditionReadyForReview is a task whose checks passed.
	ConditionReadyForReview Condition = "ready_for_review"
	// ConditionVerificationFailed is a task whose checks failed.
	ConditionVerificationFailed Condition = "verification_failed"
	// ConditionVerificationBlocked is a task whose checks could not be run at
	// all. It is separate from a failure because it is the user's to fix and the
	// agent's to do nothing about: a check that never started is a statement
	// about the project's configuration or the environment it runs in, and
	// nothing has been established about the work (ADR-051).
	ConditionVerificationBlocked Condition = "verification_blocked"
	// ConditionTaskFailed is a task whose lifecycle failed.
	ConditionTaskFailed Condition = "task_failed"
	// ConditionSessionFailed is an agent process that died while the task itself
	// stayed where it was.
	ConditionSessionFailed Condition = "session_failed"
	// ConditionRuntimeFailed is a task's application services failing.
	ConditionRuntimeFailed Condition = "runtime_failed"
)

// Conditions returns every condition Feat interrupts a user for.
//
// It exists so that "each one has been shown to reach a real desktop" can be a
// property of the suite rather than of the day somebody checked: a condition
// added later has to be walked too, and a list that has to be extended by hand
// is a list that eventually is not.
func Conditions() []Condition {
	return []Condition{
		ConditionIdle,
		ConditionReviewRequested,
		ConditionReadyForReview,
		ConditionVerificationFailed,
		ConditionVerificationBlocked,
		ConditionTaskFailed,
		ConditionSessionFailed,
		ConditionRuntimeFailed,
	}
}

// notifiableWorkflow maps the workflow states worth interrupting for.
//
// The table is the policy, in the shape ADR-026 used for the workflow
// transitions and ADR-032 for agent events: what Feat will interrupt somebody
// for is a product decision and should be readable as itself.
//
// Its most important property is an absence. No entry reaches this table from an
// end of turn or from an idle process, because idle is not a state a task
// arrives in — it is a state it stays in, and how long it has stayed is what
// decides whether it is worth saying. That is armed by the daemon's grace timer
// instead, which is what makes "idle notifications do not fire immediately" a
// property of the mechanism rather than of a value somebody chose.
//
// Two conditions are deliberately not here, for the same reason: neither is a
// state a task arrives in. Idle is a duration, and a gate that could not run
// leaves the task in review_requested — the request stands and a person decides,
// exactly as it does for a project that configures no checks — so what has to be
// said is about the run rather than about where the task landed. The daemon
// names those two itself (ADR-051).
var notifiableWorkflow = map[domain.WorkflowState]Condition{
	domain.WorkflowReviewRequested:    ConditionReviewRequested,
	domain.WorkflowReadyForReview:     ConditionReadyForReview,
	domain.WorkflowVerificationFailed: ConditionVerificationFailed,
	domain.WorkflowFailed:             ConditionTaskFailed,
}

// notifiableProcess maps the agent process states worth interrupting for.
//
// Only a failure, and only when the workflow did not move: slice 7's
// normalization already turns a dead agent into a failed task wherever that is
// meaningful, and two notifications for one death would be one too many. The
// caller chooses between this table and the one above; see daemon.notifyTask.
var notifiableProcess = map[domain.ProcessState]Condition{
	domain.ProcessFailed: ConditionSessionFailed,
}

// notifiableRuntime maps the application runtime states worth interrupting for.
//
// A stopped runtime is deliberately absent: v0 stops services only when a user
// asks, so a stop is something they just did rather than something they need to
// hear about (FR-RUN-005).
var notifiableRuntime = map[domain.RuntimeState]Condition{
	domain.RuntimeFailed: ConditionRuntimeFailed,
}

// ForWorkflow reports the condition a workflow state is worth interrupting for.
func ForWorkflow(state domain.WorkflowState) (Condition, bool) {
	condition, ok := notifiableWorkflow[state]
	return condition, ok
}

// ForProcess reports the condition an agent process state is worth interrupting
// for.
func ForProcess(state domain.ProcessState) (Condition, bool) {
	condition, ok := notifiableProcess[state]
	return condition, ok
}

// ForRuntime reports the condition a runtime state is worth interrupting for.
func ForRuntime(state domain.RuntimeState) (Condition, bool) {
	condition, ok := notifiableRuntime[state]
	return condition, ok
}

// Policy is what a project decides about being interrupted.
type Policy struct {
	// Desktop enables desktop notifications.
	Desktop bool
	// SuppressWhileAttached drops a notification about a task the user is
	// already looking at.
	SuppressWhileAttached bool
	// IdleGrace is how long a task must have been idle before it is worth
	// mentioning. It is measured from the moment the task became idle, not from
	// the end of the turn: the dashboard shows idle as soon as the provider's own
	// grace has passed, and this decides when that is worth interrupting somebody
	// for (ADR-035).
	IdleGrace time.Duration
}

// DefaultIdleGrace is used when a project configures none.
const DefaultIdleGrace = 5 * time.Second

// DefaultPolicy is what a project that says nothing gets.
func DefaultPolicy() Policy {
	return Policy{Desktop: true, SuppressWhileAttached: true, IdleGrace: DefaultIdleGrace}
}

// Grace returns the idle grace period, falling back to the default.
func (p Policy) Grace() time.Duration {
	if p.IdleGrace <= 0 {
		return DefaultIdleGrace
	}
	return p.IdleGrace
}

// Subject identifies the task a notification is about.
//
// It carries identification and nothing else. There is deliberately no field for
// the brief, the agent's summary, a repository path, a command, or anything read
// from configuration: a notification leaves the daemon's process and lands
// somewhere the user did not choose, and the way to keep task content out of it
// is to have no way to put it in (docs/05-security-model.md).
type Subject struct {
	// Key is the task's short human-facing identifier.
	Key string
	// Title is the task's title, which is what the user called it.
	Title string
	// Project is the project the task belongs to.
	Project string
}

// Notification is what a notifier delivers.
type Notification struct {
	// Title is the notification's heading.
	Title string
	// Body is its one line of text.
	Body string
}

// Notifier delivers a notification to the user's desktop.
type Notifier interface {
	// Notify delivers one notification. A failure is worth logging and is never
	// worth failing a task for.
	Notify(ctx context.Context, notification Notification) error
	// Available reports whether this build can deliver on this platform, and says
	// why not when it cannot. A build that cannot deliver says so once rather
	// than failing every time it is asked.
	Available() (bool, string)
}

// phrases are what each condition is called, in the user's terms.
//
// They are fixed strings rather than anything composed from what happened,
// because the alternative is a sentence assembled from a tool's output, and a
// tool's output is where a path, a command, or a value from somebody's
// environment would come from.
var phrases = map[Condition]string{
	ConditionIdle:                "the agent has been idle",
	ConditionReviewRequested:     "the agent asked for review",
	ConditionReadyForReview:      "ready for review",
	ConditionVerificationFailed:  "its checks failed",
	ConditionVerificationBlocked: "its checks could not run",
	ConditionTaskFailed:          "the task failed",
	ConditionSessionFailed:       "the agent session failed",
	ConditionRuntimeFailed:       "its application services failed",
}

// maxTitle bounds how much of a task's title reaches a notification.
//
// A desktop notification is one line, and a title longer than this would push
// the part that says what happened off the end of it.
const maxTitle = 60

// Compose renders a notification, and reports false for a condition this build
// does not notify about.
//
// The idle duration is the only measurement that reaches the text, and it is
// Feat's own: how long the task has been idle, rounded to something a person
// would say out loud.
func Compose(condition Condition, subject Subject, idle time.Duration) (Notification, bool) {
	phrase, known := phrases[condition]
	if !known {
		return Notification{}, false
	}
	if condition == ConditionIdle {
		phrase += " for " + roundIdle(idle).String()
	}

	heading := "feat"
	if subject.Key != "" {
		heading += " · " + subject.Key
	}
	if subject.Project != "" {
		heading += " · " + subject.Project
	}

	body := phrase
	if title := truncate(subject.Title, maxTitle); title != "" {
		body += " — " + title
	}
	return Notification{Title: heading, Body: body}, true
}

// roundIdle renders a duration the way somebody would say it.
func roundIdle(idle time.Duration) time.Duration {
	switch {
	case idle < 0:
		return 0
	case idle < time.Minute:
		return idle.Round(time.Second)
	case idle < time.Hour:
		return idle.Round(time.Minute)
	default:
		return idle.Round(time.Hour)
	}
}

// truncate shortens a title that would not fit, marking that it was shortened.
func truncate(title string, limit int) string {
	title = strings.Join(strings.Fields(title), " ")
	runes := []rune(title)
	if len(runes) <= limit {
		return title
	}
	return strings.TrimRight(string(runes[:limit]), " ") + "…"
}
