package control_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ma8el/feat/internal/control"
	"github.com/ma8el/feat/internal/domain"
)

const (
	testProject = "app"
	testTask    = "3f2504e0-4f89-41d3-9a0c-0305e82c3301"
	otherTask   = "3f2504e0-4f89-41d3-9a0c-0305e82c3302"
)

// clock is a test clock, so that the parse grace period is decided by the test
// rather than by how long the test took to run.
type clock struct{ at time.Time }

func (c *clock) now() time.Time { return c.at }

func (c *clock) advance(d time.Duration) { c.at = c.at.Add(d) }

func newWorkspace(t *testing.T) (*control.Workspace, *clock) {
	t.Helper()

	moment := &clock{at: time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)}
	workspace, err := control.Open(t.TempDir(), testProject, testTask, control.Options{
		Now:        moment.now,
		ParseGrace: 3 * time.Second,
	})
	if err != nil {
		t.Fatalf("opening a workspace: %v", err)
	}
	if err := workspace.Create(); err != nil {
		t.Fatalf("creating the workspace: %v", err)
	}
	return workspace, moment
}

// write puts a document in the outbox under the given name.
func write(t *testing.T, workspace *control.Workspace, name, body string) {
	t.Helper()

	path := filepath.Join(workspace.OutboxDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}

// envelope renders a well-formed message with the given fields replaced.
func envelope(id, task string, kind control.MessageType) string {
	document := map[string]any{
		"schema_version": control.SchemaVersion,
		"id":             id,
		"task_id":        task,
		"type":           string(kind),
		"occurred_at":    "2026-08-06T12:00:00Z",
		"payload":        map[string]any{"summary": "done"},
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func TestOpenResolvesWithoutCreatingAnything(t *testing.T) {
	root := t.TempDir()
	workspace, err := control.Open(root, testProject, testTask, control.Options{})
	if err != nil {
		t.Fatalf("opening a workspace: %v", err)
	}

	want := filepath.Join(root, testProject, testTask)
	if workspace.Root() != want {
		t.Errorf("workspace root = %q, want %q", workspace.Root(), want)
	}
	if workspace.Exists() {
		t.Error("opening a workspace created it; resolving where a workspace belongs must create nothing")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("reading the control root: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("opening a workspace left %d entries under the control root", len(entries))
	}
}

func TestOpenRefusesIdentifiersThatCouldEscapeTheirDirectory(t *testing.T) {
	for _, id := range []string{"../escape", "a/b", ".", ""} {
		if _, err := control.Open(t.TempDir(), domain.ProjectID(id), testTask, control.Options{}); err == nil {
			t.Errorf("project %q was accepted; an identifier reaching a path must be validated first", id)
		}
	}
	for _, id := range []string{"../escape", "not-a-uuid", ""} {
		if _, err := control.Open(t.TempDir(), testProject, domain.TaskID(id), control.Options{}); err == nil {
			t.Errorf("task %q was accepted; an identifier reaching a path must be validated first", id)
		}
	}
}

func TestCreateIsIdempotent(t *testing.T) {
	workspace, _ := newWorkspace(t)

	// A launch that failed part way through is retried rather than cleaned up
	// first, so creating an existing workspace must not fail.
	if err := workspace.Create(); err != nil {
		t.Fatalf("creating an existing workspace: %v", err)
	}
	for _, dir := range []string{
		workspace.ContextDir(), workspace.InboxDir(), workspace.OutboxDir(),
		workspace.ReportsDir(), workspace.AgentDir(),
	} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Errorf("stat %s: %v", dir, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%s is not a directory", dir)
		}
	}
}

func TestBriefIsWrittenVerbatim(t *testing.T) {
	workspace, _ := newWorkspace(t)

	// The brief is what the agent receives, so it is stored byte for byte:
	// trailing whitespace, CRLF, and a missing final newline all survive.
	brief := "# Title\r\n\r\nDo the thing.   \n\tindented\nno trailing newline"
	if err := workspace.WriteBrief(brief); err != nil {
		t.Fatalf("writing the brief: %v", err)
	}

	stored, err := os.ReadFile(workspace.BriefPath())
	if err != nil {
		t.Fatalf("reading the brief: %v", err)
	}
	if string(stored) != brief {
		t.Errorf("stored brief = %q, want %q", stored, brief)
	}
}

func TestAgentFilesCannotLeaveTheHostOnlyDirectory(t *testing.T) {
	workspace, _ := newWorkspace(t)

	for _, name := range []string{"../outbox/injected.json", "/etc/passwd", "hooks/../../escape", "", "a//b"} {
		if _, err := workspace.AgentPath(name); err == nil {
			t.Errorf("agent path %q was accepted; a generated name must stay inside agent/", name)
		}
	}

	if err := workspace.WriteAgentFile("hooks/stop.sh", []byte("#!/bin/sh\n"), true); err != nil {
		t.Fatalf("writing a generated hook: %v", err)
	}
	info, err := os.Stat(filepath.Join(workspace.AgentDir(), "hooks", "stop.sh"))
	if err != nil {
		t.Fatalf("stat the generated hook: %v", err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Errorf("generated hook mode = %v, want the owner execute bit set", info.Mode().Perm())
	}
}

func TestPendingReturnsMessagesOldestFirst(t *testing.T) {
	workspace, _ := newWorkspace(t)

	// Names are deliberately not in chronological order, so that a reader
	// sorting by name alone would fail this.
	for i, name := range []string{"c.json", "a.json", "b.json"} {
		write(t, workspace, name, envelope("event-"+name, testTask, control.TypeProviderEvent))
		when := time.Date(2026, 8, 6, 12, 0, i, 0, time.UTC)
		if err := os.Chtimes(filepath.Join(workspace.OutboxDir(), name), when, when); err != nil {
			t.Fatalf("setting the modification time of %s: %v", name, err)
		}
	}

	messages, rejected, err := workspace.Pending()
	if err != nil {
		t.Fatalf("reading pending messages: %v", err)
	}
	if len(rejected) != 0 {
		t.Fatalf("well-formed messages were rejected: %v", rejected)
	}

	var order []string
	for _, message := range messages {
		order = append(order, message.File())
	}
	want := []string{"c.json", "a.json", "b.json"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Errorf("message order = %v, want %v", order, want)
	}
}

// TestDuplicateMalformedAndOutOfTaskMessagesDoNotExecute is the protocol half of
// the slice 7 acceptance criterion. The daemon half, which proves no transition
// follows, lives in internal/daemon.
func TestDuplicateMalformedAndOutOfTaskMessagesDoNotExecute(t *testing.T) {
	oversize := func() string {
		document := map[string]any{
			"schema_version": control.SchemaVersion,
			"id":             "oversize",
			"task_id":        testTask,
			"type":           string(control.TypeProviderEvent),
			"occurred_at":    "2026-08-06T12:00:00Z",
			"payload":        map[string]any{"text": strings.Repeat("x", control.MaxMessageBytes)},
		}
		encoded, err := json.Marshal(document)
		if err != nil {
			t.Fatalf("building an oversize message: %v", err)
		}
		return string(encoded)
	}()

	for _, test := range []struct {
		name string
		file string
		body string
		want string
	}{
		{
			name: "an unknown schema version",
			file: "version.json",
			body: `{"schema_version":99,"id":"a","task_id":"` + testTask +
				`","type":"provider_event","occurred_at":"2026-08-06T12:00:00Z"}`,
			want: "schema version 99",
		},
		{
			name: "a message naming another task",
			file: "foreign.json",
			body: envelope("b", otherTask, control.TypeProviderEvent),
			want: "names task",
		},
		{
			name: "an unknown message type",
			file: "type.json",
			body: envelope("c", testTask, "delete_everything"),
			want: "message type",
		},
		{
			name: "a message with no event id",
			file: "anonymous.json",
			body: envelope("", testTask, control.TypeProviderEvent),
			want: "no event id",
		},
		{
			name: "an event id that is not a plain name",
			file: "traversal-id.json",
			body: envelope("../../escape", testTask, control.TypeProviderEvent),
			want: "not a plain name",
		},
		{
			name: "a message with no occurrence time",
			file: "timeless.json",
			body: `{"schema_version":1,"id":"d","task_id":"` + testTask + `","type":"provider_event"}`,
			want: "no occurrence time",
		},
		{
			name: "a message larger than the limit",
			file: "oversize.json",
			body: oversize,
			want: "limit is",
		},
		{
			name: "a runtime request, which no agent is granted",
			file: "runtime.json",
			body: envelope("e", testTask, control.TypeRuntimeRequested),
			want: "inert until the host validates it",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			workspace, moment := newWorkspace(t)
			write(t, workspace, test.file, test.body)
			// Past the grace period, so that nothing is held back as a write in
			// progress.
			moment.advance(time.Minute)

			messages, rejected, err := workspace.Pending()
			if err != nil {
				t.Fatalf("reading pending messages: %v", err)
			}
			if len(messages) != 0 {
				t.Errorf("%d messages were returned; a refused message must never be applied", len(messages))
			}
			if len(rejected) != 1 {
				t.Fatalf("rejections = %d, want 1: %v", len(rejected), rejected)
			}
			if !strings.Contains(rejected[0].Error(), test.want) {
				t.Errorf("rejection = %q, want it to mention %q", rejected[0], test.want)
			}
			if !strings.Contains(rejected[0].Error(), test.file) {
				t.Errorf("rejection = %q, want it to name the file %q", rejected[0], test.file)
			}
		})
	}

	t.Run("a duplicate identifier is applied once", func(t *testing.T) {
		workspace, moment := newWorkspace(t)
		write(t, workspace, "first.json", envelope("same-id", testTask, control.TypeProviderEvent))

		messages, rejected, err := workspace.Pending()
		if err != nil {
			t.Fatalf("reading pending messages: %v", err)
		}
		if len(messages) != 1 || len(rejected) != 0 {
			t.Fatalf("first read returned %d messages and %d rejections, want 1 and 0", len(messages), len(rejected))
		}
		if err := workspace.MarkProcessed(messages[0], control.OutcomeApplied, ""); err != nil {
			t.Fatalf("marking processed: %v", err)
		}

		// The same event arriving again under a different name, which is what a
		// replayed or re-delivered message looks like.
		write(t, workspace, "second.json", envelope("same-id", testTask, control.TypeProviderEvent))
		moment.advance(time.Minute)

		messages, rejected, err = workspace.Pending()
		if err != nil {
			t.Fatalf("reading pending messages: %v", err)
		}
		if len(messages) != 0 {
			t.Errorf("a replayed identifier was returned again; the identifier exists to prevent exactly that")
		}
		if len(rejected) != 0 {
			t.Errorf("a replayed identifier was reported as a problem: %v", rejected)
		}
	})

	t.Run("a symbolic link is not read as a message", func(t *testing.T) {
		workspace, moment := newWorkspace(t)
		target := filepath.Join(t.TempDir(), "elsewhere.json")
		if err := os.WriteFile(target, []byte(envelope("f", testTask, control.TypeProviderEvent)), 0o600); err != nil {
			t.Fatalf("writing the link target: %v", err)
		}
		if err := os.Symlink(target, filepath.Join(workspace.OutboxDir(), "link.json")); err != nil {
			t.Fatalf("creating the symbolic link: %v", err)
		}
		moment.advance(time.Minute)

		messages, rejected, err := workspace.Pending()
		if err != nil {
			t.Fatalf("reading pending messages: %v", err)
		}
		if len(messages) != 0 {
			t.Errorf("a symbolic link was read as a message; only regular files in the outbox are messages")
		}
		if len(rejected) != 1 || !strings.Contains(rejected[0].Error(), "regular file") {
			t.Errorf("rejections = %v, want one naming the file kind", rejected)
		}
	})

	t.Run("a subdirectory is not read as a message", func(t *testing.T) {
		workspace, moment := newWorkspace(t)
		if err := os.Mkdir(filepath.Join(workspace.OutboxDir(), "nested.json"), 0o700); err != nil {
			t.Fatalf("creating the subdirectory: %v", err)
		}
		moment.advance(time.Minute)

		messages, rejected, err := workspace.Pending()
		if err != nil {
			t.Fatalf("reading pending messages: %v", err)
		}
		if len(messages) != 0 {
			t.Errorf("a directory was read as a message")
		}
		if len(rejected) != 1 || !strings.Contains(rejected[0].Error(), "directory") {
			t.Errorf("rejections = %v, want one naming the directory", rejected)
		}
	})
}

func TestAnIncompleteWriteIsNotReportedAsMalformed(t *testing.T) {
	workspace, moment := newWorkspace(t)

	// Half a document, which is what a reader sees when a writer did not stage
	// its file before renaming it into place.
	write(t, workspace, "partial.json", `{"schema_version":1,"id":"a","task_`)

	messages, rejected, err := workspace.Pending()
	if err != nil {
		t.Fatalf("reading pending messages: %v", err)
	}
	if len(messages) != 0 || len(rejected) != 0 {
		t.Fatalf("a document still being written was judged immediately: %d messages, %v", len(messages), rejected)
	}

	// Completed within the grace period: it must now be read as the message it
	// always was, and never reported as malformed.
	write(t, workspace, "partial.json", envelope("a", testTask, control.TypeProviderEvent))
	moment.advance(time.Second)

	messages, rejected, err = workspace.Pending()
	if err != nil {
		t.Fatalf("reading pending messages: %v", err)
	}
	if len(rejected) != 0 {
		t.Fatalf("a completed document was reported as malformed: %v", rejected)
	}
	if len(messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(messages))
	}

	// A document that never completes is eventually the agent's mistake.
	write(t, workspace, "broken.json", `{"schema_version":1,`)
	if _, rejected, err = workspace.Pending(); err != nil || len(rejected) != 0 {
		t.Fatalf("a new partial document was judged immediately: %v, %v", rejected, err)
	}
	moment.advance(time.Minute)
	_, rejected, err = workspace.Pending()
	if err != nil {
		t.Fatalf("reading pending messages: %v", err)
	}
	if len(rejected) != 1 || !strings.Contains(rejected[0].Error(), "valid JSON") {
		t.Errorf("rejections = %v, want one naming the JSON failure", rejected)
	}
}

func TestProcessedMessagesSurviveAReopenAndAnInterruptedAppend(t *testing.T) {
	root := t.TempDir()
	moment := &clock{at: time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)}

	workspace, err := control.Open(root, testProject, testTask, control.Options{Now: moment.now})
	if err != nil {
		t.Fatalf("opening a workspace: %v", err)
	}
	if err := workspace.Create(); err != nil {
		t.Fatalf("creating the workspace: %v", err)
	}
	write(t, workspace, "one.json", envelope("applied-one", testTask, control.TypeProviderEvent))

	messages, _, err := workspace.Pending()
	if err != nil || len(messages) != 1 {
		t.Fatalf("first read returned %d messages: %v", len(messages), err)
	}
	if err := workspace.MarkProcessed(messages[0], control.OutcomeApplied, ""); err != nil {
		t.Fatalf("marking processed: %v", err)
	}

	// A crash during an append leaves a record without its newline. The next
	// append must cost that record and nothing else, so the identifier before it
	// still counts as applied.
	record := filepath.Join(workspace.AgentDir(), "processed.jsonl")
	existing, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("reading the processed record: %v", err)
	}
	if err := os.WriteFile(record, append(existing, []byte(`{"id":"half-writ`)...), 0o600); err != nil {
		t.Fatalf("damaging the processed record: %v", err)
	}

	// A fresh workspace is what a restarted daemon builds.
	restarted, err := control.Open(root, testProject, testTask, control.Options{Now: moment.now})
	if err != nil {
		t.Fatalf("reopening the workspace: %v", err)
	}
	applied, err := restarted.Processed("applied-one")
	if err != nil {
		t.Fatalf("reading the processed record: %v", err)
	}
	if !applied {
		t.Error("an applied identifier was forgotten after a restart; the message would be applied twice")
	}

	messages, rejected, err := restarted.Pending()
	if err != nil {
		t.Fatalf("reading pending messages after a restart: %v", err)
	}
	if len(messages) != 0 || len(rejected) != 0 {
		t.Errorf("a restart re-delivered an applied message: %d messages, %v", len(messages), rejected)
	}

	// Appending after the damaged line must produce a readable record again.
	write(t, workspace, "two.json", envelope("applied-two", testTask, control.TypeProviderEvent))
	moment.advance(time.Minute)
	messages, _, err = restarted.Pending()
	if err != nil || len(messages) != 1 {
		t.Fatalf("second read returned %d messages: %v", len(messages), err)
	}
	if err := restarted.MarkProcessed(messages[0], control.OutcomeApplied, ""); err != nil {
		t.Fatalf("marking the second message processed: %v", err)
	}

	final, err := control.Open(root, testProject, testTask, control.Options{Now: moment.now})
	if err != nil {
		t.Fatalf("reopening the workspace: %v", err)
	}
	for _, id := range []string{"applied-one", "applied-two"} {
		applied, err := final.Processed(id)
		if err != nil {
			t.Fatalf("reading the processed record: %v", err)
		}
		if !applied {
			t.Errorf("identifier %q was not recorded as applied", id)
		}
	}
}

func TestRejectionErrorIsRecognisable(t *testing.T) {
	workspace, moment := newWorkspace(t)
	write(t, workspace, "foreign.json", envelope("a", otherTask, control.TypeProviderEvent))
	moment.advance(time.Minute)

	_, rejected, err := workspace.Pending()
	if err != nil {
		t.Fatalf("reading pending messages: %v", err)
	}
	if len(rejected) != 1 {
		t.Fatalf("rejections = %d, want 1", len(rejected))
	}

	// A rejected message is an ordinary event rather than a daemon failure, so
	// callers must be able to tell the two apart without reading the message.
	var rejection *control.RejectionError
	if !errors.As(rejected[0], &rejection) {
		t.Fatalf("rejection %v is not a *control.RejectionError", rejected[0])
	}
	if rejection.File != "foreign.json" {
		t.Errorf("rejection file = %q, want %q", rejection.File, "foreign.json")
	}
}
