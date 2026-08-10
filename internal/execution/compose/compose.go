package compose

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/execution"
)

// Executable is the container tool Feat drives on the host.
//
// It is a constant rather than a configured value, for the reason the Claude
// executable is: a project that could name it would be naming a program the
// daemon starts on its owner's behalf.
const Executable = "docker"

// MinimumVersion is the oldest Docker Compose this adapter supports.
//
// The generated override uses the !reset tag to remove a base file's
// container_name and published ports, which Compose gained in 2.24. An older
// build fails with a YAML error that says nothing about why Feat wrote that
// document, so the version is checked and reported instead (ADR-033).
var MinimumVersion = Version{Major: 2, Minor: 24, Text: "2.24", Parsed: true}

// Environment runs an agent inside a Compose service on the trusted host.
//
// It receives final values and reads neither configuration nor persistent
// state: the daemon expands templates and records what this reports (ADR-033).
type Environment struct {
	spec   execution.Spec
	runner Runner
	// docker is the absolute path of the container tool, resolved once. It is
	// absolute because a task terminal inherits the daemon's environment rather
	// than the user's shell, and a PATH lookup there is not the lookup the user
	// would expect.
	docker string
	// readyTimeout bounds how long Prepare waits for the service to be running.
	readyTimeout time.Duration
	// pollInterval is how often it asks.
	pollInterval time.Duration
	now          func() time.Time
}

var _ execution.Environment = (*Environment)(nil)

// Options configure an environment.
type Options struct {
	// Runner executes Docker commands. A nil value uses the host.
	Runner Runner
	// ReadyTimeout bounds waiting for the service to run. Zero uses the default.
	ReadyTimeout time.Duration
	// PollInterval is how often readiness is checked. Zero uses the default.
	PollInterval time.Duration
	// Now supplies the current time. A nil value uses the wall clock.
	Now func() time.Time
}

const (
	defaultReadyTimeout = 3 * time.Minute
	defaultPollInterval = 500 * time.Millisecond
)

// New returns the Compose environment for one task.
//
// It validates the specification and resolves the container tool before
// anything can be created, so a specification that could never work is refused
// where the message can still name the field that is wrong.
func New(spec execution.Spec, opts Options) (*Environment, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}

	runner := opts.Runner
	if runner == nil {
		runner = HostRunner{}
	}
	docker, err := runner.Look(Executable)
	if err != nil {
		return nil, fmt.Errorf("task %s runs its agent in a container, and %w. "+
			"Install Docker, or set agent.execution.mode to host", spec.Task, err)
	}

	environment := &Environment{
		spec:         spec,
		runner:       runner,
		docker:       docker,
		readyTimeout: opts.ReadyTimeout,
		pollInterval: opts.PollInterval,
		now:          opts.Now,
	}
	if environment.readyTimeout <= 0 {
		environment.readyTimeout = defaultReadyTimeout
	}
	if environment.pollInterval <= 0 {
		environment.pollInterval = defaultPollInterval
	}
	if environment.now == nil {
		environment.now = time.Now
	}
	return environment, nil
}

// Identity returns the Compose project name the environment owns.
func (e *Environment) Identity() string { return e.spec.Identity }

// OverridePath returns the generated override this environment writes.
func (e *Environment) OverridePath() string { return e.spec.OverridePath }

// Validate reports whether this host can run the environment.
//
// It asks about the host only. Whether the container has Claude, runs as a
// non-root user, or can write the control workspace are questions about a
// container that does not exist until Prepare, and asking them here would mean
// answering them somewhere other than where the agent runs.
func (e *Environment) Validate(ctx context.Context) error {
	version, err := e.Version(ctx)
	if err != nil {
		return err
	}
	if version.Below(MinimumVersion) {
		return fmt.Errorf("the installed Docker Compose %s is older than the %s Feat needs: "+
			"the generated override uses the !reset tag to remove a base file's container_name and "+
			"published ports, without which two tasks cannot run at once. Upgrade Docker Compose",
			version, MinimumVersion)
	}
	return nil
}

// Version reports the installed Docker Compose version.
func (e *Environment) Version(ctx context.Context) (Version, error) {
	output, err := e.runner.Run(ctx, execution.Invocation{
		Program:   e.docker,
		Arguments: []string{"compose", "version", "--short"},
	})
	if err != nil {
		return Version{}, err
	}
	if !output.Succeeded() {
		return Version{}, fmt.Errorf("docker compose did not report its version: %s",
			firstLine(output.Stderr, output.Stdout))
	}
	return ParseVersion(strings.TrimSpace(output.Stdout)), nil
}

