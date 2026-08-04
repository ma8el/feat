package domain

import "time"

// AgentSession is the one native coding-agent session a task owns.
//
// The provider is an adapter identifier rather than an enumeration, so that
// adding an adapter does not change the domain. Nothing here is specific to any
// one agent.
type AgentSession struct {
	// Provider identifies the agent adapter that owns the session.
	Provider string
	// ExecutionMode is where the session runs.
	ExecutionMode ExecutionMode
	// Tmux locates the session's terminal. It is an execution reference, never
	// task identity (invariant 10).
	Tmux TmuxTarget
	// ProviderSessionID is the agent's own session identifier when it exposes
	// one, so a reconnect can be correlated with provider-side state.
	ProviderSessionID string
	// Process is the last observed process state.
	Process ProcessState
	// ControlPath is the absolute host path of the task control workspace.
	ControlPath string
	// LastEventSequence is the sequence number of the last control event Feat
	// processed, so a replay after a restart neither repeats nor skips one.
	LastEventSequence uint64
	// CreatedAt is when the session was launched.
	CreatedAt time.Time
	// LastActivityAt is when the session last produced an event.
	LastActivityAt time.Time
}

// TmuxTarget locates a session's terminal on the Feat-owned tmux server.
//
// The fields hold tmux identifiers rather than indexes or display names,
// because a user may renumber or rename windows at any time (FR-TMUX-004).
type TmuxTarget struct {
	// Socket is the dedicated tmux server socket Feat manages.
	Socket string
	// Session is the tmux session identifier for the project.
	Session string
	// Window is the tmux window identifier for the task.
	Window string
	// Pane is the tmux pane identifier running the agent.
	Pane string
}

// NewAgentSession creates a session in the starting process state.
func NewAgentSession(provider string, mode ExecutionMode, target TmuxTarget, controlPath string, now time.Time) (*AgentSession, error) {
	session := &AgentSession{
		Provider:       provider,
		ExecutionMode:  mode,
		Tmux:           target,
		Process:        ProcessStarting,
		ControlPath:    controlPath,
		CreatedAt:      normalizeTime(now),
		LastActivityAt: normalizeTime(now),
	}
	if err := session.Validate(""); err != nil {
		return nil, err
	}
	return session, nil
}

// Observe records the process state the execution backend reported.
//
// Process state is an observation, so any documented value may follow any
// other: a daemon that restarts finds a session already running, and a session
// may fail from any state. Reaching idle in particular says nothing about the
// task's workflow state (invariant 13).
func (s *AgentSession) Observe(state ProcessState, now time.Time) error {
	if !state.Valid() {
		return &ValidationError{
			Entity: "agent session",
			Field:  "process",
			Reason: "must be a documented process state, but is " + quote(string(state)),
		}
	}
	s.Process = state
	s.LastActivityAt = normalizeTime(now)
	return nil
}

// RecordEvent advances the last processed control-event sequence.
//
// Sequences are required to increase, which is what makes replaying a control
// outbox idempotent: an event Feat already applied can be recognised and
// skipped rather than applied twice.
func (s *AgentSession) RecordEvent(sequence uint64, now time.Time) error {
	if sequence <= s.LastEventSequence {
		return &ValidationError{
			Entity: "agent session",
			Field:  "last_event_sequence",
			Reason: "must increase, but " + formatUint(sequence) + " does not follow " + formatUint(s.LastEventSequence),
		}
	}
	s.LastEventSequence = sequence
	s.LastActivityAt = normalizeTime(now)
	return nil
}

// Validate reports whether the session is internally consistent. The task
// identifier is used only to make the message name the task; it may be empty
// when the session is not attached yet.
func (s *AgentSession) Validate(task TaskID) error {
	id := task.String()
	if s.Provider == "" {
		return &ValidationError{Entity: "agent session", ID: id, Field: "provider", Reason: "must not be empty"}
	}
	if !s.ExecutionMode.Valid() {
		return &ValidationError{
			Entity: "agent session",
			ID:     id,
			Field:  "execution_mode",
			Reason: "must be host or devcontainer, but is " + quote(string(s.ExecutionMode)),
		}
	}
	if !s.Process.Valid() {
		return &ValidationError{
			Entity: "agent session",
			ID:     id,
			Field:  "process",
			Reason: "must be a documented process state, but is " + quote(string(s.Process)),
		}
	}
	if s.ControlPath != "" && !isAbsPath(s.ControlPath) {
		return &ValidationError{
			Entity: "agent session",
			ID:     id,
			Field:  "control_path",
			Reason: "must be absolute, but is " + quote(s.ControlPath),
		}
	}
	if s.CreatedAt.IsZero() {
		return &ValidationError{Entity: "agent session", ID: id, Field: "created_at", Reason: "must be set"}
	}
	if s.LastActivityAt.Before(s.CreatedAt) {
		return &ValidationError{
			Entity: "agent session",
			ID:     id,
			Field:  "last_activity_at",
			Reason: "must not precede created_at",
		}
	}
	return nil
}
