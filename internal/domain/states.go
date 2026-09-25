package domain

// The four state dimensions in docs/03-domain-model.md stay separate, and they
// differ in how this package validates them. Feat decides workflow state, so a
// transition table checks every change. Process, attention, and runtime states
// are observations of a world Feat does not control, so only their values are
// checked. Rejecting an observation for arriving out of order would record what
// Feat expected rather than what it saw.

// WorkflowState is the product-level state of a task.
type WorkflowState string

// Workflow states from docs/03-domain-model.md.
const (
	// WorkflowDraft is a task being prepared. Nothing has been created for it.
	WorkflowDraft WorkflowState = "draft"
	// WorkflowPreparing is a confirmed task whose resources are being created.
	WorkflowPreparing WorkflowState = "preparing"
	// WorkflowWorking is a task with a running agent session.
	WorkflowWorking WorkflowState = "working"
	// WorkflowReviewRequested is a task whose agent explicitly asked for
	// review. An idle or end-of-turn signal never produces this state.
	WorkflowReviewRequested WorkflowState = "review_requested"
	// WorkflowVerifying is a task whose configured checks are running.
	WorkflowVerifying WorkflowState = "verifying"
	// WorkflowReadyForReview is a task whose checks passed.
	WorkflowReadyForReview WorkflowState = "ready_for_review"
	// WorkflowVerificationFailed is a task whose checks failed.
	WorkflowVerificationFailed WorkflowState = "verification_failed"
	// WorkflowArchived is a task whose metadata was archived by cleanup.
	WorkflowArchived WorkflowState = "archived"
	// WorkflowFailed is a task whose lifecycle failed part way through.
	WorkflowFailed WorkflowState = "failed"
)

// workflowTransitions lists the states reachable from each workflow state.
//
// Two rows carry a constraint the table itself does not show. Working has no
// edge to ready_for_review, because semantic completion needs the agent's own
// review request (FR-AGENT-008), and such an edge is the shape a
// Stop-means-complete bug would take. Review_requested reaches ready_for_review
// directly for a project with no configured checks, which would otherwise leave
// the task waiting on a gate that has nothing to run
// (docs/02-user-workflows.md §6).
//
// The remaining edges are recorded elsewhere. Draft reaches preparing through
// the confirmation step in FR-TASK-003. The gate and its interrupted-run return
// are in ADR-036, the re-run of checks on work that passed in ADR-087, and the
// removal of approved and changes_requested in ADR-086, which rewrote
// FR-REV-004. Revision returns a task to working (FR-AGENT-009), and failed
// returns to preparing or working so a broken lifecycle can be resumed rather
// than recreated.
//
// Archived is reachable from every other state and has no outgoing edge,
// because cleanup archives task metadata whenever the user asks for it
// (docs/02-user-workflows.md §11). CanTransitionTo handles it rather than every
// row repeating it.
var workflowTransitions = map[WorkflowState][]WorkflowState{
	WorkflowDraft:              {WorkflowPreparing},
	WorkflowPreparing:          {WorkflowWorking, WorkflowFailed},
	WorkflowWorking:            {WorkflowReviewRequested, WorkflowFailed},
	WorkflowReviewRequested:    {WorkflowVerifying, WorkflowReadyForReview, WorkflowWorking, WorkflowFailed},
	WorkflowVerifying:          {WorkflowReadyForReview, WorkflowVerificationFailed, WorkflowReviewRequested, WorkflowFailed},
	WorkflowReadyForReview:     {WorkflowWorking, WorkflowReviewRequested, WorkflowFailed},
	WorkflowVerificationFailed: {WorkflowWorking, WorkflowReviewRequested, WorkflowFailed},
	WorkflowFailed:             {WorkflowPreparing, WorkflowWorking},
	WorkflowArchived:           {},
}

// Valid reports whether the state is one of the documented workflow states.
func (s WorkflowState) Valid() bool {
	_, ok := workflowTransitions[s]
	return ok
}

// Terminal reports whether the state has no outgoing transition.
func (s WorkflowState) Terminal() bool { return s == WorkflowArchived }

// CanTransitionTo reports whether next is reachable from s.
func (s WorkflowState) CanTransitionTo(next WorkflowState) bool {
	if !s.Valid() || !next.Valid() {
		return false
	}
	if next == WorkflowArchived {
		return s != WorkflowArchived
	}
	for _, candidate := range workflowTransitions[s] {
		if candidate == next {
			return true
		}
	}
	return false
}

