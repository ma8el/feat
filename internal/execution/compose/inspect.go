package compose

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/ma8el/feat/internal/execution"
	"github.com/ma8el/feat/internal/paths"
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

// hostConfiguration decodes the grants of `docker inspect`.
//
// Every field here is something the container runtime was asked for by the
// project's own Compose files and reports back on the container. What is
// decoded is what a rule below reads: a field nobody checks would be a claim
// that it had been looked at.
type hostConfiguration struct {
	Privileged  bool     `json:"Privileged"`
	CapAdd      []string `json:"CapAdd"`
	PidMode     string   `json:"PidMode"`
	IpcMode     string   `json:"IpcMode"`
	NetworkMode string   `json:"NetworkMode"`
	SecurityOpt []string `json:"SecurityOpt"`
	Devices     []struct {
		PathOnHost string `json:"PathOnHost"`
	} `json:"Devices"`
	// The two lists that say what a container may reach of the kernel's own
	// interfaces. They are read because one grant is visible nowhere else:
	// systempaths=unconfined never appears in SecurityOpt, and empties these
	// (ADR-067). Docker spells the second one Readonly and Go spells it
	// ReadOnly; the tag is Docker's.
	MaskedPaths   []string `json:"MaskedPaths"`
	ReadonlyPaths []string `json:"ReadonlyPaths"`
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
		observed := execution.ObservedMount{
			Type: one.Type, Name: one.Name, Source: one.Source,
			Destination: one.Destination, Writable: one.Writable,
		}
		if observed.Type == "volume" && observed.Name != "" {
			device, err := e.volumeDevice(ctx, observed.Name)
			if err != nil {
				return nil, err
			}
			observed.Device = device
		}
		mounts = append(mounts, observed)
	}
	return mounts, nil
}

// volumeDevice reports the host path a named volume is backed by, and "" for
// the ordinary volume that is backed by storage the runtime owns.
//
// It is a second question because the first one cannot answer it. A local
// volume declared `driver_opts: {type: none, device: /var/run, o: bind}` is a
// bind wearing a volume's name, and `docker inspect` reports it as
// {Type: "volume", Source: "/var/lib/docker/volumes/<name>/_data"} — the
// volume's own mountpoint, never the device. Measured on Docker Desktop
// 2026-08-19; the rules below all read a host path, and without this call there
// is no host path for them to read, so every one of them is one YAML
// indirection away from being bypassed.
//
// The volume is asked rather than the project's Compose files, for the reason
// Mounts gives: what exists is evidence and what was written is a claim.
func (e *Environment) volumeDevice(ctx context.Context, name string) (string, error) {
	output, err := e.runner.Run(ctx, execution.Invocation{
		Program:   e.docker,
		Arguments: []string{"volume", "inspect", "--format", "{{json .Options}}", name},
	})
	if err != nil {
		return "", err
	}
	if !output.Succeeded() {
		return "", fmt.Errorf(
			"reading the definition of volume %s, which is mounted into Compose project %s: %s. "+
				"Feat cannot tell what that volume is a window onto, so it will not start an agent in it",
			name, e.spec.Identity, firstLine(output.Stderr, output.Stdout))
	}

	trimmed := strings.TrimSpace(output.Stdout)
	if trimmed == "" || trimmed == "null" {
		return "", nil
	}
	var options map[string]string
	if err := json.Unmarshal([]byte(trimmed), &options); err != nil {
		return "", fmt.Errorf("reading the driver options of volume %s: %w", name, err)
	}
	return hostDevice(options["device"]), nil
}

// hostDevice reports the host path a volume's device option names, and "" when
// it names something that is not one.
//
// device belongs to the driver rather than to Docker: the local driver hands it
// to mount(2), so it is a host path for `type: none` and `type: ext4`, and a
// remote address for nfs (":/export") or cifs ("//server/share"). Only an
// absolute local path is a window onto this host, and refusing a network volume
// by naming a host path that does not exist would be a refusal nobody could act
// on.
func hostDevice(device string) string {
	if !strings.HasPrefix(device, "/") || strings.HasPrefix(device, "//") {
		return ""
	}
	return device
}

