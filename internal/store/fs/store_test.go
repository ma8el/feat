package fs

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/store"
	"github.com/ma8el/feat/internal/store/storetest"
)

// TestTaskWithSeveralRepositoriesRoundTripsExactly checks the slice 1 acceptance
// criterion. The fixture binds two repositories with different access, owns a
// session and a runtime, and carries observations, so a field the mapping
// forgets is a field that comes back changed.
func TestTaskWithSeveralRepositoriesRoundTripsExactly(t *testing.T) {
	ctx := context.Background()
	fixture := storetest.Task()
	tasks := newStore(t).Tasks()

	if err := tasks.Save(ctx, fixture); err != nil {
		t.Fatalf("saving the task: %v", err)
	}
	loaded, err := tasks.Load(ctx, store.Ref(fixture))
	if err != nil {
		t.Fatalf("loading the task: %v", err)
	}

	if !reflect.DeepEqual(fixture, loaded) {
		t.Errorf("the task changed on the way through storage.\n got: %#v\nwant: %#v", loaded, fixture)
	}
	if len(loaded.Repositories) != 2 {
		t.Fatalf("the task came back with %d repositories", len(loaded.Repositories))
	}

	primary, ok := loaded.Repository(storetest.PrimaryRepositoryID)
	if !ok {
		t.Fatalf("%s is missing from the loaded task", storetest.PrimaryRepositoryID)
	}
	if primary.Access != domain.TaskAccessReadWrite || primary.Branch == "" {
		t.Errorf("the read-write binding came back as %s with branch %q", primary.Access, primary.Branch)
	}
	secondary, ok := loaded.Repository(storetest.SecondaryRepositoryID)
	if !ok {
		t.Fatalf("%s is missing from the loaded task", storetest.SecondaryRepositoryID)
	}
	if secondary.Access != domain.TaskAccessReadOnly || secondary.Branch != "" {
		t.Errorf("the read-only binding came back as %s with branch %q", secondary.Access, secondary.Branch)
	}
	if primary.BaseCommit == secondary.BaseCommit {
		t.Error("both repositories came back with the same base commit")
	}
}

// TestFixturesPopulateEveryPersistedField is what makes the round-trip tests
// mean something: a field that no fixture sets would round-trip perfectly
// without ever being written.
func TestFixturesPopulateEveryPersistedField(t *testing.T) {
	fixtures := map[string]any{
		"project": storetest.Project(),
		"task":    storetest.Task(),
		"review":  storetest.Review(),
	}
	for name, fixture := range fixtures {
		if unpopulated := storetest.UnpopulatedFields(fixture); len(unpopulated) > 0 {
			t.Errorf("the %s fixture leaves %v unset, so the round-trip test does not cover them",
				name, unpopulated)
		}
	}
}

// TestProjectRoundTripsExactly checks the project aggregate, including its
// repository topology.
func TestProjectRoundTripsExactly(t *testing.T) {
	ctx := context.Background()
	fixture := storetest.Project()
	projects := newStore(t).Projects()

	if err := projects.Save(ctx, fixture); err != nil {
		t.Fatalf("saving the project: %v", err)
	}
	loaded, err := projects.Load(ctx, fixture.ID)
	if err != nil {
		t.Fatalf("loading the project: %v", err)
	}
	if !reflect.DeepEqual(fixture, loaded) {
		t.Errorf("the project changed on the way through storage.\n got: %#v\nwant: %#v", loaded, fixture)
	}
}

// TestReviewRoundTripsExactly checks that review state survives a restart,
// including the base commit each repository was compared against.
func TestReviewRoundTripsExactly(t *testing.T) {
	ctx := context.Background()
	fixture := storetest.Review()
	ref := store.TaskRef{Project: storetest.ProjectID, Task: storetest.TaskID}
	reviews := newStore(t).Reviews()

	if err := reviews.Save(ctx, ref, fixture); err != nil {
		t.Fatalf("saving the review: %v", err)
	}
	loaded, err := reviews.Load(ctx, ref)
	if err != nil {
		t.Fatalf("loading the review: %v", err)
	}
	if !reflect.DeepEqual(fixture, loaded) {
		t.Errorf("the review changed on the way through storage.\n got: %#v\nwant: %#v", loaded, fixture)
	}

	primary, ok := loaded.Repository(storetest.PrimaryRepositoryID)
	if !ok {
		t.Fatal("the primary repository is missing from the loaded review")
	}
	if primary.BaseCommit != storetest.PrimaryBaseCommit {
		t.Errorf("the review compares against %s", primary.BaseCommit)
	}
}

