package api

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/store/storetest"
)

// publicationPath is the endpoint of one publication action for the fixture
// task.
func publicationPath(action string) string {
	return "/v1/tasks/" + storetest.TaskID.String() + "/publication/" + action
}

// TestPublicationResponseBodies pins the publication surface.
//
// Two actions, plan and apply, for the reason cleanup has two: what is sent has
// to be what the user read, and a publication reaches somebody else's server
// where nothing Feat creates can be reliably un-created (ADR-073).
func TestPublicationResponseBodies(t *testing.T) {
	service := newFakeService()
	handler := NewHandler(Options{Service: service})

	response := request(t, handler, http.MethodPost, publicationPath("plan"))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", response.Code, response.Body.String())
	}
	compare(t, "publication-plan.golden", response.Body.String())
	if !slices.Contains(service.actions, "publication-plan") {
		t.Errorf("the endpoint reached the actions %v, and its path names a plan", service.actions)
	}

	service = newFakeService()
	handler = NewHandler(Options{Service: service})
	response = requestBody(t, handler, http.MethodPost, publicationPath("apply"),
		`{"repositories":[{"repository_id":"api","title":"Add the rate limit","body":"Why.",`+
			`"commit":"`+storetest.PrimaryBaseCommit+`"}]}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", response.Code, response.Body.String())
	}
	compare(t, "publication-applied.golden", response.Body.String())
	if !slices.Contains(service.actions, "publish:api:Add the rate limit") {
		t.Errorf("the approved words did not reach the daemon: %v", service.actions)
	}
}

// TestAnUnknownPublicationActionIsNotAnInstruction keeps the vocabulary closed,
// as the review and runtime vocabularies are.
func TestAnUnknownPublicationActionIsNotAnInstruction(t *testing.T) {
	service := newFakeService()
	handler := NewHandler(Options{Service: service})

	response := request(t, handler, http.MethodPost, publicationPath("merge"))
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d, body: %s", response.Code, http.StatusNotFound, response.Body.String())
	}
	if len(service.actions) != 0 {
		t.Fatalf("an unknown action reached the daemon: %v", service.actions)
	}
}

// TestAPlanCarriesNothing is the rule every reading endpoint follows.
//
// A plan asks what publishing would do. A request that could name a title would
// be a caller writing the words before anybody had read them, which is the one
// thing the approval step exists to prevent.
func TestAPlanCarriesNothing(t *testing.T) {
	service := newFakeService()
	handler := NewHandler(Options{Service: service})

	response := requestBody(t, handler, http.MethodPost, publicationPath("plan"),
		`{"repositories":[{"repository_id":"api","title":"Whatever I like"}]}`)
	if response.Code != http.StatusBadRequest {
		t.Errorf("a plan carrying words was accepted: status = %d, body: %s",
			response.Code, response.Body.String())
	}
	if len(service.actions) != 0 {
		t.Fatalf("a request carrying words reached the daemon: %v", service.actions)
	}
}

// TestPublishingATaskThatIsNotThereIsNotFound checks that the transport reports
// an unknown task as a missing one rather than as a failure.
func TestPublishingATaskThatIsNotThereIsNotFound(t *testing.T) {
	handler := NewHandler(Options{Service: newFakeService()})

	response := request(t, handler, http.MethodPost,
		"/v1/tasks/00000000-0000-4000-8000-000000000000/publication/plan")
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d, body: %s", response.Code, http.StatusNotFound, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "not_found") {
		t.Errorf("body = %s", response.Body.String())
	}
}
