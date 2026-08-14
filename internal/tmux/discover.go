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
	paneFormat    = "#{session_id}\t#{window_id}\t#{pane_id}\t#{pane_dead}\t#{pane_dead_status}\t#{pane_current_path}\t#{@feat_managed}\t#{@feat_schema}\t#{@feat_project_id}\t#{@feat_task_id}\t#{@feat_pane_role}\t#{pane_pid}\t#{pane_dead_signal}"
)

// The pane format asks how a dead pane died, and not only whether it is dead,
// because on tmux 3.4 those are two facts that arrive at different moments.
//
// `pane_dead` there is `wp->fd == -1`, while `pane_dead_status` additionally
// requires `PANE_STATUSREADY` — the flag tmux sets once it has reaped the child
// and recorded its wait status. So a pane can report itself dead with no outcome
// published yet, and a reader that took the first of those and asked for the
// second in the same breath would see a process that had failed and call it
// stopped. tmux 3.7 closed the gap by making `pane_dead` require the same flag,
// which is why this is invisible on a machine with a current tmux and why it
// showed up on Linux CI first.
//
// Feat therefore derives the guarantee rather than depending on the version
// having it: a pane is dead when tmux can say how it ended, by an exit status or
// by a signal. Until then the pane is reported as it was — running — which is
// exactly what tmux 3.7 says about the same moment.
//
// The field counts below are derived from the formats rather than written beside
// them. A format that gains a field and a parser that keeps the old count is an
// index out of range on a line every discovery reads, and both fakes in the
// tests happened to emit the wider line — so the panic waited for real tmux.
var (
	sessionFields = fieldCount(sessionFormat)
	windowFields  = fieldCount(windowFormat)
	paneFields    = fieldCount(paneFormat)
)

func fieldCount(format string) int { return strings.Count(format, "\t") + 1 }

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
// server, together with the tagged objects it could not use.
//
// A missing server is the same as an empty result: the first task creation
// starts it. A tmux command that fails is an error, because the enumeration
// itself failed; an object the enumeration returned and this code cannot make
// sense of is quarantined instead, so that one damaged terminal does not make
// every healthy one unreachable (ADR-037).
func (t *Tmux) Discover(ctx context.Context) (Discovery, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.discover(ctx)
}

func (t *Tmux) discover(ctx context.Context) (Discovery, error) {
	sessionOutput, err := t.list(ctx, "list-sessions", sessionFormat)
	if err != nil {
		return Discovery{}, err
	}
	windowOutput, err := t.list(ctx, "list-windows", windowFormat, "-a")
	if err != nil {
		return Discovery{}, err
	}
	paneOutput, err := t.list(ctx, "list-panes", paneFormat, "-a")
	if err != nil {
		return Discovery{}, err
	}
	if sessionOutput == nil || windowOutput == nil || paneOutput == nil {
		return Discovery{Terminals: []Terminal{}}, nil
	}

	sessions, damaged := parseSessions(*sessionOutput)
	windows, windowDamage := parseWindows(*windowOutput)
	panes, paneDamage := parsePanes(*paneOutput)
	damaged = append(damaged, windowDamage...)
	damaged = append(damaged, paneDamage...)

	return assemble(t.socket, sessions, windows, panes, damaged), nil
}

// list runs one enumeration. A nil result means the dedicated server is not
// running, which is an empty managed server rather than a failure.
func (t *Tmux) list(ctx context.Context, command, format string, extra ...string) (*string, error) {
	args := append([]string{command}, extra...)
	args = append(args, "-F", format)
	output, err := t.runner.Run(ctx, t.socket, args...)
	if errors.Is(err, ErrServerNotRunning) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("discovering tmux objects with %s: %w", command, err)
	}
	return &output, nil
}

func parseSessions(output string) (map[string]sessionRecord, []Damaged) {
	records := make(map[string]sessionRecord)
	projects := make(map[domain.ProjectID]string)
	var damaged []Damaged
	for _, line := range outputLines(output) {
		fields := splitFields(line, sessionFields)
		if fields[1] != "1" {
			continue
		}
		id := fields[0]
		if fields[2] != metadataVersion {
			// A session written by a different build of Feat. Quarantining it
			// rather than failing is what lets an older daemon keep serving the
			// tasks it does understand.
			damaged = append(damaged, Damaged{Kind: DamagedSession, ID: id, Reason: fmt.Sprintf(
				"the session carries Feat metadata schema %q, and this build reads %q", fields[2], metadataVersion)})
			continue
		}
		if !sessionIDPattern.MatchString(id) {
			damaged = append(damaged, Damaged{Kind: DamagedSession, Reason: fmt.Sprintf(
				"tmux reported the invalid session id %q", id)})
			continue
		}
		project := domain.ProjectID(fields[3])
		if err := project.Validate(); err != nil {
			damaged = append(damaged, Damaged{Kind: DamagedSession, ID: id, Reason: fmt.Sprintf(
				"the session's project metadata is invalid: %s", err)})
			continue
		}
		if previous, exists := projects[project]; exists && previous != id {
			// Neither session can be trusted to be the project's, so both are
			// quarantined and the project is refused a third one rather than
			// given one. The damage is bounded to the project it belongs to.
			damaged = append(damaged,
				Damaged{Kind: DamagedSession, ID: previous, Project: project, Reason: fmt.Sprintf(
					"sessions %s and %s both claim project %s", previous, id, project)},
				Damaged{Kind: DamagedSession, ID: id, Project: project, Reason: fmt.Sprintf(
					"sessions %s and %s both claim project %s", previous, id, project)})
			delete(records, previous)
			continue
		}
		projects[project] = id
		records[id] = sessionRecord{id: id, project: project}
	}
	return records, damaged
}

