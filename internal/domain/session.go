package domain

import (
	"regexp"
	"time"
)

var (
	tmuxSessionIDPattern = regexp.MustCompile(`^\$[0-9]+$`)
	tmuxWindowIDPattern  = regexp.MustCompile(`^@[0-9]+$`)
	tmuxPaneIDPattern    = regexp.MustCompile(`^%[0-9]+$`)
)

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
	// Execution records the environment the session runs in, when that is not
	// this machine. It is nil for host execution, where the environment is the
	// host and there is nothing to identify.
	Execution *ExecutionEnvironment
	// LastEventSequence is the sequence number of the last control event Feat
	// processed, so a replay after a restart neither repeats nor skips one.
	LastEventSequence uint64
	// TurnEndedAt is the turn end an idle grace period is still counting from.
	// It is set exactly when the provider has reported a turn ending and nothing
	// has been observed about the process since.
	//
	// It is recorded because the grace period that decides what an ended turn
	// means is held by a timer in memory, while the message that reported the
	// turn end is settled in the outbox as soon as it is applied. Without this
	// field a daemon stopping inside the grace period loses the transition for
	// good: the only other path into idle is a fresh end-of-turn event, and a
	// session that has finished speaking sends no more (ADR-096).
	TurnEndedAt time.Time
	// CreatedAt is when the session was launched.
	CreatedAt time.Time
	// LastActivityAt is when the session last produced an event.
	LastActivityAt time.Time
}

// ExecutionEnvironment is the isolated environment one agent session runs in.
//
// It is a separate concept from RuntimeEnvironment even when both are Compose
// projects: this is how the agent runs, and that is the application the user
// tests. Keeping them apart is what lets Feat manage Docker on the host without
// ever giving the agent Docker access.
//
// The first group of fields is what Feat asked for, recorded so that cleanup and
// reconciliation resolve what a task owns rather than recomputing it from
// configuration that may since have changed. The second group is what Feat saw,
// and is never assumed from the first.
type ExecutionEnvironment struct {
	// Provider identifies the execution adapter, such as the Compose adapter.
	Provider string
	// Identity is the unique environment identity, which is the Compose project
	// name for the Compose adapter. It is what makes an action affect one
	// task's container and no other's.
	Identity string
	// Files are the configured inputs defining the environment, in order.
	Files []string
	// GeneratedOverridePath is the override Feat generated for this task. It
	// carries mounts and generated non-secret variables, never copied secret
	// values.
	GeneratedOverridePath string
	// Service is the service the agent runs in.
	Service string
	// User is the user the agent runs as, which must not be root.
	User string

	// Container is the observed container, empty when nothing was observed.
	Container string
	// Running is whether the environment was observed to be up.
	Running bool
	// Status is what the environment itself called its state, kept verbatim so
	// the dashboard can quote the tool rather than paraphrase it.
	Status string
	// Health is the observed health, which is separate from running.
	Health HealthState
	// ObservedAt is when the last four fields were established.
	ObservedAt time.Time
}

// Validate reports whether the execution environment is internally consistent.
func (e *ExecutionEnvironment) Validate(task TaskID) error {
	id := task.String()
	for _, field := range []struct{ name, value string }{
		{"provider", e.Provider},
		{"identity", e.Identity},
		{"service", e.Service},
		{"user", e.User},
	} {
		if field.value == "" {
			return &ValidationError{Entity: "execution environment", ID: id, Field: field.name,
				Reason: "must not be empty"}
		}
	}
	if isRootUser(e.User) {
		// The security model requires a non-root agent, and a record saying
		// otherwise would be a record of a rule being broken (invariant: the
		// agent runs as the configured non-root user).
		return &InvariantError{Entity: "execution environment", ID: id,
			Rule:   "the agent runs as a non-root user",
			Reason: "the recorded user is " + quote(e.User)}
	}
	if e.Health != "" && !e.Health.Valid() {
		return &ValidationError{Entity: "execution environment", ID: id, Field: "health",
			Reason: "must be a documented health state, but is " + quote(string(e.Health))}
	}
	if e.GeneratedOverridePath != "" && !isAbsPath(e.GeneratedOverridePath) {
		return &ValidationError{Entity: "execution environment", ID: id, Field: "generated_override_path",
			Reason: "must be absolute, but is " + quote(e.GeneratedOverridePath)}
	}
	return nil
}

// Observe records what an execution adapter saw.
//
// Every value here is an observation. A stopped environment found during
// recovery is reported as stopped and never restarted (FR-STATE-004).
func (e *ExecutionEnvironment) Observe(container string, running bool, status string, health HealthState, now time.Time) {
	e.Container = container
	e.Running = running
	e.Status = status
	e.Health = health
	e.ObservedAt = normalizeTime(now)
}

