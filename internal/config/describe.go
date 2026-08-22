package config

import (
	"fmt"
	"sort"
	"strings"
)

// Secret handling in resolved output.
//
// docs/05-security-model.md draws the line at the file boundary: Feat passes
// host-side environment files to Docker Compose by path and should avoid
// reading their values. This package therefore holds paths and never contents,
// which makes "secret values never appear in diagnostics" a property of the
// data rather than a filter applied to the output. There is nothing here to
// redact because nothing here was ever read.
//
// The marker below labels those paths in printed output, so that a reader can
// see which files carry values Feat deliberately did not open.
const secretMarker = "(contents not read)"

// Section is one titled block of resolved configuration.
type Section struct {
	// Title names the block.
	Title string
	// Fields are the block's values, in the order they should be printed.
	Fields []Field
}

// Field is one resolved value.
type Field struct {
	// Name is the field's dotted configuration path.
	Name string
	// Value is the resolved value as it will be used.
	Value string
	// Note explains a value that would otherwise need explaining, such as a
	// default Feat filled in or a file it does not read.
	Note string
}

// Describe renders the resolved configuration.
//
// It is what `feat project show` prints and what `feat doctor` reports against:
// the values Feat will actually act on, after "~" expansion and after defaults
// are filled, rather than the text of the file. A user checking whether their
// configuration says what they meant needs the first, not the second.
func (c *Config) Describe() []Section {
	sections := []Section{c.describeProject(), c.describeRepositories(), c.describeGit(), c.describeAgent()}
	if c.HasRuntime() {
		sections = append(sections, c.describeRuntime())
	}
	sections = append(sections, c.describeReview())
	if len(c.Checks) > 0 {
		sections = append(sections, c.describeChecks())
	}
	return append(sections, c.describeNotifications())
}

// Mounts returns the repository-to-container path mapping.
//
// It is a table of its own because it is the mapping a task depends on and the
// one a user most often needs to check: a repository at the wrong path is a
// task that compiles nothing, or an application that serves the ordinary
// checkout while every record Feat keeps stays correct (ADR-065 evidence 7).
func (c *Config) Mounts() []Mount {
	mounts := make([]Mount, 0, len(c.Repositories))
	for _, id := range c.RepositoryIDs() {
		repository := c.Repositories[id]
		mount := Mount{
			RepositoryID:  id,
			HostPath:      repository.HostPath,
			AgentPath:     repository.Agent.ContainerPath,
			DefaultAccess: repository.DefaultAccess,
			Primary:       id == c.Project.PrimaryRepository,
		}
		if repository.Runtime != nil {
			mount.RuntimePath = repository.Runtime.ContainerPath
			mount.RuntimeServices = append([]string(nil), repository.Runtime.Services...)
		}
		mounts = append(mounts, mount)
	}
	return mounts
}

// Mount is one repository's place on the host, in the agent's container, and in
// its own services' containers.
type Mount struct {
	// RepositoryID identifies the repository within the project.
	RepositoryID string
	// HostPath is the ordinary checkout, after expansion.
	HostPath string
	// AgentPath is where task worktrees are mounted in the agent's own
	// container, empty for host-native execution.
	AgentPath string
	// RuntimePath is where this repository's own services expect their source,
	// empty for a repository whose code no service runs.
	RuntimePath string
	// RuntimeServices are the services this repository asks Feat to manage.
	RuntimeServices []string
	// DefaultAccess is the repository's default participation in a task.
	DefaultAccess string
	// Primary reports whether this is the project's primary repository.
	Primary bool
}

func (c *Config) describeProject() Section {
	return Section{Title: "project", Fields: []Field{
		{Name: "id", Value: c.Project.ID},
		{Name: "name", Value: c.Project.Name},
		{Name: "primary_repository", Value: c.Project.PrimaryRepository},
		{Name: "configuration", Value: c.path},
	}}
}

func (c *Config) describeRepositories() Section {
	section := Section{Title: "repositories"}
	for _, id := range c.RepositoryIDs() {
		repository := c.Repositories[id]
		note := ""
		if id == c.Project.PrimaryRepository {
			note = "primary"
		}
		section.Fields = append(section.Fields,
			Field{Name: id + ".host_path", Value: repository.HostPath, Note: note},
			Field{Name: id + ".agent.container_path", Value: orNone(repository.Agent.ContainerPath),
				Note: "where the agent's container mounts the worktree"},
			Field{Name: id + ".default_branch", Value: repository.DefaultBranch},
			Field{Name: id + ".remote", Value: repository.Remote},
			Field{Name: id + ".default_access", Value: repository.DefaultAccess},
		)
		if repository.Runtime == nil {
			continue
		}
		// Printed whatever the execution mode. It is the mapping that decides
		// whether the user's own services run their task, and a project whose
		// agent is host-native has services all the same (ADR-065 evidence 6).
		section.Fields = append(section.Fields,
			Field{Name: id + ".runtime.container_path", Value: orNone(repository.Runtime.ContainerPath),
				Note: "where this repository's own services expect their source"},
			Field{Name: id + ".runtime.services", Value: orNone(strings.Join(repository.Runtime.Services, ", "))},
		)
		if len(repository.Runtime.Reachable) > 0 {
			section.Fields = append(section.Fields, Field{
				Name:  id + ".runtime.reachable",
				Value: strings.Join(repository.Runtime.Reachable, ", "),
			})
		}
		for i, file := range repository.Runtime.ComposeFiles {
			section.Fields = append(section.Fields, Field{
				Name:  fmt.Sprintf("%s.runtime.compose_files[%d]", id, i),
				Value: file,
			})
		}
	}
	return section
}

