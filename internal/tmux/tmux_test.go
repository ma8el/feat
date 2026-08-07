package tmux

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/ma8el/feat/internal/domain"
)

var (
	testProject = domain.ProjectID("app")
	testTask    = domain.TaskID("12345678-1234-4234-8234-123456789abc")
	// otherTask is a second task of the same project, for the tests whose
	// subject is that one task's terminal is not another's.
	otherTask = domain.TaskID("87654321-4321-4321-8321-cba987654321")
)

type fakeSession struct {
	id      string
	options map[string]string
}

type fakeWindow struct {
	session string
	id      string
	options map[string]string
}

type fakePane struct {
	session   string
	window    string
	id        string
	directory string
	program   string
	dead      bool
	status    string
	options   map[string]string
}

type fakeTmux struct {
	mu sync.Mutex

	calls   [][]string
	sockets []string

	sessions map[string]*fakeSession
	windows  map[string]*fakeWindow
	panes    map[string]*fakePane

	nextWindow  int
	nextPane    int
	failOption  string
	failCommand string
}

func newFakeTmux() *fakeTmux {
	return &fakeTmux{
		sessions:   make(map[string]*fakeSession),
		windows:    make(map[string]*fakeWindow),
		panes:      make(map[string]*fakePane),
		nextWindow: 7,
		nextPane:   11,
	}
}

func (f *fakeTmux) Run(_ context.Context, socket string, args ...string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, append([]string(nil), args...))
	f.sockets = append(f.sockets, socket)

	if len(args) == 0 {
		return "", errors.New("fake tmux: missing command")
	}
	switch args[0] {
	case "list-sessions":
		if len(f.sessions) == 0 {
			return "", ErrServerNotRunning
		}
		return f.listSessions(), nil
	case "list-windows":
		return f.listWindows(), nil
	case "list-panes":
		return f.listPanes(), nil
	case "new-session":
		return f.newSession(args), nil
	case "new-window":
		return f.newWindow(args), nil
	case "split-window":
		return f.splitWindow(args), nil
	case "respawn-pane":
		return "", f.respawnPane(args)
	case "set-option":
		return "", f.setOption(args)
	case "kill-session":
		f.killSession(valueAfter(args, "-t"))
		return "", nil
	case "kill-window":
		f.killWindow(valueAfter(args, "-t"))
		return "", nil
	case "kill-pane":
		delete(f.panes, valueAfter(args, "-t"))
		return "", nil
	default:
		return "", fmt.Errorf("fake tmux: unexpected command %q", strings.Join(args, " "))
	}
}

func (f *fakeTmux) newSession(args []string) string {
	session := "$1"
	window := fmt.Sprintf("@%d", f.nextWindow)
	pane := fmt.Sprintf("%%%d", f.nextPane)
	f.nextWindow++
	f.nextPane++
	f.sessions[session] = &fakeSession{id: session, options: make(map[string]string)}
	f.windows[window] = &fakeWindow{session: session, id: window, options: make(map[string]string)}
	f.panes[pane] = &fakePane{
		session: session, window: window, id: pane,
		directory: valueAfter(args, "-c"), options: make(map[string]string),
	}
	return session + "\t" + window + "\t" + pane
}

func (f *fakeTmux) newWindow(args []string) string {
	session := valueAfter(args, "-t")
	window := fmt.Sprintf("@%d", f.nextWindow)
	pane := fmt.Sprintf("%%%d", f.nextPane)
	f.nextWindow++
	f.nextPane++
	f.windows[window] = &fakeWindow{session: session, id: window, options: make(map[string]string)}
	f.panes[pane] = &fakePane{
		session: session, window: window, id: pane,
		directory: valueAfter(args, "-c"), options: make(map[string]string),
	}
	return session + "\t" + window + "\t" + pane
}

