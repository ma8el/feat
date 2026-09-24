package domain

import (
	"net"
	"strconv"
	"strings"
	"time"
)

// RuntimeEnvironment is the application environment associated with one task. It
// stays separate from the agent's execution environment even when both use
// Compose: that is how the agent runs, and this is what the user tests.
type RuntimeEnvironment struct {
	// Provider identifies the runtime adapter, such as the Compose adapter.
	Provider string
	// Identity is the unique runtime identity, which is the Compose project
	// name for the Compose adapter. It is what makes an action affect one
	// task's services and no other's.
	Identity string
	// Composition is what the application is made of: one entry per repository
	// that brings Compose files, each with the directory its own relative paths
	// resolve against.
	Composition []RuntimeSource
	// GeneratedIncludePath is the Compose include document Feat generated to
	// join the composition into one application.
	GeneratedIncludePath string
	// StaticOverrides are user-authored override files, in order.
	StaticOverrides []string
	// GeneratedOverridePath is the override Feat generated for the task. It
	// carries mounts, labels, and generated non-secret variables, never copied
	// secret values.
	GeneratedOverridePath string
	// EnvFiles are host-side environment files, passed to the runtime by path
	// so that Feat never reads their values.
	EnvFiles []string
	// Services are the services the task runs.
	Services []string
	// Provenance says where each managed service's code comes from. It is
	// resolved when the runtime is, from configuration and the project's own
	// Compose files, rather than discovered from the containers afterwards.
	Provenance []ServiceProvenance
	// Allocations are the host ports Feat reserved for this task's reachable
	// services. They are held for as long as the runtime exists and released
	// when it becomes absent.
	Allocations []PortAllocation
	// Ports are the observed port publications.
	Ports []PortAssignment
	// Networks are the observed networks the runtime owns.
	Networks []string
	// Volumes are the observed volumes the runtime owns. They are retained by
	// default during cleanup.
	Volumes []string
	// State is the observed lifecycle state.
	State RuntimeState
	// Health is the observed service health, which is separate from the
	// lifecycle state.
	Health HealthState
	// ObservedAt is when the state, health, and resource lists were last
	// observed.
	ObservedAt time.Time
	// Generation counts how many times this record has been changed, so a stale
	// answer cannot be written over a runtime that was destroyed and re-created
	// while the question was in flight (ADR-065 evidence 16). It counts rather
	// than timestamps, because the daemon reads its clock once per operation. It
	// starts at one, because a record that exists has been written once.
	Generation uint64
}

// ServiceProvenance is where one managed service's code comes from. Getting it
// wrong is silent — a healthy runtime that is not running the task's work — so it
// is resolved before a start, from configuration and the project's own Compose
// files (ADR-065 evidence 7). Mounted and built are kept apart because a mount
// is current when a file is written and an image is not (ADR-065 evidence 9).
type ServiceProvenance struct {
	// Service is the managed service.
	Service string
	// Repositories are the repositories that asked Feat to manage the service,
	// which are the ones whose code it is meant to run.
	Repositories []string
	// Mounted are the repositories whose task worktree the service mounts.
	Mounted []string
	// Built are the repositories whose task worktree the service's image is
	// built from.
	Built []string
}

// RunsTaskCode reports whether any of the task's work reaches the service.
func (p ServiceProvenance) RunsTaskCode() bool { return len(p.Mounted) > 0 || len(p.Built) > 0 }

// Baked are the repositories whose code reaches the service through its image
// alone, so a change appears there only once the image is built again. A
// repository that is also mounted is not one of them, because the mount is what
// the service reads.
func (p ServiceProvenance) Baked() []string {
	if len(p.Built) == 0 {
		return nil
	}
	mounted := make(map[string]bool, len(p.Mounted))
	for _, repository := range p.Mounted {
		mounted[repository] = true
	}
	var baked []string
	for _, repository := range p.Built {
		if !mounted[repository] {
			baked = append(baked, repository)
		}
	}
	return baked
}

