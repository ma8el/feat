package project

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ma8el/feat/internal/config"
	"github.com/ma8el/feat/internal/domain"
)

// The executables Feat drives on the trusted host.
const (
	gitExecutable    = "git"
	tmuxExecutable   = "tmux"
	dockerExecutable = "docker"
)

// slice8 names the slice that delivers the checks this build cannot run, so
// that a skipped finding says when it stops being skipped.
const slice8 = "delivered by implementation slice 8, which runs the agent's execution environment"

// checker collects findings for one project.
type checker struct {
	runner   Runner
	config   *config.Config
	findings []Finding
}

func (c *checker) add(finding Finding) { c.findings = append(c.findings, finding) }

func (c *checker) ok(check, summary string) {
	c.add(Finding{Check: check, Severity: SeverityOK, Summary: summary})
}

func (c *checker) warn(check, summary, action string) {
	c.add(Finding{Check: check, Severity: SeverityWarning, Summary: summary, Action: action})
}

func (c *checker) fail(check, summary, action string) {
	c.add(Finding{Check: check, Severity: SeverityError, Summary: summary, Action: action})
}

func (c *checker) skip(check, summary, action string) {
	c.add(Finding{Check: check, Severity: SeveritySkipped, Summary: summary, Action: action})
}

// run performs every check for one project.
func (c *checker) run(ctx context.Context) []Finding {
	c.checkRepositories(ctx)
	c.checkWorktreeRoot()
	c.checkExecution(ctx)
	c.checkRuntime(ctx)
	c.checkReviewCommands()
	c.checkChecks()
	c.checkCapabilities()
	return c.findings
}

// checkRepositories checks that each configured repository is where the
// configuration says it is, and is what it says it is.
func (c *checker) checkRepositories(ctx context.Context) {
	for _, id := range c.config.RepositoryIDs() {
		repository, _ := c.config.Repository(id)
		check := "repositories." + id

		if domain.DefaultAccess(repository.DefaultAccess) == domain.DefaultAccessOmitted {
			c.skip(check, "omitted from tasks by default", "")
			continue
		}

		info, err := os.Stat(repository.HostPath)
		switch {
		case errors.Is(err, os.ErrNotExist):
			c.fail(check, fmt.Sprintf("%s does not exist", repository.HostPath),
				"clone the repository there, or correct "+check+".host_path")
			continue
		case err != nil:
			c.fail(check, fmt.Sprintf("%s cannot be read: %v", repository.HostPath, err),
				"check the directory's permissions")
			continue
		case !info.IsDir():
			c.fail(check, fmt.Sprintf("%s is not a directory", repository.HostPath),
				"correct "+check+".host_path")
			continue
		}

		if _, err := c.runner.Run(ctx, repository.HostPath, gitExecutable, "rev-parse", "--git-dir"); err != nil {
			c.fail(check, fmt.Sprintf("%s is not a Git repository", repository.HostPath),
				"correct "+check+".host_path, or run `git init` there")
			continue
		}
		c.ok(check, repository.HostPath)
		c.checkRemote(ctx, id, repository)
		c.checkDefaultBranch(ctx, id, repository)
	}
}

// checkRemote checks the remote a base policy resolves against.
func (c *checker) checkRemote(ctx context.Context, id string, repository config.Repository) {
	check := "repositories." + id + ".remote"

	if _, err := c.runner.Run(ctx, repository.HostPath, gitExecutable,
		"remote", "get-url", repository.Remote); err != nil {
		summary := fmt.Sprintf("%s has no remote %q", repository.HostPath, repository.Remote)
		action := "add it with `git remote add`, or correct " + check
		// A remote matters only when a base is resolved from it. Reporting a
		// missing remote as an error for a project that resolves bases locally
		// would be a diagnostic about a fact Feat never uses.
		if c.config.Git.BasePolicy == config.PolicyRemote {
			c.fail(check, summary+", and git.base_policy is "+config.PolicyRemote, action)
		} else {
			c.warn(check, summary, action)
		}
		return
	}
	c.ok(check, repository.Remote)
}

