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
	// ComposeFiles are the configured base files, in order.
	ComposeFiles []string
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
	// ExternalResources are resources the runtime uses but does not own, such
	// as a pre-existing staging database. Feat never provisions or destroys
	// them.
	ExternalResources []ExternalResource
	// ObservedAt is when the state, health, and resource lists were last
	// observed.
	ObservedAt time.Time
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

// ResourceLifecycle records who owns a runtime resource.
type ResourceLifecycle string

// Resource lifecycles from docs/07-configuration-model.md. A shared lifecycle,
// with explicit isolation semantics, is roadmap work.
const (
	// LifecycleManaged is a resource Feat creates, observes, and may remove.
	LifecycleManaged ResourceLifecycle = "managed"
	// LifecycleExternal is a resource Feat references but never provisions or
	// destroys.
	LifecycleExternal ResourceLifecycle = "external"
)

// Valid reports whether the lifecycle is documented.
func (l ResourceLifecycle) Valid() bool {
	return l == LifecycleManaged || l == LifecycleExternal
}

// ExternalResource is a shared development resource a task's runtime uses.
type ExternalResource struct {
	// ID identifies the binding within the task.
	ID string
	// Kind describes the resource, such as the engine of a shared development
	// database.
	Kind string
	// Lifecycle records ownership. It is always external here, and is recorded
	// explicitly so a cleanup plan can prove it excluded the resource.
	Lifecycle ResourceLifecycle
	// Selector is the non-secret generated value that tells the application
	// which shared resource this task uses.
	Selector string
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
	for _, resource := range r.ExternalResources {
		if resource.ID == "" {
			return &ValidationError{
				Entity: "runtime",
				ID:     id,
				Field:  "external_resources",
				Reason: "must give every resource an identifier",
			}
		}
		if resource.Lifecycle != LifecycleExternal {
			return &InvariantError{
				Entity: "runtime",
				ID:     id,
				Rule:   "external resources are referenced, never owned",
				Reason: "resource " + resource.ID + " is recorded as " + quote(string(resource.Lifecycle)),
			}
		}
	}
	return nil
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