// TestAReviewWrittenBeforeTheDecisionMovedStillLoads covers the one risk ADR-047
// took by removing two stored fields without moving the schema version.
//
// A document written by an earlier build carries `status` and `decided_at`. The
// decision they held is the task's workflow state, which the task snapshot beside
// this one already records, so nothing has to be upgraded — but a state directory
// that had been in use had to keep loading, and the next save had to stop writing
// what nothing reads.
func TestAReviewWrittenBeforeTheDecisionMovedStillLoads(t *testing.T) {
	ctx := context.Background()
	ref := store.TaskRef{Project: storetest.ProjectID, Task: storetest.TaskID}
	filestore := newStore(t)
	reviews := filestore.Reviews()

	// Saved by this build, then edited to what the build before it wrote.
	if err := reviews.Save(ctx, ref, storetest.Review()); err != nil {
		t.Fatalf("saving the review: %v", err)
	}
	dir, err := filestore.taskDir(ref)
	if err != nil {
		t.Fatalf("resolving the task directory: %v", err)
	}
	path := filepath.Join(dir, reviewFile)
	current, err := os.ReadFile(path) // #nosec G304 -- a path this test just created
	if err != nil {
		t.Fatalf("reading the review: %v", err)
	}
	older := strings.Replace(string(current),
		`"completion_summary"`,
		`"status": "approved",
  "decided_at": "2026-08-04T10:02:00Z",
  "completion_summary"`, 1)
	if older == string(current) {
		t.Fatal("the stored document has no completion_summary to write the older fields beside")
	}
	if err := os.WriteFile(path, []byte(older), 0o600); err != nil {
		t.Fatalf("writing the older document: %v", err)
	}

	loaded, err := reviews.Load(ctx, ref)
	if err != nil {
		t.Fatalf("loading a review written before the decision moved: %v", err)
	}
	if loaded.CompletionSummary == "" || len(loaded.Checks) == 0 {
		t.Error("the older document lost what it does still carry")
	}

	// Saving it again is what removes the fields for good.
	if err := reviews.Save(ctx, ref, loaded); err != nil {
		t.Fatalf("saving the loaded review: %v", err)
	}
	rewritten, err := os.ReadFile(path) // #nosec G304 -- a path this test just created
	if err != nil {
		t.Fatalf("reading the rewritten review: %v", err)
	}
	// The top-level keys rather than the text: a check result carries a status of
	// its own, which is a different field and stays.
	var document map[string]any
	if err := json.Unmarshal(rewritten, &document); err != nil {
		t.Fatalf("decoding the rewritten review: %v", err)
	}
	for _, gone := range []string{"status", "decided_at"} {
		if _, present := document[gone]; present {
			t.Errorf("the rewritten review still carries %q, which nothing reads", gone)
		}
	}
	if _, err := os.Stat(path + ".v1.bak"); err == nil {
		t.Error("a backup was retained for a version that did not move")
	}
}

// TestBriefIsStoredAsMarkdown checks that a task brief is stored as the Markdown
// the user wrote, byte for byte, rather than escaped into the snapshot.
func TestBriefIsStoredAsMarkdown(t *testing.T) {
	ctx := context.Background()
	fixture := storetest.Task()
	filestore := newStore(t)

	if err := filestore.Tasks().Save(ctx, fixture); err != nil {
		t.Fatalf("saving the task: %v", err)
	}

	brief, err := os.ReadFile(taskPath(t, filestore, briefFile))
	if err != nil {
		t.Fatalf("reading the brief: %v", err)
	}
	if string(brief) != storetest.Brief {
		t.Errorf("the brief was rewritten.\n got: %q\nwant: %q", brief, storetest.Brief)
	}

	snapshot, err := os.ReadFile(taskPath(t, filestore, taskFile))
	if err != nil {
		t.Fatalf("reading the snapshot: %v", err)
	}
	if strings.Contains(string(snapshot), "Acceptance criteria") {
		t.Error("the brief is duplicated into the task snapshot, so the two can disagree")
	}
}

// TestMissingBriefIsReportedAsCorruptState checks that half a task is not
// quietly returned as a whole one.
func TestMissingBriefIsReportedAsCorruptState(t *testing.T) {
	ctx := context.Background()
	fixture := storetest.Task()
	filestore := newStore(t)

	if err := filestore.Tasks().Save(ctx, fixture); err != nil {
		t.Fatalf("saving the task: %v", err)
	}
	if err := os.Remove(taskPath(t, filestore, briefFile)); err != nil {
		t.Fatalf("removing the brief: %v", err)
	}

	_, err := filestore.Tasks().Load(ctx, store.Ref(fixture))
	if !errors.Is(err, store.ErrCorrupt) {
		t.Fatalf("want ErrCorrupt, got %v", err)
	}
	if !strings.Contains(err.Error(), briefFile) {
		t.Errorf("error does not name the missing file: %v", err)
	}
}

