// Package runtimetest supplies a fake Docker for application-runtime tests.
//
// It exists so that the orchestration around a task's services — a start that
// fails on a taken port, a service that exits non-zero, a destroy that must
// retain volumes, two tasks acting on their own projects — runs in the default
// `go test ./...` rather than only on a machine with Docker. Those branches
// decide whether a half-finished lifecycle is recoverable and whether one task
// can disturb another, which is exactly what should not depend on the tester's
// machine.
//
// It is the application runtime's fake. internal/execution/compose/composetest
// is the agent environment's, and the two are separate for the same reason the
// adapters are.
//
// Test support only; no production code imports it.
package runtimetest

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/ma8el/feat/internal/runtime"
)

// Docker is a fake Docker CLI.
//
// Commands are matched on their whole argument vector with the Compose project
// flags removed, so a test arranges "up --detach api" rather than repeating the
// project name, the files, and the environment files that every invocation
// carries.
type Docker struct {
	mu sync.Mutex
	// responses are keyed by the shortened command.
	responses map[string]runtime.Output
	// errors are commands that could not be started at all.
	errors map[string]error
	// missing marks executables this host does not have.
	missing map[string]bool
	// hooks run just before a command is answered.
	hooks map[string]func()
	// calls records every invocation in order, shortened the same way.
	calls []string
	// full records every invocation verbatim, which is what a test pinning an
	// argument vector reads.
	full [][]string
}

// New returns a fake Docker that answers plausibly for one service named api: a
// recent Compose, a service that starts and runs, and no other resources.
func New() *Docker {
	d := &Docker{
		responses: make(map[string]runtime.Output),
		errors:    make(map[string]error),
		missing:   make(map[string]bool),
		hooks:     make(map[string]func()),
	}
	return d.
		Answer("version --short", "5.1.4").
		Answer("up --detach api", "").
		Answer("create api", "").
		Answer("stop api", "").
		Answer("down", "").
		Answer("ps --all --format json api", Container("api", "c0ffee", "running", "Up 2 seconds")).
		Answer("network ls --filter label=com.docker.compose.project=feat-app-11111111 --format {{.Name}}", "").
		Answer("volume ls --filter label=com.docker.compose.project=feat-app-11111111 --format {{.Name}}", "").
		Answer("inspect --type container --format {{json .Mounts}} c0ffee", `[]`)
}

// Container renders one line of `docker compose ps --format json`.
//
// It is here rather than in each test because the field names are Compose's and
// a test that spelled them itself would pass while production read something
// else.
func Container(service, id, state, status string) string {
	return fmt.Sprintf(
		`{"ID":%q,"Name":"feat-%s-1","Service":%q,"State":%q,"Status":%q,"Health":"","ExitCode":0}`,
		id, service, service, state, status)
}

// Answer arranges the standard output of one command.
func (d *Docker) Answer(command, stdout string) *Docker {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.responses[command] = runtime.Output{Stdout: stdout}
	return d
}

// Fail arranges a command that runs and exits non-zero.
func (d *Docker) Fail(command, stderr string, code int) *Docker {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.responses[command] = runtime.Output{Stderr: stderr, ExitCode: code}
	return d
}

// Reply arranges both streams and an exit code together.
//
// It exists because which stream a tool reports on is sometimes the thing under
// test: Docker Compose narrates progress on one stream and fails on the other,
// and an implementation that reads only one passes every test that cannot tell
// them apart (ADR-033 evidence 10).
func (d *Docker) Reply(command, stdout, stderr string, code int) *Docker {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.responses[command] = runtime.Output{Stdout: stdout, Stderr: stderr, ExitCode: code}
	return d
}

// Refuse arranges a command that could not be started at all.
func (d *Docker) Refuse(command string, err error) *Docker {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.errors[command] = err
	return d
}

// Missing marks an executable as absent from this host.
func (d *Docker) Missing(name string) *Docker {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.missing[name] = true
	return d
}

// Before arranges a function to run just before a command is answered.
//
// It exists for the tests that check ordering rather than outcome — that a
// record naming a resource reaches disk before the resource exists — which can
// only be observed from inside the operation that creates it.
func (d *Docker) Before(command string, hook func()) *Docker {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.hooks[command] = hook
	return d
}

// Look resolves an executable unless it was marked missing.
func (d *Docker) Look(name string) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.missing[name] {
		return "", fmt.Errorf("%w: %s", runtime.ErrNotInstalled, name)
	}
	return "/usr/local/bin/" + name, nil
}

// Run answers an arranged command, and reports an unarranged one rather than
// inventing an answer: a fake that succeeds by default hides the call a test
// meant to make.
func (d *Docker) Run(_ context.Context, invocation runtime.Invocation) (runtime.Output, error) {
	key := Shorten(invocation.Arguments)

	d.mu.Lock()
	d.calls = append(d.calls, key)
	d.full = append(d.full, invocation.Arguments)
	hook := d.hooks[key]
	d.mu.Unlock()

	// Outside the lock: a hook observes the system under test, which may call
	// back into this fake.
	if hook != nil {
		hook()
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if err, arranged := d.errors[key]; arranged {
		return runtime.Output{}, err
	}
	if output, arranged := d.responses[key]; arranged {
		return output, nil
	}
	return runtime.Output{
		ExitCode: 127,
		Stderr:   fmt.Sprintf("runtimetest: no answer is arranged for %q", key),
	}, nil
}

// Calls returns every command run, shortened.
func (d *Docker) Calls() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.calls...)
}

// Ran reports whether a command was run.
func (d *Docker) Ran(command string) bool {
	for _, call := range d.Calls() {
		if call == command {
			return true
		}
	}
	return false
}

// Vectors returns every verbatim argument vector, which is what a test asserting
// that one task never touched another's project reads.
func (d *Docker) Vectors() [][]string {
	d.mu.Lock()
	defer d.mu.Unlock()
	vectors := make([][]string, 0, len(d.full))
	for _, vector := range d.full {
		vectors = append(vectors, append([]string(nil), vector...))
	}
	return vectors
}

// Vector returns the verbatim argument vector of the first call matching the
// shortened command, which is what a test pinning flags reads.
func (d *Docker) Vector(command string) ([]string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i, call := range d.calls {
		if call == command {
			return append([]string(nil), d.full[i]...), true
		}
	}
	return nil, false
}

// Shorten removes the flags every invocation carries, so a test names the
// command it cares about.
func Shorten(arguments []string) string {
	var kept []string
	for i := 0; i < len(arguments); i++ {
		switch arguments[i] {
		case "compose":
			continue
		case "--project-name", "--project-directory", "--file", "--env-file":
			i++
			continue
		}
		kept = append(kept, arguments[i])
	}
	return strings.Join(kept, " ")
}
