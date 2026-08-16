package config

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/paths"
)

// volumeNamePattern is what Docker accepts as a named volume, which belongs to
// another tool's namespace.
var volumeNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// Validate reports every rule the resolved configuration breaks.
//
// It checks shape and safety only. Whether a configured path exists, whether it
// holds a Git repository, and whether an executable is installed are host
// questions, and they belong to diagnostics: a configuration file must stay
// loadable on a machine where a repository is temporarily missing, or `feat
// doctor` would have nothing left to report it with.
func (c *Config) Validate() error {
	if !c.resolved {
		return fmt.Errorf("the configuration must be resolved before it is validated")
	}

	found := &problems{}
	c.validateVersion(found)
	c.validateProject(found)
	c.validateRepositories(found)
	c.validateGit(found)
	c.validateAgent(found)
	c.validateRuntime(found)
	c.validateReview(found)
	c.validateChecks(found)

	return found.err(c.path, c.source)
}

func (c *Config) validateVersion(found *problems) {
	if c.Version != SchemaVersion {
		found.add("version", fmt.Sprintf(
			"must be %d, but is %d: this build understands schema version %d only",
			SchemaVersion, c.Version, SchemaVersion))
	}
}

func (c *Config) validateProject(found *problems) {
	if err := domain.ProjectID(c.Project.ID).Validate(); err != nil {
		found.add("project.id", reason(err))
	}
	found.require(c.Project.Name != "", "project.name", "must not be empty")

	if c.Project.PrimaryRepository == "" {
		found.add("project.primary_repository", "must name the repository a task works in by default")
		return
	}
	primary, ok := c.Primary()
	if !ok {
		found.add("project.primary_repository", fmt.Sprintf(
			"names %q, which is not one of this project's repositories: %s",
			c.Project.PrimaryRepository, words(c.RepositoryIDs())))
		return
	}
	// FR-PROJ-003: the primary repository is where the agent works, so a
	// project whose primary repository can never be written to has no editable
	// workspace at all.
	if !domain.DefaultAccess(primary.DefaultAccess).CanBeReadWrite() {
		found.add("project.primary_repository", fmt.Sprintf(
			"names %q, whose default_access is %q: the primary repository must be one a task can edit, so it must be %q or %q",
			c.Project.PrimaryRepository, primary.DefaultAccess,
			domain.DefaultAccessReadWrite, domain.DefaultAccessSelectable))
	}
}

func (c *Config) validateRepositories(found *problems) {
	if len(c.Repositories) == 0 {
		found.add("repositories", "must contain at least one repository")
		return
	}

	// Container paths in the agent's own container are collected as they are
	// checked, so that the overlap rule can report the pair rather than one half
	// of it. Runtime container paths are not: a repository's worktree is mounted
	// into its own services alone, so two repositories expecting their source at
	// the same path are two containers agreeing rather than a collision.
	var agentMounts []mount

	for _, id := range c.RepositoryIDs() {
		repository := c.Repositories[id]
		field := "repositories." + id

		if err := domain.RepositoryID(id).Validate(); err != nil {
			found.add(field, reason(err))
		}
		found.require(repository.HostPath != "", field+".host_path",
			"must be the path of the ordinary checkout on this machine")
		found.require(repository.DefaultBranch != "", field+".default_branch", "must not be empty")
		found.require(repository.Remote != "", field+".remote", "must not be empty")

		switch {
		case repository.DefaultAccess == "":
			// Not defaulted on purpose: whether an agent may write to a
			// repository is the user's decision to state, not Feat's to guess.
			found.add(field+".default_access", fmt.Sprintf(
				"must say how the repository takes part in a task by default: %s",
				words(accessModes())))
		case !domain.DefaultAccess(repository.DefaultAccess).Valid():
			found.add(field+".default_access", fmt.Sprintf(
				"is %q, which is not an access mode: %s", repository.DefaultAccess, words(accessModes())))
		}

		if path := repository.Agent.ContainerPath; path != "" {
			if !c.Agent.Execution.Devcontainer() {
				// A field the selected mode would ignore is rejected rather than
				// ignored, as every other one is (ADR-028). A host-native agent has
				// no container to mount anything in.
				found.add(field+".agent.container_path", fmt.Sprintf(
					"applies only to %q execution, and agent.execution.mode is %q",
					ModeDevcontainer, c.Agent.Execution.Mode))
			} else if checkContainerPath(found, field+".agent.container_path", path) {
				agentMounts = append(agentMounts, mount{id: id, path: path})
			}
		} else if c.Agent.Execution.Devcontainer() &&
			domain.DefaultAccess(repository.DefaultAccess) != domain.DefaultAccessOmitted {
			found.add(field+".agent.container_path", fmt.Sprintf(
				"must say where the repository is mounted in the agent's container, because agent.execution.mode is %q",
				ModeDevcontainer))
		}

		c.validateRepositoryRuntime(found, id, repository)
	}

	checkOverlaps(found, "agent", agentMounts)
}

