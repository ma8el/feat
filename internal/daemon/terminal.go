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

// PrepareTerminal creates or rediscovers the persistent terminal of a
// confirmed task. The final command comes from an execution-environment or
// agent adapter; until those arrive it is the task shell that launch supplies
// (ADR-030, ADR-031).
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
	return s.ensureTerminal(ctx, task, cfg, command)
}

// ensureTerminal creates or rediscovers a confirmed task's terminal and records
// what it observed.
//
// It takes the task rather than loading one, because launch has already loaded
// it, transitioned it, and created its worktrees. Re-reading it here would put a
// second reader between the transition and the terminal it belongs to.
func (s *service) ensureTerminal(
	ctx context.Context, task *domain.Task, cfg *config.Config, command tmux.CommandSpec,
) (*domain.Task, error) {
	if task.Workflow != domain.WorkflowPreparing && task.Workflow != domain.WorkflowFailed {
		return nil, fmt.Errorf("%w: task %s is %s, and its terminal is created only after confirmation",
			api.ErrInvalid, task.ID, task.Workflow)
	}

	terminal, err := s.terminals.EnsureTask(ctx, task.ProjectID, task.ID, command)
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
			domain.ExecutionMode(cfg.Agent.Execution.Mode),
			terminal.Target,
			"",
			s.now(),
		)
		if err != nil {
			return nil, err
		}
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
	return task, nil
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

	command, err := s.agentCommand(cfg, task)
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
			api.ErrNotFound, id, s.terminals.Socket())
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

// reconcileTmux compares stored task sessions with tagged objects after a
// daemon restart. It owns only the tmux part of startup recovery; slice 12
// combines every resource class into one recovery workflow.
func (s *service) reconcileTmux(ctx context.Context) error {
	projects, err := s.store.Projects().List(ctx)
	if err != nil {
		return err
	}

	var tasks []*domain.Task
	for _, project := range projects {
		owned, err := s.store.Tasks().List(ctx, project.ID)
		if err != nil {
			return err
		}
		for _, task := range owned {
			if task.Session != nil || task.Workflow == domain.WorkflowPreparing || task.Workflow == domain.WorkflowFailed {
				tasks = append(tasks, task)
			}
		}
	}
	observed, err := s.terminals.Discover(ctx)
	if err != nil {
		return err
	}
	targets := make(map[domain.TaskID]tmux.Terminal, len(observed))
	for _, terminal := range observed {
		if previous, exists := targets[terminal.Task]; exists {
			return fmt.Errorf("tmux terminals %s and %s both claim task %s",
				previous.Target.Window, terminal.Target.Window, terminal.Task)
		}
		targets[terminal.Task] = terminal
	}

	known := make(map[domain.TaskID]bool, len(tasks))
	var failures []error
	for _, task := range tasks {
		known[task.ID] = true
		terminal, found := targets[task.ID]
		if found && terminal.Project != task.ProjectID {
			failures = append(failures, fmt.Errorf("tmux terminal for task %s claims project %s, recorded task belongs to %s",
				task.ID, terminal.Project, task.ProjectID))
			continue
		}

		if !found {
			if task.Session == nil {
				continue
			}
			from := task.Session.Process
			if err := task.Session.Observe(domain.ProcessStopped, s.now()); err != nil {
				failures = append(failures, err)
				continue
			}
			if err := s.store.Tasks().Save(ctx, task); err != nil {
				failures = append(failures, err)
				continue
			}
			s.record(ctx, task, domain.Event{
				Type: domain.EventReconciled, From: string(from), To: string(domain.ProcessStopped),
				Detail: "the recorded tmux terminal was not found; it was not restarted",
			})
			continue
		}

		from := domain.ProcessStarting
		if task.Session == nil {
			cfg, err := config.Load(s.layout.ProjectConfigDir(), task.ProjectID.String(), s.configOptions())
			if err != nil {
				failures = append(failures, err)
				continue
			}
			session, err := domain.NewAgentSession(
				cfg.Agent.Provider,
				domain.ExecutionMode(cfg.Agent.Execution.Mode),
				terminal.Target,
				"",
				s.now(),
			)
			if err != nil {
				failures = append(failures, err)
				continue
			}
			if err := task.AttachSession(session, s.now()); err != nil {
				failures = append(failures, err)
				continue
			}
		} else {
			from = task.Session.Process
		}
		if err := task.Session.ReconcileTerminal(terminal.Target, terminal.ProcessState(), task.ID, s.now()); err != nil {
			failures = append(failures, err)
			continue
		}
		if err := s.store.Tasks().Save(ctx, task); err != nil {
			failures = append(failures, err)
			continue
		}
		s.record(ctx, task, domain.Event{
			Type: domain.EventReconciled, From: string(from), To: string(task.Session.Process),
			Detail: "rediscovered tagged tmux terminal " + terminal.Target.Session + "/" +
				terminal.Target.Window + "/" + terminal.Target.Pane,
		})
	}

	for _, terminal := range observed {
		if known[terminal.Task] {
			continue
		}
		s.logger.WarnContext(ctx, "found a tagged tmux terminal with no recorded task",
			slog.String("project", terminal.Project.String()),
			slog.String("task", terminal.Task.String()),
			slog.String("window", terminal.Target.Window))
	}
	return errors.Join(failures...)
}
