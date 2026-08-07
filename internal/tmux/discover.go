package tmux

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/ma8el/feat/internal/domain"
)

// The formats discovery reads. Every field appended to one of them is appended
// at the end, so that adding an observation cannot move the index another field
// is read from.
//
// The window format carries window_active_clients, which is how many attached
// clients are looking at that window right now. It is the per-window answer to
// "is the user watching this task", and session_attached is not: a user attached
// to a project's session is looking at one of its task windows and not at the
// others, so a session-level answer would silence every task the moment one of
// them was being watched. Measured against tmux 3.7b, where it follows a window
// switch immediately (ADR-035).
const (
	sessionFormat = "#{session_id}\t#{@feat_managed}\t#{@feat_schema}\t#{@feat_project_id}"
	windowFormat  = "#{session_id}\t#{window_id}\t#{@feat_managed}\t#{@feat_schema}\t#{@feat_project_id}\t#{@feat_task_id}\t#{window_active_clients}"
	paneFormat    = "#{session_id}\t#{window_id}\t#{pane_id}\t#{pane_dead}\t#{pane_dead_status}\t#{pane_current_path}\t#{@feat_managed}\t#{@feat_schema}\t#{@feat_project_id}\t#{@feat_task_id}\t#{@feat_pane_role}\t#{pane_pid}"
)

var (
	sessionIDPattern = regexp.MustCompile(`^\$[0-9]+$`)
	windowIDPattern  = regexp.MustCompile(`^@[0-9]+$`)
	paneIDPattern    = regexp.MustCompile(`^%[0-9]+$`)
)

type sessionRecord struct {
	id      string
	project domain.ProjectID
}

type windowRecord struct {
	session string
	id      string
	project domain.ProjectID
	task    domain.TaskID
	// viewers is how many attached clients are looking at this window.
	viewers int
}

type paneRecord struct {
	session string
	window  string
	pane    Pane
	project domain.ProjectID
	task    domain.TaskID
}

// Discover returns every completely tagged task terminal on the dedicated
// server. A missing server is the same as an empty result: the first task
// creation starts it.
func (t *Tmux) Discover(ctx context.Context) ([]Terminal, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.discover(ctx)
}

