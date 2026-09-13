package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/api"
)

// The documents below are pinned against golden files rather than checked field
// by field, because what is under test is the whole shape a caller parses. A
// field that changed name, moved, or stopped being printed is the defect, and
// only the whole document shows it.
//
// The fixtures are the same wire payloads the table tests render, so the two
// renderings of one response cannot drift apart in a way no test notices.

// TestTaskListDocument pins what `feat task list --json` prints.
//
// The archived task is in the fixture on purpose: the table counts it and does
// not show it, and the document carries it, which is the one place the two
// renderings deliberately disagree.
func TestTaskListDocument(t *testing.T) {
	archived := draftTask()
	archived.ID = "3d5f7b91-2c4e-4a63-8b7d-0f1e2a3b4c5d"
	archived.Key = "3d5f7b91"
	archived.Title = "Abandoned before it started"
	archived.Workflow = "archived"

	var out bytes.Buffer
	if err := emitJSON(&out, api.NewTaskList([]api.Task{launchedTask(), draftTask(), archived})); err != nil {
		t.Fatalf("printing the task list: %v", err)
	}
	compareDocument(t, "task-list.json", out.String())
}

// TestAnEmptyTaskListIsAnEmptyList checks the shape a machine reads when a
// person would be told "no tasks": a document with nothing in it, rather than
// the advice that follows the table or a null the caller has to guard.
func TestAnEmptyTaskListIsAnEmptyList(t *testing.T) {
	var out bytes.Buffer
	if err := emitJSON(&out, api.NewTaskList(nil)); err != nil {
		t.Fatalf("printing an empty task list: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "{\n  \"tasks\": []\n}" {
		t.Errorf("an empty list printed as:\n%s", got)
	}
}

// TestReviewDocument pins what `feat task review --json` prints.
func TestReviewDocument(t *testing.T) {
	var out bytes.Buffer
	if err := emitJSON(&out, reviewStatus()); err != nil {
		t.Fatalf("printing a review: %v", err)
	}
	compareDocument(t, "review.json", out.String())
}

// TestRuntimeStatusDocument pins what `feat runtime status --json` prints.
func TestRuntimeStatusDocument(t *testing.T) {
	var out bytes.Buffer
	if err := emitJSON(&out, runtimeStatus()); err != nil {
		t.Fatalf("printing a runtime status: %v", err)
	}
	compareDocument(t, "runtime-status.json", out.String())
}

// TestProjectShowDocumentDescribesTheResolvedConfiguration runs the real
// command against a real configuration file, because that is the only way to
// see that the values are resolved: a fixture built in Go would have the "~"
// already expanded by whoever wrote it.
//
// It is checked field by field rather than against a golden, because the paths
// in it are a temporary directory's and no golden can hold those.
func TestProjectShowDocumentDescribesTheResolvedConfiguration(t *testing.T) {
	machine := prepare(t)
	machine.configure(t, "app", projectFixture)

	code, stdout, stderr := machine.run(t, "project", "show", "app", "--json")
	if code != ExitOK {
		t.Fatalf("exit = %d, stderr = %s", code, stderr)
	}

	var document api.ProjectConfiguration
	if err := json.Unmarshal([]byte(stdout), &document); err != nil {
		t.Fatalf("the document does not parse: %v\n%s", err, stdout)
	}

	if document.ID != "app" {
		t.Errorf("id = %q, want app", document.ID)
	}
	if document.PrimaryRepository != "api" {
		t.Errorf("primary_repository = %q, want api", document.PrimaryRepository)
	}
	if len(document.Repositories) != 2 {
		t.Fatalf("the document describes %d repositories, want 2", len(document.Repositories))
	}

	byID := map[string]api.ConfiguredRepository{}
	for _, repository := range document.Repositories {
		byID[repository.ID] = repository
	}

	primary, ok := byID["api"]
	if !ok {
		t.Fatalf("the document does not describe the api repository")
	}
	if !primary.Primary {
		t.Errorf("api is the project's primary repository and the document does not say so")
	}
	if strings.HasPrefix(primary.HostPath, "~") {
		t.Errorf("host_path = %q, and the document is the resolved configuration", primary.HostPath)
	}
	if primary.AgentPath != "/srv/api" {
		t.Errorf("agent_path = %q, want /srv/api", primary.AgentPath)
	}
	if primary.RuntimePath != "/app" {
		t.Errorf("runtime_path = %q, want /app", primary.RuntimePath)
	}

	// A repository whose code no service runs still carries every key, so that a
	// caller reads it without asking whether the field is there.
	store, ok := byID["store"]
	if !ok {
		t.Fatalf("the document does not describe the store repository")
	}
	if store.RuntimePath != "" {
		t.Errorf("runtime_path = %q for a repository no service runs", store.RuntimePath)
	}
	if store.RuntimeServices == nil {
		t.Errorf("runtime_services is null, and a caller should be able to iterate it")
	}
	if store.DefaultAccess != "selectable" {
		t.Errorf("default_access = %q, want selectable", store.DefaultAccess)
	}

	// The sections are the rest of what the table prints, in its own order.
	if len(document.Sections) == 0 {
		t.Fatal("the document describes no resolved values")
	}
	if !hasField(document.Sections, "execution.mode", "devcontainer") {
		t.Errorf("the document does not resolve execution.mode to devcontainer:\n%s", stdout)
	}
}

// TestAFailureLeavesTheDocumentStreamEmpty is what an error looks like in the
// document: it does not appear in one.
//
// A parser reading standard output gets a document or nothing, never prose and
// never a document describing a failure. What says a command failed is the exit
// code, which ADR-027 already made the machine-readable answer.
func TestAFailureLeavesTheDocumentStreamEmpty(t *testing.T) {
	machine := prepare(t)

	code, stdout, stderr := machine.run(t, "task", "list", "--json")
	if code != ExitNotRunning {
		t.Errorf("exit = %d, want %d for an absent daemon", code, ExitNotRunning)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("a failed run printed this on standard output:\n%s", stdout)
	}
	if !strings.Contains(stderr, "daemon") {
		t.Errorf("standard error does not say what went wrong:\n%s", stderr)
	}
}

// hasField reports whether a resolved value appears under a dotted name.
func hasField(sections []api.ConfigurationSection, name, value string) bool {
	for _, section := range sections {
		for _, field := range section.Fields {
			if field.Name == name && field.Value == value {
				return true
			}
		}
	}
	return false
}

// compareDocument checks a printed document against its golden file, which
// `go test ./internal/cli -update` rewrites.
func compareDocument(t *testing.T, name, got string) {
	t.Helper()

	golden := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatalf("writing %s: %v", golden, err)
		}
		return
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("reading %s: %v\nRun: go test ./internal/cli -update", golden, err)
	}
	if got != string(want) {
		t.Errorf("the document changed.\n\ngot:\n%s\nwant:\n%s\n"+
			"A printed document is what a caller parses. If this change is intended, update "+
			"schema/feat-output.schema.json with it, then run:\n\tgo test ./internal/cli -update",
			got, string(want))
	}
}