func parseWindows(output string) (map[string]windowRecord, []Damaged) {
	records := make(map[string]windowRecord)
	var damaged []Damaged
	for _, line := range outputLines(output) {
		fields := splitFields(line, windowFields)
		if fields[2] != "1" || fields[5] == "" {
			continue
		}
		id := fields[1]
		if fields[3] != metadataVersion {
			damaged = append(damaged, Damaged{Kind: DamagedWindow, ID: id, Reason: fmt.Sprintf(
				"the window carries Feat metadata schema %q, and this build reads %q", fields[3], metadataVersion)})
			continue
		}
		if !sessionIDPattern.MatchString(fields[0]) || !windowIDPattern.MatchString(id) {
			damaged = append(damaged, Damaged{Kind: DamagedWindow, Reason: fmt.Sprintf(
				"tmux reported the invalid session and window ids %q and %q", fields[0], id)})
			continue
		}
		project := domain.ProjectID(fields[4])
		task := domain.TaskID(fields[5])
		if err := project.Validate(); err != nil {
			damaged = append(damaged, Damaged{Kind: DamagedWindow, ID: id, Reason: fmt.Sprintf(
				"the window's project metadata is invalid: %s", err)})
			continue
		}
		if err := task.Validate(); err != nil {
			damaged = append(damaged, Damaged{Kind: DamagedWindow, ID: id, Project: project, Reason: fmt.Sprintf(
				"the window's task metadata is invalid: %s", err)})
			continue
		}
		records[fields[0]+"\x00"+id] = windowRecord{
			session: fields[0], id: id, project: project, task: task,
			// A tmux without this format reports an empty field, which reads as
			// nobody watching. Suppression then errs towards delivering a
			// notification, which is the safer of the two mistakes: a notification
			// the user did not need is noise, and one they never got is the
			// failure this slice exists to prevent.
			viewers: parseCount(fields[6]),
		}
	}
	return records, damaged
}

func parsePanes(output string) ([]paneRecord, []Damaged) {
	var records []paneRecord
	var damaged []Damaged
	for _, line := range outputLines(output) {
		fields := splitFields(line, paneFields)
		// User-created panes in a managed window inherit window options. A role
		// is the pane-local marker that says Feat owns this pane, and a pane
		// without one is the user's rather than damage.
		if fields[10] == "" {
			continue
		}
		id := fields[2]
		hurt := func(format string, args ...any) {
			damaged = append(damaged, Damaged{Kind: DamagedPane, ID: id, Reason: fmt.Sprintf(format, args...)})
		}
		if fields[6] != "1" || fields[7] != metadataVersion {
			hurt("the pane has incomplete Feat ownership metadata")
			continue
		}
		if fields[10] != roleAgent && fields[10] != roleShell {
			hurt("the pane has the unknown Feat role %q", fields[10])
			continue
		}
		if !sessionIDPattern.MatchString(fields[0]) || !windowIDPattern.MatchString(fields[1]) ||
			!paneIDPattern.MatchString(id) {
			damaged = append(damaged, Damaged{Kind: DamagedPane, Reason: fmt.Sprintf(
				"tmux reported the invalid target ids %q, %q, and %q", fields[0], fields[1], id)})
			continue
		}

		project := domain.ProjectID(fields[8])
		task := domain.TaskID(fields[9])
		if err := project.Validate(); err != nil {
			hurt("the pane's project metadata is invalid: %s", err)
			continue
		}
		if err := task.Validate(); err != nil {
			hurt("the pane's task metadata is invalid: %s", err)
			continue
		}

		reported, err := parseBool(fields[3])
		if err != nil {
			hurt("the pane's dead flag %s", err)
			continue
		}
		var status *int
		if reported && fields[4] != "" {
			code, err := strconv.Atoi(fields[4])
			if err != nil {
				hurt("the pane's exit status %q is not a number", fields[4])
				continue
			}
			status = &code
		}
		signal := ""
		if reported {
			signal = fields[12]
		}
		// Dead when tmux can say how it ended, and not merely when the pane's
		// file descriptor has gone; see the note on paneFormat.
		dead := reported && (status != nil || signal != "")

		records = append(records, paneRecord{
			session: fields[0],
			window:  fields[1],
			project: project,
			task:    task,
			pane: Pane{
				ID: id, Role: fields[10], Directory: fields[5],
				Dead: dead, ExitStatus: status, Signal: signal,
				// The process tmux started in the pane. What the task is really
				// using is that process and everything it started, which the
				// resource observer walks; the pane itself is only where the walk
				// begins.
				PID: parseCount(fields[11]),
			},
		})
	}
	return records, damaged
}

