package runtime

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/ma8el/feat/internal/domain"
)

// Runtime is one task's application environment.
//
// The six lifecycle methods are FR-RUN-005's manual actions, and they are
// manual all the way down: nothing here is called by a workflow transition, a
// recovery pass, or an agent. Only a user asks for any of them.
//
// Logs returns a command rather than output, because FR-RUN-006 asks for normal
// Compose logs rather than something Feat aggregates or persists; the client
// runs it with its own terminal, exactly as it runs native tmux.
//
// No method exposes an implementation type, so a later runtime backend can
// replace this one without changing a caller (ADR-024).
type Runtime interface {
	// Validate reports whether this host can drive the runtime at all. It asks
	// about the host's tools and nothing about the user's services.
	Validate(ctx context.Context) error
	// Create brings the resources into existence without starting them. It is
	// separate from Start because a created, stopped service is a state a user
	// may want and because it makes "stopped containers are never restarted"
	// something that can be arranged deliberately.
	Create(ctx context.Context) (State, error)
	// Start brings the services up.
	Start(ctx context.Context) (State, error)
	// Stop stops the services and keeps their containers.
	Stop(ctx context.Context) (State, error)
	// Destroy removes the containers and networks this task owns. It never
	// removes a volume and never touches an external resource (FR-CLEAN-002,
	// FR-CLEAN-004).
	Destroy(ctx context.Context) (State, error)
	// Volumes lists the named volumes this task's services own. A resource the
	// project declares external is not one of them, because it carries no
	// label naming this Compose project.
	Volumes(ctx context.Context) ([]string, error)
	// RemoveVolumes removes the named volumes and reports which were removed.
	//
	// It is a separate method rather than a flag on Destroy, so that "volumes
	// are retained by default" is the shape of this interface rather than an
	// argument somebody can pass wrongly (ADR-037).
	RemoveVolumes(ctx context.Context, names []string) ([]string, error)
	// Observe reports what the runtime looks like now. It starts nothing: a
	// stopped service is reported as stopped (FR-STATE-004).
	Observe(ctx context.Context) (State, error)
	// Logs returns the host command that shows this task's normal Compose logs.
	Logs(ctx context.Context) (Invocation, error)
	// Inspect asks the running containers what they turned out to be, so that a
	// mount nobody intended is reported rather than left to be discovered by a
	// user wondering why their change had no effect.
	Inspect(ctx context.Context, state State) (Report, error)
}

// ErrNotInstalled reports an executable this host does not have.
var ErrNotInstalled = errors.New("not installed on this host")

// Spec is everything a runtime adapter needs, already resolved.
//
// Every value is final. The daemon reads configuration, expands the project
// name template, and resolves paths; an adapter that read configuration would
// duplicate a vocabulary internal/config validates (ADR-029, ADR-033, ADR-034).
type Spec struct {
	// Project and Task own the runtime. They appear in its ownership labels, so
	// what a task owns is discoverable without reading persistent state.
	Project domain.ProjectID
	Task    domain.TaskID
	// Identity is the Compose project name. It is what makes an action affect
	// one task's services and no other's, and it comes from the project's
	// configured template, which validation requires to be per-task.
	Identity string
	// Files are the configured base Compose files, in order.
	Files []string
	// StaticOverrides are user-authored overrides, applied after the base files
	// and before the generated one.
	StaticOverrides []string
	// Directory is what relative paths inside Files resolve against: the first
	// configured file's directory, so those files keep working while the
	// generated override lives under the state directory.
	Directory string
	// OverridePath is where the generated override is written. It is host-only
	// and is never mounted anywhere.
	OverridePath string
	// EnvFiles are host-side environment files, passed to the runtime by path so
	// that Feat never reads a value out of one (docs/05-security-model.md).
	EnvFiles []string
	// Services are the services the project asked Feat to manage. They are what
	// a create or a start targets, and they are not the whole of what exists:
	// Compose also starts whatever they depend on, and everything it starts is
	// in this task's Compose project. Every other action therefore addresses the
	// project rather than this list (ADR-034).
	Services []string
	// Mounts are the task worktrees, at the container paths their repositories
	// configure.
	Mounts []Mount
	// Variables are generated non-secret environment entries. A secret never
	// reaches this field because nothing that reads one ever fills it.
	Variables map[string]string
	// ForbiddenSources are the project's ordinary repository checkouts. A
	// container that turns out to mount one is running the user's own working
	// copy rather than this task's worktree, which is reported (ADR-034).
	ForbiddenSources []string
}

