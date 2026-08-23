package fs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ma8el/feat/internal/store"
	"github.com/ma8el/feat/internal/store/storetest"
)

var errCrash = errors.New("simulated crash")

// TestACrashNeverLeavesAPartiallyReplacedSnapshot checks the atomicity rule at
// every point of a replacement.
//
// The store is interrupted while replacing an existing snapshot with a different
// one. Before the rename the previous snapshot must still be the current one and
// must still be complete; after the rename the new one must be, because that is
// the moment the replacement happens.
func TestACrashNeverLeavesAPartiallyReplacedSnapshot(t *testing.T) {
	tests := map[string]struct {
		point    string
		replaced bool
	}{
		"while the temporary file is being created": {point: pointTempCreated},
		"while the new snapshot is being written":   {point: pointTempWritten},
		"after the new snapshot is flushed":         {point: pointTempSynced},
		"after the rename":                          {point: pointRenamed, replaced: true},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			filestore := newStore(t)
			projects := filestore.Projects()

			before := storetest.Project()
			if err := projects.Save(ctx, before); err != nil {
				t.Fatalf("saving the first snapshot: %v", err)
			}

			// A snapshot that differs in size as well as in content, so that a
			// replacement that wrote in place would leave a mixture of the two.
			after := storetest.Project()
			after.Name = "Renamed"
			after.Repositories = after.Repositories[:1]

			filestore.interrupt = func(point, _ string) error {
				if point == test.point {
					return errCrash
				}
				return nil
			}
			if err := projects.Save(ctx, after); !errors.Is(err, errCrash) {
				t.Fatalf("the interrupted save returned %v", err)
			}
			filestore.interrupt = nil

			// A crash runs no cleanup, so recovery starts from whatever the
			// directory holds. Reopening makes that explicit.
			reopened, err := Open(filestore.Root())
			if err != nil {
				t.Fatalf("reopening the store: %v", err)
			}
			loaded, err := reopened.Projects().Load(ctx, before.ID)
			if err != nil {
				t.Fatalf("loading after the crash: %v", err)
			}

			want := before
			if test.replaced {
				want = after
			}
			if loaded.Name != want.Name || len(loaded.Repositories) != len(want.Repositories) {
				t.Errorf("after a crash %s the current snapshot is %q with %d repositories, want %q with %d",
					name, loaded.Name, len(loaded.Repositories), want.Name, len(want.Repositories))
			}

			listed, err := reopened.Projects().List(ctx)
			if err != nil {
				t.Fatalf("listing after the crash: %v", err)
			}
			if len(listed) != 1 {
				t.Errorf("listing after a crash returned %d projects", len(listed))
			}
		})
	}
}

// TestACrashWhileWritingATaskKeepsThePreviousTask checks the same property for
// the task snapshot, which is written next to its brief.
func TestACrashWhileWritingATaskKeepsThePreviousTask(t *testing.T) {
	ctx := context.Background()
	filestore := newStore(t)
	fixture := storetest.Task()

	if err := filestore.Tasks().Save(ctx, fixture); err != nil {
		t.Fatalf("saving the task: %v", err)
	}

	observed := storetest.Task()
	if err := observed.SetAttention("needs_input", storetest.Origin); err != nil {
		t.Fatalf("changing the task: %v", err)
	}

	filestore.interrupt = func(point, path string) error {
		if point == pointTempWritten && filepath.Base(path) == taskFile {
			return errCrash
		}
		return nil
	}
	if err := filestore.Tasks().Save(ctx, observed); !errors.Is(err, errCrash) {
		t.Fatalf("the interrupted save returned %v", err)
	}
	filestore.interrupt = nil

	loaded, err := filestore.Tasks().Load(ctx, store.Ref(fixture))
	if err != nil {
		t.Fatalf("loading after the crash: %v", err)
	}
	if loaded.Attention != fixture.Attention {
		t.Errorf("the task came back as %s, want the previous %s", loaded.Attention, fixture.Attention)
	}
}