func (f *fakeTmux) splitWindow(args []string) string {
	target := f.panes[valueAfter(args, "-t")]
	pane := fmt.Sprintf("%%%d", f.nextPane)
	f.nextPane++
	f.panes[pane] = &fakePane{
		session: target.session, window: target.window, id: pane,
		directory: valueAfter(args, "-c"), options: make(map[string]string),
	}
	return target.session + "\t" + target.window + "\t" + pane
}

// respawnPane replaces a pane's process the way tmux does: the pane keeps its
// identity and its options, so metadata applied to the holder survives.
func (f *fakeTmux) respawnPane(args []string) error {
	if f.failCommand == "respawn-pane" {
		return errors.New("injected failure starting the command")
	}
	pane, ok := f.panes[valueAfter(args, "-t")]
	if !ok {
		return fmt.Errorf("fake tmux: no pane %q to respawn", valueAfter(args, "-t"))
	}
	command := commandAfter(args)
	if len(command) == 0 {
		return errors.New("fake tmux: respawn-pane without a command")
	}
	pane.directory = valueAfter(args, "-c")
	pane.program = command[0]
	return nil
}

func (f *fakeTmux) setOption(args []string) error {
	target := valueAfter(args, "-t")
	index := indexOf(args, "-t")
	if index < 0 || len(args) <= index+3 {
		return fmt.Errorf("fake tmux: malformed set-option %q", args)
	}
	option, value := args[index+2], args[index+3]
	if option == f.failOption {
		return fmt.Errorf("injected failure setting %s", option)
	}
	switch {
	case contains(args, "-p"):
		f.panes[target].options[option] = value
	case contains(args, "-w"):
		f.windows[target].options[option] = value
	default:
		f.sessions[target].options[option] = value
	}
	return nil
}

func (f *fakeTmux) listSessions() string {
	ids := sortedKeys(f.sessions)
	lines := make([]string, 0, len(ids))
	for _, id := range ids {
		session := f.sessions[id]
		lines = append(lines, strings.Join([]string{
			session.id, session.options[optionManaged], session.options[optionVersion], session.options[optionProject],
		}, "\t"))
	}
	return strings.Join(lines, "\n")
}

func (f *fakeTmux) listWindows() string {
	ids := sortedKeys(f.windows)
	lines := make([]string, 0, len(ids))
	for _, id := range ids {
		window := f.windows[id]
		lines = append(lines, strings.Join([]string{
			window.session, window.id, window.options[optionManaged], window.options[optionVersion],
			window.options[optionProject], window.options[optionTask],
		}, "\t"))
	}
	return strings.Join(lines, "\n")
}

func (f *fakeTmux) listPanes() string {
	ids := sortedKeys(f.panes)
	lines := make([]string, 0, len(ids))
	for _, id := range ids {
		pane := f.panes[id]
		dead := "0"
		if pane.dead {
			dead = "1"
		}
		lines = append(lines, strings.Join([]string{
			pane.session, pane.window, pane.id, dead, pane.status, pane.directory,
			pane.options[optionManaged], pane.options[optionVersion], pane.options[optionProject],
			pane.options[optionTask], pane.options[optionRole],
		}, "\t"))
	}
	return strings.Join(lines, "\n")
}

func (f *fakeTmux) killSession(id string) {
	delete(f.sessions, id)
	for window, record := range f.windows {
		if record.session == id {
			f.killWindow(window)
		}
	}
}

func (f *fakeTmux) killWindow(id string) {
	delete(f.windows, id)
	for pane, record := range f.panes {
		if record.window == id {
			delete(f.panes, pane)
		}
	}
}