// Privileges reports what the running container was granted beyond its mounts.
//
// The mount rules read what a container can reach through its filesystem, and
// this is what it can reach around it. A container with CAP_SYS_ADMIN remounts
// the read-only control workspace read-write, so the read-only half of what
// Feat grants holds only while the runtime's default capability set does; one
// on the host's network namespace reaches a daemon on the host's own loopback
// with nothing mounted and no DOCKER_HOST for Endpoints to find.
//
// The container is asked rather than the configuration, for the reason Mounts
// gives (ADR-028, ADR-033).
func (e *Environment) Privileges(ctx context.Context, container string) (execution.ObservedPrivileges, error) {
	if container == "" {
		return execution.ObservedPrivileges{}, fmt.Errorf(
			"reading what Compose project %s grants its container needs a container", e.spec.Identity)
	}
	output, err := e.runner.Run(ctx, execution.Invocation{
		Program: e.docker,
		Arguments: []string{
			"inspect", "--type", "container", "--format", "{{json .HostConfig}}", container,
		},
	})
	if err != nil {
		return execution.ObservedPrivileges{}, err
	}
	if !output.Succeeded() {
		return execution.ObservedPrivileges{}, fmt.Errorf(
			"reading what container %s of Compose project %s was granted failed: %s",
			container, e.spec.Identity, firstLine(output.Stderr, output.Stdout))
	}

	trimmed := strings.TrimSpace(output.Stdout)
	if trimmed == "" || trimmed == "null" {
		// Nothing was read, which is not the same answer as nothing was
		// granted. Known stays false and Check refuses rather than assuming.
		return execution.ObservedPrivileges{}, nil
	}
	var decoded hostConfiguration
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return execution.ObservedPrivileges{}, fmt.Errorf(
			"reading what container %s was granted: %w", container, err)
	}

	privileges := execution.ObservedPrivileges{
		Known:         true,
		Privileged:    decoded.Privileged,
		PidMode:       decoded.PidMode,
		IpcMode:       decoded.IpcMode,
		NetworkMode:   decoded.NetworkMode,
		MaskedPaths:   decoded.MaskedPaths,
		ReadOnlyPaths: decoded.ReadonlyPaths,
	}
	for _, added := range decoded.CapAdd {
		privileges.Capabilities = append(privileges.Capabilities, capabilityName(added))
	}
	for _, option := range decoded.SecurityOpt {
		privileges.SecurityOptions = append(privileges.SecurityOptions, securityOption(option))
	}
	for _, device := range decoded.Devices {
		if device.PathOnHost != "" {
			privileges.Devices = append(privileges.Devices, device.PathOnHost)
		}
	}
	return privileges, nil
}

// securityOption splits one security_opt entry into the option and its value.
//
// Both separators, because both reach a running container: Compose passes
// `seccomp:unconfined` through to the daemon exactly as it passes
// `seccomp=unconfined`, measured rather than assumed (ADR-067). A rule written
// for one spelling would be a rule with the other spelling as its bypass, which
// is what capabilityName exists to prevent one field up.
//
// Only the first separator splits. A value carries both characters — a label is
// written `label=user:someone` and a seccomp profile arrives as JSON — and an
// entry cut at the wrong one is an option nobody named.
func securityOption(entry string) execution.SecurityOption {
	trimmed := strings.TrimSpace(entry)
	at := strings.IndexAny(trimmed, "=:")
	if at < 0 {
		return execution.SecurityOption{Name: strings.ToLower(trimmed)}
	}
	return execution.SecurityOption{
		Name:  strings.ToLower(trimmed[:at]),
		Value: trimmed[at+1:],
	}
}

// capabilityName is a capability as this package compares them.
//
// Docker keeps what the project wrote, and a project may write SYS_ADMIN,
// CAP_SYS_ADMIN, or cap_sys_admin. A comparison that missed one of the three
// would be a deny-list with a spelling as its bypass.
func capabilityName(value string) string {
	return strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(value)), "CAP_")
}

// DockerEndpointVariables are the environment entries that point a client at a
// container daemon over the network.
//
// docs/05-security-model.md forbids "Docker-over-TCP credentials" in the same
// breath as the socket, and this is the form they take: a container with one of
// these and a client to use it reaches a daemon with nothing mounted at all.
var DockerEndpointVariables = []string{
	"DOCKER_HOST",
	"DOCKER_TLS_VERIFY",
	"DOCKER_CERT_PATH",
	"DOCKER_CONTEXT",
}

// Endpoints reports which of those entries the container's own environment sets.
//
// It returns names and never values. A value carries a host, a port, and a path
// into somebody's filesystem, and what a refusal needs to say is which entry to
// remove — the project's own Compose files are where it is written.
//
// The container is asked rather than the configuration, for the reason Mounts
// gives: `docker compose config` would render the project including the values
// of environment files Feat must not read (ADR-028).
func (e *Environment) Endpoints(ctx context.Context, container string) ([]string, error) {
	if container == "" {
		return nil, fmt.Errorf("reading the environment of Compose project %s needs a container", e.spec.Identity)
	}
	output, err := e.runner.Run(ctx, execution.Invocation{
		Program: e.docker,
		Arguments: []string{
			"inspect", "--type", "container", "--format", "{{json .Config.Env}}", container,
		},
	})
	if err != nil {
		return nil, err
	}
	if !output.Succeeded() {
		return nil, fmt.Errorf("reading the environment of container %s of Compose project %s failed: %s",
			container, e.spec.Identity, firstLine(output.Stderr, output.Stdout))
	}

	trimmed := strings.TrimSpace(output.Stdout)
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	var decoded []string
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return nil, fmt.Errorf("reading the environment of container %s: %w", container, err)
	}

	var found []string
	for _, entry := range decoded {
		name, _, split := strings.Cut(entry, "=")
		if !split {
			continue
		}
		for _, endpoint := range DockerEndpointVariables {
			if name == endpoint {
				found = append(found, name)
			}
		}
	}
	sort.Strings(found)
	return found, nil
}