// Mount is one host directory a service exposes.
type Mount struct {
	// Source is the absolute host path.
	Source string
	// Target is the absolute path inside the container.
	Target string
	// ReadOnly reports whether the service may write through the mount.
	ReadOnly bool
	// Description says what the mount is, so a diagnostic can name it in the
	// user's terms rather than by path alone.
	Description string
}

// State is what a runtime looks like now.
//
// Every field is an observation. Nothing is assumed from the specification that
// created the runtime, because a record of what Feat asked for is not a record
// of what exists (CLAUDE.md architectural rules).
type State struct {
	// Present reports whether any resource of this runtime exists.
	Present bool
	// Lifecycle is the observed runtime state.
	Lifecycle domain.RuntimeState
	// Health is the observed service health, which is separate from whether the
	// containers are running: without a configured health check the honest
	// answer is unknown (FR-RUN-007).
	Health domain.HealthState
	// Services are the observed services, in configured order.
	Services []ServiceState
	// Ports are the observed publications.
	Ports []domain.PortAssignment
	// Networks and Volumes are the resources Compose reports as this project's.
	// Volumes are listed because destroy retains them and a retained resource
	// nobody can see is a resource nobody will clean up.
	Networks []string
	Volumes  []string
}

// ServiceState is one observed service.
type ServiceState struct {
	// Name is the service name.
	Name string
	// Container is the observed container identifier, empty when there is none.
	Container string
	// State is what the container runtime called it, such as running or exited.
	State string
	// Status is the runtime's own longer phrasing, kept verbatim so a diagnostic
	// can quote the tool rather than paraphrase it.
	Status string
	// Health is the observed health of this service alone.
	Health domain.HealthState
	// ExitCode is the exit status of a container that has stopped.
	ExitCode int
	// Managed reports whether the project named this service. A service that is
	// not managed is one Compose started because a managed one depends on it:
	// Feat did not ask for it, it is in the task's Compose project because Feat
	// acted, and it is stopped and removed with the rest.
	Managed bool
}

// Running reports whether the service's container is up.
func (s ServiceState) Running() bool { return strings.EqualFold(s.State, "running") }

// Report is what the started containers turned out to be.
//
// It is evidence rather than judgement, as execution.Report is: the adapter
// gathers it, and what it means is stated to the user rather than acted on.
type Report struct {
	// Mounts are the observed bindings of every service container.
	Mounts []ObservedMount
	// Notes are what a user should know about what they just started, in Feat's
	// terms. A note is never a secret and never a raw tool message.
	Notes []string
}

// ObservedMount is one binding a container turned out to have, and the service
// it belongs to.
type ObservedMount struct {
	// Service is the service whose container reported it.
	Service string
	// Type is the kind of mount, such as a bind or a named volume.
	Type string
	// Name is the volume name, for a named volume.
	Name string
	// Source is where it comes from on the host.
	Source string
	// Destination is where it appears inside the container.
	Destination string
	// Writable reports whether the service may write through it.
	Writable bool
}

// Invocation is a host command.
type Invocation struct {
	// Program is the host executable, always an absolute path: the daemon's own
	// PATH is not the user's shell.
	Program string
	// Arguments are its arguments, each already one vector element.
	Arguments []string
	// Directory is the host working directory it runs from.
	Directory string
}

// Output is what a command produced.
type Output struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Succeeded reports whether the command exited cleanly.
func (o Output) Succeeded() bool { return o.ExitCode == 0 }

// Runner executes host commands for a runtime adapter.
//
// It is an interface for the reason git.Runner and tmux.Runner are: a test can
// arrange a Compose that refuses, a service that exits at once, or a port that
// is already taken, without needing a machine in that state.
type Runner interface {
	// Run executes the command and returns what it produced. A command that runs
	// and exits non-zero is not an error — "the service is not running" is an
	// answer — and only a command that could not be started at all fails.
	Run(ctx context.Context, invocation Invocation) (Output, error)
	// Look resolves an executable to an absolute path.
	Look(name string) (string, error)
}

