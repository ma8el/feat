package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"

	"github.com/ma8el/feat/internal/agent"
	"github.com/ma8el/feat/internal/config"
	"github.com/ma8el/feat/internal/control"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/execution"
	"github.com/ma8el/feat/internal/execution/compose"
	"github.com/ma8el/feat/internal/tmux"
)

// executionProvider is the adapter identifier recorded on a session.
const executionProvider = "compose"

// overrideName is the generated Compose override of one task.
const overrideName = "compose.override.yaml"

// identityPrefix distinguishes the agent's own Compose project from the
// application runtime's, and from any project the user brings up by hand from
// the same files.
//
// It is generated rather than configured: the environment is Feat's own
// resource, and a template whose only use is to break the guarantee it provides
// is not a setting worth having (ADR-033).
const identityPrefix = "feat-agent"

// executionSpec resolves a task's execution environment from configuration.
//
// Everything the adapter receives is final here: absolute paths, an identity, a
// service, a user, and the exact mounts. The adapter reads no configuration, so
// this function is the only place the two vocabularies meet.
func (s *service) executionSpec(
	cfg *config.Config, task *domain.Task, workspace *control.Workspace,
) (execution.Spec, error) {
	if !cfg.Agent.Execution.Devcontainer() {
		return execution.Spec{}, fmt.Errorf("task %s does not run its agent in a container", task.ID)
	}
	if len(cfg.Agent.Execution.ComposeFiles) == 0 {
		return execution.Spec{}, fmt.Errorf("project %s configures no Compose files for its devcontainer", task.ProjectID)
	}

	override, err := s.overridePath(task)
	if err != nil {
		return execution.Spec{}, err
	}

	mounts, err := taskMounts(cfg, task, workspace)
	if err != nil {
		return execution.Spec{}, err
	}

	spec := execution.Spec{
		Project:  task.ProjectID,
		Task:     task.ID,
		Identity: identityPrefix + "-" + task.ProjectID.String() + "-" + task.ID.String(),
		Files:    append([]string(nil), cfg.Agent.Execution.ComposeFiles...),
		// The first configured file's directory, so that file's own relative
		// sources and build contexts resolve as they do when the user runs
		// Compose by hand (ADR-033).
		Directory:        filepath.Dir(cfg.Agent.Execution.ComposeFiles[0]),
		OverridePath:     override,
		Service:          cfg.Agent.Execution.Service,
		User:             cfg.Agent.Execution.User,
		WorkingDirectory: cfg.Agent.Execution.WorkingDirectory,
		Mounts:           mounts,
		ForbiddenSources: checkouts(cfg),
	}

	if volume := cfg.Agent.Claude.ConfigVolume; volume != "" {
		// Only when the project asked for one. A project that supplies the
		// provider's configuration through its own Compose files — by mounting
		// the user's own directory, which the security model permits as an
		// explicit choice — must not have Feat mount a second one over it.
		spec.Volumes = []execution.Volume{{Name: volume, Target: cfg.Agent.Claude.ConfigPath}}
		spec.Variables = map[string]string{"CLAUDE_CONFIG_DIR": cfg.Agent.Claude.ConfigPath}
	}
	if err := spec.Validate(); err != nil {
		return execution.Spec{}, err
	}
	return spec, nil
}

// overridePath is where a task's generated Compose override is written.
//
// Both identifiers are validated before either reaches a path, so no stored
// value can name a directory outside the execution root.
func (s *service) overridePath(task *domain.Task) (string, error) {
	if err := task.ProjectID.Validate(); err != nil {
		return "", err
	}
	if err := task.ID.Validate(); err != nil {
		return "", err
	}
	return filepath.Join(s.layout.ExecutionRoot(), task.ProjectID.String(), task.ID.String(), overrideName), nil
}

