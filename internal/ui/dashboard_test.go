package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ma8el/feat/internal/api"
)

var (
	// dashboardOrigin is when the fixture tasks were created.
	dashboardOrigin = time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)
	// dashboardNow is when the dashboard renders them.
	dashboardNow = dashboardOrigin.Add(90 * time.Minute)
)

func liveTask() api.Task {
	return api.Task{
		ID: "7f3a1c2e-5b6d-4a80-9c1f-2d3e4f5a6b7c", Key: "7f3a1c2e",
		ProjectID: "example", Title: "Add a scheduled export job",
		Brief:    "Export the daily report to the configured bucket.",
		Source:   api.Source{Kind: "markdown", Reference: "/srv/notes/export.md"},
		Workflow: "working", Attention: "possibly_waiting",
		Repositories: []api.TaskRepository{
			{
				RepositoryID: "core", Access: "read_write",
				BaseRef: "origin/main", BaseCommit: "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d",
				Branch:       "feat/7f3a1c2e-add-a-scheduled-export-job",
				WorktreePath: "/srv/worktrees/example/7f3a1c2e/core",
				Observation:  &api.GitObservation{Dirty: true, ChangedFiles: 7, ObservedAt: dashboardOrigin},
			},
			{
				RepositoryID: "schema", Access: "read_only",
				BaseRef: "origin/main", BaseCommit: "9f8e7d6c5b4a39281706f5e4d3c2b1a09f8e7d6c",
				WorktreePath: "/srv/worktrees/example/7f3a1c2e/schema",
			},
		},
		Session: &api.Session{
			Provider: "claude", ExecutionMode: "devcontainer", Process: "running",
			Tmux: api.Tmux{Socket: "/run/feat/tmux.sock", Session: "$0", Window: "@3", Pane: "%7"},
			Execution: &api.Execution{
				Provider: "compose", Identity: "feat-agent-example-7f3a1c2e",
				Service: "dev", User: "coder",
				Container: "9f8e7d6c5b4a", Running: true, Status: "Up 4 minutes",
			},
		},
		CreatedAt: dashboardOrigin, UpdatedAt: dashboardOrigin,
	}
}

// TestVerificationIsShownAsAClaimRatherThanAResult checks the honesty of the
// column ADR-032 narrowed.
//
// An agent-reported result and a provider-enforced one are different facts, and
// the dashboard shows checks the agent asserted about its own work. Rendering
// them as though something had verified them would tell the user what Feat does
// not know.
func TestVerificationIsShownAsAClaimRatherThanAResult(t *testing.T) {
	reported := liveTask()
	reported.Verification = &api.Verification{
		Source: "agent", Passed: 2, Failed: 1,
		Summary: "Added the export job; one test still fails.", ReportedAt: dashboardOrigin,
	}

	model := dashboard(newFakeBackend(), reported)
	list := model.View()
	if !strings.Contains(list, "2/3") {
		t.Errorf("the task list does not show the reported counts:\n%s", list)
	}
	if !strings.Contains(list, "~") {
		t.Errorf("the task list does not mark the result as the agent's claim:\n%s", list)
	}

	model.selected = reported.ID
	model.screen = screenDetail
	detail := model.View()

	for _, want := range []string{"2 passed", "1 failed", "reported by the agent", "one test still fails"} {
		if !strings.Contains(detail, want) {
			t.Errorf("the detail view does not show %q:\n%s", want, detail)
		}
	}

	// A task whose agent has reported nothing shows nothing, rather than a row
	// of zeroes claiming that nothing failed.
	silent := dashboard(newFakeBackend(), liveTask()).View()
	if strings.Contains(silent, "0/0") {
		t.Errorf("a task that reported no checks was rendered as having run zero:\n%s", silent)
	}
}

func pendingDraft() api.Task {
	return api.Task{
		ID: "2c4e6a80-1b3d-4f52-8a7c-9e0d1f2a3b4c", Key: "2c4e6a80",
		ProjectID: "example", Title: "Retry failed exports",
		Brief:    "Retry three times before giving up.",
		Source:   api.Source{Kind: "prompt"},
		Workflow: "draft", Attention: "none",
		CreatedAt: dashboardOrigin, UpdatedAt: dashboardOrigin,
	}
}

// dashboard returns a loaded dashboard over the given tasks.
func dashboard(backend *fakeBackend, tasks ...api.Task) Model {
	backend.tasks = tasks

	model := New(Options{
		Backend: backend,
		Daemon:  Daemon{Version: "v0.0.0-test", Socket: "/run/feat/feat.sock"},
		Now:     func() time.Time { return dashboardNow },
	})
	updated, _ := model.Update(tasksMsg{tasks: tasks})
	return updated.(Model)
}

