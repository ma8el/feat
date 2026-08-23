package tmux

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/integrationtest"
)

func requireTmux(t *testing.T) {
	t.Helper()
	if !integrationtest.Enabled() {
		t.Skipf("set %s=1 to run tests against real tmux", integrationtest.Env)
	}
	if _, err := exec.LookPath(Executable); err != nil {
		integrationtest.Unavailable(t, integrationtest.Tmux, "tmux is not installed")
	}
}

type realServer struct {
	dir      string
	socket   string
	ordinary string
	runner   HostRunner
}

func realTmux(t *testing.T) *realServer {
	t.Helper()
	requireTmux(t)

	dir, err := os.MkdirTemp("", "feat-tmux")
	if err != nil {
		t.Fatalf("creating a short tmux test directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	// The managed server must load normal user configuration. These values are
	// intentionally hostile to any implementation that assumes pane 0 or
	// window 0, while automatic rename makes the display name non-authoritative.
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatalf("creating test home: %v", err)
	}
	configuration := "set-option -g base-index 7\n" +
		"set-option -g pane-base-index 4\n" +
		"set-option -g automatic-rename on\n"
	if err := os.WriteFile(filepath.Join(home, ".tmux.conf"), []byte(configuration), 0o600); err != nil {
		t.Fatalf("writing tmux config: %v", err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	server := &realServer{
		dir:      dir,
		socket:   filepath.Join(dir, "tmux.sock"),
		ordinary: filepath.Join(dir, "ordinary.sock"),
		runner:   HostRunner{Timeout: 10 * time.Second},
	}
	t.Cleanup(func() {
		_, _ = server.runner.Run(context.Background(), server.socket, "kill-server")
		_, _ = server.runner.Run(context.Background(), server.ordinary, "kill-server")
	})
	return server
}

// TestRealManagedServerIsIsolatedAndIdentitySurvivesUserChanges covers three
// rules against tmux itself: the dedicated socket does not collide with another
// server, user indexes and names do not become identity, and a fresh adapter
// rediscovers the tagged objects after a daemon-equivalent restart.
func TestRealManagedServerIsIsolatedAndIdentitySurvivesUserChanges(t *testing.T) {
	server := realTmux(t)
	ctx := context.Background()

	if _, err := server.runner.Run(ctx, server.ordinary,
		"new-session", "-d", "-s", "feat-app", "/usr/bin/yes"); err != nil {
		t.Fatalf("creating ordinary session: %v", err)
	}

	backend, err := New(server.socket, server.runner)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	terminal, err := backend.EnsureTask(ctx, testProject, testTask, CommandSpec{
		Program: "/usr/bin/yes", Directory: server.dir,
	})
	if err != nil {
		t.Fatalf("EnsureTask: %v", err)
	}

	if base, err := server.runner.Run(ctx, server.socket, "show-options", "-gv", "base-index"); err != nil {
		t.Fatalf("reading user base-index: %v", err)
	} else if base != "7" {
		t.Errorf("base-index = %q, want user-configured 7", base)
	}
	if paneBase, err := server.runner.Run(ctx, server.socket, "show-options", "-gv", "pane-base-index"); err != nil {
		t.Fatalf("reading user pane-base-index: %v", err)
	} else if paneBase != "4" {
		t.Errorf("pane-base-index = %q, want user-configured 4", paneBase)
	}

	if _, err := server.runner.Run(ctx, server.socket, "rename-session", "-t", terminal.Target.Session, "user-renamed"); err != nil {
		t.Fatalf("renaming session: %v", err)
	}
	if _, err := server.runner.Run(ctx, server.socket, "rename-window", "-t", terminal.Target.Window, "user-window"); err != nil {
		t.Fatalf("renaming window: %v", err)
	}
	if _, err := server.runner.Run(ctx, server.socket, "move-window", "-s", terminal.Target.Window, "-t", "user-renamed:42"); err != nil {
		t.Fatalf("renumbering window: %v", err)
	}
	if _, err := server.runner.Run(ctx, server.socket,
		"split-window", "-d", "-t", terminal.Target.Pane, "/usr/bin/yes"); err != nil {
		t.Fatalf("creating an untagged user pane: %v", err)
	}

	restarted, _ := New(server.socket, server.runner)
	discovered, err := restarted.Discover(ctx)
	if err != nil {
		t.Fatalf("Discover after restart: %v", err)
	}
	if len(discovered.Terminals) != 1 {
		t.Fatalf("discovered %d terminals, want 1", len(discovered.Terminals))
	}
	if discovered.Terminals[0].Target != terminal.Target {
		t.Errorf("target after rename/renumber = %+v, want %+v", discovered.Terminals[0].Target, terminal.Target)
	}
	if len(discovered.Damaged) != 0 {
		t.Errorf("discovery quarantined %d objects, want none: %+v", len(discovered.Damaged), discovered.Damaged)
	}

	ordinary, err := server.runner.Run(ctx, server.ordinary, "list-sessions", "-F", "#{session_name}")
	if err != nil {
		t.Fatalf("ordinary server disappeared: %v", err)
	}
	if ordinary != "feat-app" {
		t.Errorf("ordinary sessions = %q, want feat-app", ordinary)
	}
}

// TestRealShellUsesPrimaryWorkspace verifies tmux's -c behavior rather than a
// fake runner's assumption about it.
func TestRealShellUsesPrimaryWorkspace(t *testing.T) {
	server := realTmux(t)
	primary := filepath.Join(server.dir, "primary")
	if err := os.MkdirAll(primary, 0o700); err != nil {
		t.Fatalf("creating primary workspace: %v", err)
	}

	backend, _ := New(server.socket, server.runner)
	if _, err := backend.EnsureTask(context.Background(), testProject, testTask, CommandSpec{
		Program: "/usr/bin/yes", Directory: primary,
	}); err != nil {
		t.Fatalf("EnsureTask: %v", err)
	}
	terminal, err := backend.EnsureShell(context.Background(), testProject, testTask, CommandSpec{
		Program: "/usr/bin/yes", Directory: primary,
	})
	if err != nil {
		t.Fatalf("EnsureShell: %v", err)
	}
	canonical, err := filepath.EvalSymlinks(primary)
	if err != nil {
		t.Fatalf("resolving primary workspace: %v", err)
	}
	if terminal.Shell == nil || terminal.Shell.Directory != canonical {
		t.Errorf("shell = %+v, want directory %s", terminal.Shell, canonical)
	}
}

// TestRealCommandThatExitsImmediatelyStaysObservable is the regression test for
// the tag-then-start order.
//
// A program that fails at once is the ordinary first-run failure: the agent
// binary is missing, or its configuration is rejected. Against real tmux, a
// pane created with such a command dies before any option can be set, which
// destroys the window and — for the first task — the whole server. The failure
// then surfaces as "the dedicated tmux server is not running", which is both
// wrong and unactionable. Creating the pane first has to turn that into an
// observable dead pane carrying its exit status.
func TestRealCommandThatExitsImmediatelyStaysObservable(t *testing.T) {
	server := realTmux(t)
	ctx := context.Background()
	backend, err := New(server.socket, server.runner)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := backend.EnsureTask(ctx, testProject, testTask, CommandSpec{
		Program: "/usr/bin/false", Directory: server.dir,
	}); err != nil {
		t.Fatalf("EnsureTask with a command that exits immediately: %v", err)
	}

	terminal := awaitDeadAgent(t, backend)
	if state := terminal.ProcessState(); state != domain.ProcessFailed {
		t.Errorf("process state = %q, want failed", state)
	}
	if terminal.Agent.ExitStatus == nil || *terminal.Agent.ExitStatus != 1 {
		t.Errorf("exit status = %v, want 1", terminal.Agent.ExitStatus)
	}

	// The shell seam splits from a dead agent pane and takes the same route.
	shell, err := backend.EnsureShell(ctx, testProject, testTask, CommandSpec{
		Program: "/usr/bin/false", Directory: server.dir,
	})
	if err != nil {
		t.Fatalf("EnsureShell with a command that exits immediately: %v", err)
	}
	if shell.Shell == nil {
		t.Fatal("shell pane did not survive its command")
	}
}

// awaitDeadAgent waits for tmux to report the agent process as dead. The
// command exits on its own schedule, so the assertion is on the state tmux
// settles into rather than on the state immediately after creation.
func awaitDeadAgent(t *testing.T, backend *Tmux) Terminal {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for {
		terminal, found, err := backend.Find(context.Background(), testProject, testTask)
		if err != nil {
			t.Fatalf("Find: %v", err)
		}
		if !found {
			t.Fatal("the task terminal disappeared when its command exited")
		}
		if terminal.Agent.Dead {
			return terminal
		}
		if time.Now().After(deadline) {
			t.Fatalf("agent pane never reported its exit: %+v", terminal.Agent)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestRealDetachEndsTheNativeClient exercises attach and detach through a real
// tmux control-mode client. Control mode avoids needing a human terminal while
// retaining tmux's attach-session and detach-client lifecycle.
func TestRealDetachEndsTheNativeClient(t *testing.T) {
	server := realTmux(t)
	backend, _ := New(server.socket, server.runner)
	terminal, err := backend.EnsureTask(context.Background(), testProject, testTask, CommandSpec{
		Program: "/usr/bin/yes", Directory: server.dir,
	})
	if err != nil {
		t.Fatalf("EnsureTask: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	target := terminal.Target.Session + ":" + terminal.Target.Window + "." + terminal.Target.Pane
	command := exec.CommandContext(ctx, Executable, "-C", "-S", server.socket, "attach-session", "-t", target)
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatalf("attach stdin: %v", err)
	}
	defer func() { _ = stdin.Close() }()
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	command.Env = withoutTmux(command.Environ())
	if err := command.Start(); err != nil {
		t.Fatalf("starting attach: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		clients, listErr := server.runner.Run(ctx, server.socket, "list-clients", "-t", terminal.Target.Session)
		if listErr == nil && clients != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("attach client did not appear: %v\n%s", listErr, output.String())
		}
		time.Sleep(10 * time.Millisecond)
	}

	if _, err := server.runner.Run(ctx, server.socket, "detach-client", "-s", terminal.Target.Session); err != nil {
		t.Fatalf("detaching client: %v", err)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("attach did not return cleanly after detach: %v\n%s", err, output.String())
	}
}

// TestRealAnAttachedClientGetsItsOwnSizeBack is the reported defect against tmux
// itself: attaching to a task and finding the agent drawn into part of the
// terminal, with tmux's fill characters over the rest of it.
//
// A rendering sizes the window to the dashboard's main region, and tmux holds a
// sized window there however large the client attaching is. Releasing the size
// as the attach target is handed out is not enough on its own, because a frame
// drawn between that release and the client arriving is told nobody is attached
// and pins the window again. This is the other half: the first frame that finds
// a client on a window Feat pinned gives the size back.
//
// The first part measures what tmux does, so the assertion afterwards is about
// Feat's choice rather than about a claim about some version's behaviour.
func TestRealAnAttachedClientGetsItsOwnSizeBack(t *testing.T) {
	server := realTmux(t)
	backend, _ := New(server.socket, server.runner)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	terminal, err := backend.EnsureTask(ctx, testProject, testTask, CommandSpec{
		Program: "/bin/sh", Arguments: []string{"-c", "sleep 60"}, Directory: server.dir,
	})
	if err != nil {
		t.Fatalf("EnsureTask: %v", err)
	}
	window, pane := terminal.Target.Window, terminal.Target.Pane

	// The dashboard draws the pane into its main region, which pins the window.
	const regionWidth, regionHeight = 87, 21
	if _, err := backend.RenderPane(ctx, window, pane, regionWidth, regionHeight, false); err != nil {
		t.Fatalf("RenderPane for the dashboard: %v", err)
	}

	// And a user attaches, in a terminal larger than that region.
	const clientWidth, clientHeight = 132, 40
	client, output := attachClient(ctx, t, server, terminal, clientWidth, clientHeight)
	defer client()
	waitForWatched(ctx, t, backend, terminal.Task, true, output)

	// What tmux does with the pin still on: the client's terminal is not the
	// window's size, which is the blank space the user reported.
	held, err := backend.CapturePane(ctx, pane)
	if err != nil {
		t.Fatalf("CapturePane while pinned: %v", err)
	}
	if held.Width >= clientWidth {
		// The precondition this test needs is a behaviour of the installed tmux,
		// and a tmux that does not produce it leaves the repair below unproven.
		// Through the demand rather than as a bare skip: a run that asked for tmux
		// and got one this test cannot use has not made the proof, and a skipped
		// package still prints "ok".
		integrationtest.Unavailable(t, integrationtest.Tmux,
			"this tmux gave a pinned window to its client anyway (%d cells), so the blank space "+
				"this checks the repair of cannot be arranged on it", held.Width)
	}

	// The frame the dashboard draws next is what repairs it.
	frame, err := backend.RenderPane(ctx, window, pane, regionWidth, regionHeight, true)
	if err != nil {
		t.Fatalf("RenderPane for a watched window: %v", err)
	}
	if frame.Width != clientWidth || frame.Height != clientHeight {
		t.Errorf("the attached client was left with a %dx%d window, want its own %dx%d",
			frame.Width, frame.Height, clientWidth, clientHeight)
	}
	// And the option is off rather than set to some value of Feat's, so the
	// window keeps following the client from here on.
	option, err := server.runner.Run(ctx, server.socket, "show-window-options", "-t", window, "window-size")
	if err != nil {
		t.Fatalf("reading the released window's sizing: %v", err)
	}
	if strings.Contains(option, "manual") {
		t.Errorf("the window is still pinned against its client: %q", strings.TrimSpace(option))
	}
}

// attachClient attaches a real control-mode client of a given size and returns
// the function that detaches it.
//
// Control mode avoids needing a human terminal while keeping tmux's attach
// lifecycle. Such a client has no window size of its own, so it is given one:
// refresh-client is how tmux itself lets a control client say how big it is, and
// a size is the whole point of these tests.
func attachClient(
	ctx context.Context, t *testing.T, server *realServer, terminal Terminal, width, height int,
) (func(), *bytes.Buffer) {
	t.Helper()

	target := terminal.Target.Session + ":" + terminal.Target.Window + "." + terminal.Target.Pane
	client := exec.CommandContext(ctx, Executable, "-C", "-S", server.socket, "attach-session", "-t", target)
	stdin, err := client.StdinPipe()
	if err != nil {
		t.Fatalf("attach stdin: %v", err)
	}
	output := &bytes.Buffer{}
	client.Stdout, client.Stderr = output, output
	client.Env = withoutTmux(client.Environ())
	if err := client.Start(); err != nil {
		t.Fatalf("starting attach: %v", err)
	}
	detach := func() {
		_, _ = server.runner.Run(context.WithoutCancel(ctx), server.socket,
			"detach-client", "-s", terminal.Target.Session)
		_ = stdin.Close()
		_ = client.Wait()
	}

	name := ""
	deadline := time.Now().Add(5 * time.Second)
	for name == "" {
		if time.Now().After(deadline) {
			detach()
			t.Fatalf("attach client did not appear\n%s", output.String())
		}
		listed, err := server.runner.Run(ctx, server.socket, "list-clients", "-F", "#{client_name}")
		if err == nil {
			name = strings.TrimSpace(listed)
		}
		if name == "" {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if _, err := server.runner.Run(ctx, server.socket, "refresh-client",
		"-t", name, "-C", strconv.Itoa(width)+"x"+strconv.Itoa(height)); err != nil {
		detach()
		t.Fatalf("sizing the attached client: %v\n%s", err, output.String())
	}
	return detach, output
}

func withoutTmux(environment []string) []string {
	out := make([]string, 0, len(environment))
	for _, entry := range environment {
		if strings.HasPrefix(entry, "TMUX=") || strings.HasPrefix(entry, "TMUX_PANE=") {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// TestRealWindowClientsFollowTheUser checks the observation notification
// suppression turns on.
//
// The question is "is the user looking at this task's terminal", and it has to
// be answered per window: a user attached to a project's session is looking at
// one of its tasks and not at the others. tmux answers it with
// window_active_clients, which this measures against a real client and requires
// to follow a window switch — because a signal that lagged behind the user would
// silence the task they had just left (ADR-035).
func TestRealWindowClientsFollowTheUser(t *testing.T) {
	server := realTmux(t)
	backend, _ := New(server.socket, server.runner)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	watched, err := backend.EnsureTask(ctx, testProject, testTask, CommandSpec{
		Program: "/bin/sh", Arguments: []string{"-c", "sleep 60"}, Directory: server.dir,
	})
	if err != nil {
		t.Fatalf("EnsureTask: %v", err)
	}
	other, err := backend.EnsureTask(ctx, testProject, otherTask, CommandSpec{
		Program: "/bin/sh", Arguments: []string{"-c", "sleep 60"}, Directory: server.dir,
	})
	if err != nil {
		t.Fatalf("EnsureTask for a second task: %v", err)
	}

	// Nobody is attached, so nobody is watching either task.
	for _, terminal := range mustDiscover(ctx, t, backend) {
		if terminal.Watched() {
			t.Fatalf("task %s is reported as watched with no client attached", terminal.Task)
		}
	}

	target := watched.Target.Session + ":" + watched.Target.Window + "." + watched.Target.Pane
	client := exec.CommandContext(ctx, Executable, "-C", "-S", server.socket, "attach-session", "-t", target)
	stdin, err := client.StdinPipe()
	if err != nil {
		t.Fatalf("attach stdin: %v", err)
	}
	defer func() { _ = stdin.Close() }()
	var output bytes.Buffer
	client.Stdout, client.Stderr = &output, &output
	client.Env = withoutTmux(client.Environ())
	if err := client.Start(); err != nil {
		t.Fatalf("starting attach: %v", err)
	}
	defer func() {
		_, _ = server.runner.Run(context.Background(), server.socket, "detach-client", "-s", watched.Target.Session)
		_ = client.Wait()
	}()

	// The client attached to one task's window. That task is watched and the
	// other one is not, which is the whole distinction.
	waitForWatched(ctx, t, backend, watched.Task, true, &output)
	for _, terminal := range mustDiscover(ctx, t, backend) {
		if terminal.Task == other.Task && terminal.Watched() {
			t.Fatalf("the second task is reported as watched while the client is on the first")
		}
	}

	// The user switches to the other task. The answer follows them.
	if _, err := server.runner.Run(ctx, server.socket, "select-window", "-t", other.Target.Window); err != nil {
		t.Fatalf("selecting the other task's window: %v", err)
	}
	waitForWatched(ctx, t, backend, other.Task, true, &output)
	waitForWatched(ctx, t, backend, watched.Task, false, &output)
}

// mustDiscover returns every managed terminal or fails the test.
func mustDiscover(ctx context.Context, t *testing.T, backend *Tmux) []Terminal {
	t.Helper()

	found, err := backend.Discover(ctx)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	return found.Terminals
}

// waitForWatched waits until a task's window reports the expected viewer state.
//
// tmux applies an attach and a window switch asynchronously to the client, so
// the state is polled rather than read once.
func waitForWatched(
	ctx context.Context, t *testing.T, backend *Tmux, task domain.TaskID, want bool, output *bytes.Buffer,
) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for {
		for _, terminal := range mustDiscover(ctx, t, backend) {
			if terminal.Task == task && terminal.Watched() == want {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("task %s watched = %v was never reported\n%s", task, want, output.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestRealTerminalsWorkWithoutALocale pins the defect an end-to-end review run
// found in this adapter.
//
// A tmux client whose locale is not UTF-8 replaces every non-printable character
// in the output of `-F` with an underscore, and every format this package uses
// is tab-separated. Without the flag that forces UTF-8 output, creating a
// terminal cannot parse the identifiers of the terminal it just created and
// discovery finds nothing — so a daemon started by a service manager, which is
// how a daemon is meant to run, could not launch a single task.
//
// The environment is emptied of every locale variable rather than set to a wrong
// one, because that is what a sanitised environment looks like.
func TestRealTerminalsWorkWithoutALocale(t *testing.T) {
	server := realTmux(t)
	ctx := context.Background()

	for _, name := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatalf("unsetting %s: %v", name, err)
		}
	}

	adapter, err := New(server.socket, server.runner)
	if err != nil {
		t.Fatalf("building the adapter: %v", err)
	}

	terminal, err := adapter.EnsureTask(ctx, testProject, testTask,
		CommandSpec{Program: "/bin/sh", Arguments: []string{"-c", "sleep 30"}, Directory: server.dir})
	if err != nil {
		t.Fatalf("creating a terminal without a locale: %v", err)
	}
	if !sessionIDPattern.MatchString(terminal.Target.Session) ||
		!windowIDPattern.MatchString(terminal.Target.Window) ||
		!paneIDPattern.MatchString(terminal.Target.Pane) {
		t.Fatalf("the created terminal is %+v, want stable tmux identifiers", terminal.Target)
	}

	// And what was created can be found again, which is the half a substituted
	// separator breaks silently: discovery would return nothing and every task
	// would look like a task whose terminal had gone.
	found, ok, err := adapter.Find(ctx, testProject, testTask)
	if err != nil {
		t.Fatalf("discovering without a locale: %v", err)
	}
	if !ok {
		t.Fatal("a terminal created without a locale cannot be discovered")
	}
	if found.Target != terminal.Target {
		t.Errorf("discovery found %+v, want the created %+v", found.Target, terminal.Target)
	}
	// The tail rather than the whole path: a temporary directory on macOS is
	// reached through a symbolic link, and what this checks is that the field
	// after the separator survived rather than what the kernel calls it.
	if !strings.HasSuffix(found.Agent.Directory, filepath.Base(server.dir)) {
		t.Errorf("the discovered working directory is %q, want the created %q", found.Agent.Directory, server.dir)
	}
}

// TestRealPaneCaptureAndInputRoundTrip is ADR-042's mechanics against real
// tmux.
//
// Every command here was chosen from a claim about tmux's behaviour, and each
// claim is what this checks: that a capture returns what the program printed
// with its colour intact, that measurements arrive as five fields, that keys
// sent after a terminator reach the program, that a bracketed paste arrives and
// takes its buffer away with it, and that a manually sized window keeps the size
// Feat asked for. Without this the unit tests only prove Feat builds the
// argument vectors it means to.
func TestRealPaneCaptureAndInputRoundTrip(t *testing.T) {
	server := realTmux(t)
	ctx := context.Background()

	backend, err := New(server.socket, server.runner)
	if err != nil {
		t.Fatalf("building the adapter: %v", err)
	}
	terminal, err := backend.EnsureTask(ctx, testProject, testTask, CommandSpec{
		Program: "/bin/sh", Directory: server.dir,
	})
	if err != nil {
		t.Fatalf("EnsureTask: %v", err)
	}
	pane := terminal.Target.Pane

	// A size Feat chose rather than one a client imposed, which is what a pane
	// drawn into a region needs.
	if err := backend.ResizeWindow(ctx, terminal.Target.Window, 100, 30); err != nil {
		t.Fatalf("ResizeWindow: %v", err)
	}

	if err := backend.SendKeys(ctx, pane, "printf '\\033[32mgreen\\033[m\\n'", "Enter"); err != nil {
		t.Fatalf("SendKeys: %v", err)
	}
	if err := backend.TypeText(ctx, pane, "echo pasted-by-feat"); err != nil {
		t.Fatalf("TypeText: %v", err)
	}
	if err := backend.SendKeys(ctx, pane, "Enter"); err != nil {
		t.Fatalf("SendKeys after paste: %v", err)
	}

	var frame PaneFrame
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		frame, err = backend.CapturePane(ctx, pane)
		if err != nil {
			t.Fatalf("CapturePane: %v", err)
		}
		if strings.Contains(strings.Join(frame.Content, "\n"), "pasted-by-feat") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	body := strings.Join(frame.Content, "\n")
	if !strings.Contains(body, "pasted-by-feat") {
		t.Errorf("the pasted command never reached the pane:\n%s", body)
	}
	if !strings.Contains(body, "\x1b[32m") {
		t.Errorf("the capture lost the colour tmux had rendered:\n%q", body)
	}
	if frame.Width != 100 || frame.Height != 30 {
		t.Errorf("the pane is %dx%d, want the 100x30 Feat set", frame.Width, frame.Height)
	}
	if frame.Dead {
		t.Error("a live shell was reported as dead")
	}

	// A window with two panes must come back as two panes that tile it, which is
	// what the dashboard composes. Capturing one and drawing it into a region
	// sized for the window fills half of it.
	if _, err := backend.EnsureShell(ctx, testProject, testTask, CommandSpec{
		Program: "/bin/sh", Directory: server.dir,
	}); err != nil {
		t.Fatalf("EnsureShell: %v", err)
	}
	measured, err := server.runner.Run(ctx, server.socket, "display-message", "-p",
		"-t", terminal.Target.Window, "#{window_width}")
	if err != nil {
		t.Fatalf("measuring the window: %v", err)
	}
	windowWidth, err := strconv.Atoi(strings.TrimSpace(measured))
	if err != nil {
		t.Fatalf("window width %q: %v", measured, err)
	}
	half, err := backend.CapturePane(ctx, pane)
	if err != nil {
		t.Fatalf("CapturePane: %v", err)
	}
	if half.Width >= windowWidth {
		t.Fatalf("a split window gave the agent %d of %d cells, so this proves nothing",
			half.Width, windowWidth)
	}

	// Zooming is what makes the dashboard's one pane fill the region.
	if err := backend.ZoomPane(ctx, pane); err != nil {
		t.Fatalf("ZoomPane: %v", err)
	}
	whole, err := backend.CapturePane(ctx, pane)
	if err != nil {
		t.Fatalf("CapturePane after zooming: %v", err)
	}
	if whole.Width != windowWidth {
		t.Errorf("the zoomed pane is %d cells of a %d-cell window", whole.Width, windowWidth)
	}

	// Repeating it must not toggle the zoom back off, which a bare resize-pane
	// -Z would do on every poll.
	if err := backend.ZoomPane(ctx, pane); err != nil {
		t.Fatalf("ZoomPane again: %v", err)
	}
	again, err := backend.CapturePane(ctx, pane)
	if err != nil {
		t.Fatalf("CapturePane after a second zoom: %v", err)
	}
	if again.Width != windowWidth {
		t.Errorf("zooming twice unzoomed the pane: %d cells", again.Width)
	}

	// And a user attaching gets their shell back.
	if err := backend.UnzoomWindow(ctx, terminal.Target.Window); err != nil {
		t.Fatalf("UnzoomWindow: %v", err)
	}
	restored, err := backend.CapturePane(ctx, pane)
	if err != nil {
		t.Fatalf("CapturePane after unzooming: %v", err)
	}
	if restored.Width >= windowWidth {
		t.Errorf("unzooming left the pane filling the window: %d cells", restored.Width)
	}

	// Rendering pins the window. A native client attaching afterwards must get
	// its own size back, which is the regression a real attach showed: the
	// dashboard's main region became the size of the whole terminal.
	if err := backend.ReleaseWindowSize(ctx, terminal.Target.Window); err != nil {
		t.Fatalf("ReleaseWindowSize: %v", err)
	}
	// What matters is the option rather than the size: the window keeps its
	// pinned dimensions until a client arrives, and tmux resizes it then. A
	// release that resized here would have re-pinned it — resize-window -A sets
	// window-size back to manual, which is how the first attempt at this made a
	// native attach smaller than the defect it was fixing.
	option, err := server.runner.Run(ctx, server.socket, "show-window-options",
		"-t", terminal.Target.Window, "window-size")
	if err != nil {
		t.Fatalf("reading the released window's sizing: %v", err)
	}
	if strings.Contains(option, "manual") {
		t.Errorf("the window is still pinned after release: %q", strings.TrimSpace(option))
	}

	// The paste must not stay in the user's buffer stack.
	buffers, err := server.runner.Run(ctx, server.socket, "list-buffers")
	if err == nil && strings.Contains(buffers, inputBuffer) {
		t.Errorf("Feat's input buffer outlived the paste: %s", buffers)
	}
}

// TestRealAStoppedPaneKeepsTheScreenItStoppedOn is the reported glitch against
// tmux itself: an agent's prompt drawn over two rows in the terminal tab, for
// tasks whose agent had stopped.
//
// tmux reflows a pane when its width changes, and a pane whose program has ended
// has nobody to repaint it, so the reflow is the last word. Feat's own
// remain-on-exit is what keeps such a pane readable (ADR-030), and rendering it
// used to size its window to the dashboard's region — which took the retained
// screen apart. The first half of this measures the reflow, so that the second
// half is a check on Feat rather than on a claim about tmux.
func TestRealAStoppedPaneKeepsTheScreenItStoppedOn(t *testing.T) {
	server := realTmux(t)
	ctx := context.Background()

	backend, err := New(server.socket, server.runner)
	if err != nil {
		t.Fatalf("building the adapter: %v", err)
	}
	// A prompt as wide as an agent's own, printed by a command that then ends. It
	// is the shape of what a stopped agent leaves behind: a full-width box nothing
	// will draw again.
	//
	// Three of them, because tmux scrolls the screen by one row to write its own
	// "Pane is dead" line under it: a prompt printed on the first row would be in
	// the history rather than on the screen, and a capture reads the screen.
	prompt := "│ > " + strings.Repeat(" ", 66) + "│"
	terminal, err := backend.EnsureTask(ctx, testProject, testTask, CommandSpec{
		Program:   "/bin/sh",
		Arguments: []string{"-c", "printf '%s\\n'" + strings.Repeat(" '"+prompt+"'", 3)},
		Directory: server.dir,
	})
	if err != nil {
		t.Fatalf("EnsureTask: %v", err)
	}
	window, pane := terminal.Target.Window, terminal.Target.Pane
	awaitDeadAgent(t, backend)

	painted, err := backend.CapturePane(ctx, pane)
	if err != nil {
		t.Fatalf("CapturePane: %v", err)
	}
	if !slices.Contains(painted.Content, prompt) {
		t.Fatalf("the pane is %d cells and never held the whole %d-cell prompt, "+
			"so this proves nothing:\n%q", painted.Width, len([]rune(prompt)), painted.Content)
	}
	// The height stays where it is throughout. Taking rows away from a dead pane
	// pushes the screen into the history, which is a different loss from the one
	// this is about.
	region := painted.Height
	narrow := len([]rune(prompt)) - 6

	// What tmux does to it, measured here so that the assertions below are about
	// Feat's choice rather than about a claim about some version's behaviour.
	if err := backend.ResizeWindow(ctx, window, narrow, region); err != nil {
		t.Fatalf("ResizeWindow smaller: %v", err)
	}
	reflowed, err := backend.CapturePane(ctx, pane)
	if err != nil {
		t.Fatalf("CapturePane after shrinking: %v", err)
	}
	if slices.Contains(reflowed.Content, prompt) {
		// Same shape as the pinned-window measurement above: the loss this test
		// checks Feat prevents is one the installed tmux has to be able to inflict
		// before there is anything to prevent.
		integrationtest.Unavailable(t, integrationtest.Tmux,
			"this tmux does not reflow a dead pane, so the loss this checks the prevention of "+
				"cannot be arranged on it")
	}
	if err := backend.ResizeWindow(ctx, window, painted.Width, region); err != nil {
		t.Fatalf("ResizeWindow back: %v", err)
	}

	// And now the same shrink through the call the dashboard actually makes.
	frame, err := backend.RenderPane(ctx, window, pane, narrow, region, false)
	if err != nil {
		t.Fatalf("RenderPane: %v", err)
	}
	if !slices.Contains(frame.Content, prompt) {
		t.Errorf("rendering a stopped pane into a narrower region took its screen apart:\n%q", frame.Content)
	}
	if frame.Width != painted.Width {
		t.Errorf("the frame reports %d cells, want the %d the pane stopped at", frame.Width, painted.Width)
	}

	// A region with room for it is the repair: tmux rejoins what it split, so a
	// screen an earlier Feat already wrapped comes back whole.
	if err := backend.ResizeWindow(ctx, window, narrow, region); err != nil {
		t.Fatalf("ResizeWindow to the wrapped state: %v", err)
	}
	repaired, err := backend.RenderPane(ctx, window, pane, painted.Width, region, false)
	if err != nil {
		t.Fatalf("RenderPane at the full width: %v", err)
	}
	if !slices.Contains(repaired.Content, prompt) {
		t.Errorf("a stopped pane with room to spare was not grown back:\n%q", repaired.Content)
	}
}
