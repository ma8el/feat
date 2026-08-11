package execution

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/ma8el/feat/internal/domain"
)

// Environment is one place an agent can run.
//
// The five methods are the contract in docs/06-technical-architecture.md, with
// the three amendments ADR-033 records: Command returns an argument vector
// rather than an *exec.Cmd, because the terminal backend constructs the
// process; Run exists because validation asks an environment questions rather
// than attaching a terminal to it; and Shell is folded into Command, because the
// daemon already decides what a task shell is. Destroy is slice 12's, which owns
// what is retained and what needs confirming.
//
// No method exposes an implementation type, so slice 14 can add host-native
// execution without changing a caller (ADR-024).
type Environment interface {
	// Validate reports whether this machine can run the environment at all. It
	// answers questions about the tools on the host, not about a container: a
	// container that does not exist yet cannot be asked anything.
	Validate(ctx context.Context) error
	// Prepare brings the environment up and leaves it ready to run commands. It
	// is the one method that creates something.
	Prepare(ctx context.Context) error
	// Command returns how to run something inside the environment from the
	// host, as an argument vector the terminal backend can start.
	Command(ctx context.Context, command Command) (Invocation, error)
	// Run executes a command inside the environment and returns what it
	// produced. A command that runs and fails is not an error; a command that
	// could not be started is.
	Run(ctx context.Context, command Command) (Output, error)
	// Observe reports what the environment looks like now. It never starts
	// anything: a stopped environment is reported as stopped (FR-STATE-004).
	Observe(ctx context.Context) (State, error)
	// Inspect asks the prepared environment what it turned out to be, checking
	// that the given directories can be written to as the agent's own user.
	Inspect(ctx context.Context, writable []string) (Report, error)
	// Check reports every reason a report means an agent must not be started
	// here. It is separate from Inspect so that diagnostics can show the same
	// facts a launch refuses over.
	Check(report Report) error
	// Destroy removes the containers and networks this environment owns, and no
	// volume. It is only ever reached from a cleanup the user confirmed.
	Destroy(ctx context.Context) (State, error)
	// Volumes lists the named volumes this environment owns. A volume the
	// project declares external is not one of them.
	Volumes(ctx context.Context) ([]string, error)
	// RemoveVolumes removes the named volumes and reports which were removed.
	//
	// It is a separate method rather than a flag on Destroy, so that "volumes
	// are retained by default" is the shape of this interface rather than an
	// argument somebody can pass wrongly (FR-CLEAN-004, ADR-037).
	RemoveVolumes(ctx context.Context, names []string) ([]string, error)
}

// ErrNotInEnvironment reports an executable the agent's environment does not
// have.
//
// It is a distinct error because the remedy is distinct: an absent tool is
// installed in the image, while a failing one is configured or logged in to.
var ErrNotInEnvironment = errors.New("not installed in the agent's environment")

// Report is what a prepared environment turned out to be.
//
// It is evidence rather than judgement: Inspect gathers it and Check decides
// what is unacceptable, so `feat doctor` can show the same facts that a launch
// refuses over.
type Report struct {
	// Container identifies what was inspected, when the environment has such a
	// thing.
	Container string
	// User is what the agent's own process reports its name as.
	User string
	// UID is what that process reports its numeric identity as.
	UID int
	// UIDKnown reports whether the identity could be read at all. An unread
	// identity is never treated as a non-root answer.
	UIDKnown bool
	// DockerCLI names a Docker client found inside the environment, empty when
	// there is none.
	DockerCLI string
	// Mounts are the environment's observed bindings.
	Mounts []ObservedMount
	// MissingTools are the executables the generated hooks need and the
	// environment does not have.
	MissingTools []string
	// Unwritable maps a directory the agent must be able to write to onto what
	// the attempt reported.
	Unwritable map[string]string
}

// ObservedMount is one binding an environment turned out to have.
//
// It is separate from Mount, which is what Feat asked for: a record of a request
// and a record of what exists are different things, and conflating them is how a
// container ends up trusted for mounts nobody checked.
type ObservedMount struct {
	// Type is the kind of mount, such as a bind or a named volume.
	Type string
	// Name is the volume name, for a named volume.
	Name string
	// Source is where it comes from on the host.
	Source string
	// Destination is where it appears inside the environment.
	Destination string
	// Writable reports whether the agent may write through it.
	Writable bool
}

// Mode reports which kind of environment a specification describes.
type Mode = domain.ExecutionMode

