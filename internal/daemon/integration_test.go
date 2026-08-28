package daemon

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ma8el/feat/internal/integrationtest"
)

// TestBinaryLifecycle exercises `feat daemon start`, `status`, and `stop` as
// separate operating-system processes, and is the literal form of the rule that
// two client processes can query the daemon concurrently.
//
// It is opt-in because it builds the binary. Set FEAT_INTEGRATION=1 to run it;
// CI does.
func TestBinaryLifecycle(t *testing.T) {
	if !integrationtest.Enabled() {
		t.Skipf("set %s=1 to run the tests that build and run the binary", integrationtest.Env)
	}

	binary := buildBinary(t)
	layout := testLayout(t)
	environment := append(userEnvironment(),
		"FEAT_RUNTIME_DIR="+layout.Runtime,
		"XDG_DATA_HOME="+filepath.Dir(layout.State),
		"XDG_CONFIG_HOME="+filepath.Dir(layout.Config),
	)

	// The state directory is <XDG_DATA_HOME>/feat, so the layout the binary
	// resolves has to match the one this test asserts against.
	stateParent := filepath.Dir(layout.State)
	if filepath.Base(layout.State) != "feat" {
		layout.State = filepath.Join(stateParent, "feat")
	}

	run := func(t *testing.T, args ...string) (string, int) {
		t.Helper()

		command := exec.Command(binary, args...)
		command.Env = environment
		output, err := command.CombinedOutput()

		code := 0
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		} else if err != nil {
			t.Fatalf("running %s %v: %v", binary, args, err)
		}
		return string(output), code
	}

	// Nothing is running yet, and that is reported as its own exit code rather
	// than as a failure.
	if output, code := run(t, "daemon", "status"); code != 4 {
		t.Fatalf("status without a daemon: exit %d, want 4\n%s", code, output)
	}

	output, code := run(t, "daemon", "start")
	if code != 0 {
		t.Fatalf("start: exit %d\n%s", code, output)
	}
	if !strings.Contains(output, layout.Socket) {
		t.Errorf("start does not report the socket:\n%s", output)
	}
	t.Cleanup(func() {
		if Answering(layout.Socket) {
			run(t, "daemon", "stop")
		}
	})

	// Two client processes at once: each runs the whole start-up, dial, request,
	// and exit path of a real client.
	var clients sync.WaitGroup
	outputs := make(chan string, 4)
	for i := 0; i < 2; i++ {
		clients.Add(2)
		go func() {
			defer clients.Done()
			out, code := run(t, "daemon", "status")
			if code != 0 {
				outputs <- "status failed: " + out
			}
		}()
		go func() {
			defer clients.Done()
			out, code := run(t, "task", "list")
			// The daemon has no tasks, so an empty list is the expected
			// answer; what matters is that the process read from a live daemon
			// without disturbing it.
			if code != 0 {
				outputs <- "task list: unexpected exit\n" + out
			}
		}()
	}
	clients.Wait()
	close(outputs)
	for problem := range outputs {
		t.Error(problem)
	}

	// Starting again reports the running daemon instead of replacing it.
	output, code = run(t, "daemon", "start")
	if code != 0 {
		t.Fatalf("second start: exit %d\n%s", code, output)
	}
	if !strings.Contains(output, "already running") {
		t.Errorf("a second start did not report the running daemon:\n%s", output)
	}

	// Restarting replaces the process, which is the whole of what it promises:
	// a restart that reported success while leaving the old daemon running is
	// exactly the state a changed settings file would then not reach. It is
	// asserted here rather than in internal/cli because it spawns, and a client
	// starts the binary it was built as — which in a test process is the test.
	before, err := ReadEndpoint(layout)
	if err != nil {
		t.Fatalf("reading the endpoint before the restart: %v", err)
	}
	output, code = run(t, "daemon", "restart")
	if code != 0 {
		t.Fatalf("restart: exit %d\n%s", code, output)
	}
	if !strings.Contains(output, "daemon stopped") || !strings.Contains(output, "daemon started") {
		t.Errorf("restart does not report both halves:\n%s", output)
	}
	after, err := ReadEndpoint(layout)
	if err != nil {
		t.Fatalf("reading the endpoint after the restart: %v", err)
	}
	if after.PID == before.PID {
		t.Errorf("the daemon after the restart is pid %d, the same process as before", after.PID)
	}
	if !Answering(layout.Socket) {
		t.Error("nothing answers on the socket after a restart")
	}

	output, code = run(t, "daemon", "stop")
	if code != 0 {
		t.Fatalf("stop: exit %d\n%s", code, output)
	}
	if Answering(layout.Socket) {
		t.Error("the daemon still answers after stop")
	}

	// And a restart with nothing to stop starts one anyway, which is what makes
	// it the command to reach for without checking first.
	output, code = run(t, "daemon", "restart")
	if code != 0 {
		t.Fatalf("restart with no daemon: exit %d\n%s", code, output)
	}
	if !strings.Contains(output, "no daemon was running") {
		t.Errorf("a restart with nothing to stop does not say so:\n%s", output)
	}
	if !Answering(layout.Socket) {
		t.Error("a restart with nothing to stop did not start a daemon")
	}
	if output, code := run(t, "daemon", "stop"); code != 0 {
		t.Fatalf("stopping the restarted daemon: exit %d\n%s", code, output)
	}
	for _, path := range []string{layout.Socket, layout.EndpointFile()} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s survived stop", path)
		}
	}

	// The daemon wrote a log, which is the only account of a background process.
	if _, err := os.Lstat(layout.LogFile()); err != nil {
		t.Errorf("no daemon log at %s: %v", layout.LogFile(), err)
	}
}

// userEnvironment is this process's environment with Feat's own bookkeeping
// removed, which is what a user's shell would hand the binary.
//
// FEAT_DAEMON_SPAWNED is the one that matters: `feat daemon start` refuses to
// start a daemon when it is set, and this test drives that command as a user
// does. A suite run from a shell never sees it; a suite run as a check by a Feat
// daemon inherits it, and the test then fails on the environment of whoever ran
// it. The daemon no longer passes it on — see TestTheSpawnMarkerDoesNotOutliveTheSpawn
// — and this keeps the harness independent of the daemon that happens to be
// serving while the fix has not yet been deployed.
func userEnvironment() []string {
	environment := os.Environ()
	clean := make([]string, 0, len(environment))
	for _, entry := range environment {
		if strings.HasPrefix(entry, envSpawned+"=") {
			continue
		}
		clean = append(clean, entry)
	}
	return clean
}

// buildBinary builds cmd/feat into a temporary directory.
func buildBinary(t *testing.T) string {
	t.Helper()

	binary := filepath.Join(t.TempDir(), "feat")
	build := exec.Command("go", "build", "-o", binary, "github.com/ma8el/feat/cmd/feat")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the binary: %v\n%s", err, output)
	}
	return binary
}
