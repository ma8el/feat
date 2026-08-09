package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/config"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/store"
	"github.com/ma8el/feat/internal/tmux"
)

// PrepareTerminal creates or rediscovers the persistent terminal of a confirmed
// task, running a caller-supplied command in it.
//
// It is the seam ADR-030 left for the slices that decide what a task terminal
// runs. Launch itself no longer goes through it: what a launch runs is chosen by
// planLaunch, which knows whether an agent can start. What remains here is the
// terminal lifecycle, exercised directly by the tests that own it.
func (s *service) PrepareTerminal(ctx context.Context, ref store.TaskRef, command tmux.CommandSpec) (*domain.Task, error) {
	if err := ref.Validate(); err != nil {
		return nil, err
	}
	task, err := s.store.Tasks().Load(ctx, ref)
	if err != nil {
		return nil, translate(err, "no task "+ref.Task.String()+" in project "+ref.Project.String())
	}

	cfg, err := config.Load(s.layout.ProjectConfigDir(), ref.Project.String(), s.configOptions())
	if err != nil {
		return nil, translateConfig(err)
	}
	// A caller-supplied command is not an agent session: nothing will report a
	// session start for it, so the task must not be recorded as working.
	return s.ensureTerminal(ctx, task, cfg, launchPlan{
		command: command,
		mode:    domain.ExecutionMode(cfg.Agent.Execution.Mode),
	})
}

// ensureTerminal creates or rediscovers a confirmed task's terminal and records
// what it observed.
//
// It takes the task rather than loading one, because launch has already loaded
// it, transitioned it, and created its worktrees. Re-reading it here would put a
// second reader between the transition and the terminal it belongs to.
func (s *service) ensureTerminal(
	ctx context.Context, task *domain.Task, cfg *config.Config, plan launchPlan,
) (*domain.Task, error) {
	// A launch happens in preparing or after a failure. A resume additionally
	// reaches this for a task whose workflow never moved, because its process
	// died while no daemon was watching — the terminal is being restarted rather
	// than created, and refusing would leave the one task that most needs
	// recovery unable to have it (ADR-037).
	if !plan.restart && task.Workflow != domain.WorkflowPreparing && task.Workflow != domain.WorkflowFailed {
		return nil, fmt.Errorf("%w: task %s is %s, and its terminal is created only after confirmation",
			api.ErrInvalid, task.ID, task.Workflow)
	}
	if task.Workflow == domain.WorkflowDraft || task.Workflow == domain.WorkflowArchived {
		return nil, fmt.Errorf("%w: task %s is %s, and has no terminal to restart",
			api.ErrInvalid, task.ID, task.Workflow)
	}

	terminal, err := s.ensureTmux(ctx, task, plan)
	if err != nil {
		if task.Workflow == domain.WorkflowPreparing {
			if transitionErr := s.transition(ctx, task, domain.WorkflowFailed, err.Error()); transitionErr != nil {
				return nil, errors.Join(err, transitionErr)
			}
		}
		return task, err
	}

	from := domain.ProcessStarting
	if task.Session == nil {
		session, err := domain.NewAgentSession(
			cfg.Agent.Provider,
			plan.mode,
			terminal.Target,
			s.controlPath(task),
			s.now(),
		)
		if err != nil {
			return nil, err
		}
		// The environment the launch prepared, recorded on the session the
		// moment there is one to record it on. Until this point it exists and
		// the task cannot name it, which is the window ADR-029's ordering
		// narrows rather than closes.
		session.Execution = plan.environment
		if err := session.Observe(terminal.ProcessState(), s.now()); err != nil {
			return nil, err
		}
		if err := task.AttachSession(session, s.now()); err != nil {
			return nil, err
		}
	} else {
		from = task.Session.Process
		if err := task.Session.ReconcileTerminal(terminal.Target, terminal.ProcessState(), task.ID, s.now()); err != nil {
			return nil, err
		}
		task.Session.ControlPath = s.controlPath(task)
		if plan.environment != nil {
			task.Session.Execution = plan.environment
		}
	}

	if err := s.store.Tasks().Save(ctx, task); err != nil {
		// The tagged terminal is deliberately retained. Startup reconciliation
		// can recover it; killing it here could destroy work already entered.
		return nil, err
	}
	s.record(ctx, task, domain.Event{
		Type: domain.EventProcessChanged,
		From: string(from),
		To:   string(task.Session.Process),
		Detail: "tmux terminal " + terminal.Target.Session + "/" +
			terminal.Target.Window + "/" + terminal.Target.Pane + " is available",
	})
	if plan.note != "" {
		s.logger.InfoContext(ctx, "task terminal launched",
			slog.String("task", task.ID.String()), slog.String("note", plan.note))
	}

	// The task stays preparing until the agent says it started. Nothing here
	// claims a running agent: a shell is not one, and even a launched Claude has
	// not begun until its own session-start event arrives (ADR-031, ADR-032).
	if plan.agentStarted {
		// A session-start event follows within seconds, and a user watching the
		// dashboard should see the task reach working then rather than at
		// whatever point in the polling interval they happened to launch.
		s.nudge()
		// And if it does not follow, the task must say so rather than sit there
		// looking busy: an agent can be waiting for a person before it has
		// emitted anything Feat could have heard.
		s.armStartup(ctx, task)
	}
	return task, nil
}