// mount is one repository's container path, kept with its repository so that an
// overlap can be reported as the pair it is.
type mount struct {
	id   string
	path string
}

// checkOverlaps rejects two repositories mounted inside one another.
//
// One mount inside another does not fail at Compose; it produces a container
// where one repository shadows part of another, which is far harder to
// recognise later than a rejected configuration now.
func checkOverlaps(found *problems, kind string, mounts []mount) {
	for i, outer := range mounts {
		for _, inner := range mounts[i+1:] {
			if !overlaps(outer.path, inner.path) {
				continue
			}
			found.add(fmt.Sprintf("repositories.%s.%s.container_path", inner.id, kind), fmt.Sprintf(
				"is %q, which overlaps repository %s at %q: two repositories cannot be mounted inside one another",
				inner.path, outer.id, outer.path))
		}
	}
}

// validateRepositoryRuntime checks one repository's contribution to the
// application runtime.
//
// Whether a contribution without a container path can produce a runtime worth
// having is a separate question with a separate answer: it produces services
// running the user's ordinary checkout, which is ADR-065 evidence 1, and
// refusing it belongs to the slice that can also say which services those are.
func (c *Config) validateRepositoryRuntime(found *problems, id string, repository Repository) {
	runtime := repository.Runtime
	if runtime == nil {
		return
	}
	field := "repositories." + id + ".runtime"

	if c.Runtime == nil {
		found.add(field, "contributes Compose files and services to an application runtime this project "+
			"does not configure: add a runtime section, or remove this one")
		return
	}

	if len(runtime.ComposeFiles) == 0 && runtime.ContainerPath == "" && len(runtime.Services) == 0 {
		found.add(field, "is empty: a repository takes part in the application runtime by bringing "+
			"Compose files, by naming services, or by saying where its services expect its source")
	}
	for i, file := range runtime.ComposeFiles {
		element := fmt.Sprintf("%s.compose_files[%d]", field, i)
		if !filepath.IsAbs(file) {
			found.add(element, fmt.Sprintf(
				"must resolve to an absolute path, but is %q", file))
		}
	}

	seen := make(map[string]bool, len(runtime.Services))
	for i, service := range runtime.Services {
		element := fmt.Sprintf("%s.services[%d]", field, i)
		switch {
		case service == "":
			found.add(element, "must not be empty")
		case seen[service]:
			found.add(element, fmt.Sprintf("names %q twice", service))
		}
		seen[service] = true
	}
	for i, service := range runtime.Reachable {
		element := fmt.Sprintf("%s.reachable[%d]", field, i)
		if !seen[service] {
			// Feat publishes a port for a reachable service and writes the address
			// into the override of every managed service. A name that is not one of
			// this repository's own services is one nothing would ever publish.
			found.add(element, fmt.Sprintf(
				"names %q, which is not one of %s.services: a repository declares which of its own "+
					"services are reachable", service, field))
		}
	}

	if runtime.ContainerPath == "" {
		return
	}
	checkContainerPath(found, field+".container_path", runtime.ContainerPath)
	if len(runtime.Services) == 0 {
		// The path is where this repository's worktree is mounted in this
		// repository's own services, so a path with no services is a mount
		// nothing would ever receive — the silent kind of wrong this whole
		// section exists to remove (ADR-065 evidence 7).
		found.add(field+".container_path", fmt.Sprintf(
			"is %q, and %s.services names no service to mount it in: a repository's runtime container "+
				"path is where its own services expect its source, so a repository that manages none has "+
				"nowhere to put it", runtime.ContainerPath, field))
	}
}

