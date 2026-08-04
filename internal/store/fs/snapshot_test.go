package fs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/store"
	"github.com/ma8el/feat/internal/store/storetest"
)

// sample is a stored document used to exercise the versioning machinery without
// waiting for a real schema change.
type sample struct {
	SchemaVersion int    `json:"schema_version"`
	ID            string `json:"id"`
	Title         string `json:"title"`
}

// TestADocumentFromANewerBuildIsRejected checks that state written by a newer
// Feat is refused rather than partially understood. Migrations are one way, so
// there is nothing this build could do with it.
func TestADocumentFromANewerBuildIsRejected(t *testing.T) {
	ctx := context.Background()
	filestore := newStore(t)
	path := filepath.Join(filestore.Root(), projectsDir, storetest.ProjectID.String(), projectFile)
	write(t, path, `{"schema_version":99,"id":"example","name":"Example"}`)

	_, err := filestore.Projects().Load(ctx, storetest.ProjectID)
	if !errors.Is(err, store.ErrUnsupportedSchema) {
		t.Fatalf("want ErrUnsupportedSchema, got %v", err)
	}

	var schema *store.SchemaError
	if !errors.As(err, &schema) {
		t.Fatalf("error is not a *store.SchemaError: %v", err)
	}
	if schema.Found != 99 || schema.Supported != projectSchemaVersion {
		t.Errorf("error reports version %d against %d", schema.Found, schema.Supported)
	}
	if !strings.Contains(err.Error(), "newer version") {
		t.Errorf("error does not say the state was written by a newer version: %v", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error does not name the file: %v", err)
	}
}

// TestADocumentWithoutASchemaVersionIsCorrupt checks that an unversioned
// document is never guessed at.
func TestADocumentWithoutASchemaVersionIsCorrupt(t *testing.T) {
	ctx := context.Background()
	filestore := newStore(t)
	write(t, filepath.Join(filestore.Root(), projectsDir, storetest.ProjectID.String(), projectFile),
		`{"id":"example","name":"Example"}`)

	_, err := filestore.Projects().Load(ctx, storetest.ProjectID)
	if !errors.Is(err, store.ErrCorrupt) {
		t.Fatalf("want ErrCorrupt, got %v", err)
	}
	if !strings.Contains(err.Error(), "schema_version") {
		t.Errorf("error does not name the missing field: %v", err)
	}
}

// TestAnUnreadableDocumentIsCorrupt checks that a truncated snapshot is reported
// as corrupt state naming the file, so the user can repair or discard it.
func TestAnUnreadableDocumentIsCorrupt(t *testing.T) {
	ctx := context.Background()
	filestore := newStore(t)
	path := filepath.Join(filestore.Root(), projectsDir, storetest.ProjectID.String(), projectFile)
	write(t, path, `{"schema_version":1,"id":"exam`)

	_, err := filestore.Projects().Load(ctx, storetest.ProjectID)
	if !errors.Is(err, store.ErrCorrupt) {
		t.Fatalf("want ErrCorrupt, got %v", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error does not name the file: %v", err)
	}
}

// TestASnapshotThatBreaksAnInvariantIsCorrupt checks that storage validates what
// it reads. A snapshot the domain would never have produced is corrupt state
// rather than a task to operate on.
func TestASnapshotThatBreaksAnInvariantIsCorrupt(t *testing.T) {
	ctx := context.Background()
	filestore := newStore(t)
	fixture := storetest.Task()

	if err := filestore.Tasks().Save(ctx, fixture); err != nil {
		t.Fatalf("saving the task: %v", err)
	}

	path := taskPath(t, filestore, taskFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the snapshot: %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("decoding the snapshot: %v", err)
	}
	delete(document, "session")
	edited, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encoding the snapshot: %v", err)
	}
	write(t, path, string(edited))

	if _, err := filestore.Tasks().Load(ctx, store.Ref(fixture)); !errors.Is(err, store.ErrCorrupt) {
		t.Fatalf("a task in a running state without a session loaded: %v", err)
	}
}

// TestAMigrationUpgradesAnOlderDocument checks the migration mechanism end to
// end with a synthetic schema change. v0.1 has one version of every document, so
// without this the first real migration would be the first one ever run.
func TestAMigrationUpgradesAnOlderDocument(t *testing.T) {
	upgrading := codec{
		kind:    "sample",
		version: 3,
		migrations: []migration{
			{from: 1, apply: func(document map[string]any) error {
				document["title"] = document["name"]
				delete(document, "name")
				return nil
			}},
			{from: 2, apply: func(document map[string]any) error {
				title, _ := document["title"].(string)
				document["title"] = strings.ToUpper(title)
				return nil
			}},
		},
	}

	var upgraded sample
	err := upgrading.unmarshal("example", "sample.json",
		[]byte(`{"schema_version":1,"id":"example","name":"before"}`), &upgraded)
	if err != nil {
		t.Fatalf("migrating: %v", err)
	}
	if upgraded.Title != "BEFORE" {
		t.Errorf("the migrated document holds %q", upgraded.Title)
	}
	if upgraded.SchemaVersion != 3 {
		t.Errorf("the migrated document reports version %d", upgraded.SchemaVersion)
	}
	if upgraded.ID != "example" {
		t.Errorf("the migrated document lost its identifier: %q", upgraded.ID)
	}
}

// TestAVersionWithoutAMigrationIsRejected checks that a gap in the migration
// chain stops the load rather than producing a document nobody upgraded.
func TestAVersionWithoutAMigrationIsRejected(t *testing.T) {
	gapped := codec{kind: "sample", version: 3, migrations: []migration{
		{from: 2, apply: func(map[string]any) error { return nil }},
	}}

	var upgraded sample
	err := gapped.unmarshal("example", "sample.json", []byte(`{"schema_version":1,"id":"example"}`), &upgraded)
	if !errors.Is(err, store.ErrUnsupportedSchema) {
		t.Fatalf("want ErrUnsupportedSchema, got %v", err)
	}
}

// TestAFailedMigrationIsReportedAgainstItsVersion checks that a migration which
// cannot interpret a document says which upgrade failed.
func TestAFailedMigrationIsReportedAgainstItsVersion(t *testing.T) {
	failing := codec{kind: "sample", version: 2, migrations: []migration{
		{from: 1, apply: func(map[string]any) error { return fmt.Errorf("the title is missing") }},
	}}

	var upgraded sample
	err := failing.unmarshal("example", "sample.json", []byte(`{"schema_version":1,"id":"example"}`), &upgraded)
	if !errors.Is(err, store.ErrCorrupt) {
		t.Fatalf("want ErrCorrupt, got %v", err)
	}
	if !strings.Contains(err.Error(), "schema version 1") {
		t.Errorf("error does not name the upgrade that failed: %v", err)
	}
}

// TestThePreviousDocumentIsRetainedBeforeAnUpgrade checks the retention rule in
// docs/07-configuration-model.md: an upgrade keeps the document it replaced, so
// a migration that turns out to be wrong is not the end of the state.
func TestThePreviousDocumentIsRetainedBeforeAnUpgrade(t *testing.T) {
	filestore := newStore(t)
	path := filepath.Join(filestore.Root(), "sample.json")
	original := `{"schema_version":1,"id":"example","title":"before"}`
	write(t, path, original)

	upgrading := codec{kind: "sample", version: 2}
	if err := filestore.writeSnapshot(upgrading, path, sample{SchemaVersion: 2, ID: "example", Title: "after"}); err != nil {
		t.Fatalf("writing the upgraded document: %v", err)
	}

	retained, err := os.ReadFile(path + ".v1.bak")
	if err != nil {
		t.Fatalf("reading the retained document: %v", err)
	}
	if string(retained) != original {
		t.Errorf("the retained document is %q", retained)
	}

	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the current document: %v", err)
	}
	if !strings.Contains(string(current), `"title": "after"`) {
		t.Errorf("the current document is %q", current)
	}
}

// TestAnUnchangedVersionIsNotRetained checks that ordinary saves do not litter
// the state directory with copies.
func TestAnUnchangedVersionIsNotRetained(t *testing.T) {
	ctx := context.Background()
	filestore := newStore(t)
	fixture := storetest.Project()

	for range 3 {
		if err := filestore.Projects().Save(ctx, fixture); err != nil {
			t.Fatalf("saving the project: %v", err)
		}
	}

	entries, err := os.ReadDir(filepath.Join(filestore.Root(), projectsDir, fixture.ID.String()))
	if err != nil {
		t.Fatalf("reading the project directory: %v", err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".bak") {
			t.Errorf("%s was retained without a schema change", entry.Name())
		}
	}
}
