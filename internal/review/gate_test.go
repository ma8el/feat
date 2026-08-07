package review

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ma8el/feat/internal/domain"
)

// fakeRunner answers checks from a table, and records what it was asked to run.
type fakeRunner struct {
	outputs map[string]Output
	errs    map[string]error
	delay   map[string]time.Duration
	ran     []string
}

func (f *fakeRunner) Run(ctx context.Context, check Check) (Output, error) {
	f.ran = append(f.ran, check.ID)
	if wait := f.delay[check.ID]; wait > 0 {
		select {
		case <-ctx.Done():
			return Output{}, ctx.Err()
		case <-time.After(wait):
		}
	}
	if err, failing := f.errs[check.ID]; failing {
		return Output{}, err
	}
	return f.outputs[check.ID], nil
}

// TestAGateRunsEachCheckWhereItsConfigurationSays checks that the execution
// field decides which environment answers, which is the whole of what it means.
func TestAGateRunsEachCheckWhereItsConfigurationSays(t *testing.T) {
	host := &fakeRunner{outputs: map[string]Output{"lint": {}}}
	agent := &fakeRunner{outputs: map[string]Output{"test": {}}}

	results := Gate{Host: host, Agent: agent}.Run(context.Background(), []Check{
		{ID: "test", RepositoryID: "api", Program: "pytest", Directory: "/w/api"},
		{ID: "lint", RepositoryID: "api", Program: "ruff", Directory: "/w/api", OnHost: true},
	})

	if len(agent.ran) != 1 || agent.ran[0] != "test" {
		t.Errorf("the agent's environment ran %v, want only the agent check", agent.ran)
	}
	if len(host.ran) != 1 || host.ran[0] != "lint" {
		t.Errorf("the host ran %v, want only the host check", host.ran)
	}
	for _, result := range results {
		if result.Reporter != domain.ReporterProvider {
			t.Errorf("check %s is attributed to %s, and a gate ran it", result.ID, result.Reporter)
		}
		if result.Status != domain.CheckPassed {
			t.Errorf("check %s is %s, want passed", result.ID, result.Status)
		}
	}
}

// TestAFailingCheckCarriesTheTailOfItsOutput checks that what a user sees is the
// end of a failing run, which is where a test runner puts its summary.
func TestAFailingCheckCarriesTheTailOfItsOutput(t *testing.T) {
	agent := &fakeRunner{outputs: map[string]Output{
		"test": {Stdout: strings.Repeat("progress\n", 5000) + "2 failed, 82 passed", ExitCode: 1},
	}}

	results := Gate{Agent: agent}.Run(context.Background(), []Check{
		{ID: "test", RepositoryID: "api", Program: "pytest", Directory: "/w/api"},
	})

	result := results[0]
	if result.Status != domain.CheckFailed {
		t.Fatalf("the check is %s, want failed", result.Status)
	}
	if !strings.Contains(result.Detail, "2 failed, 82 passed") {
		t.Error("the excerpt does not carry the end of the output, which is where the reason is")
	}
	if !strings.Contains(result.Detail, "exited with status 1") {
		t.Error("the excerpt does not say what the check exited with")
	}
	if len(result.Detail) > domain.MaxCheckDetail {
		t.Errorf("the excerpt is %d bytes, and a review document accepts %d",
			len(result.Detail), domain.MaxCheckDetail)
	}
	if err := result.Validate("0f8fad5b-d9cb-469f-a165-70867728950e"); err != nil {
		t.Errorf("the result a gate produced would be refused by the aggregate that stores it: %v", err)
	}
}