func (c *Config) validateGit(found *problems) {
	policies := []string{PolicyRemote, PolicyLocal, PolicyCurrent, PolicyExplicit}
	if !contains(policies, c.Git.BasePolicy) {
		found.add("git.base_policy", fmt.Sprintf(
			"is %q, which is not a base policy: %s", c.Git.BasePolicy, words(policies)))
	}

	p := c.probe()

	checkPlaceholders(found, "git.branch_template", c.Git.BranchTemplate, branchPlaceholders)
	checkTaskScoped(found, "git.branch_template", c.Git.BranchTemplate, "branch")
	checkBranchName(found, "git.branch_template", c.Git.BranchTemplate, p)

	c.validateWorktreeRoot(found, p)
}

// validateWorktreeRoot checks where task worktrees are created.
//
// This is the one template whose expansion Feat later removes files from, so it
// is checked against what it resolves to rather than only against how it is
// written.
func (c *Config) validateWorktreeRoot(found *problems, p probe) {
	const field = "git.worktree_root"

	if c.Git.WorktreeRoot == "" {
		found.add(field, "must say where task worktrees are created")
		return
	}
	checkPlaceholders(found, field, c.Git.WorktreeRoot, worktreePlaceholders)
	checkTaskScoped(found, field, c.Git.WorktreeRoot, "worktree directory")

	resolved := filepath.Clean(p.expand(c.Git.WorktreeRoot))
	if !filepath.IsAbs(resolved) {
		found.add(field, fmt.Sprintf("must be an absolute path, but expands to %q", resolved))
		return
	}
	// Cleanup creates and removes directories under the fixed part of this
	// path, so that part is what has to belong to Feat. Checking the expanded
	// path alone would accept "/var/{task_id}/work", which puts Feat's
	// directories in a system location one placeholder deeper down.
	if prefix := StaticPrefix(c.Git.WorktreeRoot); paths.Broad(prefix) {
		found.add(field, fmt.Sprintf(
			"is rooted at %q, which is a shared directory: Feat creates and removes task worktrees under this root, so its fixed part must be a directory Feat owns",
			prefix))
		return
	}

	// A worktree root inside a checkout would put generated worktrees under
	// Git's own working tree; a checkout inside the worktree root would put the
	// user's own repository inside what cleanup removes.
	for _, id := range c.RepositoryIDs() {
		host := c.Repositories[id].HostPath
		if host == "" {
			continue
		}
		if overlaps(filepath.ToSlash(host), filepath.ToSlash(resolved)) {
			found.add(field, fmt.Sprintf(
				"expands to %q, which overlaps the checkout of repository %s at %q: task worktrees must live outside the repositories they come from",
				resolved, id, host))
			return
		}
	}
}

func (c *Config) validateAgent(found *problems) {
	if c.Agent.Provider != ProviderClaude {
		found.add("agent.provider", fmt.Sprintf(
			"is %q, and Claude Code is the only agent this version supports: %s",
			c.Agent.Provider, words([]string{ProviderClaude})))
	}
	c.validateExecution(found)
	c.validateCapabilities(found)

	if volume := c.Agent.Claude.ConfigVolume; volume != "" && !volumeNamePattern.MatchString(volume) {
		found.add("agent.claude.config_volume", fmt.Sprintf(
			"is %q, which Docker rejects as a volume name: it must start with a letter or digit and contain only letters, digits, %q, %q, and %q",
			volume, "-", "_", "."))
	}
	c.validateClaudeConfigPath(found)
}

