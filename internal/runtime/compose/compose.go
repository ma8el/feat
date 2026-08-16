package compose

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/ma8el/feat/internal/runtime"
)

// Executable is the container tool Feat drives on the host.
//
// It is a constant rather than a configured value: a project that could name it
// would be naming a program the daemon starts on its owner's behalf.
const Executable = "docker"

// MinimumVersion is the oldest Docker Compose this adapter supports.
//
// The generated override uses the !reset tag to remove a base file's
// container_name, which Compose gained in 2.24. An older build fails with a YAML
// error that says nothing about why Feat wrote that document (ADR-033, ADR-034).
var MinimumVersion = Version{Major: 2, Minor: 24, Text: "2.24", Parsed: true}

// Runtime runs one task's application services through the Docker Compose CLI
// on the trusted host.
//
// It receives final values and reads neither configuration nor persistent
// state: the daemon expands the project name template and records what this
// reports (ADR-034).
//
// This adapter and internal/execution/compose never meet. They drive the same
// tool and answer different questions — where the agent runs, and what the user
// tests — and CLAUDE.md keeps them apart even where the commands rhyme.
type Runtime struct {
	spec   runtime.Spec
	runner runtime.Runner
	// docker is the absolute path of the container tool, resolved once, because
	// the daemon's PATH is not the user's shell.
	docker string
}

var _ runtime.Runtime = (*Runtime)(nil)

// Options configure a runtime.
type Options struct {
	// Runner executes Docker commands. A nil value uses the host.
	Runner runtime.Runner
}

// New returns the Compose runtime for one task.
//
// It validates the specification and resolves the container tool before
// anything can be created, so a specification that could never work is refused
// where the message can still name the field that is wrong.
//
// It also writes the generated include document, which is the one file every
// command needs: a status, a stop, and a destroy all name it, and the first
// thing a user does with a runtime is ask what it is doing before anything has
// created it. The generated override is different and is written where it is
// used, because it exists only once there is something to override.
func New(spec runtime.Spec, opts Options) (*Runtime, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}

	runner := opts.Runner
	if runner == nil {
		runner = runtime.HostRunner{}
	}
	docker, err := runner.Look(Executable)
	if err != nil {
		return nil, fmt.Errorf("task %s runs its application services through Docker Compose, and %w. "+
			"Install Docker, or remove the runtime section from the project's configuration", spec.Task, err)
	}
	if err := writeInclude(spec); err != nil {
		return nil, err
	}
	return &Runtime{spec: spec, runner: runner, docker: docker}, nil
}

// IncludePath returns the generated include document this runtime writes.
func (r *Runtime) IncludePath() string { return r.spec.IncludePath }

// Identity returns the Compose project name this runtime owns.
func (r *Runtime) Identity() string { return r.spec.Identity }

// OverridePath returns the generated override this runtime writes.
func (r *Runtime) OverridePath() string { return r.spec.OverridePath }

// Validate reports whether this host can drive the runtime.
//
// It asks about the host only. Whether the user's services build, bind their
// ports, or become healthy are questions about services that do not exist until
// something creates them, and answering them here would mean answering them
// before there is anything to answer about.
func (r *Runtime) Validate(ctx context.Context) error {
	version, err := r.Version(ctx)
	if err != nil {
		return err
	}
	if version.Below(MinimumVersion) {
		return fmt.Errorf("the installed Docker Compose %s is older than the %s Feat needs: "+
			"the generated override uses the !reset tag to remove a base file's container_name, without "+
			"which two tasks cannot run the same services at once. Upgrade Docker Compose",
			version, MinimumVersion)
	}
	return nil
}

