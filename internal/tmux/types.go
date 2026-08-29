package tmux

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ma8el/feat/internal/domain"
)

const (
	metadataVersion = "1"
	optionManaged   = "@feat_managed"
	optionVersion   = "@feat_schema"
	optionProject   = "@feat_project_id"
	optionTask      = "@feat_task_id"
	optionRole      = "@feat_pane_role"
	roleAgent       = "agent"
	roleShell       = "shell"
)

// Size is a terminal's dimensions in cells.
//
// The zero value means the caller has nothing to go on and tmux's own choice
// stands, which for a window nobody is attached to is 80x24.
type Size struct{ Width, Height int }

// Known reports a size there is any point applying.
func (s Size) Known() bool { return s.Width > 0 && s.Height > 0 }

// CommandSpec is one process a tmux pane starts.
//
// It is already resolved for a host or devcontainer execution environment.
// tmux preserves the terminal and does not interpret where the command runs.
type CommandSpec struct {
	Program   string
	Arguments []string
	Directory string
	// Variables are environment entries the process starts with, on top of the
	// environment the tmux server itself passes down. They are generated,
	// non-secret values: tmux reports a pane's environment to anyone who can
	// reach the server, and the server outlives the process.
	Variables map[string]string
}

// Validate reports whether the command can be passed to tmux without tmux
// interpreting its program as one of new-window's own flags, and without any of
// its values breaking the formats discovery parses.
//
// The working directory is checked with the same rule as the arguments, and not
// only for being absolute. It is the one caller-supplied value tmux reports
// back, as #{pane_current_path} inside a tab-separated list format, so a tab in
// a path misaligns every pane field and breaks discovery for every terminal on
// the server — the blast radius quarantine bounds, reached before quarantine can
// bound it (ADR-030 evidence 10, settled by ADR-037).
func (s CommandSpec) Validate() error {
	if err := safeArgument("program", s.Program, false); err != nil {
		return err
	}
	if !filepath.IsAbs(s.Directory) {
		return fmt.Errorf("tmux command working directory must be absolute, but is %q", s.Directory)
	}
	if err := safeArgument("working directory", s.Directory, false); err != nil {
		return err
	}
	for i, argument := range s.Arguments {
		if err := safeArgument(fmt.Sprintf("argument %d", i+1), argument, true); err != nil {
			return err
		}
	}
	for name, value := range s.Variables {
		if name == "" {
			return fmt.Errorf("a tmux command environment variable has no name")
		}
		if strings.Contains(name, "=") {
			return fmt.Errorf("the tmux command environment variable %q must not contain %q", name, "=")
		}
		if err := safeArgument("environment variable "+name, name+"="+value, false); err != nil {
			return err
		}
	}
	return nil
}

// Entries renders the variables as sorted KEY=VALUE arguments.
//
// A map's iteration order does not repeat, and these reach an argument vector,
// so sorting makes the same specification the same command every time — which
// is what lets a test pin one.
func (s CommandSpec) Entries() []string {
	if len(s.Variables) == 0 {
		return nil
	}
	names := make([]string, 0, len(s.Variables))
	for name := range s.Variables {
		names = append(names, name)
	}
	sort.Strings(names)

	entries := make([]string, 0, len(names))
	for _, name := range names {
		entries = append(entries, name+"="+s.Variables[name])
	}
	return entries
}

// Discovery is what one pass over the dedicated server found.
//
// It carries what could be read and what could not, rather than failing as a
// whole when one tagged object is inconsistent. A damaged pane quarantines the
// terminal it belongs to, because half a terminal is not one, and a damaged
// session quarantines its windows for the same reason; everything else stays
// usable. One task whose agent pane was killed while its shell pane survived
// must not make every unrelated task unreachable (ADR-030 evidence 9, ADR-037).
type Discovery struct {
	// Terminals are the completely tagged, internally consistent task
	// terminals.
	Terminals []Terminal
	// Sessions are the managed project sessions, including those whose windows
	// were all quarantined. A project whose session is here but whose terminals
	// are not must not be given a second session.
	Sessions []Session
	// Damaged are the tagged objects this pass could not use, each with the
	// reason. They are reported and never repaired, adopted, or removed.
	Damaged []Damaged
}