// CheckMounts refuses a container whose mounts break a rule the security model
// states.
//
// Two rules, and each is a statement about the running system rather than about
// what Feat generated:
//
//   - no mount is a container runtime's socket, because a container with one
//     controls the host's containers and therefore the host;
//   - no mount exposes one of the host paths the task's specification forbids —
//     an ordinary repository checkout, Feat's own runtime or state directory, or
//     the home directory of the user the daemon runs as;
//   - no path the task holds read-only is writable, which is invariant 6 asked
//     of the container rather than of the document Feat generated.
//
// Each of the first two reads the host path a mount reaches rather than the
// source the runtime reports, because those are not the same thing for a named
// volume: a local volume with a bind device is an ordinary bind wearing a
// volume's name, and reading the reported source would compare the volume's own
// mountpoint against every rule and find nothing.
//
// Both failures are silent: a task with an extra mount behaves normally and
// every record Feat keeps about it is correct. That is the failure ADR-033
// evidence 1 describes, and the reason this check reads the container.
//
// A mount reports at most one problem. The specification lists its forbidden
// paths in the order a refusal should explain them, because a single mount can
// expose several — a container that mounts the home directory reaches Feat's
// state through it — and a reader needs the most direct account of what they
// gave away rather than all of them.
func (e *Environment) CheckMounts(mounts []execution.ObservedMount) error {
	var problems []error

	for _, mount := range mounts {
		if exposure := dockerSocket(mount); exposure.reason != noSocket {
			problems = append(problems, e.socketProblem(mount, exposure))
			continue
		}
		if forbidden, found := e.forbidden(mount); found {
			problems = append(problems, e.forbiddenProblem(mount, forbidden))
			continue
		}
		if problem := e.writableProblem(mount); problem != nil {
			problems = append(problems, problem)
		}
	}
	return errors.Join(problems...)
}

// ForbiddenCapability is one capability an agent's container must not be given,
// with what holding it would let a process do.
//
// The reason travels with the name because a refusal has to survive contact
// with a reader who added the line for an unrelated purpose: "SYS_ADMIN is not
// allowed" invites an argument, and "SYS_ADMIN is mount(2)" ends one.
type ForbiddenCapability struct {
	// Name is the capability as Linux names it, without the CAP_ prefix.
	Name string
	// Because is what a process holding it can do, in one clause.
	Because string
}

// ForbiddenCapabilities are the capabilities that must not be added to the
// agent's container.
//
// docs/05-security-model.md accepts the container runtime's default capability
// set — "dropped capabilities beyond runtime defaults" is listed as not
// required — so what is refused here is an addition to those defaults rather
// than the defaults themselves. Each of these reaches past the container in a
// way the mount rules cannot see: the rules refuse a mount of the home
// directory, and a process that can mount the host's disk from inside has
// strictly more than that mount would have given it.
//
// SYS_PTRACE is deliberately absent. It is what a debugger needs, devcontainer
// templates add it for exactly that, and inside the container's own PID
// namespace it traces the agent's own processes; it reaches the host's only
// through a shared PID namespace, which is refused on its own below.
var ForbiddenCapabilities = []ForbiddenCapability{
	{"ALL", "every capability at once, including all of those below"},
	{"SYS_ADMIN", "mount(2): it remounts the read-only control workspace read-write, and mounts " +
		"the host's own filesystem from a block device"},
	{"SYS_MODULE", "loading a kernel module, and the kernel is the host's"},
	{"SYS_RAWIO", "raw access to devices and to physical memory"},
	{"SYS_BOOT", "rebooting the host"},
	{"DAC_READ_SEARCH", "open_by_handle_at(2): it reads any file on the host's filesystem, " +
		"mounted or not"},
	{"MAC_ADMIN", "changing the host's mandatory access control policy"},
	{"MAC_OVERRIDE", "overriding the host's mandatory access control policy"},
	{"BPF", "loading BPF programs into the host's kernel"},
	{"PERFMON", "reading the host's performance counters, which includes other processes' memory"},
	{"SYSLOG", "reading the host's kernel log, which leaks kernel addresses"},
}

// ConfinementLayer is one restriction a container runtime applies to every
// container, named by the security_opt entry that switches it off.
//
// The reason travels with the name for the reason ForbiddenCapability's does. A
// reader who added the line to make one tool work is owed the sentence that ends
// the argument rather than the one that starts it: "seccomp=unconfined is not
// allowed" invites a reply, and "seccomp=unconfined is what makes the capability
// rule above a rule" does not.
type ConfinementLayer struct {
	// Option is the security_opt entry, as Docker names it.
	Option string
	// Because is what the layer denies, and therefore what removing it reaches.
	Because string
}

// UnconfinedValue is what a security_opt entry is set to in order to switch its
// layer off, as Docker spells it.
const UnconfinedValue = "unconfined"

// ConfinementLayers are the restrictions that must not be switched off for the
// agent's container.
//
// docs/05-security-model.md lists "custom seccomp/AppArmor policy" among the
// things the dogfood profile does not require, which is not the same statement
// as permitting the default one to be removed. Every rule in this file compares
// a name — a path, a capability, a namespace — and each layer here is what makes
// one of those rules enforceable rather than advisory: the capability deny-list
// is a rule about what Docker was asked to add, and the syscall filter is what
// stops a process manufacturing the same capability for itself.
//
// systempaths is the third of these and is checked separately below. It is the
// one that reports itself nowhere: the daemon consumes it rather than recording
// it, so the rule that finds it reads its effect (ADR-067).
var ConfinementLayers = []ConfinementLayer{
	{"seccomp", "the syscall filter every container is given by default. It is what keeps a process " +
		"in the container from calling unshare(2) into a user namespace of its own, where it holds the " +
		"CAP_SYS_ADMIN this check refuses in cap_add"},
	{"apparmor", "the profile Docker loads for every container on a host that carries AppArmor. It " +
		"denies mount(2) from inside the container, and denies writing /proc/sys and /proc/sysrq-trigger"},
}

