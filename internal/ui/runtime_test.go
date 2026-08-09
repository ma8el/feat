package ui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ma8el/feat/internal/api"
)

// runningRuntime is a task whose application services are up.
func runningRuntime() *api.Runtime {
	return &api.Runtime{
		Provider: "compose",
		Identity: "feat-example-7f3a1c2e",
		Services: []string{"api", "worker"},
		State:    "running",
		Health:   "unknown",
		Ports:    []api.Port{{Service: "api", ContainerPort: 8000, HostPort: 8080}},
		Volumes:  []string{"feat-example-7f3a1c2e_pgdata"},
		External: []api.ExternalResource{
			{ID: "staging_db", Kind: "postgres", Lifecycle: "external", Selector: "7f3a1c2e"},
		},
	}
}

// press sends one key to the model and returns what it became, running whatever
// command it produced so that a fake backend's answer is applied.
func press(t *testing.T, model Model, key string) Model {
	t.Helper()

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	next := updated.(Model)
	if cmd == nil {
		return next
	}
	if message := cmd(); message != nil {
		applied, _ := next.Update(message)
		return applied.(Model)
	}
	return next
}

// runtimeScreen opens the runtime screen for the fixture task.
func runtimeScreen(t *testing.T, backend *fakeBackend, task api.Task) Model {
	t.Helper()

	model := dashboard(backend, task)
	return press(t, model, "R")
}

// TestTheRuntimeScreenShowsWhatTheTaskOwns keeps every resource a user would
// have to clean up in front of them.
func TestTheRuntimeScreenShowsWhatTheTaskOwns(t *testing.T) {
	task := liveTask()
	task.Runtime = runningRuntime()

	backend := newFakeBackend()
	backend.runtimeStatus = api.RuntimeStatus{
		Task: task,
		Services: []api.RuntimeService{
			{Name: "api", Container: "c0ffee", State: "running", Status: "Up 2 minutes",
				Health: "unknown", Managed: true},
			{Name: "worker", State: "exited", Status: "Exited (0) 1 minute ago",
				Health: "unknown", Managed: true},
		},
	}

	view := content(runtimeScreen(t, backend, task))

	for _, required := range []string{
		"feat-example-7f3a1c2e", // the Compose project an action reaches
		"api",                   // the services, one per line
		"worker",
		"Exited (0) 1 minute ago",      // including the one that is not running
		"8000 → 8080",                  // how to reach the application
		"feat-example-7f3a1c2e_pgdata", // retained by every destroy
		"never created or destroyed",   // and what Feat will not touch at all
		"staging_db",
	} {
		if !strings.Contains(view, required) {
			t.Errorf("the runtime screen does not show %q:\n%s", required, view)
		}
	}
}

// TestTheScreenShowsWhatComposeStartedAlongTheWay keeps a container of the task
// visible whether or not the project named the service.
//
// Compose starts what a configured service depends on, and Feat stops and
// removes those with the rest, so the screen shows them and says where they came
// from.
func TestTheScreenShowsWhatComposeStartedAlongTheWay(t *testing.T) {
	task := liveTask()
	task.Runtime = runningRuntime()

	backend := newFakeBackend()
	backend.runtimeStatus = api.RuntimeStatus{
		Task: task,
		Services: []api.RuntimeService{
			{Name: "api", Container: "c0ffee", State: "running", Status: "Up 2 minutes",
				Health: "unknown", Managed: true},
			{Name: "postgres", Container: "cafe", State: "running", Status: "Up 12 minutes",
				Health: "healthy"},
		},
	}

	view := content(runtimeScreen(t, backend, task))
	for _, required := range []string{"postgres", "dependency", "configured", "removes it with the rest"} {
		if !strings.Contains(view, required) {
			t.Errorf("the runtime screen does not show %q:\n%s", required, view)
		}
	}
}

// TestOpeningTheRuntimeScreenStartsNothing is FR-RUN-005 at the dashboard.
//
// Looking at what is running must not run anything: the screen asks for the
// status and nothing else, whatever a user was hoping to see.
func TestOpeningTheRuntimeScreenStartsNothing(t *testing.T) {
	task := liveTask()
	backend := newFakeBackend()
	backend.runtimeStatus = api.RuntimeStatus{Task: task}

	runtimeScreen(t, backend, task)

	if len(backend.runtimeCalls) != 1 {
		t.Fatalf("opening the screen made %d requests: %v", len(backend.runtimeCalls), backend.runtimeCalls)
	}
	if got := backend.runtimeCalls[0]; !strings.HasPrefix(got, "status ") {
		t.Errorf("opening the screen asked for %q, want a status", got)
	}
}

// TestEachRuntimeKeyAsksForItsOwnAction pins what the screen does with a key.
func TestEachRuntimeKeyAsksForItsOwnAction(t *testing.T) {
	for key, want := range map[string]string{
		"c": "create",
		"u": "start",
		"t": "stop",
		"r": "status",
	} {
		t.Run(want, func(t *testing.T) {
			task := liveTask()
			task.Runtime = runningRuntime()
			backend := newFakeBackend()
			backend.runtimeStatus = api.RuntimeStatus{Task: task}

			model := runtimeScreen(t, backend, task)
			press(t, model, key)

			last := backend.runtimeCalls[len(backend.runtimeCalls)-1]
			if !strings.HasPrefix(last, want+" ") {
				t.Errorf("pressing %q asked for %q, want %s", key, last, want)
			}
		})
	}
}

