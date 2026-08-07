package api

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/store/storetest"
)

// runtimePath is the endpoint of one action for the fixture task.
func runtimePath(action string) string {
	return "/v1/tasks/" + storetest.TaskID.String() + "/runtime/" + action
}

// TestRuntimeResponseBodies pins the manual lifecycle's published surface.
//
// One endpoint per action, because each is a separate thing a user asks for and
// because the path is what names it: an action Feat does not perform is a 404
// rather than a request the daemon had to interpret.
func TestRuntimeResponseBodies(t *testing.T) {
	for _, test := range []struct {
		name   string
		path   string
		body   string
		golden string
	}{
		{name: "create", path: runtimePath("create"), golden: "runtime-status.golden"},
		{name: "start", path: runtimePath("start"), golden: "runtime-status.golden"},
		{name: "stop", path: runtimePath("stop"), golden: "runtime-status.golden"},
		{name: "status", path: runtimePath("status"), golden: "runtime-status.golden"},
		{name: "destroy", path: runtimePath("destroy"), body: `{"confirm":true}`,
			golden: "runtime-status.golden"},
		{name: "logs-info", path: runtimePath("logs-info"), golden: "runtime-logs.golden"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := newFakeService()
			handler := NewHandler(Options{Service: service})

			response := requestBody(t, handler, http.MethodPost, test.path, test.body)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body: %s", response.Code, response.Body.String())
			}
			compare(t, test.golden, response.Body.String())

			if test.name != "logs-info" && !slices.Contains(service.actions, test.name) {
				t.Errorf("the endpoint reached the actions %v, and its path names %q",
					service.actions, test.name)
			}
		})
	}
}

// TestDestroyingWithoutConfirmationIsRefused keeps a removal behind something
// somebody meant.
//
// It is the rule ADR-031 applied in the other direction: what is created is what
// was displayed, and what is removed is what was confirmed. Volumes are retained
// either way, which the refusal says so that a user is not left believing the
// confirmation is about them.
func TestDestroyingWithoutConfirmationIsRefused(t *testing.T) {
	service := newFakeService()
	handler := NewHandler(Options{Service: service})

	for name, body := range map[string]string{
		"an empty body":       "",
		"an explicit refusal": `{"confirm":false}`,
	} {
		t.Run(name, func(t *testing.T) {
			response := requestBody(t, handler, http.MethodPost, runtimePath("destroy"), body)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d, body: %s",
					response.Code, http.StatusBadRequest, response.Body.String())
			}
			if !strings.Contains(response.Body.String(), "Volumes are retained") {
				t.Errorf("the refusal does not say what is retained: %s", response.Body.String())
			}
			if len(service.actions) != 0 {
				t.Fatalf("an unconfirmed destroy reached the daemon: %v", service.actions)
			}
		})
	}
}

// TestAnUnknownRuntimeActionIsNotAnInstruction keeps the vocabulary closed.
//
// The action is a path segment, so something Feat does not do is an endpoint
// that does not exist rather than a field the daemon has to decide what to make
// of.
func TestAnUnknownRuntimeActionIsNotAnInstruction(t *testing.T) {
	service := newFakeService()
	handler := NewHandler(Options{Service: service})

	response := request(t, handler, http.MethodPost, runtimePath("restart"))
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d, body: %s", response.Code, http.StatusNotFound, response.Body.String())
	}
	if len(service.actions) != 0 {
		t.Fatalf("an unknown action reached the daemon: %v", service.actions)
	}
}

// TestARuntimeActionCarriesNothingToExecute is the rule the shell endpoint
// follows, applied here.
//
// The daemon runs these commands on its owner's behalf, so a caller that sent a
// service list, a Compose file, or a program is told that Feat does not take one
// rather than left believing it did.
func TestARuntimeActionCarriesNothingToExecute(t *testing.T) {
	service := newFakeService()
	handler := NewHandler(Options{Service: service})

	for _, path := range []string{runtimePath("start"), runtimePath("logs-info")} {
		response := requestBody(t, handler, http.MethodPost, path, `{"services":["postgres"]}`)
		if response.Code != http.StatusBadRequest {
			t.Errorf("%s accepted a body naming what to run: status = %d, body: %s",
				path, response.Code, response.Body.String())
		}
	}
	if len(service.actions) != 0 {
		t.Fatalf("a request carrying instructions reached the daemon: %v", service.actions)
	}
}
