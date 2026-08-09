package domain

// The four state dimensions in docs/03-domain-model.md are deliberately not
// collapsed into one enum. They also differ in kind, which decides how this
// package validates them:
//
//   - workflow state is decided by Feat, so it has a transition table and every
//     change is checked against it;
//   - process, attention, and runtime states are observations of a world Feat
//     does not control, so only their values are checked. Rejecting an
//     observation because it does not follow the expected order would make the
//     stored state a claim about the world rather than a record of it, which is
//     exactly what CLAUDE.md forbids when it says never to assume persisted
//     desired state equals observed state.

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
	// WorkflowChangesRequested is a task the user sent back for revision.
	WorkflowChangesRequested WorkflowState = "changes_requested"
	// WorkflowApproved is a task the user approved. Approval does not stop the
	// runtime and does not remove any resource.
	WorkflowApproved WorkflowState = "approved"
	// WorkflowArchived is a task whose metadata was archived by cleanup.
	WorkflowArchived WorkflowState = "archived"
	// WorkflowFailed is a task whose lifecycle failed part way through.
	WorkflowFailed WorkflowState = "failed"
)

// workflowTransitions lists the states reachable from each workflow state.
//
// Every edge below is required by a documented workflow:
//
//   - draft to preparing is the confirmation step in FR-TASK-003. A draft has
//     no other outgoing edge because nothing exists to fail or to review yet.
//   - preparing to working is a successful launch; preparing to failed is a
//     lifecycle that broke half way through.
//   - working to review_requested is the explicit provider event required by
//     FR-AGENT-008. There is deliberately no edge from working to
//     ready_for_review, because that is the shape a Stop-means-complete bug
//     would take.
//   - review_requested to verifying to ready_for_review or verification_failed
//     is the completion gate in docs/02-user-workflows.md §6. The direct edge
//     from review_requested to ready_for_review covers a project with no
//     configured checks, where there is nothing to verify; without a gate a
//     task otherwise stays in review_requested.
//   - verifying back to review_requested is a gate that did not finish, which a
//     daemon restart produces. Without it the task would rest in verifying for
//     ever, claiming that checks are running when nothing is (ADR-036).
//   - verification_failed to review_requested is an agent that fixed what the
//     gate caught and asked again. Without it a task whose checks failed once
//     could never be reviewed again without a user typing something first.
//   - review_requested and ready_for_review to approved or changes_requested
//     are the review decisions in FR-REV-004; the edges back to working are the
//     conservative revision transition in FR-AGENT-009.
//   - failed to preparing or working are recovery edges, so a lifecycle that
//     broke half way through can be resumed rather than recreated.
//
// Archived is reachable from every other state and has no outgoing edge,
// because cleanup archives task metadata whenever the user asks for it
// (docs/02-user-workflows.md §11). It is handled in CanTransitionTo rather than
// repeated in every row here.
var workflowTransitions = map[WorkflowState][]WorkflowState{
	WorkflowDraft:              {WorkflowPreparing},
	WorkflowPreparing:          {WorkflowWorking, WorkflowFailed},
	WorkflowWorking:            {WorkflowReviewRequested, WorkflowFailed},
	WorkflowReviewRequested:    {WorkflowVerifying, WorkflowReadyForReview, WorkflowChangesRequested, WorkflowApproved, WorkflowWorking, WorkflowFailed},
	WorkflowVerifying:          {WorkflowReadyForReview, WorkflowVerificationFailed, WorkflowReviewRequested, WorkflowFailed},
	WorkflowReadyForReview:     {WorkflowApproved, WorkflowChangesRequested, WorkflowWorking, WorkflowFailed},
	WorkflowVerificationFailed: {WorkflowWorking, WorkflowReviewRequested, WorkflowChangesRequested, WorkflowFailed},
	WorkflowChangesRequested:   {WorkflowWorking, WorkflowFailed},
	WorkflowApproved:           {},
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
		WorkflowReadyForReview, WorkflowVerificationFailed,
		WorkflowChangesRequested, WorkflowApproved:
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
// Idle is alive: a session that finished a turn is a session still sitting in
// its terminal, which is the distinction that keeps idle a process observation
// rather than a claim about the work.
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
// whether its containers are running. Without configured health checks the
// honest answer is unknown.
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
// Read-only is always available: taking less than the default is a choice a task
// may always make. Read-write is not, and the asymmetry is the point — a
// repository a project declared read-only must not become writable because one
// task asked. The modes that say nothing about writing, because they leave the
// repository out of a task by default, permit either once the user has
// explicitly selected the repository.
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
