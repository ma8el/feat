package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ma8el/feat/internal/control"
	"github.com/ma8el/feat/internal/domain"
)

// Adapter is one coding-agent provider.
//
// The four methods are the contract in docs/06-technical-architecture.md. None
// of them exposes a provider type: an adapter receives resolved values and
// returns normalized ones, so that adding a provider changes no caller and a
// future external plugin protocol stays possible (ADR-024).
type Adapter interface {
	// ID is the provider identifier recorded on the agent session.
	ID() string
	// Validate reports whether the environment can run this provider. It is
	// called before anything is created, so that a task whose agent could never
	// start does not leave a terminal and a session record behind.
	Validate(ctx context.Context, env Environment) error
	// Prepare generates whatever the provider needs outside the repositories
	// and returns how to launch it. It writes only into the host-only area of
	// the control workspace it is given.
	Prepare(ctx context.Context, req PrepareRequest) (LaunchSpec, error)
	// ParseEvent normalizes one provider-native control message. It reports
	// false when the message carries a provider event this build does not act
	// on, which is not an error: a provider may emit more than Feat models.
	ParseEvent(ctx context.Context, message control.Message) (Event, bool, error)
}

// Environment describes where an agent will run and how to ask it questions.
//
// Host-native execution fills it for the trusted host, and a devcontainer fills
// the same structure for a Compose service. That is the whole reason validation
// takes an environment rather than reaching for the host itself: a check that
// ran in the wrong place answers a question nobody asked.
type Environment struct {
	// Mode is where the agent runs.
	Mode domain.ExecutionMode
	// OutsideConfiguredBoundary reports that the agent will run somewhere other
	// than where the project configured it. It exists so that a message can say
	// so rather than implying a boundary that is not there.
	OutsideConfiguredBoundary bool
	// Runner executes probe commands in that environment. It is required.
	Runner Runner
}

// Runner runs one command inside an agent execution environment.
//
// It is an interface for the reason git.Runner and tmux.Runner are: a test can
// arrange an unauthenticated CLI, a missing executable, or a hanging probe
// without needing a machine that has one.
type Runner interface {
	// Run executes the command and returns what it produced. A command that
	// runs and fails is not an error; a command that could not be started is.
	Run(ctx context.Context, command Command) (Output, error)
}

// Command is one probe executed in an agent environment.
//
// It is an argument vector rather than a string, so nothing is ever handed to a
// shell to re-split (CLAUDE.md architectural rules).
type Command struct {
	// Program is the executable.
	Program string
	// Arguments are its arguments, each already a single vector element.
	Arguments []string
	// Directory is where it runs. An empty value means the environment's
	// default.
	Directory string
}

// Output is what a probe produced.
type Output struct {
	// Stdout and Stderr are the captured streams.
	Stdout string
	Stderr string
	// ExitCode is the process exit status.
	ExitCode int
}

// Succeeded reports whether the probe exited cleanly.
func (o Output) Succeeded() bool { return o.ExitCode == 0 }

// PrepareRequest is everything an adapter needs to generate a launch.
type PrepareRequest struct {
	// Task is the confirmed task. Its shape is frozen, so the brief here is the
	// brief the user accepted.
	Task *domain.Task
	// Workspace says how the agent will see its own filesystem.
	Workspace Workspace
	// Control is the host-side control workspace. An adapter writes generated
	// files into its host-only area and nowhere else.
	Control *control.Workspace
	// Environment is where the agent will run.
	Environment Environment
	// Gate says whether a completion gate will answer this task's review
	// requests.
	Gate Gate
	// Publication says what publishing this task would cover, so that an
	// adapter asks the agent for a draft only where there is somewhere to
	// publish.
	Publication Publication
	// Resume is the provider's own identifier for a session to continue, empty
	// for an ordinary launch.
	//
	// It is neutral on purpose: what continuing a session means is the
	// provider's, and an adapter whose agent cannot resume one is free to
	// refuse. What an adapter must not do is quietly start a new session
	// instead — a resumed session that lost its history looks identical from
	// the outside, which is the failure mode ADR-032's evidence 4 describes
	// (ADR-037).
	Resume string
}