// Prepare writes the generated override and brings the agent's service up.
//
// It is the one method that creates something, and what it creates is recorded
// before it exists: the daemon writes the environment onto the task before
// calling this, so an interruption leaves a record naming a superset of what
// exists (ADR-029's ordering, applied to containers).
func (e *Environment) Prepare(ctx context.Context) error {
	defined, err := e.defined(ctx)
	if err != nil {
		return err
	}
	if err := writeOverride(e.spec, defined); err != nil {
		return err
	}

	output, err := e.runner.Run(ctx, e.invoke("up", "--detach", e.spec.Service))
	if err != nil {
		return err
	}
	if !output.Succeeded() {
		reported := lastLine(output.Stderr, output.Stdout)
		return fmt.Errorf("starting service %s of Compose project %s failed: %s%s",
			e.spec.Service, e.spec.Identity, reported, e.explain(reported))
	}
	return e.waitRunning(ctx)
}

// waitRunning waits until the service reports that it is running.
//
// `up --detach` returns once the container has been started, which is not the
// same as the container still being up: an image whose command exits at once
// leaves a service that was started and is already gone. Probing such a service
// produces "container is not running", which describes the symptom rather than
// the cause.
func (e *Environment) waitRunning(ctx context.Context) error {
	deadline := e.now().Add(e.readyTimeout)
	var last execution.State

	for {
		state, err := e.Observe(ctx)
		if err != nil {
			return err
		}
		last = state
		if state.Running {
			return nil
		}
		if state.Present && state.Status != "" && !restarting(state) {
			// A container that exists and is not running is not going to start
			// by itself. Waiting for the deadline would only delay the message.
			break
		}
		if !e.now().Before(deadline) {
			break
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(e.pollInterval):
		}
	}

	return fmt.Errorf("service %s of Compose project %s did not stay running: %s. "+
		"Its command may exit immediately; a devcontainer service needs one that keeps running, "+
		"such as `command: sleep infinity`. Run `%s compose --project-name %s logs %s` to see why",
		e.spec.Service, e.spec.Identity, describe(last), Executable, e.spec.Identity, e.spec.Service)
}

// explain adds what Feat knows about a failure the container runtime reported
// in its own terms.
//
// The runtime's message is accurate and describes a path, not a decision. When
// that path is inside something Feat mounted read-only, the decision was the
// task's repository access, and saying so turns "read-only file system" into a
// choice the user can make differently. Nothing is added when Feat has nothing
// to add: a guess dressed as an explanation is worse than the original message.
func (e *Environment) explain(reported string) string {
	lowered := strings.ToLower(reported)

	switch {
	case strings.Contains(lowered, "read-only file system"):
		for _, mount := range e.spec.Mounts {
			if !mount.ReadOnly || !strings.Contains(reported, mount.Target+"/") {
				continue
			}
			return fmt.Sprintf(
				". Feat mounted %s read-only because this task selected that repository read-only, "+
					"and the project's own Compose files mount something inside it that has to be created. "+
					"Select the repository read-write for this task, or move that mount out of %s",
				mount.Target, mount.Target)
		}

	case strings.Contains(lowered, "is outside of rootfs"):
		for _, mount := range e.spec.Mounts {
			if !strings.Contains(reported, mount.Target+"/") {
				continue
			}
			return fmt.Sprintf(
				". The project's own Compose files mount something inside %s, and Feat mounts this task's "+
					"own worktree there rather than the ordinary checkout. A worktree holds only what Git "+
					"tracks, so a file that mount expects to find — an ignored .env, for instance — is not "+
					"there, and the container runtime will not create it inside a shared directory. "+
					"Remove that mount, or make it one the worktree can satisfy",
				mount.Target)
		}
	}
	return ""
}

// restarting reports whether a container is on its way up.
func restarting(state execution.State) bool {
	status := strings.ToLower(state.Status)
	return strings.Contains(status, "restarting") || strings.Contains(status, "created") ||
		strings.Contains(status, "starting")
}

// describe renders an observed state for a message.
func describe(state execution.State) string {
	switch {
	case !state.Present:
		return "no container exists for it"
	case state.Status != "":
		return state.Status
	default:
		return "its state is unknown"
	}
}