// TestACheckThatDidNotFinishIsNotAFailure checks the distinction ADR-036 draws:
// a bound that expired and a check that failed are different things, and only
// one of them is a statement about the code.
//
// Both stop the task reaching ready_for_review, which is the other half of it: a
// verification nobody managed to run is not a verification.
func TestACheckThatDidNotFinishIsNotAFailure(t *testing.T) {
	agent := &fakeRunner{delay: map[string]time.Duration{"test": time.Minute}}

	results := Gate{Agent: agent, CheckTimeout: 10 * time.Millisecond}.Run(context.Background(), []Check{
		{ID: "test", RepositoryID: "api", Program: "pytest", Directory: "/w/api"},
	})

	result := results[0]
	if result.Status == domain.CheckFailed {
		t.Fatal("a check that ran out of time was recorded as having failed")
	}
	if result.Status != domain.CheckUnknown {
		t.Fatalf("the check is %s, want unknown", result.Status)
	}
	if !strings.Contains(result.Detail, "did not finish within") {
		t.Errorf("the detail is %q, and it should say what happened", result.Detail)
	}
	if Decide(results).Passed {
		t.Error("a gate whose check never reported was decided as passed")
	}
}

// TestACheckThatCouldNotStartIsInconclusive checks that a missing program is
// reported as itself. An absent test runner is a configuration or an image to
// fix, and calling it a failing test would send the user to look at their code.
func TestACheckThatCouldNotStartIsInconclusive(t *testing.T) {
	agent := &fakeRunner{errs: map[string]error{"test": errNotInstalled{}}}

	results := Gate{Agent: agent}.Run(context.Background(), []Check{
		{ID: "test", RepositoryID: "api", Program: "pytest", Directory: "/w/api"},
	})

	if results[0].Status != domain.CheckUnknown {
		t.Errorf("the check is %s, want unknown", results[0].Status)
	}
	if !strings.Contains(results[0].Detail, "could not be started") {
		t.Errorf("the detail is %q, and it should say the check never ran", results[0].Detail)
	}
	if Decide(results).Passed {
		t.Error("a gate whose check could not be started was decided as passed")
	}
}

// TestTheGateStopsAtItsOwnBound checks that a run of many slow checks is bounded
// as a whole, and that the checks it never reached say so rather than looking
// like results.
func TestTheGateStopsAtItsOwnBound(t *testing.T) {
	agent := &fakeRunner{
		outputs: map[string]Output{"first": {}},
		delay:   map[string]time.Duration{"first": 20 * time.Millisecond},
	}

	results := Gate{Agent: agent, Total: 10 * time.Millisecond}.Run(context.Background(), []Check{
		{ID: "first", RepositoryID: "api", Program: "a", Directory: "/w/api"},
		{ID: "second", RepositoryID: "api", Program: "b", Directory: "/w/api"},
	})

	if len(agent.ran) != 1 {
		t.Errorf("the gate ran %v, want it to stop after its bound elapsed", agent.ran)
	}
	if results[1].Status != domain.CheckUnknown {
		t.Errorf("the check that never ran is %s, want unknown", results[1].Status)
	}
	if !strings.Contains(results[1].Detail, "elapsed before this check ran") {
		t.Errorf("the detail is %q, and it should say the gate ran out of time", results[1].Detail)
	}
}

// TestASkippedCheckDoesNotBlockAPass checks that a check Feat deliberately did
// not run — a read-only repository's — leaves the verdict to the ones that did.
func TestASkippedCheckDoesNotBlockAPass(t *testing.T) {
	now := time.Now()
	results := []domain.Check{
		{ID: "test", RepositoryID: "api", Status: domain.CheckPassed, Reporter: domain.ReporterProvider, RanAt: now},
		Skip(Check{ID: "test", RepositoryID: "web"}, "the task holds this repository read-only", now),
	}

	verdict := Decide(results)
	if !verdict.Passed {
		t.Errorf("the verdict is %+v, want a pass", verdict)
	}
	if !strings.Contains(verdict.Summary, "1 skipped") {
		t.Errorf("the summary is %q, and a skipped check is never invisible", verdict.Summary)
	}
}

// TestAProjectWithNoChecksSaysSo checks that an empty gate is explained rather
// than reported as a pass nobody measured.
func TestAProjectWithNoChecksSaysSo(t *testing.T) {
	verdict := Decide(nil)
	if !verdict.Passed {
		t.Error("a task with no configured checks cannot fail a gate")
	}
	if !strings.Contains(verdict.Summary, "no checks") {
		t.Errorf("the summary is %q, want it to say there were none", verdict.Summary)
	}
}

type errNotInstalled struct{}

func (errNotInstalled) Error() string { return "pytest is not installed" }
