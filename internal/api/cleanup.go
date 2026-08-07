package api

import (
	"time"

	"github.com/ma8el/feat/internal/domain"
)

// CleanupPlan is the response of POST /v1/tasks/{task_id}/cleanup/plan.
//
// It is an inventory and not an instruction. Producing it removes nothing, and
// the token is what a later execution carries back so that what is removed is
// what the user read — the shape ADR-031 used for a launch fingerprint, applied
// to the other direction (FR-CLEAN-001).
type CleanupPlan struct {
	// TaskID is the task the plan belongs to.
	TaskID string `json:"task_id"`
	// TaskKey is the short key the task is shown by.
	TaskKey string `json:"task_key"`
	// ProjectID owns the task.
	ProjectID string `json:"project_id"`
	// Workflow is the task's state, so a screen can say what is being cleaned
	// up: a task still working on something is a different decision from an
	// approved one.
	Workflow string `json:"workflow"`
	// Token names exactly the resources below. An execution that carries a
	// different one is refused rather than performed.
	Token string `json:"token"`
	// Classes are the independent choices, in the order they would be removed.
	Classes []CleanupClass `json:"classes"`
	// Problems are recorded resources the plan refuses to name as targets, with
	// the reason. They are not removable and are shown so that a user is not
	// left wondering why something is missing from the list.
	Problems []string `json:"problems,omitempty"`
	// Archivable reports whether the task could be archived by removing
	// everything the plan names. It is false when a class the plan lists cannot
	// currently be resolved.
	Archivable bool `json:"archivable"`
	// ResolvedAt is when the inventory was taken. Everything in it is an
	// observation of that moment.
	ResolvedAt time.Time `json:"resolved_at"`
}

// CleanupClass is one independent choice.
type CleanupClass struct {
	// Class is the class identifier a selection names.
	Class string `json:"class"`
	// Title is the class rendered for a person.
	Title string `json:"title"`
	// Targets are the resources this choice would remove.
	Targets []CleanupTarget `json:"targets"`
	// Warnings are every distinct reason this class needs explicit
	// confirmation. A selection echoes them back, which is what makes the
	// confirmation a statement about what the user was actually shown
	// (FR-CLEAN-003).
	Warnings []string `json:"warnings,omitempty"`
}

// CleanupTarget is one resource.
type CleanupTarget struct {
	// Identity is the resource: a path, a branch, a Compose project, a volume,
	// or a tmux window identifier.
	Identity string `json:"identity"`
	// Repository is the repository a worktree or branch belongs to, empty
	// otherwise.
	Repository string `json:"repository,omitempty"`
	// Detail describes it for a person.
	Detail string `json:"detail"`
	// Present reports whether it is there now.
	Present bool `json:"present"`
	// Warnings are what removing this one would cost.
	Warnings []string `json:"warnings,omitempty"`
}

// CleanupSelection is the body of POST /v1/tasks/{task_id}/cleanup/execute.
//
// It carries identifiers and confirmations, never a path: a destructive request
// names resources the daemon resolved, for the reason every other destructive
// request does (docs/05-security-model.md, local daemon API).
type CleanupSelection struct {
	// Token is the token of the plan the user was shown.
	Token string `json:"token"`
	// Classes are the classes chosen, each echoing the warnings that were
	// displayed for it.
	Classes []CleanupChoice `json:"classes"`
	// Archive asks for the task's metadata to be archived once the selected
	// resources are gone. It is refused while the plan still names resources
	// the selection leaves behind.
	Archive bool `json:"archive"`
}

// CleanupChoice is one class a user selected.
type CleanupChoice struct {
	// Class is the class identifier.
	Class string `json:"class"`
	// ConfirmedWarnings are the exact warnings the user accepted.
	ConfirmedWarnings []string `json:"confirmed_warnings,omitempty"`
}

// CleanupStatus is the response of an executed cleanup.
type CleanupStatus struct {
	// Task is the task as it is now recorded.
	Task Task `json:"task"`
	// Removed is one entry per resource the cleanup addressed, saying whether it
	// went. A resource that was already gone reports false and is not a failure:
	// the user asked for it to be absent, and it is.
	Removed []CleanupRemoval `json:"removed"`
	// Archived reports whether the task's metadata was archived.
	Archived bool `json:"archived"`
}

// CleanupRemoval is one resource a cleanup addressed.
type CleanupRemoval struct {
	// Class is which choice removed it.
	Class string `json:"class"`
	// Identity is the resource.
	Identity string `json:"identity"`
	// Removed reports whether there was something to remove.
	Removed bool `json:"removed"`
}

// CleanupResult is what the daemon reports after a cleanup.
type CleanupResult struct {
	// Task is the task as it is now recorded.
	Task *domain.Task
	// Removed is what was addressed.
	Removed []CleanupRemoval
	// Archived reports whether the metadata was archived.
	Archived bool
}

// NewCleanupStatus renders an executed cleanup for the wire.
func NewCleanupStatus(result CleanupResult) CleanupStatus {
	status := CleanupStatus{
		Task:     newTask(result.Task, nil),
		Removed:  result.Removed,
		Archived: result.Archived,
	}
	if status.Removed == nil {
		status.Removed = []CleanupRemoval{}
	}
	return status
}
