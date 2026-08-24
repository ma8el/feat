package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var origin = time.Date(2026, time.August, 4, 9, 30, 0, 0, time.UTC)

const (
	testProject   = ProjectID("example")
	testPrimary   = RepositoryID("core")
	testSecondary = RepositoryID("schema")
	testTask      = TaskID("7f3a1c2e-5b6d-4a80-9c1f-2d3e4f5a6b7c")
	testCommit    = "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d"
	otherCommit   = "9f8e7d6c5b4a39281706f5e4d3c2b1a09f8e7d6c"
)

// TestInvalidTransitionsAreTyped checks that an invalid state transition fails
// with a typed error, and that the error explains where the task can go
// instead.
func TestInvalidTransitionsAreTyped(t *testing.T) {
	task := launchedTask(t)

	err := task.TransitionTo(WorkflowReadyForReview, origin)
	if err == nil {
		t.Fatal("a working task became ready for review without an explicit review request")
	}
	if !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("error does not match ErrInvalidTransition: %v", err)
	}

	var transition *TransitionError
	if !errors.As(err, &transition) {
		t.Fatalf("error is not a *TransitionError: %v", err)
	}
	if transition.From != string(WorkflowWorking) || transition.To != string(WorkflowReadyForReview) {
		t.Errorf("error reports %s -> %s", transition.From, transition.To)
	}
	if transition.Dimension != "workflow" {
		t.Errorf("error reports the %q dimension", transition.Dimension)
	}
	if transition.ID != testTask.String() {
		t.Errorf("error names task %q", transition.ID)
	}
	if !strings.Contains(err.Error(), string(WorkflowReviewRequested)) {
		t.Errorf("error does not name a reachable state: %v", err)
	}
	if task.Workflow != WorkflowWorking {
		t.Errorf("the rejected transition changed the task to %s", task.Workflow)
	}
}

// TestUndocumentedTransitionTargetIsRejected checks that a state outside the
// documented set cannot be reached, even by a caller that made one up.
func TestUndocumentedTransitionTargetIsRejected(t *testing.T) {
	task := launchedTask(t)

	err := task.TransitionTo("done", origin)
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("want ErrInvalidTransition, got %v", err)
	}
	if !strings.Contains(err.Error(), "no such workflow state") {
		t.Errorf("error does not explain that the state does not exist: %v", err)
	}
}

// TestConfirmationRequiresEverythingTheUserAccepted checks the preconditions for
// leaving draft: a task is confirmed with a brief, a repository selection, a
// resolved base for every selected repository, and a branch wherever the agent
// may write.
func TestConfirmationRequiresEverythingTheUserAccepted(t *testing.T) {
	tests := []struct {
		name    string
		shape   func(*testing.T, *Task)
		missing string
	}{
		{
			name:    "without a brief",
			shape:   func(t *testing.T, task *Task) { bindPrimary(t, task); resolvePrimary(t, task) },
			missing: "no brief",
		},
		{
			name:    "without a repository",
			shape:   func(t *testing.T, task *Task) { setBrief(t, task) },
			missing: "selects no repository",
		},
		{
			name:    "without a resolved base",
			shape:   func(t *testing.T, task *Task) { setBrief(t, task); bindPrimary(t, task) },
			missing: "no resolved base commit",
		},
		{
			name: "without a worktree",
			shape: func(t *testing.T, task *Task) {
				setBrief(t, task)
				bind(t, task, TaskRepository{RepositoryID: testPrimary, Access: TaskAccessReadWrite, Branch: "feat/7f3a1c2e-export"})
				resolvePrimary(t, task)
			},
			missing: "no worktree path",
		},
		{
			name: "without a branch on a read-write repository",
			shape: func(t *testing.T, task *Task) {
				setBrief(t, task)
				bind(t, task, TaskRepository{
					RepositoryID: testPrimary,
					Access:       TaskAccessReadWrite,
					WorktreePath: "/srv/state/worktrees/example/7f3a1c2e/core",
				})
				resolvePrimary(t, task)
			},
			missing: "no task branch",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			task := draftTask(t)
			test.shape(t, task)

			err := task.TransitionTo(WorkflowPreparing, origin)
			if err == nil {
				t.Fatalf("a task %s was confirmed", test.name)
			}
			if !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("want ErrInvalidTransition, got %v", err)
			}
			if !strings.Contains(err.Error(), test.missing) {
				t.Errorf("error does not say what is missing (%q): %v", test.missing, err)
			}
			if task.Workflow != WorkflowDraft {
				t.Errorf("the task left draft anyway, to %s", task.Workflow)
			}
		})
	}
}

