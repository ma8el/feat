package compose

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/runtime"
)

// composeProjectLabel is the label Docker Compose puts on every resource it
// creates for a project.
//
// Feat asks Docker for the networks and volumes carrying this task's project
// name rather than rendering the Compose configuration, because
// `docker compose config` resolves the project including the values of its
// environment files, and those must never be read (ADR-028, ADR-034).
const composeProjectLabel = "com.docker.compose.project"

// Observe reports what the runtime looks like now.
//
// It starts nothing. A stopped service is reported as stopped and stays stopped,
// which is what FR-STATE-004 requires of every observation Feat makes.
func (r *Runtime) Observe(ctx context.Context) (runtime.State, error) {
	output, err := r.runner.Run(ctx, r.invoke(r.services("ps", "--all", "--format", "json")...))
	if err != nil {
		return runtime.State{}, err
	}
	if !output.Succeeded() {
		return runtime.State{}, fmt.Errorf("asking Compose project %s for its state failed: %s",
			r.spec.Identity, firstLine(output.Stderr, output.Stdout))
	}

	containers, err := parseContainers(output.Stdout)
	if err != nil {
		return runtime.State{}, fmt.Errorf("reading the state of Compose project %s: %w", r.spec.Identity, err)
	}

	state := aggregate(r.spec.Services, containers)
	if !state.Present {
		// Nothing exists, so there is nothing labelled with this project either.
		// Two Docker calls to confirm an absence would be two calls per poll for
		// every task whose services are not running.
		return state, nil
	}

	networks, err := r.resources(ctx, "network")
	if err != nil {
		return runtime.State{}, err
	}
	volumes, err := r.resources(ctx, "volume")
	if err != nil {
		return runtime.State{}, err
	}
	state.Networks, state.Volumes = networks, volumes
	return state, nil
}

// resources lists the networks or volumes Compose labelled with this project.
func (r *Runtime) resources(ctx context.Context, kind string) ([]string, error) {
	output, err := r.runner.Run(ctx, runtime.Invocation{
		Program: r.docker,
		Arguments: []string{
			kind, "ls",
			"--filter", "label=" + composeProjectLabel + "=" + r.spec.Identity,
			"--format", "{{.Name}}",
		},
	})
	if err != nil {
		return nil, err
	}
	if !output.Succeeded() {
		return nil, fmt.Errorf("listing the %ss of Compose project %s failed: %s",
			kind, r.spec.Identity, firstLine(output.Stderr, output.Stdout))
	}

	var names []string
	for line := range strings.SplitSeq(output.Stdout, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			names = append(names, trimmed)
		}
	}
	return names, nil
}

// container is one entry of `docker compose ps --format json`.
type container struct {
	ID        string      `json:"ID"`
	Name      string      `json:"Name"`
	Service   string      `json:"Service"`
	State     string      `json:"State"`
	Health    string      `json:"Health"`
	Status    string      `json:"Status"`
	ExitCode  int         `json:"ExitCode"`
	Publisher []publisher `json:"Publishers"`
}

// publisher is one published port of one container.
type publisher struct {
	TargetPort    int    `json:"TargetPort"`
	PublishedPort int    `json:"PublishedPort"`
	Protocol      string `json:"Protocol"`
}

// parseContainers reads what Compose printed.
//
// Compose has printed both a JSON array and newline-delimited objects across its
// versions, so both are accepted rather than one being assumed. Guessing wrong
// would report every task's services as absent, and Feat would then tell a user
// their running application had stopped.
func parseContainers(output string) ([]container, error) {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return nil, nil
	}

	if strings.HasPrefix(trimmed, "[") {
		var containers []container
		if err := json.Unmarshal([]byte(trimmed), &containers); err != nil {
			return nil, err
		}
		return containers, nil
	}

	var containers []container
	for line := range strings.SplitSeq(trimmed, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var one container
		if err := json.Unmarshal([]byte(line), &one); err != nil {
			return nil, err
		}
		containers = append(containers, one)
	}
	return containers, nil
}

// aggregate turns the observed containers into one runtime state.
//
// The mapping is a table rather than a chain of conditions, in the shape ADR-026
// used for the workflow transitions and ADR-032 for agent events: what a runtime
// state means is a product decision, and a product decision should be readable
// as itself rather than reconstructed from the order of a few if statements.
//
// The services are the configured ones, in configured order, so a service that
// has no container at all is reported as such rather than omitted — a runtime
// missing half its services is not a runtime that is running.
func aggregate(services []string, containers []container) runtime.State {
	observed := make(map[string]container, len(containers))
	for _, one := range containers {
		// A service with several containers is a scaled service, which v0 does
		// not configure. The first one wins and the rest are counted through
		// their own service name, so nothing is silently dropped.
		if _, seen := observed[one.Service]; !seen {
			observed[one.Service] = one
		}
	}

	state := runtime.State{Lifecycle: domain.RuntimeAbsent, Health: domain.HealthUnknown}
	var counts tally

	for _, service := range services {
		one, found := observed[service]
		if !found {
			state.Services = append(state.Services, runtime.ServiceState{
				Name: service, Health: domain.HealthUnknown,
			})
			counts.missing++
			continue
		}

		health := healthOf(one)
		state.Services = append(state.Services, runtime.ServiceState{
			Name:      service,
			Container: one.ID,
			State:     one.State,
			Status:    one.Status,
			Health:    health,
			ExitCode:  one.ExitCode,
		})
		state.Present = true
		counts.add(one, health)

		for _, published := range one.Publisher {
			if published.PublishedPort == 0 {
				continue
			}
			state.Ports = append(state.Ports, domain.PortAssignment{
				Service:       service,
				ContainerPort: published.TargetPort,
				HostPort:      published.PublishedPort,
			})
		}
	}

	if !state.Present {
		return state
	}
	state.Lifecycle, state.Health = counts.resolve()
	return state
}