func (c *Config) describeGit() Section {
	return Section{Title: "git", Fields: []Field{
		{Name: "base_policy", Value: c.Git.BasePolicy, Note: basePolicyNote(c.Git.BasePolicy)},
		{Name: "fetch_before_task", Value: yesNo(c.Git.FetchesBeforeTask()),
			Note: "fetching never changes the ordinary checkout"},
		{Name: "branch_template", Value: c.Git.BranchTemplate},
		{Name: "worktree_root", Value: c.Git.WorktreeRoot},
	}}
}

func (c *Config) describeAgent() Section {
	execution := c.Agent.Execution
	fields := []Field{
		{Name: "provider", Value: c.Agent.Provider},
		{Name: "execution.mode", Value: execution.Mode, Note: executionNote(execution.Mode)},
	}
	if execution.Devcontainer() {
		fields = append(fields,
			Field{Name: "execution.service", Value: execution.Service},
			Field{Name: "execution.user", Value: execution.User, Note: "non-root"},
			Field{Name: "execution.working_directory", Value: execution.WorkingDirectory},
			Field{Name: "execution.control_path", Value: execution.ControlPath},
		)
		for i, file := range execution.ComposeFiles {
			fields = append(fields, Field{Name: fmt.Sprintf("execution.compose_files[%d]", i), Value: file})
		}
	}
	if c.Agent.Claude.ConfigVolume != "" {
		fields = append(fields,
			Field{Name: "claude.config_volume", Value: c.Agent.Claude.ConfigVolume,
				Note: "dedicated volume; the user's own ~/.claude is not mounted"},
			Field{Name: "claude.config_path", Value: c.Agent.Claude.ConfigPath,
				Note: "mounted here, and CLAUDE_CONFIG_DIR is set to it"},
		)
	} else if execution.Devcontainer() {
		// Said rather than omitted: a user who expected a dedicated login should
		// find out here, not from a container that turns out to hold their own.
		fields = append(fields, Field{Name: "claude.config_volume", Value: "none",
			Note: "Feat mounts no Claude configuration; the project's own Compose files supply it"})
	}
	fields = append(fields,
		Field{Name: "claude.idle_grace_period", Value: c.Agent.Claude.IdleGracePeriod,
			Note: "idle never means complete"},
		Field{Name: "capabilities.docker", Value: c.Agent.Capabilities.Docker,
			Note: dockerNote(execution.Mode)},
		Field{Name: "capabilities.network", Value: c.Agent.Capabilities.Network,
			Note: "Feat does not provide network data-loss prevention"},
		Field{Name: "capabilities.git", Value: c.Agent.Capabilities.Git,
			Note: "a native worktree shares repository metadata with the agent"},
		Field{Name: "capabilities.github_cli", Value: c.Agent.Capabilities.GitHubCLI},
		Field{Name: "capabilities.gitlab_cli", Value: c.Agent.Capabilities.GitLabCLI},
	)
	return Section{Title: "agent", Fields: fields}
}

func (c *Config) describeRuntime() Section {
	runtime := c.Runtime
	fields := []Field{
		{Name: "provider", Value: runtime.Provider},
		{Name: "start_policy", Value: runtime.StartPolicy, Note: "services start only when you ask"},
		{Name: "project_name_template", Value: runtime.ProjectNameTemplate,
			Note: "one Compose project per task"},
		// Printed because it is usually a default, and a default a user cannot
		// see is a default they cannot check (ADR-062). It is also the answer to
		// the question an exhausted range asks.
		{Name: "port_range", Value: runtime.PortRange,
			Note: "one host port per reachable service per task"},
		// Printed for the same reason and one more: it decides who can reach a
		// service published without an address of its own, and a user who has not
		// set it should be able to read what Feat chose for them rather than
		// assume.
		//
		// Scoped to that case rather than left as a claim about the project,
		// because it is not one. A repository's own Compose file may name an
		// address per publication and Feat keeps it (docs/07 §
		// runtime.bind_address), so a project configured 127.0.0.1 whose
		// repository publishes "0.0.0.0:3000:3000" would otherwise be told here
		// that its services are reachable from this machine alone. What each
		// publication is actually bound on is per publication and is printed
		// beside its address by `feat runtime status`.
		{Name: "bind_address", Value: runtime.BindAddress,
			Note: "where a publication names no address: " + bindAddressNote(runtime.BindAddress)},
		{Name: "services", Value: orNone(strings.Join(c.RuntimeServices(), ", ")),
			Note: "composed of the repositories below"},
	}
	// The composition rather than a file list: each repository brings its own
	// files, resolved against its own checkout, and Feat generates the include
	// document that joins them.
	for _, contribution := range c.RuntimeComposition() {
		fields = append(fields, Field{
			Name:  "composed_of." + contribution.RepositoryID,
			Value: strings.Join(contribution.ComposeFiles, ", "),
			Note:  "resolved against " + contribution.Directory,
		})
	}
	for i, file := range runtime.StaticOverrides {
		fields = append(fields, Field{Name: fmt.Sprintf("static_overrides[%d]", i), Value: file})
	}
	for i, file := range runtime.EnvFiles {
		// The path is printed and the file is not opened. Feat passes it to
		// Compose by path, so nothing it writes can contain a value from it.
		fields = append(fields, Field{Name: fmt.Sprintf("env_files[%d]", i), Value: file, Note: secretMarker})
	}
	return Section{Title: "runtime", Fields: fields}
}

