package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/domain"
)

// Stop puts a task's agent to sleep and leaves everything else where it is.
//
// It is the inverse of Resume and the pair is the whole lifecycle a user drives:
// an agent environment comes into being with a launch, comes back with a resume,
// sleeps with this, and is removed by cleanup. There is deliberately no verb that
// starts one, because a container with no session behind it is the resource class
// Feat is least able to account for — every route to a running environment goes
// through the session that owns it (ADR-057).
//
// What it keeps is everything a resume needs and everything the work lives in:
// the worktrees, the branches, the control workspace, the volumes, and the tmux
// window, whose pane holds the output of the session that ran and is often the
// only account of what it did (ADR-030). Stopping is not a small cleanup, and
// nothing here asks for confirmation because nothing here is hard to undo.
//
// It does not touch the task's application services. A feature environment is a
// co-equal thing a task owns, with verbs of its own under `feat runtime`, and a
// user who stops an agent to free the machine overnight may well want the
// application they were testing to stay up.
func (s *service) Stop(ctx context.Context, id domain.TaskID) (*domain.Task, error) {
	// One budget for the whole action, so that the ceiling is a single number
	// both ends of the request know: the client waits for it, and this stops
	// waiting at it (api.AgentTimeout).
	ctx, cancel := context.WithTimeout(ctx, s.agentBudget())
	defer cancel()

	// One task's records are changed by one goroutine at a time (ADR-036).
	defer s.locks.lock(id)()

	task, err := s.Task(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := stoppable(task); err != nil {
		return nil, err
	}

	environment, err := s.environmentFor(task)
	if err != nil {
		return nil, err
	}
	state, err := environment.Stop(ctx)
	if err != nil {
		// Nothing is undone and nothing is retried. A Compose project that
		// half stopped is a state the next observation describes correctly,
		// and the record still names every container that may exist.
		return nil, fmt.Errorf("stopping the agent of task %s: %w", task.ID, err)
	}

	s.logger.InfoContext(ctx, "stopped a task's agent environment",
		slog.String("task", task.ID.String()),
		slog.String("environment", task.Session.Execution.Identity))

	return task, s.recordStopped(ctx, task, state)
}

// recordStopped writes down what a stop left behind.
//
// The process state is written by the act that intended it, which is what lets
// every later observation stay a pure observation: reconciliation treats an
// alive process against a container that is not running as a death nobody asked
// for, and this is how the one somebody did ask for is told apart from it
// without a desired-state field for anybody to disagree with (ADR-057).
func (s *service) recordStopped(ctx context.Context, task *domain.Task, state executionState) error {
	now := s.now()
	from := task.Session.Process
	task.Session.Execution.Observe(state.Container, state.Running, state.Status, state.Health, now)
	if err := task.Session.Observe(domain.ProcessStopped, now); err != nil {
		return err
	}

	// A task with no agent cannot be waiting for its user. Leaving the
	// attention state where it was would keep a stopped task in the band of
	// things asking to be looked at, which is how an attention state stops
	// meaning anything.
	attention := task.Attention
	if attention != domain.AttentionNone {
		if err := task.SetAttention(domain.AttentionNone, now); err != nil {
			return err
		}
	}

	if err := s.store.Tasks().Save(ctx, task); err != nil {
		return err
	}

	s.record(ctx, task, domain.Event{
		Type: domain.EventProcessChanged, From: string(from), To: string(domain.ProcessStopped),
		Detail: "the user stopped the task's agent environment; its worktrees, branches, " +
			"control workspace, volumes, and terminal were kept",
	})
	s.record(ctx, task, domain.Event{
		Type: domain.EventExecutionChanged, To: task.Session.Execution.Identity,
		Detail: "the agent containers were stopped and kept, and can be started again by resuming the task",
	})
	if attention != domain.AttentionNone {
		s.record(ctx, task, domain.Event{
			Type: domain.EventAttentionChanged, From: string(attention), To: string(domain.AttentionNone),
			Detail: "the task's agent was stopped, so nothing is waiting for an answer",
		})
	}
	// No notification. A stop is something the user just did, which is the rule
	// a stopped application runtime is already held to (notify.notifiableRuntime).
	return nil
}

// stoppable reports why a task's agent cannot be stopped, in terms that name the
// remedy.
//
// Every refusal is a fact about the record and is decided from it alone. There is
// no refusal for a task whose containers are already stopped: `docker compose
// stop` on a stopped project succeeds, and a user asking for a state the machine
// is already in should be told they have it rather than that they were wrong to
// ask (FR-CLEAN-001's rule for a resource that is already gone).
func stoppable(task *domain.Task) error {
	if task.Session == nil {
		return fmt.Errorf("%w: task %s has no agent session to stop. "+
			"Nothing was ever launched for it", api.ErrInvalid, task.ID)
	}
	if task.Session.Execution == nil {
		return fmt.Errorf("%w: the agent of task %s runs on this machine rather than in a container, "+
			"and Feat owns no process there to stop. Close its tmux pane to end the session, "+
			"or clean the task up", api.ErrInvalid, task.ID)
	}
	if task.Workflow == domain.WorkflowArchived {
		return fmt.Errorf("%w: task %s is archived", api.ErrInvalid, task.ID)
	}
	return nil
}

// agentBudget is how long one launch, resume, or stop may take.
func (s *service) agentBudget() time.Duration {
	if s.agentOverride > 0 {
		return s.agentOverride
	}
	return api.AgentTimeout
}
