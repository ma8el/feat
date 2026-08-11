// Package composetest supplies a fake Docker for tests.
//
// It exists so that the orchestration around a devcontainer — a service that
// refuses to start, a container that exits at once, a probe that reports root,
// a mount that should never have been there — runs in the default `go test
// ./...` rather than only on a machine with Docker. Those branches decide
// whether a half-finished launch is recoverable, which is exactly what should
// not depend on the tester's machine.
//
// Test support only; no production code imports it.
package composetest

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/ma8el/feat/internal/execution"
)

// Docker is a fake Docker CLI.
//
// Commands are matched on their whole argument vector with the Compose project
// flags removed, so a test arranges "up --detach dev" rather than repeating the
// files and project name that every invocation carries.
type Docker struct {
	mu sync.Mutex
	// responses are keyed by the shortened command.
	responses map[string]execution.Output
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

// New returns a fake Docker that answers plausibly: a recent Compose, a service
// that starts and runs, and a container with no unexpected mounts.
func New() *Docker {
	d := &Docker{
		responses: make(map[string]execution.Output),
		errors:    make(map[string]error),
		missing:   make(map[string]bool),
		hooks:     make(map[string]func()),
	}
	return d.
		Answer("version --short", "5.1.4").
		Answer("up --detach dev", "").
		Answer("ps --all --format json dev",
			`{"ID":"c0ffee","Name":"feat-dev-1","Service":"dev","State":"running","Status":"Up 2 seconds"}`).
		Answer("inspect --type container --format {{json .Mounts}} c0ffee", `[]`).
		// An environment with nothing pointing at a container daemon, which is
		// the other observation a launch refuses over.
		Answer("inspect --type container --format {{json .Config.Env}} c0ffee", `["PATH=/usr/bin"]`)
}

// Answer arranges the standard output of one command.
func (d *Docker) Answer(command, stdout string) *Docker {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.responses[command] = execution.Output{Stdout: stdout}
	return d
}

// Fail arranges a command that runs and exits non-zero.
func (d *Docker) Fail(command, stderr string, code int) *Docker {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.responses[command] = execution.Output{Stderr: stderr, ExitCode: code}
	return d
}

// Reply arranges both streams and an exit code together.
//
// It exists because which stream a tool reports on is sometimes the thing under
// test: Docker Compose announces an absent executable on standard output, and an
// implementation that reads only standard error passes every test that cannot
// tell the two apart.
func (d *Docker) Reply(command, stdout, stderr string, code int) *Docker {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.responses[command] = execution.Output{Stdout: stdout, Stderr: stderr, ExitCode: code}
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
		return "", fmt.Errorf("%s is not installed", name)
	}
	return "/usr/local/bin/" + name, nil
}

// Run answers an arranged command, and reports an unarranged one rather than
// inventing an answer: a fake that succeeds by default hides the call a test
// meant to make.
func (d *Docker) Run(_ context.Context, invocation execution.Invocation) (execution.Output, error) {
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
		return execution.Output{}, err
	}
	if output, arranged := d.responses[key]; arranged {
		return output, nil
	}
	return execution.Output{
		ExitCode: 127,
		Stderr:   fmt.Sprintf("composetest: no answer is arranged for %q", key),
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

// Shorten removes the Compose project flags every invocation carries, so a test
// names the command it cares about.
func Shorten(arguments []string) string {
	var kept []string
	for i := 0; i < len(arguments); i++ {
		switch arguments[i] {
		case "compose":
			continue
		case "--project-name", "--project-directory", "--file":
			i++
			continue
		}
		kept = append(kept, arguments[i])
	}
	return strings.Join(kept, " ")
}