// Validate reports whether the specification can be used.
//
// It is strict about paths and identity for the reason execution.Spec is:
// everything here ends up in a command that creates containers and mounts the
// user's filesystem, and a value that is wrong in a way nobody checked becomes a
// mount nobody intended.
func (s Spec) Validate() error {
	if err := s.Project.Validate(); err != nil {
		return err
	}
	if err := s.Task.Validate(); err != nil {
		return err
	}
	if s.Identity == "" {
		return fmt.Errorf("the runtime of task %s has no identity, and its identity is what makes an action "+
			"affect one task's services and no other's", s.Task)
	}
	if len(s.Files) == 0 {
		return fmt.Errorf("the runtime of task %s names no Compose files defining its services", s.Task)
	}
	if len(s.Services) == 0 {
		return fmt.Errorf("the runtime of task %s names no services to manage", s.Task)
	}

	for _, field := range []struct{ name, value string }{
		{"project directory", s.Directory},
		{"generated override path", s.OverridePath},
	} {
		if err := checkHostPath(field.name, field.value); err != nil {
			return err
		}
	}
	for i, file := range s.Files {
		if err := checkHostPath(fmt.Sprintf("Compose file %d", i+1), file); err != nil {
			return err
		}
	}
	for i, file := range s.StaticOverrides {
		if err := checkHostPath(fmt.Sprintf("static override %d", i+1), file); err != nil {
			return err
		}
	}
	for i, file := range s.EnvFiles {
		if err := checkHostPath(fmt.Sprintf("environment file %d", i+1), file); err != nil {
			return err
		}
	}

	seen := make(map[string]bool, len(s.Services))
	for _, service := range s.Services {
		if err := checkName("service", service); err != nil {
			return err
		}
		if seen[service] {
			return fmt.Errorf("the runtime of task %s names service %q twice", s.Task, service)
		}
		seen[service] = true
	}

	if err := s.validateMounts(); err != nil {
		return err
	}
	return sortedVariables(s.Variables).validate()
}

// validateMounts checks the bindings for the two ways a mount set goes wrong: a
// path that is not what it claims to be, and two mounts at one target.
func (s Spec) validateMounts() error {
	targets := make(map[string]string, len(s.Mounts))
	for _, mount := range s.Mounts {
		if err := checkHostPath("mount source", mount.Source); err != nil {
			return err
		}
		if err := checkContainerPath("mount target", mount.Target); err != nil {
			return err
		}
		if previous, taken := targets[mount.Target]; taken {
			return fmt.Errorf("two mounts of task %s target %s: %s and %s. One would hide the other, "+
				"and which one is not something Feat should decide", s.Task, mount.Target, previous, mount.Source)
		}
		targets[mount.Target] = mount.Source
	}
	return nil
}

// checkHostPath rejects a host path that is not absolute or not clean.
//
// A relative path would resolve against whatever directory the daemon happened
// to be started in, and an uncleaned one hides where it really points.
func checkHostPath(name, value string) error {
	if value == "" {
		return fmt.Errorf("the runtime %s must not be empty", name)
	}
	if !path.IsAbs(value) {
		return fmt.Errorf("the runtime %s must be absolute, but is %q", name, value)
	}
	if path.Clean(value) != value {
		return fmt.Errorf("the runtime %s must be written cleanly, but %q is not %q", name, value, path.Clean(value))
	}
	return nil
}

// checkContainerPath rejects a path inside a container that is not absolute or
// not clean. The separator is the container's rather than the host's, which is
// why this uses path rather than filepath.
func checkContainerPath(name, value string) error { return checkHostPath(name, value) }

// checkName rejects a value the Compose CLI would read as one of its own flags,
// or that carries a character an argument vector should never contain.
func checkName(kind, value string) error {
	if value == "" {
		return fmt.Errorf("a runtime %s must have a name", kind)
	}
	if strings.HasPrefix(value, "-") {
		// Otherwise Compose reads it as a flag, which is the class of defect
		// ADR-029 refused for Git remotes.
		return fmt.Errorf("a runtime %s must not begin with %q, but %q does", kind, "-", value)
	}
	if strings.ContainsAny(value, "\x00\n\r") {
		return fmt.Errorf("a runtime %s must not contain a NUL or a newline, but %q does", kind, value)
	}
	return nil
}

// sortedVariables renders a variable map in a fixed order.
//
// Environment entries reach a generated document and an argument vector, and a
// map's iteration order does not repeat. Sorting makes a generated file the same
// file every time, which is what lets a golden test pin one.
type sortedVariables map[string]string

func (v sortedVariables) validate() error {
	for name, value := range v {
		if name == "" {
			return fmt.Errorf("a generated environment variable has no name")
		}
		if strings.ContainsAny(name, "=\x00\n\r") || strings.ContainsAny(value, "\x00\n\r") {
			return fmt.Errorf("the environment variable %q must not contain %q, a NUL, or a newline", name, "=")
		}
	}
	return nil
}

// Names returns the variable names in a fixed order.
func (v sortedVariables) Names() []string {
	names := make([]string, 0, len(v))
	for name := range v {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Entries renders a specification's generated variables as sorted KEY=VALUE
// pairs.
func (s Spec) Entries() [][2]string {
	variables := sortedVariables(s.Variables)
	entries := make([][2]string, 0, len(s.Variables))
	for _, name := range variables.Names() {
		entries = append(entries, [2]string{name, s.Variables[name]})
	}
	return entries
}