// TestACancelledDraftCanBeArchived checks that a draft the user abandons before
// confirming it can still be recorded. Nothing was created for it, so it has no
// brief, no base, and no session, and the readiness rules that apply to a live
// task must not apply to it.
func TestACancelledDraftCanBeArchived(t *testing.T) {
	task := draftTask(t)

	if err := task.TransitionTo(WorkflowArchived, origin); err != nil {
		t.Fatalf("archiving a cancelled draft: %v", err)
	}
	if err := task.Validate(); err != nil {
		t.Errorf("the archived draft is invalid: %v", err)
	}
	if err := task.TransitionTo(WorkflowPreparing, origin); !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("an archived task was revived: %v", err)
	}
}

// TestRunningStatesRequireASession checks that a task cannot report work in
// progress without the agent session it owns.
func TestRunningStatesRequireASession(t *testing.T) {
	task := confirmedTask(t)

	err := task.TransitionTo(WorkflowWorking, origin)
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("want ErrInvalidTransition, got %v", err)
	}
	if !strings.Contains(err.Error(), "owns no agent session") {
		t.Errorf("error does not name the missing session: %v", err)
	}

	if err := task.TransitionTo(WorkflowFailed, origin); err != nil {
		t.Errorf("a task whose preparation broke cannot be recorded as failed: %v", err)
	}
}

// TestShapeFreezesWhenTheTaskLeavesDraft checks that confirming a task freezes
// what the user confirmed. Feat creates nothing before confirmation, and after
// it the agent's brief and repository selection stop moving.
func TestShapeFreezesWhenTheTaskLeavesDraft(t *testing.T) {
	task := confirmedTask(t)

	changes := map[string]func() error{
		"title": func() error { return task.SetTitle("Another title", origin) },
		"brief": func() error { return task.SetBrief("Another brief", origin) },
		"bind": func() error {
			return task.Bind(TaskRepository{RepositoryID: testSecondary, Access: TaskAccessReadOnly}, origin)
		},
		"unbind": func() error { return task.Unbind(testPrimary, origin) },
	}

	for name, change := range changes {
		err := change()
		if !errors.Is(err, ErrInvariant) {
			t.Errorf("%s: want ErrInvariant, got %v", name, err)
		}
		var invariant *InvariantError
		if errors.As(err, &invariant) && !strings.Contains(invariant.Reason, string(WorkflowPreparing)) {
			t.Errorf("%s: error does not say which state froze the task: %v", name, err)
		}
	}

	if task.Title != "Add a scheduled export job" {
		t.Errorf("the title changed to %q", task.Title)
	}
	if len(task.Repositories) != 1 {
		t.Errorf("the repository selection changed to %d entries", len(task.Repositories))
	}
}

// TestBaseCommitIsImmutableAfterConfirmation checks invariant 8. A draft may be
// refreshed; a confirmed task's base is what every later diff, review, and
// cleanup decision is measured against.
func TestBaseCommitIsImmutableAfterConfirmation(t *testing.T) {
	task := draftTask(t)
	setBrief(t, task)
	bindPrimary(t, task)

	if err := task.ResolveBase(testPrimary, "origin/main", testCommit, origin); err != nil {
		t.Fatalf("resolving a draft's base: %v", err)
	}
	if err := task.ResolveBase(testPrimary, "origin/main", otherCommit, origin); err != nil {
		t.Fatalf("re-resolving a draft's base: %v", err)
	}

	if err := task.TransitionTo(WorkflowPreparing, origin); err != nil {
		t.Fatalf("confirming the draft: %v", err)
	}

	err := task.ResolveBase(testPrimary, "origin/main", testCommit, origin)
	if !errors.Is(err, ErrInvariant) {
		t.Fatalf("want ErrInvariant, got %v", err)
	}
	var invariant *InvariantError
	if !errors.As(err, &invariant) {
		t.Fatalf("error is not an *InvariantError: %v", err)
	}
	if invariant.Invariant != 8 {
		t.Errorf("error cites invariant %d", invariant.Invariant)
	}

	binding, _ := task.Repository(testPrimary)
	if binding.BaseCommit != otherCommit {
		t.Errorf("the recorded base changed to %s", binding.BaseCommit)
	}

	// Recording the same base again is what reconciliation does when it
	// re-reads a worktree, and it must not be an error.
	if err := task.ResolveBase(testPrimary, "origin/main", otherCommit, origin); err != nil {
		t.Errorf("re-recording the same base failed: %v", err)
	}
}

