package integrationtest_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/integrationtest"
)

// recorder stands in for *testing.T so that what Unavailable does to a test can
// be observed instead of happening.
//
// Real Skipf and Fatalf do not return, so the recorder keeps only the first
// call: a caller that reached Fatalf and carried on would be reporting on code
// that cannot run.
type recorder struct {
	helpers int
	outcome string
	message string
}

func (r *recorder) Helper() { r.helpers++ }

func (r *recorder) Skipf(format string, args ...any) {
	r.record("skip", format, args...)
}

func (r *recorder) Fatalf(format string, args ...any) {
	r.record("fatal", format, args...)
}

func (r *recorder) record(outcome, format string, args ...any) {
	if r.outcome != "" {
		return
	}
	r.outcome = outcome
	r.message = fmt.Sprintf(format, args...)
}

// TestADemandedToolThatDidNotAnswerFailsTheRun is the whole point of the
// package: `make check` reported green on a machine with no Docker because a
// missing tool was a skip, and a skipped package prints "ok".
func TestADemandedToolThatDidNotAnswerFailsTheRun(t *testing.T) {
	t.Setenv(integrationtest.Env, "1")
	t.Setenv(integrationtest.EnvRequire, "git,docker,tmux")

	got := &recorder{}
	integrationtest.Unavailable(got, integrationtest.Docker, "no Docker daemon is reachable from this machine")

	if got.outcome != "fatal" {
		t.Fatalf("a demanded tool that did not answer was a %s, want a fatal: %s", got.outcome, got.message)
	}
	// The failure has to say which proof went missing and which demand it
	// broke, because the next reader of it is somebody whose gate just turned
	// red for the first time.
	for _, want := range []string{"no Docker daemon is reachable", "docker", integrationtest.EnvRequire} {
		if !strings.Contains(got.message, want) {
			t.Errorf("the failure does not mention %q: %s", want, got.message)
		}
	}
	if got.helpers == 0 {
		t.Errorf("Unavailable did not mark itself a helper, so the failure points at the wrong line")
	}
}

// TestAToolThisRunDoesNotDemandStillSkips is the other half, and it is what
// keeps macOS CI — a runner image with no Docker daemon — able to run the tier
// at all.
func TestAToolThisRunDoesNotDemandStillSkips(t *testing.T) {
	t.Setenv(integrationtest.Env, "1")
	t.Setenv(integrationtest.EnvRequire, "git,tmux")

	got := &recorder{}
	integrationtest.Unavailable(got, integrationtest.Docker, "Docker is not installed on this machine")

	if got.outcome != "skip" {
		t.Fatalf("an undemanded tool was a %s, want a skip: %s", got.outcome, got.message)
	}
	if !strings.Contains(got.message, "Docker is not installed") {
		t.Errorf("the skip does not carry the caller's reason: %s", got.message)
	}
	// The skip says how to turn itself into a failure, because a reader who
	// wanted this proof needs to know it is one variable away.
	if !strings.Contains(got.message, integrationtest.EnvRequire+"=docker") {
		t.Errorf("the skip does not say how to demand the tool: %s", got.message)
	}
}

// TestNoDemandAtAllStillSkips pins that a bare `go test -run TestReal ./...`
// behaves as it always did. Requiring the variable to be set would make every
// existing invocation fail for a reason that is not about the machine.
func TestNoDemandAtAllStillSkips(t *testing.T) {
	t.Setenv(integrationtest.Env, "1")
	t.Setenv(integrationtest.EnvRequire, "")

	got := &recorder{}
	integrationtest.Unavailable(got, integrationtest.Tmux, "tmux is not installed")

	if got.outcome != "skip" {
		t.Fatalf("an unset demand produced a %s, want a skip: %s", got.outcome, got.message)
	}
}