// Command returns how to run something inside the environment.
//
// The result is an argument vector for the host: the terminal backend starts
// the process and keeps its terminal, and this adapter decides only what that
// process is (ADR-030, ADR-033).
func (e *Environment) Command(_ context.Context, command execution.Command) (execution.Invocation, error) {
	if err := command.Validate(); err != nil {
		return execution.Invocation{}, err
	}

	arguments := []string{"exec"}
	if !command.Interactive {
		// A probe has no terminal. Saying so explicitly means the answer does
		// not depend on what the daemon's own standard input happens to be.
		arguments = append(arguments, "--no-TTY")
	}
	arguments = append(arguments, "--user", e.spec.User)

	directory := command.Directory
	if directory == "" {
		directory = e.spec.WorkingDirectory
	}
	arguments = append(arguments, "--workdir", directory)

	for _, entry := range command.Entries() {
		arguments = append(arguments, "--env", entry)
	}

	// The service, then the program, then its arguments. Nothing variadic sits
	// last: a flag that takes a list immediately before a positional argument
	// swallows it, which is the defect ADR-032 evidence 12 records.
	arguments = append(arguments, e.spec.Service, command.Program)
	arguments = append(arguments, command.Arguments...)

	return e.invoke(arguments...), nil
}

// Run executes a command inside the environment and returns what it produced.
func (e *Environment) Run(ctx context.Context, command execution.Command) (execution.Output, error) {
	invocation, err := e.Command(ctx, command)
	if err != nil {
		return execution.Output{}, err
	}
	output, err := e.runner.Run(ctx, invocation)
	if err != nil {
		return output, err
	}
	// Compose reports an executable the container does not have as its own
	// failure. Translating it here means a caller can tell "glab is missing"
	// from "glab said no", which are fixed in different ways.
	//
	// Both streams are read, because Docker Compose writes this particular
	// failure to standard output rather than standard error. Reading only
	// standard error looks right and reports every absent tool as present,
	// which was found by running the real thing rather than by reasoning about
	// it (ADR-033).
	if !output.Succeeded() && notFound(output.Stdout+"\n"+output.Stderr, command.Program) {
		return output, fmt.Errorf("%w: %s", ErrNotInEnvironment, command.Program)
	}
	return output, nil
}

// notFound reports whether Compose refused a command because the container has
// no such executable.
//
// The distinction it draws is narrow on purpose. A tool that ran and could not
// open a file also says "no such file or directory", and reading that as an
// absent executable would report a missing settings file as a missing Claude —
// sending the user to install something they already have. Only the container
// runtime's own refusal to start the process counts, which it announces by
// quoting the program it could not exec.
func notFound(reported, program string) bool {
	lowered := strings.ToLower(reported)
	if strings.Contains(lowered, "executable file not found") {
		return true
	}
	// `exec: "/bin/tool": stat /bin/tool: no such file or directory` — an
	// absolute program the runtime could not find. The quoted form is what
	// separates it from a file the program itself could not open.
	quoted := `exec: "` + strings.ToLower(program) + `"`
	return strings.Contains(lowered, quoted) && strings.Contains(lowered, "no such file or directory")
}

// Observe reports what the environment looks like now.
//
// It starts nothing. A stopped container is reported as stopped, which is what
// FR-STATE-004 requires of every observation Feat makes.
func (e *Environment) Observe(ctx context.Context) (execution.State, error) {
	output, err := e.runner.Run(ctx, e.invoke("ps", "--all", "--format", "json", e.spec.Service))
	if err != nil {
		return execution.State{}, err
	}
	if !output.Succeeded() {
		return execution.State{}, fmt.Errorf("asking Compose project %s for its state failed: %s",
			e.spec.Identity, firstLine(output.Stderr, output.Stdout))
	}

	containers, err := parseContainers(output.Stdout)
	if err != nil {
		return execution.State{}, fmt.Errorf("reading the state of Compose project %s: %w", e.spec.Identity, err)
	}
	for _, container := range containers {
		if container.Service != e.spec.Service {
			continue
		}
		return execution.State{
			Present:   true,
			Running:   strings.EqualFold(container.State, "running"),
			Container: container.ID,
			Status:    container.Status,
			Health:    health(container),
		}, nil
	}
	return execution.State{}, nil
}

// container is one entry of `docker compose ps --format json`.
type container struct {
	ID      string `json:"ID"`
	Name    string `json:"Name"`
	Service string `json:"Service"`
	State   string `json:"State"`
	Health  string `json:"Health"`
	Status  string `json:"Status"`
}