// SystemPathsOption is the security_opt entry that unmasks the kernel
// interfaces a runtime hides from every container.
//
// It is a constant rather than a ConfinementLayer because no container reports
// it: `docker inspect` does not carry it under .HostConfig.SecurityOpt at all,
// since the daemon turns it into empty MaskedPaths and ReadonlyPaths when the
// container is created (measured, ADR-067). The rule below reads that effect and
// the message names this cause, so a reader is told what was observed before
// they are sent to a line in their own file.
const SystemPathsOption = "systempaths=unconfined"

// CheckPrivileges refuses a container granted more than its mounts.
//
// It is the other half of CheckMounts and asks the same kind of question: what
// the running container turned out to be, rather than what the generated
// override asked for. A project's own Compose files decide all of this, Feat
// generates none of it, and each grant below defeats a rule Feat does enforce —
// the read-only mounts, the forbidden host paths, and the Docker boundary that
// a daemon on the host's loopback sits outside of.
//
// The last two read what the container is confined by rather than what it holds.
// They are the same question asked of the enforcement: a rule about a capability
// nobody added is worth what the syscall filter and the mandatory access control
// profile are worth, and a mask nobody removed is what stands between a root
// process in the container and this host's memory.
func (e *Environment) CheckPrivileges(privileges execution.ObservedPrivileges) error {
	if !privileges.Known {
		return fmt.Errorf(
			"what the container of service %s in Compose project %s was granted could not be read, so Feat "+
				"cannot tell whether it is privileged, shares this host's namespaces, or holds capabilities "+
				"that would undo the mounts it does check",
			e.spec.Service, e.spec.Identity)
	}

	var problems []error
	if privileges.Privileged {
		problems = append(problems, fmt.Errorf(
			"service %s runs privileged, which grants every capability and every host device at once. "+
				"A privileged container is the host: it remounts the control workspace Feat mounts "+
				"read-only, mounts the host's filesystem from /dev, and loads kernel modules. Remove "+
				"privileged: true from the Compose files that define service %s",
			e.spec.Service, e.spec.Service))
	}

	for _, added := range forbiddenCapabilities(privileges.Capabilities) {
		problems = append(problems, fmt.Errorf(
			"service %s is granted the capability %s beyond the container runtime's defaults, which is %s. "+
				"Feat's read-only mounts and its refusal of host paths both hold only while the agent's "+
				"process cannot do that. Remove it from the cap_add of the Compose files that define "+
				"service %s",
			e.spec.Service, added.Name, added.Because, e.spec.Service))
	}

	for _, namespace := range []struct{ mode, field, consequence string }{
		{privileges.PidMode, "pid: host",
			"every process on this machine is visible to the agent, and the isolation the other rules " +
				"assume is the isolation this one removes"},
		{privileges.IpcMode, "ipc: host",
			"the shared memory of every process on this machine is readable and writable from inside"},
		{privileges.NetworkMode, "network_mode: host",
			"the agent reaches every service bound to this machine's loopback, including a container " +
				"daemon listening on 127.0.0.1:2375 — which needs no socket mounted and no DOCKER_HOST " +
				"for the environment check to find"},
	} {
		if !hostNamespace(namespace.mode) {
			continue
		}
		problems = append(problems, fmt.Errorf(
			"service %s shares this host's namespace (%s), so %s. Remove it from the Compose files that "+
				"define service %s",
			e.spec.Service, namespace.field, namespace.consequence, e.spec.Service))
	}

	if len(privileges.Devices) > 0 {
		problems = append(problems, fmt.Errorf(
			"service %s is given the host devices %s. A device is the host's hardware or storage "+
				"directly, and a block device is every file on the filesystem it holds, whatever the "+
				"mount rules say about paths. Remove the devices: entries from the Compose files that "+
				"define service %s",
			e.spec.Service, strings.Join(privileges.Devices, ", "), e.spec.Service))
	}

	for _, switched := range switchedOff(privileges.SecurityOptions) {
		problems = append(problems, fmt.Errorf(
			"service %s runs with %s=%s, which removes %s. The rules above are rules about names a "+
				"project wrote, and this is the enforcement they stand on: with it gone, what the agent's "+
				"process may do is bounded by the kernel rather than by anything Feat or the runtime can "+
				"be asked to show. Remove %s=%s from the security_opt of the Compose files that define "+
				"service %s",
			e.spec.Service, switched.Option, UnconfinedValue, switched.Because,
			switched.Option, UnconfinedValue, e.spec.Service))
	}

	if unmasked(privileges) {
		problems = append(problems, fmt.Errorf(
			"service %s has none of the kernel interfaces a container runtime hides from every "+
				"container: nothing masks /proc/kcore, which is this host's physical memory, and "+
				"/proc/sys and /proc/sysrq-trigger are writable rather than read-only. A root process "+
				"in that container reboots this machine with one write to /proc/sysrq-trigger, and "+
				"names in /proc/sys/kernel/core_pattern a program the host's kernel then runs as root "+
				"outside the container — and root in the container is one sudo away in any image "+
				"carrying the NOPASSWD rule devcontainer templates ship, which is what the launch "+
				"reports separately. None of it needs a capability the rules above read or a mount the "+
				"rules below do. The line that produces this is security_opt: %s; remove it from the "+
				"Compose files that define service %s",
			e.spec.Service, SystemPathsOption, e.spec.Service))
	}
	return errors.Join(problems...)
}