// TestTaskOwnsOneSessionAndOneRuntime checks invariants 2 and 3.
func TestTaskOwnsOneSessionAndOneRuntime(t *testing.T) {
	task := launchedTask(t)

	err := task.AttachSession(testSession(t), origin)
	if !errors.Is(err, ErrInvariant) {
		t.Fatalf("a task accepted a second agent session: %v", err)
	}

	runtime := &RuntimeEnvironment{Provider: "compose", Identity: "feat-example-7f3a1c2e", State: RuntimeStopped, Health: HealthUnknown}
	if err := task.AttachRuntime(runtime, origin); err != nil {
		t.Fatalf("attaching a runtime: %v", err)
	}
	if err := task.AttachRuntime(runtime, origin); !errors.Is(err, ErrInvariant) {
		t.Fatalf("a task accepted a second runtime: %v", err)
	}
}

func TestAgentSessionRequiresStableTmuxObjectIDs(t *testing.T) {
	valid := TmuxTarget{Socket: "/run/feat/tmux.sock", Session: "$1", Window: "@3", Pane: "%5"}
	if err := valid.Validate(testTask); err != nil {
		t.Fatalf("valid target: %v", err)
	}

	for name, mutate := range map[string]func(*TmuxTarget){
		"relative socket": func(target *TmuxTarget) { target.Socket = "tmux.sock" },
		"session name":    func(target *TmuxTarget) { target.Session = "example" },
		"window index":    func(target *TmuxTarget) { target.Window = "3" },
		"pane index":      func(target *TmuxTarget) { target.Pane = "1" },
	} {
		t.Run(name, func(t *testing.T) {
			target := valid
			mutate(&target)
			if err := target.Validate(testTask); !errors.Is(err, ErrInvalid) {
				t.Errorf("Validate(%+v) error = %v, want ErrInvalid", target, err)
			}
		})
	}

	session := testSession(t)
	reconciled := TmuxTarget{Socket: "/run/feat/tmux.sock", Session: "$9", Window: "@11", Pane: "%13"}
	when := origin.Add(time.Minute)
	if err := session.ReconcileTerminal(reconciled, ProcessRunning, testTask, when); err != nil {
		t.Fatalf("ReconcileTerminal: %v", err)
	}
	if session.Tmux != reconciled || session.Process != ProcessRunning || session.LastActivityAt != when {
		t.Errorf("reconciled session = %+v", session)
	}
}

// TestBindingRulesAreEnforced checks the rules a repository binding carries: no
// repository twice, a branch only where the agent may write, and absolute paths.
func TestBindingRulesAreEnforced(t *testing.T) {
	task := draftTask(t)
	bindPrimary(t, task)

	if err := task.Bind(TaskRepository{
		RepositoryID: testPrimary,
		Access:       TaskAccessReadOnly,
		WorktreePath: "/srv/state/worktrees/example/7f3a1c2e/core",
	}, origin); !errors.Is(err, ErrInvariant) {
		t.Errorf("a repository was bound twice: %v", err)
	}

	if err := task.Bind(TaskRepository{
		RepositoryID: testSecondary,
		Access:       TaskAccessReadOnly,
		Branch:       "feat/7f3a1c2e-export",
		WorktreePath: "/srv/state/worktrees/example/7f3a1c2e/schema",
	}, origin); !errors.Is(err, ErrInvariant) {
		t.Errorf("a read-only repository was given a task branch: %v", err)
	}

	if err := task.Bind(TaskRepository{
		RepositoryID: testSecondary,
		Access:       TaskAccessReadOnly,
		WorktreePath: "state/worktrees/schema",
	}, origin); !errors.Is(err, ErrInvalid) {
		t.Errorf("a relative worktree path was accepted: %v", err)
	}

	if err := task.ResolveBase(testPrimary, "origin/main", "1a2b3c4", origin); !errors.Is(err, ErrInvalid) {
		t.Errorf("an abbreviated commit was accepted as a recorded base: %v", err)
	}

	if err := task.ResolveBase(testSecondary, "origin/main", testCommit, origin); !errors.Is(err, ErrInvalid) {
		t.Errorf("a base was resolved for a repository the task does not bind: %v", err)
	}
}