// TestAMisspeltDemandFailsRatherThanDemandingNothing closes the way this fix
// could be undone by a typo. "FEAT_INTEGRATION_REQUIRE=dockr" that quietly
// demanded nothing would be the original defect with a variable in front of it.
func TestAMisspeltDemandFailsRatherThanDemandingNothing(t *testing.T) {
	t.Setenv(integrationtest.Env, "1")
	t.Setenv(integrationtest.EnvRequire, "git,dockr,tmux")

	if _, err := integrationtest.Requirements(); err == nil {
		t.Fatal("a requirement list naming no known tool parsed cleanly")
	}

	got := &recorder{}
	integrationtest.Unavailable(got, integrationtest.Docker, "no Docker daemon is reachable")

	if got.outcome != "fatal" {
		t.Fatalf("a misspelt demand produced a %s, want a fatal: %s", got.outcome, got.message)
	}
	if !strings.Contains(got.message, "dockr") {
		t.Errorf("the failure does not name the value that is not a tool: %s", got.message)
	}
}

// TestRequirementsReadsTheNamesItAccepts covers the parsing itself: spacing,
// repetition, and an empty entry are all a person editing a Makefile line.
func TestRequirementsReadsTheNamesItAccepts(t *testing.T) {
	for _, testCase := range []struct {
		value string
		want  []integrationtest.Tool
	}{
		{value: "", want: nil},
		{value: "   ", want: nil},
		{value: "docker", want: []integrationtest.Tool{integrationtest.Docker}},
		{value: " git , docker ,, tmux ", want: []integrationtest.Tool{
			integrationtest.Git, integrationtest.Docker, integrationtest.Tmux}},
		{value: "git,git", want: []integrationtest.Tool{integrationtest.Git}},
	} {
		t.Run(testCase.value, func(t *testing.T) {
			t.Setenv(integrationtest.EnvRequire, testCase.value)

			got, err := integrationtest.Requirements()
			if err != nil {
				t.Fatalf("Requirements(%q): %v", testCase.value, err)
			}
			if len(got) != len(testCase.want) {
				t.Fatalf("Requirements(%q) = %v, want %v", testCase.value, got, testCase.want)
			}
			for i := range got {
				if got[i] != testCase.want[i] {
					t.Fatalf("Requirements(%q) = %v, want %v", testCase.value, got, testCase.want)
				}
			}
		})
	}
}

// TestMissingNamesEveryDemandedToolThatDidNotAnswer covers the preflight's
// decision without needing a machine that has lost a tool.
//
// The preflight test itself supplies the real probe; this supplies one that
// answers however the case needs, which is the only way the "Docker is gone"
// arm is ever exercised on a machine that has Docker.
func TestMissingNamesEveryDemandedToolThatDidNotAnswer(t *testing.T) {
	stopped := errors.New("no Docker daemon is reachable")
	probe := func(tool integrationtest.Tool) error {
		if tool == integrationtest.Docker {
			return stopped
		}
		return nil
	}

	absent := integrationtest.Missing(
		[]integrationtest.Tool{integrationtest.Git, integrationtest.Docker, integrationtest.Tmux}, probe)

	if len(absent) != 1 {
		t.Fatalf("Missing reported %d absences, want 1: %v", len(absent), absent)
	}
	if absent[0].Tool != integrationtest.Docker {
		t.Errorf("the absence is %q, want docker", absent[0].Tool)
	}
	if !errors.Is(absent[0].Reason, stopped) {
		t.Errorf("the absence reports %v, want %v", absent[0].Reason, stopped)
	}
}

// TestMissingIsSilentWhenEveryDemandedToolAnswers is the ordinary case, and it
// is here because a preflight that reported an absence on a healthy machine
// would be reverted within a day.
func TestMissingIsSilentWhenEveryDemandedToolAnswers(t *testing.T) {
	probe := func(integrationtest.Tool) error { return nil }

	if absent := integrationtest.Missing(integrationtest.Tools, probe); len(absent) != 0 {
		t.Fatalf("a machine that answers every demand reported %v", absent)
	}
}
