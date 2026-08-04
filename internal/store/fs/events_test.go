package fs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/store"
	"github.com/ma8el/feat/internal/store/storetest"
)

// TestEventsReplayInTheOrderTheyWereAppended checks that the log assigns
// sequences, keeps them in order, and returns every event unchanged.
func TestEventsReplayInTheOrderTheyWereAppended(t *testing.T) {
	ctx := context.Background()
	filestore := newStore(t)
	ref := fixtureRef()

	fixtures := storetest.Events()
	appended := make([]domain.Event, 0, len(fixtures))
	for i, event := range fixtures {
		stored, err := filestore.Events().Append(ctx, ref, event)
		if err != nil {
			t.Fatalf("appending event %d: %v", i, err)
		}
		if stored.Sequence != uint64(i+1) {
			t.Errorf("event %d was given sequence %d", i, stored.Sequence)
		}
		appended = append(appended, stored)
	}

	log, err := filestore.Events().Replay(ctx, ref)
	if err != nil {
		t.Fatalf("replaying: %v", err)
	}
	if log.IncompleteFinalRecord {
		t.Error("a complete log reports an incomplete final record")
	}
	if !reflect.DeepEqual(appended, log.Events) {
		t.Errorf("the history changed on the way through storage.\n got: %#v\nwant: %#v", log.Events, appended)
	}
	if last, ok := log.Last(); !ok || last.Sequence != uint64(len(fixtures)) {
		t.Errorf("the last event is %#v", last)
	}

	// The fixtures cover every field of an event between them, so a field the
	// log drops is a field that comes back empty everywhere.
	if unpopulated := storetest.UnpopulatedFields(log.Events); len(unpopulated) > 0 {
		t.Errorf("the replayed history leaves %v unset", unpopulated)
	}
}

// TestSequencesContinueAfterARestart checks that a daemon which restarts does
// not begin numbering a task's history again, which would make two different
// events share a sequence.
func TestSequencesContinueAfterARestart(t *testing.T) {
	ctx := context.Background()
	filestore := newStore(t)
	ref := fixtureRef()

	for _, event := range storetest.Events()[:3] {
		if _, err := filestore.Events().Append(ctx, ref, event); err != nil {
			t.Fatalf("appending: %v", err)
		}
	}

	restarted, err := Open(filestore.Root())
	if err != nil {
		t.Fatalf("reopening the store: %v", err)
	}
	stored, err := restarted.Events().Append(ctx, ref, storetest.Events()[3])
	if err != nil {
		t.Fatalf("appending after a restart: %v", err)
	}
	if stored.Sequence != 4 {
		t.Errorf("the first event after a restart was given sequence %d", stored.Sequence)
	}
}

// TestReplayIgnoresOnlyAnIncompleteFinalRecord checks the slice 1 acceptance
// criterion.
//
// A record is complete when it is terminated by a newline. An unterminated final
// record is what a crash during an append leaves behind. Anything else that
// cannot be read is corruption, and quietly dropping it would rewrite a history
// that later reconciliation depends on.
func TestReplayIgnoresOnlyAnIncompleteFinalRecord(t *testing.T) {
	complete := func(sequence int) string {
		return `{"schema_version":1,"sequence":` + strconv.Itoa(sequence) +
			`,"occurred_at":"2026-08-04T09:34:00Z","project_id":"example",` +
			`"task_id":"` + storetest.TaskID.String() + `","type":"task_workflow_changed"}`
	}

	tests := map[string]struct {
		content    string
		events     int
		incomplete bool
		corrupt    string
	}{
		"a complete log": {
			content: complete(1) + "\n" + complete(2) + "\n",
			events:  2,
		},
		"a crash part way through the final record": {
			content:    complete(1) + "\n" + complete(2) + "\n" + `{"schema_version":1,"sequ`,
			events:     2,
			incomplete: true,
		},
		"a final record that is valid but unterminated": {
			content:    complete(1) + "\n" + complete(2),
			events:     1,
			incomplete: true,
		},
		"a malformed record in the middle": {
			content: complete(1) + "\n" + `{"schema_version":1,"sequ` + "\n" + complete(3) + "\n",
			corrupt: ":2",
		},
		"a malformed final record that is terminated": {
			content: complete(1) + "\n" + `{"schema_version":1,"sequ` + "\n",
			corrupt: ":2",
		},
		"a record without a schema version": {
			content: complete(1) + "\n" + `{"sequence":2}` + "\n",
			corrupt: "schema_version",
		},
		"a record that is not an event": {
			content: complete(1) + "\n" + `{"schema_version":1,"sequence":2}` + "\n",
			corrupt: ":2",
		},
		"a record out of order": {
			content: complete(2) + "\n" + complete(1) + "\n",
			corrupt: "not in order",
		},
		"a blank record": {
			content: complete(1) + "\n\n" + complete(2) + "\n",
			corrupt: ":2",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			filestore := newStore(t)
			write(t, eventsPath(filestore), test.content)

			log, err := filestore.Events().Replay(ctx, fixtureRef())
			if test.corrupt != "" {
				if !errors.Is(err, store.ErrCorrupt) {
					t.Fatalf("want ErrCorrupt, got %v", err)
				}
				if !strings.Contains(err.Error(), test.corrupt) {
					t.Errorf("error does not point at the record (%q): %v", test.corrupt, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("replaying: %v", err)
			}
			if len(log.Events) != test.events {
				t.Errorf("replayed %d events, want %d", len(log.Events), test.events)
			}
			if log.IncompleteFinalRecord != test.incomplete {
				t.Errorf("IncompleteFinalRecord is %t, want %t", log.IncompleteFinalRecord, test.incomplete)
			}
		})
	}
}

// TestAppendingAfterACrashDiscardsTheIncompleteRecord checks that an interrupted
// append costs the record it was writing and nothing else. Appending after an
// unterminated record would otherwise join the two into one unreadable line.
func TestAppendingAfterACrashDiscardsTheIncompleteRecord(t *testing.T) {
	ctx := context.Background()
	filestore := newStore(t)
	ref := fixtureRef()

	fixtures := storetest.Events()
	for _, event := range fixtures[:2] {
		if _, err := filestore.Events().Append(ctx, ref, event); err != nil {
			t.Fatalf("appending: %v", err)
		}
	}

	path := eventsPath(filestore)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the log: %v", err)
	}
	if err := os.WriteFile(path, append(raw, []byte(`{"schema_version":1,"sequ`)...), filePerm); err != nil {
		t.Fatalf("writing a partial record: %v", err)
	}

	restarted, err := Open(filestore.Root())
	if err != nil {
		t.Fatalf("reopening the store: %v", err)
	}
	stored, err := restarted.Events().Append(ctx, ref, fixtures[2])
	if err != nil {
		t.Fatalf("appending after the partial record: %v", err)
	}
	if stored.Sequence != 3 {
		t.Errorf("the event after the partial record was given sequence %d", stored.Sequence)
	}

	log, err := restarted.Events().Replay(ctx, ref)
	if err != nil {
		t.Fatalf("replaying after the repair: %v", err)
	}
	if log.IncompleteFinalRecord {
		t.Error("the repaired log still reports an incomplete final record")
	}
	if len(log.Events) != 3 {
		t.Fatalf("the repaired log holds %d events, want 3", len(log.Events))
	}
	if log.Events[2].Type != fixtures[2].Type {
		t.Errorf("the appended event came back as %s", log.Events[2].Type)
	}
}

