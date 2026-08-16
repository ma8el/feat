package domain

import "time"

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
	runtime := &RuntimeEnvironment{State: RuntimeAbsent, Health: HealthUnknown}
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
	r.ObservedAt = normalizeTime(now)
	return nil
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
	r.ObservedAt = normalizeTime(now)
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
	r.ObservedAt = normalizeTime(now)
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
	r.ObservedAt = normalizeTime(now)
}
