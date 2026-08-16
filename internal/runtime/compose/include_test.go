package compose_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/runtime"
	"github.com/ma8el/feat/internal/runtime/compose"
	"github.com/ma8el/feat/internal/runtime/compose/runtimetest"
)

// TestTheGeneratedIncludeIsPinned holds the document that joins a project's
// repositories into one application to a golden file.
//
// Every line of it is a decision: which files each repository brings, and which
// directory that repository's own relative paths resolve against. Listing the
// same files with --file instead resolves every relative path against one
// directory, and the second repository's build contexts then point into the
// first (ADR-065 evidence 2).
func TestTheGeneratedIncludeIsPinned(t *testing.T) {
	docker := runtimetest.New()
	_, spec := arrange(t, docker)

	written, err := os.ReadFile(spec.IncludePath)
	if err != nil {
		t.Fatalf("reading the generated include: %v", err)
	}

	golden := filepath.Join("testdata", "include.golden")
	if *update {
		if err := os.WriteFile(golden, written, 0o600); err != nil {
			t.Fatalf("updating the golden file: %v", err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("reading the golden file: %v", err)
	}
	if string(written) != string(want) {
		t.Errorf("the generated include changed\n got:\n%s\nwant:\n%s", written, want)
	}
}

// TestTheIncludeCarriesEachRepositorysOwnDirectory says the properties the
// golden happens to show, so that a deliberate update to the document cannot
// quietly drop one.
func TestTheIncludeCarriesEachRepositorysOwnDirectory(t *testing.T) {
	docker := runtimetest.New()
	_, spec := arrange(t, docker)

	written, err := os.ReadFile(spec.IncludePath)
	if err != nil {
		t.Fatalf("reading the generated include: %v", err)
	}
	document := string(written)

	if count := strings.Count(document, "project_directory:"); count != len(spec.Includes) {
		t.Errorf("%d entries carry a project directory, want one per repository (%d):\n%s",
			count, len(spec.Includes), document)
	}
	for _, include := range spec.Includes {
		if !strings.Contains(document, `project_directory: "`+include.Directory+`"`) {
			t.Errorf("%s's own directory is not the project directory of its entry:\n%s",
				include.Repository, document)
		}
		for _, file := range include.Files {
			if !strings.Contains(document, `"`+file+`"`) {
				t.Errorf("the include does not bring %s:\n%s", file, document)
			}
		}
	}
	// The long form, because the short form takes no project directory, and the
	// project directory is the whole point.
	if strings.Contains(document, "include:\n  - /") {
		t.Errorf("an entry uses the short form, which cannot carry a project directory:\n%s", document)
	}
}

// TestTheIncludeIsThereBeforeAnythingIsCreated covers the first thing a user
// does.
//
// Every command names the include document, and a status of a runtime nothing
// has created is the first one they run. A document written only when something
// is created would answer "what is running?" with a Compose error about a file
// Feat generates — which is the defect ADR-034 recorded for the generated
// override, arriving one document over.
func TestTheIncludeIsThereBeforeAnythingIsCreated(t *testing.T) {
	docker := runtimetest.New()
	_, spec := arrange(t, docker)

	info, err := os.Stat(spec.IncludePath)
	if err != nil {
		t.Fatalf("the include was not written before anything was created: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("the generated include is mode %o, want 600", mode)
	}
	if !strings.HasPrefix(spec.IncludePath, spec.Directory+string(filepath.Separator)) {
		t.Errorf("the include is at %s, which is outside the Compose project directory %s",
			spec.IncludePath, spec.Directory)
	}
}

// TestAnUnchangedIncludeIsNotRewritten keeps a poll from writing a file four
// times a minute per task for nothing.
func TestAnUnchangedIncludeIsNotRewritten(t *testing.T) {
	docker := runtimetest.New()
	_, spec := arrange(t, docker)

	// Something a rewrite would replace, and a skipped write would leave.
	marker := []byte("# rewritten\n")
	body, err := os.ReadFile(spec.IncludePath)
	if err != nil {
		t.Fatalf("reading the generated include: %v", err)
	}
	if err := os.WriteFile(spec.IncludePath, append(body, marker...), 0o600); err != nil {
		t.Fatalf("marking the generated include: %v", err)
	}

	if _, err := compose.New(spec, compose.Options{Runner: docker}); err != nil {
		t.Fatalf("rebuilding the runtime: %v", err)
	}
	after, err := os.ReadFile(spec.IncludePath)
	if err != nil {
		t.Fatalf("reading the generated include: %v", err)
	}
	if strings.Contains(string(after), "# rewritten") {
		t.Error("the include was left alone when its own contents had changed")
	}

	// And a composition that changed is written: the file follows the
	// specification rather than whatever was there first.
	changed := spec
	changed.Includes = []runtime.Include{{
		Repository: "api",
		Directory:  "/repos/app/api",
		Files:      []string{"/repos/app/api/docker-compose.yml"},
	}}
	if _, err := compose.New(changed, compose.Options{Runner: docker}); err != nil {
		t.Fatalf("rebuilding the runtime: %v", err)
	}
	final, err := os.ReadFile(spec.IncludePath)
	if err != nil {
		t.Fatalf("reading the generated include: %v", err)
	}
	if strings.Contains(string(final), "/repos/app/store") {
		t.Errorf("a repository that left the composition is still included:\n%s", final)
	}
}
