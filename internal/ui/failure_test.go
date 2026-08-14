package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/ma8el/feat/internal/api"
)

// failedTask is a task whose launch was refused after its container existed,
// which is the state a user meets with the least to go on.
func failedTask() api.Task {
	task := liveTask()
	task.Workflow = "failed"
	task.Attention = "needs_input"
	task.Session = nil
	task.Failure = &api.TaskFailure{
		Reason: "task 7f3a1c2e cannot run its agent in service dev of feat-agent-example-7f3a1c2e: " +
			"the container mounts the home directory of the user the daemon runs as at /host-home",
		At: dashboardOrigin.Add(-2 * time.Minute),
	}
	return task
}

// TestAFailedTaskSaysWhyOnItsPanel is the dogfood finding.
//
// The panel said `workflow failed` and stopped. The reason was recorded — it is
// the detail of the workflow transition — and it was reachable from nowhere: the
// event log is a file on disk, and the error banner the launch produced had
// already gone by the time anybody looked. A state a user cannot act on is one
// that only describes itself.
func TestAFailedTaskSaysWhyOnItsPanel(t *testing.T) {
	task := failedTask()
	model := dashboard(newFakeBackend(), task)
	model.selected = task.ID
	model.screen = screenTask

	panel := plainText(model.taskPanel())
	if !strings.Contains(panel, "workflow") || !strings.Contains(panel, "failed") {
		t.Fatalf("the panel does not show the workflow:\n%s", panel)
	}
	for what, want := range map[string]string{
		"the service that refused it": "service dev",
		"what was wrong with it":      "the home directory of the user the daemon runs as",
		"when it failed":              "failed at",
	} {
		if !strings.Contains(panel, want) {
			t.Errorf("the panel does not say %s (%q):\n%s", what, want, panel)
		}
	}
}

// TestTheReasonIsNotCutShort keeps the end of the sentence, which is the half
// that identifies what to change.
//
// A reason names a service, a mount, or a path, and every one of them is at the
// end: truncating to the panel's width would leave "the container mounts the
// home dir…" — a message that reads like an explanation and is not one. The
// panel wraps instead, and the wrap is what the region renders.
func TestTheReasonIsNotCutShort(t *testing.T) {
	task := failedTask()
	model := dashboard(newFakeBackend(), task)
	model.selected = task.ID
	model.screen = screenTask

	// Both ends of the reason survive a width narrower than it is. A truncation
	// would keep the first and drop the second, which is the failure this
	// guards: the identifying half is at the end.
	wrapped := strings.Join(strings.Fields(plainText(model.wrappedPanel(60))), " ")
	for what, want := range map[string]string{
		"the beginning of the reason": "cannot run its agent",
		"the end of the reason":       "at /host-home",
	} {
		if !strings.Contains(wrapped, want) {
			t.Errorf("%s is missing from a panel wrapped to 60 columns (%q):\n%s", what, want, wrapped)
		}
	}
}

// TestATaskThatIsNotFailedExplainsNothing keeps the block from becoming
// furniture: a working task has no failure to report, and a panel that always
// carried the heading would say something about every task that is only true of
// some.
func TestATaskThatIsNotFailedExplainsNothing(t *testing.T) {
	model := dashboard(newFakeBackend(), liveTask())
	model.selected = liveTask().ID
	model.screen = screenTask

	if panel := plainText(model.taskPanel()); strings.Contains(panel, "failed at") {
		t.Errorf("a working task's panel talks about a failure:\n%s", panel)
	}
}