// isRootUser reports whether a container user is the superuser.
func isRootUser(user string) bool {
	return user == "root" || user == "0" || user == "0:0"
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

// Validate reports whether the target contains tmux's immutable object IDs.
// Display names and numeric indexes are deliberately not accepted as stored
// identity (FR-TMUX-004).
func (t TmuxTarget) Validate(task TaskID) error {
	id := task.String()
	if !isAbsPath(t.Socket) {
		return &ValidationError{Entity: "agent session", ID: id, Field: "tmux.socket", Reason: "must be absolute, but is " + quote(t.Socket)}
	}
	for _, field := range []struct {
		name    string
		value   string
		pattern *regexp.Regexp
		kind    string
	}{
		{"session", t.Session, tmuxSessionIDPattern, "$session id"},
		{"window", t.Window, tmuxWindowIDPattern, "@window id"},
		{"pane", t.Pane, tmuxPaneIDPattern, "%pane id"},
	} {
		if !field.pattern.MatchString(field.value) {
			return &ValidationError{
				Entity: "agent session", ID: id, Field: "tmux." + field.name,
				Reason: "must be a stable tmux " + field.kind + ", but is " + quote(field.value),
			}
		}
	}
	return nil
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
//
// It also drops a recorded turn end, which is what keeps that record meaning one
// thing. A turn end is a transition nothing has applied yet, held about the
// process the session was last reported in; an observation of that process
// either is the transition — the session went idle — or supersedes it: the
// session ended, failed, or is running something else. In every case there is
// nothing left for a restart to re-arm (ADR-096).
func (s *AgentSession) Observe(state ProcessState, now time.Time) error {
	if !state.Valid() {
		return &ValidationError{
			Entity: "agent session",
			Field:  "process",
			Reason: "must be a documented process state, but is " + quote(string(state)),
		}
	}
	s.Process = state
	s.TurnEndedAt = time.Time{}
	s.LastActivityAt = normalizeTime(now)
	return nil
}

// ReconcileTerminal records the stable target of the terminal a session was
// found in, and whatever that terminal can establish about its process.
//
// The target is always adopted: user renames and window indexes never alter task
// identity, so the identifiers the terminal reports are the ones to keep.
//
// The process state is adopted only where the terminal establishes something.
// A terminal sees alive, exited, or signalled and nothing finer, so a live
// terminal under a session already recorded in an alive state says nothing that
// is not already recorded — and writing what it can say anyway is what promoted
// a session the provider had reported idle back to running, for the rest of its
// life (ADR-096). Whether a session is running or idle is the provider's to
// report through its hooks, and a live terminal is not an observation of it.
//
// What it still establishes is a terminal that has ended, which no provider
// event may arrive to report, and a live terminal under a session recorded as
// over, which is a session that has been started again in it.
func (s *AgentSession) ReconcileTerminal(target TmuxTarget, terminal ProcessState, task TaskID, now time.Time) error {
	if err := target.Validate(task); err != nil {
		return err
	}
	if !terminal.Valid() {
		return &ValidationError{
			Entity: "agent session",
			ID:     task.String(),
			Field:  "process",
			Reason: "must be a documented process state, but is " + quote(string(terminal)),
		}
	}

	s.Tmux = target
	if state, observed := reconciledProcess(s.Process, terminal); observed {
		return s.Observe(state, now)
	}
	return nil
}

// reconciledProcess is what a terminal observation may record over a session's
// recorded process state, and whether it may record anything at all.
func reconciledProcess(recorded, terminal ProcessState) (ProcessState, bool) {
	if !terminal.Alive() {
		return terminal, true
	}
	if recorded.Alive() {
		return recorded, false
	}
	return ProcessRunning, true
}

// RecordTurnEnd notes the turn end an idle grace period is counting from.
//
// It is recorded before the transition it arms is applied, which is the ordering
// every other resource in Feat is created under: plan, record, then apply, so
// that an interruption leaves a record naming what was decided rather than
// losing it (ADR-096).
func (s *AgentSession) RecordTurnEnd(ended time.Time) {
	s.TurnEndedAt = normalizeTime(ended)
}

// ClearTurnEnd drops a recorded turn end because the session spoke again.
//
// Observe drops one too, and this is the other half: several of the things a
// session says — it asked a question, it requested review, it drafted a
// publication — are activity that changes no process state, so there is no
// observation to carry the drop.
func (s *AgentSession) ClearTurnEnd() {
	s.TurnEndedAt = time.Time{}
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
	if err := s.Tmux.Validate(task); err != nil {
		return err
	}
	if s.ControlPath != "" && !isAbsPath(s.ControlPath) {
		return &ValidationError{
			Entity: "agent session",
			ID:     id,
			Field:  "control_path",
			Reason: "must be absolute, but is " + quote(s.ControlPath),
		}
	}
	if s.Execution != nil {
		if s.ExecutionMode == ExecutionHost {
			return &InvariantError{
				Entity: "agent session",
				ID:     id,
				Rule:   "a host session has no execution environment to identify",
				Reason: "the session records the environment " + quote(s.Execution.Identity) + " and runs on the host",
			}
		}
		if err := s.Execution.Validate(task); err != nil {
			return err
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
