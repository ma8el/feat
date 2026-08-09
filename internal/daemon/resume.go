package daemon

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/config"
	"github.com/ma8el/feat/internal/domain"
)

// Resume continues a task's recorded agent session in a fresh terminal.
//
// ADR-032 deferred this to the slice that owns recovery, and the reason it gave
// is the reason it is here rather than in the agent adapter: a dead agent pane
// is the same recovery question as a missing tmux window, a removed worktree,
// and a stopped Compose project, and answering it inside one adapter would set a
// policy for all of them.
//
// Three properties make it a recovery rather than a restart:
//
//   - nothing reaches it on its own. Reconciliation reports that a session can
//     be resumed and never resumes one, no workflow transition arrives here, and
//     no agent message does;
//   - it continues the provider session the task recorded, which slice 7
//     captured from the session-start event before the process could fail. A new
//     session would have lost the task's history, and would look identical from
//     the outside;
//   - a provider that cannot find the recorded session fails visibly. Measured
//     against Claude Code 2.1.220 in a real terminal: an unknown session
//     identifier exits 1 with a message rather than opening the interactive
//     picker (ADR-037 evidence 6).
//
// It does bring a devcontainer up, which is not FR-STATE-004's forbidden
// automatic restart: that rule is about recovery starting things by itself, and
// this is a user asking.
func (s *service) Resume(ctx context.Context, id domain.TaskID) (*domain.Task, error) {
	defer s.locks.lock(id)()

	task, err := s.Task(ctx, id)
	if err != nil {
		return nil, err
	}

	// A record claiming a live process is the one case where tmux has to be
	// asked, and it is asked rather than believed. Nothing watches tmux
	// continuously, so that record only becomes stopped when a reconciliation
	// pass runs or the provider's own end-of-session hook lands: a window killed
	// from tmux leaves it saying idle while there is nothing there, and refusing
	// on it answered "attach to it instead" for a terminal there was nothing to
	// attach to. That left the one task most in need of recovery unable to have
	// it. A tmux server that is not running discovers as empty rather than
	// failing, so a machine that rebooted arrives here with live false and
	// resumes (ADR-037).
	live := false
	if task.Session != nil && task.Session.Process.Alive() {
		_, found, err := s.terminals.Find(ctx, task.ProjectID, task.ID)
		if err != nil {
			// An unreadable tmux is not evidence of a dead terminal, and a resume
			// that assumed it was would start a second agent beside a live one.
			return nil, err
		}
		live = found
	}
	if err := resumable(task, live); err != nil {
		return nil, err
	}
	// Passing with a record that still claims a live process means tmux answered
	// that the terminal is gone. It is corrected before anything acts on it: the
	// container comes up and the agent starts over the seconds that follow, and a
	// dashboard claiming idle throughout would be describing the session this
	// call is replacing.
	if task.Session.Process.Alive() {
		if err := s.markTerminalGone(ctx, task); err != nil {
			return nil, err
		}
	}

	cfg, err := config.Load(s.layout.ProjectConfigDir(), task.ProjectID.String(), s.configOptions())
	if err != nil {
		return nil, translateConfig(err)
	}

	// A failed task goes back to preparing, so it stops claiming a failure while
	// the container comes up and the agent starts; the launch that follows
	// leaves it failed again if it cannot finish, which is where it already was.
	//
	// A task whose workflow is still working is left alone. That is not an odd
	// case: a process that dies while no daemon is watching leaves the workflow
	// where it was, and reconciliation reports the dead process rather than
	// moving it — reporting instead of repairing is the whole rule. Transitioning
	// unconditionally refused those tasks outright, because working has no edge
	// to preparing and should not gain one. Found by resuming a real task whose
	// container had been killed a day earlier (ADR-037).
	restored := task.Workflow
	if task.Workflow == domain.WorkflowFailed {
		if err := s.transition(ctx, task, domain.WorkflowPreparing,
			"resuming the recorded "+task.Session.Provider+" session at the user's request"); err != nil {
			return nil, err
		}
	}

	plan, err := s.planResume(ctx, cfg, task)
	if err != nil {
		// Only a task this call moved is moved back. One that was already
		// working keeps saying so: its agent is dead either way, and a failed
		// resume is not new information about the work.
		if restored == domain.WorkflowFailed {
			if transitionErr := s.transition(ctx, task, domain.WorkflowFailed, err.Error()); transitionErr != nil {
				return nil, fmt.Errorf("%w (and the task could not be returned to failed: %w)", err, transitionErr)
			}
		}
		return nil, err
	}

	s.logger.InfoContext(ctx, "resuming a recorded agent session",
		slog.String("task", task.ID.String()),
		slog.String("provider", task.Session.Provider),
		slog.String("provider_session", task.Session.ProviderSessionID))

	return s.ensureTerminal(ctx, task, cfg, plan)
}

// resumable reports why a task cannot be resumed, in terms that name the remedy.
//
// The first two refusals are facts about the record and are decided from it
// alone. The third is a fact about the machine, so it takes the caller's
// observation of tmux: a record claiming a live process is refused only when
// there is a terminal to attach to instead.
func resumable(task *domain.Task, live bool) error {
	if task.Session == nil {
		return fmt.Errorf("%w: task %s has no agent session to resume. "+
			"Nothing was ever launched for it", api.ErrInvalid, task.ID)
	}
	if task.Session.ProviderSessionID == "" {
		return fmt.Errorf("%w: task %s recorded no %s session identifier, so there is nothing to continue. "+
			"Its agent never reported starting, and resuming would open an empty session that looked like the "+
			"old one", api.ErrInvalid, task.ID, task.Session.Provider)
	}
	if live && task.Session.Process.Alive() {
		return fmt.Errorf("%w: the agent session of task %s is %s in a terminal that is still there, "+
			"so there is nothing to resume. Attach to it instead", api.ErrInvalid, task.ID, task.Session.Process)
	}
	if task.Workflow == domain.WorkflowArchived {
		return fmt.Errorf("%w: task %s is archived", api.ErrInvalid, task.ID)
	}
	return nil
}

// planResume builds the launch that continues the recorded session.
//
// It is the ordinary launch path with one value added, so that everything a
// launch validates is validated again: the container is brought up and probed,
// the provider CLI is checked, and the generated files are rewritten. A resume
// that skipped those would be the one launch in Feat nobody checked.
func (s *service) planResume(ctx context.Context, cfg *config.Config, task *domain.Task) (launchPlan, error) {
	plan, err := s.planLaunchResuming(ctx, cfg, task, task.Session.ProviderSessionID)
	if err != nil {
		return launchPlan{}, err
	}
	// The task's pane already exists and holds the output of the session that
	// died, which is often the only account of why. It is reused rather than
	// replaced, and the program in it is what changes.
	plan.restart = true
	return plan, nil
}
