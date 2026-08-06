package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/ma8el/feat/internal/agent"
	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/control"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/store"
)

// effect is what one normalized agent event does to a task.
//
// The table below is the whole mapping from what a provider reported to what
// Feat records, and it is written as data so that it can be read and pinned
// rather than inferred from control flow. The rule that matters most is visible
// as an absence: no row sets a workflow state from KindTurnEnded. That is the
// shape a "Stop means complete" defect would take, and it would have to be
// introduced by editing the table that documents it (invariant 13,
// FR-AGENT-008).
type effect struct {
	// process is the process state to record, or empty to leave it alone.
	process domain.ProcessState
	// attention is the attention state to record, or empty to leave it alone.
	attention domain.AttentionState
	// arms reports that the event starts the idle grace period rather than
	// changing the process state now.
	arms bool
	// cancels reports that the event is activity, so a pending idle grace no
	// longer applies.
	cancels bool
	// workflow computes the workflow transition, or returns an empty state when
	// the event causes none. It is a function rather than a value because the
	// answer depends on where the task already is.
	workflow func(current domain.WorkflowState) domain.WorkflowState
}

// effects maps each normalized event kind onto what it changes.
var effects = map[agent.EventKind]effect{
	// A session that genuinely started is what takes a confirmed task to
	// working: before it, the task was preparing and no agent was running.
	agent.KindSessionStarted: {
		process: domain.ProcessRunning, attention: domain.AttentionNone, cancels: true,
		workflow: func(current domain.WorkflowState) domain.WorkflowState {
			if current == domain.WorkflowPreparing {
				return domain.WorkflowWorking
			}
			return ""
		},
	},

	// A prompt is activity, and in a review state it is the conservative
	// revision signal FR-AGENT-009 asks for: the user said something, so the
	// task is being worked on again until the agent asks for review afresh.
	agent.KindPromptSubmitted: {
		process: domain.ProcessRunning, attention: domain.AttentionNone, cancels: true,
		workflow: func(current domain.WorkflowState) domain.WorkflowState {
			switch current {
			case domain.WorkflowReviewRequested, domain.WorkflowReadyForReview,
				domain.WorkflowVerificationFailed, domain.WorkflowChangesRequested:
				return domain.WorkflowWorking
			default:
				return ""
			}
		},
	},

	// The end of a turn. It arms the grace period and touches neither the
	// workflow nor the process state: what it means is decided when the grace
	// period expires, and even then it means only idle.
	//
	// It does clear attention, because a turn cannot end while the agent is
	// blocked on a permission dialog: reaching the end of a turn is evidence
	// that whatever it was waiting for has been answered. Without this a task
	// that once asked for permission would report needing the user for the rest
	// of its life, and an attention state that never clears is one nobody reads.
	// The idle grace that follows then sets the conservative possibly-waiting.
	agent.KindTurnEnded: {attention: domain.AttentionNone, arms: true},

	// The provider asked for the user. This is the one signal that
	// distinguishes a session waiting on a person from one that merely finished
	// speaking, which is why it is worth installing a hook for.
	agent.KindNotification: {attention: domain.AttentionNeedsInput, cancels: true},

	agent.KindOpenQuestion: {attention: domain.AttentionNeedsInput, cancels: true},

	agent.KindSessionEnded: {
		process: domain.ProcessStopped, attention: domain.AttentionNone, cancels: true,
	},

	agent.KindSessionFailed: {
		process: domain.ProcessFailed, attention: domain.AttentionNeedsInput, cancels: true,
		workflow: func(current domain.WorkflowState) domain.WorkflowState {
			// A session that failed before it ever reported working leaves a
			// task that would otherwise rest in preparing for ever.
			if current == domain.WorkflowPreparing || current == domain.WorkflowWorking {
				return domain.WorkflowFailed
			}
			return ""
		},
	},

	// The explicit semantic event. It is the only entry in this table that
	// reaches a review state, and it exists only because an agent authored a
	// message saying so.
	agent.KindReviewRequested: {
		attention: domain.AttentionPossiblyWaiting, cancels: true,
		workflow: func(current domain.WorkflowState) domain.WorkflowState {
			if current == domain.WorkflowWorking {
				return domain.WorkflowReviewRequested
			}
			return ""
		},
	},

	// A completion report records what the agent claims without asserting that
	// the work is ready. An agent that wants review asks for it.
	agent.KindCompletionReport: {cancels: true},
}

