package compose_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/runtime"
	"github.com/ma8el/feat/internal/runtime/compose"
	"github.com/ma8el/feat/internal/runtime/compose/runtimetest"
)

// observed returns the state the adapter reads from a given `ps` answer.
func observed(t *testing.T, services []string, answer string) runtime.State {
	t.Helper()

	docker := runtimetest.New().
		Answer("ps --all --format json "+strings.Join(services, " "), answer).
		Answer("network ls --filter label=com.docker.compose.project="+identity+" --format {{.Name}}", "").
		Answer("volume ls --filter label=com.docker.compose.project="+identity+" --format {{.Name}}", "")

	_, spec := arrange(t, docker)
	spec.Services = services
	target, err := compose.New(spec, compose.Options{Runner: docker})
	if err != nil {
		t.Fatalf("building the runtime: %v", err)
	}

	state, err := target.Observe(context.Background())
	if err != nil {
		t.Fatalf("observing: %v", err)
	}
	return state
}

// TestWhatTheContainersSayIsWhatTheRuntimeIs pins the table that turns observed
// containers into a runtime state.
//
// It is a table for the reason ADR-026 made the workflow transitions one and
// ADR-032 the agent events: what "degraded" means is a product decision, and a
// product decision should be readable as itself rather than reconstructed from
// the order of a few conditions. The row that matters most is the last: a
// container with no health check is running with health unknown, never healthy.
func TestWhatTheContainersSayIsWhatTheRuntimeIs(t *testing.T) {
	for name, testCase := range map[string]struct {
		services  []string
		answer    string
		lifecycle domain.RuntimeState
		health    domain.HealthState
	}{
		"nothing exists": {
			services:  []string{"api"},
			answer:    "",
			lifecycle: domain.RuntimeAbsent,
			health:    domain.HealthUnknown,
		},
		"created and never started": {
			services:  []string{"api"},
			answer:    runtimetest.Container("api", "c1", "created", "Created"),
			lifecycle: domain.RuntimeStopped,
			health:    domain.HealthUnknown,
		},
		"running with no health check": {
			services:  []string{"api"},
			answer:    runtimetest.Container("api", "c1", "running", "Up 2 seconds"),
			lifecycle: domain.RuntimeRunning,
			health:    domain.HealthUnknown,
		},
		"running and healthy": {
			services: []string{"api"},
			answer: `{"ID":"c1","Service":"api","State":"running","Status":"Up 2 minutes (healthy)",` +
				`"Health":"healthy"}`,
			lifecycle: domain.RuntimeRunning,
			health:    domain.HealthHealthy,
		},
		"running and unhealthy": {
			services: []string{"api"},
			answer: `{"ID":"c1","Service":"api","State":"running","Status":"Up 2 minutes (unhealthy)",` +
				`"Health":"unhealthy"}`,
			lifecycle: domain.RuntimeDegraded,
			health:    domain.HealthUnhealthy,
		},
		"health check still settling": {
			services: []string{"api"},
			answer: `{"ID":"c1","Service":"api","State":"running","Status":"Up 2 seconds (health: starting)",` +
				`"Health":"starting"}`,
			lifecycle: domain.RuntimeRunning,
			health:    domain.HealthStarting,
		},
		"one service up and one missing": {
			services:  []string{"api", "worker"},
			answer:    runtimetest.Container("api", "c1", "running", "Up 2 seconds"),
			lifecycle: domain.RuntimeDegraded,
			health:    domain.HealthUnknown,
		},
		"stopped cleanly": {
			services:  []string{"api"},
			answer:    `{"ID":"c1","Service":"api","State":"exited","Status":"Exited (0) 3 seconds ago"}`,
			lifecycle: domain.RuntimeStopped,
			health:    domain.HealthUnknown,
		},
		"exited with a failure": {
			services: []string{"api"},
			answer: `{"ID":"c1","Service":"api","State":"exited","Status":"Exited (1) 3 seconds ago",` +
				`"ExitCode":1}`,
			lifecycle: domain.RuntimeFailed,
			health:    domain.HealthUnknown,
		},
		// The ordinary stop. `docker compose stop` sends SIGTERM and kills a
		// container that does not exit, and a service running as PID 1 has no
		// default handler for it — so the service the user just stopped exits
		// 137. Reading that as a failure would report every stop as one, which
		// real Docker demonstrated and no fixture would have.
		"stopped by the signal a stop sends": {
			services: []string{"api"},
			answer: `{"ID":"c1","Service":"api","State":"exited","Status":"Exited (137) 1 second ago",` +
				`"ExitCode":137}`,
			lifecycle: domain.RuntimeStopped,
			health:    domain.HealthUnknown,
		},
		"stopped by SIGTERM": {
			services: []string{"api"},
			answer: `{"ID":"c1","Service":"api","State":"exited","Status":"Exited (143) 1 second ago",` +
				`"ExitCode":143}`,
			lifecycle: domain.RuntimeStopped,
			health:    domain.HealthUnknown,
		},
		"on its way up": {
			services:  []string{"api"},
			answer:    `{"ID":"c1","Service":"api","State":"restarting","Status":"Restarting (1)"}`,
			lifecycle: domain.RuntimeStarting,
			health:    domain.HealthUnknown,
		},
		"healthy beside something with no health check": {
			services: []string{"api", "worker"},
			answer: `{"ID":"c1","Service":"api","State":"running","Status":"Up","Health":"healthy"}` + "\n" +
				`{"ID":"c2","Service":"worker","State":"running","Status":"Up"}`,
			lifecycle: domain.RuntimeRunning,
			health:    domain.HealthUnknown,
		},
	} {
		t.Run(name, func(t *testing.T) {
			state := observed(t, testCase.services, testCase.answer)

			if state.Lifecycle != testCase.lifecycle {
				t.Errorf("lifecycle is %q, want %q", state.Lifecycle, testCase.lifecycle)
			}
			if state.Health != testCase.health {
				t.Errorf("health is %q, want %q", state.Health, testCase.health)
			}
			if len(state.Services) != len(testCase.services) {
				t.Errorf("%d services were reported, want %d: a service with no container is still a "+
					"service the runtime is missing", len(state.Services), len(testCase.services))
			}
		})
	}
}