// validateClaudeConfigPath checks where the Claude configuration volume is
// mounted.
//
// The path exists only to carry a volume, so configuring one without the other
// is rejected rather than ignored, as every other field a mode would not use is
// (ADR-028). Where it does apply, it is checked exactly as the control path is:
// a mount that landed inside a repository would hide part of the repository
// behind Claude's own state.
func (c *Config) validateClaudeConfigPath(found *problems) {
	claude := c.Agent.Claude
	devcontainer := c.Agent.Execution.Devcontainer()

	if !devcontainer {
		for _, unused := range []struct {
			path  string
			unset bool
		}{
			{"agent.claude.config_volume", claude.ConfigVolume == ""},
			{"agent.claude.config_path", claude.ConfigPath == ""},
		} {
			found.require(unused.unset, unused.path, fmt.Sprintf(
				"applies only to %q execution, and agent.execution.mode is %q",
				ModeDevcontainer, c.Agent.Execution.Mode))
		}
		return
	}

	if claude.ConfigPath == "" {
		return
	}
	if claude.ConfigVolume == "" {
		found.add("agent.claude.config_path", fmt.Sprintf(
			"is %q, but agent.claude.config_volume names no volume to mount there: without a volume Feat mounts nothing and sets no CLAUDE_CONFIG_DIR",
			claude.ConfigPath))
		return
	}
	if !checkContainerPath(found, "agent.claude.config_path", claude.ConfigPath) {
		return
	}

	if control := c.Agent.Execution.ControlPath; control != "" && overlaps(control, claude.ConfigPath) {
		found.add("agent.claude.config_path", fmt.Sprintf(
			"is %q, which overlaps the control workspace at %q: Claude's configuration and the task control workspace must be separate",
			claude.ConfigPath, control))
		return
	}
	for _, id := range c.RepositoryIDs() {
		container := c.Repositories[id].Agent.ContainerPath
		if container != "" && overlaps(container, claude.ConfigPath) {
			found.add("agent.claude.config_path", fmt.Sprintf(
				"is %q, which overlaps the mount of repository %s at %q: Claude's configuration must not be mounted inside a repository",
				claude.ConfigPath, id, container))
			return
		}
	}
}

func (c *Config) validateExecution(found *problems) {
	execution := c.Agent.Execution
	modes := []string{ModeHost, ModeDevcontainer}

	switch execution.Mode {
	case "":
		found.add("agent.execution.mode", fmt.Sprintf(
			"must say where the agent runs: %s. %q has no container boundary; %q runs the agent in a configured Compose service",
			words(modes), ModeHost, ModeDevcontainer))
		return
	case ModeHost:
		// A field that means nothing in this mode is rejected rather than
		// ignored, for the same reason an unknown field is: a user who
		// configured a service and a non-root user should not be left believing
		// the agent is in a container.
		for _, unused := range []struct {
			path  string
			unset bool
		}{
			{"agent.execution.compose_files", len(execution.ComposeFiles) == 0},
			{"agent.execution.service", execution.Service == ""},
			{"agent.execution.user", execution.User == ""},
			{"agent.execution.control_path", execution.ControlPath == ""},
		} {
			found.require(unused.unset, unused.path, fmt.Sprintf(
				"applies only to %q execution, and agent.execution.mode is %q",
				ModeDevcontainer, ModeHost))
		}
		// The host agent works in the primary task worktree, whose path is
		// generated per task, so a configured directory could only contradict
		// it (docs/07-configuration-model.md, execution mode).
		found.require(execution.WorkingDirectory == "", "agent.execution.working_directory", fmt.Sprintf(
			"is set by Feat in %q execution: the agent works in the primary task worktree",
			ModeHost))
		return
	case ModeDevcontainer:
	default:
		found.add("agent.execution.mode", fmt.Sprintf(
			"is %q, which is not an execution mode: %s", execution.Mode, words(modes)))
		return
	}

	found.require(len(execution.ComposeFiles) > 0, "agent.execution.compose_files",
		"must list the Compose files that define the devcontainer")
	found.require(execution.Service != "", "agent.execution.service",
		"must name the Compose service the agent runs in")

	switch {
	case execution.User == "":
		found.add("agent.execution.user", "must name the container user the agent runs as, which must not be root")
	case isRoot(execution.User):
		// docs/05-security-model.md requires a non-root agent user, and slice 8
		// checks the running process. Refusing the configuration is the earlier
		// and cheaper of the two.
		found.add("agent.execution.user", fmt.Sprintf(
			"is %q: the agent must run as a non-root user in the devcontainer", execution.User))
	}

	if execution.WorkingDirectory == "" {
		found.add("agent.execution.working_directory",
			"must be the agent's working directory inside the devcontainer")
	} else if checkContainerPath(found, "agent.execution.working_directory", execution.WorkingDirectory) {
		if !c.mounted(execution.WorkingDirectory) {
			found.add("agent.execution.working_directory", fmt.Sprintf(
				"is %q, which is not inside any repository's agent.container_path: the agent would start in a directory no repository is mounted at",
				execution.WorkingDirectory))
		}
	}

	if execution.ControlPath == "" {
		found.add("agent.execution.control_path",
			"must be where the task control workspace is mounted in the devcontainer")
		return
	}
	if !checkContainerPath(found, "agent.execution.control_path", execution.ControlPath) {
		return
	}
	for _, id := range c.RepositoryIDs() {
		container := c.Repositories[id].Agent.ContainerPath
		if container != "" && overlaps(container, execution.ControlPath) {
			found.add("agent.execution.control_path", fmt.Sprintf(
				"is %q, which overlaps the mount of repository %s at %q: the control workspace must be separate from every repository",
				execution.ControlPath, id, container))
			return
		}
	}
}

