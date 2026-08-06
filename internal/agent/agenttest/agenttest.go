// Package agenttest provides a fake agent execution environment.
//
// It exists so that the branches that decide whether a launch is refused — a
// missing executable, an unauthenticated provider CLI, a probe that fails — can
// be tested without a machine that has each of those states. It is test support
// only and is never linked into a running daemon.
package agenttest

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/ma8el/feat/internal/agent"
)

// Runner is a fake agent.Runner.
//
// Responses are keyed by the whole command line, so a test states what a
// specific probe answers rather than what any probe answers. An unanswered
// probe reports the executable as absent, which is the honest default: an
// environment a test did not describe does not have the tool.
type Runner struct {
	mu sync.Mutex
	// responses maps a command line to what running it produces.
	responses map[string]agent.Output
	// failures maps a command line to an error starting it.
	failures map[string]error
	// calls records every command in order.
	calls []agent.Command
}

var _ agent.Runner = (*Runner)(nil)

// New returns a runner that knows about nothing.
func New() *Runner {
	return &Runner{
		responses: make(map[string]agent.Output),
		failures:  make(map[string]error),
	}
}

// Answer makes one command line produce the given output.
func (r *Runner) Answer(output agent.Output, program string, arguments ...string) *Runner {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.responses[key(program, arguments)] = output
	return r
}

// Succeed makes one command line exit cleanly with the given standard output.
func (r *Runner) Succeed(stdout, program string, arguments ...string) *Runner {
	return r.Answer(agent.Output{Stdout: stdout}, program, arguments...)
}

// Fail makes one command line exit with the given status and standard error,
// which is what an unauthenticated provider CLI does.
func (r *Runner) Fail(code int, stderr, program string, arguments ...string) *Runner {
	return r.Answer(agent.Output{Stderr: stderr, ExitCode: code}, program, arguments...)
}

// Absent makes one program report itself as not installed.
func (r *Runner) Absent(program string, arguments ...string) *Runner {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.failures[key(program, arguments)] = fmt.Errorf("%w: %s", agent.ErrNotInstalled, program)
	return r
}

// Run answers a probe.
func (r *Runner) Run(_ context.Context, command agent.Command) (agent.Output, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.calls = append(r.calls, command)
	line := key(command.Program, command.Arguments)
	if err, ok := r.failures[line]; ok {
		return agent.Output{}, err
	}
	if output, ok := r.responses[line]; ok {
		return output, nil
	}
	return agent.Output{}, fmt.Errorf("%w: %s", agent.ErrNotInstalled, command.Program)
}

// Calls returns every command the runner was asked to execute, in order.
func (r *Runner) Calls() []agent.Command {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]agent.Command(nil), r.calls...)
}

// Ran reports whether a command line was executed.
func (r *Runner) Ran(program string, arguments ...string) bool {
	line := key(program, arguments)
	for _, call := range r.Calls() {
		if key(call.Program, call.Arguments) == line {
			return true
		}
	}
	return false
}

func key(program string, arguments []string) string {
	return strings.Join(append([]string{program}, arguments...), "\x00")
}