// TestComposeIsReadInBothOfItsShapes covers the two things Docker Compose has
// printed for `ps --format json` across its versions.
//
// Assuming one would report every task's services as absent, and Feat would then
// tell a user their running application had stopped.
func TestComposeIsReadInBothOfItsShapes(t *testing.T) {
	array := `[{"ID":"c1","Service":"api","State":"running","Status":"Up"},` +
		`{"ID":"c2","Service":"worker","State":"running","Status":"Up"}]`
	lines := `{"ID":"c1","Service":"api","State":"running","Status":"Up"}` + "\n" +
		`{"ID":"c2","Service":"worker","State":"running","Status":"Up"}`

	for name, answer := range map[string]string{"a JSON array": array, "one object per line": lines} {
		t.Run(name, func(t *testing.T) {
			state := observed(t, []string{"api", "worker"}, answer)
			if state.Lifecycle != domain.RuntimeRunning {
				t.Fatalf("lifecycle is %q, want running", state.Lifecycle)
			}
		})
	}
}

// TestPublishedPortsAreReportedFromTheRunningContainers keeps the ports a user
// needs in order to reach their application in front of them.
func TestPublishedPortsAreReportedFromTheRunningContainers(t *testing.T) {
	state := observed(t, []string{"api"},
		`{"ID":"c1","Service":"api","State":"running","Status":"Up","Publishers":[`+
			`{"TargetPort":8000,"PublishedPort":8080,"Protocol":"tcp"},`+
			`{"TargetPort":9000,"PublishedPort":0,"Protocol":"tcp"}]}`)

	if len(state.Ports) != 1 {
		t.Fatalf("%d ports were reported, want 1: a port nothing published is not an assignment", len(state.Ports))
	}
	port := state.Ports[0]
	if port.Service != "api" || port.ContainerPort != 8000 || port.HostPort != 8080 {
		t.Errorf("the reported port is %+v, want api 8000 published on 8080", port)
	}
}

// TestObservingStartsNothing is FR-STATE-004 at the adapter: a stopped service
// is reported as stopped, and observing it never brings it back.
func TestObservingStartsNothing(t *testing.T) {
	docker := runtimetest.New().
		Answer("ps --all --format json api", `{"ID":"c1","Service":"api","State":"exited","Status":"Exited (0)"}`).
		Answer("network ls --filter label=com.docker.compose.project="+identity+" --format {{.Name}}", "").
		Answer("volume ls --filter label=com.docker.compose.project="+identity+" --format {{.Name}}", "")
	services, _ := arrange(t, docker)

	state, err := services.Observe(context.Background())
	if err != nil {
		t.Fatalf("observing: %v", err)
	}
	if state.Lifecycle != domain.RuntimeStopped {
		t.Errorf("lifecycle is %q, want stopped", state.Lifecycle)
	}
	for _, call := range docker.Calls() {
		if strings.HasPrefix(call, "up") || strings.HasPrefix(call, "create") || strings.HasPrefix(call, "start") {
			t.Errorf("observing ran %q, and observing must never start anything", call)
		}
	}
}

// TestAnAbsentRuntimeCostsOneCommand keeps the poll cheap for the ordinary case.
//
// Most tasks have no services running most of the time. Asking Docker for the
// networks and volumes of a project that has nothing would be two more commands
// per task per poll to confirm an absence.
func TestAnAbsentRuntimeCostsOneCommand(t *testing.T) {
	docker := runtimetest.New().Answer("ps --all --format json api", "")
	services, _ := arrange(t, docker)

	if _, err := services.Observe(context.Background()); err != nil {
		t.Fatalf("observing: %v", err)
	}
	if calls := docker.Calls(); len(calls) != 1 {
		t.Errorf("observing an absent runtime ran %d commands, want 1: %v", len(calls), calls)
	}
}