// switchedOff reports which confinement layers the container's own security_opt
// turns off, in the order this package lists them.
func switchedOff(options []execution.SecurityOption) []ConfinementLayer {
	var found []ConfinementLayer
	for _, layer := range ConfinementLayers {
		for _, option := range options {
			if option.Name == layer.Option && switchesOff(option) {
				found = append(found, layer)
				break
			}
		}
	}
	return found
}

// switchesOff reports whether one entry sets a layer this package refuses to
// lose to no policy at all.
//
// The value is compared without case for the reason hostNamespace ignores it,
// and the name arrives lowercased from securityOption: a deny-list answered by a
// spelling is not one.
func switchesOff(option execution.SecurityOption) bool {
	if !strings.EqualFold(strings.TrimSpace(option.Value), UnconfinedValue) {
		return false
	}
	return slices.ContainsFunc(ConfinementLayers, func(layer ConfinementLayer) bool {
		return layer.Option == option.Name
	})
}

// unmasked reports whether the container reaches the kernel interfaces a runtime
// hides from every container.
//
// Both lists have to be empty, and the container must not be privileged. Docker
// reports the two as null for a privileged container and as [] for one given
// systempaths=unconfined (measured, ADR-067); privileged is refused above by the
// line that produced it, and adding a second refusal naming a security_opt entry
// nobody wrote would send that reader looking for a line that is not there.
//
// A runtime that reported neither list would be read as unmasked here. That is
// the direction this whole file errs in — an unread answer is never the
// reassuring one — and the refusal says what was observed before it names the
// line, so a reader on such a runtime is told something true.
func unmasked(privileges execution.ObservedPrivileges) bool {
	return !privileges.Privileged &&
		len(privileges.MaskedPaths) == 0 && len(privileges.ReadOnlyPaths) == 0
}

// unevaluatedOptions are the security_opt entries a launch reports rather than
// refuses: the ones that replace a layer of confinement instead of removing it.
//
// A custom seccomp or AppArmor profile can be stricter than the runtime's
// default or can allow every syscall — `{"defaultAction":"SCMP_ACT_ALLOW"}` is
// unconfined under another name — and an SELinux label option decides which
// policy the host applies. Telling any of those apart means reading a policy,
// and every other rule here compares a name.
//
// So they are said out loud rather than refused, which is ADR-066's decision
// applied to the same kind of fact: the project is entitled to configure this
// and the next person is not entitled to be surprised by it. Refusing instead
// would refuse the user who hardened their container, and the edit that answered
// the refusal would be deleting the profile.
//
// no-new-privileges is left silent. It only ever tightens — it is the flag that
// stops a setuid binary handing back privilege — and a warning about a container
// doing better than the default is a warning people learn to skip, which is the
// reasoning EscalationTools' own presence check follows.
func unevaluatedOptions(privileges execution.ObservedPrivileges) []execution.SecurityOption {
	var found []execution.SecurityOption
	for _, option := range privileges.SecurityOptions {
		if option.Name == noNewPrivilegesOption || switchesOff(option) {
			continue
		}
		found = append(found, option)
	}
	return found
}

// noNewPrivilegesOption is the one security_opt entry that only ever confines
// further.
const noNewPrivilegesOption = "no-new-privileges"

// forbiddenCapabilities reports which of the added capabilities must not have
// been added, in the order this package lists them.
func forbiddenCapabilities(added []string) []ForbiddenCapability {
	var found []ForbiddenCapability
	for _, forbidden := range ForbiddenCapabilities {
		for _, one := range added {
			if capabilityName(one) == forbidden.Name {
				found = append(found, forbidden)
				break
			}
		}
	}
	return found
}

// hostNamespace reports whether a namespace mode is the host's own.
//
// The other values are the container's own namespace, another container's, or a
// Compose service's, and none of those is this host's.
func hostNamespace(mode string) bool {
	return strings.EqualFold(strings.TrimSpace(mode), "host")
}