// validateCapabilities checks the declared capabilities against what Feat can
// actually deliver.
//
// Three of them accept one value each. That is deliberate: Feat has no
// mechanism that grants an agent Docker, restricts its network, or limits its
// Git access, so accepting another value would record a promise the binary does
// not keep. The declaration is still worth making, because slice 8 checks the
// running container against it (docs/05-security-model.md).
func (c *Config) validateCapabilities(found *problems) {
	capabilities := c.Agent.Capabilities

	if capabilities.Docker != CapabilityDenied {
		found.add("agent.capabilities.docker", fmt.Sprintf(
			"is %q, and %q is the only value Feat supports: the agent never receives a Docker socket or a Docker CLI that reaches the host, and only the trusted host runs Docker Compose",
			capabilities.Docker, CapabilityDenied))
	}
	if capabilities.Network != CapabilityUnrestricted {
		found.add("agent.capabilities.network", fmt.Sprintf(
			"is %q, and %q is the only value Feat supports: Feat does not implement network restriction and does not claim data-loss prevention",
			capabilities.Network, CapabilityUnrestricted))
	}
	if capabilities.Git != CapabilityFull {
		found.add("agent.capabilities.git", fmt.Sprintf(
			"is %q, and %q is the only value Feat supports: a native Git worktree shares repository metadata with the agent, which Feat does not restrict",
			capabilities.Git, CapabilityFull))
	}

	levels := []string{CLIDisabled, CLIOptional, CLIRequired}
	for _, capability := range []struct {
		path  string
		value string
		tool  string
	}{
		{"agent.capabilities.github_cli", capabilities.GitHubCLI, "gh"},
		{"agent.capabilities.gitlab_cli", capabilities.GitLabCLI, "glab"},
	} {
		if !contains(levels, capability.value) {
			found.add(capability.path, fmt.Sprintf(
				"is %q, which is not a capability level: %s. %q reports %s but never fails, and %q fails task launch when it is missing",
				capability.value, words(levels), CLIOptional, capability.tool, CLIRequired))
		}
	}
}

func (c *Config) validateRuntime(found *problems) {
	if c.Runtime == nil {
		return
	}
	runtime := c.Runtime

	if runtime.Provider != ProviderCompose {
		found.add("runtime.provider", fmt.Sprintf(
			"is %q, and Docker Compose is the only runtime this version supports: %s",
			runtime.Provider, words([]string{ProviderCompose})))
	}
	if runtime.StartPolicy != StartManual {
		found.add("runtime.start_policy", fmt.Sprintf(
			"is %q, and %q is the only policy in v0: application services start by explicit user action",
			runtime.StartPolicy, StartManual))
	}
	checkPlaceholders(found, "runtime.project_name_template", runtime.ProjectNameTemplate, runtimePlaceholders)
	checkTaskScoped(found, "runtime.project_name_template", runtime.ProjectNameTemplate, "Compose project")
	checkComposeName(found, "runtime.project_name_template", runtime.ProjectNameTemplate, c.probe())

	c.validateComposition(found)
}

// validateComposition checks the runtime the repositories compose between them.
//
// The per-repository rules are checked where the repositories are. What is left
// is what only the whole can say: that there is something to run, and that no
// file is brought twice.
//
// Two repositories naming one service is deliberately allowed. A repository's
// services are the ones that run its code, and a service that runs an
// application and a shared library it depends on runs the code of two
// repositories — at two container paths, which is exactly what a per-repository
// container path is for.
func (c *Config) validateComposition(found *problems) {
	composition := c.RuntimeComposition()

	if len(composition) == 0 {
		found.add("runtime", "is configured, and no repository contributes anything to it: "+
			"give a repository a runtime section naming the Compose files it brings and the services "+
			"Feat manages")
		return
	}
	if len(c.RuntimeServices()) == 0 {
		found.add("runtime", "is configured, and no repository names a service Feat manages: "+
			"a runtime with nothing to start is a runtime nothing can act on")
	}

	files := make(map[string]string)
	for _, contribution := range composition {
		field := "repositories." + contribution.RepositoryID + ".runtime"

		for i, file := range contribution.ComposeFiles {
			if owner, taken := files[file]; taken {
				found.add(fmt.Sprintf("%s.compose_files[%d]", field, i), fmt.Sprintf(
					"is %q, which repository %s also brings: including one file twice defines its "+
						"services twice", file, owner))
				continue
			}
			files[file] = contribution.RepositoryID
		}
	}

	c.checkMountable(found, composition)
}

