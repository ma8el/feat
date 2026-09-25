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
// application runtime's, and from any project the user brings up by hand from the
// same files. It is generated rather than configured, because the environment is
// Feat's own resource (ADR-033).
const identityPrefix = "feat-agent"

// executionSpec resolves a task's execution environment from configuration.
// Everything the adapter receives is final here: absolute paths, an identity, a
// service, a user, and the exact mounts. The adapter reads no configuration, so
// this is the only place the two vocabularies meet.
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

	forbidden, err := s.forbiddenSources(cfg, task)
	if err != nil {
		return execution.Spec{}, err
	}

	spec := execution.Spec{
		Project:  task.ProjectID,
		Task:     task.ID,
		Identity: agentIdentity(task),
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
		ForbiddenSources: forbidden,
	}

	if volume := cfg.Agent.Claude.ConfigVolume; volume != "" {
		// Only when the project asked for one. A project that supplies the provider's
		// configuration through its own Compose files, by mounting the user's own
		// directory, must not have Feat mount a second one over it.
		spec.Volumes = []execution.Volume{{Name: volume, Target: cfg.Agent.Claude.ConfigPath}}
		spec.Variables = map[string]string{"CLAUDE_CONFIG_DIR": cfg.Agent.Claude.ConfigPath}
	}
	if err := spec.Validate(); err != nil {
		return execution.Spec{}, err
	}
	return spec, nil
}

// agentIdentity is the Compose project name a task's agent environment has.
//
// It is derived from the two identifiers and reads nothing, so it can answer for
// a task whose record does not carry it. The session the identity is recorded on
// is created after the container, so an interruption between them leaves
// containers the snapshot cannot name.
//
// ADR-033 refused to make the template configurable, so a launch of this task can
// only ever have used one name, and the same launch writes that name to the event
// log before creating anything (recordEnvironment).
func agentIdentity(task *domain.Task) string {
	return identityPrefix + "-" + task.ProjectID.String() + "-" + task.ID.String()
}

// agentComposeProject is the name of the Compose project a task's agent runs in:
// the one its session recorded, or the derived one when no session carries it.
// The recorded name wins for the reason environmentFor prefers the whole recorded
// specification: a task's environment is the one it was launched with, and an
// edited project file must not point an action at a different container.
func agentComposeProject(task *domain.Task) string {
	if task.Session != nil && task.Session.Execution != nil && task.Session.Execution.Identity != "" {
		return task.Session.Execution.Identity
	}
	return agentIdentity(task)
}

// agentProject addresses a task's agent Compose project by name, for the two
// questions that can be asked without a specification: what is still there, and
// remove it.
//
// It has three answers and the third is the point. A nil project and no error is
// a question that does not apply, which only the task's own record says: a draft
// has had nothing created for it (domain.WorkflowDraft), and a session recording
// domain.ExecutionHost ran its agent on this host. An error is the third: the
// question applies and could not be asked, which every caller must say out loud
// rather than read as nothing to worry about (ADR-059).
//
// Today's configuration is deliberately not consulted. `agent.execution.mode` is
// a line a user edits, and reading it here let a later edit decide whether Feat
// asked about a container the launch had already created, which removed a control
// workspace a live container still mounted. A task whose record does not say
// where its agent ran is asked about instead, because the name depends on nothing
// but the two identifiers. That is the ADR-059 case and only that case: a launch
// that failed before it recorded a session.
//
// A machine with no Docker therefore refuses rather than answering. It is the
// case the derived name exists for, and the one where no answer is least
// affordable: a task that may hold a container, on a host that cannot be asked.
func (s *service) agentProject(task *domain.Task) (*compose.Project, error) {
	if task.Workflow == domain.WorkflowDraft {
		return nil, nil
	}
	if task.Session != nil && task.Session.ExecutionMode == domain.ExecutionHost {
		return nil, nil
	}

	directory, err := s.composeDirectory()
	if err != nil {
		return nil, fmt.Errorf("the agent Compose project of task %s could not be addressed: %w", task.ID, err)
	}
	project, err := compose.ByName(agentComposeProject(task), directory, compose.Options{Runner: s.docker})
	if err != nil {
		return nil, fmt.Errorf("the agent Compose project of task %s could not be addressed: %w", task.ID, err)
	}
	return project, nil
}

