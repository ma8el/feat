package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestExecuteExitCodes(t *testing.T) {
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

		{"daemon start", []string{"daemon", "start"}, ExitNotImplemented},
		{"doctor", []string{"doctor"}, ExitNotImplemented},
		{"attach with a task", []string{"attach", "abc123"}, ExitNotImplemented},
		{"runtime start", []string{"runtime", "start", "abc123"}, ExitNotImplemented},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			got := Execute(context.Background(), test.args, &stdout, &stderr)
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
	var stdout, stderr bytes.Buffer

	Execute(context.Background(), []string{"daemon", "start"}, &stdout, &stderr)

	message := stderr.String()
	for _, want := range []string{"feat daemon start", "slice 2", "docs/11-implementation-plan.md"} {
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
