package daemon

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/client"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/store/storetest"
)

// TestTaskIsAddressableByTaskIDAlone covers the resolution ADR-027 puts in the
// daemon: storage addresses a task by project and task, and the command surface
// addresses it by task alone.
func TestTaskIsAddressableByTaskIDAlone(t *testing.T) {
	live := serve(t, Options{})
	seed(t, live, storetest.Project(), storetest.Task())

	task, err := live.client(t).Task(context.Background(), storetest.TaskID.String())
	if err != nil {
		t.Fatalf("Task: %v", err)
	}

	if task.ID != storetest.TaskID.String() {
		t.Errorf("task id = %q, want %q", task.ID, storetest.TaskID)
	}
	if task.ProjectID != storetest.ProjectID.String() {
		t.Errorf("project id = %q, want %q: the daemon resolves the owning project",
			task.ProjectID, storetest.ProjectID)
	}
	if task.Key != storetest.TaskID.Key().String() {
		t.Errorf("key = %q, want %q", task.Key, storetest.TaskID.Key())
	}
	if len(task.Repositories) != len(storetest.Task().Repositories) {
		t.Errorf("task has %d repositories, want %d",
			len(task.Repositories), len(storetest.Task().Repositories))
	}
}

func TestUnknownTaskIsNotFound(t *testing.T) {
	live := serve(t, Options{})
	seed(t, live, storetest.Project(), nil)

	missing := domain.TaskID("11111111-2222-4333-8444-555555555555")
	_, err := live.client(t).Task(context.Background(), missing.String())

	if err == nil {
		t.Fatal("a task that does not exist was returned")
	}
	if !notFound(err) {
		t.Errorf("error = %v, want a 404", err)
	}
}

// TestMalformedIdentifierIsRejectedBeforeStorage checks that a bad identifier is
// a client error rather than a lookup: nothing malformed may reach the code that
// builds a path from it.
func TestMalformedIdentifierIsRejectedBeforeStorage(t *testing.T) {
	live := serve(t, Options{})

	for _, id := range []string{"not-a-uuid", "../../etc/passwd", "%2e%2e%2f"} {
		_, err := live.client(t).Task(context.Background(), id)
		if err == nil {
			t.Errorf("Task(%q) succeeded", id)
			continue
		}
		if !badRequest(err) {
			t.Errorf("Task(%q) failed with %v, want a 400", id, err)
		}
	}
}

func TestProjectsAndTasksListEverything(t *testing.T) {
	live := serve(t, Options{})
	seed(t, live, storetest.Project(), storetest.Task())

	caller := live.client(t)

	projects, err := caller.Projects(context.Background())
	if err != nil {
		t.Fatalf("Projects: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("got %d projects, want 1", len(projects))
	}
	if projects[0].ID != storetest.ProjectID.String() {
		t.Errorf("project id = %q, want %q", projects[0].ID, storetest.ProjectID)
	}
	if len(projects[0].Repositories) == 0 {
		t.Error("the project has no repositories on the wire")
	}

	tasks, err := caller.Tasks(context.Background())
	if err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("got %d tasks, want 1", len(tasks))
	}
}

func TestUnknownProjectIsNotFound(t *testing.T) {
	live := serve(t, Options{})

	_, err := live.client(t).Project(context.Background(), "absent")
	if err == nil {
		t.Fatal("a project that is not registered was returned")
	}
	if !notFound(err) {
		t.Errorf("error = %v, want a 404", err)
	}
}

// TestHealthDegradesWhenStateCannotBeRead pins the choice that health answers
// even when part of the state is unreadable. A client asking whether the daemon
// is alive learns more from "running, but the state directory cannot be listed"
// than from a failed request.
func TestHealthDegradesWhenStateCannotBeRead(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores directory permissions, so there is nothing to make unreadable")
	}

	live := serve(t, Options{})
	seed(t, live, storetest.Project(), nil)

	projects := filepath.Join(live.layout.State, "projects")
	if err := os.Chmod(projects, 0o000); err != nil {
		t.Fatalf("making the state directory unreadable: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(projects, 0o700) })

	health, err := live.client(t).Health(context.Background())
	if err != nil {
		t.Fatalf("Health failed instead of degrading: %v", err)
	}

	if health.Status != api.StatusDegraded {
		t.Errorf("status = %q, want %q", health.Status, api.StatusDegraded)
	}
	if health.Detail == "" {
		t.Error("a degraded daemon does not say what is wrong")
	}
	if health.Daemon.PID != os.Getpid() {
		t.Errorf("a degraded health report lost the daemon's identity: pid = %d", health.Daemon.PID)
	}
}

// notFound and badRequest read the status the daemon returned.
func notFound(err error) bool { return hasStatus(err, http.StatusNotFound) }

func badRequest(err error) bool { return hasStatus(err, http.StatusBadRequest) }

func hasStatus(err error, want int) bool {
	var status *client.StatusError
	return errors.As(err, &status) && status.Status == want
}
