package store_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/store"
)

// TestErrorsExplainWhatTheUserCanDo checks the messages an implementation
// produces. Storage errors are read by someone whose state directory is in an
// unexpected condition, so each has to name the record, the file, and the
// difference between "absent", "unreadable", and "written by a newer build".
func TestErrorsExplainWhatTheUserCanDo(t *testing.T) {
	tests := map[string]struct {
		err     error
		class   error
		mention []string
	}{
		"a missing record": {
			err:     &store.NotFoundError{Kind: "task", ID: "example/7f3a1c2e"},
			class:   store.ErrNotFound,
			mention: []string{"task", "example/7f3a1c2e", "does not exist"},
		},
		"an unreadable document": {
			err:     &store.CorruptError{Kind: "task", ID: "example/7f3a1c2e", Path: "/srv/state/task.json", Err: errors.New("unexpected end of JSON input")},
			class:   store.ErrCorrupt,
			mention: []string{"task", "/srv/state/task.json", "unexpected end of JSON input"},
		},
		"an unreadable record in a log": {
			err:     &store.CorruptError{Kind: "event", ID: "example/7f3a1c2e", Path: "/srv/state/events.jsonl", Record: 12, Err: errors.New("invalid character")},
			class:   store.ErrCorrupt,
			mention: []string{"/srv/state/events.jsonl:12"},
		},
		"a document from a newer build": {
			err:     &store.SchemaError{Kind: "task", Path: "/srv/state/task.json", Found: 4, Supported: 1},
			class:   store.ErrUnsupportedSchema,
			mention: []string{"/srv/state/task.json", "schema version 4", "newer version"},
		},
		"a document this build cannot migrate": {
			err:     &store.SchemaError{Kind: "task", Path: "/srv/state/task.json", Found: 1, Supported: 4},
			class:   store.ErrUnsupportedSchema,
			mention: []string{"cannot migrate"},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if !errors.Is(test.err, test.class) {
				t.Errorf("%v does not match its error class", test.err)
			}
			for _, want := range test.mention {
				if !strings.Contains(test.err.Error(), want) {
					t.Errorf("message does not mention %q: %v", want, test.err)
				}
			}
		})
	}
}

// TestCorruptErrorKeepsTheUnderlyingFailure checks that the decoding failure
// stays reachable, so a caller can distinguish the kinds of corruption without
// reading the message.
func TestCorruptErrorKeepsTheUnderlyingFailure(t *testing.T) {
	underlying := errors.New("unexpected end of JSON input")
	err := error(&store.CorruptError{Kind: "task", ID: "example/7f3a1c2e", Path: "/srv/state/task.json", Err: underlying})

	if !errors.Is(err, underlying) {
		t.Error("the underlying failure is not reachable")
	}
	if !errors.Is(err, store.ErrCorrupt) {
		t.Error("the error class is not reachable")
	}
}

// TestTaskRefValidatesBothIdentifiers checks the reference an implementation
// turns into a path.
func TestTaskRefValidatesBothIdentifiers(t *testing.T) {
	valid := store.TaskRef{Project: "example", Task: "7f3a1c2e-5b6d-4a80-9c1f-2d3e4f5a6b7c"}
	if err := valid.Validate(); err != nil {
		t.Errorf("a valid reference is rejected: %v", err)
	}
	if got := valid.String(); got != "example/7f3a1c2e-5b6d-4a80-9c1f-2d3e4f5a6b7c" {
		t.Errorf("reference renders as %q", got)
	}

	unsafe := []store.TaskRef{
		{Project: "..", Task: valid.Task},
		{Project: valid.Project, Task: "../escape"},
		{Project: "", Task: valid.Task},
		{Project: valid.Project, Task: ""},
	}
	for _, ref := range unsafe {
		if err := ref.Validate(); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%q/%q validates", ref.Project, ref.Task)
		}
	}
}

// TestRefAddressesATask checks the helper that derives a reference from a task,
// which is what keeps a task's own project from being second-guessed.
func TestRefAddressesATask(t *testing.T) {
	task, err := domain.NewTask("7f3a1c2e-5b6d-4a80-9c1f-2d3e4f5a6b7c", "example", "Title",
		domain.TaskSource{Kind: domain.SourcePrompt}, time.Date(2026, time.August, 4, 9, 30, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("creating a task: %v", err)
	}

	ref := store.Ref(task)
	if ref.Project != task.ProjectID || ref.Task != task.ID {
		t.Errorf("the reference addresses %s", ref)
	}
}

// TestEventLogLast checks the accessor a caller uses to continue a history.
func TestEventLogLast(t *testing.T) {
	var empty store.EventLog
	if _, ok := empty.Last(); ok {
		t.Error("an empty log reports a last event")
	}

	log := store.EventLog{Events: []domain.Event{{Sequence: 1}, {Sequence: 2}}}
	last, ok := log.Last()
	if !ok || last.Sequence != 2 {
		t.Errorf("the last event is %#v", last)
	}
}