// tally counts the observed containers by the thing each count decides.
type tally struct {
	missing    int
	running    int
	created    int
	restarting int
	exited     int
	failed     int
	other      int

	healthy   int
	unhealthy int
	starting  int
	unknown   int
}

// add records one observed container.
func (t *tally) add(one container, health domain.HealthState) {
	switch strings.ToLower(one.State) {
	case "running":
		t.running++
	case "created":
		t.created++
	case "restarting":
		t.restarting++
	case "paused", "removing", "dead":
		t.other++
	case "exited":
		if stoppedByASignal(one.ExitCode) {
			t.exited++
		} else {
			t.failed++
		}
	default:
		if strings.Contains(strings.ToLower(one.Status), "exited (0)") {
			// Some Compose versions report the state only inside the status
			// line. An exit that succeeded is a stop, not a failure.
			t.exited++
			return
		}
		t.other++
	}

	switch health {
	case domain.HealthHealthy:
		t.healthy++
	case domain.HealthUnhealthy:
		t.unhealthy++
	case domain.HealthStarting:
		t.starting++
	default:
		t.unknown++
	}
}

// resolve maps the counts onto the documented runtime and health states.
//
// The order of the cases is the table, and each row says what a user is looking
// at:
//
//   - anything unhealthy, or up beside something that is not, is degraded: the
//     application is partly there, which is neither running nor stopped;
//   - a container that exited non-zero is a failure, and one that exited cleanly
//     or was never started is stopped. Feat never restarts either;
//   - health is separate from all of it, and without a health check the honest
//     answer is unknown rather than healthy (FR-RUN-007).
func (t tally) resolve() (domain.RuntimeState, domain.HealthState) {
	health := domain.HealthUnknown
	switch {
	case t.unhealthy > 0:
		health = domain.HealthUnhealthy
	case t.starting > 0:
		health = domain.HealthStarting
	case t.healthy > 0 && t.unknown == 0:
		health = domain.HealthHealthy
	}

	up := t.running > 0
	down := t.missing + t.created + t.exited + t.failed + t.other

	switch {
	case t.restarting > 0 && !up:
		return domain.RuntimeStarting, health
	case up && (down > 0 || t.unhealthy > 0 || t.restarting > 0):
		return domain.RuntimeDegraded, health
	case up:
		return domain.RuntimeRunning, health
	case t.failed > 0:
		return domain.RuntimeFailed, health
	case t.other > 0:
		return domain.RuntimeDegraded, health
	default:
		// Created and never started, or stopped cleanly. Both are a runtime whose
		// containers exist and are not running, and Feat leaves them that way.
		return domain.RuntimeStopped, health
	}
}

// stoppedByASignal reports whether an exit status is how a container ends when
// somebody stops it.
//
// This distinction was found by running the real thing rather than by reasoning
// about it. `docker compose stop` sends SIGTERM and kills the container when it
// does not exit, and a process running as PID 1 has no default signal handlers —
// so an ordinary `sleep infinity` service exits 137, and the obvious rule
// "non-zero means failed" reports every stop the user asked for as a failure.
// That is worse than saying nothing: a state that cries wolf on the ordinary
// path is a state people learn to ignore.
//
// The shell's convention is 128 plus the signal number, and the three that end a
// container on purpose are SIGINT, SIGKILL, and SIGTERM. Anything else non-zero
// is the application exiting on its own, which is a failure and is reported as
// one.
func stoppedByASignal(code int) bool {
	switch code {
	case 0, 130, 137, 143:
		return true
	default:
		return false
	}
}

// healthOf maps Compose's health onto the domain's.
//
// A service with no health check reports nothing, which is unknown rather than
// healthy: docs/02-user-workflows.md requires a container without a health check
// to be shown as running with health unknown.
func healthOf(c container) domain.HealthState {
	switch strings.ToLower(c.Health) {
	case "healthy":
		return domain.HealthHealthy
	case "unhealthy":
		return domain.HealthUnhealthy
	case "starting":
		return domain.HealthStarting
	default:
		return domain.HealthUnknown
	}
}