func TestTaskTerminalUsesDedicatedSocketAndStableMetadata(t *testing.T) {
	runner := newFakeTmux()
	backend, err := New("/run/user/501/feat/tmux.sock", runner)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	terminal, err := backend.EnsureTask(context.Background(), testProject, testTask, CommandSpec{
		Program: "/usr/bin/yes", Directory: "/work/primary",
	})
	if err != nil {
		t.Fatalf("EnsureTask: %v", err)
	}
	if terminal.Target.Session != "$1" || terminal.Target.Window != "@7" || terminal.Target.Pane != "%11" {
		t.Errorf("target = %+v, want stable tmux IDs", terminal.Target)
	}
	if terminal.Target.Socket != backend.Socket() {
		t.Errorf("socket = %q, want %q", terminal.Target.Socket, backend.Socket())
	}
	for _, socket := range runner.sockets {
		if socket != backend.Socket() {
			t.Errorf("command reached socket %q, want only %q", socket, backend.Socket())
		}
	}
	for _, call := range runner.calls {
		if contains(call, "-L") || contains(call, "-f") {
			t.Errorf("tmux call bypasses the dedicated socket or user config: %q", call)
		}
	}

	// The command starts only after the pane is tagged and remain-on-exit is in
	// place. A pane created with the command can die before its metadata lands,
	// taking the window and the server with it.
	for _, call := range runner.calls {
		if call[0] == "new-session" && len(commandAfter(call)) != 0 {
			t.Errorf("new-session started the command before the pane was tagged: %q", call)
		}
	}
	if pane := runner.panes[terminal.Target.Pane]; pane == nil || pane.program != "/usr/bin/yes" {
		t.Errorf("agent pane = %+v, want the caller's program started in it", pane)
	}

	// Rename and renumber are deliberately absent from identity. A second call
	// discovers metadata and creates nothing new.
	before := countCommand(runner.calls, "new-session")
	again, err := backend.EnsureTask(context.Background(), testProject, testTask, CommandSpec{
		Program: "/bin/false", Directory: "/somewhere/else",
	})
	if err != nil {
		t.Fatalf("second EnsureTask: %v", err)
	}
	if again.Target != terminal.Target {
		t.Errorf("rediscovered target = %+v, want %+v", again.Target, terminal.Target)
	}
	if got := countCommand(runner.calls, "new-session"); got != before {
		t.Errorf("new-session ran %d times after rediscovery, want %d", got, before)
	}
}

func TestShellPaneOpensInTheProvidedPrimaryWorkspace(t *testing.T) {
	runner := newFakeTmux()
	backend, _ := New("/runtime/feat/tmux.sock", runner)
	_, err := backend.EnsureTask(context.Background(), testProject, testTask, CommandSpec{
		Program: "/usr/bin/yes", Directory: "/work/primary",
	})
	if err != nil {
		t.Fatalf("EnsureTask: %v", err)
	}

	terminal, err := backend.EnsureShell(context.Background(), testProject, testTask, CommandSpec{
		Program: "/bin/zsh", Arguments: []string{"-l"}, Directory: "/work/primary",
	})
	if err != nil {
		t.Fatalf("EnsureShell: %v", err)
	}
	if terminal.Shell == nil {
		t.Fatal("shell pane was not discovered")
	}
	if terminal.Shell.Directory != "/work/primary" {
		t.Errorf("shell directory = %q, want /work/primary", terminal.Shell.Directory)
	}
	if got := countCommand(runner.calls, "split-window"); got != 1 {
		t.Errorf("split-window calls = %d, want 1", got)
	}

	// The action is idempotent: a second request attaches to the same pane.
	again, err := backend.EnsureShell(context.Background(), testProject, testTask, CommandSpec{
		Program: "/bin/zsh", Directory: "/work/primary",
	})
	if err != nil {
		t.Fatalf("second EnsureShell: %v", err)
	}
	if again.Shell == nil || again.Shell.ID != terminal.Shell.ID {
		t.Errorf("second shell = %+v, want pane %s", again.Shell, terminal.Shell.ID)
	}
	if got := countCommand(runner.calls, "split-window"); got != 1 {
		t.Errorf("split-window calls after repeat = %d, want 1", got)
	}
}