// Spec is everything an environment needs, already resolved.
//
// Every value here is final. The daemon reads configuration, expands templates,
// and resolves paths; an adapter that read configuration would duplicate a
// vocabulary that internal/config validates (ADR-029, ADR-032, ADR-033).
type Spec struct {
	// Project and Task own the environment. They appear in its identity and in
	// the labels that make it discoverable without reading stored state.
	Project domain.ProjectID
	Task    domain.TaskID
	// Identity is the environment's unique name, which is the Compose project
	// name for the Compose adapter. It is what makes an action affect one task's
	// container and no other's.
	Identity string
	// Files are the configured base files defining the environment, in order.
	Files []string
	// Directory is the directory relative paths inside Files resolve against.
	// It is the first configured file's directory, so a base file keeps working
	// while the generated override lives elsewhere.
	Directory string
	// OverridePath is where the generated override is written. It is under the
	// state directory and never inside the control workspace: nothing the agent
	// can write may decide what its own container mounts.
	OverridePath string
	// Service is the service the agent runs in.
	Service string
	// User is the user the agent runs as. It must not be root.
	User string
	// WorkingDirectory is where the agent starts, as the agent sees it.
	WorkingDirectory string
	// Mounts are the task's filesystem bindings, in the order they are written.
	Mounts []Mount
	// Volumes are named volumes the environment mounts, such as a dedicated
	// provider configuration volume.
	Volumes []Volume
	// Variables are generated non-secret environment variables. A secret value
	// never reaches this field, because nothing that reads one ever fills it
	// (docs/05-security-model.md).
	Variables map[string]string
	// ForbiddenSources are host paths that must not be mounted into the
	// environment, in the order a refusal should explain them.
	ForbiddenSources []ForbiddenSource
}

// ForbiddenSource is one host path a task's environment must not expose to the
// agent.
//
// It carries a kind rather than a message. Which directory a path is can only be
// answered where configuration and the layout are known, and what a container
// did with it can only be answered once a container exists; a refusal has to say
// both, so the daemon resolves the first and the adapter writes the second.
type ForbiddenSource struct {
	// Path is the absolute host path.
	Path string
	// Kind is which rule the path belongs to.
	Kind ForbiddenKind
}

// ForbiddenKind names a category of host path that must not reach an agent.
//
// Each is one of the things docs/05-security-model.md forbids, and each is
// checked against the container that exists rather than against the
// specification Feat generated: what Feat asked for and what a project's own
// Compose files produced are different records (CLAUDE.md architectural rules).
type ForbiddenKind string

// The categories of forbidden host path.
const (
	// ForbiddenCheckout is a repository's ordinary checkout: the working copy a
	// task exists to leave alone (ADR-033 evidence 1).
	ForbiddenCheckout ForbiddenKind = "checkout"
	// ForbiddenStableCheckout is the checkout of a repository the project keeps
	// stable and read-only, which this task did not promote.
	//
	// It is the one kind Feat mounts itself, so it is forbidden everywhere
	// except at the target Feat mounts it at: the project declared that the
	// agent reads that repository from the checkout, and did not declare a
	// second, writable path to it.
	ForbiddenStableCheckout ForbiddenKind = "stable_checkout"
	// ForbiddenRuntime is Feat's own runtime directory.
	ForbiddenRuntime ForbiddenKind = "runtime"
	// ForbiddenState is Feat's own state directory.
	ForbiddenState ForbiddenKind = "state"
	// ForbiddenHome is the home directory of the user the daemon runs as.
	ForbiddenHome ForbiddenKind = "home"
)

// Describe says what a kind of path is, in the user's terms.
//
// It is one sentence fragment, shared by the specification check and by the
// adapter that reads a running container, so that a launch refused before
// anything was created and a launch refused after both name the same thing.
func (k ForbiddenKind) Describe() string {
	switch k {
	case ForbiddenCheckout:
		return "a repository's ordinary checkout"
	case ForbiddenStableCheckout:
		return "the ordinary checkout of a repository this project keeps stable and read-only"
	case ForbiddenRuntime:
		return "Feat's runtime directory, which holds the daemon's API socket and the tmux control socket"
	case ForbiddenState:
		return "Feat's state directory, which holds every task's control workspace, brief, and event log"
	case ForbiddenHome:
		return "the home directory of the user this daemon runs as"
	}
	return "a host path that must not reach an agent"
}

// Mount is one host directory the environment exposes to the agent.
type Mount struct {
	// Source is the absolute host path.
	Source string
	// Target is the absolute path as the agent sees it.
	Target string
	// ReadOnly reports whether the agent may write through the mount.
	ReadOnly bool
	// Description says what the mount is for, so a diagnostic can name it in
	// the user's terms rather than by path alone.
	Description string
}

