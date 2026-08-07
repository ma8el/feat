package tmux

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ma8el/feat/internal/domain"
)

const envIntegration = "FEAT_INTEGRATION"

func requireTmux(t *testing.T) {
	t.Helper()
	if os.Getenv(envIntegration) == "" {
		t.Skipf("set %s=1 to run tests against real tmux", envIntegration)
	}
	if _, err := exec.LookPath(Executable); err != nil {
		t.Skip("tmux is not installed")
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
// Slice 5 criteria against tmux itself: the dedicated socket does not collide
// with another server, user indexes/names do not become identity, and a fresh
// adapter rediscovers the tagged objects after a daemon-equivalent restart.
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
	if len(discovered) != 1 {
		t.Fatalf("discovered %d terminals, want 1", len(discovered))
	}
	if discovered[0].Target != terminal.Target {
		t.Errorf("target after rename/renumber = %+v, want %+v", discovered[0].Target, terminal.Target)
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

	terminals, err := backend.Discover(ctx)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	return terminals
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