// Version reports the installed Docker Compose version.
func (r *Runtime) Version(ctx context.Context) (Version, error) {
	output, err := r.runner.Run(ctx, runtime.Invocation{
		Program:   r.docker,
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

// Create brings the task's containers into existence without starting them.
//
// It exists as its own action because FR-RUN-005 names it and because a created
// service that is not running is a state a user may want: an application whose
// containers exist, whose volumes exist, and which is deliberately not up.
//
// `up --no-start` rather than `create`, which is the same action by name and not
// by behaviour: `docker compose create api` builds the image of `api` and then
// creates a container for the service `api` depends on, whose image it did not
// build and which therefore does not exist. `up --no-start api` builds the whole
// dependency closure and starts none of it, which is what this action means
// (ADR-034 evidence 13).
func (r *Runtime) Create(ctx context.Context) (runtime.State, error) {
	return r.bring(ctx, "creating", r.services("up", "--no-start")...)
}

// Start brings the task's services up.
//
// Only a user reaches this. No workflow transition, no recovery pass, and no
// agent starts an application service (FR-RUN-005, FR-STATE-004).
func (r *Runtime) Start(ctx context.Context) (runtime.State, error) {
	return r.bring(ctx, "starting", r.services("up", "--detach")...)
}

// bring runs a command that creates or starts, writing the generated override
// first and observing the result afterwards.
func (r *Runtime) bring(ctx context.Context, action string, arguments ...string) (runtime.State, error) {
	defined, err := r.defined(ctx)
	if err != nil {
		return runtime.State{}, err
	}
	if err := writeOverride(r.spec, defined); err != nil {
		return runtime.State{}, err
	}

	output, err := r.runner.Run(ctx, r.invoke(arguments...))
	if err != nil {
		return runtime.State{}, err
	}
	if !output.Succeeded() {
		// The last line, not the first: Compose narrates every image it pulls and
		// every resource it creates, so a failed run begins with progress and ends
		// with the reason (ADR-033 evidence 14).
		reported := lastLine(output.Stderr, output.Stdout)
		return runtime.State{}, fmt.Errorf("%s the services of task %s failed: %s%s",
			action, r.spec.Task, reported, r.explain(reported))
	}
	return r.Observe(ctx)
}

// Stop stops the services and keeps their containers.
//
// `stop` rather than `down`: stopping is reversible and removes nothing, and the
// user who asked for it is testing an application rather than tidying up after
// one.
//
// It names no services, so it stops the whole of this task's Compose project.
// Naming the managed ones stopped exactly the containers Feat asked Compose for
// and left the ones Compose started to satisfy them — a database still running,
// still holding its published port, invisible to every status Feat printed, and
// stopped by nothing short of a destroy (ADR-034 evidence 12).
func (r *Runtime) Stop(ctx context.Context) (runtime.State, error) {
	output, err := r.runner.Run(ctx, r.invoke("stop"))
	if err != nil {
		return runtime.State{}, err
	}
	if !output.Succeeded() {
		return runtime.State{}, fmt.Errorf("stopping the services of task %s failed: %s",
			r.spec.Task, lastLine(output.Stderr, output.Stdout))
	}
	return r.Observe(ctx)
}

// Destroy removes the containers and networks of this task's Compose project.
//
// Three things it deliberately does not do, and each is a rule rather than an
// omission:
//
//   - no --volumes, so every volume survives. Volumes are retained by default
//     and removing one is a choice slice 12 asks for explicitly (FR-CLEAN-004);
//   - no --remove-orphans, because an orphan is a container Feat did not put
//     there and removing one would be a destructive act nobody asked for;
//   - nothing outside this Compose project, so an external staging database is
//     untouched by construction rather than by exclusion (FR-RUN-008).
func (r *Runtime) Destroy(ctx context.Context) (runtime.State, error) {
	// Read before removing, so the answer names what was retained rather than
	// what is left, which after a `down` is the same list minus nothing.
	before, err := r.Observe(ctx)
	if err != nil {
		return runtime.State{}, err
	}

	output, err := r.runner.Run(ctx, r.invoke("down"))
	if err != nil {
		return runtime.State{}, err
	}
	if !output.Succeeded() {
		return runtime.State{}, fmt.Errorf("removing the containers of task %s failed: %s",
			r.spec.Task, lastLine(output.Stderr, output.Stdout))
	}

	after, err := r.Observe(ctx)
	if err != nil {
		return runtime.State{}, err
	}
	// The volumes are read again rather than assumed: "Feat retained them" is a
	// claim about what exists, and Observe is what has looked.
	if len(after.Volumes) == 0 {
		after.Volumes = before.Volumes
	}
	return after, nil
}

// Logs returns the host command that shows this task's normal Compose logs.
//
// Feat does not aggregate, persist, or re-render them (FR-RUN-006). The client
// runs this with its own terminal, exactly as it runs native tmux for attach.
//
// The whole project, for the reason Stop takes it: the log a user needs when a
// managed service will not start is usually the one written by the service it
// waits for.
func (r *Runtime) Logs(_ context.Context) (runtime.Invocation, error) {
	return r.invoke("logs", "--follow"), nil
}

// services appends the task's managed services to a command.
//
// Only create and start take them. They are what Compose is asked to bring up,
// and Compose brings up whatever those depend on as well; every other action
// addresses the project, because everything in it is there because Feat acted.
//
// Nothing variadic is ever left before a positional argument elsewhere in Feat;
// here the services are the positional arguments and they always come last, so
// no flag can swallow one (ADR-032 evidence 12).
func (r *Runtime) services(arguments ...string) []string {
	return append(arguments, r.spec.Services...)
}

// defined asks Compose which services this task's project defines.
//
// Feat targets the managed services and Compose starts what they depend on, so
// the project holds more services than the project file names. Every one of them
// needs its container_name reset and its ownership labels, or the base file's
// fixed name is global to the Docker daemon again and the second task to start
// collides with the first — which is the one thing a per-task Compose project
// exists to prevent.
//
// It reads names and nothing else. `docker compose config` renders the whole
// project including the values of its environment files, which Feat never reads;
// --services prints one service name per line. The generated override is left
// out of the file list on purpose, so that a stale one cannot reintroduce a
// service the project has since removed.
func (r *Runtime) defined(ctx context.Context) ([]string, error) {
	output, err := r.runner.Run(ctx, r.compose(false, "config", "--services"))
	if err != nil {
		return nil, err
	}
	if !output.Succeeded() {
		reported := lastLine(output.Stderr, output.Stdout)
		return nil, fmt.Errorf("reading the services of task %s from its Compose files failed: %s%s",
			r.spec.Task, reported, r.explain(reported))
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
// Every invocation carries the project name, which is what makes an action
// affect one task's services and no other's, and the project directory, which is
// where Feat's own generated documents live. Nothing relative resolves against
// it: every path in those documents is absolute, and each repository's own
// relative paths resolve against the project directory its include entry
// carries.
//
// The file order is the merge order: the generated include, then the project's
// own static overrides, then Feat's generated override last. Environment files
// are passed by path and never read (docs/05-security-model.md).
func (r *Runtime) invoke(arguments ...string) runtime.Invocation {
	return r.compose(true, arguments...)
}

// compose builds one Compose command, with or without Feat's generated
// override. Only the command that asks which services the project defines
// leaves it out; see defined.
func (r *Runtime) compose(generated bool, arguments ...string) runtime.Invocation {
	base := []string{
		"compose",
		"--project-name", r.spec.Identity,
		"--project-directory", r.spec.Directory,
		"--file", r.spec.IncludePath,
	}
	for _, file := range r.spec.StaticOverrides {
		base = append(base, "--file", file)
	}
	if generated {
		if _, err := os.Stat(r.spec.OverridePath); err == nil {
			// Only when it is there. Every action that creates something writes it
			// first, so this is never the path a start takes; what it covers is
			// asking a runtime that has never been created what it is doing, which
			// is the first thing a user does and which Compose would otherwise
			// refuse with "no such file or directory" about a document Feat
			// generates.
			base = append(base, "--file", r.spec.OverridePath)
		}
	}
	for _, file := range r.spec.EnvFiles {
		base = append(base, "--env-file", file)
	}

	return runtime.Invocation{
		Program:   r.docker,
		Arguments: append(base, arguments...),
		Directory: r.spec.Directory,
	}
}

// lastLine returns the last non-empty line of the given outputs.
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