// checkDefaultBranch checks the branch a base policy resolves.
//
// A missing branch is a warning rather than an error throughout: the
// remote-tracking ref appears after the first fetch, which Feat performs before
// it resolves a base, so a fresh clone can legitimately be missing it now and
// have it by the time it matters.
func (c *checker) checkDefaultBranch(ctx context.Context, id string, repository config.Repository) {
	check := "repositories." + id + ".default_branch"

	ref := "refs/heads/" + repository.DefaultBranch
	if c.config.Git.BasePolicy == config.PolicyRemote {
		ref = "refs/remotes/" + repository.Remote + "/" + repository.DefaultBranch
	}

	if _, err := c.runner.Run(ctx, repository.HostPath, gitExecutable,
		"rev-parse", "--verify", "--quiet", ref); err != nil {
		c.warn(check, fmt.Sprintf("%s has no %s", repository.HostPath, ref),
			"run `git fetch "+repository.Remote+"` there, or correct "+check)
		return
	}
	c.ok(check, ref)
}

// checkWorktreeRoot checks that Feat can create task worktrees.
//
// The directory itself does not exist until the first task, so what is checked
// is the deepest part of the path that already exists: whether it is a
// directory, and whether it can be written to.
func (c *checker) checkWorktreeRoot() {
	const check = "git.worktree_root"

	root := c.config.Git.WorktreeRoot
	existing := nearestExisting(root)
	if existing == "" {
		c.warn(check, fmt.Sprintf("no part of %s exists yet", root),
			"Feat creates it when the first task launches")
		return
	}

	info, err := os.Stat(existing)
	if err != nil {
		c.fail(check, fmt.Sprintf("%s cannot be read: %v", existing, err), "check the directory's permissions")
		return
	}
	if !info.IsDir() {
		c.fail(check, fmt.Sprintf("%s is not a directory", existing), "correct "+check)
		return
	}
	if err := writable(existing); err != nil {
		c.fail(check, fmt.Sprintf("%s cannot be written to: %v", existing, err),
			"grant write access to "+existing+", or correct "+check)
		return
	}
	c.ok(check, root)
}

// checkExecution checks the agent's execution environment.
func (c *checker) checkExecution(ctx context.Context) {
	execution := c.config.Agent.Execution
	if !execution.Devcontainer() {
		c.ok("agent.execution.mode", "host, with no container boundary around the agent")
		c.skipAgentEnvironmentChecks()
		return
	}

	files := c.checkComposeFiles("agent.execution.compose_files", execution.ComposeFiles)
	if files {
		c.checkComposeService(ctx, "agent.execution.service", execution.ComposeFiles, execution.Service)
	}
	c.skipAgentEnvironmentChecks()
}

// skipAgentEnvironmentChecks records the checks FR-PROJ-004 asks for that this
// build cannot perform.
//
// Validating the agent executable, the container user, and provider CLI
// authentication has to happen inside the environment where the agent will run
// them, and nothing starts that environment until slice 8. Reporting them as
// skipped rather than omitting them keeps `feat doctor` honest about its own
// coverage: the checks are named, and so is the reason they did not run.
func (c *checker) skipAgentEnvironmentChecks() {
	c.skip("agent.executable", "the agent executable is not checked in this build", slice8)
	if c.config.Agent.Execution.Devcontainer() {
		c.skip("agent.execution.user",
			fmt.Sprintf("the running process is not checked to be %q in this build", c.config.Agent.Execution.User),
			slice8)
	}

	for _, capability := range []struct {
		field string
		tool  string
		level string
	}{
		{"agent.capabilities.github_cli", "gh", c.config.Agent.Capabilities.GitHubCLI},
		{"agent.capabilities.gitlab_cli", "glab", c.config.Agent.Capabilities.GitLabCLI},
	} {
		if capability.level == config.CLIDisabled {
			c.ok(capability.field, "disabled")
			continue
		}
		c.skip(capability.field,
			fmt.Sprintf("%s is %s, and its installation and authentication are not checked in this build",
				capability.tool, capability.level),
			slice8)
	}
}

