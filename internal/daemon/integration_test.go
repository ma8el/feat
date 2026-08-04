package daemon

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// envIntegration opts a run in to the tests that build the binary and run it.
const envIntegration = "FEAT_INTEGRATION"

// TestBinaryLifecycle exercises `feat daemon start`, `status`, and `stop` as
// separate operating-system processes, and is the literal form of the slice 2
// acceptance criterion that two client processes can query the daemon
// concurrently.
//
// It is opt-in because it builds the binary. Set FEAT_INTEGRATION=1 to run it;
// CI does.
func TestBinaryLifecycle(t *testing.T) {
	if os.Getenv(envIntegration) == "" {
		t.Skipf("set %s=1 to run the tests that build and run the binary", envIntegration)
	}

	binary := buildBinary(t)
	layout := testLayout(t)
	environment := append(os.Environ(),
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
			// Task listing arrives with slice 6, so an unimplemented exit code
			// is the expected answer; what matters is that the process ran
			// against a live daemon without disturbing it.
			if code != 3 {
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

	output, code = run(t, "daemon", "stop")
	if code != 0 {
		t.Fatalf("stop: exit %d\n%s", code, output)
	}
	if Answering(layout.Socket) {
		t.Error("the daemon still answers after stop")
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