func (c *Config) describeReview() Section {
	return Section{Title: "review", Fields: []Field{
		{Name: "diff.command", Value: renderCommand(c.Review.Diff)},
		{Name: "editor.command", Value: renderCommand(c.Review.Editor), Note: editorNote(c.Review.Editor)},
		{Name: "status.command", Value: renderCommand(c.Review.Status)},
	}}
}

func (c *Config) describeChecks() Section {
	section := Section{Title: "checks"}
	for _, repository := range sortedKeys(c.Checks) {
		checks := c.Checks[repository]
		sorted := append([]Check(nil), checks...)
		sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
		for _, check := range sorted {
			section.Fields = append(section.Fields, Field{
				Name:  repository + "." + check.ID,
				Value: strings.Join(check.Command, " "),
				Note:  "runs on the " + check.Execution,
			})
		}
	}
	return section
}

func (c *Config) describeNotifications() Section {
	return Section{Title: "notifications", Fields: []Field{
		{Name: "desktop", Value: yesNo(c.Notifications.DesktopEnabled())},
		{Name: "idle_grace_period", Value: c.Notifications.IdleGracePeriod},
		{Name: "suppress_while_attached", Value: yesNo(c.Notifications.SuppressedWhileAttached())},
		{Name: "resources.sample_interval", Value: c.Resources.SampleInterval},
	}}
}

func renderCommand(command Command) string {
	if command.Empty() {
		return "(none)"
	}
	return strings.Join(command.Command, " ")
}

func editorNote(command Command) string {
	if command.Empty() {
		return "$EDITOR is not set in this environment"
	}
	return ""
}

func basePolicyNote(policy string) string {
	switch policy {
	case PolicyRemote:
		return "resolved from the remote-tracking branch after a fetch"
	case PolicyLocal:
		return "resolved from the local default branch"
	case PolicyCurrent:
		return "resolved from the ordinary checkout's current commit"
	case PolicyExplicit:
		return "supplied during task preparation"
	default:
		return ""
	}
}

func executionNote(mode string) string {
	switch mode {
	case ModeHost:
		return "no container boundary"
	case ModeDevcontainer:
		// The variable is named because this command cannot read it: it belongs
		// to the daemon's environment, and `feat project show` loads
		// configuration without asking a daemon anything. Naming what overrides
		// the mode is the honest form of a claim about the mode.
		return "the agent runs in a configured Compose service, unless the daemon was started with " + EnvHostAgent
	default:
		return ""
	}
}

// dockerNote says what the declared Docker capability means where the agent
// runs.
//
// `denied` is honest in both modes and means the same thing in neither. Feat
// mounts no socket and installs no client either way; in a container that is a
// rule a launch then enforces against the container it is about to use, and on
// this host the agent is a process of the user the daemon runs as, with that
// user's socket and CLI already on its path. One gloss covering both would have
// to be false in one of them, and it was: a host-mode project was told that no
// Docker socket and no host Docker CLI reach its agent, four lines under
// `execution.mode host (no container boundary)`.
func dockerNote(mode string) string {
	if mode == ModeDevcontainer {
		return "Feat mounts no socket and adds no client; a launch refuses a container that has either"
	}
	return "host execution: the agent runs as the daemon's own user, with that user's Docker"
}

// bindAddressNote says who can reach this project's published services.
//
// It distinguishes the two answers rather than restating the value, because the
// value is what a user cannot judge on sight: "0.0.0.0" and "127.0.0.1" look
// alike in a printed field and differ by whether the whole network the machine
// is on can open the service.
func bindAddressNote(address string) string {
	switch address {
	case "127.0.0.1", "::1":
		return "reachable from this machine only"
	case wildcardAddress, "::":
		return "reachable from every network this machine is on, and from every container on it"
	default:
		return "reachable wherever this address is"
	}
}

func orNone(value string) string {
	if value == "" {
		return "(none)"
	}
	return value
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}