func (t *Tmux) discover(ctx context.Context) ([]Terminal, error) {
	sessionOutput, err := t.runner.Run(ctx, t.socket, "list-sessions", "-F", sessionFormat)
	if errors.Is(err, ErrServerNotRunning) {
		return []Terminal{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("discovering tmux sessions: %w", err)
	}

	windowOutput, err := t.runner.Run(ctx, t.socket, "list-windows", "-a", "-F", windowFormat)
	if errors.Is(err, ErrServerNotRunning) {
		return []Terminal{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("discovering tmux windows: %w", err)
	}

	paneOutput, err := t.runner.Run(ctx, t.socket, "list-panes", "-a", "-F", paneFormat)
	if errors.Is(err, ErrServerNotRunning) {
		return []Terminal{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("discovering tmux panes: %w", err)
	}

	sessions, err := parseSessions(sessionOutput)
	if err != nil {
		return nil, err
	}
	windows, err := parseWindows(windowOutput)
	if err != nil {
		return nil, err
	}
	panes, err := parsePanes(paneOutput)
	if err != nil {
		return nil, err
	}

	return assemble(t.socket, sessions, windows, panes)
}

func parseSessions(output string) (map[string]sessionRecord, error) {
	records := make(map[string]sessionRecord)
	projects := make(map[domain.ProjectID]string)
	for _, line := range outputLines(output) {
		fields := splitFields(line, 4)
		if fields[1] != "1" {
			continue
		}
		if fields[2] != metadataVersion {
			return nil, fmt.Errorf("tmux session %s has Feat metadata schema %q, want %q",
				fields[0], fields[2], metadataVersion)
		}
		if !sessionIDPattern.MatchString(fields[0]) {
			return nil, fmt.Errorf("tmux reported invalid session id %q", fields[0])
		}
		project := domain.ProjectID(fields[3])
		if err := project.Validate(); err != nil {
			return nil, fmt.Errorf("tmux session %s has invalid project metadata: %w", fields[0], err)
		}
		if previous, exists := projects[project]; exists && previous != fields[0] {
			return nil, fmt.Errorf("tmux sessions %s and %s both claim project %s",
				previous, fields[0], project)
		}
		projects[project] = fields[0]
		records[fields[0]] = sessionRecord{id: fields[0], project: project}
	}
	return records, nil
}

func parseWindows(output string) (map[string]windowRecord, error) {
	records := make(map[string]windowRecord)
	for _, line := range outputLines(output) {
		fields := splitFields(line, 7)
		if fields[2] != "1" || fields[5] == "" {
			continue
		}
		if fields[3] != metadataVersion {
			return nil, fmt.Errorf("tmux window %s has Feat metadata schema %q, want %q",
				fields[1], fields[3], metadataVersion)
		}
		if !sessionIDPattern.MatchString(fields[0]) || !windowIDPattern.MatchString(fields[1]) {
			return nil, fmt.Errorf("tmux reported invalid session/window ids %q and %q", fields[0], fields[1])
		}
		project := domain.ProjectID(fields[4])
		task := domain.TaskID(fields[5])
		if err := project.Validate(); err != nil {
			return nil, fmt.Errorf("tmux window %s has invalid project metadata: %w", fields[1], err)
		}
		if err := task.Validate(); err != nil {
			return nil, fmt.Errorf("tmux window %s has invalid task metadata: %w", fields[1], err)
		}
		key := fields[0] + "\x00" + fields[1]
		records[key] = windowRecord{
			session: fields[0], id: fields[1], project: project, task: task,
			// A tmux without this format reports an empty field, which reads as
			// nobody watching. Suppression then errs towards delivering a
			// notification, which is the safer of the two mistakes: a notification
			// the user did not need is noise, and one they never got is the
			// failure this slice exists to prevent.
			viewers: parseCount(fields[6]),
		}
	}
	return records, nil
}

func parsePanes(output string) ([]paneRecord, error) {
	var records []paneRecord
	for _, line := range outputLines(output) {
		fields := splitFields(line, 12)
		// User-created panes in a managed window inherit window options. A role
		// is the pane-local marker that says Feat owns this pane.
		if fields[10] == "" {
			continue
		}
		if fields[6] != "1" || fields[7] != metadataVersion {
			return nil, fmt.Errorf("tmux pane %s has incomplete Feat ownership metadata", fields[2])
		}
		if fields[10] != roleAgent && fields[10] != roleShell {
			return nil, fmt.Errorf("tmux pane %s has unknown Feat role %q", fields[2], fields[10])
		}
		if !sessionIDPattern.MatchString(fields[0]) || !windowIDPattern.MatchString(fields[1]) ||
			!paneIDPattern.MatchString(fields[2]) {
			return nil, fmt.Errorf("tmux reported invalid target ids %q, %q, and %q",
				fields[0], fields[1], fields[2])
		}

		project := domain.ProjectID(fields[8])
		task := domain.TaskID(fields[9])
		if err := project.Validate(); err != nil {
			return nil, fmt.Errorf("tmux pane %s has invalid project metadata: %w", fields[2], err)
		}
		if err := task.Validate(); err != nil {
			return nil, fmt.Errorf("tmux pane %s has invalid task metadata: %w", fields[2], err)
		}

		dead, err := parseBool(fields[3])
		if err != nil {
			return nil, fmt.Errorf("tmux pane %s dead flag: %w", fields[2], err)
		}
		var status *int
		if dead && fields[4] != "" {
			code, err := strconv.Atoi(fields[4])
			if err != nil {
				return nil, fmt.Errorf("tmux pane %s exit status %q is not a number", fields[2], fields[4])
			}
			status = &code
		}
		records = append(records, paneRecord{
			session: fields[0],
			window:  fields[1],
			project: project,
			task:    task,
			pane: Pane{
				ID: fields[2], Role: fields[10], Directory: fields[5], Dead: dead, ExitStatus: status,
				// The process tmux started in the pane. What the task is really
				// using is that process and everything it started, which the
				// resource observer walks; the pane itself is only where the walk
				// begins.
				PID: parseCount(fields[11]),
			},
		})
	}
	return records, nil
}

func assemble(socket string, sessions map[string]sessionRecord, windows map[string]windowRecord, panes []paneRecord) ([]Terminal, error) {
	type key struct {
		project domain.ProjectID
		task    domain.TaskID
	}
	terminals := make(map[key]*Terminal)

	for _, pane := range panes {
		session, ok := sessions[pane.session]
		if !ok {
			return nil, fmt.Errorf("tmux pane %s is tagged for task %s but session %s is not managed",
				pane.pane.ID, pane.task, pane.session)
		}
		window, ok := windows[pane.session+"\x00"+pane.window]
		if !ok {
			return nil, fmt.Errorf("tmux pane %s is tagged for task %s but window %s is not managed",
				pane.pane.ID, pane.task, pane.window)
		}
		if session.project != pane.project || window.project != pane.project || window.task != pane.task {
			return nil, fmt.Errorf("tmux target %s/%s/%s has conflicting Feat project/task metadata",
				pane.session, pane.window, pane.pane.ID)
		}

		identity := key{project: pane.project, task: pane.task}
		terminal := terminals[identity]
		if terminal == nil {
			terminal = &Terminal{
				Project: pane.project,
				Task:    pane.task,
				Target: domain.TmuxTarget{
					Socket: socket, Session: pane.session, Window: pane.window,
				},
				Viewers: window.viewers,
			}
			terminals[identity] = terminal
		}
		if terminal.Target.Session != pane.session || terminal.Target.Window != pane.window {
			return nil, fmt.Errorf("multiple tmux windows claim task %s in project %s", pane.task, pane.project)
		}

		switch pane.pane.Role {
		case roleAgent:
			if terminal.Target.Pane != "" {
				return nil, fmt.Errorf("tmux panes %s and %s both claim the agent role for task %s",
					terminal.Target.Pane, pane.pane.ID, pane.task)
			}
			terminal.Target.Pane = pane.pane.ID
			terminal.Agent = pane.pane
		case roleShell:
			if terminal.Shell != nil {
				return nil, fmt.Errorf("multiple tmux panes claim the shell role for task %s", pane.task)
			}
			shellPane := pane.pane
			terminal.Shell = &shellPane
		}
	}

	out := make([]Terminal, 0, len(terminals))
	for _, terminal := range terminals {
		if terminal.Target.Pane == "" {
			return nil, fmt.Errorf("tmux window %s for task %s has no tagged agent pane",
				terminal.Target.Window, terminal.Task)
		}
		out = append(out, *terminal)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Project != out[j].Project {
			return out[i].Project < out[j].Project
		}
		return out[i].Task < out[j].Task
	})
	return out, nil
}

func outputLines(output string) []string {
	if output == "" {
		return nil
	}
	return strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
}

func splitFields(line string, count int) []string {
	fields := strings.Split(line, "\t")
	for len(fields) < count {
		fields = append(fields, "")
	}
	return fields
}

// parseCount reads a non-negative number tmux reported, treating anything else
// as zero.
//
// A format an older tmux does not know is printed back empty rather than as an
// error, and neither a client count nor a process identifier is worth failing
// discovery over: both are observations beside the identity, and identity is
// what discovery exists to establish.
func parseCount(value string) int {
	count, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || count < 0 {
		return 0
	}
	return count
}

func parseBool(value string) (bool, error) {
	switch value {
	case "0":
		return false, nil
	case "1":
		return true, nil
	default:
		return false, fmt.Errorf("must be 0 or 1, but is %q", value)
	}
}