// Reachable lists the states reachable from s, in a stable order.
func (s WorkflowState) Reachable() []WorkflowState {
	if !s.Valid() || s == WorkflowArchived {
		return nil
	}
	reachable := make([]WorkflowState, 0, len(workflowTransitions[s])+1)
	reachable = append(reachable, workflowTransitions[s]...)
	return append(reachable, WorkflowArchived)
}

// requiresSession reports whether a task in this state must already own an
// agent session. Preparing may precede the session, and a failed or archived
// task may never have reached one.
func (s WorkflowState) requiresSession() bool {
	switch s {
	case WorkflowWorking, WorkflowReviewRequested, WorkflowVerifying,
		WorkflowReadyForReview, WorkflowVerificationFailed:
		return true
	default:
		return false
	}
}

// ProcessState is the observed state of an agent process.
type ProcessState string

// Process states from docs/03-domain-model.md.
const (
	// ProcessStarting is a session whose process is being launched.
	ProcessStarting ProcessState = "starting"
	// ProcessRunning is a session actively producing output.
	ProcessRunning ProcessState = "running"
	// ProcessIdle is a session that finished a turn. Idle is a process
	// observation and never means the task is complete.
	ProcessIdle ProcessState = "idle"
	// ProcessStopped is a session whose process exited.
	ProcessStopped ProcessState = "stopped"
	// ProcessFailed is a session whose process exited abnormally.
	ProcessFailed ProcessState = "failed"
)

// Valid reports whether the state is one of the documented process states.
func (s ProcessState) Valid() bool {
	switch s {
	case ProcessStarting, ProcessRunning, ProcessIdle, ProcessStopped, ProcessFailed:
		return true
	default:
		return false
	}
}

// Alive reports whether the state describes a process that has not ended.
//
// Idle is alive: a session that finished a turn is still sitting in its
// terminal.
func (s ProcessState) Alive() bool {
	switch s {
	case ProcessStarting, ProcessRunning, ProcessIdle:
		return true
	default:
		return false
	}
}

// AttentionState records whether the user may need to intervene.
type AttentionState string

// Attention states from docs/03-domain-model.md.
const (
	// AttentionNone is a task that does not need the user.
	AttentionNone AttentionState = "none"
	// AttentionPossiblyWaiting is the conservative state for a provider that
	// cannot distinguish a finished turn from a question.
	AttentionPossiblyWaiting AttentionState = "possibly_waiting"
	// AttentionNeedsInput is a task the provider reported as blocked on the
	// user.
	AttentionNeedsInput AttentionState = "needs_input"
)

// Valid reports whether the state is one of the documented attention states.
func (s AttentionState) Valid() bool {
	switch s {
	case AttentionNone, AttentionPossiblyWaiting, AttentionNeedsInput:
		return true
	default:
		return false
	}
}

// RuntimeState is the observed lifecycle state of an application runtime.
type RuntimeState string

// Runtime states from docs/03-domain-model.md.
const (
	// RuntimeAbsent is a task with no runtime resources.
	RuntimeAbsent RuntimeState = "absent"
	// RuntimeCreating is a runtime whose resources are being created.
	RuntimeCreating RuntimeState = "creating"
	// RuntimeStopped is a created runtime that is not running. Feat never
	// restarts it automatically after recovery.
	RuntimeStopped RuntimeState = "stopped"
	// RuntimeStarting is a runtime whose services are starting.
	RuntimeStarting RuntimeState = "starting"
	// RuntimeRunning is a runtime whose services are up.
	RuntimeRunning RuntimeState = "running"
	// RuntimeDegraded is a runtime that is up but unhealthy in part.
	RuntimeDegraded RuntimeState = "degraded"
	// RuntimeFailed is a runtime whose services failed.
	RuntimeFailed RuntimeState = "failed"
	// RuntimeRemoving is a runtime whose resources are being removed.
	RuntimeRemoving RuntimeState = "removing"
)

// Valid reports whether the state is one of the documented runtime states.
func (s RuntimeState) Valid() bool {
	switch s {
	case RuntimeAbsent, RuntimeCreating, RuntimeStopped, RuntimeStarting,
		RuntimeRunning, RuntimeDegraded, RuntimeFailed, RuntimeRemoving:
		return true
	default:
		return false
	}
}

