package daemon

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/config"
	"github.com/ma8el/feat/internal/domain"
)

// Resume continues a task's recorded agent session in a fresh terminal. It lives
// here rather than in the agent adapter, because a dead agent pane is the same
// recovery question as a missing tmux window, a removed worktree, or a stopped
// Compose project (ADR-032).
//
// Three properties make it a recovery rather than a restart:
//
//   - nothing reaches it on its own. Reconciliation reports that a session can
//     be resumed and never resumes one, and no workflow transition or agent
//     message arrives here;
//   - it continues the provider session the task recorded, captured from the
//     session-start event before the process could fail. A new session would
//     have lost the task's history and looked identical from the outside;
//   - a provider that cannot find the recorded session fails visibly. Claude
//     Code 2.1.220 exits 1 with a message on an unknown session identifier
//     rather than opening the interactive picker (ADR-037 evidence 6).
//
// It does bring a devcontainer up, which is not FR-STATE-004's forbidden
// automatic restart: that rule is about recovery starting things by itself, and
// this is a user asking.
func (s *service) Resume(ctx context.Context, id domain.TaskID) (*domain.Task, error) {
	// A resume brings a container up, which is not work that finishes inside an
	// ordinary request's budget. One number bounds it at both ends
	// (api.AgentTimeout).
	ctx, cancel := context.WithTimeout(ctx, s.agentBudget())
	defer cancel()

	defer s.locks.lock(id)()

	task, err := s.Task(ctx, id)
	if err != nil {
		return nil, err
	}

	// A record claiming a live process is asked about rather than believed.
	// Nothing watches tmux continuously, so a window killed from tmux leaves the
	// record saying idle, and refusing on it would send the user to attach to a
	// terminal that is not there. A tmux server that is not running discovers as
	// empty rather than failing, so a machine that rebooted resumes (ADR-037).
	live, reason, err := s.attachable(ctx, task)
	if err != nil {
		return nil, err
	}
	if err := resumable(task, live); err != nil {
		return nil, err
	}
	// The machine answered that nothing is there, so the record is corrected
	// before anything acts on it. The container and the agent take seconds to come
	// up, and a dashboard claiming idle meanwhile would describe the session this
	// call is replacing.
	if task.Session.Process.Alive() {
		if err := s.markSessionEnded(ctx, task, reason); err != nil {
			return nil, err
		}
	}

	cfg, err := config.Load(s.layout.ProjectConfigDir(), task.ProjectID.String(), s.configOptions())
	if err != nil {
		return nil, translateConfig(err)
	}

	// A failed task goes back to preparing, so it stops claiming a failure while
	// the container comes up; the launch that follows leaves it failed again if it
	// cannot finish.
	//
	// A task still recorded as working is left alone. A process that dies while no
	// daemon is watching leaves the workflow where it was, and reconciliation
	// reports the dead process rather than moving it. Working has no edge to
	// preparing, so transitioning unconditionally refused those tasks (ADR-037).
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

// attachable reports whether a recorded session is something a user could attach
// to instead of resuming, and why it is not when it is not.
//
// Three things have to hold and the record establishes none of them. The window
// has to be there, because one killed from tmux leaves the record saying idle.
// The agent pane has to be alive, because Feat sets remain-on-exit on every pane
// it creates, so tmux still reports a pane whose program has exited (ADR-030).
// The environment has to be running, because the pane's process is on the host
// side of the container and outlives it (ADR-057).
//
// A record that does not claim a live process is not asked about at all: there is
// nothing to contradict, and a task already known to be stopped should not spend
// a container command establishing it twice.
func (s *service) attachable(ctx context.Context, task *domain.Task) (bool, string, error) {
	if task.Session == nil || !task.Session.Process.Alive() {
		return false, "", nil
	}

	terminal, found, err := s.terminals.Find(ctx, task.ProjectID, task.ID)
	if err != nil {
		// An unreadable tmux is not evidence of a dead terminal, and a resume
		// that assumed it was would start a second agent beside a live one.
		return false, "", err
	}
	if !found {
		return false, terminalGone, nil
	}
	if !terminal.ProcessState().Alive() {
		return false, "the agent in the recorded tmux pane had already exited; " +
			"the pane it left behind was reused rather than replaced", nil
	}

	// A host-native session records no environment, and the host is not
	// something that can be missing from itself.
	if task.Session.Execution == nil {
		return true, "", nil
	}
	environment, err := s.environmentFor(task)
	if err != nil {
		return false, "", fmt.Errorf("%w: the agent environment of task %s could not be resolved: %w",
			api.ErrInvalid, task.ID, err)
	}
	state, err := environment.Observe(ctx)
	if err != nil {
		// The rule tmux gets above: not being able to look says nothing about what
		// is there.
		return false, "", fmt.Errorf("the agent environment of task %s could not be observed: %w", task.ID, err)
	}
	if state.Running {
		return true, "", nil
	}
	return false, "the agent container was " + describeStatus(state) +
		", so the session running inside it had already ended", nil
}

// resumable reports why a task cannot be resumed, in terms that name the remedy.
// The first two refusals are decided from the record alone. The third takes the
// caller's observation of the machine: a record claiming a live process is
// refused only when there is a terminal to attach to instead.
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

// planResume builds the launch that continues the recorded session. It is the
// ordinary launch path with one value added, so everything a launch validates is
// validated again: the container comes up and is probed, the provider CLI is
// checked, and the generated files are rewritten.
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
