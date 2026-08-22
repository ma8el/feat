package domain

import (
	"net"
	"strconv"
	"strings"
	"time"
)

// RuntimeEnvironment is the application environment associated with one task.
//
// It is a separate concept from the agent execution environment even when both
// use the same Compose project: the agent's environment is how the agent runs,
// and this is the application the user tests. Keeping them apart is what lets
// Feat manage Docker on the host without ever giving the agent Docker access.
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
	// Generation counts how many times this record has been changed.
	//
	// It is what tells this record apart from one that was destroyed and
	// re-created while a question about it was in flight. Everything else about
	// the two can match: the identity is derived from the task, a re-created
	// runtime is running again, and the allocator hands back the lowest free
	// port — which is the number the destroy released. An answer taken before
	// that pair and written down after it records absent and gives the new
	// containers' ports away, which is the defect ADR-065 evidence 16 records,
	// and no comparison of the record's shape can see it.
	//
	// It counts rather than timestamps because a timestamp is not fine enough to
	// be a guard: the daemon reads its clock once per operation, so two
	// operations can share a reading, and a test that holds the clock still
	// would make every record look unchanged.
	//
	// It starts at one, because a record that exists has been written once.
	Generation uint64
}

// ServiceProvenance is where one managed service's code comes from.
//
// It is a state rather than a note because every way of getting this wrong is
// silent: the containers start, the application serves, and every record Feat
// keeps stays correct while the user looks at a healthy runtime that is not
// running their task (ADR-065 evidence 7). A service the task cannot reach is
// something to know before a start rather than after one, so this is resolved
// from configuration and from the project's own Compose files rather than
// inspected out of the containers.
//
// The two routes are separate because they behave differently. A worktree the
// service mounts shows a change as soon as it is written; code the service baked
// into its image shows one when the image is built again, and an agent confined
// to a devcontainer has no Docker and can build nothing (ADR-065 evidence 9).
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
// alone, so that a change appears there only once the image is built again.
//
// A repository that is also mounted is not one of them: the mount is what the
// service reads, and it is current the moment the file is written.
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
}

// PortAllocation is one host port Feat reserved for one service of one task.
//
// It is an intention rather than an observation, which is what separates it
// from PortAssignment: this is the port the generated override tells Compose to
// publish, and that is the port `docker compose ps` reported afterwards. Both
// are kept because they can disagree — a container that never started publishes
// nothing — and a record that conflated them could not say which.
//
// A host port is global to the machine, so an allocation is held against every
// other task for as long as the runtime exists and released when it becomes
// absent. That is the whole of what makes several tasks able to run the same
// application at once (ADR-065 evidence 8).
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
	// HostIP is the address the project publishes on, empty when it names none.
	// It is carried so that a project which deliberately publishes on the
	// loopback address keeps doing so.
	HostIP string
}

// Address is where the service is reached from this machine.
//
// A service published on every address is reached at localhost, which is the
// name a browser and a shell both take. A service the project published on one
// address is reached there and nowhere else, so that address is what is said.
func (p PortAllocation) Address() string {
	return net.JoinHostPort(p.host(), strconv.Itoa(p.HostPort))
}

// URL is the address as a client would open it, and whether there is one.
//
// Only a stream port has one: a URL for a datagram port would be a sentence
// nothing could use, and the port on its own is still worth telling a service.
// The scheme is http because Feat cannot know what a service speaks and http is
// what a development service speaks; a project that terminates TLS itself
// composes its own address from the port.
func (p PortAllocation) URL() (string, bool) {
	if p.Protocol != "tcp" {
		return "", false
	}
	return "http://" + p.Address(), true
}

// host names the machine address a publication is reached at.
func (p PortAllocation) host() string {
	trimmed := strings.TrimSuffix(strings.TrimPrefix(p.HostIP, "["), "]")
	switch trimmed {
	case "", "0.0.0.0", "::":
		return "localhost"
	default:
		return trimmed
	}
}

// PortVariable is the generated variable naming one service's allocated host
// port, and URLVariable the address it makes.
//
// The naming rule lives in the domain because two packages depend on it being
// one rule: the daemon generates these variables, and configuration refuses a
// project where two service names would produce the same one before it can
// generate anything. A collision would be one service silently receiving
// another's address.
func PortVariable(service string) string { return portVariablePrefix + variableToken(service) }

