package fs

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/ma8el/feat/internal/store"
	"github.com/ma8el/feat/internal/store/storetest"
)

var update = flag.Bool("update", false, "rewrite golden files")

// TestStoredFormat pins the layout and the content of the state directory.
//
// The files are a compatibility surface: docs/07-configuration-model.md requires
// every schema change to be explicit and migrated. Comparing against golden
// files makes an accidental change to the format fail here, where the fix is to
// add a schema version and a migration, rather than in a user's state directory
// after an upgrade.
func TestStoredFormat(t *testing.T) {
	ctx := context.Background()
	filestore := newStore(t)
	task := storetest.Task()
	ref := store.Ref(task)
	published := storetest.Published()

	if err := filestore.Projects().Save(ctx, storetest.Project()); err != nil {
		t.Fatalf("saving the project: %v", err)
	}
	if err := filestore.Tasks().Save(ctx, task); err != nil {
		t.Fatalf("saving the task: %v", err)
	}
	// A second task, because the ticket a brief was composed from and the
	// publication a task recorded are shapes the first one cannot hold: a brief
	// comes from one source, and a task that was never published has no
	// publication.
	if err := filestore.Tasks().Save(ctx, published); err != nil {
		t.Fatalf("saving the published task: %v", err)
	}
	if err := filestore.Reviews().Save(ctx, ref, storetest.Review()); err != nil {
		t.Fatalf("saving the review: %v", err)
	}
	for _, event := range storetest.Events() {
		if _, err := filestore.Events().Append(ctx, ref, event); err != nil {
			t.Fatalf("appending an event: %v", err)
		}
	}

	projectDir := filepath.Join(filestore.Root(), projectsDir, storetest.ProjectID.String())
	taskDir := filepath.Join(projectDir, tasksDir, storetest.TaskID.String())
	publishedDir := filepath.Join(projectDir, tasksDir, storetest.PublishedID.String())
	files := map[string]string{
		"project.json.golden":        filepath.Join(projectDir, projectFile),
		"task.json.golden":           filepath.Join(taskDir, taskFile),
		"published-task.json.golden": filepath.Join(publishedDir, taskFile),
		"prompt.md.golden":           filepath.Join(taskDir, briefFile),
		"review.json.golden":         filepath.Join(taskDir, reviewFile),
		"events.jsonl.golden":        filepath.Join(taskDir, eventsFile),
	}

	for name, path := range files {
		golden := filepath.Join("testdata", name)
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}

		if *update {
			if err := os.WriteFile(golden, got, 0o644); err != nil {
				t.Fatalf("writing %s: %v", golden, err)
			}
			continue
		}

		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatalf("reading %s: %v\nRun: go test ./internal/store/fs -run TestStoredFormat -update", golden, err)
		}
		if string(got) != string(want) {
			t.Errorf("%s changed.\n\ngot:\n%s\nwant:\n%s\n"+
				"A change to the stored format needs a schema version and a migration "+
				"(docs/07-configuration-model.md). If this change is intended, add them, then run:\n"+
				"\tgo test ./internal/store/fs -run TestStoredFormat -update", name, got, want)
		}
	}
}

// TestTheStateLayoutMatchesTheArchitecture checks the paths themselves against
// docs/06-technical-architecture.md, so the layout cannot drift silently while
// the file contents still match.
func TestTheStateLayoutMatchesTheArchitecture(t *testing.T) {
	ctx := context.Background()
	filestore := newStore(t)
	task := storetest.Task()
	ref := store.Ref(task)

	if err := filestore.Projects().Save(ctx, storetest.Project()); err != nil {
		t.Fatalf("saving the project: %v", err)
	}
	if err := filestore.Tasks().Save(ctx, task); err != nil {
		t.Fatalf("saving the task: %v", err)
	}
	if err := filestore.Reviews().Save(ctx, ref, storetest.Review()); err != nil {
		t.Fatalf("saving the review: %v", err)
	}
	if _, err := filestore.Events().Append(ctx, ref, storetest.Events()[0]); err != nil {
		t.Fatalf("appending an event: %v", err)
	}

	want := []string{
		"projects/example/project.json",
		"projects/example/tasks/7f3a1c2e-5b6d-4a80-9c1f-2d3e4f5a6b7c/task.json",
		"projects/example/tasks/7f3a1c2e-5b6d-4a80-9c1f-2d3e4f5a6b7c/prompt.md",
		"projects/example/tasks/7f3a1c2e-5b6d-4a80-9c1f-2d3e4f5a6b7c/events.jsonl",
		"projects/example/tasks/7f3a1c2e-5b6d-4a80-9c1f-2d3e4f5a6b7c/review.json",
	}
	for _, relative := range want {
		if _, err := os.Stat(filepath.Join(filestore.Root(), filepath.FromSlash(relative))); err != nil {
			t.Errorf("%s is missing: %v", relative, err)
		}
	}
}