// TestObservationsDoNotChangeTheWorkflow checks that the observed dimensions
// stay separate from the product-level state: an agent going idle leaves the
// task exactly where it was.
func TestObservationsDoNotChangeTheWorkflow(t *testing.T) {
	task := launchedTask(t)

	if err := task.Session.Observe(ProcessIdle, origin.Add(time.Minute)); err != nil {
		t.Fatalf("observing the process: %v", err)
	}
	if err := task.SetAttention(AttentionPossiblyWaiting, origin.Add(time.Minute)); err != nil {
		t.Fatalf("recording attention: %v", err)
	}
	if err := task.ObserveRepository(testPrimary, GitObservation{ChangedFiles: 3, ObservedAt: origin}, origin); err != nil {
		t.Fatalf("observing a repository: %v", err)
	}

	if task.Workflow != WorkflowWorking {
		t.Errorf("observations moved the task to %s", task.Workflow)
	}
	if err := task.Validate(); err != nil {
		t.Errorf("the observed task is invalid: %v", err)
	}
}

// TestValidateRejectsHandBuiltInconsistency checks that a task assembled by
// setting fields directly, rather than through the methods, is still caught. The
// store validates everything it writes and reads, so this is what stops an
// inconsistent aggregate from becoming the recorded state.
func TestValidateRejectsHandBuiltInconsistency(t *testing.T) {
	tests := map[string]func(*Task){
		"a running task without a session": func(task *Task) { task.Session = nil },
		"an unknown workflow state":        func(task *Task) { task.Workflow = "done" },
		"an unknown attention state":       func(task *Task) { task.Attention = "waiting" },
		"a repository bound twice":         func(task *Task) { task.Repositories = append(task.Repositories, task.Repositories[0]) },
		"a task with no title":             func(task *Task) { task.Title = "" },
		"an update before creation":        func(task *Task) { task.UpdatedAt = task.CreatedAt.Add(-time.Hour) },
		"a base that is not a commit":      func(task *Task) { task.Repositories[0].BaseCommit = "HEAD" },
	}

	for name, breakIt := range tests {
		t.Run(name, func(t *testing.T) {
			task := launchedTask(t)
			breakIt(task)
			if err := task.Validate(); err == nil {
				t.Errorf("%s validated", name)
			}
		})
	}
}

// TestNewTaskRejectsAnInvalidIdentity checks the identifiers a task is created
// with, which every later path and message depends on.
func TestNewTaskRejectsAnInvalidIdentity(t *testing.T) {
	source := TaskSource{Kind: SourcePrompt}

	if _, err := NewTask("../escape", testProject, "Title", source, origin); !errors.Is(err, ErrInvalid) {
		t.Errorf("a task identifier that is not a UUID was accepted: %v", err)
	}
	if _, err := NewTask(testTask, "../escape", "Title", source, origin); !errors.Is(err, ErrInvalid) {
		t.Errorf("a project identifier containing a path was accepted: %v", err)
	}
	if _, err := NewTask(testTask, testProject, "", source, origin); !errors.Is(err, ErrInvalid) {
		t.Errorf("a task without a title was accepted: %v", err)
	}
	if _, err := NewTask(testTask, testProject, "Title", TaskSource{Kind: SourceMarkdown}, origin); !errors.Is(err, ErrInvalid) {
		t.Errorf("an imported task without a file reference was accepted: %v", err)
	}
	if _, err := NewTask(testTask, testProject, "Title", TaskSource{Kind: "epic"}, origin); !errors.Is(err, ErrInvalid) {
		t.Errorf("an undocumented source was accepted: %v", err)
	}
}

// testTicket is the ticket a task brief was composed from.
func testTicket() *ExternalTaskReference {
	return &ExternalTaskReference{
		Provider:  "tracker",
		Reference: "PROJ-482",
		URL:       "https://tracker.example.com/stories/482",
		Snapshot: TicketSnapshot{
			Title:   "Retry the nightly export",
			Body:    "The nightly export gives up on the first timeout.\n",
			State:   "in progress",
			TakenAt: origin,
		},
	}
}