// PortAssignment is one published port of one service.
type PortAssignment struct {
	// Service is the service publishing the port.
	Service string
	// ContainerPort is the port inside the container.
	ContainerPort int
	// HostPort is the port published on the host.
	HostPort int
	// HostIP is the address the container runtime reported the port bound on. It
	// is read back rather than assumed, so a binding wider than the allocation
	// asked for is visible. It is empty when the runtime reported none.
	HostIP string
}

// PortAllocation is one host port Feat reserved for one service of one task. It
// is an intention, where PortAssignment is what `docker compose ps` reported;
// both are kept because a container that never started publishes nothing.
//
// A host port is global to the machine, so an allocation is held against every
// other task while the runtime exists and released when it becomes absent. That
// is what lets several tasks run the same application (ADR-065 evidence 8).
type PortAllocation struct {
	// Service is the managed service the port belongs to.
	Service string
	// ContainerPort is the port inside the container, which the project's own
	// Compose files declare.
	ContainerPort int
	// HostPort is the port Feat reserved on the host.
	HostPort int
	// Protocol is "tcp" or "udp". A host port is per protocol, so two
	// allocations may share a number when their protocols differ.
	Protocol string
	// HostIP is the host address this port is published on: the one the
	// project's Compose file named, or its configured runtime.bind_address. Feat
	// tells the user an address read from the same field the generated document
	// binds. It is empty only in a record written before Feat had a bind
	// address, and such a record's containers were given every address.
	HostIP string
}

// Address is where the service is reached from this machine. A loopback or
// wildcard binding is reached at localhost, which is what a user would type; a
// particular address is said as itself. A client that needs the literal address
// reads HostIP rather than parsing this.
func (p PortAllocation) Address() string {
	return net.JoinHostPort(p.host(), strconv.Itoa(p.HostPort))
}

// URL is the address as a client would open it, and whether there is one. Only
// a stream port has one. The scheme is http because Feat cannot know what a
// service speaks; a project terminating TLS composes its own address from the
// port.
func (p PortAllocation) URL() (string, bool) {
	if p.Protocol != "tcp" {
		return "", false
	}
	return "http://" + p.Address(), true
}

// host names the machine address a publication is reached at. The loopback
// addresses become localhost, and so does a wildcard binding, which is reached
// there too. What such a binding also allows is said beside the address by a
// surface that asks BoundEverywhere, because this has to stay what a client
// dials.
func (p PortAllocation) host() string {
	trimmed := unbracket(p.HostIP)
	switch {
	case BoundEverywhere(trimmed), trimmed == "127.0.0.1", trimmed == "::1":
		return "localhost"
	default:
		return trimmed
	}
}

// BoundEverywhere reports whether a host address publishes on every interface
// the machine has. The unspecified addresses say so, and so does an empty one.
//
// It is exported because Address cannot answer it: a loopback port and a port on
// every interface are both dialled at localhost, while one of them is open to
// every network this machine is joined to (docs/05-security-model.md §
// Published ports and who can reach them).
func BoundEverywhere(hostIP string) bool {
	switch unbracket(hostIP) {
	case "", "0.0.0.0", "::":
		return true
	default:
		return false
	}
}

// unbracket strips the brackets an IPv6 literal may be carried in.
func unbracket(hostIP string) string {
	return strings.TrimSuffix(strings.TrimPrefix(hostIP, "["), "]")
}

// PortVariable is the generated variable naming one service's allocated host
// port, and URLVariable the address it makes. The naming rule lives in the
// domain because the daemon generates the variables and configuration refuses a
// project whose service names would collide into one.
func PortVariable(service string) string { return portVariablePrefix + variableToken(service) }

// URLVariable names the address of one service's allocated host port.
func URLVariable(service string) string { return urlVariablePrefix + variableToken(service) }

// The prefixes of the generated addressing variables.
//
// HOST is in the name because the name is all a user sees where the value is
// written. These carry a host address, and a service calling a sibling wants
// the Compose service name and the container port instead. Under the older
// FEAT_URL_ that mistake failed as a silent connection refused (G4-08,
// ADR-065's amendment of 2026-08-22).
const (
	portVariablePrefix = "FEAT_HOST_PORT_"
	urlVariablePrefix  = "FEAT_HOST_URL_"
)

