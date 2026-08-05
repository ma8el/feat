package tmux

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ma8el/feat/internal/domain"
)

const createFormat = "#{session_id}\t#{window_id}\t#{pane_id}"

// Tmux manages persistent task terminals on one dedicated server.
type Tmux struct {
	socket string
	runner Runner
	mu     sync.Mutex
}

// New creates an adapter for one explicit tmux socket.
func New(socket string, runner Runner) (*Tmux, error) {
	if !filepath.IsAbs(socket) {
		return nil, fmt.Errorf("tmux socket must be absolute, but is %q", socket)
	}
	if runner == nil {
		runner = HostRunner{}
	}
	return &Tmux{socket: filepath.Clean(socket), runner: runner}, nil
}

// Host returns an adapter for the real tmux executable.
func Host(socket string) (*Tmux, error) { return New(socket, HostRunner{}) }

// Socket returns the dedicated server socket this adapter always supplies.
func (t *Tmux) Socket() string { return t.socket }

// EnsureTask creates a project session and task window when their stable
// metadata is not already present. Repeating the request returns the existing
// target, regardless of names or indexes the user has changed.
func (t *Tmux) EnsureTask(ctx context.Context, project domain.ProjectID, task domain.TaskID, command CommandSpec) (Terminal, error) {
	if err := project.Validate(); err != nil {
		return Terminal{}, err
	}
	if err := task.Validate(); err != nil {
		return Terminal{}, err
	}
	if err := command.Validate(); err != nil {
		return Terminal{}, err
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	terminals, err := t.discover(ctx)
	if err != nil {
		return Terminal{}, err
	}
	if existing, ok := findTerminal(terminals, project, task); ok {
		return existing, nil
	}

	session, err := projectSession(terminals, project)
	if err != nil {
		return Terminal{}, err
	}

	var created domain.TmuxTarget
	if session == "" {
		created, err = t.createSession(ctx, project, task, command)
	} else {
		created, err = t.createWindow(ctx, session, project, task, command)
	}
	if err != nil {
		return Terminal{}, err
	}

	// Once all three scopes are tagged, a storage failure must leave the
	// terminal in place: discovery can recover it, while rollback could destroy
	// work already entered in the pane (ADR-030).
	terminals, err = t.discover(ctx)
	if err != nil {
		return Terminal{}, fmt.Errorf("task terminal %s was created and tagged at %s/%s/%s but could not be rediscovered: %w",
			task, created.Session, created.Window, created.Pane, err)
	}
	terminal, ok := findTerminal(terminals, project, task)
	if !ok {
		return Terminal{}, fmt.Errorf("task terminal %s was created at %s/%s/%s but its Feat metadata was not discoverable",
			task, created.Session, created.Window, created.Pane)
	}
	return terminal, nil
}

// EnsureShell creates the one on-demand shell pane for a task, or returns the
// existing tagged pane. The caller supplies the execution-environment command
// and primary workspace; tmux does not construct either one.
func (t *Tmux) EnsureShell(ctx context.Context, project domain.ProjectID, task domain.TaskID, command CommandSpec) (Terminal, error) {
	if err := project.Validate(); err != nil {
		return Terminal{}, err
	}
	if err := task.Validate(); err != nil {
		return Terminal{}, err
	}
	if err := command.Validate(); err != nil {
		return Terminal{}, err
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	terminals, err := t.discover(ctx)
	if err != nil {
		return Terminal{}, err
	}
	terminal, ok := findTerminal(terminals, project, task)
	if !ok {
		return Terminal{}, fmt.Errorf("task %s has no managed tmux terminal on %s", task, t.socket)
	}
	if terminal.Shell != nil {
		return terminal, nil
	}

	output, err := t.runner.Run(ctx, t.socket, "split-window", "-d", "-P", "-F", createFormat,
		"-t", terminal.Target.Pane, "-c", command.Directory)
	if err != nil {
		return Terminal{}, fmt.Errorf("creating the shell pane for task %s: %w", task, err)
	}
	created, err := parseCreated(output)
	if err != nil {
		return Terminal{}, fmt.Errorf("creating the shell pane for task %s: %w", task, err)
	}
	if created.Session != terminal.Target.Session || created.Window != terminal.Target.Window {
		cleanupErr := t.kill(ctx, "kill-pane", created.Pane)
		return Terminal{}, errors.Join(
			fmt.Errorf("tmux created shell pane %s outside task window %s", created.Pane, terminal.Target.Window),
			cleanupErr,
		)
	}

	if err := t.tagPane(ctx, created.Pane, project, task, roleShell); err != nil {
		return Terminal{}, errors.Join(err, t.kill(ctx, "kill-pane", created.Pane))
	}
	if err := t.start(ctx, created.Pane, command); err != nil {
		return Terminal{}, errors.Join(err, t.kill(ctx, "kill-pane", created.Pane))
	}

	terminals, err = t.discover(ctx)
	if err != nil {
		return Terminal{}, fmt.Errorf("shell pane %s was tagged but could not be rediscovered: %w", created.Pane, err)
	}
	terminal, ok = findTerminal(terminals, project, task)
	if !ok || terminal.Shell == nil {
		return Terminal{}, fmt.Errorf("shell pane %s was created but its Feat metadata was not discoverable", created.Pane)
	}
	return terminal, nil
}

// Find returns the terminal carrying one task's metadata.
func (t *Tmux) Find(ctx context.Context, project domain.ProjectID, task domain.TaskID) (Terminal, bool, error) {
	terminals, err := t.Discover(ctx)
	if err != nil {
		return Terminal{}, false, err
	}
	terminal, ok := findTerminal(terminals, project, task)
	return terminal, ok, nil
}

func (t *Tmux) createSession(ctx context.Context, project domain.ProjectID, task domain.TaskID, command CommandSpec) (domain.TmuxTarget, error) {
	output, err := t.runner.Run(ctx, t.socket, "new-session", "-d", "-P", "-F", createFormat,
		"-n", "task-"+task.Key().String(), "-c", command.Directory)
	if err != nil {
		return domain.TmuxTarget{}, fmt.Errorf("creating the tmux session for project %s: %w", project, err)
	}
	created, err := parseCreated(output)
	if err != nil {
		return domain.TmuxTarget{}, fmt.Errorf("creating the tmux session for project %s: %w", project, err)
	}

	if err := t.tagPane(ctx, created.Pane, project, task, roleAgent); err != nil {
		return domain.TmuxTarget{}, errors.Join(err, t.kill(ctx, "kill-session", created.Session))
	}
	if err := t.tagWindow(ctx, created.Window, project, task); err != nil {
		return domain.TmuxTarget{}, errors.Join(err, t.kill(ctx, "kill-session", created.Session))
	}
	if err := t.tagSession(ctx, created.Session, project); err != nil {
		return domain.TmuxTarget{}, errors.Join(err, t.kill(ctx, "kill-session", created.Session))
	}
	if err := t.start(ctx, created.Pane, command); err != nil {
		return domain.TmuxTarget{}, errors.Join(err, t.kill(ctx, "kill-session", created.Session))
	}
	return created, nil
}

func (t *Tmux) createWindow(ctx context.Context, session string, project domain.ProjectID, task domain.TaskID, command CommandSpec) (domain.TmuxTarget, error) {
	output, err := t.runner.Run(ctx, t.socket, "new-window", "-d", "-P", "-F", createFormat,
		"-t", session, "-n", "task-"+task.Key().String(), "-c", command.Directory)
	if err != nil {
		return domain.TmuxTarget{}, fmt.Errorf("creating the tmux window for task %s: %w", task, err)
	}
	created, err := parseCreated(output)
	if err != nil {
		return domain.TmuxTarget{}, fmt.Errorf("creating the tmux window for task %s: %w", task, err)
	}
	if created.Session != session {
		return domain.TmuxTarget{}, errors.Join(
			fmt.Errorf("tmux created task window %s in session %s, want %s", created.Window, created.Session, session),
			t.kill(ctx, "kill-window", created.Window),
		)
	}

	if err := t.tagPane(ctx, created.Pane, project, task, roleAgent); err != nil {
		return domain.TmuxTarget{}, errors.Join(err, t.kill(ctx, "kill-window", created.Window))
	}
	if err := t.tagWindow(ctx, created.Window, project, task); err != nil {
		return domain.TmuxTarget{}, errors.Join(err, t.kill(ctx, "kill-window", created.Window))
	}
	if err := t.start(ctx, created.Pane, command); err != nil {
		return domain.TmuxTarget{}, errors.Join(err, t.kill(ctx, "kill-window", created.Window))
	}
	return created, nil
}

// start replaces a tagged pane's holder shell with the caller's command.
//
// Panes are created without a command and tagged first, so remain-on-exit is
// already in effect when the real program starts. A program that exits at once
// then leaves a dead pane carrying its exit status, which discovery reports as
// a failed process. Starting the program with the pane would instead destroy
// the pane, its window, and possibly the server before any metadata landed,
// and the caller would be told the tmux server was not running.
//
// The holder shell never ran the caller's command, so a failure here removes
// the exact object just created rather than leaving it for reconciliation: the
// retention rule in ADR-030 protects work entered in a pane, and there is none.
func (t *Tmux) start(ctx context.Context, pane string, command CommandSpec) error {
	args := []string{"respawn-pane", "-k", "-t", pane, "-c", command.Directory, command.Program}
	args = append(args, command.Arguments...)
	if _, err := t.runner.Run(ctx, t.socket, args...); err != nil {
		return fmt.Errorf("starting %s in tmux pane %s: %w", command.Program, pane, err)
	}
	return nil
}

func (t *Tmux) tagSession(ctx context.Context, session string, project domain.ProjectID) error {
	// Apply persistence before @feat_managed makes the session discoverable. An
	// interrupted setup is then either invisible and cleaned up on this call's
	// failure path, or complete enough for startup reconciliation.
	if err := t.setOption(ctx, nil, session, "destroy-unattached", "off"); err != nil {
		return err
	}
	for _, option := range [][2]string{
		{optionVersion, metadataVersion}, {optionProject, project.String()}, {optionManaged, "1"},
	} {
		if err := t.setOption(ctx, nil, session, option[0], option[1]); err != nil {
			return err
		}
	}
	return nil
}

func (t *Tmux) tagWindow(ctx context.Context, window string, project domain.ProjectID, task domain.TaskID) error {
	for _, option := range [][2]string{
		{optionVersion, metadataVersion}, {optionProject, project.String()},
		{optionTask, task.String()}, {optionManaged, "1"},
	} {
		if err := t.setOption(ctx, []string{"-w"}, window, option[0], option[1]); err != nil {
			return err
		}
	}
	return nil
}

func (t *Tmux) tagPane(ctx context.Context, pane string, project domain.ProjectID, task domain.TaskID, role string) error {
	if err := t.setOption(ctx, []string{"-p"}, pane, "remain-on-exit", "on"); err != nil {
		return err
	}
	for _, option := range [][2]string{
		{optionVersion, metadataVersion}, {optionProject, project.String()},
		{optionTask, task.String()}, {optionManaged, "1"}, {optionRole, role},
	} {
		if err := t.setOption(ctx, []string{"-p"}, pane, option[0], option[1]); err != nil {
			return err
		}
	}
	return nil
}

func (t *Tmux) setOption(ctx context.Context, scope []string, target, option, value string) error {
	args := []string{"set-option"}
	args = append(args, scope...)
	args = append(args, "-t", target, option, value)
	if _, err := t.runner.Run(ctx, t.socket, args...); err != nil {
		return fmt.Errorf("setting tmux option %s on %s: %w", option, target, err)
	}
	return nil
}

func (t *Tmux) kill(ctx context.Context, command, target string) error {
	if _, err := t.runner.Run(ctx, t.socket, command, "-t", target); err != nil && !errors.Is(err, ErrServerNotRunning) {
		return fmt.Errorf("cleaning up tmux target %s after incomplete creation: %w", target, err)
	}
	return nil
}

func parseCreated(output string) (domain.TmuxTarget, error) {
	fields := strings.Split(strings.TrimSpace(output), "\t")
	if len(fields) != 3 || !sessionIDPattern.MatchString(fields[0]) ||
		!windowIDPattern.MatchString(fields[1]) || !paneIDPattern.MatchString(fields[2]) {
		return domain.TmuxTarget{}, fmt.Errorf("tmux returned %q, want stable session, window, and pane ids", output)
	}
	return domain.TmuxTarget{Session: fields[0], Window: fields[1], Pane: fields[2]}, nil
}

func findTerminal(terminals []Terminal, project domain.ProjectID, task domain.TaskID) (Terminal, bool) {
	for _, terminal := range terminals {
		if terminal.Project == project && terminal.Task == task {
			return terminal, true
		}
	}
	return Terminal{}, false
}

func projectSession(terminals []Terminal, project domain.ProjectID) (string, error) {
	var session string
	for _, terminal := range terminals {
		if terminal.Project != project {
			continue
		}
		if session != "" && session != terminal.Target.Session {
			return "", fmt.Errorf("multiple managed tmux sessions claim project %s", project)
		}
		session = terminal.Target.Session
	}
	return session, nil
}