// taskMounts is what the agent's container gets to see.
//
// Every entry is deliberate and there are only three kinds: a task worktree at
// the container path its repository configures, a stable repository from its
// ordinary checkout when the project says it is read-only there, and the control
// workspace. Nothing else is mounted, and a project's own Compose files decide
// nothing about the repositories, because Compose merges these by target and
// these replace whatever was there (ADR-033).
func taskMounts(cfg *config.Config, task *domain.Task, workspace *control.Workspace) ([]execution.Mount, error) {
	var mounts []execution.Mount

	for _, binding := range task.Repositories {
		if _, known := cfg.Repositories[binding.RepositoryID.String()]; !known {
			return nil, fmt.Errorf("task %s selected repository %s, which project %s no longer configures",
				task.ID, binding.RepositoryID, task.ProjectID)
		}
		if binding.ContainerPath == "" {
			return nil, fmt.Errorf(
				"repository %s has no container_path, and task %s runs its agent in a container: "+
					"the agent would have no path to work in", binding.RepositoryID, task.ID)
		}
		if binding.WorktreePath == "" {
			return nil, fmt.Errorf("task %s has no worktree recorded for repository %s",
				task.ID, binding.RepositoryID)
		}
		mounts = append(mounts, execution.Mount{
			Source:      binding.WorktreePath,
			Target:      binding.ContainerPath,
			ReadOnly:    binding.Access == domain.TaskAccessReadOnly,
			Description: describeMount(binding),
		})

		metadata, err := gitMetadataMount(cfg, binding)
		if err != nil {
			return nil, err
		}
		mounts = append(mounts, metadata)
	}

	// A repository the project keeps stable and read-only is not a task
	// repository: it has no branch and no worktree, and the agent reads it from
	// the ordinary checkout. Mounting it read-only is what makes acceptance
	// criterion 2 Feat's to satisfy rather than the project's.
	for _, id := range cfg.RepositoryIDs() {
		repository := cfg.Repositories[id]
		if repository.DefaultAccess != string(domain.DefaultAccessStableReadOnly) {
			continue
		}
		if _, selected := task.Repository(domain.RepositoryID(id)); selected {
			// The task promoted it, so it has a worktree and is mounted above.
			continue
		}
		if repository.ContainerPath == "" {
			continue
		}
		mounts = append(mounts, execution.Mount{
			Source:      repository.HostPath,
			Target:      repository.ContainerPath,
			ReadOnly:    true,
			Description: "the stable " + id + " checkout, read-only",
		})
	}

	mounts = append(mounts, controlMounts(cfg, workspace)...)
	return mounts, nil
}

// controlMounts is the control workspace, mounted the way its own layout is
// split.
//
// The tree is read-only: task.md, context/, and inbox/ are host-written and
// agent-read, and agent/ is host-only — it holds the hooks the provider adapter
// generated and the record of which messages have been applied, which is what
// makes deduplication something the agent cannot reach (ADR-032, and the layout
// docs/06-technical-architecture.md describes). Only the two directories the
// agent reports through are writable.
//
// They are mounted over the tree rather than beside it. Compose merges a
// service's volumes by target, and a nested target is a different target, so
// what a container gets is the read-only workspace with two writable
// directories inside it.
func controlMounts(cfg *config.Config, workspace *control.Workspace) []execution.Mount {
	mounts := []execution.Mount{{
		Source:      workspace.Root(),
		Target:      cfg.Agent.Execution.ControlPath,
		ReadOnly:    true,
		Description: "the task control workspace, read-only",
	}}
	for _, name := range control.AgentWritable() {
		mounts = append(mounts, execution.Mount{
			Source:      filepath.Join(workspace.Root(), name),
			Target:      path.Join(cfg.Agent.Execution.ControlPath, name),
			Description: "the control workspace " + name + ", which is the agent's to write",
		})
	}
	return mounts
}

// controlWritable is what a launch must prove the agent can write to inside the
// control workspace, as the agent sees it.
//
// It is the same two directories controlMounts makes writable, because proving
// the workspace root writable would now be proving the opposite of what Feat
// asks for.
func controlWritable(cfg *config.Config) []string {
	writable := make([]string, 0, len(control.AgentWritable()))
	for _, name := range control.AgentWritable() {
		writable = append(writable, path.Join(cfg.Agent.Execution.ControlPath, name))
	}
	return writable
}

// gitDirName is the Git metadata directory of an ordinary checkout.
const gitDirName = ".git"

