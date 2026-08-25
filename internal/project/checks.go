package project

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ma8el/feat/internal/agent/claude"
	"github.com/ma8el/feat/internal/config"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/execution/compose"
	"github.com/ma8el/feat/internal/forge"
	"github.com/ma8el/feat/internal/forge/gitlab"
)

// The executables Feat drives on the trusted host.
const (
	gitExecutable    = "git"
	tmuxExecutable   = "tmux"
	dockerExecutable = "docker"
)

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
	c.checkPublication(ctx)
	c.checkChecks(ctx)
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
		c.checkPrePush(ctx, id, repository)
	}
}

// checkPrePush reports what a host-side publication will not run.
//
// Feat pushes with hooks disabled, because a task's worktrees share `.git/hooks`
// with this checkout and the agent can write them: approving a publication must
// not be how a user runs what the agent left there (ADR-050, ADR-070). Disabling
// them costs nothing in a repository that has none, and it does not cost nothing
// everywhere — a `pre-push` hook may be what scans for secrets before anything
// leaves the machine, and Feat's publication would then be the one route out
// that skips the check.
//
// It is reported here for the reason a tracker's output is validated here: what
// Feat will not run is better learned when the user asks whether the project is
// configured than at the moment they are approving a publication. It warns
// rather than fails, because Feat cannot tell a load-bearing hook from a
// personal convenience — which is what OQ-015 leaves open — and it says nothing
// where there is no hook, because a check with nothing to report reports nothing
// (ADR-028).
//
// A repository with no forge is never published, so it is never asked.
//
// The check is named for what it is about rather than for the configuration
// field that decides whether it runs. `repositories.<id>.forge` was the first
// name, and it reads as a check on the forge declaration — which is validated
// when the configuration loads, and is not this.
func (c *checker) checkPrePush(ctx context.Context, id string, repository config.Repository) {
	if repository.Forge == nil {
		return
	}
	check := "repositories." + id + ".publication"

	if path, err := c.runner.Run(ctx, repository.HostPath, gitExecutable,
		"config", "--get", "core.hooksPath"); err == nil && strings.TrimSpace(path) != "" {
		// An error is Git's answer that the setting is not there, which is the
		// ordinary case: `config --get` exits 1 for an unset key.
		c.warn(check, fmt.Sprintf("core.hooksPath is %s, and Feat's push runs no hook from it",
			strings.TrimSpace(path)),
			"push by hand if one of those hooks has to run before your work leaves this machine")
	}

	hooks, err := c.runner.Run(ctx, repository.HostPath, gitExecutable, "rev-parse", "--git-path", "hooks")
	if err != nil {
		return
	}
	hooks = strings.TrimSpace(hooks)
	if !filepath.IsAbs(hooks) {
		hooks = filepath.Join(repository.HostPath, hooks)
	}
	// The filesystem rather than Git: Git has no command that reports which
	// hooks a repository has without running one, and running one is what this
	// exists to say Feat does not do. A file named exactly `pre-push` is the
	// hook; Git's own examples are installed as `pre-push.sample` and never run.
	hook := filepath.Join(hooks, "pre-push")
	if info, err := os.Stat(hook); err == nil && !info.IsDir() {
		c.warn(check, fmt.Sprintf("%s exists and Feat's push does not run it", hook),
			"push by hand if that hook has to run before your work leaves this machine")
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
//
// Both modes report which one they are, because every claim below this line
// depends on it: the same capability means different things in a container and
// on this host, and a reader with no mode line has no way to catch a claim that
// belongs to the other one.
func (c *checker) checkExecution(ctx context.Context) {
	execution := c.config.Agent.Execution
	if !execution.Devcontainer() {
		c.ok("agent.execution.mode", "host, with no container boundary around the agent")
		c.checkAgentEnvironment(ctx)
		return
	}

	// The override is named rather than resolved. It is read from the daemon's
	// own environment (ADR-032), and `feat doctor` runs without a daemon and
	// before one exists (ADR-028), so this command can say what would change the
	// answer and cannot say whether it did.
	c.ok("agent.execution.mode", fmt.Sprintf(
		"devcontainer: the agent runs in service %s, unless the daemon was started with %s=1, "+
			"which runs it on this host with no container boundary",
		execution.Service, config.EnvHostAgent))

	files := c.checkComposeFiles("agent.execution.compose_files", execution.ComposeFiles)
	if files {
		c.checkComposeService(ctx, "agent.execution.service", "", execution.ComposeFiles, execution.Service)
	}
	c.checkAgentEnvironment(ctx)
}

// checkAgentEnvironment performs the checks FR-PROJ-004 asks for inside the
// environment where the agent runs, or records why it could not.
//
// The requirement is worded around that environment for a reason: an
// authenticated `glab` on the host says nothing about a container that has no
// `glab` in it. A host-mode project is therefore checked on this machine, and a
// devcontainer project is checked inside a container of its own that is already
// running.
//
// `feat doctor` still starts nothing (ADR-028). A project with no live task has
// no container to look inside, and that is reported as skipped with the reason —
// not as passing, and not as a capability that has yet to arrive: whether the
// check can run is a fact about the machine rather than about Feat (ADR-033).
func (c *checker) checkAgentEnvironment(ctx context.Context) {
	if !c.config.Agent.Execution.Devcontainer() {
		c.checkHostCapabilities()
		c.checkHostDockerCapability()
		c.checkAgentExecutable(ctx)
		for _, capability := range providerCLIs(c.config) {
			c.checkProviderCLI(ctx, capability)
		}
		return
	}

	container, found := c.agentContainer(ctx)
	if !found {
		c.skipAgentEnvironmentChecks()
		return
	}

	// The same checks, asked of the container rather than of this machine. Only
	// where the question is asked changes, which is the point: a diagnostic that
	// asked a different question of a container would be a different diagnostic
	// wearing the same name.
	host := c.runner
	c.runner = containerRunner{
		host:      host,
		container: container,
		user:      c.config.Agent.Execution.User,
	}
	c.checkContainerDockerCapability(ctx)
	c.checkAgentExecutable(ctx)
	for _, capability := range providerCLIs(c.config) {
		c.checkProviderCLI(ctx, capability)
	}
	c.runner = host

	c.checkContainerUser(ctx, container)
}

// agentContainer finds a live container belonging to a task of this project.
//
// It is found by Feat's own ownership labels rather than by reading state,
// because `feat doctor` runs without a daemon and must not read the state
// directory the daemon owns (ADR-028).
func (c *checker) agentContainer(ctx context.Context) (string, bool) {
	output, err := c.runner.Run(ctx, "", dockerExecutable, "ps",
		"--filter", "label="+compose.LabelOwner+"="+compose.OwnerValue,
		"--filter", "label="+compose.LabelProject+"="+c.config.Project.ID,
		"--format", "{{.ID}}")
	if err != nil {
		return "", false
	}
	for line := range strings.SplitSeq(output, "\n") {
		if id := strings.TrimSpace(line); id != "" {
			return id, true
		}
	}
	return "", false
}

// checkContainerUser reports the identity the agent would actually run as.
//
// Configuration already refuses a root user, so this is the question
// configuration cannot answer: what the process turns out to be in the image the
// project builds. And then the question the uid cannot answer, because an
// identity is a fact about an instant: whether the image also hands that user a
// way back to root (ADR-066).
func (c *checker) checkContainerUser(ctx context.Context, container string) {
	const check = "agent.execution.user"
	configured := c.config.Agent.Execution.User

	output, err := c.runner.Run(ctx, "", dockerExecutable, "exec", "--user", configured, container, "id", "-u")
	if err != nil {
		c.warn(check, fmt.Sprintf("the identity of %q in the running container could not be read: %v",
			configured, err),
			"check that the image has that user")
		return
	}

	uid := strings.TrimSpace(output)
	if uid == "0" {
		c.fail(check, fmt.Sprintf("%q is uid 0 in the running container, and the agent must not be root", configured),
			"give the image a non-root user and name it in "+check)
		return
	}
	if tool, granted := c.containerEscalation(ctx, container, configured); granted {
		c.warn(check, fmt.Sprintf(
			"%s is uid %s in the running container, and %s there returns root without a password, "+
				"so it is non-root only until the agent asks", configured, uid, tool),
			"drop "+tool+" from the image if a session does not need it. A launch reports this rather than "+
				"refusing it, because a project may want it")
		return
	}
	c.ok(check, fmt.Sprintf("%s is uid %s in the running container, with no way back to root that Feat found",
		configured, uid))
}

// containerEscalation names the first tool in the container that returns root
// to the agent's user without a password.
//
// Reporting the uid alone and calling the requirement met is the shape F6-08
// records one function down: a security property stated as verified, where the
// claim replaces the reader's own review. The uid is true of the instant it was
// read, and an image that installs `sudo` beside a NOPASSWD rule — which is what
// a devcontainer template writes so a session can install a package — makes it
// true of nothing else.
//
// A tool that could not be asked is not reported as granting anything. What the
// finding above says is what was established, which is the same direction the
// launch takes and the opposite of the overclaim: Feat never reads a sudoers
// file, so a rule narrow enough to exclude `true` is not found this way.
func (c *checker) containerEscalation(ctx context.Context, container, user string) (string, bool) {
	for _, tool := range compose.EscalationTools {
		vector := append([]string{"exec", "--user", user, container, tool.Name}, tool.Arguments...)
		if _, err := c.runner.Run(ctx, "", dockerExecutable, vector...); err == nil {
			return tool.Name, true
		}
	}
	return "", false
}

// checkAgentExecutable reports whether the configured agent can be started.
func (c *checker) checkAgentExecutable(ctx context.Context) {
	const check = "agent.executable"

	path, err := c.runner.Look(claude.Executable)
	if errors.Is(err, ErrNotInstalled) {
		c.fail(check, fmt.Sprintf("%s is not installed", claude.Executable),
			"install Claude Code, or change agent.provider")
		return
	}
	if err != nil {
		c.fail(check, fmt.Sprintf("%s could not be resolved: %v", claude.Executable, err), "check the PATH")
		return
	}

	output, err := c.runner.Run(ctx, "", claude.Executable, "--version")
	if err != nil {
		c.fail(check, fmt.Sprintf("%s does not run: %v", path, err),
			"check the installation with `claude doctor`")
		return
	}

	// A version outside the range this build's hooks were verified against is
	// worth reporting rather than refusing: the failure mode of a changed hook
	// schema is a session that runs well and never reports, and a warning that
	// names the likely cause is what turns that silence into a diagnosis.
	version := claude.ParseVersion(output)
	if warning := version.Unverified(); warning != "" {
		c.warn(check, warning,
			"if tasks stop updating, compare the installed hook schema with this build's expectations")
		return
	}
	c.ok(check, fmt.Sprintf("%s, version %s", path, version))
}

// checkProviderCLI reports whether a provider CLI is installed and
// authenticated.
func (c *checker) checkProviderCLI(ctx context.Context, capability providerCLI) {
	if capability.level == config.CLIDisabled {
		c.ok(capability.field, "disabled")
		return
	}

	// An optional CLI that is absent is a note; a required one that is absent
	// stops a launch, so `feat doctor` says so before the user finds out at
	// launch time.
	report := c.warn
	if capability.level == config.CLIRequired {
		report = c.fail
	}

	if _, err := c.runner.Look(capability.tool); errors.Is(err, ErrNotInstalled) {
		report(capability.field,
			fmt.Sprintf("%s is %s, and it is not installed", capability.tool, capability.level),
			fmt.Sprintf("install %s, or set %s to disabled", capability.tool, capability.field))
		return
	} else if err != nil {
		report(capability.field, fmt.Sprintf("%s could not be resolved: %v", capability.tool, err), "check the PATH")
		return
	}

	if _, err := c.runner.Run(ctx, "", capability.tool, "auth", "status"); err != nil {
		report(capability.field,
			fmt.Sprintf("%s is %s, and it is not authenticated", capability.tool, capability.level),
			fmt.Sprintf("run `%s auth login`", capability.tool))
		return
	}
	c.ok(capability.field, fmt.Sprintf("%s is installed and authenticated", capability.tool))
}

// providerCLI is one configured provider-CLI capability.
type providerCLI struct {
	field string
	tool  string
	level string
}

func providerCLIs(cfg *config.Config) []providerCLI {
	return []providerCLI{
		{"agent.capabilities.github_cli", "gh", cfg.Agent.Capabilities.GitHubCLI},
		{"agent.capabilities.gitlab_cli", "glab", cfg.Agent.Capabilities.GitLabCLI},
	}
}

// noContainer is why the devcontainer checks did not run.
//
// It names the condition rather than a missing capability, because the check
// exists and it is the state of the machine that decides whether it can run. The
// action is what a user can actually do about it.
const noContainer = "launch a task for this project and run `feat doctor` again; " +
	"a task launch checks the same things in its own container before it starts an agent"

// skipAgentEnvironmentChecks records the checks that need a container to look
// inside when there is none.
//
// Reporting them as skipped rather than omitting them keeps `feat doctor`
// honest about its own coverage: the checks are named, and so is the reason they
// did not run. `feat doctor` never starts a container to answer them, because a
// command that reports on a machine should not change it (ADR-028).
func (c *checker) skipAgentEnvironmentChecks() {
	reason := "no container of this project is running, so there is nothing to look inside"

	c.skip("agent.executable",
		fmt.Sprintf("%s is not checked in the devcontainer: %s", claude.Executable, reason), noContainer)
	c.skip("agent.execution.user",
		fmt.Sprintf("the running process is not checked to be %q: %s", c.config.Agent.Execution.User, reason),
		noContainer)
	c.skip("agent.capabilities.docker",
		fmt.Sprintf("%s: the devcontainer is not checked for a client that speaks a container runtime's API: %s",
			c.config.Agent.Capabilities.Docker, reason),
		noContainer)

	for _, capability := range providerCLIs(c.config) {
		if capability.level == config.CLIDisabled {
			c.ok(capability.field, "disabled")
			continue
		}
		c.skip(capability.field,
			fmt.Sprintf("%s is %s, and its installation and authentication in the devcontainer are not checked: %s",
				capability.tool, capability.level, reason),
			noContainer)
	}
}

// checkRuntime checks the application runtime inputs.
//
// Each repository's contribution is checked on its own, with its own checkout
// as the project directory. Asking Compose about two repositories' files at
// once would resolve every relative path against the first one's directory,
// which is the failure the generated include document exists to prevent
// (ADR-065 evidence 2), and a diagnostic that reproduced it would report a
// project that works as broken.
func (c *checker) checkRuntime(ctx context.Context) {
	runtime := c.config.Runtime
	if runtime == nil {
		return
	}

	c.checkComposeFiles("runtime.static_overrides", runtime.StaticOverrides)
	c.checkEnvFiles(runtime.EnvFiles)

	for _, contribution := range c.config.RuntimeComposition() {
		field := "repositories." + contribution.RepositoryID + ".runtime"

		if !c.checkComposeFiles(field+".compose_files", contribution.ComposeFiles) {
			continue
		}
		for i, service := range contribution.Services {
			c.checkComposeService(ctx, fmt.Sprintf("%s.services[%d]", field, i),
				contribution.Directory, contribution.ComposeFiles, service)
		}
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
func (c *checker) checkComposeService(
	ctx context.Context, check, directory string, files []string, service string,
) {
	if _, err := c.runner.Look(dockerExecutable); err != nil {
		c.skip(check, fmt.Sprintf("service %q is not checked: %v", service, err),
			"install Docker to check configured service names")
		return
	}

	args := []string{"compose"}
	if directory != "" {
		// The directory relative paths inside these files resolve against, which
		// for a repository's contribution is its own checkout: the same project
		// directory Feat gives that repository's include entry.
		args = append(args, "--project-directory", directory)
	}
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

// checkPublication reports whether this machine can publish what the project
// declares.
//
// Nothing else asks. `agent.capabilities.gitlab_cli` and `github_cli` describe
// the agent's environment — in a devcontainer they are probed inside the
// container, and ADR-070 expects them to be `disabled` — so on a project that
// publishes through the host they answer a different question about a different
// machine. Without this, a project could be configured for a forge, pass every
// check, and then push a branch and fail to open the merge request because the
// host has no `glab` or is logged out. What Feat cannot do is better learned
// when the user asks whether the project is configured (ADR-070, ADR-074).
//
// It is asked once per forge the repositories declare rather than once per
// repository: the answer is about this machine, and a project with five GitLab
// repositories does not need to be told five times. The repositories are named
// in the finding, so it still says what is affected. A project that declares no
// forge is never asked, and says nothing.
//
// It warns rather than fails. A project may be configured before anybody has
// logged in, and a task that is never published works perfectly without any of
// this.
func (c *checker) checkPublication(ctx context.Context) {
	for _, kind := range declaredForges(c.config) {
		check := "publication." + kind.forge
		declared := "repositories " + listOf(kind.repositories, "and")
		if len(kind.repositories) == 1 {
			declared = "repository " + kind.repositories[0]
		}

		if !forge.Available(domain.ForgeKind(kind.forge)) {
			// Configurable and not built. Publishing refuses it by name, and
			// this is where that is learned rather than at the moment a branch
			// has been pushed.
			c.warn(check, fmt.Sprintf(
				"%s publishes to %s, and this build opens merge requests on %s",
				declared, kind.forge, forgesBuilt()),
				fmt.Sprintf("publish %s by hand, or set its forge.kind to %s", declared, forgesBuilt()))
			continue
		}

		tool, known := forgeTool(domain.ForgeKind(kind.forge))
		if !known {
			// This build has an adapter and this function does not know what it
			// drives. It is said rather than assumed: a diagnostic that reported
			// a check it did not run is the one thing worse than not running it
			// (ADR-028).
			c.skip(check, fmt.Sprintf("%s publishes to %s, and `feat doctor` does not know which command "+
				"line that adapter drives", declared, kind.forge),
				"report this: publication will still work, and this check will not")
			continue
		}
		c.checkForgeCLI(ctx, check, tool, declared)
	}
}

// checkForgeCLI asks the host whether one forge's command line can publish.
//
// It is the host that is asked, always. Publication runs there in both execution
// modes, so a `glab` inside the agent's container is not an answer — which is
// exactly the mistake the capability probes would make if they were reused here.
func (c *checker) checkForgeCLI(ctx context.Context, check, tool, declared string) {
	if _, err := c.runner.Look(tool); errors.Is(err, ErrNotInstalled) {
		c.warn(check, fmt.Sprintf("%s publishes through %s, and it is not installed on this machine",
			declared, tool),
			"install "+tool+"; Feat opens every merge request from here, with your own authentication")
		return
	} else if err != nil {
		c.warn(check, fmt.Sprintf("%s could not be resolved: %v", tool, err), "check the PATH")
		return
	}

	if _, err := c.runner.Run(ctx, "", tool, "auth", "status"); err != nil {
		c.warn(check, fmt.Sprintf("%s publishes through %s, and it is not authenticated on this machine",
			declared, tool),
			fmt.Sprintf("run `%s auth login`; the agent is never given a provider credential, so this "+
				"is the authentication a publication uses", tool))
		return
	}
	c.ok(check, fmt.Sprintf("%s is installed and authenticated for %s", tool, declared))
}

// forgeDeclaration is one forge the project's repositories publish to, and which
// of them declared it.
type forgeDeclaration struct {
	forge        string
	repositories []string
}

// declaredForges lists the forges the project publishes to, in a stable order,
// so that a diagnostic reads the same way twice.
func declaredForges(cfg *config.Config) []forgeDeclaration {
	var order []string
	byForge := make(map[string][]string)

	for _, id := range cfg.RepositoryIDs() {
		repository, _ := cfg.Repository(id)
		if repository.Forge == nil || repository.Forge.Kind == "" {
			continue
		}
		if _, seen := byForge[repository.Forge.Kind]; !seen {
			order = append(order, repository.Forge.Kind)
		}
		byForge[repository.Forge.Kind] = append(byForge[repository.Forge.Kind], id)
	}

	declarations := make([]forgeDeclaration, 0, len(order))
	for _, kind := range order {
		declarations = append(declarations, forgeDeclaration{forge: kind, repositories: byForge[kind]})
	}
	return declarations
}

// forgeTool names the command line one built forge is driven through.
//
// The adapter is the one that names its own executable, so this maps a forge
// onto the package that owns it rather than repeating the name.
func forgeTool(kind domain.ForgeKind) (string, bool) {
	if kind == domain.ForgeGitLab {
		return gitlab.Executable, true
	}
	return "", false
}

// forgesBuilt renders the forges this build publishes to, so that a refusal says
// what would have been accepted.
func forgesBuilt() string {
	names := make([]string, 0, len(forge.Built))
	for _, kind := range forge.Built {
		names = append(names, string(kind))
	}
	return listOf(names, "or")
}

// checkChecks checks the verification commands the completion gate runs.
//
// A check configured to run on the host is looked up here. One configured to run
// in the agent's environment is looked up inside a live container of this
// project, and reported as skipped when there is none — the rule ADR-033 set for
// every other question about that environment: whether the check can run is a
// fact about the machine rather than about this build.
func (c *checker) checkChecks(ctx context.Context) {
	container, found := "", false
	if c.config.Agent.Execution.Devcontainer() {
		container, found = c.agentContainer(ctx)
	}

	for _, repository := range checkedRepositories(c.config) {
		for _, check := range c.config.Checks[repository] {
			field := "checks." + repository + "." + check.ID

			switch {
			case check.Execution == config.ExecutionHost:
				c.lookUp(field, check.Command[0])

			case !c.config.Agent.Execution.Devcontainer():
				// The agent's environment is this host, so this is the same
				// question asked of the same machine.
				c.lookUp(field, check.Command[0])

			case !found:
				c.skip(field, fmt.Sprintf(
					"%q runs in the agent's environment, and no container of this project is running, "+
						"so there is nothing to look inside", strings.Join(check.Command, " ")), noContainer)

			default:
				host := c.runner
				c.runner = containerRunner{
					host:      host,
					container: container,
					user:      c.config.Agent.Execution.User,
				}
				c.lookUp(field, check.Command[0])
				c.runner = host
			}
		}
	}
}

// checkedRepositories lists the repositories that configure checks, in a stable
// order, so that a diagnostic reads the same way twice.
func checkedRepositories(cfg *config.Config) []string {
	names := make([]string, 0, len(cfg.Checks))
	for repository := range cfg.Checks {
		names = append(names, repository)
	}
	sort.Strings(names)
	return names
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

// checkHostDockerCapability reports what the declared Docker capability means
// for an agent that runs on this machine.
//
// `denied` is what the project declared and it is not a boundary here. A
// host-mode agent is a process of the user the daemon runs as, with that user's
// socket and CLI already on its path, and telling that project that no Docker
// socket and no host Docker CLI reach its agent — which is what this said, four
// lines under `agent.execution.mode host, with no container boundary around the
// agent` — is the overclaim CLAUDE.md's honesty rule exists for.
//
// There is nothing to probe. What the capability grants is nothing either way;
// what differs is what host execution leaves within reach, and that is a fact
// about the mode rather than about this machine.
func (c *checker) checkHostDockerCapability() {
	c.ok("agent.capabilities.docker", c.config.Agent.Capabilities.Docker+
		": Feat adds no Docker socket and no Docker CLI, and host execution takes neither away — "+
		"the agent runs as the user the daemon runs as, with that user's Docker")
}

// checkContainerDockerCapability asks the running container whether it has a
// client that speaks a container runtime's API.
//
// Here the declaration is a rule rather than a description, and this used to be
// where `feat doctor` asserted it: it found a live container, ran three probes
// inside it, and then reported the Docker capability as an OK finding without
// asking that container anything. The rest of this package is careful about the
// difference — skipAgentEnvironmentChecks exists so that an unrun check is named
// rather than omitted — and a security property stated as verified is the one
// place the carelessness costs something, because the claim replaces the
// reader's own review (F6-08).
//
// What is asked is the half of the boundary that is a property of the image, so
// it is the half a diagnostic can answer before any task exists. The other half
// — what the container mounts and what its environment sets — is checked at
// launch against that task's own specification, which is what names the
// forbidden sources and the read-only paths, and doctor has no task. That is
// said rather than left out.
func (c *checker) checkContainerDockerCapability(ctx context.Context) {
	const check = "agent.capabilities.docker"
	declared := c.config.Agent.Capabilities.Docker

	var installed []string
	for _, client := range compose.ContainerClients {
		// Run rather than Look: containerRunner.Look reports every failure that
		// is not "no such executable" as the tool being present, which would turn
		// a container that stopped between the two commands into a report that
		// the image ships podman.
		_, err := c.runner.Run(ctx, "", client, "--version")
		switch {
		case err == nil:
			installed = append(installed, client)
		case errors.Is(err, ErrNotInstalled):
		default:
			c.warn(check, fmt.Sprintf(
				"%s: the running container could not be asked whether it has %s: %v", declared, client, err),
				"run `docker exec "+client+" --version` in that container to see the whole message")
			return
		}
	}

	if len(installed) > 0 {
		c.fail(check, fmt.Sprintf("%s, and the running container has %s installed",
			declared, listOf(installed, "and")),
			"remove it from the image; a launch refuses a container carrying a client that speaks a "+
				"container runtime's API")
		return
	}
	c.ok(check, fmt.Sprintf(
		"%s: the running container has no %s; what it mounts and what its environment sets are checked "+
			"against a task's own specification when that task launches",
		declared, listOf(compose.ContainerClients, "or")))
}

// listOf renders names for a message, so that a finding about three executables
// reads as a sentence rather than as a slice.
func listOf(values []string, conjunction string) string {
	switch len(values) {
	case 0:
		return ""
	case 1:
		return values[0]
	case 2:
		return values[0] + " " + conjunction + " " + values[1]
	default:
		return strings.Join(values[:len(values)-1], ", ") + ", " + conjunction + " " + values[len(values)-1]
	}
}

// checkHostCapabilities reports what a capability level means for an agent that
// runs on this machine.
//
// In a container a declaration has an effect whether or not Feat enforces it: a
// credential the project does not mount is not there. On the host there is no
// inside for a token to be kept out of. An agent launched here runs as the user
// and inherits the user's environment, so it reaches whatever `gh` or `glab`
// authentication that user has and can call a provider's API besides, whatever
// the levels below say.
//
// It warns rather than fails, because host execution is a supported mode and
// this is what it is rather than something wrong with it. Saying it out loud is
// the point: claiming the container's property in both modes would be exactly
// the uniform security property ADR-066 and ADR-067 exist to refuse (ADR-070).
//
// Publication is unaffected either way. Feat opens merge requests from the host
// in both modes, so what the mode changes is what the approval buys — a control
// in a container, and a product behaviour here — rather than whether it happens.
func (c *checker) checkHostCapabilities() {
	c.warn("agent.capabilities",
		"agent.execution.mode is host, so the capability levels below describe intent rather than "+
			"enforcement: the agent runs as the user the daemon runs as and inherits that user's "+
			"environment, including any gh or glab authentication in it",
		"run the agent in a devcontainer if a capability has to be a boundary rather than a declaration")
}

// checkCapabilities reports the declared capabilities.
//
// They are validated by internal/config and enforced by the execution adapter.
// Reporting them here is what makes the security profile of a project visible
// in one place, next to the mounts it grants.
//
// Docker is not among them. What `denied` means depends on where the agent runs
// and, in a container, on what that container turns out to be, so it is reported
// beside the rest of the checks on the agent's own environment.
func (c *checker) checkCapabilities() {
	capabilities := c.config.Agent.Capabilities
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