// writableProblem reports a path the task holds read-only that the container
// reports the agent can write through.
//
// read_only: true in the generated override is a request, and this is the answer
// to it. Compose merges a service's volumes by target, so what a path ends up
// being depends on every file in the project as well as on Feat's: volumes_from
// copies another service's bindings, and a static override applied after Feat's
// replaces them. Nothing else in the product asks this question — the field is
// decoded from `docker inspect` and was until now read by nobody — and a task
// that can write the code it said it may only read looks correct in every record
// Feat keeps.
func (e *Environment) writableProblem(mount execution.ObservedMount) error {
	if !mount.Writable {
		return nil
	}
	for _, own := range e.spec.Mounts {
		if own.ReadOnly && samePath(mount.Destination, own.Target) {
			return e.writableRefusal(mount, own.Description)
		}
	}
	for _, volume := range e.spec.Volumes {
		if volume.ReadOnly && samePath(mount.Destination, volume.Target) {
			return e.writableRefusal(mount, "the "+volume.Name+" volume, read-only")
		}
	}
	return nil
}

// writableRefusal says which read-only path turned out to be writable.
func (e *Environment) writableRefusal(mount execution.ObservedMount, description string) error {
	return fmt.Errorf(
		"the container mounts %s at %s read-write, and this task holds that path read-only (%s). "+
			"Feat's generated override asks for read_only: true there, so something else decides it — "+
			"a volumes_from that copies another service's bindings, or an override applied after Feat's. "+
			"An agent that can write what the task said it may only read makes every record Feat keeps "+
			"about that repository wrong; make that path read-only in the Compose files that define "+
			"service %s",
		mount.Describe(), mount.Destination, description, e.spec.Service)
}

// forbiddenProblem says what a mount exposed and what to do about it.
//
// Every message names the mount as the container reports it, then the path it
// exposes, then the one edit that removes it. These are the user's only route
// out of a refused launch: the mount is in the project's own Compose files,
// which Feat does not edit and deliberately cannot read the environment of.
func (e *Environment) forbiddenProblem(mount execution.ObservedMount, forbidden execution.ForbiddenSource) error {
	switch forbidden.Kind {
	case execution.ForbiddenCheckout:
		return fmt.Errorf(
			"the container mounts %s at %s, which exposes %s. The task works in its own worktree, "+
				"and an agent that can also reach the checkout can edit the working copy this task was "+
				"meant to leave alone. Set the repository's container_path to the path its Compose files "+
				"already mount it at, so Feat's generated override replaces that mount instead of adding "+
				"one beside it",
			mount.Describe(), mount.Destination, exposed(forbidden, mount))

	case execution.ForbiddenStableCheckout:
		return fmt.Errorf(
			"the container mounts %s at %s, which exposes %s. Feat mounts that checkout read-only at %s "+
				"already, because the project declared the repository stable; a second mount of it gives the "+
				"agent a writable path to the user's own working copy beside the read-only one. Remove that "+
				"mount from the Compose files that define service %s, or give the repository the "+
				"container_path those files already use",
			mount.Describe(), mount.Destination, exposed(forbidden, mount), e.target(forbidden.Path), e.spec.Service)

	case execution.ForbiddenRuntime:
		return fmt.Errorf(
			"the container mounts %s at %s, which exposes %s. A process that can reach the tmux socket runs "+
				"commands on this host outside the container, and one that can reach the daemon's socket can "+
				"launch, control, and clean up every task on this machine. Remove that mount from the Compose "+
				"files that define service %s, or set %s to a directory they do not mount",
			mount.Describe(), mount.Destination, exposed(forbidden, mount), e.spec.Service, paths.EnvRuntimeOverride)

	case execution.ForbiddenState:
		return fmt.Errorf(
			"the container mounts %s at %s, which exposes %s. This task's own control workspace is mounted "+
				"already and is the only part of it the agent may see; the rest is every other task's "+
				"workspace, brief, and event log. Remove that mount from the Compose files that define "+
				"service %s",
			mount.Describe(), mount.Destination, exposed(forbidden, mount), e.spec.Service)

	case execution.ForbiddenHome:
		return fmt.Errorf(
			"the container mounts %s at %s, which exposes %s. Feat does not allow home directory mounts. "+
				"Remove that mount from the Compose files that define service %s, and mount the "+
				"directories the agent actually needs",
			mount.Describe(), mount.Destination, exposed(forbidden, mount), e.spec.Service)
	}

	return fmt.Errorf("the container mounts %s at %s, which exposes %s. Remove that mount from the "+
		"Compose files that define service %s",
		mount.Describe(), mount.Destination, exposed(forbidden, mount), e.spec.Service)
}

// target is where this task's own specification mounts a host path, or the path
// itself when it mounts it nowhere.
//
// It exists so that a refusal about a second mount of something Feat mounts can
// say where the first one is. A user reading "remove that mount" needs to know
// which of the two is theirs.
func (e *Environment) target(source string) string {
	for _, own := range e.spec.Mounts {
		if samePath(own.Source, source) {
			return own.Target
		}
	}
	return source
}

// exposed names the forbidden path a mount exposes, saying which it is when the
// mount is not that path itself.
//
// `- /tmp:/tmp` and a mount of the runtime directory are the same refusal and
// read as different problems, and the first is the one nobody would find without
// being told the path.
func exposed(forbidden execution.ForbiddenSource, mount execution.ObservedMount) string {
	if samePath(mount.HostPath(), forbidden.Path) {
		return forbidden.Kind.Describe()
	}
	return forbidden.Path + ", " + forbidden.Kind.Describe()
}