// checkMountable refuses a runtime that could mount no task worktree at all.
//
// This is the half of "a service is not running the task's code" that needs no
// Docker to diagnose, and it is the shape a real project arrived at twice: no
// repository said where its source goes, so the generated override carried no
// `volumes:` at all, every service ran the user's ordinary checkout, and every
// record Feat kept about the task stayed correct (ADR-065 evidence 1).
//
// It is asked of the runtime rather than of each repository, because
// configuration cannot see the other way a repository's code reaches a service.
// A service whose image is built from the repository runs the task's worktree
// once Feat redirects its build context, and it may have no mount anywhere and
// want none — the reference project's frontend is a multi-stage build ending in
// nginx, where mounting a worktree would be meaningless at best. Requiring a
// container path of every contributing repository would refuse that project;
// requiring one somewhere refuses only the runtime that can carry no task work
// at all. Which particular service is not running the task's code is resolved
// when the runtime is, where the project's own Compose files can be read.
//
// A repository no task ever selects is not counted: it has no worktree to mount,
// whatever it says here.
func (c *Config) checkMountable(found *problems, composition []RuntimeContribution) {
	var eligible []RuntimeContribution
	for _, contribution := range composition {
		if contribution.ContainerPath != "" {
			return
		}
		repository := c.Repositories[contribution.RepositoryID]
		if len(contribution.Services) == 0 ||
			domain.DefaultAccess(repository.DefaultAccess) == domain.DefaultAccessOmitted {
			continue
		}
		eligible = append(eligible, contribution)
	}

	for _, contribution := range eligible {
		found.add("repositories."+contribution.RepositoryID+".runtime.container_path", fmt.Sprintf(
			"must say where %s. A task selects this repository, and no repository in this project answers "+
				"that question, so the runtime would mount no task worktree anywhere and every service "+
				"would run the ordinary checkout. Where these services bake their code instead, Feat builds "+
				"them from the task's worktree and this can be a path they never read",
			expectation(contribution.Services)))
	}
}

// expectation names a repository's managed services and the source they expect,
// with the verb that agrees.
func expectation(names []string) string {
	if len(names) == 1 {
		return "the service " + names[0] + " of this repository expects its source"
	}
	return "the services " + strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1] +
		" of this repository expect their source"
}

func (c *Config) validateReview(found *problems) {
	for _, command := range []struct {
		path    string
		value   Command
		purpose string
	}{
		{"review.diff.command", c.Review.Diff, "compare a repository against its recorded base commit"},
		{"review.editor.command", c.Review.Editor, "open a repository for editing"},
		{"review.status.command", c.Review.Status, "show a repository's Git status"},
	} {
		if command.value.Empty() {
			// An unset editor is the one command that may be missing: it
			// defaults to $EDITOR, which the daemon's environment may not have.
			// Diagnostics report it; a project that never opens an editor is
			// still a valid project.
			continue
		}
		checkCommand(found, command.path, command.value.Command)
	}
}

func (c *Config) validateChecks(found *problems) {
	for _, repository := range sortedKeys(c.Checks) {
		field := "checks." + repository
		if _, ok := c.Repository(repository); !ok {
			found.add(field, fmt.Sprintf(
				"names %q, which is not one of this project's repositories: %s",
				repository, words(c.RepositoryIDs())))
			continue
		}

		seen := make(map[string]bool)
		for i, check := range c.Checks[repository] {
			element := fmt.Sprintf("%s[%d]", field, i)

			if err := domain.RepositoryID(check.ID).Validate(); err != nil {
				found.add(element+".id", reason(err))
			} else if seen[check.ID] {
				found.add(element+".id", fmt.Sprintf("is %q, which another check of %s already uses", check.ID, repository))
			}
			seen[check.ID] = true

			checkCommand(found, element+".command", check.Command)

			executions := []string{ExecutionAgent, ExecutionHost}
			if !contains(executions, check.Execution) {
				found.add(element+".execution", fmt.Sprintf(
					"is %q, which does not say where the check runs: %s",
					check.Execution, words(executions)))
			}
		}
	}
}

