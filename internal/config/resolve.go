package config

import (
	"errors"
	"fmt"
	"path/filepath"
	"time"
)

// Defaults applied by Resolve.
//
// They are filled into the configuration rather than applied at the point of
// use, so that `feat project show` prints the values Feat will act on. A
// default a user cannot see is a default they cannot check.
const (
	defaultBranchTemplate      = "feat/{task_key}-{slug}"
	defaultWorktreeDir         = "worktrees"
	defaultRemote              = "origin"
	defaultBranch              = "main"
	defaultControlPath         = "/feat"
	defaultProjectNameTemplate = "feat-{project_id}-{task_id}"
	defaultIdleGracePeriod     = "5s"
	defaultSampleInterval      = "2s"
)

// defaultDiffCommand compares a repository against the base commit recorded for
// this task, which is the only comparison review is allowed to make.
func defaultDiffCommand() []string { return []string{"git", "diff", "{base_commit}"} }

// defaultStatusCommand shows the working tree and branch of one repository.
func defaultStatusCommand() []string { return []string{"git", "status", "--short", "--branch"} }

// Resolve expands paths and fills defaults.
//
// It touches no file other than the one already read: expansion needs the home
// directory and the environment, both of which are supplied. Whether a resolved
// path exists on this machine is a host question, and it belongs to diagnostics
// rather than to loading (docs/04-functional-specification.md, FR-PROJ-004).
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
	if err := c.resolveReview(opts); err != nil {
		return err
	}
	if err := c.resolveIntervals(); err != nil {
		return err
	}

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
		// Under the state directory, one directory per project and task: the
		// default has to be deterministic, and it has to keep two tasks from
		// ever resolving to the same worktree.
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

	capabilities := &c.Agent.Capabilities
	if capabilities.Docker == "" {
		capabilities.Docker = CapabilityDenied
	}
	if capabilities.Network == "" {
		capabilities.Network = CapabilityUnrestricted
	}
	if capabilities.Git == "" {
		capabilities.Git = CapabilityFull
	}
	if capabilities.GitHubCLI == "" {
		capabilities.GitHubCLI = CLIDisabled
	}
	if capabilities.GitLabCLI == "" {
		capabilities.GitLabCLI = CLIDisabled
	}

	execution := &c.Agent.Execution
	if execution.Devcontainer() {
		if execution.ControlPath == "" {
			execution.ControlPath = defaultControlPath
		}
		// The agent starts where the user works: the primary repository's mount
		// point. A project that wants another directory says so.
		if execution.WorkingDirectory == "" {
			if primary, ok := c.Primary(); ok {
				execution.WorkingDirectory = primary.ContainerPath
			}
		}
	}

	files, err := expandAll(opts, "agent.execution.compose_files", execution.ComposeFiles)
	if err != nil {
		return c.problem(err)
	}
	execution.ComposeFiles = files
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

	for _, field := range []struct {
		path  string
		value *[]string
	}{
		{"runtime.compose_files", &runtime.ComposeFiles},
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

func (c *Config) resolveReview(opts Options) error {
	if c.Review.Diff.Empty() {
		c.Review.Diff.Command = defaultDiffCommand()
	}
	if c.Review.Status.Empty() {
		c.Review.Status.Command = defaultStatusCommand()
	}
	if c.Review.Editor.Empty() {
		// FR-REV-003 defaults the editor to $EDITOR. An unset $EDITOR leaves
		// the command empty rather than guessing an editor: diagnostics report
		// it, and a project that never opens an editor is still valid.
		if editor := opts.Env.Getenv("EDITOR"); editor != "" {
			c.Review.Editor.Command = []string{editor, "{repository_path}"}
		}
	}
	return nil
}

// resolveIntervals parses the durations, which are held as strings so that a
// malformed one is reported against its field rather than by the YAML decoder,
// which cannot name the field a custom scalar type failed in.
func (c *Config) resolveIntervals() error {
	if c.Notifications.IdleGracePeriod == "" {
		c.Notifications.IdleGracePeriod = defaultIdleGracePeriod
	}
	if c.Resources.SampleInterval == "" {
		c.Resources.SampleInterval = defaultSampleInterval
	}

	for _, field := range []struct {
		path   string
		value  string
		target *time.Duration
	}{
		{"agent.claude.idle_grace_period", c.Agent.Claude.IdleGracePeriod, &c.Agent.Claude.idleGracePeriod},
		{"notifications.idle_grace_period", c.Notifications.IdleGracePeriod, &c.Notifications.idleGracePeriod},
		{"resources.sample_interval", c.Resources.SampleInterval, &c.Resources.sampleInterval},
	} {
		parsed, err := time.ParseDuration(field.value)
		if err != nil {
			return c.problem(&fieldError{
				path:   field.path,
				reason: fmt.Sprintf("must be a duration such as %q, but is %q", defaultIdleGracePeriod, field.value),
			})
		}
		if parsed < 0 {
			return c.problem(&fieldError{
				path:   field.path,
				reason: fmt.Sprintf("must not be negative, but is %q", field.value),
			})
		}
		*field.target = parsed
	}
	return nil
}

// expand resolves a leading "~" and requires the result to be absolute.
//
// A template placeholder is left in place: git.worktree_root is expanded here
// and completed per task in slice 4, so "~/x/{task_id}" has to survive this
// step intact.
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

// problem wraps a resolution failure with the file it came from, so that it
// reads like every other configuration error and can be shown in place.
func (c *Config) problem(err error) error {
	var field *fieldError
	if errors.As(err, &field) {
		return &Error{
			File:     c.path,
			source:   c.source,
			Problems: []Problem{{Path: field.path, Reason: field.reason}},
		}
	}
	return err
}