// socketProblem says which mount reaches a daemon socket, and how.
//
// The forms are worth keeping apart. A mount of the socket itself is something
// a reader can see in their own Compose file; a mount of the directory holding
// it is not, and telling them "the container mounts /var/run/docker.sock" about
// the line `- /var/run:/var/run` would send them looking for a line that is not
// there.
func (e *Environment) socketProblem(mount execution.ObservedMount, exposure socketExposure) error {
	switch exposure.reason {
	case socketInDirectory:
		return fmt.Errorf(
			"the container mounts %s at %s, and the daemon socket %s is inside it. A container that can "+
				"reach a container runtime's socket controls this host's containers and therefore the host. "+
				"Feat never grants an agent Docker access; mount the directories the agent needs rather than "+
				"the one holding that socket, in the Compose files that define service %s",
			mount.Describe(), mount.Destination, exposure.path, e.spec.Service)

	case runtimeDirectory:
		return fmt.Errorf(
			"the container mounts %s at %s, and %s is where a rootless container runtime puts its API "+
				"socket. Feat cannot enumerate that path — the uid in it is the user's — so a directory "+
				"there is refused rather than searched: a container that can reach a runtime's socket "+
				"controls this host's containers and therefore the host. Mount the paths the agent needs "+
				"rather than that directory, in the Compose files that define service %s",
			mount.Describe(), mount.Destination, exposure.path, e.spec.Service)
	}

	// The socket is named twice when the mount is not the socket itself — a
	// volume with a socket for a device, or a source that lands on one. What a
	// reader has to find in their own Compose file is the mount, and what makes
	// it a refusal is the socket; a message with only one of the two sends them
	// looking for a line that is not there.
	if description := mount.Describe(); !samePath(description, exposure.path) {
		return fmt.Errorf(
			"the container mounts %s at %s, and %s is a container runtime's API socket. A container that "+
				"can reach one controls this host's containers and therefore the host. Feat never grants "+
				"an agent Docker access; remove that mount from the Compose files that define service %s",
			description, mount.Destination, exposure.path, e.spec.Service)
	}
	return fmt.Errorf(
		"the container mounts the Docker socket %s at %s, which would give the agent control of "+
			"this host's Docker daemon. Feat never grants an agent Docker access; remove that mount "+
			"from the Compose files that define service %s",
		exposure.path, mount.Destination, e.spec.Service)
}

// socketExposure is how one mount reaches a container runtime's API socket.
type socketExposure struct {
	// path is the socket, or the directory that holds or would hold one.
	path string
	// reason is which of the ways below found it, because each is a different
	// thing to tell the reader.
	reason socketReason
}

// socketReason names the way a mount reaches a runtime socket.
type socketReason int

const (
	// noSocket is a mount that reaches none.
	noSocket socketReason = iota
	// socketItself is a mount of a socket, by a known path or by name.
	socketItself
	// socketInDirectory is a mount of a directory a known socket sits in.
	socketInDirectory
	// runtimeDirectory is a mount of the per-user runtime directory a rootless
	// daemon puts its socket in, whose path cannot be enumerated.
	runtimeDirectory
)

