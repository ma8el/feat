package claude

import (
	"context"
	"fmt"
	"regexp"

	"github.com/ma8el/feat/internal/agent"
	"github.com/ma8el/feat/internal/control"
)

// Provider is the adapter identifier recorded on an agent session.
const Provider = "claude"

// Executable is the Claude Code command.
//
// It is a constant rather than a configured value: a project that could name
// the agent's executable would be a project that could name a program the
// daemon starts on its owner's behalf, and the agent provider is already
// declared by agent.provider.
const Executable = "claude"

// Adapter implements agent.Adapter for Claude Code.
//
// Every Claude-specific decision lives in this package: the flags, the settings
// document, the hook event names, and the shape of what a hook writes. The
// daemon sees normalized events and a launch specification, so a second
// provider changes nothing outside its own package.
type Adapter struct{}

var _ agent.Adapter = Adapter{}

// New returns the Claude adapter.
func New() Adapter { return Adapter{} }

// ID returns the provider identifier.
func (Adapter) ID() string { return Provider }

// Prepare generates the settings, hooks, and helper the session needs, writes
// the task brief, and returns how to launch the native interactive CLI.
//
// Everything generated goes into the host-only area of the control workspace,
// which is outside every repository: a checked-in CLAUDE.md and a project's own
// settings keep applying, because Feat adds a settings file rather than
// replacing the user's (FR-AGENT-003, docs/06-technical-architecture.md).
func (a Adapter) Prepare(_ context.Context, req agent.PrepareRequest) (agent.LaunchSpec, error) {
	if req.Task == nil {
		return agent.LaunchSpec{}, fmt.Errorf("preparing a Claude session needs a task")
	}
	if req.Control == nil {
		return agent.LaunchSpec{}, fmt.Errorf("preparing a Claude session needs a control workspace")
	}
	if err := req.Workspace.Validate(); err != nil {
		return agent.LaunchSpec{}, err
	}
	if req.Task.Brief == "" {
		return agent.LaunchSpec{}, fmt.Errorf("task %s has no brief, and the brief is what the session starts from", req.Task.ID)
	}

	// The brief first. It is what the agent reads, and the launch below names
	// its path, so a launch that started before it existed would point the agent
	// at a file that is not there.
	if err := req.Control.WriteBrief(req.Task.Brief); err != nil {
		return agent.LaunchSpec{}, err
	}

	generated, err := a.generate(req)
	if err != nil {
		return agent.LaunchSpec{}, err
	}

	arguments := []string{
		// --add-dir is variadic, so it must never be the last flag before
		// the prompt: it would swallow the prompt as a second directory and
		// the session would start with no task at all. Another flag after it
		// ends the list. This ordering is load-bearing and is pinned by a
		// test.
		//
		// It exists because the control workspace is outside the working
		// directory, so without it the session's first act is to ask
		// permission to read the brief Feat wrote for it — on every task
		// launch. A permission dialog nobody needed teaches a user to click
		// through the ones that matter. It grants tool access to one
		// directory Feat generated for this task and widens nothing else.
		"--add-dir", req.Workspace.ControlPath,
		// The generated settings are added to the user's own rather than
		// replacing them, and --setting-sources is deliberately not narrowed:
		// the project's checked-in configuration is part of how the user
		// works and Feat has no business switching it off.
		"--settings", generated.settingsPath,
		"--append-system-prompt-file", generated.instructionsPath,
	}

	if req.Resume != "" {
		// --resume takes an optional value, so it has the same hazard --add-dir
		// has and one more: given no value it opens an interactive picker. The
		// identifier is therefore always passed explicitly, and the flag is
		// never the last thing before a positional argument.
		if err := checkSessionID(req.Resume); err != nil {
			return agent.LaunchSpec{}, err
		}
		arguments = append(arguments, "--resume", req.Resume)
		// No prompt. A resumed session already holds the conversation this task
		// has had, and a prompt invented here would be Feat putting words in the
		// user's mouth. The session comes back where it was, and what happens
		// next is theirs (ADR-037).
	} else {
		arguments = append(arguments, generated.initialPrompt)
	}

	spec := agent.LaunchSpec{
		Program:   Executable,
		Arguments: arguments,
		Directory: req.Workspace.WorkingDirectory,
	}
	if err := spec.Validate(); err != nil {
		return agent.LaunchSpec{}, err
	}
	return spec, nil
}

// sessionIDPattern is what Claude Code's own session identifiers look like.
//
// It is checked rather than trusted because the value reaches an argument
// vector: it comes from a provider message, and a message an agent could write
// is not a value to pass through unexamined (docs/05-security-model.md, control
// workspace validation).
var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

// checkSessionID refuses a recorded identifier that is not one.
func checkSessionID(id string) error {
	if !sessionIDPattern.MatchString(id) {
		return fmt.Errorf("%q is not a Claude session identifier Feat will pass to the CLI", id)
	}
	return nil
}

// ParseEvent normalizes one control message into an agent event.
//
// A provider event carries Claude's own hook payload, which only this package
// reads. An agent-authored message carries a document this package defines for
// the purpose, so that the agent has one documented way to say "this is ready".
func (a Adapter) ParseEvent(_ context.Context, message control.Message) (agent.Event, bool, error) {
	switch message.Type {
	case control.TypeProviderEvent:
		return parseHook(message)
	case control.TypeReviewRequested:
		return parseReport(message, agent.KindReviewRequested)
	case control.TypeCompletionReport:
		return parseReport(message, agent.KindCompletionReport)
	case control.TypeOpenQuestion:
		return parseQuestion(message)
	case control.TypeRuntimeRequested:
		// A runtime request never reaches an adapter: the protocol refuses it
		// for want of a capability before anything is asked to interpret it.
		return agent.Event{}, false, nil
	default:
		return agent.Event{}, false, nil
	}
}