// TestConcurrentAppendsProduceOneOrderedHistory checks the ordering guarantee
// under the concurrency the daemon actually has: several requests recording
// events for one task at the same time.
func TestConcurrentAppendsProduceOneOrderedHistory(t *testing.T) {
	ctx := context.Background()
	filestore := newStore(t)
	ref := fixtureRef()

	const writers = 4
	each := len(storetest.Events())

	var group sync.WaitGroup
	for range writers {
		group.Add(1)
		go func() {
			defer group.Done()
			for _, event := range storetest.Events() {
				if _, err := filestore.Events().Append(ctx, ref, event); err != nil {
					t.Errorf("appending: %v", err)
					return
				}
			}
		}()
	}
	group.Wait()

	log, err := filestore.Events().Replay(ctx, ref)
	if err != nil {
		t.Fatalf("replaying: %v", err)
	}
	if len(log.Events) != writers*each {
		t.Fatalf("the history holds %d events, want %d", len(log.Events), writers*each)
	}
	for i, event := range log.Events {
		if event.Sequence != uint64(i+1) {
			t.Fatalf("event %d has sequence %d, so the history has a gap or a repeat", i, event.Sequence)
		}
	}
}

// TestEventsBelongToTheirTask checks that an event cannot be filed under a task
// it does not describe, which would attribute one task's history to another.
func TestEventsBelongToTheirTask(t *testing.T) {
	ctx := context.Background()
	filestore := newStore(t)
	other := store.TaskRef{Project: storetest.ProjectID, Task: domain.NewTaskID()}

	if _, err := filestore.Events().Append(ctx, other, storetest.Events()[0]); err == nil {
		t.Error("an event was appended to another task's history")
	}

	broken := storetest.Events()[0]
	broken.Type = "everything_is_fine"
	if _, err := filestore.Events().Append(ctx, fixtureRef(), broken); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("an undocumented event type was appended: %v", err)
	}

	undated := storetest.Events()[0]
	undated.OccurredAt = time.Time{}
	if _, err := filestore.Events().Append(ctx, fixtureRef(), undated); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("an event without a time was appended: %v", err)
	}
}

// TestReplayingATaskWithNoHistoryIsEmpty checks that a task which has recorded
// nothing yet is not a missing task.
func TestReplayingATaskWithNoHistoryIsEmpty(t *testing.T) {
	log, err := newStore(t).Events().Replay(context.Background(), fixtureRef())
	if err != nil {
		t.Fatalf("replaying an empty history: %v", err)
	}
	if len(log.Events) != 0 || log.IncompleteFinalRecord {
		t.Errorf("an empty history replayed as %#v", log)
	}
	if _, ok := log.Last(); ok {
		t.Error("an empty history has a last event")
	}
}

func fixtureRef() store.TaskRef {
	return store.TaskRef{Project: storetest.ProjectID, Task: storetest.TaskID}
}

func eventsPath(filestore *Store) string {
	return filepath.Join(filestore.Root(), projectsDir, storetest.ProjectID.String(),
		tasksDir, storetest.TaskID.String(), eventsFile)
}