// TestDestroyingAsksFirst keeps a removal behind a confirmation.
//
// The point is checked at the backend rather than at the screen: what matters is
// not that a prompt appeared but that nothing was destroyed while it was up.
func TestDestroyingAsksFirst(t *testing.T) {
	task := liveTask()
	task.Runtime = runningRuntime()
	backend := newFakeBackend()
	backend.runtimeStatus = api.RuntimeStatus{Task: task}

	model := runtimeScreen(t, backend, task)
	asked := press(t, model, "d")

	if !strings.Contains(content(asked), "Volumes are retained") {
		t.Errorf("the confirmation does not say what is retained:\n%s", content(asked))
	}
	for _, call := range backend.runtimeCalls {
		if strings.HasPrefix(call, "destroy") {
			t.Fatalf("the runtime was destroyed while the confirmation was still up: %v", backend.runtimeCalls)
		}
	}

	// Anything other than yes leaves everything alone.
	declined := press(t, asked, "n")
	for _, call := range backend.runtimeCalls {
		if strings.HasPrefix(call, "destroy") {
			t.Fatalf("declining destroyed the runtime anyway: %v", backend.runtimeCalls)
		}
	}

	// And yes does it.
	press(t, press(t, declined, "d"), "y")
	if !confirmedDestroy(backend.runtimeCalls) {
		t.Fatalf("confirming did not destroy the runtime: %v", backend.runtimeCalls)
	}
}

func confirmedDestroy(calls []string) bool {
	for _, call := range calls {
		if strings.HasPrefix(call, "destroy ") {
			return true
		}
	}
	return false
}

// TestApprovalOffersToStopTheRuntimeWithoutStopping is the fifth acceptance
// criterion at the dashboard.
//
// An approved task whose services are still running is offered the stop, in
// words, on both the screens a user reads after approving. What the dashboard
// must never do is take the offer itself.
func TestApprovalOffersToStopTheRuntimeWithoutStopping(t *testing.T) {
	task := liveTask()
	task.Workflow = "approved"
	task.Runtime = runningRuntime()

	backend := newFakeBackend()
	backend.runtimeStatus = api.RuntimeStatus{Task: task}

	model := dashboard(backend, task)
	model.selected = task.ID
	model.screen = screenTask

	detail := content(model)
	if !strings.Contains(detail, "press t to stop") {
		t.Errorf("the task detail does not offer to stop the runtime:\n%s", detail)
	}
	if !strings.Contains(detail, "never stops them for you") {
		t.Errorf("the task detail does not say that Feat leaves it running:\n%s", detail)
	}

	screen := runtimeScreen(t, backend, task)
	if !strings.Contains(content(screen), "press t to stop") {
		t.Errorf("the runtime screen does not offer to stop the runtime:\n%s", content(screen))
	}

	// Rendering both screens asked for a status and nothing else.
	for _, call := range backend.runtimeCalls {
		if !strings.HasPrefix(call, "status ") {
			t.Fatalf("approval reached the runtime: %v", backend.runtimeCalls)
		}
	}
}

// TestAStoppedRuntimeIsNotOfferedAStop keeps the offer from becoming noise.
func TestAStoppedRuntimeIsNotOfferedAStop(t *testing.T) {
	task := liveTask()
	task.Workflow = "approved"
	task.Runtime = runningRuntime()
	task.Runtime.State = "stopped"

	if offer := approvalOffer(task); offer != "" {
		t.Errorf("a stopped runtime was offered a stop: %s", offer)
	}
}

// TestAFailedActionIsShownRatherThanThrown keeps one refused start from taking
// the view of every other task with it.
func TestAFailedActionIsShownRatherThanThrown(t *testing.T) {
	task := liveTask()
	backend := newFakeBackend()
	backend.runtimeErr = errors.New("host port 8080 is already taken")

	model := runtimeScreen(t, backend, task)
	view := content(model)

	if !strings.Contains(view, "8080") {
		t.Errorf("the failure is not shown on the screen:\n%s", view)
	}
	if model.quitting {
		t.Error("a refused action quit the dashboard")
	}
}

// TestTheLogsActionYieldsTheTerminal checks that the screen opens the ordinary
// Compose logs rather than rendering something Feat collected.
func TestTheLogsActionYieldsTheTerminal(t *testing.T) {
	task := liveTask()
	task.Runtime = runningRuntime()
	backend := newFakeBackend()
	backend.runtimeStatus = api.RuntimeStatus{Task: task}

	model := runtimeScreen(t, backend, task)
	press(t, model, "o")

	if len(backend.logs) != 1 || backend.logs[0] != task.ID {
		t.Fatalf("the logs action asked for %v, want the open task", backend.logs)
	}
}

// TestADraftReachesTheRuntimeScreenAndIsToldItHasNone.
//
// The screen used to refuse a draft outright. That was right about a draft
// having no services and wrong about what to do: a tab that declines to open is
// a tab the cycle cannot pass. It opens and says so, and still asks the daemon
// nothing — there is nothing to observe.
func TestADraftReachesTheRuntimeScreenAndIsToldItHasNone(t *testing.T) {
	draft := liveTask()
	draft.Workflow = "draft"
	draft.Session = nil

	backend := newFakeBackend()
	model := dashboard(backend, draft)
	opened := press(t, model, "R")

	if opened.screen != screenRuntime {
		t.Fatalf("R left the dashboard on %v", opened.screen)
	}
	if !strings.Contains(opened.runtimeBody(), "still a draft") {
		t.Errorf("the runtime screen does not say why it is empty:\n%s", opened.runtimeBody())
	}
	if len(backend.runtimeCalls) != 0 {
		t.Errorf("a draft reached the runtime: %v", backend.runtimeCalls)
	}
}
