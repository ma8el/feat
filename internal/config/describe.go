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
// It is a table of its own because it is the mapping a devcontainer task
// depends on and the one a user most often needs to check: a repository at the
// wrong path is a task that compiles nothing.
func (c *Config) Mounts() []Mount {
	mounts := make([]Mount, 0, len(c.Repositories))
	for _, id := range c.RepositoryIDs() {
		repository := c.Repositories[id]
		mounts = append(mounts, Mount{
			RepositoryID:  id,
			HostPath:      repository.HostPath,
			ContainerPath: repository.ContainerPath,
			DefaultAccess: repository.DefaultAccess,
			Primary:       id == c.Project.PrimaryRepository,
		})
	}
	return mounts
}

// Mount is one repository's place on the host and in the execution
// environment.
type Mount struct {
	// RepositoryID identifies the repository within the project.
	RepositoryID string
	// HostPath is the ordinary checkout, after expansion.
	HostPath string
	// ContainerPath is where task worktrees are mounted, empty for host-native
	// execution.
	ContainerPath string
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
			Field{Name: id + ".container_path", Value: orNone(repository.ContainerPath)},
			Field{Name: id + ".default_branch", Value: repository.DefaultBranch},
			Field{Name: id + ".remote", Value: repository.Remote},
			Field{Name: id + ".default_access", Value: repository.DefaultAccess},
		)
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
			Note: "no Docker socket and no host Docker CLI reach the agent"},
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
		{Name: "services", Value: strings.Join(runtime.Services, ", ")},
	}
	for i, file := range runtime.ComposeFiles {
		fields = append(fields, Field{Name: fmt.Sprintf("compose_files[%d]", i), Value: file})
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
		return "the agent runs in a configured Compose service"
	default:
		return ""
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
