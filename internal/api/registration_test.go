package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/store/storetest"
)

// post runs one request with a JSON body and returns the response.
func post(t *testing.T, handler http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

// TestRegisterProjectResponses pins the payloads of the first endpoint that
// changes state, in both of the outcomes it distinguishes.
func TestRegisterProjectResponses(t *testing.T) {
	t.Run("created", func(t *testing.T) {
		handler := NewHandler(Options{Service: newFakeService()})

		response := post(t, handler, "/v1/projects", `{"project_id":"fresh"}`)

		if response.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201, body: %s", response.Code, response.Body.String())
		}
		compare(t, "registration-created.golden", response.Body.String())
	})

	t.Run("updated", func(t *testing.T) {
		handler := NewHandler(Options{Service: newFakeService()})

		// Registering a project Feat already has re-reads its configuration.
		// That is a 200 rather than a 201, so that a user running the command
		// twice can tell which of the two happened.
		response := post(t, handler, "/v1/projects",
			`{"project_id":"`+storetest.ProjectID.String()+`"}`)

		if response.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body: %s", response.Code, response.Body.String())
		}
		compare(t, "registration-updated.golden", response.Body.String())
	})
}

// TestRegisterProjectRejectsBadRequests checks that the endpoint validates
// before it acts.
func TestRegisterProjectRejectsBadRequests(t *testing.T) {
	for name, testCase := range map[string]struct {
		body    string
		status  int
		code    string
		arrange func(*fakeService)
	}{
		"not json": {
			body: `not json`, status: http.StatusBadRequest, code: CodeInvalid,
		},
		"unknown field": {
			// A client that asked for something Feat did not do should be told,
			// not left to assume it worked.
			body: `{"project_id":"app","force":true}`, status: http.StatusBadRequest, code: CodeInvalid,
		},
		"two documents": {
			body: `{"project_id":"app"}{"project_id":"other"}`, status: http.StatusBadRequest, code: CodeInvalid,
		},
		"missing identifier": {
			body: `{}`, status: http.StatusBadRequest, code: CodeInvalid,
		},
		"identifier escapes the configuration directory": {
			body: `{"project_id":"../../etc/passwd"}`, status: http.StatusBadRequest, code: CodeInvalid,
		},
		"no configuration file": {
			body: `{"project_id":"absent"}`, status: http.StatusNotFound, code: CodeNotFound,
			arrange: func(f *fakeService) { f.unconfigured["absent"] = true },
		},
		"configuration does not validate": {
			body: `{"project_id":"broken"}`, status: http.StatusBadRequest, code: CodeInvalid,
			arrange: func(f *fakeService) { f.unregistrable["broken"] = true },
		},
	} {
		t.Run(name, func(t *testing.T) {
			service := newFakeService()
			if testCase.arrange != nil {
				testCase.arrange(service)
			}
			handler := NewHandler(Options{Service: service})

			response := post(t, handler, "/v1/projects", testCase.body)

			if response.Code != testCase.status {
				t.Errorf("status = %d, want %d, body: %s", response.Code, testCase.status, response.Body.String())
			}

			var envelope struct {
				Error Error `json:"error"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("the error body is not JSON: %v\n%s", err, response.Body.String())
			}
			if envelope.Error.Code != testCase.code {
				t.Errorf("code = %q, want %q", envelope.Error.Code, testCase.code)
			}
			if envelope.Error.Message == "" {
				t.Error("the error carries no message")
			}
		})
	}
}

// TestMalformedIdentifierNeverReachesTheDaemon checks that an identifier which
// could be joined into a path is rejected by the transport.
func TestMalformedIdentifierNeverReachesTheDaemon(t *testing.T) {
	service := newFakeService()
	handler := NewHandler(Options{Service: service})

	before := len(service.projects)
	for _, id := range []string{"../escape", "/etc", "a/b", "Upper", ""} {
		body, err := json.Marshal(RegisterProject{ProjectID: id})
		if err != nil {
			t.Fatalf("building the request: %v", err)
		}
		response := post(t, handler, "/v1/projects", string(body))
		if response.Code != http.StatusBadRequest {
			t.Errorf("identifier %q produced status %d, want 400", id, response.Code)
		}
	}
	if len(service.projects) != before {
		t.Error("a malformed identifier reached the daemon and registered something")
	}
}

// TestProjectsEndpointServesBothMethods checks that adding a write did not take
// the read away, and that the 405 still names what the endpoint does serve.
func TestProjectsEndpointServesBothMethods(t *testing.T) {
	handler := NewHandler(Options{Service: newFakeService()})

	if response := request(t, handler, http.MethodGet, "/v1/projects"); response.Code != http.StatusOK {
		t.Errorf("GET /v1/projects = %d, want 200", response.Code)
	}

	response := request(t, handler, http.MethodDelete, "/v1/projects")
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("DELETE /v1/projects = %d, want 405", response.Code)
	}
	if got, want := response.Header().Get("Allow"), "GET, HEAD, POST"; got != want {
		t.Errorf("Allow = %q, want %q", got, want)
	}
}

// TestRegistrationIsIdempotent checks that registering twice records one
// project, because a user who edits their configuration runs the command again.
func TestRegistrationIsIdempotent(t *testing.T) {
	service := newFakeService()
	handler := NewHandler(Options{Service: service})

	first := post(t, handler, "/v1/projects", `{"project_id":"fresh"}`)
	if first.Code != http.StatusCreated {
		t.Fatalf("first registration = %d, want 201", first.Code)
	}
	second := post(t, handler, "/v1/projects", `{"project_id":"fresh"}`)
	if second.Code != http.StatusOK {
		t.Fatalf("second registration = %d, want 200", second.Code)
	}

	var count int
	for _, project := range service.projects {
		if project.ID == domain.ProjectID("fresh") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("registering twice recorded the project %d times, want 1", count)
	}
}
