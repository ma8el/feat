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
//
// The size is the one a new window is given before its program starts, and is
// the caller's best account of the region the terminal will be drawn into. It
// is ignored when a terminal already exists: that window has a size, and a
// program is running in it that would be told to reflow for no reason.
func (t *Tmux) EnsureTask(
	ctx context.Context, project domain.ProjectID, task domain.TaskID, command CommandSpec, size Size,
) (Terminal, error) {
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

	found, err := t.discover(ctx)
	if err != nil {
		return Terminal{}, err
	}
	if existing, ok := found.Terminal(project, task); ok {
		return existing, nil
	}
	// A task whose terminal was quarantined must not be given a second one: the
	// first still exists, and creating another would make the ambiguity
	// permanent. The damage is what the caller is told about.
	if damage := found.DamageFor(project, task); len(damage) > 0 {
		return Terminal{}, fmt.Errorf("task %s already has a tmux terminal that Feat cannot use: %s",
			task, damage[0].Reason)
	}

	session, err := projectSession(found, project)
	if err != nil {
		return Terminal{}, err
	}

	var created domain.TmuxTarget
	if session == "" {
		created, err = t.createSession(ctx, project, task, command, size)
	} else {
		created, err = t.createWindow(ctx, session, project, task, command, size)
	}
	if err != nil {
		return Terminal{}, err
	}

	// Once all three scopes are tagged, a storage failure must leave the
	// terminal in place: discovery can recover it, while rollback could destroy
	// work already entered in the pane (ADR-030).
	found, err = t.discover(ctx)
	if err != nil {
		return Terminal{}, fmt.Errorf("task terminal %s was created and tagged at %s/%s/%s but could not be rediscovered: %w",
			task, created.Session, created.Window, created.Pane, err)
	}
	terminal, ok := found.Terminal(project, task)
	if !ok {
		return Terminal{}, fmt.Errorf("task terminal %s was created at %s/%s/%s but its Feat metadata was not discoverable%s",
			task, created.Session, created.Window, created.Pane, describeDamage(found.DamageFor(project, task)))
	}
	return terminal, nil
}