// variableToken renders a service name as part of an environment variable name:
// upper case, with everything that is not a letter or a digit replaced. The
// rendering is lossy, because Compose allows dots and hyphens where a variable
// name does not, so configuration refuses a project whose services collide.
func variableToken(service string) string {
	rendered := make([]rune, 0, len(service))
	for _, r := range service {
		switch {
		case r >= 'a' && r <= 'z':
			rendered = append(rendered, r-'a'+'A')
		case (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			rendered = append(rendered, r)
		default:
			rendered = append(rendered, '_')
		}
	}
	return string(rendered)
}

// Validate reports whether the runtime environment is internally consistent.
func (r *RuntimeEnvironment) Validate(task TaskID) error {
	id := task.String()
	if r.Provider == "" {
		return &ValidationError{Entity: "runtime", ID: id, Field: "provider", Reason: "must not be empty"}
	}
	if r.Identity == "" {
		return &ValidationError{
			Entity: "runtime",
			ID:     id,
			Field:  "identity",
			Reason: "must not be empty, because every runtime action resolves the task's own identity",
		}
	}
	if !r.State.Valid() {
		return &ValidationError{
			Entity: "runtime",
			ID:     id,
			Field:  "state",
			Reason: "must be a documented runtime state, but is " + quote(string(r.State)),
		}
	}
	if !r.Health.Valid() {
		return &ValidationError{
			Entity: "runtime",
			ID:     id,
			Field:  "health",
			Reason: "must be a documented health state, but is " + quote(string(r.Health)),
		}
	}
	return nil
}

// RuntimeInputs are the exact values a runtime was created from. They are
// recorded rather than recomputed, because a later action must reach the
// resources the task owns and configuration may have been edited since
// (docs/07-configuration-model.md).
type RuntimeInputs struct {
	Provider              string
	Identity              string
	Composition           []RuntimeSource
	GeneratedIncludePath  string
	StaticOverrides       []string
	GeneratedOverridePath string
	EnvFiles              []string
	Services              []string
	// Allocations are the host ports reserved for this runtime. They are an
	// input rather than an observation because they are held: the generated
	// override publishes the recorded ones, so a second task cannot be given a
	// port the first is still using.
	Allocations []PortAllocation
}

// RuntimeSource is one repository's contribution to a task's application. A
// runtime is composed of repositories rather than of a flat list of files,
// because the repository decides what relative paths resolve against (ADR-065).
type RuntimeSource struct {
	// Repository identifies the repository within the project.
	Repository string
	// Directory is the repository's ordinary checkout, which is the project
	// directory of its include entry.
	Directory string
	// Files are that repository's own Compose files, in order.
	Files []string
}

// NewRuntimeEnvironment records a task's application runtime before anything
// exists for it. It starts absent with unknown health, because state and health
// are observations and nothing has been observed yet.
func NewRuntimeEnvironment(inputs RuntimeInputs) *RuntimeEnvironment {
	runtime := &RuntimeEnvironment{State: RuntimeAbsent, Health: HealthUnknown, Generation: 1}
	runtime.apply(inputs)
	return runtime
}

// ReplaceInputs re-resolves the runtime from current configuration. It is
// refused unless the runtime is absent: a user who edits their Compose files
// with services running must not have the next stop reach a different Compose
// project, and one who fixed them after destroying everything gets the fix.
func (r *RuntimeEnvironment) ReplaceInputs(inputs RuntimeInputs, now time.Time) error {
	if r.State != RuntimeAbsent {
		return &InvariantError{
			Entity: "runtime",
			ID:     r.Identity,
			Rule:   "a runtime's recorded inputs are the ones its resources were created from",
			Reason: "the runtime is " + string(r.State) + ", so its inputs can only change once it is absent",
		}
	}
	r.apply(inputs)
	r.changed(now)
	return nil
}

// changed records that the runtime was written to. Every method that alters the
// record ends with it, so no change leaves the generation behind and a stale
// answer looking current.
func (r *RuntimeEnvironment) changed(now time.Time) {
	r.Generation++
	r.ObservedAt = normalizeTime(now)
}

// apply copies the inputs onto the runtime, leaving every observation alone.
func (r *RuntimeEnvironment) apply(inputs RuntimeInputs) {
	r.Provider = inputs.Provider
	r.Identity = inputs.Identity
	r.Composition = inputs.Composition
	r.GeneratedIncludePath = inputs.GeneratedIncludePath
	r.StaticOverrides = inputs.StaticOverrides
	r.GeneratedOverridePath = inputs.GeneratedOverridePath
	r.EnvFiles = inputs.EnvFiles
	r.Services = inputs.Services
	r.Allocations = inputs.Allocations
}

// ReleasePorts gives up the host ports this runtime held. It is refused while
// anything exists, because a port released under a live container is one a
// second task would be given and could not bind. It reports whether anything
// was released, so a caller saves only when there is something to save.
func (r *RuntimeEnvironment) ReleasePorts(now time.Time) bool {
	if r.State != RuntimeAbsent || len(r.Allocations) == 0 {
		return false
	}
	r.Allocations = nil
	r.changed(now)
	return true
}

// Allocation returns the first host port reserved for one service, which is the
// address that service is reached at. A service publishing several ports has one
// allocation per port, and the first is the one its generated variables name.
func (r *RuntimeEnvironment) Allocation(service string) (PortAllocation, bool) {
	for _, allocation := range r.Allocations {
		if allocation.Service == service {
			return allocation, true
		}
	}
	return PortAllocation{}, false
}

// ResolveProvenance records where each managed service's code comes from.
//
// It stays out of RuntimeInputs, which freeze while resources exist, because the
// generated override resolves its mounts and build contexts from current
// configuration each time it is written. A frozen provenance would contradict
// the file Feat had just generated. It reports whether anything changed, so a
// caller saves only when there is something to save.
func (r *RuntimeEnvironment) ResolveProvenance(provenance []ServiceProvenance, now time.Time) bool {
	if sameProvenance(r.Provenance, provenance) {
		return false
	}
	r.Provenance = provenance
	r.changed(now)
	return true
}

// ServiceProvenance returns what is recorded about one managed service.
func (r *RuntimeEnvironment) ServiceProvenance(service string) (ServiceProvenance, bool) {
	for _, entry := range r.Provenance {
		if entry.Service == service {
			return entry, true
		}
	}
	return ServiceProvenance{}, false
}

// sameProvenance reports whether two resolutions say the same thing.
func sameProvenance(a, b []ServiceProvenance) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Service != b[i].Service ||
			!sameStrings(a[i].Repositories, b[i].Repositories) ||
			!sameStrings(a[i].Mounted, b[i].Mounted) ||
			!sameStrings(a[i].Built, b[i].Built) {
			return false
		}
	}
	return true
}

