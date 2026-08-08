package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/store/storetest"
)

// TestRecoveryResponseBodies pins the reconciliation and cleanup surface.
//
// The wire format is a published surface (ADR-027), and these payloads decide
// what a user is shown before they remove something: a field that changed
// meaning without failing here would change what a screen says it is about to
// delete.
func TestRecoveryResponseBodies(t *testing.T) {
	task := storetest.TaskID.String()

	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "reconciliation", method: http.MethodPost, path: "/v1/reconciliation"},
		{name: "cleanup-plan", method: http.MethodPost, path: "/v1/tasks/" + task + "/cleanup/plan"},
		{
			name: "cleanup-executed", method: http.MethodPost, path: "/v1/tasks/" + task + "/cleanup/execute",
			body: `{"token":"0f1e2d3c","classes":[{"class":"worktrees",` +
				`"confirmed_warnings":["the worktree has uncommitted or untracked changes"]}],"archive":true}`,
		},
		{name: "resume", method: http.MethodPost, path: "/v1/tasks/" + task + "/resume"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := NewHandler(Options{Service: newFakeService()})

			response := requestBody(t, handler, test.method, test.path, test.body)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body: %s", response.Code, response.Body.String())
			}
			compare(t, test.name+".golden", response.Body.String())
		})
	}
}

// TestReconciliationIsReadableWithoutRunningOne separates the two verbs on one
// path.
//
// A GET must not trigger a pass: reconciliation asks the container runtime about
// every task, and a dashboard polling a read would be asking Docker several
// times a minute.
func TestReconciliationIsReadableWithoutRunningOne(t *testing.T) {
	service := newFakeService()
	handler := NewHandler(Options{Service: service})

	response := request(t, handler, http.MethodGet, "/v1/reconciliation")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", response.Code, response.Body.String())
	}
	if len(service.actions) != 0 {
		t.Errorf("reading the report ran %v; a GET must not reconcile", service.actions)
	}
	if !strings.Contains(response.Body.String(), `"ran": false`) {
		t.Errorf("body = %s, want it to say no pass has run", response.Body.String())
	}

	if response := request(t, handler, http.MethodPost, "/v1/reconciliation"); response.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", response.Code, response.Body.String())
	}
	if len(service.actions) != 1 || service.actions[0] != "reconcile" {
		t.Errorf("actions = %v, want exactly one reconcile", service.actions)
	}
}

// TestACleanupWithoutAPlanTokenIsRefused is the destructive-request rule: a
// request that removes something says which plan it was shown.
//
// It is refused by the transport rather than passed through, because "which plan
// was this" is the one question a destructive request must not leave open — and
// a client that sent nothing should be told rather than have a daemon guess.
func TestACleanupWithoutAPlanTokenIsRefused(t *testing.T) {
	service := newFakeService()
	handler := NewHandler(Options{Service: service})

	for _, body := range []string{"", "{}", `{"classes":[{"class":"worktrees"}]}`} {
		response := requestBody(t, handler, http.MethodPost,
			"/v1/tasks/"+storetest.TaskID.String()+"/cleanup/execute", body)

		if response.Code != http.StatusBadRequest {
			t.Errorf("status = %d for body %q, want 400", response.Code, body)
		}
		if !strings.Contains(response.Body.String(), "token") {
			t.Errorf("body = %s, want the reason the cleanup was refused", response.Body.String())
		}
	}
	if len(service.selections) != 0 {
		t.Errorf("a cleanup with no token reached the daemon: %+v", service.selections)
	}
}

// TestACleanupCarriesIdentifiersAndConfirmations checks that what reaches the
// daemon is what the request said, warnings included.
//
// The confirmed warnings are the mechanism behind FR-CLEAN-003, so a transport
// that dropped them would turn an explicit confirmation into an implicit one
// without anything failing.
func TestACleanupCarriesIdentifiersAndConfirmations(t *testing.T) {
	service := newFakeService()
	handler := NewHandler(Options{Service: service})

	response := requestBody(t, handler, http.MethodPost,
		"/v1/tasks/"+storetest.TaskID.String()+"/cleanup/execute",
		`{"token":"0f1e2d3c","classes":[{"class":"worktrees","confirmed_warnings":["dirty"]},`+
			`{"class":"branches"}],"archive":false}`)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", response.Code, response.Body.String())
	}
	if len(service.selections) != 1 {
		t.Fatalf("the daemon received %d selections, want 1", len(service.selections))
	}

	selection := service.selections[0]
	if selection.Token != "0f1e2d3c" {
		t.Errorf("token = %q", selection.Token)
	}
	if len(selection.Classes) != 2 {
		t.Fatalf("classes = %+v, want two", selection.Classes)
	}
	if len(selection.Classes[0].ConfirmedWarnings) != 1 || selection.Classes[0].ConfirmedWarnings[0] != "dirty" {
		t.Errorf("confirmations = %+v, want the warning the user accepted", selection.Classes[0])
	}
	if selection.Archive {
		t.Error("the archive flag was set on a request that did not ask for one")
	}
}

// TestPlanningACleanupTakesNoBody keeps the plan a question.
//
// It carries an identifier and nothing else, for the reason every other task
// endpoint does: the resources are the daemon's to resolve, never the caller's
// to supply.
func TestPlanningACleanupTakesNoBody(t *testing.T) {
	handler := NewHandler(Options{Service: newFakeService()})

	response := requestBody(t, handler, http.MethodPost,
		"/v1/tasks/"+storetest.TaskID.String()+"/cleanup/plan", `{"paths":["/etc"]}`)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body: %s", response.Code, response.Body.String())
	}
}

// TestResumeTakesNoBody is the same rule for the recovery action: which session
// is continued is the daemon's record, never a value a caller supplies.
func TestResumeTakesNoBody(t *testing.T) {
	handler := NewHandler(Options{Service: newFakeService()})

	response := requestBody(t, handler, http.MethodPost,
		"/v1/tasks/"+storetest.TaskID.String()+"/resume", `{"session_id":"somebody-elses"}`)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body: %s", response.Code, response.Body.String())
	}
}