// composeDirectory is the directory the by-name Compose invocations run from.
//
// Any directory Feat owns would do. What matters is that it is neither the
// daemon's inherited working directory nor anything a project controls, so no
// compose.yaml on the machine can become the model for a project addressed by
// name (ADR-059's queries read no file).
//
// It is created rather than assumed, because the first task of a fresh
// installation asks these questions before any launch has written under it.
func (s *service) composeDirectory() (string, error) {
	root := s.layout.ExecutionRoot()
	if err := os.MkdirAll(root, stateDirPerm); err != nil {
		return "", fmt.Errorf("creating the execution directory %s: %w", root, err)
	}
	return root, nil
}

// executionDirectory is where a task's generated execution input is written. It
// is named separately from the file in it because it is created for one task's
// agent Compose project and removed with that project, so cleanup addresses the
// directory and a launch writes the file (ADR-037 evidence 16).
//
// Both identifiers are validated before either reaches a path, so no stored value
// can name a directory outside the execution root.
func (s *service) executionDirectory(task *domain.Task) (string, error) {
	if err := task.ProjectID.Validate(); err != nil {
		return "", err
	}
	if err := task.ID.Validate(); err != nil {
		return "", err
	}
	return filepath.Join(s.layout.ExecutionRoot(), task.ProjectID.String(), task.ID.String()), nil
}

// overridePath is where a task's generated Compose override is written.
func (s *service) overridePath(task *domain.Task) (string, error) {
	directory, err := s.executionDirectory(task)
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, overrideName), nil
}

