package domain

import (
	"slices"
	"testing"
)

// TestWorkflowTransitionsMatchTheLifecycle pins the transition table against the
// lifecycle in docs/03-domain-model.md and docs/02-user-workflows.md.
//
// The expectation is written out rather than derived from the table, so that
// widening the lifecycle is a deliberate edit in two places instead of a
// side effect of one.
func TestWorkflowTransitionsMatchTheLifecycle(t *testing.T) {
	want := map[WorkflowState][]WorkflowState{
		WorkflowDraft:              {WorkflowPreparing, WorkflowArchived},
		WorkflowPreparing:          {WorkflowWorking, WorkflowFailed, WorkflowArchived},
		WorkflowWorking:            {WorkflowReviewRequested, WorkflowFailed, WorkflowArchived},
		WorkflowReviewRequested:    {WorkflowVerifying, WorkflowReadyForReview, WorkflowChangesRequested, WorkflowApproved, WorkflowWorking, WorkflowFailed, WorkflowArchived},
		WorkflowVerifying:          {WorkflowReadyForReview, WorkflowVerificationFailed, WorkflowReviewRequested, WorkflowFailed, WorkflowArchived},
		WorkflowReadyForReview:     {WorkflowApproved, WorkflowChangesRequested, WorkflowWorking, WorkflowFailed, WorkflowArchived},
		WorkflowVerificationFailed: {WorkflowWorking, WorkflowReviewRequested, WorkflowChangesRequested, WorkflowFailed, WorkflowArchived},
		WorkflowChangesRequested:   {WorkflowWorking, WorkflowFailed, WorkflowArchived},
		WorkflowApproved:           {WorkflowArchived},
		WorkflowFailed:             {WorkflowPreparing, WorkflowWorking, WorkflowArchived},
		WorkflowArchived:           nil,
	}

	for state, reachable := range want {
		if got := state.Reachable(); !slices.Equal(got, reachable) {
			t.Errorf("%s reaches %v, want %v", state, got, reachable)
		}
		for _, target := range allWorkflowStates() {
			wantReachable := slices.Contains(reachable, target)
			if got := state.CanTransitionTo(target); got != wantReachable {
				t.Errorf("%s -> %s: got %t, want %t", state, target, got, wantReachable)
			}
		}
	}

	if len(want) != len(workflowTransitions) {
		t.Errorf("the table has %d states and the expectation has %d; docs/03-domain-model.md lists the workflow states",
			len(workflowTransitions), len(want))
	}
}

// TestIdleIsNotCompletion checks invariant 13 as a property of the lifecycle: no
// state reaches ready_for_review or approved without passing through an explicit
// review request.
//
// A Stop or end-of-turn signal is an observation of a process. The shape a
// "Stop means done" defect would take is exactly an edge into a review state
// from somewhere the agent never asked for review.
func TestIdleIsNotCompletion(t *testing.T) {
	allowed := map[WorkflowState]bool{
		WorkflowReviewRequested: true,
		WorkflowVerifying:       true,
		WorkflowReadyForReview:  true,
	}

	for _, state := range allWorkflowStates() {
		if allowed[state] {
			continue
		}
		for _, completion := range []WorkflowState{WorkflowReadyForReview, WorkflowApproved} {
			if state.CanTransitionTo(completion) {
				t.Errorf("%s reaches %s without an explicit review request (FR-AGENT-008)", state, completion)
			}
		}
	}
}

// TestArchivedIsTheOnlyTerminalState checks that cleanup can archive a task
// whatever state it is in, and that nothing leaves an archived task.
func TestArchivedIsTheOnlyTerminalState(t *testing.T) {
	for _, state := range allWorkflowStates() {
		if state == WorkflowArchived {
			continue
		}
		if !state.CanTransitionTo(WorkflowArchived) {
			t.Errorf("%s cannot be archived, but cleanup archives task metadata from any state", state)
		}
		if state.Terminal() {
			t.Errorf("%s reports itself terminal, but it still has outgoing transitions", state)
		}
	}

	if !WorkflowArchived.Terminal() {
		t.Error("archived is not terminal")
	}
	if got := WorkflowArchived.Reachable(); len(got) != 0 {
		t.Errorf("archived reaches %v", got)
	}
}

// TestUnknownStatesAreRejected checks that a value from outside the documented
// set is never treated as a state.
func TestUnknownStatesAreRejected(t *testing.T) {
	if WorkflowState("done").Valid() {
		t.Error("an undocumented workflow state validates")
	}
	if WorkflowDraft.CanTransitionTo("done") {
		t.Error("a task can transition to an undocumented workflow state")
	}
	if ProcessState("busy").Valid() {
		t.Error("an undocumented process state validates")
	}
	if AttentionState("waiting").Valid() {
		t.Error("an undocumented attention state validates")
	}
	if RuntimeState("up").Valid() {
		t.Error("an undocumented runtime state validates")
	}
	if HealthState("ok").Valid() {
		t.Error("an undocumented health state validates")
	}
	if TaskAccess(string(DefaultAccessSelectable)).Valid() {
		t.Error("selectable is a configuration default, not an access a task can hold")
	}
}

// TestPrimaryRepositoryAccess checks which configured defaults allow a task to
// edit a repository, which is what the primary-repository rule depends on.
func TestPrimaryRepositoryAccess(t *testing.T) {
	editable := map[DefaultAccess]bool{
		DefaultAccessReadWrite:      true,
		DefaultAccessSelectable:     true,
		DefaultAccessReadOnly:       false,
		DefaultAccessStableReadOnly: false,
		DefaultAccessOmitted:        false,
	}
	for access, want := range editable {
		if !access.Valid() {
			t.Errorf("%s is not a documented default access mode", access)
		}
		if got := access.CanBeReadWrite(); got != want {
			t.Errorf("%s can be read-write: got %t, want %t", access, got, want)
		}
	}
}

// TestTaskAccessCannotExceedTheConfiguredDefault checks the asymmetry a task's
// repository selection rests on.
//
// Taking less access than the project configured is always available to a task.
// Taking more is not: a repository a project declared read-only must not become
// writable because one task asked for it.
func TestTaskAccessCannotExceedTheConfiguredDefault(t *testing.T) {
	writable := map[DefaultAccess]bool{
		DefaultAccessReadWrite:      true,
		DefaultAccessSelectable:     true,
		DefaultAccessStableReadOnly: true,
		DefaultAccessOmitted:        true,
		DefaultAccessReadOnly:       false,
	}
	for access, want := range writable {
		if got := access.Permits(TaskAccessReadWrite); got != want {
			t.Errorf("%s permits a read-write task binding: got %t, want %t", access, got, want)
		}
		if !access.Permits(TaskAccessReadOnly) {
			t.Errorf("%s refuses a read-only task binding, and less access is always available", access)
		}
	}

	if DefaultAccess("invented").Permits(TaskAccessReadOnly) {
		t.Error("an undocumented default access mode permitted a binding")
	}
	if DefaultAccessReadWrite.Permits(TaskAccess("invented")) {
		t.Error("an undocumented task access mode was permitted")
	}
}

func allWorkflowStates() []WorkflowState {
	states := make([]WorkflowState, 0, len(workflowTransitions))
	for state := range workflowTransitions {
		states = append(states, state)
	}
	slices.Sort(states)
	return states
}
