package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/daemon"
	"github.com/ma8el/feat/internal/paths"
)

// isolate points path resolution at a temporary directory.
//
// Without it these tests would read, and `feat daemon` would write, the
// directories of whoever is running them: a unit test must never reach the
// developer's own daemon.
func isolate(t *testing.T) paths.Layout {
	t.Helper()

	root := t.TempDir()
	t.Setenv(paths.EnvRuntimeOverride, shortDir(t))
	t.Setenv(paths.EnvDataHome, filepath.Join(root, "state"))
	t.Setenv(paths.EnvConfigHome, filepath.Join(root, "config"))

	current, err := paths.Current()
	if err != nil {
		t.Fatalf("paths.Current: %v", err)
	}
	layout, err := paths.Resolve(current)
	if err != nil {
		t.Fatalf("paths.Resolve: %v", err)
	}
	return layout
}

// shortDir returns a temporary directory with a short path.
//
// t.TempDir() embeds the test's name, which on macOS pushes a socket path past
// the platform's limit — the limit paths.Resolve checks, so this would otherwise
// fail before the test began.
func shortDir(t *testing.T) string {
	t.Helper()

	dir, err := os.MkdirTemp("", "feat")
	if err != nil {
		t.Fatalf("creating a temporary directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestExecuteExitCodes(t *testing.T) {
	isolate(t)

	tests := []struct {
		name string
		args []string
		want int
	}{
		{"dashboard falls back to plain text", nil, ExitOK},
		{"version", []string{"version"}, ExitOK},
		{"help", []string{"--help"}, ExitOK},
		{"group without subcommand", []string{"daemon"}, ExitOK},

		{"unknown command", []string{"bogus"}, ExitUsage},
		{"unknown flag", []string{"--nope"}, ExitUsage},
		{"missing required argument", []string{"attach"}, ExitUsage},
		{"too many arguments", []string{"attach", "one", "two"}, ExitUsage},
		{"unknown subcommand argument", []string{"task", "list", "extra"}, ExitUsage},
		{"unknown log level", []string{"daemon", "status", "--log-level", "loud"}, ExitUsage},

		{"project add without a project", []string{"project", "add"}, ExitUsage},

		{"review", []string{"review", "abc123"}, ExitNotImplemented},
		{"attach without a daemon", []string{"attach", "abc123"}, ExitNotRunning},

		// Every runtime action reaches the daemon, so an absent one is the state
		// each of them reports rather than a failure of the command.
		{"runtime create without a daemon", []string{"runtime", "create", "abc123"}, ExitNotRunning},
		{"runtime start without a daemon", []string{"runtime", "start", "abc123"}, ExitNotRunning},
		{"runtime stop without a daemon", []string{"runtime", "stop", "abc123"}, ExitNotRunning},
		{"runtime status without a daemon", []string{"runtime", "status", "abc123"}, ExitNotRunning},
		{"runtime logs without a daemon", []string{"runtime", "logs", "abc123"}, ExitNotRunning},
		{"runtime destroy without a daemon", []string{"runtime", "destroy", "abc123", "--yes"}, ExitNotRunning},

		// Preparation and the task list need a daemon, which is a state rather
		// than a failure. Preparation additionally needs a terminal, because
		// nothing is created until the user confirms it, and the test process
		// has none: an absent daemon is found first either way.
		{"implement without a daemon", []string{"implement"}, ExitError},
		{"task list without a daemon", []string{"task", "list"}, ExitNotRunning},

		// A machine with nothing configured is diagnosable and not broken.
		{"doctor on an empty machine", []string{"doctor"}, ExitOK},

		// A project with no configuration file is a failure of the command,
		// unlike an absent daemon, which is a state.
		{"project add without a configuration", []string{"project", "add", "absent"}, ExitError},
		{"project show without a configuration", []string{"project", "show", "absent"}, ExitError},

		// An absent daemon is a state a script may act on, not a failure of the
		// command.
		{"daemon status without a daemon", []string{"daemon", "status"}, ExitNotRunning},
		{"daemon stop without a daemon", []string{"daemon", "stop"}, ExitNotRunning},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			got := execute(context.Background(), Options{Runner: workingHost{}}, test.args, &stdout, &stderr)
			if got != test.want {
				t.Errorf("exit code = %d, want %d\nstdout: %s\nstderr: %s",
					got, test.want, stdout.String(), stderr.String())
			}
		})
	}
}

// TestNotImplementedErrorIsActionable checks the quality bar in CLAUDE.md: an
// error must say what is missing and where to look, not merely that something
// failed.
func TestNotImplementedErrorIsActionable(t *testing.T) {
	isolate(t)

	var stdout, stderr bytes.Buffer

	Execute(context.Background(), []string{"review", "abc123"}, &stdout, &stderr)

	message := stderr.String()
	for _, want := range []string{"feat review", "slice 11", "docs/11-implementation-plan.md"} {
		if !strings.Contains(message, want) {
			t.Errorf("error message does not mention %q:\n%s", want, message)
		}
	}
	if stdout.Len() != 0 {
		t.Errorf("an unimplemented command wrote to stdout: %q", stdout.String())
	}
}

func TestUsageErrorPrintsUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer

	Execute(context.Background(), []string{"attach"}, &stdout, &stderr)

	if !strings.Contains(stderr.String(), "Usage:") {
		t.Errorf("usage error did not print usage text:\n%s", stderr.String())
	}
}

func TestDashboardRendersHealthWithoutATerminal(t *testing.T) {
	isolate(t)

	var stdout, stderr bytes.Buffer

	if code := Execute(context.Background(), nil, &stdout, &stderr); code != ExitOK {
		t.Fatalf("exit code = %d, stderr: %s", code, stderr.String())
	}

	out := stdout.String()
	for _, want := range []string{"version", "platform", "daemon"} {
		if !strings.Contains(out, want) {
			t.Errorf("health output does not mention %q:\n%s", want, out)
		}
	}
}

// TestDashboardDoesNotStartADaemonWithoutATerminal pins the rule that opening
// the dashboard starts a daemon (ADR-008) while printing a summary does not.
// Piping `feat` into another command, or running it in CI, must not leave a
// background process behind.
func TestDashboardDoesNotStartADaemonWithoutATerminal(t *testing.T) {
	layout := isolate(t)

	var stdout, stderr bytes.Buffer
	if code := Execute(context.Background(), nil, &stdout, &stderr); code != ExitOK {
		t.Fatalf("exit code = %d, stderr: %s", code, stderr.String())
	}

	if daemon.Answering(layout.Socket) {
		t.Error("a non-interactive dashboard started a daemon")
	}
	if !strings.Contains(stdout.String(), "no daemon is running") {
		t.Errorf("the summary does not report the daemon's absence:\n%s", stdout.String())
	}
}