// Gate describes the completion gate an adapter has to tell the agent about.
//
// It is neutral on purpose. What a provider does with it is the provider's:
// Claude's helper waits for the verdict and exits non-zero when a check failed,
// so that the failure arrives as a failed tool call the model reads and carries
// on from (ADR-036). A provider whose native loop offers something better can
// use this same description differently.
type Gate struct {
	// Configured reports that the project has checks the gate will run for this
	// task. When it is false a review request is recorded and answered at once,
	// and the agent is told that a human decides from here.
	Configured bool
	// Acknowledge is how long the agent waits for the daemon to say it has the
	// request at all. A daemon that is not running must not leave a session
	// waiting for an hour.
	Acknowledge time.Duration
	// Verdict is how long the agent then waits for the answer. It is the gate's
	// own bound plus enough slack that a gate which finished is never missed by
	// the thing waiting for it.
	Verdict time.Duration
	// Describe is a sentence for the generated instructions, saying what will
	// run. It names the checks so that an agent knows what it is waiting for.
	Describe string
}

// Publication describes what publishing this task's work would cover.
//
// It is neutral on purpose, and it carries repositories rather than forges: an
// adapter asks the agent for words, and which forge those words are bound for
// is the daemon's to know. A task whose project configures no forge gets an
// empty value, and the agent is never told about a document it has nowhere to
// send (ADR-070).
type Publication struct {
	// Repositories are the identifiers of the repositories a publication would
	// open a merge request for, in the order the task binds them. An empty list
	// means this task publishes nowhere.
	Repositories []string
}

// Configured reports whether a publication has anywhere to go.
func (p Publication) Configured() bool { return len(p.Repositories) > 0 }

// Workspace tells an adapter how the agent will see its own filesystem.
//
// Every path here is expressed in the agent's terms rather than the host's.
// Under host-native execution the two are the same; with the agent in a
// container they are not, and the adapter is written against this structure so
// that it never has to know which case it is in.
type Workspace struct {
	// WorkingDirectory is where the agent starts, as the agent sees it. It is
	// the task's primary worktree.
	WorkingDirectory string
	// ControlPath is the control workspace, as the agent sees it.
	ControlPath string
}

// Validate reports whether the workspace is usable.
func (w Workspace) Validate() error {
	for _, field := range []struct{ name, value string }{
		{"working directory", w.WorkingDirectory},
		{"control path", w.ControlPath},
	} {
		if field.value == "" {
			return fmt.Errorf("the agent %s must not be empty", field.name)
		}
		if !strings.HasPrefix(field.value, "/") {
			return fmt.Errorf("the agent %s must be absolute, but is %q", field.name, field.value)
		}
	}
	return nil
}

// LaunchSpec is how to start an agent, expressed in the terms its own
// environment uses.
//
// The daemon turns it into something a terminal can run: by using the values
// directly on the host, or by wrapping them in a command that enters the
// configured container. Neither is the adapter's business.
type LaunchSpec struct {
	// Program is the agent executable.
	Program string
	// Arguments are its arguments.
	Arguments []string
	// Directory is the working directory, as the agent sees it.
	Directory string
	// Environment is the additional environment, as KEY=VALUE, that the agent
	// process needs. It never carries a secret.
	Environment []string
}

// Validate reports whether the specification can be launched.
func (s LaunchSpec) Validate() error {
	if s.Program == "" {
		return fmt.Errorf("the agent launch specification names no program")
	}
	if !strings.HasPrefix(s.Directory, "/") {
		return fmt.Errorf("the agent working directory must be absolute, but is %q", s.Directory)
	}
	for _, value := range s.Environment {
		if !strings.Contains(value, "=") {
			return fmt.Errorf("the agent environment entry %q is not KEY=VALUE", value)
		}
	}
	return nil
}
