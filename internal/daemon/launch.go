package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/ma8el/feat/internal/agent"
	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/config"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/execution"
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
	// environment is the execution environment the session runs in, or nil for
	// host execution. It is recorded on the session so that cleanup and
	// reconciliation resolve what the task owns rather than recomputing it.
	environment *domain.ExecutionEnvironment
	// note explains an unusual launch: a shell where an agent was expected, an
	// agent outside the boundary the project configured, or what the generated
	// override changed about the project's own Compose service.
	note string
}

// planLaunch decides what a task's terminal runs and prepares whatever it needs.
//
// A project that configures a container gets one, unless the daemon itself was
// started with the host-agent opt-in — the only thing that can move an agent
// outside its configured boundary, and never a request field (ADR-032).
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
		return s.planContainerAgent(ctx, cfg, task)
	}
}

// planContainerAgent starts the task's devcontainer and prepares an agent
// launch inside it.
//
// The order is start, validate, prepare, and it differs from host execution on
// purpose: every question worth asking is about the container, so the container
// has to exist before any of them can be answered. ADR-033 records that this
// amends ADR-032's "validation creates nothing" for this mode alone, and what it
// creates is the environment the validation is about.
func (s *service) planContainerAgent(
	ctx context.Context, cfg *config.Config, task *domain.Task,
) (launchPlan, error) {
	workspace, err := s.controlWorkspace(task)
	if err != nil {
		return launchPlan{}, err
	}
	if err := workspace.Create(); err != nil {
		return launchPlan{}, err
	}

	spec, err := s.executionSpec(cfg, task, workspace)
	if err != nil {
		return launchPlan{}, fmt.Errorf("%w: task %s cannot start its devcontainer: %w", api.ErrInvalid, task.ID, err)
	}
	environment, err := s.environments(spec)
	if err != nil {
		return launchPlan{}, fmt.Errorf("%w: %w", api.ErrInvalid, err)
	}
	if err := environment.Validate(ctx); err != nil {
		return launchPlan{}, fmt.Errorf("%w: task %s cannot start its devcontainer: %w", api.ErrInvalid, task.ID, err)
	}

	record := &domain.ExecutionEnvironment{
		Provider:              executionProvider,
		Identity:              spec.Identity,
		Files:                 spec.Files,
		GeneratedOverridePath: spec.OverridePath,
		Service:               spec.Service,
		User:                  spec.User,
	}
	// Recorded before it exists, so an interruption leaves a record naming a
	// superset of what exists, which is ADR-029's ordering applied to
	// containers rather than to worktrees.
	if err := s.recordEnvironment(ctx, task, record,
		"the task will run its agent in service "+spec.Service+" of Compose project "+spec.Identity+
			", which is about to be started"); err != nil {
		return launchPlan{}, err
	}

	if err := environment.Prepare(ctx); err != nil {
		return launchPlan{}, fmt.Errorf("%w: %w", api.ErrInvalid, err)
	}
	state, err := environment.Observe(ctx)
	if err != nil {
		return launchPlan{}, err
	}
	record.Observe(state.Container, state.Running, state.Status, state.Health, s.now())
	if err := s.recordEnvironment(ctx, task, record, "container "+state.Container+" is "+state.Status); err != nil {
		return launchPlan{}, err
	}

	// What the container turned out to be, rather than what it was asked to be.
	report, err := environment.Inspect(ctx, []string{
		cfg.Agent.Execution.ControlPath, cfg.Agent.Execution.WorkingDirectory,
	})
	if err != nil {
		return launchPlan{}, err
	}
	if err := environment.Check(report); err != nil {
		return launchPlan{}, fmt.Errorf("%w: task %s cannot run its agent in service %s of %s: %w",
			api.ErrInvalid, task.ID, spec.Service, spec.Identity, err)
	}

	env := agent.Environment{
		Mode:      domain.ExecutionDevcontainer,
		Runner:    containerRunner{environment: environment},
		GitHubCLI: agent.CapabilityLevel(cfg.Agent.Capabilities.GitHubCLI),
		GitLabCLI: agent.CapabilityLevel(cfg.Agent.Capabilities.GitLabCLI),
	}
	if err := s.agent.Validate(ctx, env); err != nil {
		return launchPlan{}, fmt.Errorf("%w: task %s cannot start its agent: %w", api.ErrInvalid, task.ID, err)
	}

	// The agent's own view of its filesystem, which is the container's. The
	// provider adapter is written against this and never learns which case it
	// is in (ADR-032).
	spec2, err := s.agent.Prepare(ctx, agent.PrepareRequest{
		Task: task,
		Workspace: agent.Workspace{
			WorkingDirectory: cfg.Agent.Execution.WorkingDirectory,
			ControlPath:      cfg.Agent.Execution.ControlPath,
		},
		Control:     workspace,
		Environment: env,
	})
	if err != nil {
		return launchPlan{}, err
	}

	invocation, err := environment.Command(ctx, execution.Command{
		Program:     spec2.Program,
		Arguments:   spec2.Arguments,
		Directory:   spec2.Directory,
		Variables:   variables(spec2.Environment),
		Interactive: true,
	})
	if err != nil {
		return launchPlan{}, err
	}
	command := tmux.CommandSpec{
		Program:   invocation.Program,
		Arguments: invocation.Arguments,
		Directory: invocation.Directory,
	}
	if err := command.Validate(); err != nil {
		return launchPlan{}, err
	}

	return launchPlan{
		command:      command,
		agentStarted: true,
		mode:         domain.ExecutionDevcontainer,
		environment:  record,
		note: "the agent runs as " + spec.User + " in service " + spec.Service + " of Compose project " +
			spec.Identity + ". Feat's generated override mounts this task's worktrees at their configured " +
			"container paths and resets container_name and published ports for that service, so several tasks " +
			"can run at once",
	}, nil
}

// variables turns an adapter's KEY=VALUE entries into a map.
func variables(entries []string) map[string]string {
	if len(entries) == 0 {
		return nil
	}
	values := make(map[string]string, len(entries))
	for _, entry := range entries {
		name, value, found := strings.Cut(entry, "=")
		if found {
			values[name] = value
		}
	}
	return values
}

// recordEnvironment writes down an execution environment, before or after it
// exists.
//
// The task snapshot is its natural home and is where it lives for most of a
// task's life. It cannot be at the moment that matters most, though: a session
// needs the tmux target of the terminal that runs inside the container, so no
// session exists to record anything on until after the container is running.
//
// The event log has no such requirement, and it is append-only, durable, and
// per task, so the identity is written there first. An interruption between the
// two therefore still leaves a record naming what may exist, which keeps
// ADR-029's ordering rather than abandoning it for containers (ADR-033).
func (s *service) recordEnvironment(
	ctx context.Context, task *domain.Task, environment *domain.ExecutionEnvironment, detail string,
) error {
	if task.Session != nil {
		task.Session.Execution = environment
		if err := s.store.Tasks().Save(ctx, task); err != nil {
			return err
		}
	}
	s.record(ctx, task, domain.Event{
		Type:   domain.EventExecutionChanged,
		To:     environment.Identity,
		Detail: detail,
	})
	return nil
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