// TestMissingRecordsReportNotFound checks that an absent record is
// distinguishable from a failure, since the daemon has to create what is absent
// and stop for what failed.
func TestMissingRecordsReportNotFound(t *testing.T) {
	ctx := context.Background()
	filestore := newStore(t)
	ref := store.TaskRef{Project: storetest.ProjectID, Task: storetest.TaskID}

	if _, err := filestore.Projects().Load(ctx, storetest.ProjectID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("loading an unknown project: want ErrNotFound, got %v", err)
	}
	if _, err := filestore.Tasks().Load(ctx, ref); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("loading an unknown task: want ErrNotFound, got %v", err)
	}
	if _, err := filestore.Reviews().Load(ctx, ref); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("loading an unknown review: want ErrNotFound, got %v", err)
	}

	projects, err := filestore.Projects().List(ctx)
	if err != nil || len(projects) != 0 {
		t.Errorf("listing an empty store returned %d projects and %v", len(projects), err)
	}
	tasks, err := filestore.Tasks().List(ctx, storetest.ProjectID)
	if err != nil || len(tasks) != 0 {
		t.Errorf("listing a project with no tasks returned %d tasks and %v", len(tasks), err)
	}
}

// TestListingIgnoresEntriesThatAreNotRecords checks that the state directory
// tolerates the files an operating system and an interrupted write leave in it.
func TestListingIgnoresEntriesThatAreNotRecords(t *testing.T) {
	ctx := context.Background()
	filestore := newStore(t)
	fixture := storetest.Task()

	if err := filestore.Projects().Save(ctx, storetest.Project()); err != nil {
		t.Fatalf("saving the project: %v", err)
	}
	if err := filestore.Tasks().Save(ctx, fixture); err != nil {
		t.Fatalf("saving the task: %v", err)
	}

	projectsRoot := filepath.Join(filestore.Root(), projectsDir)
	write(t, filepath.Join(projectsRoot, ".DS_Store"), "")
	mkdir(t, filepath.Join(projectsRoot, "Not An ID"))
	mkdir(t, filepath.Join(projectsRoot, storetest.ProjectID.String(), tasksDir, "interrupted"))
	// A task directory an interrupted creation left without a snapshot.
	mkdir(t, filepath.Join(projectsRoot, storetest.ProjectID.String(), tasksDir, "3c1e5f80-1a2b-4c3d-8e9f-0a1b2c3d4e5f"))

	projects, err := filestore.Projects().List(ctx)
	if err != nil {
		t.Fatalf("listing projects: %v", err)
	}
	if len(projects) != 1 || projects[0].ID != storetest.ProjectID {
		t.Errorf("listing projects returned %d entries", len(projects))
	}

	tasks, err := filestore.Tasks().List(ctx, storetest.ProjectID)
	if err != nil {
		t.Fatalf("listing tasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != fixture.ID {
		t.Errorf("listing tasks returned %d entries", len(tasks))
	}
}

// TestUnsafeIdentifiersNeverReachTheFilesystem checks that storage validates
// identifiers itself rather than trusting a caller to have done it. A record
// name is a path segment, and a path segment that traverses a directory is how
// state outside the store gets read or replaced.
func TestUnsafeIdentifiersNeverReachTheFilesystem(t *testing.T) {
	ctx := context.Background()
	filestore := newStore(t)

	unsafe := []domain.ProjectID{"..", "../escape", "example/../../etc", "/etc", ""}
	for _, id := range unsafe {
		if _, err := filestore.Projects().Load(ctx, id); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("loading project %q: want a validation error, got %v", id, err)
		}
		if _, err := filestore.Tasks().List(ctx, id); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("listing tasks of project %q: want a validation error, got %v", id, err)
		}
	}

	unsafeTasks := []domain.TaskID{"..", "../../7f3a1c2e", "", "7f3a1c2e"}
	for _, id := range unsafeTasks {
		ref := store.TaskRef{Project: storetest.ProjectID, Task: id}
		if _, err := filestore.Tasks().Load(ctx, ref); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("loading task %q: want a validation error, got %v", id, err)
		}
		if _, err := filestore.Reviews().Load(ctx, ref); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("loading the review of task %q: want a validation error, got %v", id, err)
		}
		if _, err := filestore.Events().Replay(ctx, ref); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("replaying the events of task %q: want a validation error, got %v", id, err)
		}
	}
}

