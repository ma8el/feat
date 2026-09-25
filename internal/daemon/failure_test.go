package daemon

import (
	"context"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/domain"
)

// TestAFailedLaunchRecordsWhyOnTheTask checks that a refused launch leaves its
// reason on the task. It is the same sentence the caller was given, because two
// accounts of one event would be one too many.
func TestAFailedLaunchRecordsWhyOnTheTask(t *testing.T) {
	arranged := arrangeDrafting(t)
	arranged.docker.Answer("inspect --type container --format {{json .Mounts}} c0ffee",
		`[{"Type":"bind","Source":"/var/run/docker.sock","Destination":"/var/run/docker.sock","RW":true}]`)

	draft := arranged.draft(t, "Add a rate limit")
	arranged.selectRepositories(t, draft.ID)
	displayed := arranged.resolve(t, draft.ID)

	_, err := arranged.service.LaunchDraft(context.Background(), draft.ID, api.Confirmation{Fingerprint: displayed.Fingerprint})
	if err == nil {
		t.Fatal("the launch succeeded")
	}

	// Reloaded rather than read from the returned task. What a user sees minutes
	// later is what storage kept, and a reason held only in memory is gone by then.
	task := arranged.reload(t, draft.ID)
	if task.Workflow != domain.WorkflowFailed {
		t.Fatalf("workflow = %q, want failed", task.Workflow)
	}
	if task.Failure == nil {
		t.Fatal("the failed task records no reason, so the panel can only say that something went wrong")
	}
	if task.Failure.Reason != err.Error() {
		t.Errorf("the task says %q and the caller was told %q; they describe one event",
			task.Failure.Reason, err)
	}
	if !strings.Contains(task.Failure.Reason, "docker.sock") {
		t.Errorf("the recorded reason does not name what was wrong: %q", task.Failure.Reason)
	}
	if task.Failure.At.IsZero() {
		t.Error("the failure has no time, so nothing can say whether it is the current one")
	}
}
