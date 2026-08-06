package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ma8el/feat/internal/agent"
	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/config"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/tmux"
)

// launchPlan is what a confirmed task's terminal will run.
type launchPlan struct {
	// command is what the pane starts.
	command tmux.CommandSpec
	// agentStarted reports whether that command is an agent session. When it is
	// not, no session-start event will ever arrive, and the task stays
	// preparing rather than claiming to be working.
	agentStarted bool
	// mode is the execution mode to record on the session.
	mode domain.ExecutionMode
	// note explains an unusual launch: a shell where an agent was expected, or
	// an agent outside the boundary the project configured.
	note string
}

// planLaunch decides what a task's terminal runs and prepares whatever it needs.
//
// The decision is deliberately narrow. Devcontainer execution arrives with
// slice 8, so a project that configures a container gets a shell and a message
// naming that slice — unless the daemon itself was started with the host-agent
// opt-in, which is the only thing that can move an agent outside its configured
// boundary (ADR-032).
func (s *service) planLaunch(ctx context.Context, cfg *config.Config, task *domain.Task) (launchPlan, error) {
	directory, err := primaryWorktree(cfg, task)
	if err != nil {
		return launchPlan{}, err
	}

	configured := domain.ExecutionMode(cfg.Agent.Execution.Mode)
	switch {
	case cfg.Agent.Provider != config.ProviderClaude:
		// The configuration vocabulary allows one provider and validation
		// enforces it, so this is a guard rather than a branch anyone reaches.
		return launchPlan{}, fmt.Errorf("%w: no adapter for agent provider %q", api.ErrInvalid, cfg.Agent.Provider)

	case configured == domain.ExecutionHost:
		return s.planAgent(ctx, cfg, task, directory, domain.ExecutionHost, false)

	case s.hostAgent:
		return s.planAgent(ctx, cfg, task, directory, domain.ExecutionHost, true)

	default:
		command, err := s.shellCommand(cfg, task)
		if err != nil {
			return launchPlan{}, err
		}
		return launchPlan{
			command: command,
			mode:    configured,
			note: "this project runs its agent in a devcontainer, which implementation slice 8 delivers; " +
				"the task terminal holds a shell in the primary worktree until then, and no agent is running. " +
				"Set " + EnvHostAgent + "=1 in the daemon's environment to run Claude on this host instead",
		}, nil
	}
}

// planAgent validates the environment and prepares an agent launch.
//
// Validation comes first and creates nothing. A task whose agent could never
// start should not be given a terminal, a session record, and a workflow state
// that says an agent is running in it (acceptance criterion 6).
func (s *service) planAgent(
	ctx context.Context, cfg *config.Config, task *domain.Task,
	directory string, mode domain.ExecutionMode, outsideBoundary bool,
) (launchPlan, error) {
	env := agent.Environment{
		Mode:                      mode,
		OutsideConfiguredBoundary: outsideBoundary,
		Runner:                    s.runner,
		GitHubCLI:                 agent.CapabilityLevel(cfg.Agent.Capabilities.GitHubCLI),
		GitLabCLI:                 agent.CapabilityLevel(cfg.Agent.Capabilities.GitLabCLI),
	}
	if err := s.agent.Validate(ctx, env); err != nil {
		return launchPlan{}, fmt.Errorf("%w: task %s cannot start its agent: %w", api.ErrInvalid, task.ID, err)
	}

	workspace, err := s.controlWorkspace(task)
	if err != nil {
		return launchPlan{}, err
	}
	if err := workspace.Create(); err != nil {
		return launchPlan{}, err
	}

	// While execution is host-native, the agent sees the host's own paths. Slice
	// 8 fills the same structure with the container's, and nothing in the
	// adapter changes.
	spec, err := s.agent.Prepare(ctx, agent.PrepareRequest{
		Task: task,
		Workspace: agent.Workspace{
			WorkingDirectory: directory,
			ControlPath:      workspace.Root(),
		},
		Control:     workspace,
		Environment: env,
	})
	if err != nil {
		return launchPlan{}, err
	}

	command := tmux.CommandSpec{
		Program:   spec.Program,
		Arguments: spec.Arguments,
		Directory: spec.Directory,
	}
	if err := command.Validate(); err != nil {
		return launchPlan{}, err
	}

	plan := launchPlan{command: command, agentStarted: true, mode: mode}
	if outsideBoundary {
		plan.note = "this project configures a devcontainer, and " + EnvHostAgent +
			" is set, so Claude is running directly on this host with your own access rather than inside a container"
		s.logger.WarnContext(ctx, "launching an agent outside its configured boundary",
			slog.String("task", task.ID.String()),
			slog.String("configured_mode", cfg.Agent.Execution.Mode))
	}
	return plan, nil
}

// idleGrace is how long an ended turn waits before the session is called idle.
func (s *service) idleGrace(task *domain.Task) time.Duration {
	cfg, err := config.Load(s.layout.ProjectConfigDir(), task.ProjectID.String(), s.configOptions())
	if err != nil {
		return defaultIdleGrace
	}
	if grace := cfg.Agent.Claude.IdleGrace(); grace > 0 {
		return grace
	}
	return defaultIdleGrace
}

// defaultIdleGrace is used when a project does not configure one.
const defaultIdleGrace = 5 * time.Second

// controlPath returns the control workspace path recorded on a session.
func (s *service) controlPath(task *domain.Task) string {
	workspace, err := s.controlWorkspace(task)
	if err != nil {
		return ""
	}
	if !workspace.Exists() {
		return ""
	}
	return workspace.Root()
}
