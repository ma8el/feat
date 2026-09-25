package config

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// Defaults applied by Resolve. They are filled into the configuration rather than
// applied at the point of use, so `feat project show` prints the values Feat will act
// on and the user can check them.
const (
	defaultBranchTemplate      = "feat/{task_key}-{slug}"
	defaultWorktreeDir         = "worktrees"
	defaultRemote              = "origin"
	defaultBranch              = "main"
	defaultControlPath         = "/feat"
	defaultClaudeConfigPath    = "/feat-claude"
	defaultProjectNameTemplate = "feat-{project_id}-{task_id}"
	defaultIdleGracePeriod     = "5s"
	defaultSampleInterval      = "2s"

	// defaultPortRange is where Feat publishes a task's reachable services when a
	// project names no range of its own. These thousand ports sit above the privileged
	// range and below the ephemeral ports the kernel hands out, so an allocation
	// neither needs privilege nor collides with a socket the machine opened itself.
	defaultPortRange = "21000-21999"

	// defaultBindAddress is the host address an allocated port is published on when
	// the project's own Compose files named none.
	//
	// Loopback rather than every interface, because Feat chose this host port and
	// nobody asked for the service to answer on the network the machine happens to be
	// on. A port on every interface is also reachable from every container on the
	// machine, which puts one task's containers inside another's.
	defaultBindAddress = "127.0.0.1"
)

// defaultDiffCommand compares a repository against the base commit recorded for
// this task, which is the only comparison review is allowed to make.
func defaultDiffCommand() []string { return []string{"git", "diff", "{base_commit}"} }

// defaultStatusCommand shows the working tree and branch of one repository.
func defaultStatusCommand() []string { return []string{"git", "status", "--short", "--branch"} }

// Resolve expands paths and fills defaults. It touches no file other than the one
// already read, because expansion needs only the home directory and the environment,
// both of which are supplied. Whether a resolved path exists on this machine is a
// host question and belongs to diagnostics
// (docs/04-functional-specification.md, FR-PROJ-004).
func (c *Config) Resolve(opts Options) error {
	if c.resolved {
		return nil
	}

	if err := c.resolveProject(); err != nil {
		return err
	}
	if err := c.resolveRepositories(opts); err != nil {
		return err
	}
	if err := c.resolveGit(opts); err != nil {
		return err
	}
	if err := c.resolveAgent(opts); err != nil {
		return err
	}
	if err := c.resolveRuntime(opts); err != nil {
		return err
	}
	c.resolveTracker()

	c.resolved = true
	return nil
}

func (c *Config) resolveProject() error {
	if c.Project.Name == "" {
		c.Project.Name = c.Project.ID
	}
	return nil
}

func (c *Config) resolveRepositories(opts Options) error {
	for _, id := range c.RepositoryIDs() {
		repository := c.Repositories[id]

		if repository.Name == "" {
			repository.Name = id
		}
		if repository.Remote == "" {
			repository.Remote = defaultRemote
		}
		if repository.DefaultBranch == "" {
			repository.DefaultBranch = defaultBranch
		}

		expanded, err := expand(opts, "repositories."+id+".host_path", repository.HostPath)
		if err != nil {
			return c.problem(err)
		}
		repository.HostPath = expanded

		if repository.Runtime != nil {
			// Against the repository's own checkout, which is the project directory of
			// its include entry. A repository names the files it brings the way it
			// would name them to Compose in its own directory, so nothing relative
			// crosses a repository boundary.
			files, err := expandUnder(opts, "repositories."+id+".runtime.compose_files",
				repository.HostPath, repository.Runtime.ComposeFiles)
			if err != nil {
				return c.problem(err)
			}
			runtime := *repository.Runtime
			runtime.ComposeFiles = files
			repository.Runtime = &runtime
		}

		c.Repositories[id] = repository
	}
	return nil
}

func (c *Config) resolveGit(opts Options) error {
	if c.Git.BasePolicy == "" {
		c.Git.BasePolicy = PolicyRemote
	}
	if c.Git.BranchTemplate == "" {
		c.Git.BranchTemplate = defaultBranchTemplate
	}
	if c.Git.WorktreeRoot == "" && opts.StateDir != "" {
		// Under the state directory, one directory per project and task, so the
		// default is deterministic and two tasks never resolve to the same worktree.
		c.Git.WorktreeRoot = filepath.Join(
			opts.StateDir, defaultWorktreeDir, "{project_id}", "{task_id}")
	}

	expanded, err := expand(opts, "git.worktree_root", c.Git.WorktreeRoot)
	if err != nil {
		return c.problem(err)
	}
	c.Git.WorktreeRoot = expanded
	return nil
}