// gitMetadataMount makes Git work inside the container.
//
// A task worktree is not a repository on its own. Its .git is a file holding an
// absolute path to the main checkout's `.git/worktrees/<name>`, and that path is
// the host's. Without the directory it names, every Git command in the container
// fails with "not a git repository" — so `git: full`, FR-GIT-006, and the sixth
// acceptance criterion would all be false while everything else looked right.
//
// The mount is therefore the main checkout's Git directory at the same absolute
// path it has on the host, which is what makes the recorded link resolve
// whatever Git version wrote it. What it exposes is repository metadata, which
// docs/05-security-model.md accepts explicitly and calls by its name; what it
// does not expose is the working copy, because the checkout's own directory
// exists in the container holding nothing but this.
//
// Its access follows the worktree's: a task that may not write the code may not
// rewrite the history either.
func gitMetadataMount(cfg *config.Config, binding domain.TaskRepository) (execution.Mount, error) {
	repository, _ := cfg.Repository(binding.RepositoryID.String())
	metadata := filepath.Join(repository.HostPath, gitDirName)

	info, err := os.Stat(metadata)
	switch {
	case err != nil:
		return execution.Mount{}, fmt.Errorf(
			"repository %s has no readable %s, and a task worktree needs it to be a repository at all: %w",
			binding.RepositoryID, metadata, err)
	case !info.IsDir():
		// A checkout that is itself a linked worktree keeps its metadata
		// somewhere else entirely, and the container would need that instead.
		return execution.Mount{}, fmt.Errorf(
			"repository %s has %s as a file rather than a directory, which means its checkout is itself a "+
				"linked worktree. Feat cannot give the agent a working Git from it; configure host_path to "+
				"the main checkout", binding.RepositoryID, metadata)
	}

	return execution.Mount{
		Source:      metadata,
		Target:      metadata,
		ReadOnly:    binding.Access == domain.TaskAccessReadOnly,
		Description: "the " + binding.RepositoryID.String() + " Git directory, so the worktree is a repository",
	}, nil
}

// describeMount says what a worktree mount is, in the user's terms.
func describeMount(binding domain.TaskRepository) string {
	access := "read-write"
	if binding.Access == domain.TaskAccessReadOnly {
		access = "read-only"
	}
	return "the " + binding.RepositoryID.String() + " task worktree, " + access
}

// checkouts lists the ordinary repository checkouts, which must never be
// mounted into a task's container.
//
// A stable read-only repository is deliberately not among them: the project
// declared that the agent reads it from the checkout, and Feat mounts it that
// way itself.
func checkouts(cfg *config.Config) []string {
	var paths []string
	for _, id := range cfg.RepositoryIDs() {
		repository := cfg.Repositories[id]
		if repository.DefaultAccess == string(domain.DefaultAccessStableReadOnly) {
			continue
		}
		if repository.HostPath != "" {
			paths = append(paths, repository.HostPath)
		}
	}
	return paths
}

// containerShells are the shells a task shell tries inside a container, in
// order of preference.
//
// The host's $SHELL means nothing in somebody else's image, so the choice is
// made by asking the container what it has rather than by assuming. /bin/sh is
// last because every image has one and none of the earlier answers would be
// improved by it.
var containerShells = []string{"/bin/bash", "/bin/zsh", "/bin/sh"}

// taskShell is the command a task's shell pane runs.
//
// For a host task it is the daemon owner's own shell in the task worktree. For a
// containerised one it is a shell inside the task's own container, in the
// agent's working directory, so that the pane a user opens beside their agent is
// the environment their agent is in.
func (s *service) taskShell(ctx context.Context, cfg *config.Config, task *domain.Task) (tmux.CommandSpec, error) {
	if task.Session == nil || task.Session.Execution == nil {
		return s.shellCommand(cfg, task)
	}

	environment, err := s.environmentFor(task)
	if err != nil {
		return tmux.CommandSpec{}, err
	}
	shell := s.containerShell(ctx, environment)
	invocation, err := environment.Command(ctx, execution.Command{
		Program:     shell,
		Directory:   cfg.Agent.Execution.WorkingDirectory,
		Interactive: true,
	})
	if err != nil {
		return tmux.CommandSpec{}, err
	}
	command := tmux.CommandSpec{
		Program:   invocation.Program,
		Arguments: invocation.Arguments,
		Directory: invocation.Directory,
	}
	if err := command.Validate(); err != nil {
		return tmux.CommandSpec{}, err
	}
	return command, nil
}