func TestFailedMetadataApplicationRemovesOnlyTheNewObject(t *testing.T) {
	runner := newFakeTmux()
	runner.failOption = optionTask
	backend, _ := New("/runtime/feat/tmux.sock", runner)

	_, err := backend.EnsureTask(context.Background(), testProject, testTask, CommandSpec{
		Program: "/usr/bin/yes", Directory: "/work/primary",
	})
	if err == nil {
		t.Fatal("EnsureTask succeeded despite the injected metadata failure")
	}
	if len(runner.sessions) != 0 || len(runner.windows) != 0 || len(runner.panes) != 0 {
		t.Errorf("incomplete target remains: sessions=%d windows=%d panes=%d",
			len(runner.sessions), len(runner.windows), len(runner.panes))
	}
	if countCommand(runner.calls, "kill-session") != 1 {
		t.Errorf("calls = %v, want exact new session cleanup", runner.calls)
	}
}

// TestACommandThatCannotBeStartedRemovesOnlyTheNewObject covers the other half
// of the tag-then-start order: the holder pane never ran the caller's command,
// so it holds no work and is removed rather than left for reconciliation.
func TestACommandThatCannotBeStartedRemovesOnlyTheNewObject(t *testing.T) {
	runner := newFakeTmux()
	runner.failCommand = "respawn-pane"
	backend, _ := New("/runtime/feat/tmux.sock", runner)

	_, err := backend.EnsureTask(context.Background(), testProject, testTask, CommandSpec{
		Program: "/usr/bin/yes", Directory: "/work/primary",
	})
	if err == nil {
		t.Fatal("EnsureTask succeeded despite the injected start failure")
	}
	if !strings.Contains(err.Error(), "/usr/bin/yes") {
		t.Errorf("error = %v, want the command that could not be started", err)
	}
	if len(runner.sessions) != 0 || len(runner.windows) != 0 || len(runner.panes) != 0 {
		t.Errorf("incomplete target remains: sessions=%d windows=%d panes=%d",
			len(runner.sessions), len(runner.windows), len(runner.panes))
	}
}

func TestProcessObservationDoesNotInferCompletion(t *testing.T) {
	status := 17
	terminal := Terminal{Agent: Pane{Dead: true, ExitStatus: &status}}
	if got := terminal.ProcessState(); got != domain.ProcessFailed {
		t.Errorf("ProcessState = %q, want failed", got)
	}
	status = 0
	if got := terminal.ProcessState(); got != domain.ProcessStopped {
		t.Errorf("ProcessState = %q, want stopped", got)
	}
	terminal.Agent.Dead = false
	if got := terminal.ProcessState(); got != domain.ProcessRunning {
		t.Errorf("ProcessState = %q, want running", got)
	}
}

func TestCommandSpecRejectsValuesTmuxCouldInterpret(t *testing.T) {
	for _, spec := range []CommandSpec{
		{Program: "", Directory: "/work"},
		{Program: "-e", Directory: "/work"},
		{Program: "/bin/sh", Directory: "relative"},
		{Program: "/bin/sh\nkill-server", Directory: "/work"},
		{Program: "/bin/sh", Arguments: []string{"ok\nbad"}, Directory: "/work"},
	} {
		if err := spec.Validate(); err == nil {
			t.Errorf("Validate(%+v) succeeded", spec)
		}
	}
}

func valueAfter(args []string, name string) string {
	index := indexOf(args, name)
	if index < 0 || index+1 >= len(args) {
		return ""
	}
	return args[index+1]
}

func indexOf(args []string, value string) int {
	for i, argument := range args {
		if argument == value {
			return i
		}
	}
	return -1
}

func contains(args []string, value string) bool { return indexOf(args, value) >= 0 }

// commandAfter returns the program and arguments that follow the "-c <dir>"
// pair, which is where every creation and respawn vector puts them.
func commandAfter(args []string) []string {
	index := indexOf(args, "-c")
	if index < 0 || index+2 > len(args) {
		return nil
	}
	return args[index+2:]
}

func countCommand(calls [][]string, command string) int {
	count := 0
	for _, call := range calls {
		if len(call) > 0 && call[0] == command {
			count++
		}
	}
	return count
}

func sortedKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
