package ui

import (
	"context"
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
		Allocations: []api.PortAllocation{{
			Service: "api", ContainerPort: 8000, HostPort: 21000,
			// The project's configured bind address, filled in because the
			// repository's own Compose file named none.
			Protocol: "tcp", HostIP: "127.0.0.1", Address: "localhost:21000",
		}},
		Ports:   []api.Port{{Service: "api", ContainerPort: 8000, HostPort: 21000, HostIP: "127.0.0.1"}},
		Volumes: []string{"feat-example-7f3a1c2e_pgdata"},
	}
}

// press sends one key to the model and returns what it became, running whatever
// command it produced so that a fake backend's answer is applied.
func press(t *testing.T, model Model, key string) Model {
	t.Helper()

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	return applyCommand(t, updated.(Model), cmd)
}

// applyCommand runs one command and applies what it produced, following a batch
// into the commands it holds as Bubble Tea's own loop does.
//
// A screen that opens onto a request now returns that request batched with the
// loading indicator's first frame, and a helper that applied only the outer
// message would report that the request was never made.
func applyCommand(t *testing.T, model Model, cmd tea.Cmd) Model {
	t.Helper()

	message := run(cmd)
	if message == nil {
		return model
	}
	if batch, ok := message.(tea.BatchMsg); ok {
		for _, command := range batch {
			model = applyCommand(t, model, command)
		}
		return model
	}
	updated, _ := model.Update(message)
	return updated.(Model)
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
		"8000 → localhost:21000",       // how to reach the application
		"feat-example-7f3a1c2e_pgdata", // retained by every destroy
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

// TestTheScreenSaysWhatAPortIsBoundOnAndNotOnlyWhereToDialIt is the dashboard's
// half of the same rule the CLI follows.
//
// The address of a port on every interface is localhost, exactly as it is for a
// port on the loopback address, so a screen printing the address alone shows a
// service open to every network this machine is joined to as one that answers
// here. Both answers are shown: the address to dial, and the binding.
func TestTheScreenSaysWhatAPortIsBoundOnAndNotOnlyWhereToDialIt(t *testing.T) {
	task := liveTask()
	task.Runtime = runningRuntime()
	// The repository's own Compose file named this address, so Feat kept it,
	// whatever the project's runtime.bind_address says.
	task.Runtime.Allocations = append(task.Runtime.Allocations, api.PortAllocation{
		Service: "web", ContainerPort: 3000, HostPort: 21001,
		Protocol: "tcp", HostIP: "0.0.0.0", Address: "localhost:21001",
	})

	backend := newFakeBackend()
	backend.runtimeStatus = api.RuntimeStatus{Task: task}

	view := content(runtimeScreen(t, backend, task))
	for _, required := range []string{
		"8000 → localhost:21000",
		"bound on 127.0.0.1",
		"3000 → localhost:21001",
		"bound on 0.0.0.0",
		"every network this machine is joined to",
	} {
		if !strings.Contains(view, required) {
			t.Errorf("the runtime screen does not show %q:\n%s", required, view)
		}
	}

	// And a task whose ports all answer on this machine alone is not given the
	// sentence about a binding it does not have.
	confined := liveTask()
	confined.Runtime = runningRuntime()
	backend = newFakeBackend()
	backend.runtimeStatus = api.RuntimeStatus{Task: confined}

	view = content(runtimeScreen(t, backend, confined))
	if !strings.Contains(view, "bound on 127.0.0.1") {
		t.Errorf("a confined publication does not say what it is bound on:\n%s", view)
	}
	if strings.Contains(view, "every network this machine is joined to") {
		t.Errorf("a runtime whose ports answer on this machine alone was told about a wide binding:\n%s", view)
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

// TestASecondActionWaitsForTheFirst keeps a slow start from being asked for
// twice.
//
// A first start pulls the project's images and runs its builds, so the screen
// says "waiting for start…" for minutes rather than for a moment (ADR-034
// evidence 14). Every key press during that wait used to be another request, and
// what a user pressing `u` twice would be asking for is the services started and
// then started again.
func TestASecondActionWaitsForTheFirst(t *testing.T) {
	task := liveTask()
	task.Runtime = runningRuntime()
	backend := newFakeBackend()
	backend.runtimeStatus = api.RuntimeStatus{Task: task}

	model := runtimeScreen(t, backend, task)
	// A start that has not answered yet, which is what the screen holds while
	// Docker is working.
	model.runtime.pending = api.RuntimeStart
	asked := len(backend.runtimeCalls)

	for _, key := range []string{"u", "c", "t", "r", "d"} {
		updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		next, ok := updated.(Model)
		if !ok {
			t.Fatalf("pressing %q produced a %T", key, updated)
		}
		if cmd != nil {
			cmd()
		}
		if len(backend.runtimeCalls) != asked {
			t.Fatalf("pressing %q during a start asked for something else: %v", key, backend.runtimeCalls)
		}
		if next.runtime.confirming {
			t.Errorf("pressing %q during a start opened the destroy confirmation", key)
		}
		if !strings.Contains(next.status, "waiting for start") {
			t.Errorf("pressing %q during a start says nothing about why: %q", key, next.status)
		}
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

	// Read as words rather than as lines: the panel is wrapped to the region it
	// is drawn in, and where a sentence breaks is the layout's business.
	detail := content(model)
	if !strings.Contains(flowed(detail), "press t to stop") {
		t.Errorf("the task detail does not offer to stop the runtime:\n%s", detail)
	}
	if !strings.Contains(flowed(detail), "never stops them for you") {
		t.Errorf("the task detail does not say that Feat leaves it running:\n%s", detail)
	}

	screen := runtimeScreen(t, backend, task)
	if !strings.Contains(flowed(content(screen)), "press t to stop") {
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

// TestTheDashboardOutlivesTheInterruptThatLeavesTheLogs is the other half of
// yielding the terminal.
//
// `docker compose logs --follow` ends when the user interrupts it, and the
// terminal driver sends that interrupt to every process in the foreground group
// — the dashboard included. While it holds the process-wide interrupt context,
// the dashboard is killed by the key that leaves the logs, which left no way out
// of them but quitting Feat. Its lifetime is its own, and Bubble Tea ends it:
// that is the one component that knows whether the dashboard or another program
// currently owns the terminal (ADR-049).
func TestTheDashboardOutlivesTheInterruptThatLeavesTheLogs(t *testing.T) {
	interrupted, interrupt := context.WithCancel(context.Background())
	dashboard := dashboardContext(interrupted)

	interrupt()

	if dashboard.Err() != nil {
		t.Fatalf("the interrupt that leaves the logs also ends the dashboard: %v", dashboard.Err())
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