// ensureTmux creates, finds, or restarts the task's terminal.
//
// A restart is the resume path and nothing else. It falls back to creating one
// when there is no terminal to restart, because a computer that rebooted took
// the tmux server with it and a resume then has to make the terminal as well as
// the session.
func (s *service) ensureTmux(
	ctx context.Context, task *domain.Task, plan launchPlan,
) (tmux.Terminal, error) {
	if !plan.restart {
		return s.terminals.EnsureTask(ctx, task.ProjectID, task.ID, plan.command)
	}
	if _, found, err := s.terminals.Find(ctx, task.ProjectID, task.ID); err == nil && found {
		return s.terminals.Restart(ctx, task.ProjectID, task.ID, plan.command)
	}
	return s.terminals.EnsureTask(ctx, task.ProjectID, task.ID, plan.command)
}

// OpenShell creates or finds the task's tagged shell pane.
//
// The daemon builds the command rather than accepting one: a program a caller
// chose would be a program the daemon runs on its owner's behalf, and the local
// API takes identifiers rather than things to execute
// (docs/05-security-model.md, local daemon API). The adapter still receives a
// resolved command, as ADR-030 requires; slice 8 replaces the host shell with
// one inside the execution environment.
func (s *service) OpenShell(ctx context.Context, id domain.TaskID) (api.AttachInfo, error) {
	task, err := s.Task(ctx, id)
	if err != nil {
		return api.AttachInfo{}, err
	}
	if task.Session == nil {
		return api.AttachInfo{}, fmt.Errorf("%w: task %s has no terminal to open a shell beside", api.ErrInvalid, id)
	}
	cfg, err := config.Load(s.layout.ProjectConfigDir(), task.ProjectID.String(), s.configOptions())
	if err != nil {
		return api.AttachInfo{}, translateConfig(err)
	}

	// A shell pane is a shell, whatever the agent pane is running, and it opens
	// in the same execution profile and primary workspace as the agent
	// (FR-TMUX-003). For a task whose agent is in a container that means a shell
	// in that container: a host shell beside a containerised agent would look
	// like the agent's own environment and be a different machine.
	command, err := s.taskShell(ctx, cfg, task)
	if err != nil {
		return api.AttachInfo{}, fmt.Errorf("%w: %w", api.ErrInvalid, err)
	}
	terminal, err := s.terminals.EnsureShell(ctx, task.ProjectID, task.ID, command)
	if err != nil {
		return api.AttachInfo{}, err
	}
	if terminal.Shell == nil {
		return api.AttachInfo{}, fmt.Errorf("the shell pane of task %s was created but not reported by tmux", id)
	}
	// As with the agent pane: a native client takes this window at its own size,
	// and sees every pane it has rather than the one the dashboard was showing.
	if err := s.terminals.UnzoomWindow(ctx, terminal.Target.Window); err != nil {
		return api.AttachInfo{}, err
	}
	if err := s.terminals.ReleaseWindowSize(ctx, terminal.Target.Window); err != nil {
		return api.AttachInfo{}, err
	}
	return api.AttachInfo{
		Socket:  terminal.Target.Socket,
		Session: terminal.Target.Session,
		Window:  terminal.Target.Window,
		Pane:    terminal.Shell.ID,
	}, nil
}

// AttachInfo returns a live, metadata-resolved native tmux target.
func (s *service) AttachInfo(ctx context.Context, id domain.TaskID) (api.AttachInfo, error) {
	task, err := s.Task(ctx, id)
	if err != nil {
		return api.AttachInfo{}, err
	}
	if task.Session == nil {
		return api.AttachInfo{}, fmt.Errorf("%w: task %s has no agent terminal", api.ErrNotFound, id)
	}

	terminal, found, err := s.terminals.Find(ctx, task.ProjectID, task.ID)
	if err != nil {
		return api.AttachInfo{}, err
	}
	if !found {
		return api.AttachInfo{}, fmt.Errorf("%w: task %s has no live tagged terminal on %s",
			api.ErrTerminalMissing, id, s.terminals.Socket())
	}

	// A native client is about to take this window over at its own size, so the
	// size the dashboard pinned for rendering is released first. Without this an
	// attach inherits the main region's dimensions and leaves the rest of the
	// terminal blank (ADR-042).
	if err := s.terminals.UnzoomWindow(ctx, terminal.Target.Window); err != nil {
		return api.AttachInfo{}, err
	}
	if err := s.terminals.ReleaseWindowSize(ctx, terminal.Target.Window); err != nil {
		return api.AttachInfo{}, err
	}

	if task.Session.Tmux != terminal.Target || task.Session.Process != terminal.ProcessState() {
		if err := task.Session.ReconcileTerminal(terminal.Target, terminal.ProcessState(), task.ID, s.now()); err != nil {
			return api.AttachInfo{}, err
		}
		if err := s.store.Tasks().Save(ctx, task); err != nil {
			return api.AttachInfo{}, err
		}
	}
	return api.AttachInfo{
		Socket:  terminal.Target.Socket,
		Session: terminal.Target.Session,
		Window:  terminal.Target.Window,
		Pane:    terminal.Target.Pane,
	}, nil
}