// checkCommand validates one external command.
func checkCommand(found *problems, path string, command []string) {
	if len(command) == 0 {
		found.add(path, "must be an argument vector whose first element is the program to run")
		return
	}
	program := command[0]
	switch {
	case strings.TrimSpace(program) == "":
		found.add(path, "must start with the program to run")
	case placeholderPattern.MatchString(program):
		// The program is fixed by configuration and the arguments are filled
		// per task. A placeholder in the program would let an expanded value
		// decide which executable runs.
		found.add(path, fmt.Sprintf(
			"starts with %q: the program to run is fixed by configuration, and only arguments may contain placeholders",
			program))
	}
	for i, argument := range command[1:] {
		checkPlaceholders(found, fmt.Sprintf("%s[%d]", path, i+1), argument, commandPlaceholders)
	}
}

// checkContainerPath validates a path inside an execution environment and
// reports whether it is usable.
func checkContainerPath(found *problems, field, value string) bool {
	// Container paths are always slash-separated, so they cannot be judged with
	// path/filepath, whose meaning depends on the host operating system.
	switch {
	case !strings.HasPrefix(value, "/"):
		found.add(field, fmt.Sprintf(
			"must be an absolute path inside the execution environment, but is %q", value))
		return false
	case value != path.Clean(value):
		found.add(field, fmt.Sprintf(
			"must be a clean path, but %q is written differently from %q", value, path.Clean(value)))
		return false
	case value == "/":
		found.add(field, "must not be the filesystem root of the execution environment")
		return false
	}
	return true
}

// mounted reports whether a container path is at or inside a repository's mount
// in the agent's own container.
func (c *Config) mounted(target string) bool {
	for _, id := range c.RepositoryIDs() {
		container := c.Repositories[id].Agent.ContainerPath
		if container == "" {
			continue
		}
		if target == container || strings.HasPrefix(target, strings.TrimSuffix(container, "/")+"/") {
			return true
		}
	}
	return false
}

// probe returns representative expansion values for this project.
func (c *Config) probe() probe {
	repository := c.Project.PrimaryRepository
	if repository == "" {
		if ids := c.RepositoryIDs(); len(ids) > 0 {
			repository = ids[0]
		} else {
			repository = "repository"
		}
	}
	id := c.Project.ID
	if id == "" {
		id = "project"
	}
	return probe{projectID: id, repositoryID: repository}
}

// overlaps reports whether one path is the other, or is inside it.
func overlaps(a, b string) bool {
	a = strings.TrimSuffix(a, "/")
	b = strings.TrimSuffix(b, "/")
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

// StaticPrefix returns the fixed leading directory of a path template:
// everything before the first placeholder, cut back to the last separator.
//
// It is the deepest directory Feat can know it will create things under, which
// is the directory whose ownership matters. Validation checks it here, and the
// Git adapter requires every worktree it creates to descend from it, so the
// directory a user allowed and the directory Feat writes to are the same one.
func StaticPrefix(template string) string {
	index := strings.IndexByte(template, '{')
	if index < 0 {
		return filepath.Clean(template)
	}
	head := template[:index]
	if cut := strings.LastIndexByte(head, filepath.Separator); cut >= 0 {
		head = head[:cut]
	}
	if head == "" {
		return string(filepath.Separator)
	}
	return filepath.Clean(head)
}

// isRoot reports whether a container user is the superuser, by name or by id.
func isRoot(user string) bool {
	name, _, _ := strings.Cut(user, ":")
	return name == "root" || name == "0"
}

// accessModes lists the documented default access modes.
func accessModes() []string {
	return []string{
		string(domain.DefaultAccessReadWrite),
		string(domain.DefaultAccessReadOnly),
		string(domain.DefaultAccessSelectable),
		string(domain.DefaultAccessStableReadOnly),
		string(domain.DefaultAccessOmitted),
	}
}

// reason extracts the explanation from a domain validation error, which already
// states the rule, so that a configuration problem reads as one sentence rather
// than as two nested ones.
func reason(err error) string {
	var invalid *domain.ValidationError
	if errors.As(err, &invalid) {
		return invalid.Reason
	}
	return err.Error()
}

// sortedKeys returns a map's keys in order, so that problems appear in the same
// sequence on every run.
func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