// terminalKey identifies one task's terminal while it is being assembled.
type terminalKey struct {
	project domain.ProjectID
	task    domain.TaskID
}

func assemble(
	socket string,
	sessions map[string]sessionRecord,
	windows map[string]windowRecord,
	panes []paneRecord,
	damaged []Damaged,
) Discovery {
	// A session or window that could not be read takes its contents with it: a
	// pane whose window is quarantined has no window to belong to, and adopting
	// it would mean guessing at the identity the metadata failed to establish.
	quarantined := make(map[string]bool, len(damaged))
	for _, entry := range damaged {
		if entry.ID != "" && (entry.Kind == DamagedSession || entry.Kind == DamagedWindow) {
			quarantined[entry.ID] = true
		}
	}

	terminals := make(map[terminalKey]*Terminal)
	hurt := make(map[terminalKey]bool)
	damage := func(key terminalKey, id, reason string) {
		hurt[key] = true
		damaged = append(damaged, Damaged{
			Kind: DamagedTerminal, ID: id, Project: key.project, Task: key.task, Reason: reason,
		})
	}

	for _, pane := range panes {
		key := terminalKey{project: pane.project, task: pane.task}
		if quarantined[pane.session] || quarantined[pane.window] {
			damage(key, pane.pane.ID, fmt.Sprintf(
				"pane %s belongs to a session or window this build could not read", pane.pane.ID))
			continue
		}

		session, ok := sessions[pane.session]
		if !ok {
			damage(key, pane.pane.ID, fmt.Sprintf(
				"pane %s is tagged for task %s but session %s is not managed", pane.pane.ID, pane.task, pane.session))
			continue
		}
		window, ok := windows[pane.session+"\x00"+pane.window]
		if !ok {
			damage(key, pane.pane.ID, fmt.Sprintf(
				"pane %s is tagged for task %s but window %s is not managed", pane.pane.ID, pane.task, pane.window))
			continue
		}
		if session.project != pane.project || window.project != pane.project || window.task != pane.task {
			damage(key, pane.pane.ID, fmt.Sprintf(
				"target %s/%s/%s has conflicting Feat project and task metadata",
				pane.session, pane.window, pane.pane.ID))
			continue
		}

		terminal := terminals[key]
		if terminal == nil {
			terminal = &Terminal{
				Project: pane.project,
				Task:    pane.task,
				Target: domain.TmuxTarget{
					Socket: socket, Session: pane.session, Window: pane.window,
				},
				Viewers: window.viewers,
			}
			terminals[key] = terminal
		}
		if terminal.Target.Session != pane.session || terminal.Target.Window != pane.window {
			damage(key, pane.pane.ID, fmt.Sprintf(
				"windows %s and %s both claim task %s in project %s",
				terminal.Target.Window, pane.window, pane.task, pane.project))
			continue
		}

		switch pane.pane.Role {
		case roleAgent:
			if terminal.Target.Pane != "" {
				damage(key, pane.pane.ID, fmt.Sprintf(
					"panes %s and %s both claim the agent role for task %s",
					terminal.Target.Pane, pane.pane.ID, pane.task))
				continue
			}
			terminal.Target.Pane = pane.pane.ID
			terminal.Agent = pane.pane
		case roleShell:
			if terminal.Shell != nil {
				damage(key, pane.pane.ID, fmt.Sprintf(
					"panes %s and %s both claim the shell role for task %s",
					terminal.Shell.ID, pane.pane.ID, pane.task))
				continue
			}
			shellPane := pane.pane
			terminal.Shell = &shellPane
		}
	}

	out := make([]Terminal, 0, len(terminals))
	for key, terminal := range terminals {
		if hurt[key] {
			continue
		}
		if terminal.Target.Pane == "" {
			// A window whose agent pane was killed while its shell survived. It
			// is the case ADR-030 evidence 9 is about, and the whole reason this
			// is a quarantine rather than an error.
			damage(key, terminal.Target.Window, fmt.Sprintf(
				"window %s for task %s has no tagged agent pane", terminal.Target.Window, terminal.Task))
			continue
		}
		out = append(out, *terminal)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Project != out[j].Project {
			return out[i].Project < out[j].Project
		}
		return out[i].Task < out[j].Task
	})

	managed := make([]Session, 0, len(sessions))
	for _, session := range sessions {
		managed = append(managed, Session{ID: session.id, Project: session.project})
	}
	sort.Slice(managed, func(i, j int) bool {
		if managed[i].Project != managed[j].Project {
			return managed[i].Project < managed[j].Project
		}
		return managed[i].ID < managed[j].ID
	})

	sort.SliceStable(damaged, func(i, j int) bool { return damaged[i].ID < damaged[j].ID })
	return Discovery{Terminals: out, Sessions: managed, Damaged: damaged}
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