func (c *Config) resolveAgent(opts Options) error {
	if c.Agent.Provider == "" {
		c.Agent.Provider = ProviderClaude
	}
	if c.Agent.Claude.IdleGracePeriod == "" {
		c.Agent.Claude.IdleGracePeriod = defaultIdleGracePeriod
	}

	if c.Agent.Capabilities.Docker == "" {
		c.Agent.Capabilities.Docker = CapabilityDenied
	}

	execution := &c.Agent.Execution
	if execution.Devcontainer() {
		if execution.ControlPath == "" {
			execution.ControlPath = defaultControlPath
		}
		// Only where there is a volume to mount. Defaulting a path for a configuration
		// that mounts nothing would put a value in `feat project show` that nothing
		// ever uses.
		if c.Agent.Claude.ConfigVolume != "" && c.Agent.Claude.ConfigPath == "" {
			c.Agent.Claude.ConfigPath = defaultClaudeConfigPath
		}
		// The agent starts where the user works: the primary repository's mount point
		// in the agent's own container. A project that wants another directory says
		// so.
		if execution.WorkingDirectory == "" {
			if primary, ok := c.Primary(); ok {
				execution.WorkingDirectory = primary.Agent.ContainerPath
			}
		}
	}

	files, err := expandAll(opts, "agent.execution.compose_files", execution.ComposeFiles)
	if err != nil {
		return c.problem(err)
	}
	execution.ComposeFiles = files

	// Held as a string and parsed here, so a malformed duration is reported against
	// its own field rather than by the YAML decoder, which cannot name the field a
	// custom scalar type failed in.
	//
	// It is the one duration a project still configures. It decides when Claude's
	// ended turn counts as idle, and how an agent is driven is a fact about the
	// project rather than about the machine (ADR-079).
	grace, err := parseInterval("agent.claude.idle_grace_period", c.Agent.Claude.IdleGracePeriod)
	if err != nil {
		return c.problem(err)
	}
	c.Agent.Claude.idleGracePeriod = grace
	return nil
}

func (c *Config) resolveRuntime(opts Options) error {
	if c.Runtime == nil {
		return nil
	}
	runtime := c.Runtime

	if runtime.Provider == "" {
		runtime.Provider = ProviderCompose
	}
	if runtime.StartPolicy == "" {
		runtime.StartPolicy = StartManual
	}
	if runtime.ProjectNameTemplate == "" {
		runtime.ProjectNameTemplate = defaultProjectNameTemplate
	}
	if runtime.PortRange == "" {
		runtime.PortRange = defaultPortRange
	}
	if runtime.BindAddress == "" {
		runtime.BindAddress = defaultBindAddress
	}
	// Parsed here and validated in Validate, so a range that is not a range at all is
	// reported against its own field rather than as a runtime action failing later
	// with nothing to allocate from.
	parsed, err := ParsePortRange(runtime.PortRange)
	if err != nil {
		return c.problem(&fieldError{path: "runtime.port_range", reason: err.Error()})
	}
	runtime.portRange = parsed

	for _, field := range []struct {
		path  string
		value *[]string
	}{
		{"runtime.static_overrides", &runtime.StaticOverrides},
		{"runtime.env_files", &runtime.EnvFiles},
	} {
		expanded, err := expandAll(opts, field.path, *field.value)
		if err != nil {
			return c.problem(err)
		}
		*field.value = expanded
	}
	return nil
}

// resolveTracker fills the tracker's kind. A configured command is the only kind
// there is, so a project need not write it. It is filled in for the reason every
// other default is: `feat project show` prints the values Feat will act on.
func (c *Config) resolveTracker() {
	if c.Tracker == nil {
		return
	}
	if c.Tracker.Kind == "" {
		c.Tracker.Kind = TrackerCommand
	}
}

