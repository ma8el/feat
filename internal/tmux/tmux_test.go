package tmux

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
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
	// width and height are what resize-window has made this window, and are
	// zero for a window nothing has sized.
	width, height int
}

type fakePane struct {
	session   string
	window    string
	id        string
	directory string
	program   string
	dead      bool
	status    string
	signal    string
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
	case "set-window-option":
		return "", f.setWindowOption(args)
	case "resize-window":
		return "", f.resizeWindow(args)
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

// setWindowOption records a window option tmux spells with its own command.
//
// Feat's metadata goes through set-option -w; window-size goes through this
// one, which is how the real tmux names it. The fake keeps the two apart for
// the same reason the adapter does.
func (f *fakeTmux) setWindowOption(args []string) error {
	index := indexOf(args, "-t")
	if index < 0 || len(args) <= index+3 {
		return fmt.Errorf("fake tmux: malformed set-window-option %q", args)
	}
	window, ok := f.windows[args[index+1]]
	if !ok {
		return fmt.Errorf("fake tmux: no window %q to set an option on", args[index+1])
	}
	window.options[args[index+2]] = args[index+3]
	return nil
}

func (f *fakeTmux) resizeWindow(args []string) error {
	window, ok := f.windows[valueAfter(args, "-t")]
	if !ok {
		return fmt.Errorf("fake tmux: no window %q to resize", valueAfter(args, "-t"))
	}
	width, err := strconv.Atoi(valueAfter(args, "-x"))
	if err != nil {
		return fmt.Errorf("fake tmux: resize-window width %q is not a number", valueAfter(args, "-x"))
	}
	height, err := strconv.Atoi(valueAfter(args, "-y"))
	if err != nil {
		return fmt.Errorf("fake tmux: resize-window height %q is not a number", valueAfter(args, "-y"))
	}
	window.width, window.height = width, height
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
			pane.options[optionTask], pane.options[optionRole], "0", pane.signal,
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
	}, Size{})
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
	}, Size{})
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

// TestANewTaskWindowIsSizedBeforeItsProgramStarts pins the order, which is the
// whole of the behaviour: a window sized after its program started is a program
// that has already written its first screen at 80 columns, and those lines never
// reflow.
func TestANewTaskWindowIsSizedBeforeItsProgramStarts(t *testing.T) {
	runner := newFakeTmux()
	backend, _ := New("/runtime/feat/tmux.sock", runner)

	terminal, err := backend.EnsureTask(context.Background(), testProject, testTask, CommandSpec{
		Program: "/usr/bin/yes", Directory: "/work/primary",
	}, Size{Width: 171, Height: 49})
	if err != nil {
		t.Fatalf("EnsureTask: %v", err)
	}

	window := runner.windows[terminal.Target.Window]
	if window == nil || window.width != 171 || window.height != 49 {
		t.Fatalf("window = %+v, want one sized 171x49", window)
	}
	// Pinned, because a window nobody is attached to is otherwise sized back to
	// the server's default the moment tmux next thinks about it.
	if got := window.options[sizeOption]; got != manualSize {
		t.Errorf("%s = %q, want %q", sizeOption, got, manualSize)
	}

	sized, started := firstCall(runner.calls, "resize-window"), firstCall(runner.calls, "respawn-pane")
	if sized < 0 || started < 0 {
		t.Fatalf("resize-window at %d and respawn-pane at %d, want both", sized, started)
	}
	if sized > started {
		t.Errorf("the window was sized at call %d and the program started at call %d, want the size first",
			sized, started)
	}
}

// TestATaskWindowIsLeftAloneWhenTheCallerHasNoSize keeps the fallback honest: a
// daemon that has drawn no terminal yet knows nothing about the screen, and
// guessing at one would be worse than tmux's own default.
func TestATaskWindowIsLeftAloneWhenTheCallerHasNoSize(t *testing.T) {
	runner := newFakeTmux()
	backend, _ := New("/runtime/feat/tmux.sock", runner)

	if _, err := backend.EnsureTask(context.Background(), testProject, testTask, CommandSpec{
		Program: "/usr/bin/yes", Directory: "/work/primary",
	}, Size{}); err != nil {
		t.Fatalf("EnsureTask: %v", err)
	}

	for _, command := range []string{"resize-window", "set-window-option"} {
		if got := countCommand(runner.calls, command); got != 0 {
			t.Errorf("%s ran %d times for a caller with no size, want 0", command, got)
		}
	}
}