// TestLeftoverTemporaryFilesAreInert checks that what a crash leaves behind is
// ignored rather than mistaken for state. A temporary file holding half a
// document must never be loaded, listed, or preferred over the snapshot.
func TestLeftoverTemporaryFilesAreInert(t *testing.T) {
	ctx := context.Background()
	filestore := newStore(t)
	fixture := storetest.Project()

	if err := filestore.Projects().Save(ctx, fixture); err != nil {
		t.Fatalf("saving the project: %v", err)
	}

	dir := filepath.Join(filestore.Root(), projectsDir, fixture.ID.String())
	write(t, filepath.Join(dir, "."+projectFile+".tmp-1234"), `{"schema_version":1,"id":"exam`)
	write(t, filepath.Join(dir, projectFile+".v0.bak"), `{"schema_version":1,"id":"other"}`)

	loaded, err := filestore.Projects().Load(ctx, fixture.ID)
	if err != nil {
		t.Fatalf("loading with leftovers present: %v", err)
	}
	if loaded.Name != fixture.Name {
		t.Errorf("the leftover file was loaded: %q", loaded.Name)
	}

	listed, err := filestore.Projects().List(ctx)
	if err != nil || len(listed) != 1 {
		t.Errorf("listing returned %d projects and %v", len(listed), err)
	}

	// A later successful save still replaces the real snapshot.
	fixture.Name = "Renamed"
	if err := filestore.Projects().Save(ctx, fixture); err != nil {
		t.Fatalf("saving over the leftovers: %v", err)
	}
	loaded, err = filestore.Projects().Load(ctx, fixture.ID)
	if err != nil {
		t.Fatalf("loading after the save: %v", err)
	}
	if loaded.Name != "Renamed" {
		t.Errorf("the snapshot was not replaced: %q", loaded.Name)
	}
}

// TestReadersNeverObserveAHalfWrittenSnapshot checks the same property from the
// outside, with readers running while a writer replaces a snapshot repeatedly. A
// write that truncated the target in place would be caught here.
func TestReadersNeverObserveAHalfWrittenSnapshot(t *testing.T) {
	ctx := context.Background()
	filestore := newStore(t)
	fixture := storetest.Task()

	if err := filestore.Tasks().Save(ctx, fixture); err != nil {
		t.Fatalf("saving the task: %v", err)
	}

	var group sync.WaitGroup
	group.Add(2)
	go func() {
		defer group.Done()
		for range 50 {
			if err := filestore.Tasks().Save(ctx, fixture); err != nil {
				t.Errorf("saving: %v", err)
				return
			}
		}
	}()
	go func() {
		defer group.Done()
		for range 50 {
			loaded, err := filestore.Tasks().Load(ctx, store.Ref(fixture))
			if err != nil {
				t.Errorf("loading during a save: %v", err)
				return
			}
			if len(loaded.Repositories) != len(fixture.Repositories) {
				t.Errorf("a load observed %d repositories", len(loaded.Repositories))
				return
			}
		}
	}()
	group.Wait()
}

// TestTemporaryFilesAreRemovedAfterAnOrdinaryFailure checks that a write that
// fails for a reason the process survives cleans up after itself. Only a crash
// leaves a temporary file behind.
func TestTemporaryFilesAreRemovedAfterAnOrdinaryFailure(t *testing.T) {
	filestore := newStore(t)
	dir := filepath.Join(filestore.Root(), projectsDir)
	path := filepath.Join(dir, projectFile)

	if err := os.MkdirAll(dir, dirPerm); err != nil {
		t.Fatalf("creating %s: %v", dir, err)
	}
	// A directory where the snapshot should be makes the rename fail without
	// ending the process.
	if err := os.MkdirAll(path, dirPerm); err != nil {
		t.Fatalf("creating %s: %v", path, err)
	}

	if err := filestore.replaceFile(path, []byte("{}\n")); err == nil {
		t.Fatal("replacing a directory with a file succeeded")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp-") {
			t.Errorf("%s was left behind after an ordinary failure", entry.Name())
		}
	}
}
