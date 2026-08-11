package compose

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/ma8el/feat/internal/execution"
)

// ErrNotInEnvironment is the shared sentinel for an executable the agent's
// environment does not have. It is aliased here so that callers of this adapter
// need not import two packages to recognise one condition.
var ErrNotInEnvironment = execution.ErrNotInEnvironment

// DockerSocketPaths are the socket paths that would give a container control of
// the host's Docker daemon.
//
// The agent must never receive one (docs/05-security-model.md, Docker
// boundary). The list is of destinations inside the container as well as sources
// on the host, because either end being a daemon socket is the same capability.
// A source is compared by containment as well as by equality: a directory
// holding one of these hands over the socket inside it without naming it.
var DockerSocketPaths = []string{
	"/var/run/docker.sock",
	"/run/docker.sock",
	"/var/run/docker.raw.sock",
	"/run/podman/podman.sock",
	"/var/run/podman/podman.sock",
	"/run/containerd/containerd.sock",
	"/var/run/containerd/containerd.sock",
}

// runtimeSocketNames are the file names a container runtime gives its API
// socket.
//
// The paths above cannot be enumerated: a rootless daemon puts its socket under
// /run/user/<uid>, a Docker Desktop replacement puts it under the user's home
// directory, and a project is free to point its client anywhere. What does not
// vary is the name, and every runtime named here speaks an API that creates
// containers on the host.
var runtimeSocketNames = []string{
	"docker.sock",
	"podman.sock",
	"containerd.sock",
}

// mount decodes one binding of `docker inspect`. It is the wire shape of
// execution.ObservedMount, which is what every caller outside this package sees.
type mount struct {
	Type        string `json:"Type"`
	Name        string `json:"Name"`
	Source      string `json:"Source"`
	Destination string `json:"Destination"`
	Writable    bool   `json:"RW"`
}

// Mounts returns what the running container actually mounts.
//
// It reads the container rather than the resolved Compose configuration, for two
// reasons. `docker compose config` renders the project including values taken
// from the project's environment files, which Feat must not read; and the
// container is evidence about what exists, while the configuration is a claim
// about what was asked for (ADR-033, ADR-028).
func (e *Environment) Mounts(ctx context.Context, container string) ([]execution.ObservedMount, error) {
	if container == "" {
		return nil, fmt.Errorf("inspecting the mounts of Compose project %s needs a container", e.spec.Identity)
	}
	output, err := e.runner.Run(ctx, execution.Invocation{
		Program: e.docker,
		Arguments: []string{
			"inspect", "--type", "container", "--format", "{{json .Mounts}}", container,
		},
	})
	if err != nil {
		return nil, err
	}
	if !output.Succeeded() {
		return nil, fmt.Errorf("inspecting container %s of Compose project %s failed: %s",
			container, e.spec.Identity, firstLine(output.Stderr, output.Stdout))
	}

	var decoded []mount
	trimmed := strings.TrimSpace(output.Stdout)
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return nil, fmt.Errorf("reading the mounts of container %s: %w", container, err)
	}

	mounts := make([]execution.ObservedMount, 0, len(decoded))
	for _, one := range decoded {
		mounts = append(mounts, execution.ObservedMount{
			Type: one.Type, Name: one.Name, Source: one.Source,
			Destination: one.Destination, Writable: one.Writable,
		})
	}
	return mounts, nil
}

// CheckMounts refuses a container whose mounts break a rule the security model
// states.
//
// Two rules, and each is a statement about the running system rather than about
// what Feat generated:
//
//   - no mount is a Docker socket, because a container with one controls the
//     host's Docker daemon and therefore the host;
//   - no mount has one of the project's ordinary repository checkouts as its
//     source, because the agent would then be able to edit the working copy the
//     task exists to leave alone.
//
// The second is the failure ADR-033 evidence 1 describes, and it is silent: a
// task with an extra mount behaves normally and every record Feat keeps about it
// is correct.
func (e *Environment) CheckMounts(mounts []execution.ObservedMount) error {
	var problems []error

	for _, mount := range mounts {
		if socket, inside := dockerSocket(mount); socket != "" {
			problems = append(problems, e.socketProblem(mount, socket, inside))
			continue
		}
		if checkout := e.forbidden(mount); checkout != "" {
			problems = append(problems, fmt.Errorf(
				"the container mounts the ordinary checkout %s at %s. The task works in its own worktree, "+
					"and an agent that can also reach the checkout can edit the working copy this task was "+
					"meant to leave alone. Set the repository's container_path to the path its Compose files "+
					"already mount it at, so Feat's generated override replaces that mount instead of adding "+
					"one beside it",
				checkout, mount.Destination))
		}
	}
	return errors.Join(problems...)
}