// parseContainers reads what Compose printed.
//
// Compose has printed both a JSON array and newline-delimited objects across
// its versions, so both are accepted rather than one being assumed. Guessing
// wrong here would report every task's container as absent, and the daemon
// would then say a running agent had stopped.
func parseContainers(output string) ([]container, error) {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return nil, nil
	}

	if strings.HasPrefix(trimmed, "[") {
		var containers []container
		if err := json.Unmarshal([]byte(trimmed), &containers); err != nil {
			return nil, err
		}
		return containers, nil
	}

	var containers []container
	for line := range strings.SplitSeq(trimmed, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var one container
		if err := json.Unmarshal([]byte(line), &one); err != nil {
			return nil, err
		}
		containers = append(containers, one)
	}
	return containers, nil
}

// health maps Compose's health to the domain's.
//
// A service with no health check reports nothing, which is "unknown" rather
// than "healthy": docs/02-user-workflows.md requires that a container without a
// health check is shown as running with health unknown.
func health(c container) domain.HealthState {
	switch strings.ToLower(c.Health) {
	case "healthy":
		return domain.HealthHealthy
	case "unhealthy":
		return domain.HealthUnhealthy
	case "starting":
		return domain.HealthStarting
	default:
		return domain.HealthUnknown
	}
}

// defined asks Compose which services this task's project defines.
//
// Feat brings up the agent's service and Compose brings up whatever that
// service depends on, so the project holds more services than the launch names.
// Every one of them needs the base file's fixed container_name and published
// ports removed, or the second task to start collides with the first over a
// service the user did not know Feat was starting — which is the one thing a
// per-task Compose project exists to prevent.
//
// It reads names and nothing else. `docker compose config` renders the whole
// project including the values of its environment files, which Feat never reads;
// --services prints one service name per line (ADR-028). The generated override
// is left out of the file list on purpose, so that a stale one cannot
// reintroduce a service the project has since removed — and because on a first
// launch it does not exist yet.
func (e *Environment) defined(ctx context.Context) ([]string, error) {
	output, err := e.runner.Run(ctx, e.compose(false, "config", "--services"))
	if err != nil {
		return nil, err
	}
	if !output.Succeeded() {
		reported := lastLine(output.Stderr, output.Stdout)
		return nil, fmt.Errorf("reading the services of task %s from the Compose files of project %s failed: %s%s",
			e.spec.Task, e.spec.Identity, reported, e.explain(reported))
	}

	var names []string
	for line := range strings.SplitSeq(output.Stdout, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			names = append(names, trimmed)
		}
	}
	return names, nil
}

// invoke builds one Compose command for this task's project.
//
// Every invocation carries the project name and the project directory. The
// project name is what makes an action affect one task's container and no
// other's; the project directory is the first configured file's directory, so
// that file's relative sources and build contexts keep resolving while the
// generated override lives under the state directory (ADR-033).
func (e *Environment) invoke(arguments ...string) execution.Invocation {
	return e.compose(true, arguments...)
}

// compose builds one Compose command, with or without Feat's generated
// override. Only the command that asks which services the project defines
// leaves it out; see defined.
func (e *Environment) compose(generated bool, arguments ...string) execution.Invocation {
	base := []string{
		"compose",
		"--project-name", e.spec.Identity,
		"--project-directory", e.spec.Directory,
	}
	for _, file := range e.spec.Files {
		base = append(base, "--file", file)
	}
	if generated {
		base = append(base, "--file", e.spec.OverridePath)
	}

	return execution.Invocation{
		Program:   e.docker,
		Arguments: append(base, arguments...),
		Directory: e.spec.Directory,
	}
}

// lastLine returns the last non-empty line of the given outputs.
//
// Bringing a service up is the one command whose failure is at the end rather
// than the beginning: Compose narrates every build step and every resource it
// creates, so the first line of a failed `up` is "Image … Building" and the
// reason is the last thing printed. Reporting the first line looks reasonable
// and tells the user nothing at all, which is how a mount error in their own
// Compose file reached them as a progress message.
func lastLine(outputs ...string) string {
	for _, output := range outputs {
		lines := strings.Split(output, "\n")
		for i := len(lines) - 1; i >= 0; i-- {
			if trimmed := strings.TrimSpace(lines[i]); trimmed != "" {
				return trimmed
			}
		}
	}
	return "it reported nothing"
}

// firstLine returns the first non-empty line of the given outputs.
func firstLine(outputs ...string) string {
	for _, output := range outputs {
		for line := range strings.SplitSeq(output, "\n") {
			if trimmed := strings.TrimSpace(line); trimmed != "" {
				return trimmed
			}
		}
	}
	return "it reported nothing"
}