// checkRuntime checks the application runtime inputs.
func (c *checker) checkRuntime(ctx context.Context) {
	runtime := c.config.Runtime
	if runtime == nil {
		return
	}

	files := c.checkComposeFiles("runtime.compose_files", runtime.ComposeFiles)
	c.checkComposeFiles("runtime.static_overrides", runtime.StaticOverrides)
	c.checkEnvFiles(runtime.EnvFiles)

	if files {
		for i, service := range runtime.Services {
			c.checkComposeService(ctx, fmt.Sprintf("runtime.services[%d]", i),
				append(runtime.ComposeFiles, runtime.StaticOverrides...), service)
		}
	}

	for name := range runtime.ExternalResources {
		c.ok("runtime.external_resources."+name, "referenced, never created or destroyed by Feat")
	}
}

// checkComposeFiles checks that configured Compose files exist, and reports
// whether all of them do.
func (c *checker) checkComposeFiles(check string, files []string) bool {
	complete := len(files) > 0
	for i, file := range files {
		field := fmt.Sprintf("%s[%d]", check, i)
		if _, err := os.Stat(file); err != nil {
			complete = false
			c.fail(field, fmt.Sprintf("%s cannot be read: %v", file, err),
				"correct "+field+", or create the file")
			continue
		}
		c.ok(field, file)
	}
	return complete
}

// checkEnvFiles checks that environment files exist, without reading them.
//
// Only the path and the file's metadata are examined. docs/05-security-model.md
// requires that Feat avoid reading their values, and a diagnostic is exactly
// the place where a value that was read would end up printed.
func (c *checker) checkEnvFiles(files []string) {
	for i, file := range files {
		field := fmt.Sprintf("runtime.env_files[%d]", i)
		info, err := os.Stat(file)
		if err != nil {
			// Compose fails on a missing env file, so this is not cosmetic, but
			// it is the runtime's problem rather than the project's: a project
			// whose services are never started still works.
			c.warn(field, fmt.Sprintf("%s cannot be read: %v", file, err),
				"create the file, or correct "+field)
			continue
		}
		mode := info.Mode().Perm()
		summary := fmt.Sprintf("%s (%d bytes, mode %#o, contents not read)", file, info.Size(), mode)
		if mode&0o077 != 0 {
			c.warn(field, summary, "restrict it to your user with `chmod 600`")
			continue
		}
		c.ok(field, summary)
	}
}

// checkComposeService checks that a Compose service exists.
//
// Only `config --services` is ever run, which lists service names. The plain
// `docker compose config` renders the fully resolved project, including values
// taken from environment files, and Feat must not read those (FR-PROJ-004 and
// docs/05-security-model.md).
func (c *checker) checkComposeService(ctx context.Context, check string, files []string, service string) {
	if _, err := c.runner.Look(dockerExecutable); err != nil {
		c.skip(check, fmt.Sprintf("service %q is not checked: %v", service, err),
			"install Docker to check configured service names")
		return
	}

	args := []string{"compose"}
	for _, file := range files {
		args = append(args, "--file", file)
	}
	args = append(args, "config", "--services")

	output, err := c.runner.Run(ctx, "", dockerExecutable, args...)
	if err != nil {
		// Compose could not read the project. That is worth reporting and worth
		// not guessing about: the cause may be an unset variable, a missing
		// include, or a Docker installation that cannot run.
		c.warn(check, fmt.Sprintf("Docker Compose could not list the services: %v", err),
			"run `docker compose "+strings.Join(args[1:], " ")+"` to see the whole message")
		return
	}

	for _, name := range strings.Split(output, "\n") {
		if strings.TrimSpace(name) == service {
			c.ok(check, service)
			return
		}
	}
	c.fail(check, fmt.Sprintf("the Compose files define no service %q", service),
		"correct "+check+", or add the service to the Compose files")
}

// checkReviewCommands checks that the configured external commands exist.
func (c *checker) checkReviewCommands() {
	for _, command := range []struct {
		check string
		value config.Command
	}{
		{"review.diff.command", c.config.Review.Diff},
		{"review.editor.command", c.config.Review.Editor},
		{"review.status.command", c.config.Review.Status},
	} {
		if command.value.Empty() {
			c.warn(command.check, "no command is configured",
				"set $EDITOR, or configure "+command.check)
			continue
		}
		c.lookUp(command.check, command.value.Command[0])
	}
}