// TestStoredStateIsPrivateToTheUser checks the permissions on the state
// directory. It names repositories, paths, and task briefs.
func TestStoredStateIsPrivateToTheUser(t *testing.T) {
	ctx := context.Background()
	filestore := newStore(t)
	fixture := storetest.Task()

	if err := filestore.Tasks().Save(ctx, fixture); err != nil {
		t.Fatalf("saving the task: %v", err)
	}
	if _, err := filestore.Events().Append(ctx, store.Ref(fixture), storetest.Events()[0]); err != nil {
		t.Fatalf("appending an event: %v", err)
	}

	err := filepath.WalkDir(filestore.Root(), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == filestore.Root() {
			// The root is the directory the caller chose; the store creates
			// what is inside it.
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		want := filePerm
		if entry.IsDir() {
			want = dirPerm
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s has mode %v, want %v", path, got, want)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the state directory: %v", err)
	}
}

// TestSavesAreSerialized exercises the store the way the daemon uses it: several
// requests writing and reading at once. It is a race-detector test; what it
// asserts is that no read fails and no write corrupts another.
func TestSavesAreSerialized(t *testing.T) {
	ctx := context.Background()
	filestore := newStore(t)

	tasks := make([]*domain.Task, 4)
	for i := range tasks {
		task := storetest.Task()
		task.ID = domain.NewTaskID()
		tasks[i] = task
		if err := filestore.Tasks().Save(ctx, task); err != nil {
			t.Fatalf("saving a task: %v", err)
		}
	}

	var group sync.WaitGroup
	for _, task := range tasks {
		group.Add(2)
		go func() {
			defer group.Done()
			for range 20 {
				if err := filestore.Tasks().Save(ctx, task); err != nil {
					t.Errorf("concurrent save: %v", err)
					return
				}
			}
		}()
		go func() {
			defer group.Done()
			for range 20 {
				loaded, err := filestore.Tasks().Load(ctx, store.Ref(task))
				if err != nil {
					t.Errorf("concurrent load: %v", err)
					return
				}
				if loaded.ID != task.ID {
					t.Errorf("concurrent load returned task %s", loaded.ID)
					return
				}
			}
		}()
	}
	group.Wait()

	listed, err := filestore.Tasks().List(ctx, storetest.ProjectID)
	if err != nil {
		t.Fatalf("listing tasks: %v", err)
	}
	if len(listed) != len(tasks) {
		t.Errorf("the project holds %d tasks, want %d", len(listed), len(tasks))
	}
}

// TestSaveRejectsInvalidAggregates checks that storage never records a task the
// domain would not have produced.
func TestSaveRejectsInvalidAggregates(t *testing.T) {
	ctx := context.Background()
	filestore := newStore(t)

	task := storetest.Task()
	task.Session = nil
	if err := filestore.Tasks().Save(ctx, task); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("saving a working task without a session: want a validation error, got %v", err)
	}

	project := storetest.Project()
	project.PrimaryRepository = "missing"
	if err := filestore.Projects().Save(ctx, project); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("saving a project without its primary repository: want a validation error, got %v", err)
	}

	review := storetest.Review()
	other := store.TaskRef{Project: storetest.ProjectID, Task: domain.NewTaskID()}
	if err := filestore.Reviews().Save(ctx, other, review); err == nil {
		t.Error("a review was stored under a task it does not belong to")
	}
}

// TestCancelledContextIsHonoured checks that a cancelled request does not reach
// the filesystem.
func TestCancelledContextIsHonoured(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	filestore := newStore(t)
	if err := filestore.Tasks().Save(ctx, storetest.Task()); !errors.Is(err, context.Canceled) {
		t.Errorf("saving with a cancelled context: got %v", err)
	}
	if _, err := os.Stat(filepath.Join(filestore.Root(), projectsDir)); !errors.Is(err, os.ErrNotExist) {
		t.Error("a cancelled save created state")
	}
}

// TestOpenRequiresAnAbsoluteRoot checks that the store is anchored somewhere
// explicit, rather than wherever the process happens to be running.
func TestOpenRequiresAnAbsoluteRoot(t *testing.T) {
	if _, err := Open("state"); err == nil {
		t.Error("a relative state directory was accepted")
	}
}

func newStore(t *testing.T) *Store {
	t.Helper()

	filestore, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("opening a store: %v", err)
	}
	return filestore
}

// taskPath returns the path of one file of the fixture task.
func taskPath(t *testing.T, filestore *Store, name string) string {
	t.Helper()

	return filepath.Join(filestore.Root(), projectsDir, storetest.ProjectID.String(),
		tasksDir, storetest.TaskID.String(), name)
}

func write(t *testing.T, path, content string) {
	t.Helper()

	mkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), filePerm); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

func mkdir(t *testing.T, path string) {
	t.Helper()

	if err := os.MkdirAll(path, dirPerm); err != nil {
		t.Fatalf("creating %s: %v", path, err)
	}
}