// TestATaskFromATicketCarriesTheSnapshotItWasComposedFrom checks the reference
// a ticket-sourced task holds.
//
// The snapshot is what a later change is compared against, and it is what lets
// a merge request name the ticket it closes; a source that recorded only the
// kind would leave both questions with nothing to answer them (ADR-071).
func TestATaskFromATicketCarriesTheSnapshotItWasComposedFrom(t *testing.T) {
	ticket := testTicket()
	task, err := NewTask(testTask, testProject, "Retry the nightly export",
		TaskSource{Kind: SourceTicket, Ticket: ticket}, origin)
	if err != nil {
		t.Fatalf("creating a task from a ticket: %v", err)
	}

	if task.Source.Kind != SourceTicket {
		t.Errorf("source kind = %q, want %q", task.Source.Kind, SourceTicket)
	}
	if task.Source.Ticket == nil {
		t.Fatal("the task records no ticket")
	}
	if got, want := task.Source.Ticket.Reference, ticket.Reference; got != want {
		t.Errorf("ticket reference = %q, want %q", got, want)
	}
	if got, want := task.Source.Ticket.Snapshot.State, ticket.Snapshot.State; got != want {
		// The tracker's own vocabulary, not one of Feat's: trackers do not
		// agree on states, and mapping them would be a mapping language.
		t.Errorf("ticket state = %q, want the tracker's own %q", got, want)
	}
}

// TestATicketSourceIsRefusedWhenItCannotBeActedOn covers the inconsistencies a
// ticket source must not hold.
//
// Each of them would leave Feat with a task that says it came from a ticket and
// cannot say which one, or with two answers to where it came from.
func TestATicketSourceIsRefusedWhenItCannotBeActedOn(t *testing.T) {
	without := func(edit func(ticket *ExternalTaskReference)) TaskSource {
		ticket := testTicket()
		edit(ticket)
		return TaskSource{Kind: SourceTicket, Ticket: ticket}
	}

	for name, source := range map[string]TaskSource{
		"a ticket source with no ticket": {Kind: SourceTicket},
		"a ticket beside a file reference": {
			Kind: SourceTicket, Reference: "/srv/briefs/example.md", Ticket: testTicket(),
		},
		"a ticket on a typed prompt":    {Kind: SourcePrompt, Ticket: testTicket()},
		"a ticket with no reference":    without(func(k *ExternalTaskReference) { k.Reference = "" }),
		"a ticket with nowhere to read": without(func(k *ExternalTaskReference) { k.URL = "" }),
		"a snapshot with no title":      without(func(k *ExternalTaskReference) { k.Snapshot.Title = "" }),
		"a snapshot with no state":      without(func(k *ExternalTaskReference) { k.Snapshot.State = "" }),
		"a snapshot with no time":       without(func(k *ExternalTaskReference) { k.Snapshot.TakenAt = time.Time{} }),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewTask(testTask, testProject, "Title", source, origin); !errors.Is(err, ErrInvalid) {
				t.Errorf("%s was accepted: %v", name, err)
			}
		})
	}
}

// TestATicketWithNoSourceIsStillATicket checks that the provider is optional.
//
// A project drawing on one tracker has nothing to disambiguate, so requiring it
// would make every such command carry a constant (ADR-071).
func TestATicketWithNoSourceIsStillATicket(t *testing.T) {
	ticket := testTicket()
	ticket.Provider = ""

	if _, err := NewTask(testTask, testProject, "Title",
		TaskSource{Kind: SourceTicket, Ticket: ticket}, origin); err != nil {
		t.Errorf("a ticket from an unnamed tracker was refused: %v", err)
	}
}

func draftTask(t *testing.T) *Task {
	t.Helper()

	task, err := NewTask(testTask, testProject, "Add a scheduled export job", TaskSource{Kind: SourcePrompt}, origin)
	if err != nil {
		t.Fatalf("creating a draft: %v", err)
	}
	return task
}

func confirmedTask(t *testing.T) *Task {
	t.Helper()

	task := draftTask(t)
	setBrief(t, task)
	bindPrimary(t, task)
	resolvePrimary(t, task)
	if err := task.TransitionTo(WorkflowPreparing, origin); err != nil {
		t.Fatalf("confirming the draft: %v", err)
	}
	return task
}

