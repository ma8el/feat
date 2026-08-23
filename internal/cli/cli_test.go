package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/spf13/cobra"

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
		{"missing required argument", []string{"task", "attach"}, ExitUsage},
		{"too many arguments", []string{"task", "attach", "one", "two"}, ExitUsage},
		{"unknown subcommand argument", []string{"task", "list", "extra"}, ExitUsage},
		// Cleanup is the one command ADR-040 moved without leaving an alias.
		{"a command that moved under task", []string{"cleanup", "abc123"}, ExitUsage},
		{"unknown log level", []string{"daemon", "status", "--log-level", "loud"}, ExitUsage},

		{"project add without a project", []string{"project", "add"}, ExitUsage},

		// Review, attach, and cleanup all reach the daemon, so an absent one is
		// the state each reports rather than a failure of the command. The two
		// short names ADR-040 kept are exercised beside the canonical ones,
		// because an alias that stops working is an alias nobody notices.
		{"review without a daemon", []string{"task", "review", "abc123"}, ExitNotRunning},
		{"attach without a daemon", []string{"task", "attach", "abc123"}, ExitNotRunning},
		{"cleanup without a daemon", []string{"task", "cleanup", "abc123"}, ExitNotRunning},
		{"the review alias without a daemon", []string{"review", "abc123"}, ExitNotRunning},
		{"the attach alias without a daemon", []string{"attach", "abc123"}, ExitNotRunning},

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
//
// It exercises the error rather than a command, because no command is
// unimplemented: every entry in the documented surface does its work. The rule
// still has to hold for whatever is deferred later, and
// TestPlaceholdersSayWhatIsMissing is what would catch a placeholder that
// stopped saying what is missing.
func TestNotImplementedErrorIsActionable(t *testing.T) {
	isolate(t)

	err := &NotImplementedError{
		Command: "feat example",
		Outcome: "run the agent on the host rather than in a container",
	}
	message := err.Error()
	for _, want := range []string{"feat example", "run the agent on the host", "docs/09-roadmap.md"} {
		if !strings.Contains(message, want) {
			t.Errorf("error message does not mention %q:\n%s", want, message)
		}
	}
}

// TestAnUnknownCommandNamesTheOnesItMightBe covers the other half of the quality
// bar: a rejection that says only "unknown command" leaves the user to guess.
//
// The case that matters is `feat cleanup`, which ADR-040 moved under `feat task`
// without leaving an alias, so the name a user may still type has to lead
// somewhere. A typo is the same question asked by accident.
func TestAnUnknownCommandNamesTheOnesItMightBe(t *testing.T) {
	isolate(t)

	tests := []struct {
		name string
		args []string
		want string
	}{
		{"a command that moved under task", []string{"cleanup", "abc123"}, "task"},
		{"a mistyped group", []string{"tsk"}, "task"},
		{"a mistyped command", []string{"doctr"}, "doctor"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if got := execute(context.Background(), Options{}, test.args, &stdout, &stderr); got != ExitUsage {
				t.Fatalf("exit code = %d, want %d", got, ExitUsage)
			}
			if !strings.Contains(stderr.String(), "Did you mean this?\n\t"+test.want) {
				t.Errorf("%v was not answered with %q:\n%s", test.args, test.want, stderr.String())
			}
		})
	}
}

// TestEveryDocumentedCommandIsImplemented records that the documented surface is
// whole.
//
// It is the counterpart of TestPlaceholdersSayWhatIsMissing: that one requires a
// placeholder to say what is missing, and this one requires there to be no
// placeholder left. Deferring something later has to change this test
// deliberately, which is the point.
func TestEveryDocumentedCommandIsImplemented(t *testing.T) {
	isolate(t)

	var placeholders []string
	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		// The handler is read rather than invoked: invoking one would reach the
		// running user's daemon, which is what surface_test.go's own exemption
		// list exists to avoid.
		if cmd.RunE != nil && strings.Contains(handlerName(cmd), "notImplemented") {
			placeholders = append(placeholders, cmd.CommandPath())
		}
		for _, child := range cmd.Commands() {
			walk(child)
		}
	}
	walk(NewRootCommand(Options{}))

	if len(placeholders) > 0 {
		t.Errorf("commands still report themselves as unimplemented: %s", strings.Join(placeholders, ", "))
	}
}

// handlerName returns the name of the function a command runs.
func handlerName(cmd *cobra.Command) string {
	return runtime.FuncForPC(reflect.ValueOf(cmd.RunE).Pointer()).Name()
}

func TestUsageErrorPrintsUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer

	Execute(context.Background(), []string{"task", "attach"}, &stdout, &stderr)

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
