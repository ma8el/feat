package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/ma8el/feat/internal/api"
)

// The task list is a client-side rendering of what arrives over the socket, so
// its fixtures are wire payloads rather than domain objects: what is under test
// is how the CLI renders a response, not how the daemon produced one.
var (
	// created is when the fixture tasks were created.
	created = time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)
	// listTime is when the list is rendered, an hour later, so that elapsed
	// columns do not change between runs.
	listTime = created.Add(time.Hour)
)

// launchedTask is a task that has been confirmed, observed, and has a runtime.
func launchedTask() api.Task {
	return api.Task{
		ID:        "7f3a1c2e-5b6d-4a80-9c1f-2d3e4f5a6b7c",
		Key:       "7f3a1c2e",
		ProjectID: "example",
		Title:     "Add a scheduled export job",
		Source:    api.Source{Kind: "prompt"},
		Workflow:  "review_requested",
		Attention: "possibly_waiting",
		Repositories: []api.TaskRepository{{
			RepositoryID: "core",
			Access:       "read_write",
			BaseCommit:   "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d",
			Branch:       "feat/7f3a1c2e-add-a-scheduled-export-job",
			WorktreePath: "/srv/state/worktrees/example/7f3a1c2e/core",
			Observation:  &api.GitObservation{Dirty: true, ChangedFiles: 7, ObservedAt: created},
		}},
		Session:   &api.Session{Provider: "claude", ExecutionMode: "devcontainer", Process: "running"},
		Runtime:   &api.Runtime{Provider: "compose", Identity: "feat-example-7f3a1c2e", State: "running", Health: "unknown"},
		CreatedAt: created,
		UpdatedAt: created,
	}
}

// draftTask is a resolved draft: nothing has been created for it, and nothing
// about it has been observed.
func draftTask() api.Task {
	return api.Task{
		ID:        "2c4e6a80-1b3d-4f52-8a7c-9e0d1f2a3b4c",
		Key:       "2c4e6a80",
		ProjectID: "example",
		Title:     "Retry failed exports",
		Source:    api.Source{Kind: "prompt"},
		Workflow:  "draft",
		Attention: "none",
		Repositories: []api.TaskRepository{{
			RepositoryID: "core",
			Access:       "read_write",
			BaseCommit:   "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d",
			Branch:       "feat/2c4e6a80-retry-failed-exports",
			WorktreePath: "/srv/state/worktrees/example/2c4e6a80/core",
		}},
		CreatedAt: created,
		UpdatedAt: created,
	}
}

// listed renders tasks the way `feat task list` does.
func listed(tasks []api.Task) string {
	var out bytes.Buffer
	printTasks(&out, tasks, listTime)
	return out.String()
}

// TestTaskListContainsRequiredV0Fields checks that the task list carries the
// required v0 fields.
//
// FR-UI-002 lists nine fields plus two this build cannot measure. The two are
// checked separately, in TestUnmeasuredFieldsAreNotReportedAsValues: what must
// never happen is a plausible-looking number where nothing was measured.
func TestTaskListContainsRequiredV0Fields(t *testing.T) {
	task := launchedTask()
	output := listed([]api.Task{task})

	required := map[string]string{
		"task identifier": task.Key,
		"title":           task.Title,
		"repositories":    "", // repositories are shown in the detail view, not the list
		"workflow state":  task.Workflow,
		"agent state":     task.Session.Process,
		"attention state": task.Attention,
		"runtime state":   task.Runtime.State,
		"changed files":   "7",
		"elapsed time":    "1h",
	}
	for field, want := range required {
		if want == "" {
			continue
		}
		if !strings.Contains(output, want) {
			t.Errorf("the task list does not show the %s %q:\n%s", field, want, output)
		}
	}

	// PR state is explicitly not required in a v0 task row, and nothing fills
	// one, so the list must not carry a column for it.
	heading, _, _ := strings.Cut(output, "\n")
	for _, column := range strings.Fields(heading) {
		if column == "PR" || column == "MR" {
			t.Errorf("the task list has a %s column, which v0 does not require:\n%s", column, output)
		}
	}
}

// TestUnmeasuredFieldsAreNotReportedAsValues checks the honesty rule ADR-028
// established for diagnostics and ADR-031 carried into the task list.
//
// A task nothing has observed has no change count, and a task with no session
// has no agent state. Reporting either as zero or as "stopped" would be a claim
// about the world Feat has not looked at.
func TestUnmeasuredFieldsAreNotReportedAsValues(t *testing.T) {
	draft := draftTask()
	output := listed([]api.Task{draft})

	if draft.Session != nil {
		t.Fatal("the draft fixture has a session; it should have none before launch")
	}
	if strings.Contains(output, "stopped") {
		t.Errorf("a task with no session is reported as stopped:\n%s", output)
	}
	if strings.Contains(output, " 0 ") {
		t.Errorf("a task nothing has observed reports a change count of zero:\n%s", output)
	}
	if !strings.Contains(output, absent) {
		t.Errorf("no field is marked as unmeasured:\n%s", output)
	}

	// The runtime is genuinely absent rather than unmeasured: v0 starts
	// application services only when the user asks.
	if !strings.Contains(output, "absent") {
		t.Errorf("the runtime state is missing:\n%s", output)
	}
}

// TestDraftsAndLaunchedTasksAreListedTogether checks that both states are
// visible at once, and that a cancelled draft is counted rather than listed.
func TestDraftsAndLaunchedTasksAreListedTogether(t *testing.T) {
	launched := launchedTask()
	draft := draftTask()

	cancelled := draftTask()
	cancelled.ID = "8b7a6c5d-4e3f-4a21-9b8c-7d6e5f4a3b2c"
	cancelled.Key = "8b7a6c5d"
	cancelled.Workflow = "archived"

	output := listed([]api.Task{launched, draft, cancelled})

	if !strings.Contains(output, launched.Key) {
		t.Errorf("the launched task is missing:\n%s", output)
	}
	if !strings.Contains(output, draft.Key) {
		t.Errorf("the draft is missing:\n%s", output)
	}
	if strings.Contains(output, cancelled.Key) {
		t.Errorf("an archived task is listed:\n%s", output)
	}
	if !strings.Contains(output, "1 archived task not shown") {
		t.Errorf("the archived task is not accounted for:\n%s", output)
	}
}

// TestAnEmptyTaskListSaysWhatToDo checks the quality bar: a command that finds
// nothing explains how to get something.
func TestAnEmptyTaskListSaysWhatToDo(t *testing.T) {
	output := listed(nil)

	if !strings.Contains(output, "feat implement") {
		t.Errorf("an empty task list does not say how to create a task:\n%s", output)
	}
}