// TerminalFrame renders one view of a task's pane.
//
// The pane is sized to the region the caller will draw into before it is
// captured, because a program wraps its own output: a pane left at another size
// would come back wrapped at a column the display does not have, and no amount
// of care in the renderer would straighten it.
//
// Nothing here reads what the pane contains. Feat draws these bytes and derives
// no state from them, which is ADR-042's boundary and the reason this sits
// beside AttachInfo rather than anywhere near the agent adapter.
func (s *service) TerminalFrame(ctx context.Context, id domain.TaskID, view api.TerminalView) (api.TerminalFrame, error) {
	if err := view.Validate(); err != nil {
		return api.TerminalFrame{}, err
	}
	terminal, pane, err := s.terminalPane(ctx, id, view.Shell)
	if err != nil {
		return api.TerminalFrame{}, err
	}

	// A window somebody is attached to keeps the size their terminal gives it.
	// Sizing it here would shrink a native client's screen to the dashboard's
	// main region every time this poll came round, which is the same defect as
	// attaching to a pinned window and arrives by the other route. The frame then
	// comes back at the viewer's size and the renderer clips it: a real client
	// wins over a rendering of one.
	// One operation rather than three, because zoom is a toggle and two callers
	// racing on it cancel each other out — a poll and a keystroke arriving
	// together made the agent flicker between the region's width and half of it.
	//
	// A window somebody is attached to is neither sized nor zoomed: their client
	// owns it, and a rendering must not resize the terminal they are sitting in.
	captured, err := s.terminals.RenderPane(ctx, terminal.Target.Window, pane,
		view.Width, view.Height, !terminal.Watched())
	if err != nil {
		return api.TerminalFrame{}, err
	}
	return api.TerminalFrame{
		Width:  captured.Width,
		Height: captured.Height,
		Panes: []api.TerminalPane{{
			Pane:    captured.Pane,
			Width:   captured.Width,
			Height:  captured.Height,
			CursorX: captured.CursorX,
			CursorY: captured.CursorY,
			Content: captured.Content,
			Active:  true,
			Dead:    captured.Dead,
		}},
	}, nil
}

// SendTerminalInput delivers keys or typed text to a task's pane.
func (s *service) SendTerminalInput(ctx context.Context, id domain.TaskID, input api.TerminalInput) error {
	if err := input.Validate(); err != nil {
		return err
	}
	_, pane, err := s.terminalPane(ctx, id, input.Shell)
	if err != nil {
		return err
	}

	// Text first, then keys. A user who typed a line and pressed Enter sends
	// both in one request, and the Enter has to arrive after what it submits.
	if input.Text != "" {
		deliver := s.terminals.TypeText
		if input.Paste {
			deliver = s.terminals.PasteText
		}
		if err := deliver(ctx, pane, input.Text); err != nil {
			return err
		}
	}
	return s.terminals.SendKeys(ctx, pane, input.Keys...)
}

// terminalPane resolves a task to one of its live panes.
//
// The caller names a task and a role, never a pane. Resolving which pane belongs
// to a task is the daemon's, as it is for attachment: a client that could name a
// pane could name one belonging to another task, or one Feat does not own.
func (s *service) terminalPane(ctx context.Context, id domain.TaskID, shell bool) (tmux.Terminal, string, error) {
	task, err := s.Task(ctx, id)
	if err != nil {
		return tmux.Terminal{}, "", err
	}
	if task.Session == nil {
		return tmux.Terminal{}, "", fmt.Errorf("%w: task %s has no agent terminal", api.ErrNotFound, id)
	}

	terminal, found, err := s.terminals.Find(ctx, task.ProjectID, task.ID)
	if err != nil {
		return tmux.Terminal{}, "", err
	}
	if !found {
		return tmux.Terminal{}, "", fmt.Errorf("%w: task %s has no live tagged terminal on %s",
			api.ErrTerminalMissing, id, s.terminals.Socket())
	}

	if shell {
		if terminal.Shell == nil {
			return tmux.Terminal{}, "", fmt.Errorf("%w: task %s has not been given a shell pane yet",
				api.ErrShellMissing, id)
		}
		return terminal, terminal.Shell.ID, nil
	}
	return terminal, terminal.Target.Pane, nil
}