// TestTheTaskListShowsTheRequiredV0Fields is the slice 6 acceptance criterion
// at the dashboard.
//
// FR-UI-002 requires eleven fields in a task row. Verification state is the one
// this build still cannot fill, and it appears as absent rather than as a value
// nothing measured. Resource usage is slice 10's and is filled here from a
// sample, which a separate test covers; this one runs against a dashboard that
// has not sampled yet, which is the state of every session's first seconds.
func TestTheTaskListShowsTheRequiredV0Fields(t *testing.T) {
	model := dashboard(newFakeBackend(), liveTask())
	view := model.View()

	for field, want := range map[string]string{
		"task identifier":  "7f3a1c2e",
		"title":            "Add a scheduled export job",
		"repositories":     "core",
		"read-only marker": "schema (ro)",
		"workflow":         "working",
		"agent state":      "running",
		"attention":        "possibly waiting",
		"runtime":          "absent",
		"changed files":    "7",
		"elapsed":          "1h30m",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the task row does not show the %s %q:\n%s", field, want, view)
		}
	}

	// Every required column has a heading, including the two nothing fills.
	for _, heading := range []string{"CHECKS", "RESOURCES"} {
		if !strings.Contains(view, heading) {
			t.Errorf("the task list has no %s column:\n%s", heading, view)
		}
	}
	if !strings.Contains(view, absent) {
		t.Errorf("no unmeasured field is marked as absent:\n%s", view)
	}
}

// TestTheDetailViewNamesTheSlicesItIsWaitingOn checks the honesty rule: a field
// this build cannot fill says which slice fills it, rather than showing nothing
// and leaving the user to guess.
func TestTheDetailViewNamesTheSlicesItIsWaitingOn(t *testing.T) {
	model := dashboard(newFakeBackend(), liveTask())
	model.selected = liveTask().ID
	model.screen = screenDetail

	view := model.View()
	for _, want := range []string{
		// FR-UI-003's required content.
		"Export the daily report", "core", "origin/main", "1a2b3c4d5e6f",
		"feat/7f3a1c2e-add-a-scheduled-export-job", "$0", "@3", "%7",
		// And what it cannot fill yet. Slice 7 delivered the agent-reported half
		// of verification, slice 8 the environment, and slice 10 the resource
		// figures, so what remains outstanding is the gate that runs a project's
		// configured checks, which is slice 11's.
		"slice 11",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the detail view does not show %q:\n%s", want, view)
		}
	}
	// The slices that have been delivered stop being named. A screen that still
	// promised one would be telling the user to wait for something they have.
	for _, delivered := range []string{"slice 7", "slice 8", "slice 10"} {
		if strings.Contains(view, delivered) {
			t.Errorf("the detail view still names %q, which has been delivered:\n%s", delivered, view)
		}
	}
}

// TestTheDetailViewShowsWhereTheAgentRuns checks that a containerised session
// says so, and says enough to be acted on.
//
// The identity is what a user needs to inspect or clean up the container
// themselves; the user is the boundary the security model describes, and a
// boundary nobody can see is one nobody can check.
func TestTheDetailViewShowsWhereTheAgentRuns(t *testing.T) {
	model := dashboard(newFakeBackend(), liveTask())
	model.selected = liveTask().ID
	model.screen = screenDetail

	view := model.View()
	for _, want := range []string{
		"feat-agent-example-7f3a1c2e", "dev", "coder", "no Docker access", "container_name",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the detail view does not show %q:\n%s", want, view)
		}
	}
}

// TestADraftIsDistinguishableFromALaunchedTask checks that the list does not
// make a draft look like a running task.
//
// They differ in everything that matters: a draft has no worktree, no branch,
// and no terminal.
func TestADraftIsDistinguishableFromALaunchedTask(t *testing.T) {
	model := dashboard(newFakeBackend(), liveTask(), pendingDraft())
	view := model.View()

	if !strings.Contains(view, "draft") {
		t.Errorf("the draft's workflow state is not shown:\n%s", view)
	}
	if !strings.Contains(view, "2c4e6a80") || !strings.Contains(view, "7f3a1c2e") {
		t.Errorf("both tasks should be listed:\n%s", view)
	}
}

// TestArchivedTasksAreCountedNotListed checks that a cancelled draft leaves the
// list without disappearing silently.
func TestArchivedTasksAreCountedNotListed(t *testing.T) {
	cancelled := pendingDraft()
	cancelled.Workflow = "archived"

	model := dashboard(newFakeBackend(), liveTask(), cancelled)
	view := model.View()

	if strings.Contains(view, cancelled.Key) {
		t.Errorf("an archived task is listed:\n%s", view)
	}
	if !strings.Contains(view, "1 archived task not shown") {
		t.Errorf("the archived task is not accounted for:\n%s", view)
	}
}