// Session is one managed project session on the dedicated server.
type Session struct {
	// ID is tmux's immutable $session identifier.
	ID string
	// Project is the project the session is tagged for.
	Project domain.ProjectID
}

// Damaged is one tagged object discovery could not use.
type Damaged struct {
	// Kind is the scope the damage was found at: session, window, pane, or
	// terminal for damage that is only visible once the three are assembled.
	Kind string
	// ID is the tmux identifier, empty when it was the unreadable part.
	ID string
	// Project and Task are the metadata that could be read, empty otherwise.
	Project domain.ProjectID
	Task    domain.TaskID
	// Reason says what is wrong, in terms a user can act on.
	Reason string
}

// Damage kinds.
const (
	DamagedSession  = "session"
	DamagedWindow   = "window"
	DamagedPane     = "pane"
	DamagedTerminal = "terminal"
)

// Terminal returns the terminal carrying one task's metadata.
func (d Discovery) Terminal(project domain.ProjectID, task domain.TaskID) (Terminal, bool) {
	return findTerminal(d.Terminals, project, task)
}

// DamageFor returns the damage recorded against one task, which is what turns
// "no terminal was found" into an explanation.
func (d Discovery) DamageFor(project domain.ProjectID, task domain.TaskID) []Damaged {
	var found []Damaged
	for _, damaged := range d.Damaged {
		if damaged.Task == task && (damaged.Project == project || damaged.Project == "") {
			found = append(found, damaged)
		}
	}
	return found
}

// Terminal is one tagged task window discovered on the dedicated server.
type Terminal struct {
	Project domain.ProjectID
	Task    domain.TaskID
	Target  domain.TmuxTarget
	Agent   Pane
	Shell   *Pane
	// Viewers is how many attached clients are looking at this task's window
	// right now. It is an observation of the terminal rather than a record of an
	// attach: a user who detached, or who switched to another task's window,
	// stops being a viewer without telling Feat anything.
	Viewers int
}

// Watched reports whether somebody is looking at this task's terminal.
func (t Terminal) Watched() bool { return t.Viewers > 0 }

// ProcessState maps the agent pane's observable process state onto the domain.
// It deliberately says nothing about agent idleness or task completion.
//
// A pane that ended on a signal is failed rather than stopped, and it has no
// exit status to say so with: `pane_dead_status` is the status of a process that
// exited, and tmux publishes a killed one as `pane_dead_signal` instead. Reading
// the absent status as a clean exit would report an agent the kernel killed —
// the OOM killer is the ordinary way that happens — as one that finished.
func (t Terminal) ProcessState() domain.ProcessState {
	if !t.Agent.Dead {
		return domain.ProcessRunning
	}
	if t.Agent.Signal != "" {
		return domain.ProcessFailed
	}
	if t.Agent.ExitStatus != nil && *t.Agent.ExitStatus != 0 {
		return domain.ProcessFailed
	}
	return domain.ProcessStopped
}

// Pane is one tagged pane in a task window.
type Pane struct {
	ID        string
	Role      string
	Directory string
	// Dead reports a pane whose process has ended and whose ending tmux can
	// describe. A pane whose descriptor has closed and whose child has not been
	// reaped yet is not dead here, because nothing can yet be said about how it
	// went; see the note on paneFormat.
	Dead bool
	// ExitStatus is the status of a pane process that exited, nil for one that
	// was killed.
	ExitStatus *int
	// Signal is the name of the signal that killed the pane's process, empty
	// for one that exited on its own.
	Signal string
	// PID is the process tmux started in the pane, or zero when there is none.
	// It is where a resource observer starts walking, never an identity.
	PID int
}

func safeArgument(kind, value string, allowDash bool) error {
	if value == "" {
		return fmt.Errorf("tmux command %s must not be empty", kind)
	}
	if !allowDash && strings.HasPrefix(value, "-") {
		return fmt.Errorf("tmux command %s must not begin with %q, but %q does", kind, "-", value)
	}
	for _, r := range value {
		// The tab belongs here with the NUL and the newline: all three are
		// separators discovery parses, and a value carrying one is a value that
		// makes tmux's own report unreadable.
		if r == 0 || r == '\n' || r == '\r' || r == '\t' {
			return fmt.Errorf("tmux command %s must not contain a NUL, newline, or tab, but %q does", kind, value)
		}
	}
	return nil
}