// TestAnExistingTaskWindowIsNotResized is the other half. Rediscovery returns a
// terminal that already has a size and, in the resume case, may have a client
// sitting in it: resizing there would reflow a running agent, and could resize
// the terminal of the person watching it.
func TestAnExistingTaskWindowIsNotResized(t *testing.T) {
	runner := newFakeTmux()
	backend, _ := New("/runtime/feat/tmux.sock", runner)
	command := CommandSpec{Program: "/usr/bin/yes", Directory: "/work/primary"}

	if _, err := backend.EnsureTask(context.Background(), testProject, testTask, command,
		Size{Width: 171, Height: 49}); err != nil {
		t.Fatalf("EnsureTask: %v", err)
	}
	before := countCommand(runner.calls, "resize-window")

	if _, err := backend.EnsureTask(context.Background(), testProject, testTask, command,
		Size{Width: 100, Height: 30}); err != nil {
		t.Fatalf("second EnsureTask: %v", err)
	}
	if got := countCommand(runner.calls, "resize-window"); got != before {
		t.Errorf("resize-window ran %d times after rediscovery, want %d", got, before)
	}
}

func TestShellPaneOpensInTheProvidedPrimaryWorkspace(t *testing.T) {
	runner := newFakeTmux()
	backend, _ := New("/runtime/feat/tmux.sock", runner)
	_, err := backend.EnsureTask(context.Background(), testProject, testTask, CommandSpec{
		Program: "/usr/bin/yes", Directory: "/work/primary",
	}, Size{})
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
	}, Size{})
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
	}, Size{})
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
		{Program: "/bin/sh", Directory: "/work", Variables: map[string]string{"": "value"}},
		{Program: "/bin/sh", Directory: "/work", Variables: map[string]string{"NAME=X": "value"}},
		{Program: "/bin/sh", Directory: "/work", Variables: map[string]string{"NAME": "one\nkill-server"}},
		{Program: "/bin/sh", Directory: "/work", Variables: map[string]string{"-e": "value"}},
	} {
		if err := spec.Validate(); err == nil {
			t.Errorf("Validate(%+v) succeeded", spec)
		}
	}
}

// TestTheCommandEnvironmentReachesThePane pins where the entries go.
//
// tmux applies -e to the process it respawns, and the order matters: every flag
// has to precede the program, or tmux reads the program as the value of the
// flag before it and the pane starts something else entirely.
func TestTheCommandEnvironmentReachesThePane(t *testing.T) {
	runner := newFakeTmux()
	backend, err := New("/run/user/501/feat/tmux.sock", runner)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := backend.EnsureTask(context.Background(), testProject, testTask, CommandSpec{
		Program:   "/usr/bin/claude",
		Directory: "/work/primary",
		Variables: map[string]string{"SECOND": "2", "FIRST": "1"},
	}, Size{}); err != nil {
		t.Fatalf("EnsureTask: %v", err)
	}

	var respawn []string
	for _, call := range runner.calls {
		if call[0] == "respawn-pane" {
			respawn = call
		}
	}
	if respawn == nil {
		t.Fatalf("no pane was respawned: %v", runner.calls)
	}

	// Sorted, so the same specification is the same command every time.
	want := []string{"-e", "FIRST=1", "-e", "SECOND=2", "-c", "/work/primary", "/usr/bin/claude"}
	if !strings.Contains(strings.Join(respawn, " "), strings.Join(want, " ")) {
		t.Errorf("respawn-pane = %q, want the sorted environment before the working directory and program", respawn)
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

// firstCall is where a command appears in the order they were issued, or -1.
func firstCall(calls [][]string, command string) int {
	for i, call := range calls {
		if len(call) > 0 && call[0] == command {
			return i
		}
	}
	return -1
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