// dockerSocket reports the daemon socket a mount exposes and how it reaches it.
//
// Several ways, because a socket is a capability rather than a path. The mount
// is one of the sockets this package knows; it is a directory holding one of
// them, which is what `- /var/run:/var/run` does without naming it; it is named
// like a runtime socket wherever it sits, which is what a Docker Desktop
// replacement under the user's home directory produces; or it is the per-user
// runtime directory a rootless daemon puts its socket in, which is the case
// runtimeSocketNames' own comment says the known paths cannot enumerate.
//
// The host path is read rather than the reported source, so that a bind-backed
// named volume is examined as the bind it is. Both ends are read: a path the
// container's own client will find is the same capability as the host path it
// came from, and Feat cannot see what a host directory holds, so a mount of
// something on this machine that lands where a socket lives is refused by
// where it lands.
func dockerSocket(mount execution.ObservedMount) socketExposure {
	source := mount.HostPath()
	// The two rules that read where a mount lands rather than where it comes
	// from need it to come from somewhere: a tmpfs at /run, which is how a
	// devcontainer running systemd is written, holds nothing of this machine's
	// however it is mounted, and neither does a volume the runtime backs
	// itself.
	fromTheHost := source != ""

	for _, known := range DockerSocketPaths {
		switch {
		case samePath(source, known):
			return socketExposure{path: source, reason: socketItself}
		case contains(source, known):
			return socketExposure{path: known, reason: socketInDirectory}
		case samePath(mount.Destination, known):
			return socketExposure{path: mount.Destination, reason: socketItself}
		case fromTheHost && contains(mount.Destination, known):
			return socketExposure{path: known, reason: socketInDirectory}
		}
	}
	if runtimeSocketName(source) {
		return socketExposure{path: source, reason: socketItself}
	}
	if runtimeSocketName(mount.Destination) {
		return socketExposure{path: mount.Destination, reason: socketItself}
	}
	if directory := rootlessRuntimeDirectory(source); directory != "" {
		return socketExposure{path: directory, reason: runtimeDirectory}
	}
	if fromTheHost {
		if directory := rootlessRuntimeDirectory(mount.Destination); directory != "" {
			return socketExposure{path: directory, reason: runtimeDirectory}
		}
	}
	return socketExposure{}
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

// runtimeNames are what a container runtime calls itself, which is what it
// names its own directory inside a per-user runtime directory.
var runtimeNames = []string{"docker", "podman", "containerd"}

// rootlessRuntimeDirectory reports the per-user runtime directory a path
// exposes, and "" when it exposes none.
//
// A rootless daemon puts its socket under /run/user/<uid>, where the uid is the
// user's own: no fixed path names it, which is why DockerSocketPaths cannot
// hold it and why `- /run/user/1000:/run/user/1000` passes every rule above.
// The directory is refused rather than searched, because what a host directory
// holds is not something this check can see.
//
// What is matched is the runtime directory itself and a runtime's own directory
// inside it — /run/user/1000/podman, which is where rootless podman puts
// podman.sock. A path inside it that is neither is left alone: a project that
// mounts one file of a session bus has not mounted a daemon socket, and a rule
// that refused every path under that directory would refuse it with no way out.
func rootlessRuntimeDirectory(value string) string {
	if value == "" {
		return ""
	}
	parts := strings.Split(strings.TrimPrefix(normalize(value), "/"), "/")
	if len(parts) < 2 || parts[0] != "run" || parts[1] != "user" {
		return ""
	}
	if len(parts) == 2 {
		return "/run/user"
	}
	for _, digit := range parts[2] {
		if digit < '0' || digit > '9' {
			return ""
		}
	}
	switch len(parts) {
	case 3:
		return path.Join("/run/user", parts[2])
	case 4:
		if slices.Contains(runtimeNames, parts[3]) {
			return path.Join("/run/user", parts[2], parts[3])
		}
	}
	return ""
}

// forbidden reports the protected host path a mount exposes.
//
// What is protected is exposed at any depth in either direction: the path
// itself, a directory containing it, and a directory inside it. A checkout has
// one exception, its Git directory, which carries history rather than the user's
// files and which the agent needs mounted for its worktree to be a repository at
// all. docs/05-security-model.md accepts that exposure by name and declines to
// call it repository-metadata isolation.
//
// The home directory is the one kind matched in a single direction. Mounts
// inside it are ordinary: the security model's own list of categories ends with
// "explicitly configured environment/credential mounts", a task's worktrees
// usually live there, and refusing them would refuse the configuration the
// product documents.
//
// What is compared is the host path the mount reaches rather than the source
// the runtime reports. For a bind those are the same; for a named volume the
// reported source is the volume's own mountpoint, and comparing it would let
// `driver_opts: {type: none, device: <any forbidden path>, o: bind}` past every
// rule here while the plain spelling of the same mount is refused.
func (e *Environment) forbidden(mount execution.ObservedMount) (execution.ForbiddenSource, bool) {
	source := mount.HostPath()
	if source == "" || e.declared(mount) {
		return execution.ForbiddenSource{}, false
	}
	for _, forbidden := range e.spec.ForbiddenSources {
		switch forbidden.Kind {
		case execution.ForbiddenCheckout, execution.ForbiddenStableCheckout:
			metadata := path.Join(forbidden.Path, gitDirName)
			if samePath(source, metadata) || contains(metadata, source) {
				continue
			}
		}
		if samePath(source, forbidden.Path) || contains(source, forbidden.Path) {
			return forbidden, true
		}
		if forbidden.Kind != execution.ForbiddenHome && contains(forbidden.Path, source) {
			return forbidden, true
		}
	}
	return execution.ForbiddenSource{}, false
}

// declared reports whether an observed mount is one this task asked for.
//
// Feat's own mounts sit inside the directories the rules above protect: the
// control workspace is under the state directory, and worktrees and Git
// directories are usually under the home directory. Without this the check would
// refuse the launch it exists to protect.
//
// Source and destination must both match. The same source at a second target is
// a different mount, and a second mount of the control workspace would give the
// agent the host-only agent/ directory read-write — which is the boundary
// ADR-032 draws and the one the read-only split of controlMounts implements.
//
// A named volume is not compared here at all. Feat declares its volumes by name
// and a volume with a device is a bind whatever it is called, so a volume that
// reaches a forbidden host path is refused even if its name and target are
// Feat's own: the name is what the project wrote and the device is what it
// does.
func (e *Environment) declared(mount execution.ObservedMount) bool {
	if mount.Type == "volume" {
		return false
	}
	for _, own := range e.spec.Mounts {
		if samePath(mount.Source, own.Source) && samePath(mount.Destination, own.Target) {
			return true
		}
	}
	return false
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
		// A volume is named by the name the project wrote, and by the host path
		// it is backed by when it has one. The name alone is the reassuring
		// half: `hostrun -> /var/run (volume, rw)` reads as a named volume,
		// which is a category the security model blesses, and says nothing
		// about the volume being a window onto /var/run.
		source := mount.Source
		if mount.Type == "volume" && mount.Name != "" {
			source = mount.Name
			if mount.Device != "" {
				source = mount.Name + " (" + mount.Device + ")"
			}
		}
		rendered = append(rendered, fmt.Sprintf("%s -> %s (%s, %s)", source, mount.Destination, mount.Type, access))
	}
	sort.Strings(rendered)
	return rendered
}