// URLVariable names the address of one service's allocated host port.
func URLVariable(service string) string { return urlVariablePrefix + variableToken(service) }

// The prefixes of the generated addressing variables.
const (
	portVariablePrefix = "FEAT_PORT_"
	urlVariablePrefix  = "FEAT_URL_"
)

// variableToken renders a service name as part of an environment variable name:
// upper case, with everything that is not a letter or a digit replaced.
//
// Compose service names allow dots and hyphens, which an environment variable
// name does not, so the rendering is lossy by construction — "web-app" and
// "web.app" are one name here. Configuration refuses a project where two
// services collide rather than letting one of them receive the other's port.
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

// RuntimeInputs are the exact values a runtime was created from.
//
// They are recorded rather than recomputed because an action taken later must
// reach the resources the task already owns, and the project's configuration may
// have been edited since (docs/07-configuration-model.md).
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
	// input rather than an observation because they are held: while resources
	// exist the recorded ones are what the generated override publishes, so a
	// second task cannot be given a port the first is still using.
	Allocations []PortAllocation
}

// RuntimeSource is one repository's contribution to a task's application.
//
// A runtime is composed of its repositories rather than of a flat list of
// files, because which repository a file came from is what decides the
// directory its relative paths resolve against (ADR-065).
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
// exists for it.
//
// It starts absent with unknown health, which is what a runtime nothing has
// created is: state and health are observations, and nothing has observed
// anything yet.
func NewRuntimeEnvironment(inputs RuntimeInputs) *RuntimeEnvironment {
	runtime := &RuntimeEnvironment{State: RuntimeAbsent, Health: HealthUnknown, Generation: 1}
	runtime.apply(inputs)
	return runtime
}

// ReplaceInputs re-resolves the runtime from current configuration.
//
// It is refused unless the runtime is absent — never created, or destroyed
// since. While resources exist, the recorded inputs are what an action must act
// on: a user who edits their Compose files with services running must not have
// the next stop reach a different Compose project, and a user who fixed them
// after destroying everything should get the fixed ones.
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

// changed records that the runtime was written to.
//
// Every method that alters the record ends with it, so that a change nothing
// else can see still moves the counter which says the record has moved: the two
// halves are one act, and a mutator that touched only the moment would leave a
// stale answer looking current.
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

// ReleasePorts gives up the host ports this runtime held.
//
// It is refused while anything exists, for the reason ReplaceInputs is: a port
// released while a container is still bound to it is a port a second task would
// be given and could not bind. An absent runtime holds nothing, so its ports
// belong to whichever task asks next.
//
// It reports whether anything was released, so a caller saves and says so only
// when there is something to say.
func (r *RuntimeEnvironment) ReleasePorts(now time.Time) bool {
	if r.State != RuntimeAbsent || len(r.Allocations) == 0 {
		return false
	}
	r.Allocations = nil
	r.changed(now)
	return true
}

// Allocation returns the first host port reserved for one service, which is the
// address that service is reached at.
//
// The first rather than the only: a service publishing several ports has one
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
// It is not part of RuntimeInputs, which are frozen while resources exist,
// because the mounts and build contexts of the generated override are resolved
// from current configuration every time that document is written. A frozen
// provenance beside a recomputed document would be a record contradicting the
// file Feat had just generated, which is the class of quiet disagreement this
// state exists to remove.
//
// It reports whether anything changed, so that a caller saves and says so only
// when there is something to say.
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

// Observe records the runtime state and health a runtime adapter reported.
//
// Runtime state is an observation. A stopped runtime found during recovery is
// reported as stopped and is never restarted for the user (FR-STATE-004).
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

// ObserveResources records the ports, networks, and volumes an adapter saw.
//
// They are separate from Observe because they answer a different question. The
// state says whether the application is up; these say what exists because of it,
// which is what a user needs in order to reach the application and what cleanup
// needs in order to explain what it would retain (FR-CLEAN-001, FR-CLEAN-004).
func (r *RuntimeEnvironment) ObserveResources(ports []PortAssignment, networks, volumes []string, now time.Time) {
	r.Ports = ports
	r.Networks = networks
	r.Volumes = volumes
	r.changed(now)
}
