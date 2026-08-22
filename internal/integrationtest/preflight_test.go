package integrationtest_test

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ma8el/feat/internal/execution/compose"
	"github.com/ma8el/feat/internal/git"
	"github.com/ma8el/feat/internal/integrationtest"
	"github.com/ma8el/feat/internal/notify"
	"github.com/ma8el/feat/internal/tmux"
)

// TestRealToolsThisRunDemandsAllAnswer is the tier's preflight.
//
// Every gated test refuses on its own demand, which is where the useful message
// is: "this machine cannot run a container to measure" names the proof that
// went missing. This test exists above them for the two things a per-test
// refusal cannot do. It gives one legible failure at the top of the run instead
// of the same absence restated eighteen times across five packages, and it
// fails on a requirement list naming something that is not a tool — a typo
// there would otherwise demand nothing at all, silently, which is the defect
// wearing a variable.
//
// It is named for the integration run pattern like everything else in the tier,
// so `make test-real` and CI both reach it.
func TestRealToolsThisRunDemandsAllAnswer(t *testing.T) {
	if !integrationtest.Enabled() {
		t.Skipf("set %s=1 to run the tests that drive the real tools", integrationtest.Env)
	}

	required, err := integrationtest.Requirements()
	if err != nil {
		t.Fatalf("this run's requirements cannot be read: %v", err)
	}
	if len(required) == 0 {
		// Not a failure: a bare `go test -run TestReal ./...` demands nothing,
		// and always did. It says so, because a run that proves whatever
		// happens to be installed should not read like a run that proved what
		// it set out to.
		t.Logf("this run demands no tool, so every absent one will skip. "+
			"Set %s to make the tier's proofs mandatory; `make test-real` does.", integrationtest.EnvRequire)
		return
	}

	if absent := integrationtest.Missing(required, probe); len(absent) != 0 {
		var reasons []string
		for _, one := range absent {
			reasons = append(reasons, fmt.Sprintf("\t%s: %v", one.Tool, one.Reason))
		}
		t.Fatalf("this run demands %s and %d of them did not answer, "+
			"so the proofs behind the integration tier cannot run and the gate must not report green:\n%s",
			render(required), len(absent), strings.Join(reasons, "\n"))
	}
}

// probeTimeout bounds each question. A Docker daemon that has stopped
// answering is the case this exists for, and it does not always refuse
// promptly.
const probeTimeout = 30 * time.Second

// probe asks one tool whether it is installed and answering.
//
// The questions are the cheapest ones that distinguish "on PATH" from
// "working", because a Docker CLI whose daemon is down is exactly the machine
// state that used to pass the gate.
func probe(tool integrationtest.Tool) error {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()

	switch tool {
	case integrationtest.Git:
		return answers(ctx, git.Executable, "--version")
	case integrationtest.Docker:
		if err := answers(ctx, compose.Executable, "info"); err != nil {
			return err
		}
		return answers(ctx, compose.Executable, "compose", "version")
	case integrationtest.Tmux:
		return answers(ctx, tmux.Executable, "-V")
	case integrationtest.Claude:
		return answers(ctx, "claude", "--version")
	case integrationtest.Notify:
		// The notifier is asked rather than looked up on PATH: what it needs is
		// per-platform, and the build that has no notifier at all answers this
		// too. Feat can only ever say it handed a notification over, so this is
		// the same question the tests behind the demand ask.
		if available, reason := notify.Host().Available(); !available {
			return fmt.Errorf("%s", reason)
		}
		return nil
	default:
		return fmt.Errorf("no preflight question is defined for %q", tool)
	}
}

// answers runs one question and reports what went wrong with it.
func answers(ctx context.Context, program string, args ...string) error {
	if _, err := exec.LookPath(program); err != nil {
		return fmt.Errorf("%s is not installed: %w", program, err)
	}
	output, err := exec.CommandContext(ctx, program, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("`%s %s` failed: %w\n\t\t%s",
			program, strings.Join(args, " "), err, tail(string(output)))
	}
	return nil
}

// tail returns the end of a tool's output.
//
// `docker info` prints a screenful about the client before it says the daemon
// is unreachable, and the sentence a reader needs is the last one. A preflight
// failure that scrolls the reason off the top has not told anybody anything.
const tailLines = 8

func tail(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) > tailLines {
		lines = append([]string{"…"}, lines[len(lines)-tailLines:]...)
	}
	return strings.Join(lines, "\n\t\t")
}

// render lists the demanded tools for a message.
func render(required []integrationtest.Tool) string {
	names := make([]string, 0, len(required))
	for _, tool := range required {
		names = append(names, string(tool))
	}
	return strings.Join(names, ", ")
}