// taskMounts is what the agent's container gets to see. There are three kinds: a
// task worktree at the container path its repository configures, a stable
// repository from its ordinary checkout, and the control workspace. Nothing else
// is mounted, and Compose merges these by target, so they replace whatever a
// project's own files put there (ADR-033).
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

	// A repository the project keeps stable and read-only is not a task repository:
	// it has no branch and no worktree, and the agent reads it from the ordinary
	// checkout. Mounting it read-only makes acceptance criterion 2 Feat's to satisfy
	// rather than the project's.
	for _, id := range cfg.RepositoryIDs() {
		repository := cfg.Repositories[id]
		// stable() is also what decides which checkouts are forbidden, so a
		// repository this task promoted — which has a worktree and is mounted
		// above — cannot be mounted here and refused there.
		if !stable(cfg, task, id) || repository.Agent.ContainerPath == "" {
			continue
		}
		mounts = append(mounts, execution.Mount{
			Source:      repository.HostPath,
			Target:      repository.Agent.ContainerPath,
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
// agent-read, and agent/ is host-only, holding the generated hooks and the record
// of which messages have been applied, so deduplication is out of the agent's
// reach (ADR-032, docs/06-technical-architecture.md). Only the two directories
// the agent reports through are writable.
//
// They are mounted over the tree rather than beside it. Compose merges a
// service's volumes by target and a nested target is a different target, so the
// container gets the read-only workspace with two writable directories inside it.
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
// control workspace, as the agent sees it. It is the same two directories
// controlMounts makes writable, because proving the workspace root writable would
// be proving the opposite of what Feat asks for.
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
// A task worktree is not a repository on its own: its .git is a file holding an
// absolute path to the main checkout's `.git/worktrees/<name>`, and that path is
// the host's. Without the directory it names, every Git command in the container
// fails with "not a git repository", so `git: full`, FR-GIT-006, and the sixth
// acceptance criterion would be false while everything else looked right.
//
// The mount is the main checkout's Git directory at the same absolute path it has
// on the host, so the recorded link resolves whatever Git version wrote it. It
// exposes repository metadata, which docs/05-security-model.md accepts by name,
// and not the working copy.
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

// forbiddenSources is every host path a task's container must not expose to the
// agent.
//
// Three of them are directories rather than checkouts, and nothing else would
// catch them. Feat's runtime directory holds the daemon's API socket and the tmux
// control socket: a container that reaches the first controls every task on this
// machine, and one that reaches the second runs commands on the host outside its
// own container. The state directory holds every other task's control workspace,
// and the home directory holds all of that plus the credentials the security
// model says Feat must not mount by default.
//
// The order is the order a refusal explains a mount that exposes more than one:
// the runtime directory grants control, the home directory is the widest thing a
// reader recognises in their own Compose file, and the state directory sits
// inside it.
func (s *service) forbiddenSources(cfg *config.Config, task *domain.Task) ([]execution.ForbiddenSource, error) {
	home, err := s.env.Expand("~")
	if err != nil {
		return nil, fmt.Errorf("resolving the home directory a task's container must not mount: %w", err)
	}

	sources := []execution.ForbiddenSource{
		{Path: s.layout.Runtime, Kind: execution.ForbiddenRuntime},
		{Path: home, Kind: execution.ForbiddenHome},
		{Path: s.layout.State, Kind: execution.ForbiddenState},
	}
	for _, checkout := range checkouts(cfg, task) {
		sources = append(sources, execution.ForbiddenSource{Path: checkout, Kind: execution.ForbiddenCheckout})
	}
	for _, checkout := range stableCheckouts(cfg, task) {
		sources = append(sources, execution.ForbiddenSource{
			Path: checkout, Kind: execution.ForbiddenStableCheckout,
		})
	}
	return sources, nil
}

// checkouts lists the ordinary repository checkouts, which must never be mounted
// into a task's container.
//
// A stable read-only repository is among them only when this task promoted it,
// which DefaultAccess.Permits allows. A promoted repository has a branch, a
// worktree, and a mount of that worktree like any other, so its checkout is the
// working copy the task exists to leave alone.
//
// A nil task is one whose repositories are not resolved yet, and every stable
// repository is then still the project's own to mount.
func checkouts(cfg *config.Config, task *domain.Task) []string {
	var paths []string
	for _, id := range cfg.RepositoryIDs() {
		repository := cfg.Repositories[id]
		if stable(cfg, task, id) || repository.HostPath == "" {
			continue
		}
		paths = append(paths, repository.HostPath)
	}
	return paths
}

// stableCheckouts lists the checkouts Feat mounts itself, read-only, because the
// project keeps those repositories stable and this task did not promote them.
// They are forbidden everywhere other than that one mount: a base file mounting
// the same checkout at a second target is the user's own working copy, arriving
// beside it and usually writable.
func stableCheckouts(cfg *config.Config, task *domain.Task) []string {
	var paths []string
	for _, id := range cfg.RepositoryIDs() {
		repository := cfg.Repositories[id]
		if !stable(cfg, task, id) || repository.HostPath == "" {
			continue
		}
		paths = append(paths, repository.HostPath)
	}
	return paths
}

// stable reports whether a repository is one the project keeps stable and
// read-only and the task left that way.
func stable(cfg *config.Config, task *domain.Task, id string) bool {
	if cfg.Repositories[id].DefaultAccess != string(domain.DefaultAccessStableReadOnly) {
		return false
	}
	if task == nil {
		return true
	}
	_, promoted := task.Repository(domain.RepositoryID(id))
	return !promoted
}

// containerShells are the shells a task shell tries inside a container, in order
// of preference. The host's $SHELL means nothing in somebody else's image, so the
// container is asked what it has. /bin/sh is last because every image has one.
var containerShells = []string{"/bin/bash", "/bin/zsh", "/bin/sh"}

// taskShell is the command a task's shell pane runs. For a host task it is the
// daemon owner's own shell in the task worktree; for a containerised one it is a
// shell inside the task's container, in the agent's working directory, so the
// pane a user opens beside their agent is the environment their agent is in.
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

// containerShell asks the container which shell it has. A container with none of
// them is one Feat cannot open a shell in at all, so the last candidate is
// returned as an honest attempt rather than a refusal.
func (s *service) containerShell(ctx context.Context, environment execution.Environment) string {
	for _, shell := range containerShells {
		output, err := environment.Run(ctx, execution.Command{Program: shell, Arguments: []string{"-c", ":"}})
		if err == nil && output.Succeeded() {
			return shell
		}
	}
	return containerShells[len(containerShells)-1]
}

// environmentFor rebuilds the execution environment a task's session records. It
// is rebuilt from the record rather than kept in memory, because the daemon may
// have restarted since the task launched and what a task owns has to survive in
// the record (docs/03-domain-model.md).
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

// environments builds the execution environment for one task. It is a method
// rather than a package function so a test can drive a whole launch against a
// fake Docker, because whether a half-finished launch recovers should not depend
// on the tester having a container runtime (ADR-030's reasoning for the tmux
// fake).
func (s *service) environments(spec execution.Spec) (execution.Environment, error) {
	return compose.New(spec, compose.Options{Runner: s.docker})
}

// containerRunner runs an agent adapter's probes inside an execution environment.
// It is the seam ADR-032 left: the Claude adapter asks through agent.Runner, and
// this makes the container answer rather than the host. Neither adapter knows
// about the other, because the shim is the daemon's.
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