// applyAgentEvent records one normalized agent event against a task.
//
// It is the only place a provider signal changes task state. Everything it
// applies comes from the table above, so the mapping is one thing to read
// rather than a path through several methods.
func (s *service) applyAgentEvent(ctx context.Context, task *domain.Task, event agent.Event) error {
	if task.Session == nil {
		return fmt.Errorf("task %s has no agent session to record a %s event against", task.ID, event.Kind)
	}
	change, known := effects[event.Kind]
	if !known {
		return fmt.Errorf("no effect is defined for agent event %q", event.Kind)
	}

	now := s.now()
	if event.ProviderSessionID != "" && task.Session.ProviderSessionID != event.ProviderSessionID {
		// Recorded as soon as it is known, and before anything can fail, so the
		// resume slice 12 offers can continue this session rather than open an
		// empty one (ADR-032).
		task.Session.ProviderSessionID = event.ProviderSessionID
	}

	// Any event at all means the agent is alive and talking, so it is no longer
	// silent since launch.
	s.startup.cancel(task.ID)

	// A session start that resumed, cleared, or compacted is the same session
	// carrying on. Letting it re-run the launch transition would mean a user
	// typing /clear moved their task's workflow.
	if event.Kind == agent.KindSessionStarted && event.Continued {
		change.workflow = nil
	}
	// An event that says the user is needed outranks the table's default, and
	// one that does not must never clear a wait that is still real.
	if event.NeedsInput {
		change.attention = domain.AttentionNeedsInput
	}

	from := task.Session.Process
	if change.process != "" && change.process != from {
		if err := task.Session.Observe(change.process, now); err != nil {
			return err
		}
		s.record(ctx, task, domain.Event{
			Type: domain.EventProcessChanged, From: string(from), To: string(change.process),
			Detail: event.Summary,
		})
	} else {
		task.Session.LastActivityAt = now
	}

	if change.attention != "" && change.attention != task.Attention {
		previous := task.Attention
		if err := task.SetAttention(change.attention, now); err != nil {
			return err
		}
		s.record(ctx, task, domain.Event{
			Type: domain.EventAttentionChanged, From: string(previous), To: string(change.attention),
			Detail: event.Summary,
		})
	}

	if change.workflow != nil {
		if next := change.workflow(task.Workflow); next != "" && next != task.Workflow {
			if err := s.transition(ctx, task, next, event.Summary); err != nil {
				return err
			}
		}
	}

	if event.Kind == agent.KindReviewRequested || event.Kind == agent.KindCompletionReport {
		if err := s.recordReport(ctx, task, event); err != nil {
			return err
		}
	}

	if err := s.store.Tasks().Save(ctx, task); err != nil {
		return err
	}

	switch {
	case change.arms:
		s.armIdle(ctx, task, event.OccurredAt)
	case change.cancels:
		s.cancelIdle(task.ID)
	}
	return nil
}

// recordReport records what the agent said about its own work.
//
// It goes into the review aggregate, where docs/03-domain-model.md already puts
// an agent-reported completion summary and agent-reported checks, and every
// check is attributed to the agent rather than to anything that enforced it. A
// dashboard that showed a claimed result and an enforced one alike would tell
// the user something Feat does not know.
func (s *service) recordReport(ctx context.Context, task *domain.Task, event agent.Event) error {
	ref := store.Ref(task)
	review, err := s.store.Reviews().Load(ctx, ref)
	if errors.Is(err, store.ErrNotFound) {
		review, err = domain.NewReview(task.ID, s.now())
	}
	if err != nil {
		return err
	}

	if err := review.RecordRequest(event.Summary, event.AgentReported(), s.now()); err != nil {
		return err
	}
	if err := s.store.Reviews().Save(ctx, ref, review); err != nil {
		return err
	}
	s.record(ctx, task, domain.Event{
		Type:   domain.EventReviewChanged,
		To:     string(review.Status),
		Detail: describeReport(event),
	})
	return nil
}