// socketProblem says which mount reaches a daemon socket, and how.
//
// The two forms are worth keeping apart. A mount of the socket itself is
// something a reader can see in their own Compose file; a mount of the directory
// holding it is not, and telling them "the container mounts /var/run/docker.sock"
// about the line `- /var/run:/var/run` would send them looking for a line that
// is not there.
func (e *Environment) socketProblem(mount execution.ObservedMount, socket string, inside bool) error {
	if inside {
		return fmt.Errorf(
			"the container mounts %s at %s, and the daemon socket %s is inside it. A container that can "+
				"reach a container runtime's socket controls this host's containers and therefore the host. "+
				"Feat never grants an agent Docker access; mount the directories the agent needs rather than "+
				"the one holding that socket, in the Compose files that define service %s",
			mount.Source, mount.Destination, socket, e.spec.Service)
	}
	return fmt.Errorf(
		"the container mounts the Docker socket %s at %s, which would give the agent control of "+
			"this host's Docker daemon. Feat never grants an agent Docker access; remove that mount "+
			"from the Compose files that define service %s",
		socket, mount.Destination, e.spec.Service)
}

// dockerSocket reports the daemon socket a mount exposes, and whether the mount
// reaches it through a directory rather than being the socket itself.
//
// Three ways, because a socket is a capability rather than a path. The mount is
// one of the sockets this package knows; the mount is a directory holding one of
// them, which is what `- /var/run:/var/run` does without naming it; or the mount
// is named like a runtime socket wherever it sits, which is what a rootless
// daemon under /run/user/<uid> and a Docker Desktop replacement under the user's
// home directory both produce.
func dockerSocket(mount execution.ObservedMount) (socket string, inside bool) {
	for _, known := range DockerSocketPaths {
		if samePath(mount.Source, known) {
			return mount.Source, false
		}
		if contains(mount.Source, known) {
			return known, true
		}
		if samePath(mount.Destination, known) {
			return mount.Destination, false
		}
	}
	// A socket by another name is still a socket. The destination counts as well
	// as the source: a path the container's own client will find is the same
	// capability as the host path it came from.
	if runtimeSocketName(mount.Source) {
		return mount.Source, false
	}
	if runtimeSocketName(mount.Destination) {
		return mount.Destination, false
	}
	return "", false
}

// runtimeSocketName reports whether a path is named like a runtime's socket.
func runtimeSocketName(value string) bool {
	if value == "" {
		return false
	}
	base := path.Base(normalize(value))
	for _, name := range runtimeSocketNames {
		if strings.HasSuffix(base, name) {
			return true
		}
	}
	return false
}

// forbidden reports the ordinary checkout a mount exposes, and "" otherwise.
//
// What is forbidden is the working copy, at any depth in either direction: the
// checkout itself, a directory containing it, and a directory inside it. The one
// exception is its Git directory, which carries history rather than the user's
// files and which the agent needs mounted for its worktree to be a repository at
// all. docs/05-security-model.md accepts that exposure by name and declines to
// call it repository-metadata isolation.
func (e *Environment) forbidden(mount execution.ObservedMount) string {
	if mount.Type != "bind" || mount.Source == "" {
		return ""
	}
	for _, checkout := range e.spec.ForbiddenSources {
		metadata := path.Join(checkout, gitDirName)
		if samePath(mount.Source, metadata) || contains(metadata, mount.Source) {
			continue
		}
		if samePath(mount.Source, checkout) || contains(mount.Source, checkout) ||
			contains(checkout, mount.Source) {
			return checkout
		}
	}
	return ""
}

// gitDirName is the Git metadata directory of an ordinary checkout.
const gitDirName = ".git"

// virtualPrefixes are the paths a container runtime puts in front of a host
// path when it reports one back.
//
// Docker Desktop shares the host filesystem through its own virtual machine, so
// a bind source can be reported as /host_mnt/Users/... rather than /Users/....
// The prefixes are stripped explicitly rather than matched by suffix: a suffix
// comparison would make /elsewhere/repos/api look like the configured /repos/api,
// and a mount check that reports a repository the user does not have is a check
// they will learn to ignore.
var virtualPrefixes = []string{"/host_mnt", "/run/desktop/mnt/host"}

// normalize strips a container runtime's own prefix and cleans the path.
func normalize(value string) string {
	cleaned := path.Clean(value)
	for _, prefix := range virtualPrefixes {
		if cleaned == prefix {
			return "/"
		}
		if after, found := strings.CutPrefix(cleaned, prefix+"/"); found {
			return path.Clean("/" + after)
		}
	}
	return cleaned
}

// samePath reports whether two paths name the same location.
func samePath(a, b string) bool { return normalize(a) == normalize(b) }

// contains reports whether the outer path holds the inner one.
func contains(outer, inner string) bool {
	outer, inner = normalize(outer), normalize(inner)
	if outer == "/" {
		return true
	}
	return strings.HasPrefix(inner, outer+"/")
}

// Sources renders the mounts for a diagnostic, sorted so the output repeats.
func Sources(mounts []execution.ObservedMount) []string {
	rendered := make([]string, 0, len(mounts))
	for _, mount := range mounts {
		access := "rw"
		if !mount.Writable {
			access = "ro"
		}
		source := mount.Source
		if mount.Type == "volume" && mount.Name != "" {
			source = mount.Name
		}
		rendered = append(rendered, fmt.Sprintf("%s -> %s (%s, %s)", source, mount.Destination, mount.Type, access))
	}
	sort.Strings(rendered)
	return rendered
}