// containerShell asks the container which shell it has.
//
// A container with none of them is not a container Feat can open a shell in at
// all, and the last candidate is what every image has, so the fallback is the
// honest attempt rather than a refusal.
func (s *service) containerShell(ctx context.Context, environment execution.Environment) string {
	for _, shell := range containerShells {
		output, err := environment.Run(ctx, execution.Command{Program: shell, Arguments: []string{"-c", ":"}})
		if err == nil && output.Succeeded() {
			return shell
		}
	}
	return containerShells[len(containerShells)-1]
}

// environmentFor rebuilds the execution environment a task's session records.
//
// It is rebuilt from the record rather than kept in memory, because the daemon
// may have restarted since the task launched: what a task owns has to survive in
// the record, which is why the record carries the identity and the exact inputs
// (docs/03-domain-model.md).
func (s *service) environmentFor(task *domain.Task) (execution.Environment, error) {
	recorded := task.Session.Execution
	if recorded == nil {
		return nil, fmt.Errorf("task %s records no execution environment", task.ID)
	}
	if recorded.Provider != executionProvider {
		return nil, fmt.Errorf("task %s records the execution provider %q, which this build has no adapter for",
			task.ID, recorded.Provider)
	}

	cfg, err := config.Load(s.layout.ProjectConfigDir(), task.ProjectID.String(), s.configOptions())
	if err != nil {
		return nil, err
	}
	workspace, err := s.controlWorkspace(task)
	if err != nil {
		return nil, err
	}
	spec, err := s.executionSpec(cfg, task, workspace)
	if err != nil {
		return nil, err
	}
	// The recorded identity and inputs win over what configuration says today.
	// A task's environment is the one it was launched with; an edited project
	// file must not silently point an action at a different container
	// (docs/07-configuration-model.md).
	spec.Identity = recorded.Identity
	spec.Files = recorded.Files
	spec.OverridePath = recorded.GeneratedOverridePath
	spec.Service = recorded.Service
	spec.User = recorded.User
	return s.environments(spec)
}

// environments builds the execution environment for one task.
//
// It is a method rather than a package function so that a test can drive the
// whole launch against a fake Docker: the branches that decide whether a
// half-finished launch is recoverable should not depend on the tester having a
// container runtime (ADR-030's reasoning for the tmux fake).
func (s *service) environments(spec execution.Spec) (execution.Environment, error) {
	return compose.New(spec, compose.Options{Runner: s.docker})
}

// containerRunner runs an agent adapter's probes inside an execution
// environment.
//
// It is the seam ADR-032 left for this slice: the Claude adapter asks its
// questions through agent.Runner, and this makes the answers come from the
// container rather than from the host. Neither adapter knows about the other —
// this shim is the daemon's, which is what keeps both boundaries mechanical.
type containerRunner struct{ environment execution.Environment }

var _ agent.Runner = containerRunner{}

// Run executes one probe inside the environment.
func (r containerRunner) Run(ctx context.Context, command agent.Command) (agent.Output, error) {
	output, err := r.environment.Run(ctx, execution.Command{
		Program:   command.Program,
		Arguments: command.Arguments,
		Directory: command.Directory,
	})
	result := agent.Output{Stdout: output.Stdout, Stderr: output.Stderr, ExitCode: output.ExitCode}
	if err != nil {
		// The two vocabularies for "there is nothing to run" are translated
		// here, so the agent adapter's message about a missing provider CLI
		// reads the same whichever environment answered it.
		if isMissing(err) {
			return result, fmt.Errorf("%w: %s", agent.ErrNotInstalled, command.Program)
		}
		return result, err
	}
	return result, nil
}

// isMissing reports whether an execution error means the executable is absent.
func isMissing(err error) bool {
	return errors.Is(err, compose.ErrNotInEnvironment)
}
