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

// TestAPublicationSaysWhatItIsBoundOnAndNotOnlyWhereToDialIt makes the recorded
// address readable by the surfaces that print the publication.
//
// The field was carried into this payload and then read by nothing: every
// surface rendered Address, which is localhost for a loopback binding and
// localhost for a binding on every interface. These are the two questions kept
// apart — where to dial the service, and what the port is open to.
func TestAPublicationSaysWhatItIsBoundOnAndNotOnlyWhereToDialIt(t *testing.T) {
	for name, testCase := range map[string]struct {
		allocation PortAllocation
		binding    string
		everywhere bool
	}{
		"the loopback address": {
			allocation: PortAllocation{HostIP: "127.0.0.1", Address: "localhost:21000"},
			binding:    "127.0.0.1",
		},
		// The same address as the loopback case prints, and not the same binding.
		"every interface": {
			allocation: PortAllocation{HostIP: "0.0.0.0", Address: "localhost:21000"},
			binding:    "0.0.0.0",
			everywhere: true,
		},
		"its IPv6 counterpart": {
			allocation: PortAllocation{HostIP: "::", Address: "localhost:21000"},
			binding:    "::",
			everywhere: true,
		},
		"one address of this machine": {
			allocation: PortAllocation{HostIP: "192.168.64.7", Address: "192.168.64.7:21000"},
			binding:    "192.168.64.7",
		},
		// A record written before Feat had a bind address of its own, whose
		// containers were given every address. Said in words, because there is no
		// address to print and printing nothing would read as a narrow binding.
		"a record from before there was one": {
			allocation: PortAllocation{Address: "localhost:21000"},
			binding:    "every address",
			everywhere: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := testCase.allocation.Binding(); got != testCase.binding {
				t.Errorf("the publication is bound on %q, want %q", got, testCase.binding)
			}
			if got := testCase.allocation.BoundEverywhere(); got != testCase.everywhere {
				t.Errorf("bound on every interface = %t, want %t", got, testCase.everywhere)
			}

			// And the runtime answers for the whole list, which is what decides
			// whether the sentence explaining such a binding is printed at all.
			runtime := &Runtime{Allocations: []PortAllocation{
				{HostIP: "127.0.0.1", Address: "localhost:21001"},
				testCase.allocation,
			}}
			if got := runtime.BoundEverywhere(); got != testCase.everywhere {
				t.Errorf("a runtime holding this publication reports %t, want %t", got, testCase.everywhere)
			}
		})
	}

	if (&Runtime{}).BoundEverywhere() {
		t.Error("a runtime that has allocated nothing was reported as publishing on every interface")
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