// Verification returns what the agent reported about its own checks.
//
// It reads the review aggregate rather than the task, because that is where
// docs/03-domain-model.md puts an agent-reported completion summary and
// agent-reported checks. A task with no review has reported nothing, which is
// absence rather than failure.
func (s *service) Verification(ctx context.Context, id domain.TaskID) (api.Verification, bool, error) {
	task, err := s.Task(ctx, id)
	if err != nil {
		return api.Verification{}, false, err
	}

	review, err := s.store.Reviews().Load(ctx, store.Ref(task))
	if errors.Is(err, store.ErrNotFound) {
		return api.Verification{}, false, nil
	}
	if err != nil {
		return api.Verification{}, false, err
	}
	verification, ok := api.NewVerification(review)
	return verification, ok, nil
}

// describeReport renders what the agent claimed, marked as a claim.
func describeReport(event agent.Event) string {
	if len(event.Checks) == 0 {
		return "the agent reported its work and ran no checks"
	}
	var passed, failed, other int
	for _, check := range event.Checks {
		switch check.Status {
		case domain.CheckPassed:
			passed++
		case domain.CheckFailed:
			failed++
		default:
			other++
		}
	}
	return fmt.Sprintf("the agent reports %d passed, %d failed, and %d other check results",
		passed, failed, other)
}

// deliverControl applies whatever the task's control workspace is holding.
//
// A message is validated by the protocol, normalized by the provider adapter,
// and only then allowed to change anything. A message that fails any of those
// steps is recorded as seen and refused, so it is neither applied nor offered
// again: an agent that wrote a malformed document should be told once rather
// than have Feat retry it for ever.
func (s *service) deliverControl(ctx context.Context, task *domain.Task) error {
	workspace, err := s.controlWorkspace(task)
	if err != nil {
		return err
	}
	messages, rejected, err := workspace.Pending()
	if err != nil {
		return err
	}

	for _, rejection := range rejected {
		s.logger.WarnContext(ctx, "refusing a control message",
			slog.String("task", task.ID.String()), slog.Any("error", rejection))
	}
	if len(messages) == 0 {
		return nil
	}

	var failures []error
	for _, message := range messages {
		event, applicable, err := s.agent.ParseEvent(ctx, message)
		if err != nil {
			// The provider could not read its own message. That is the agent's
			// mistake, so it is recorded and not retried.
			s.logger.WarnContext(ctx, "refusing a control message",
				slog.String("task", task.ID.String()), slog.Any("error", err))
			failures = append(failures, s.settle(ctx, workspace, message, control.OutcomeRejected, err.Error()))
			continue
		}
		if !applicable {
			failures = append(failures,
				s.settle(ctx, workspace, message, control.OutcomeInert, "this build models no state change for it"))
			continue
		}
		if err := s.applyAgentEvent(ctx, task, event); err != nil {
			// Applying failed for a reason that is Feat's rather than the
			// agent's, so the message is deliberately not marked processed: a
			// transient failure should be retried on the next poll.
			failures = append(failures, fmt.Errorf("applying %s for task %s: %w", event.Kind, task.ID, err))
			continue
		}
		failures = append(failures, s.settle(ctx, workspace, message, control.OutcomeApplied, ""))
	}
	return errors.Join(failures...)
}

// settle records that a message has been dealt with, so it is never applied
// twice.
func (s *service) settle(
	ctx context.Context, workspace *control.Workspace, message control.Message, outcome, reason string,
) error {
	if err := workspace.MarkProcessed(message, outcome, reason); err != nil {
		return fmt.Errorf("recording control message %s as %s: %w", message.ID, outcome, err)
	}
	if outcome != control.OutcomeApplied {
		s.logger.InfoContext(ctx, "control message settled without changing state",
			slog.String("message", message.ID), slog.String("outcome", outcome), slog.String("reason", reason))
	}
	return nil
}

// armIdle starts the grace period after which an ended turn becomes idle.
//
// FR-AGENT-007 asks for a short configurable delay before an end-of-turn signal
// becomes idle, because a turn that ends and immediately continues is not a
// session waiting for anybody. The delay is measured from when the provider
// reported the turn ending rather than from now, so a message that arrived
// while the daemon was down does not restart the clock.
func (s *service) armIdle(ctx context.Context, task *domain.Task, ended time.Time) {
	grace := s.idleGrace(task)
	remaining := grace - s.now().Sub(ended)
	if remaining < 0 {
		remaining = 0
	}

	id := task.ID
	s.idle.arm(id, remaining, func() {
		// A fresh context: the one that delivered the message is finished by the
		// time this runs.
		s.becomeIdle(context.WithoutCancel(ctx), id)
	})
}