func launchedTask(t *testing.T) *Task {
	t.Helper()

	task := confirmedTask(t)
	if err := task.AttachSession(testSession(t), origin); err != nil {
		t.Fatalf("attaching a session: %v", err)
	}
	if err := task.TransitionTo(WorkflowWorking, origin); err != nil {
		t.Fatalf("starting work: %v", err)
	}
	return task
}

func testSession(t *testing.T) *AgentSession {
	t.Helper()

	session, err := NewAgentSession("claude", ExecutionDevcontainer,
		TmuxTarget{Socket: "/run/feat/tmux.sock", Session: "$1", Window: "@3", Pane: "%5"},
		"/srv/state/control/example/7f3a1c2e", origin)
	if err != nil {
		t.Fatalf("creating a session: %v", err)
	}
	return session
}

func setBrief(t *testing.T, task *Task) {
	t.Helper()

	if err := task.SetBrief("Export the daily report.", origin); err != nil {
		t.Fatalf("setting the brief: %v", err)
	}
}

func bindPrimary(t *testing.T, task *Task) {
	t.Helper()

	bind(t, task, TaskRepository{
		RepositoryID:  testPrimary,
		Access:        TaskAccessReadWrite,
		Branch:        "feat/7f3a1c2e-export",
		WorktreePath:  "/srv/state/worktrees/example/7f3a1c2e/core",
		ContainerPath: "/src/core",
	})
}

func bind(t *testing.T, task *Task, binding TaskRepository) {
	t.Helper()

	if err := task.Bind(binding, origin); err != nil {
		t.Fatalf("binding %s: %v", binding.RepositoryID, err)
	}
}

func resolvePrimary(t *testing.T, task *Task) {
	t.Helper()

	if err := task.ResolveBase(testPrimary, "origin/main", testCommit, origin); err != nil {
		t.Fatalf("resolving the base: %v", err)
	}
}

// TestAFailedTaskCarriesItsReason is the rule that keeps a state and its
// explanation from being written apart.
//
// The reason a task failed was recorded only as the detail of a workflow event,
// which lives in the task's log on disk. What a user looks at is the task, and
// what it said was `failed` and nothing else.
func TestAFailedTaskCarriesItsReason(t *testing.T) {
	task := launchedTask(t)

	const reason = "the container mounts a Docker socket at /var/run/docker.sock"
	if err := task.FailWith(reason, origin.Add(time.Minute)); err != nil {
		t.Fatalf("failing the task: %v", err)
	}

	if task.Workflow != WorkflowFailed {
		t.Fatalf("workflow = %s, want failed", task.Workflow)
	}
	if task.Failure == nil {
		t.Fatal("the failed task records no reason")
	}
	if task.Failure.Reason != reason {
		t.Errorf("reason = %q, want it kept verbatim: %q", task.Failure.Reason, reason)
	}
	if !task.Failure.At.Equal(origin.Add(time.Minute)) {
		t.Errorf("failed at %s, want the moment it failed", task.Failure.At)
	}
}

// TestAFailureWithNoReasonIsRefused keeps "failed for an unknown reason" out of
// the record. Whatever failed knows what it was.
func TestAFailureWithNoReasonIsRefused(t *testing.T) {
	task := launchedTask(t)

	if err := task.FailWith("   ", origin); err == nil {
		t.Fatal("a task was failed with no reason")
	}
	if task.Workflow == WorkflowFailed {
		t.Error("the refused failure moved the task anyway")
	}
}

// TestATaskPutBackToWorkStopsExplainingItsFailure is the other half of the rule.
//
// A recovered task that still carried the reason it once failed would be read as
// failing now: a stale explanation beside a live state is worse than none.
func TestATaskPutBackToWorkStopsExplainingItsFailure(t *testing.T) {
	task := launchedTask(t)
	if err := task.FailWith("the agent could not be started", origin); err != nil {
		t.Fatalf("failing the task: %v", err)
	}

	if err := task.TransitionTo(WorkflowWorking, origin.Add(time.Minute)); err != nil {
		t.Fatalf("putting the task back to work: %v", err)
	}
	if task.Failure != nil {
		t.Errorf("a working task still explains a failure it recovered from: %+v", task.Failure)
	}
}