// Volume is one named volume the environment mounts.
type Volume struct {
	// Name is the volume name.
	Name string
	// Target is the absolute path it is mounted at, as the agent sees it.
	Target string
	// ReadOnly reports whether the agent may write through the mount.
	ReadOnly bool
}

// Command is one thing to run inside an environment.
//
// It is an argument vector rather than a string, so nothing is handed to a
// shell to re-split (CLAUDE.md architectural rules).
type Command struct {
	// Program is the executable, as the environment resolves it.
	Program string
	// Arguments are its arguments, each already one vector element.
	Arguments []string
	// Directory is where it runs, as the agent sees it. Empty means the
	// environment's own working directory.
	Directory string
	// Variables are additional environment entries for this command alone.
	Variables map[string]string
	// Interactive reports that the command needs a terminal, which is true of
	// an agent session and false of every probe.
	Interactive bool
}

// Invocation is a host command that runs a Command inside the environment.
type Invocation struct {
	// Program is the host executable, always an absolute path: the terminal
	// backend inherits the daemon's PATH rather than the user's shell.
	Program string
	// Arguments are its arguments.
	Arguments []string
	// Directory is the host working directory the invocation runs from.
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

// State is what an environment looks like now.
//
// Every field is an observation. Nothing here is assumed from the specification
// that created the environment, because a record of what Feat asked for is not a
// record of what exists (CLAUDE.md architectural rules).
type State struct {
	// Present reports whether the environment exists at all.
	Present bool
	// Running reports whether it is up.
	Running bool
	// Container identifies the observed container, when there is one.
	Container string
	// Status is what the environment itself called its state, kept verbatim so
	// a diagnostic can quote the tool rather than paraphrase it.
	Status string
	// Health is the observed health, which is separate from running.
	Health domain.HealthState
}

// Validate reports whether the specification can be used.
//
// It is deliberately strict about paths and identity. Everything here ends up
// in a command that creates containers and mounts the user's filesystem, and a
// value that is wrong in a way nobody checked becomes a mount nobody intended.
func (s Spec) Validate() error {
	if err := s.Project.Validate(); err != nil {
		return err
	}
	if err := s.Task.Validate(); err != nil {
		return err
	}
	if s.Identity == "" {
		return fmt.Errorf("the execution environment of task %s has no identity, "+
			"and its identity is what makes an action affect one task", s.Task)
	}
	if len(s.Files) == 0 {
		return fmt.Errorf("the execution environment of task %s names no files defining it", s.Task)
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
		if err := checkHostPath(fmt.Sprintf("file %d", i+1), file); err != nil {
			return err
		}
	}
	if s.Service == "" {
		return fmt.Errorf("the execution environment of task %s names no service to run the agent in", s.Task)
	}
	if err := checkUser(s.User); err != nil {
		return err
	}
	if err := checkAgentPath("working directory", s.WorkingDirectory); err != nil {
		return err
	}
	return s.validateMounts()
}

// validateMounts checks the bindings for the two ways a mount set goes wrong:
// a path that is not what it claims to be, and two mounts at one target.
func (s Spec) validateMounts() error {
	if len(s.Mounts) == 0 {
		return fmt.Errorf("the execution environment of task %s mounts nothing, "+
			"so the agent would have neither its worktrees nor its control workspace", s.Task)
	}

	targets := make(map[string]string, len(s.Mounts)+len(s.Volumes))
	for _, mount := range s.Mounts {
		if err := checkHostPath("mount source", mount.Source); err != nil {
			return err
		}
		if err := checkAgentPath("mount target", mount.Target); err != nil {
			return err
		}
		if previous, taken := targets[mount.Target]; taken {
			return fmt.Errorf("two mounts of task %s target %s: %s and %s. "+
				"One would hide the other, and which one is not something Feat should decide",
				s.Task, mount.Target, previous, mount.Source)
		}
		targets[mount.Target] = mount.Source

		for _, forbidden := range s.ForbiddenSources {
			if !samePath(mount.Source, forbidden.Path) {
				continue
			}
			if forbidden.Kind == ForbiddenStableCheckout {
				// The one kind Feat mounts itself, which is what this loop is
				// checking. Whether the container ends up with a second mount of
				// it is a question about the container, and CheckMounts asks it.
				continue
			}
			if forbidden.Kind == ForbiddenCheckout {
				return fmt.Errorf("task %s would mount the ordinary checkout %s at %s. "+
					"A task works in its own worktree; mounting the checkout as well would let the agent "+
					"edit the working copy the task was supposed to leave alone",
					s.Task, forbidden.Path, mount.Target)
			}
			return fmt.Errorf("task %s would mount %s at %s, which is %s. "+
				"Nothing Feat generates may mount it (docs/05-security-model.md)",
				s.Task, forbidden.Path, mount.Target, forbidden.Kind.Describe())
		}
	}

	for _, volume := range s.Volumes {
		if volume.Name == "" {
			return fmt.Errorf("a volume of task %s has no name", s.Task)
		}
		if err := checkAgentPath("volume target", volume.Target); err != nil {
			return err
		}
		if previous, taken := targets[volume.Target]; taken {
			return fmt.Errorf("volume %s of task %s targets %s, where %s is already mounted",
				volume.Name, s.Task, volume.Target, previous)
		}
		targets[volume.Target] = volume.Name
	}
	return nil
}

// Validate reports whether a command can be run.
func (c Command) Validate() error {
	if c.Program == "" {
		return fmt.Errorf("a command must name a program")
	}
	if strings.HasPrefix(c.Program, "-") {
		// Otherwise the container runtime reads it as one of its own flags,
		// which is the class of defect ADR-029 refused for Git remotes.
		return fmt.Errorf("a command program must not begin with %q, but %q does", "-", c.Program)
	}
	for _, value := range append([]string{c.Program}, c.Arguments...) {
		if strings.ContainsAny(value, "\x00\n\r") {
			return fmt.Errorf("a command argument must not contain a NUL or a newline, but %q does", value)
		}
	}
	if c.Directory != "" {
		if err := checkAgentPath("command directory", c.Directory); err != nil {
			return err
		}
	}
	return sortedVariables(c.Variables).validate()
}

// sortedVariables renders a variable map in a fixed order.
//
// Environment entries reach an argument vector, and a map's iteration order does
// not repeat. Sorting makes a generated command the same command every time,
// which is what lets a test pin one.
type sortedVariables map[string]string

func (v sortedVariables) validate() error {
	for name, value := range v {
		if name == "" {
			return fmt.Errorf("an environment variable has no name")
		}
		if strings.ContainsAny(name, "=\x00\n\r") || strings.ContainsAny(value, "\x00\n\r") {
			return fmt.Errorf("the environment variable %q must not contain %q, a NUL, or a newline", name, "=")
		}
	}
	return nil
}

// Entries renders the variables as sorted KEY=VALUE strings.
func (v sortedVariables) Entries() []string {
	names := make([]string, 0, len(v))
	for name := range v {
		names = append(names, name)
	}
	sort.Strings(names)

	entries := make([]string, 0, len(names))
	for _, name := range names {
		entries = append(entries, name+"="+v[name])
	}
	return entries
}

// Entries renders a command's variables in a fixed order.
func (c Command) Entries() []string { return sortedVariables(c.Variables).Entries() }

// Entries renders a specification's variables in a fixed order.
func (s Spec) Entries() []string { return sortedVariables(s.Variables).Entries() }

// checkHostPath rejects a host path that is not absolute or not clean.
//
// A relative path would resolve against whatever directory the daemon happened
// to be started in, and an uncleaned one hides where it really points.
func checkHostPath(name, value string) error {
	if value == "" {
		return fmt.Errorf("the execution environment %s must not be empty", name)
	}
	if !path.IsAbs(value) {
		return fmt.Errorf("the execution environment %s must be absolute, but is %q", name, value)
	}
	if path.Clean(value) != value {
		return fmt.Errorf("the execution environment %s must be written cleanly, but %q is not %q",
			name, value, path.Clean(value))
	}
	return nil
}

// checkAgentPath rejects a path inside the environment that is not absolute or
// not clean. The separator is the agent's rather than the host's, which is why
// this uses path rather than filepath.
func checkAgentPath(name, value string) error { return checkHostPath(name, value) }

// checkUser rejects an agent user the security model does not permit.
//
// Configuration already refuses root, so this is the second of the two places
// ADR-033 checks it: this one guards a specification that was built rather than
// parsed, and a probe inside the container checks the process that actually
// ran.
func checkUser(user string) error {
	switch user {
	case "":
		return fmt.Errorf("the execution environment names no user for the agent to run as")
	case "root", "0", "0:0":
		return fmt.Errorf("the execution environment would run the agent as %q, and the agent must not be root "+
			"(docs/05-security-model.md)", user)
	}
	if strings.ContainsAny(user, " \t\x00\n\r") {
		return fmt.Errorf("the execution environment user %q contains whitespace", user)
	}
	return nil
}

// samePath reports whether two host paths name the same directory, after
// cleaning and ignoring a trailing separator.
func samePath(a, b string) bool {
	return path.Clean(a) == path.Clean(b)
}