// resolveReviewSection fills the review commands Feat has a default for. It is a
// function on the section rather than a method on a document, because two documents
// hold one: the settings file, and a project's copy until it is moved (ADR-079).
func resolveReviewSection(opts Options, review *ReviewSection) {
	if review.Diff.Empty() {
		review.Diff.Command = defaultDiffCommand()
	}
	if review.Status.Empty() {
		review.Status.Command = defaultStatusCommand()
	}
	// FR-REV-003 defaults the editor to $EDITOR. An unset $EDITOR leaves the command
	// empty rather than guessing an editor: diagnostics report it, and a machine that
	// never opens an editor is still configured.
	//
	// An Options with no environment reader reads no variables rather than falling
	// through to the process's own, because resolution has no hidden inputs.
	//
	// $EDITOR is split on whitespace, because it holds a command rather than a
	// program: `code -w` and `emacsclient -nw` are ordinary values of it, and a single
	// element would make the whole string the name of an executable to look up. An
	// editor whose path contains a space is the cost, and every tool that reads this
	// variable pays it.
	if review.Editor.Empty() && opts.Env.Getenv != nil {
		if fields := strings.Fields(opts.Env.Getenv("EDITOR")); len(fields) > 0 {
			review.Editor.Command = append(fields, "{repository_path}")
		}
	}
}

// parseInterval reads one duration field, reporting a malformed or negative value
// against the field it was written in. The durations are held as strings because a
// custom scalar type would fail inside the YAML decoder, which cannot name the field
// it was decoding. Two documents hold one: a project's provider grace, and the
// machine's two settings.
func parseInterval(path, value string) (time.Duration, error) {
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, &fieldError{
			path:   path,
			reason: fmt.Sprintf("must be a duration such as %q, but is %q", defaultIdleGracePeriod, value),
		}
	}
	if parsed < 0 {
		return 0, &fieldError{
			path:   path,
			reason: fmt.Sprintf("must not be negative, but is %q", value),
		}
	}
	return parsed, nil
}

// expand resolves a leading "~" and requires the result to be absolute. A template
// placeholder is left in place: git.worktree_root is completed per task by the Git
// adapter, so "~/x/{task_id}" has to survive this step intact.
func expand(opts Options, path, value string) (string, error) {
	if value == "" {
		return "", nil
	}
	expanded, err := opts.Env.Expand(value)
	if err != nil {
		return "", &fieldError{path: path, reason: err.Error()}
	}
	if !filepath.IsAbs(expanded) {
		return "", &fieldError{
			path:   path,
			reason: fmt.Sprintf("must be an absolute path, but %q resolves to %q", value, expanded),
		}
	}
	return expanded, nil
}

// expandUnder resolves every path in a list against a base directory. A path that
// expands to an absolute one is taken as it stands, and anything else is joined to
// the base, so a repository can name the Compose files it brings the way it would
// name them standing in its own checkout. An unknown base is refused, because joining
// onto nothing would produce a path relative to wherever the daemon was started.
func expandUnder(opts Options, path, base string, values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	expanded := make([]string, len(values))
	for i, value := range values {
		field := fmt.Sprintf("%s[%d]", path, i)
		if value == "" {
			return nil, &fieldError{path: field, reason: "must name a file"}
		}
		one, err := opts.Env.Expand(value)
		if err != nil {
			return nil, &fieldError{path: field, reason: err.Error()}
		}
		if !filepath.IsAbs(one) {
			if base == "" {
				return nil, &fieldError{path: field, reason: fmt.Sprintf(
					"is %q, which is relative to the repository's checkout, and the repository has no host_path to resolve it against",
					value)}
			}
			one = filepath.Join(base, one)
		}
		expanded[i] = filepath.Clean(one)
	}
	return expanded, nil
}

// expandAll resolves every path in a list, naming the element that failed.
func expandAll(opts Options, path string, values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	expanded := make([]string, len(values))
	for i, value := range values {
		one, err := expand(opts, fmt.Sprintf("%s[%d]", path, i), value)
		if err != nil {
			return nil, err
		}
		expanded[i] = one
	}
	return expanded, nil
}

// fieldError is a problem found while resolving. Resolution stops at the first
// one, because a path that could not be expanded makes every later rule about
// that path meaningless.
type fieldError struct {
	path   string
	reason string
}

func (e *fieldError) Error() string { return e.path + ": " + e.reason }

// problem wraps a resolution failure with the file it came from, so it reads like
// every other configuration error and can be shown in place.
func (c *Config) problem(err error) error { return asProblem(c.path, c.source, err) }

// asProblem wraps a resolution failure with the file it came from. It takes the file
// and its bytes rather than a document, because both documents this package
// resolves, a project's and the machine's settings, report a bad field the same way.
func asProblem(file string, source []byte, err error) error {
	var field *fieldError
	if errors.As(err, &field) {
		return &Error{
			File:     file,
			source:   source,
			Problems: []Problem{{Path: field.path, Reason: field.reason}},
		}
	}
	return err
}
