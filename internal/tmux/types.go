package tmux

import (
	"fmt"
	"path/filepath"
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

// CommandSpec is one process a tmux pane starts.
//
// It is already resolved for a host or devcontainer execution environment.
// tmux preserves the terminal and does not interpret where the command runs.
type CommandSpec struct {
	Program   string
	Arguments []string
	Directory string
}

// Validate reports whether the command can be passed to tmux without tmux
// interpreting its program as one of new-window's own flags.
func (s CommandSpec) Validate() error {
	if err := safeArgument("program", s.Program, false); err != nil {
		return err
	}
	if !filepath.IsAbs(s.Directory) {
		return fmt.Errorf("tmux command working directory must be absolute, but is %q", s.Directory)
	}
	for i, argument := range s.Arguments {
		if err := safeArgument(fmt.Sprintf("argument %d", i+1), argument, true); err != nil {
			return err
		}
	}
	return nil
}

// Terminal is one tagged task window discovered on the dedicated server.
type Terminal struct {
	Project domain.ProjectID
	Task    domain.TaskID
	Target  domain.TmuxTarget
	Agent   Pane
	Shell   *Pane
}

// ProcessState maps the agent pane's observable process state onto the domain.
// It deliberately says nothing about agent idleness or task completion.
func (t Terminal) ProcessState() domain.ProcessState {
	if !t.Agent.Dead {
		return domain.ProcessRunning
	}
	if t.Agent.ExitStatus != nil && *t.Agent.ExitStatus != 0 {
		return domain.ProcessFailed
	}
	return domain.ProcessStopped
}

// Pane is one tagged pane in a task window.
type Pane struct {
	ID         string
	Role       string
	Directory  string
	Dead       bool
	ExitStatus *int
}

func safeArgument(kind, value string, allowDash bool) error {
	if value == "" {
		return fmt.Errorf("tmux command %s must not be empty", kind)
	}
	if !allowDash && strings.HasPrefix(value, "-") {
		return fmt.Errorf("tmux command %s must not begin with %q, but %q does", kind, "-", value)
	}
	for _, r := range value {
		if r == 0 || r == '\n' || r == '\r' {
			return fmt.Errorf("tmux command %s must not contain a NUL or newline, but %q does", kind, value)
		}
	}
	return nil
}