// describeDamage appends the reason a just-created terminal was quarantined,
// so a creation that succeeded and then vanished says why rather than only
// that it did.
func describeDamage(damage []Damaged) string {
	if len(damage) == 0 {
		return ""
	}
	return ": " + damage[0].Reason
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

	found, err := t.discover(ctx)
	if err != nil {
		return Terminal{}, err
	}
	terminal, ok := found.Terminal(project, task)
	if !ok {
		return Terminal{}, fmt.Errorf("task %s has no managed tmux terminal on %s%s",
			task, t.socket, describeDamage(found.DamageFor(project, task)))
	}
	if terminal.Shell != nil {
		return terminal, nil
	}

	// -h puts the shell beside the agent rather than below it, which is tmux's
	// default and was never a choice Feat made. Both panes hold wrapped text a
	// user reads, and halving the height of the agent's transcript costs more
	// than halving its width (ADR-041).
	output, err := t.runner.Run(ctx, t.socket, "split-window", "-h", "-d", "-P", "-F", createFormat,
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

	found, err = t.discover(ctx)
	if err != nil {
		return Terminal{}, fmt.Errorf("shell pane %s was tagged but could not be rediscovered: %w", created.Pane, err)
	}
	terminal, ok = found.Terminal(project, task)
	if !ok || terminal.Shell == nil {
		return Terminal{}, fmt.Errorf("shell pane %s was created but its Feat metadata was not discoverable%s",
			created.Pane, describeDamage(found.DamageFor(project, task)))
	}
	return terminal, nil
}

// Restart replaces the program running in a task's agent pane.
//
// It is what a resume needs and what EnsureTask deliberately does not do:
// EnsureTask returns an existing terminal untouched, because repeating a launch
// must not restart an agent that is already working. A resume is the one case
// where the caller means to replace the process, and it means it because a user
// asked.
//
// The pane is kept whatever happens. Unlike a pane created moments ago, this one
// may hold scrollback the user wants — the output of the session that died is
// often the only account of why — so a failure to start the new program leaves
// the terminal in place rather than removing it (ADR-030's retention rule).
func (t *Tmux) Restart(
	ctx context.Context, project domain.ProjectID, task domain.TaskID, command CommandSpec,
) (Terminal, error) {
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

	found, err := t.discover(ctx)
	if err != nil {
		return Terminal{}, err
	}
	terminal, ok := found.Terminal(project, task)
	if !ok {
		return Terminal{}, fmt.Errorf("task %s has no managed tmux terminal on %s to restart%s",
			task, t.socket, describeDamage(found.DamageFor(project, task)))
	}
	if err := t.start(ctx, terminal.Target.Pane, command); err != nil {
		return Terminal{}, err
	}

	found, err = t.discover(ctx)
	if err != nil {
		return Terminal{}, fmt.Errorf("the terminal of task %s was restarted but could not be rediscovered: %w",
			task, err)
	}
	terminal, ok = found.Terminal(project, task)
	if !ok {
		return Terminal{}, fmt.Errorf("the terminal of task %s was restarted but its metadata was not discoverable%s",
			task, describeDamage(found.DamageFor(project, task)))
	}
	return terminal, nil
}

// Find returns the terminal carrying one task's metadata.
func (t *Tmux) Find(ctx context.Context, project domain.ProjectID, task domain.TaskID) (Terminal, bool, error) {
	found, err := t.Discover(ctx)
	if err != nil {
		return Terminal{}, false, err
	}
	terminal, ok := found.Terminal(project, task)
	return terminal, ok, nil
}

// RemoveTask kills the window carrying one task's metadata, and nothing else.
//
// The target is resolved from live metadata rather than from a stored
// identifier, so a window index or display name the user changed cannot make
// this remove somebody else's window. It reports whether there was anything to
// remove, because a cleanup of a terminal that is already gone is a success
// rather than a failure.
//
// A quarantined terminal is deliberately removable: the whole point of
// reporting damage rather than repairing it is that the user decides, and
// "remove it" is one of the decisions available to them.
func (t *Tmux) RemoveTask(ctx context.Context, project domain.ProjectID, task domain.TaskID) (bool, error) {
	if err := project.Validate(); err != nil {
		return false, err
	}
	if err := task.Validate(); err != nil {
		return false, err
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	found, err := t.discover(ctx)
	if err != nil {
		return false, err
	}

	window := ""
	if terminal, ok := found.Terminal(project, task); ok {
		window = terminal.Target.Window
	} else {
		for _, damaged := range found.DamageFor(project, task) {
			if damaged.Kind == DamagedTerminal && windowIDPattern.MatchString(damaged.ID) {
				window = damaged.ID
				break
			}
		}
	}
	if window == "" {
		return false, nil
	}
	if _, err := t.runner.Run(ctx, t.socket, "kill-window", "-t", window); err != nil {
		if errors.Is(err, ErrServerNotRunning) {
			return false, nil
		}
		return false, fmt.Errorf("removing the tmux window of task %s: %w", task, err)
	}
	return true, nil
}

func (t *Tmux) createSession(
	ctx context.Context, project domain.ProjectID, task domain.TaskID, command CommandSpec, size Size,
) (domain.TmuxTarget, error) {
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
	if err := t.sizeBeforeStart(ctx, created.Window, size); err != nil {
		return domain.TmuxTarget{}, errors.Join(err, t.kill(ctx, "kill-session", created.Session))
	}
	if err := t.start(ctx, created.Pane, command); err != nil {
		return domain.TmuxTarget{}, errors.Join(err, t.kill(ctx, "kill-session", created.Session))
	}
	return created, nil
}

func (t *Tmux) createWindow(
	ctx context.Context, session string, project domain.ProjectID, task domain.TaskID,
	command CommandSpec, size Size,
) (domain.TmuxTarget, error) {
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
	if err := t.sizeBeforeStart(ctx, created.Window, size); err != nil {
		return domain.TmuxTarget{}, errors.Join(err, t.kill(ctx, "kill-window", created.Window))
	}
	if err := t.start(ctx, created.Pane, command); err != nil {
		return domain.TmuxTarget{}, errors.Join(err, t.kill(ctx, "kill-window", created.Window))
	}
	return created, nil
}

// sizeBeforeStart gives a window the size its program will be drawn at, while
// there is still no program in it to care.
//
// tmux makes a window nobody is attached to 80x24, and Feat used to leave it
// there until the dashboard drew that task for the first time. Everything the
// agent printed in between — the provider's banner, its "do you trust this
// folder" prompt, the first turn of work — was therefore written into a
// 80-column terminal, and a terminal's committed lines do not reflow when it is
// resized afterwards. They stay 80 columns wide in a region three times that
// for as long as the task is kept.
//
// The later resize is also the only correction there was, which makes it a
// single delivery of SIGWINCH that has to land while the agent happens to be
// listening. Observed on this machine against tmux 3.7b: a task window read
// 171x49 while the agent inside it was still drawing 80-cell rules, and a
// resize to 170 and back to 171 straightened it at once. Sizing here means the
// common case asks for no signal at all — the dashboard's first frame finds the
// window already the size it wanted and changes nothing.
//
// Only a window Feat has this moment created is sized here. An existing one may
// have a client attached, and resizing that window would resize the terminal
// somebody is sitting in.
//
// new-window takes no -x/-y, so this is a resize rather than an argument, and
// it is the same resize the renderer performs: it pins the window, and
// ReleaseWindowSize is what takes the pin off again for a native attach.
func (t *Tmux) sizeBeforeStart(ctx context.Context, window string, size Size) error {
	if !size.Known() {
		return nil
	}
	return t.resizeWindow(ctx, window, size.Width, size.Height)
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
	// Every flag precedes the program, and the environment precedes the working
	// directory: -e takes one value and the program follows -c, so the pane's
	// command stays the last thing on the line and nothing variadic can swallow
	// it (ADR-030).
	args := []string{"respawn-pane", "-k", "-t", pane}
	for _, entry := range command.Entries() {
		args = append(args, "-e", entry)
	}
	args = append(args, "-c", command.Directory, command.Program)
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

// projectSession returns the managed session a project's windows belong in.
//
// It reads the discovered sessions rather than the terminals, because a project
// whose every window was quarantined still has a session: deriving it from
// healthy terminals would answer "none" and give that project a second session,
// which is the conflict quarantine exists to avoid making permanent.
func projectSession(found Discovery, project domain.ProjectID) (string, error) {
	for _, damaged := range found.Damaged {
		if damaged.Kind == DamagedSession && damaged.Project == project {
			return "", fmt.Errorf("project %s cannot be given a task terminal: %s", project, damaged.Reason)
		}
	}
	var session string
	for _, candidate := range found.Sessions {
		if candidate.Project != project {
			continue
		}
		if session != "" && session != candidate.ID {
			return "", fmt.Errorf("multiple managed tmux sessions claim project %s", project)
		}
		session = candidate.ID
	}
	return session, nil
}
