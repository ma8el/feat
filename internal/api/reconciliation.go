package api

import "time"

// Reconciliation is the response of GET and POST /v1/reconciliation: what Feat
// found when it compared every persisted task with the machine. Nothing was
// repaired, and a finding names what a user can do (FR-STATE-003, FR-STATE-004).
//
// It is separate from health because health describes the daemon and this
// describes the state directory.
type Reconciliation struct {
	// Ran reports whether a pass has been performed. Only a test sees false: in
	// production the pass runs before anything is served.
	Ran bool `json:"ran"`
	// StartedAt and FinishedAt bound the pass.
	StartedAt  time.Time `json:"started_at,omitzero"`
	FinishedAt time.Time `json:"finished_at,omitzero"`
	// Findings are what was looked at, most serious first.
	Findings []ReconciliationFinding `json:"findings"`
	// Problems are the enumerations that failed, so a pass that could not look
	// says so rather than reporting that it found nothing.
	Problems []ReconciliationProblem `json:"problems"`
	// NeedsAttention reports whether anything was not simply present, which
	// decides whether a user is shown a recovery band.
	NeedsAttention bool `json:"needs_attention"`
	// PreviousRunEndedCleanly reports whether the last daemon to own this state
	// directory stopped rather than died.
	PreviousRunEndedCleanly bool `json:"previous_run_ended_cleanly"`
	// PreviousRunStoppedAt is when it last stopped, absent when unknown.
	PreviousRunStoppedAt time.Time `json:"previous_run_stopped_at,omitzero"`
}

// ReconciliationFinding is one resource a pass looked at.
type ReconciliationFinding struct {
	// Class is the kind of resource.
	Class string `json:"class"`
	// Status is present, missing, orphaned, inconsistent, or damaged.
	Status string `json:"status"`
	// ProjectID and TaskID own the resource, and a task identifier is absent for
	// an orphan that names none.
	ProjectID string `json:"project_id,omitempty"`
	TaskID    string `json:"task_id,omitempty"`
	// TaskKey is the short key the task is shown by.
	TaskKey string `json:"task_key,omitempty"`
	// Identity is the resource itself.
	Identity string `json:"identity,omitempty"`
	// Detail says what was found.
	Detail string `json:"detail"`
	// Action names what the user can do about it, absent when nothing is offered.
	Action string `json:"action,omitempty"`
}

// ReconciliationProblem is an enumeration that failed outright.
type ReconciliationProblem struct {
	// Class is what could not be enumerated, absent when the failure was not
	// scoped to one.
	Class string `json:"class,omitempty"`
	// ProjectID and TaskID narrow the failure when it was scoped to one.
	ProjectID string `json:"project_id,omitempty"`
	TaskID    string `json:"task_id,omitempty"`
	// Reason is why the enumeration failed.
	Reason string `json:"reason"`
}