// TestAttachAndShellUseTheSelectedTask checks that the two terminal actions ask
// for the task under the cursor and hand the terminal over.
func TestAttachAndShellUseTheSelectedTask(t *testing.T) {
	backend := newFakeBackend()
	model := dashboard(backend, liveTask())

	for _, action := range []struct {
		key    string
		record func() []string
		name   string
	}{
		{"a", func() []string { return backend.attached }, "attach"},
		{"s", func() []string { return backend.shells }, "shell"},
	} {
		updated, cmd := model.Update(key(action.key))
		model = updated.(Model)
		if cmd == nil {
			t.Fatalf("%s produced no command", action.name)
		}
		// The command resolves the target and returns the exec message Bubble
		// Tea would run; running it here is enough to see what was asked for.
		cmd()

		if got := action.record(); len(got) != 1 || got[0] != liveTask().ID {
			t.Errorf("%s asked for %v, want the selected task", action.name, got)
		}
	}
}

// TestATaskWithNoTerminalCannotBeAttached checks that the action explains
// itself rather than failing obscurely.
func TestATaskWithNoTerminalCannotBeAttached(t *testing.T) {
	backend := newFakeBackend()
	model := dashboard(backend, pendingDraft())

	updated, _ := model.Update(key("a"))
	model = updated.(Model)

	if len(backend.attached) != 0 {
		t.Error("a draft with no terminal was attached to")
	}
	if !strings.Contains(model.status, "no terminal yet") {
		t.Errorf("status = %q, want one explaining why nothing happened", model.status)
	}
}

// TestOnlyADraftIsCancelledFromTheDashboard checks that the destructive-looking
// key is not a shortcut for removing a launched task's resources.
//
// Cleanup resolves exact targets and confirms per resource class, and slice 12
// delivers it; a launched task must be sent there rather than quietly archived.
func TestOnlyADraftIsCancelledFromTheDashboard(t *testing.T) {
	backend := newFakeBackend()
	model := dashboard(backend, liveTask())

	updated, _ := model.Update(key("x"))
	model = updated.(Model)

	if len(backend.cancelled) != 0 {
		t.Error("a launched task was cancelled from the dashboard")
	}
	if !strings.Contains(model.status, "feat cleanup") {
		t.Errorf("status = %q, want one naming the command that removes resources", model.status)
	}

	// A draft, by contrast, is cancelled.
	model = dashboard(backend, pendingDraft())
	updated, cmd := model.Update(key("x"))
	model = updated.(Model)
	if cmd != nil {
		cmd()
	}
	if len(backend.cancelled) != 1 {
		t.Errorf("the draft was cancelled %d times, want once", len(backend.cancelled))
	}
}

// TestAnEmptyDashboardSaysWhatToDo checks the quality bar for a first run.
func TestAnEmptyDashboardSaysWhatToDo(t *testing.T) {
	model := dashboard(newFakeBackend())
	view := model.View()

	if !strings.Contains(view, "feat implement") {
		t.Errorf("an empty dashboard does not say how to create a task:\n%s", view)
	}
}

// TestAFailedReadIsReportedRatherThanFatal checks that one failed request does
// not take the view of every running task with it.
func TestAFailedReadIsReportedRatherThanFatal(t *testing.T) {
	model := dashboard(newFakeBackend(), liveTask())

	updated, _ := model.Update(tasksMsg{err: errTest})
	model = updated.(Model)

	view := model.View()
	if !strings.Contains(view, errTest.Error()) {
		t.Errorf("the failure is not reported:\n%s", view)
	}
	if !strings.Contains(view, "7f3a1c2e") {
		t.Errorf("the last known state was discarded on a failed read:\n%s", view)
	}
}

var errTest = testError("the daemon could not be reached")

type testError string

func (e testError) Error() string { return string(e) }

// TestEventsCauseAReRead checks that a published state change makes the
// dashboard re-read rather than apply the event itself.
func TestEventsCauseAReRead(t *testing.T) {
	backend := newFakeBackend()
	model := dashboard(backend, liveTask())

	_, cmd := model.Update(eventMsg{event: api.Event{Kind: api.KindTask}})
	if cmd == nil {
		t.Fatal("an event produced no command")
	}

	// tea.Batch returns a BatchMsg holding the commands it will run; one of
	// them must be the read. They are run with a bound, because another of them
	// waits for the next item on an event stream this test never opened, and a
	// command that waits is the point rather than a fault.
	batch, ok := run(cmd).(tea.BatchMsg)
	if !ok {
		t.Fatalf("an event produced %T, want a batch including a re-read", run(cmd))
	}
	var read bool
	for _, command := range batch {
		if _, isRead := run(command).(tasksMsg); isRead {
			read = true
		}
	}
	if !read {
		t.Error("an event did not make the dashboard re-read state")
	}
}