// checkChecks checks the verification commands that run on the host.
func (c *checker) checkChecks() {
	for repository, checks := range c.config.Checks {
		for _, check := range checks {
			field := "checks." + repository + "." + check.ID
			if check.Execution != config.ExecutionHost {
				c.skip(field, fmt.Sprintf("%q runs in the agent's environment and is not checked in this build",
					strings.Join(check.Command, " ")), slice8)
				continue
			}
			c.lookUp(field, check.Command[0])
		}
	}
}

// lookUp reports whether a configured program is installed.
func (c *checker) lookUp(check, program string) {
	path, err := c.runner.Look(program)
	if err != nil {
		c.warn(check, err.Error(), "install "+program+", or configure another command for "+check)
		return
	}
	c.ok(check, path)
}

// checkCapabilities reports the declared capabilities.
//
// They are validated by internal/config and enforced by the execution adapter.
// Reporting them here is what makes the security profile of a project visible
// in one place, next to the mounts it grants.
func (c *checker) checkCapabilities() {
	capabilities := c.config.Agent.Capabilities
	c.ok("agent.capabilities.docker",
		capabilities.Docker+": no Docker socket and no host Docker CLI reach the agent")
	c.ok("agent.capabilities.network",
		capabilities.Network+": Feat does not provide network data-loss prevention")
	c.ok("agent.capabilities.git",
		capabilities.Git+": a native worktree shares repository metadata with the agent")
}

// diagnoseHost checks the tools Feat drives on this machine.
//
// Which tools matter depends on what the configured projects ask for, so this
// runs after them: reporting a missing Docker Compose as an error on a machine
// where no project uses a container would be a diagnostic about nothing.
func diagnoseHost(ctx context.Context, opts Options, projects []Diagnosis) []Finding {
	host := &checker{runner: opts.Runner}

	host.version(ctx, gitExecutable, "git", []string{"--version"}, SeverityError,
		"install Git; Feat drives it for every branch, worktree, and diff")
	host.version(ctx, tmuxExecutable, "tmux", []string{"-V"}, SeverityError,
		"install tmux; Feat runs every agent session in it")

	severity, action := SeverityWarning, "install Docker to use a devcontainer or an application runtime"
	if needsCompose(projects) {
		severity = SeverityError
		action = "install Docker; a configured project runs its agent or its services through Compose"
	}
	host.version(ctx, dockerExecutable, "docker compose", []string{"compose", "version"}, severity, action)

	return host.findings
}

// version reports an executable's presence and version.
func (c *checker) version(ctx context.Context, executable, check string, args []string, absent Severity, action string) {
	if _, err := c.runner.Look(executable); err != nil {
		c.add(Finding{Check: check, Severity: absent, Summary: err.Error(), Action: action})
		return
	}
	output, err := c.runner.Run(ctx, "", executable, args...)
	if err != nil {
		c.add(Finding{
			Check:    check,
			Severity: absent,
			Summary:  fmt.Sprintf("%s is installed but did not report a version: %v", executable, err),
			Action:   action,
		})
		return
	}
	c.ok(check, firstLine(output))
}

// needsCompose reports whether any diagnosed project drives Docker Compose.
func needsCompose(projects []Diagnosis) bool {
	for _, project := range projects {
		if project.Config == nil {
			continue
		}
		if project.Config.Agent.Execution.Devcontainer() || project.Config.HasRuntime() {
			return true
		}
	}
	return false
}

// nearestExisting returns the deepest ancestor of a path that exists, or an
// empty string when none does.
func nearestExisting(path string) string {
	for current := path; ; {
		if _, err := os.Lstat(current); err == nil {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
}

// writable reports whether a directory can be written to, by trying rather than
// by interpreting permission bits, which do not account for the effective user,
// ownership, or a read-only mount.
func writable(dir string) error {
	probe, err := os.CreateTemp(dir, ".feat-doctor-*")
	if err != nil {
		return err
	}
	name := probe.Name()
	_ = probe.Close()
	return os.Remove(name)
}