// cancelIdle drops a pending idle transition because the session did something.
func (s *service) cancelIdle(id domain.TaskID) { s.idle.cancel(id) }

// armStartup starts the period after which an agent that has not reported
// starting is treated as needing the user.
//
// An agent can be blocked before it emits anything at all. Claude asks for
// workspace trust on a directory it has not seen before, and every task worktree
// is a directory it has not seen before, so the first thing a launched session
// does is wait for a person. No hook fires while it waits, because hooks are
// installed for a session that has begun.
//
// Feat therefore cannot learn this from the provider, and the honest thing is to
// say what it does know: nothing has been heard. A task that sits in `preparing`
// reporting a running process and no attention would look like a task getting on
// with its work, which is the one thing this slice exists to prevent.
func (s *service) armStartup(ctx context.Context, task *domain.Task) {
	id := task.ID
	s.startup.arm(id, startupGrace, func() {
		s.reportSilentStart(context.WithoutCancel(ctx), id)
	})
}

// startupGrace is how long a launched agent has to report that it started.
//
// It is not configurable. It is not a policy the user should have to tune: it
// bounds how long Feat will show a task as launched while having heard nothing,
// and any value that is comfortably longer than an agent's start-up is as good
// as any other.
const startupGrace = 30 * time.Second

// reportSilentStart records that a launched agent has not reported starting.
func (s *service) reportSilentStart(ctx context.Context, id domain.TaskID) {
	task, err := s.Task(ctx, id)
	if err != nil {
		s.logger.WarnContext(ctx, "a silent agent could not be loaded",
			slog.String("task", id.String()), slog.Any("error", err))
		return
	}
	// Anything at all from the session means this no longer applies.
	if task.Workflow != domain.WorkflowPreparing || task.Session == nil {
		return
	}

	// Possibly waiting rather than needs-input: the provider reported nothing,
	// so Feat knows it has not heard from the agent and does not know that the
	// agent is blocked. Claiming the stronger state would be inventing a report
	// nobody made (docs/03-domain-model.md).
	if task.Attention == domain.AttentionNone {
		if err := task.SetAttention(domain.AttentionPossiblyWaiting, s.now()); err != nil {
			s.logger.ErrorContext(ctx, "recording a silent agent", slog.Any("error", err))
			return
		}
		if err := s.store.Tasks().Save(ctx, task); err != nil {
			s.logger.ErrorContext(ctx, "saving a silent agent", slog.Any("error", err))
			return
		}
	}
	s.record(ctx, task, domain.Event{
		Type: domain.EventAttentionChanged,
		From: string(domain.AttentionNone), To: string(task.Attention),
		Detail: "the agent has not reported starting within " + startupGrace.String() +
			"; attach to the task to see what its terminal is waiting for",
	})
}

// becomeIdle records that a session has been quiet for the grace period.
//
// Idle is a process observation and nothing more. It does not touch the
// workflow, and the attention it sets is the conservative one: Feat cannot tell
// a finished turn from a question, so it says it may be waiting rather than
// that it is (docs/03-domain-model.md).
func (s *service) becomeIdle(ctx context.Context, id domain.TaskID) {
	task, err := s.Task(ctx, id)
	if err != nil {
		s.logger.WarnContext(ctx, "a task went idle and could not be loaded",
			slog.String("task", id.String()), slog.Any("error", err))
		return
	}
	if task.Session == nil || task.Session.Process != domain.ProcessRunning {
		return
	}

	now := s.now()
	if err := task.Session.Observe(domain.ProcessIdle, now); err != nil {
		s.logger.ErrorContext(ctx, "recording an idle session", slog.Any("error", err))
		return
	}
	if task.Attention == domain.AttentionNone {
		if err := task.SetAttention(domain.AttentionPossiblyWaiting, now); err != nil {
			s.logger.ErrorContext(ctx, "recording idle attention", slog.Any("error", err))
			return
		}
	}
	if err := s.store.Tasks().Save(ctx, task); err != nil {
		s.logger.ErrorContext(ctx, "saving an idle session", slog.Any("error", err))
		return
	}
	s.record(ctx, task, domain.Event{
		Type: domain.EventProcessChanged, From: string(domain.ProcessRunning), To: string(domain.ProcessIdle),
		Detail: "the agent has been quiet for the idle grace period; idle does not mean the task is complete",
	})
}
