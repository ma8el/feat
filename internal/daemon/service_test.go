package daemon

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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

// TestATaskIsAddressableByTheKeyEveryListPrints is the slice 13 acceptance
// criterion, driven through the socket.
//
// The daemon reads every project to answer it, because a key is unique within a
// project rather than globally (ADR-026) and the question is asked of the
// machine. Registering a second project is therefore the arrangement, not an
// edge case.
func TestATaskIsAddressableByTheKeyEveryListPrints(t *testing.T) {
	live := serve(t, Options{})
	seed(t, live, storetest.Project(), storetest.Task())
	seed(t, live, otherProject(t), otherTask(t, "b41c9e07-2d3f-4a5b-8c6d-7e8f9a0b1c2d"))

	for _, ref := range []string{
		storetest.TaskID.Key().String(),
		storetest.TaskID.String(),
		"7f3a",
	} {
		task, err := live.client(t).Task(context.Background(), ref)
		if err != nil {
			t.Errorf("Task(%q): %v", ref, err)
			continue
		}
		if task.ID != storetest.TaskID.String() {
			t.Errorf("Task(%q) is %q, want %q", ref, task.ID, storetest.TaskID)
		}
		if task.ProjectID != storetest.ProjectID.String() {
			t.Errorf("Task(%q) belongs to %q, want %q", ref, task.ProjectID, storetest.ProjectID)
		}
	}

	// The other project's task answers to its own key, so the test cannot pass
	// against a daemon that resolves everything to the first project it reads.
	task, err := live.client(t).Task(context.Background(), "b41c9e07")
	if err != nil {
		t.Fatalf("Task on the second project: %v", err)
	}
	if task.ProjectID == storetest.ProjectID.String() {
		t.Errorf("the second project's task resolved to %q", task.ProjectID)
	}
}

// TestAnAmbiguousKeyIsReportedRatherThanResolved checks what happens when two
// projects hold tasks whose keys share a prefix.
//
// Reporting it is the same rule ADR-029 applied to a colliding branch name: a
// user acting on a task Feat picked would be acting on something they did not
// choose, and `feat cleanup` is one of the commands that takes a task.
func TestAnAmbiguousKeyIsReportedRatherThanResolved(t *testing.T) {
	live := serve(t, Options{})
	seed(t, live, storetest.Project(), storetest.Task())
	seed(t, live, otherProject(t), otherTask(t, "7fb90cd4-1e2f-4a3b-8c4d-5e6f7a8b9c0d"))

	_, err := live.client(t).Task(context.Background(), "7f")

	if err == nil {
		t.Fatal("an ambiguous reference resolved to a task")
	}
	if !badRequest(err) {
		t.Errorf("error = %v, want a 400", err)
	}
	for _, want := range []string{"7f3a1c2e", "7fb90cd4", storetest.ProjectID.String(), "other"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
}

// TestARefusedTaskReferenceSaysWhereToFindOne checks the half of this defect that
// is not about resolution at all.
//
// The old rejection explained the format of an identifier to somebody who had no
// way of seeing one, which is the part that made the addressing gap unescapable
// rather than merely inconvenient.
func TestARefusedTaskReferenceSaysWhereToFindOne(t *testing.T) {
	live := serve(t, Options{})
	seed(t, live, storetest.Project(), storetest.Task())

	for _, ref := range []string{"deadbeef", "not-a-uuid"} {
		_, err := live.client(t).Task(context.Background(), ref)
		if err == nil {
			t.Errorf("Task(%q) succeeded", ref)
			continue
		}
		if !strings.Contains(err.Error(), "feat task list") {
			t.Errorf("Task(%q) does not say where a valid value is printed: %v", ref, err)
		}
	}
}

// otherProject is a second registered project, so that resolution is exercised
// across the whole machine rather than within one project.
func otherProject(t *testing.T) *domain.Project {
	t.Helper()

	project, err := domain.NewProject("other", "Other", "app", []domain.Repository{{
		ID:            "app",
		Name:          "App",
		HostPath:      "/srv/repositories/app",
		DefaultBranch: "main",
		Remote:        "origin",
		DefaultAccess: domain.DefaultAccessReadWrite,
	}}, storetest.Origin)
	if err != nil {
		t.Fatalf("creating the second project: %v", err)
	}
	return project
}

// otherTask is a draft in that project. A draft is enough: resolution is about
// naming a task, and a draft is a task (ADR-031).
func otherTask(t *testing.T, id domain.TaskID) *domain.Task {
	t.Helper()

	task, err := domain.NewTask(id, "other", "another task",
		domain.TaskSource{Kind: domain.SourcePrompt}, storetest.Origin)
	if err != nil {
		t.Fatalf("creating a task in the second project: %v", err)
	}
	return task
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
