package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func newTestReview(t *testing.T) *Review {
	t.Helper()

	review, err := NewReview(TaskID("0f8fad5b-d9cb-469f-a165-70867728950e"), time.Now())
	if err != nil {
		t.Fatalf("creating a review: %v", err)
	}
	return review
}

// TestGatedResultsDoNotEraseAgentClaims is half of FR-AGENT-006 in the
// aggregate: after a gate has run, a result it enforced and a result the agent
// asserted are both present and still tell each other apart.
//
// A gate that cleared everything it did not run would be Feat deciding that a
// report it had not verified never happened.
func TestGatedResultsDoNotEraseAgentClaims(t *testing.T) {
	review := newTestReview(t)
	now := time.Now()

	claimed := []Check{
		{ID: "test", RepositoryID: "api", Status: CheckPassed, Reporter: ReporterAgent, Detail: "84 passed"},
		{ID: "lint", RepositoryID: "api", Status: CheckPassed, Reporter: ReporterAgent},
	}
	if err := review.RecordRequest("done", claimed, now); err != nil {
		t.Fatalf("recording the agent's report: %v", err)
	}
	if review.Gated() {
		t.Fatal("a review holding only the agent's claims reports itself gated")
	}

	if err := review.RecordChecks([]Check{
		{ID: "test", RepositoryID: "api", Status: CheckFailed, Reporter: ReporterProvider,
			Detail: "2 failed", RanAt: now},
	}, now); err != nil {
		t.Fatalf("recording a gated result: %v", err)
	}

	if len(review.Checks) != 2 {
		t.Fatalf("the review holds %d checks, want the gated one and the claim it did not run", len(review.Checks))
	}
	test, ok := checkByID(review, "test")
	if !ok {
		t.Fatal("the gated result is not recorded")
	}
	if test.Reporter != ReporterProvider || test.Status != CheckFailed {
		t.Errorf("the gate's result is %s/%s, want a failed provider result", test.Reporter, test.Status)
	}
	lint, ok := checkByID(review, "lint")
	if !ok {
		t.Fatal("the agent's claim about a check the gate did not run was removed")
	}
	if lint.Reporter != ReporterAgent {
		t.Errorf("the untouched claim is now attributed to %s", lint.Reporter)
	}
	if !review.Gated() {
		t.Error("a review holding an enforced result does not report itself gated")
	}

	failed := review.FailedChecks()
	if len(failed) != 1 || failed[0].ID != "test" {
		t.Errorf("the failed checks are %v, want the one the gate failed", failed)
	}
}

// TestChecksOfTwoRepositoriesAreDistinct checks that a check identity is its
// repository and its identifier together. Two repositories that both configure a
// check called "test" are the ordinary case in a multi-repository project, and
// one overwriting the other would report half the work.
func TestChecksOfTwoRepositoriesAreDistinct(t *testing.T) {
	review := newTestReview(t)
	now := time.Now()

	if err := review.RecordChecks([]Check{
		{ID: "test", RepositoryID: "api", Status: CheckPassed, Reporter: ReporterProvider, RanAt: now},
		{ID: "test", RepositoryID: "web", Status: CheckFailed, Reporter: ReporterProvider, RanAt: now},
	}, now); err != nil {
		t.Fatalf("recording gated results: %v", err)
	}

	if len(review.Checks) != 2 {
		t.Fatalf("the review holds %d checks, want one per repository", len(review.Checks))
	}
	if failed := review.FailedChecks(); len(failed) != 1 || failed[0].RepositoryID != "web" {
		t.Errorf("the failed checks are %v, want only the web one", failed)
	}
}

// TestAGateCannotRecordAnAgentClaim checks that the reporter of a gated result
// is not something a caller chooses. The distinction between an enforced result
// and an asserted one is what the field exists for.
func TestAGateCannotRecordAnAgentClaim(t *testing.T) {
	review := newTestReview(t)

	err := review.RecordChecks([]Check{
		{ID: "test", Status: CheckPassed, Reporter: ReporterAgent},
	}, time.Now())
	if err == nil {
		t.Fatal("a gate recorded a result attributed to the agent")
	}
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("the refusal is %v, want a validation error", err)
	}
}

// TestCheckDetailIsBounded checks that a review document cannot grow without
// limit because a failing test printed a great deal.
func TestCheckDetailIsBounded(t *testing.T) {
	review := newTestReview(t)

	err := review.RecordChecks([]Check{
		{ID: "test", Status: CheckFailed, Reporter: ReporterProvider,
			Detail: strings.Repeat("x", MaxCheckDetail+1), RanAt: time.Now()},
	}, time.Now())
	if err == nil {
		t.Fatal("a check result carried an unbounded excerpt")
	}
	if !strings.Contains(err.Error(), "excerpt") {
		t.Errorf("the refusal is %q, and it should say what the limit is about", err)
	}
}

func checkByID(review *Review, id string) (Check, bool) {
	for _, check := range review.Checks {
		if check.ID == id {
			return check, true
		}
	}
	return Check{}, false
}