// HealthState is the health of a runtime's services, which is separate from
// whether its containers are running. A runtime with no configured health check
// reports unknown.
type HealthState string

// Health states.
const (
	// HealthUnknown is the correct answer when no health check is configured.
	HealthUnknown HealthState = "unknown"
	// HealthStarting is a service whose health check has not settled.
	HealthStarting HealthState = "starting"
	// HealthHealthy is a service whose health check passes.
	HealthHealthy HealthState = "healthy"
	// HealthUnhealthy is a service whose health check fails.
	HealthUnhealthy HealthState = "unhealthy"
)

// Valid reports whether the state is one of the documented health states.
func (s HealthState) Valid() bool {
	switch s {
	case HealthUnknown, HealthStarting, HealthHealthy, HealthUnhealthy:
		return true
	default:
		return false
	}
}

// DefaultAccess is a repository's default participation in a task, as declared
// in project configuration (docs/07-configuration-model.md).
type DefaultAccess string

// Default access modes.
const (
	// DefaultAccessReadWrite selects the repository by default and gives it a
	// task branch and worktree.
	DefaultAccessReadWrite DefaultAccess = "read_write"
	// DefaultAccessReadOnly selects the repository by default, gives it a task
	// worktree, and mounts it read-only.
	DefaultAccessReadOnly DefaultAccess = "read_only"
	// DefaultAccessSelectable requires the user to choose omitted, read-only,
	// or read-write during task preparation.
	DefaultAccessSelectable DefaultAccess = "selectable"
	// DefaultAccessStableReadOnly uses the ordinary checkout read-only unless
	// the task explicitly promotes it to a task repository.
	DefaultAccessStableReadOnly DefaultAccess = "stable_read_only"
	// DefaultAccessOmitted leaves the repository out by default.
	DefaultAccessOmitted DefaultAccess = "omitted"
)

// Valid reports whether the mode is one of the documented default access modes.
func (a DefaultAccess) Valid() bool {
	switch a {
	case DefaultAccessReadWrite, DefaultAccessReadOnly, DefaultAccessSelectable,
		DefaultAccessStableReadOnly, DefaultAccessOmitted:
		return true
	default:
		return false
	}
}

// CanBeReadWrite reports whether a task may edit the repository. It answers the
// configuration rule that a project's primary repository must be editable.
func (a DefaultAccess) CanBeReadWrite() bool {
	return a == DefaultAccessReadWrite || a == DefaultAccessSelectable
}

// Permits reports whether a task may bind the repository with the given access.
//
// Read-only is always available, because a task may take less access than the
// default. Read-write is not: a repository a project declared read-only must not
// become writable because one task asked. The modes that leave the repository
// out of a task by default say nothing about writing, so they permit either once
// the user has explicitly selected the repository.
func (a DefaultAccess) Permits(access TaskAccess) bool {
	if !a.Valid() || !access.Valid() {
		return false
	}
	if access == TaskAccessReadOnly {
		return true
	}
	return a != DefaultAccessReadOnly
}

// TaskAccess is the access one task has to one repository. A binding is either
// editable or not; the richer configuration modes are resolved into this during
// task preparation.
type TaskAccess string

// Task access modes.
const (
	// TaskAccessReadWrite gives the task its own branch and a writable
	// worktree.
	TaskAccessReadWrite TaskAccess = "read_write"
	// TaskAccessReadOnly gives the task a worktree that is mounted read-only.
	TaskAccessReadOnly TaskAccess = "read_only"
)

// Valid reports whether the mode is one of the documented task access modes.
func (a TaskAccess) Valid() bool {
	return a == TaskAccessReadWrite || a == TaskAccessReadOnly
}

// ExecutionMode is where an agent session runs.
type ExecutionMode string

// Execution modes.
const (
	// ExecutionHost runs the agent directly in the primary task worktree, with
	// no container boundary.
	ExecutionHost ExecutionMode = "host"
	// ExecutionDevcontainer runs the agent as a non-root user inside a
	// configured Compose service.
	ExecutionDevcontainer ExecutionMode = "devcontainer"
)

// Valid reports whether the mode is one of the documented execution modes.
func (m ExecutionMode) Valid() bool {
	return m == ExecutionHost || m == ExecutionDevcontainer
}