// sameStrings reports whether two lists hold the same values in the same order.
func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Observe records the runtime state and health a runtime adapter reported. A
// stopped runtime found during recovery is reported as stopped and is never
// restarted for the user (FR-STATE-004).
func (r *RuntimeEnvironment) Observe(state RuntimeState, health HealthState, now time.Time) error {
	if !state.Valid() {
		return &ValidationError{
			Entity: "runtime",
			ID:     r.Identity,
			Field:  "state",
			Reason: "must be a documented runtime state, but is " + quote(string(state)),
		}
	}
	if !health.Valid() {
		return &ValidationError{
			Entity: "runtime",
			ID:     r.Identity,
			Field:  "health",
			Reason: "must be a documented health state, but is " + quote(string(health)),
		}
	}
	r.State = state
	r.Health = health
	r.changed(now)
	return nil
}

// ObserveResources records the ports, networks, and volumes an adapter saw. The
// state says whether the application is up; these say what exists because of it,
// which is what a user reaches it by and what cleanup explains it would retain
// (FR-CLEAN-001, FR-CLEAN-004).
func (r *RuntimeEnvironment) ObserveResources(ports []PortAssignment, networks, volumes []string, now time.Time) {
	r.Ports = ports
	r.Networks = networks
	r.Volumes = volumes
	r.changed(now)
}
