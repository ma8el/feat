package daemon

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/reconcile"
	"github.com/ma8el/feat/internal/tmux"
	"github.com/ma8el/feat/internal/tmux/tmuxtest"
)

// reconcileTime is when a restarted daemon observes the world. It differs from
// the fixture's creation time so that an observation can be told apart from the
// record it repaired.
var reconcileTime = time.Date(2026, 8, 5, 11, 0, 0, 0, time.UTC)

// withTmux returns a daemon instance over the arranged state and one fake tmux
// server. Calling it twice with the same server is what a daemon restart looks
// like: the terminals outlive the process that recorded them.
func withTmux(t *testing.T, arranged *preparation, server *tmuxtest.Server) (*service, *bytes.Buffer) {
	t.Helper()

	var logs bytes.Buffer
	instance, err := New(Options{
		Layout:      arranged.service.layout,
		Environment: arranged.env,
		Build:       testBuild,
		Git:         arranged.fake,
		Tmux:        server,
		Logger:      slog.New(slog.NewTextHandler(&logs, nil)),
		Now:         func() time.Time { return reconcileTime },
	})
	if err != nil {
		t.Fatalf("creating a daemon over the fake tmux server: %v", err)
	}
	return instance.service, &logs
}

// prepared arranges a confirmed task whose terminal can be created.
func prepared(t *testing.T) *preparation {
	t.Helper()

	arranged := arrangeTask(t, newFakeGit())
	if _, err := arranged.service.PrepareTask(context.Background(), arranged.ref, selection()); err != nil {
		t.Fatalf("PrepareTask: %v", err)
	}
	return arranged
}

func placeholder(directory string) tmux.CommandSpec {
	return tmux.CommandSpec{Program: "/usr/bin/yes", Directory: directory}
}

// events returns the recorded history of the arranged task.
func events(t *testing.T, service *service, arranged *preparation) []domain.Event {
	t.Helper()

	history, err := service.store.Events().Replay(context.Background(), arranged.ref)
	if err != nil {
		t.Fatalf("replaying events: %v", err)
	}
	return history.Events
}

func recorded(history []domain.Event, kind domain.EventType) (domain.Event, bool) {
	for _, event := range history {
		if event.Type == kind {
			return event, true
		}
	}
	return domain.Event{}, false
}

// TestPreparingATerminalRecordsTheLiveTarget is the ordinary path: the daemon
// creates the terminal, records the stable target it observed, and explains the
// change on the event stream.
func TestPreparingATerminalRecordsTheLiveTarget(t *testing.T) {
	arranged := prepared(t)
	server := tmuxtest.New()
	service, _ := withTmux(t, arranged, server)

	task, err := service.PrepareTerminal(context.Background(), arranged.ref, placeholder(arranged.home))
	if err != nil {
		t.Fatalf("PrepareTerminal: %v", err)
	}
	if task.Session == nil {
		t.Fatal("the prepared task has no agent session")
	}
	if task.Session.Process != domain.ProcessRunning {
		t.Errorf("process = %q, want running", task.Session.Process)
	}
	if task.Session.Tmux.Socket != arranged.service.layout.TmuxSocket() {
		t.Errorf("socket = %q, want the dedicated one", task.Session.Tmux.Socket)
	}

	// The snapshot on disk is the only copy a restarted daemon would have.
	stored := arranged.reload(t)
	if stored.Session == nil || stored.Session.Tmux != task.Session.Tmux {
		t.Errorf("stored session = %+v, want the recorded target %+v", stored.Session, task.Session.Tmux)
	}

	event, ok := recorded(events(t, service, arranged), domain.EventProcessChanged)
	if !ok {
		t.Fatal("no process event explains the new terminal")
	}
	if !strings.Contains(event.Detail, task.Session.Tmux.Pane) {
		t.Errorf("event detail = %q, want the pane it refers to", event.Detail)
	}

	// Repeating the request is the recovery path, not a second terminal.
	again, err := service.PrepareTerminal(context.Background(), arranged.ref, placeholder(arranged.home))
	if err != nil {
		t.Fatalf("second PrepareTerminal: %v", err)
	}
	if again.Session.Tmux != task.Session.Tmux {
		t.Errorf("second target = %+v, want %+v", again.Session.Tmux, task.Session.Tmux)
	}
}

// TestATerminalThatCannotBeCreatedFailsTheTaskExplainably covers the branch a
// user meets first: tmux refuses, and the task has to say so rather than stay
// confirmed with nothing behind it.
func TestATerminalThatCannotBeCreatedFailsTheTaskExplainably(t *testing.T) {
	arranged := prepared(t)
	server := tmuxtest.New()
	server.Fail("new-session", errors.New("tmux server refused the connection"))
	service, _ := withTmux(t, arranged, server)

	_, err := service.PrepareTerminal(context.Background(), arranged.ref, placeholder(arranged.home))
	if err == nil {
		t.Fatal("PrepareTerminal succeeded despite the injected tmux failure")
	}
	if !strings.Contains(err.Error(), "refused the connection") {
		t.Errorf("error = %v, want the tmux failure it was caused by", err)
	}

	stored := arranged.reload(t)
	if stored.Workflow != domain.WorkflowFailed {
		t.Errorf("workflow = %q, want failed", stored.Workflow)
	}
	if stored.Session != nil {
		t.Errorf("session = %+v, want none recorded for a terminal that was never created", stored.Session)
	}
	if _, ok := recorded(events(t, service, arranged), domain.EventWorkflowChanged); !ok {
		t.Error("the failure was not explained on the event stream")
	}
}

// TestRestartRepairsAStaleTargetFromLiveMetadata is the restart criterion at
// the daemon boundary, without requiring tmux to be installed.
func TestRestartRepairsAStaleTargetFromLiveMetadata(t *testing.T) {
	arranged := prepared(t)
	server := tmuxtest.New()
	service, _ := withTmux(t, arranged, server)

	task, err := service.PrepareTerminal(context.Background(), arranged.ref, placeholder(arranged.home))
	if err != nil {
		t.Fatalf("PrepareTerminal: %v", err)
	}
	live := task.Session.Tmux

	// The record is stale but structurally valid, so only the metadata on the
	// live objects can repair it.
	task.Session.Tmux = domain.TmuxTarget{
		Socket: live.Socket, Session: "$999", Window: "@999", Pane: "%999",
	}
	if err := service.store.Tasks().Save(context.Background(), task); err != nil {
		t.Fatalf("saving the stale target: %v", err)
	}

	restarted, _ := withTmux(t, arranged, server)
	if _, err := restarted.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconciling: %v", err)
	}

	recoveredTask := arranged.reload(t)
	if recoveredTask.Session == nil || recoveredTask.Session.Tmux != live {
		t.Errorf("recovered target = %+v, want %+v", recoveredTask.Session, live)
	}
	if _, ok := recorded(events(t, restarted, arranged), domain.EventReconciled); !ok {
		t.Error("the restart did not record a reconciliation event")
	}

	info, err := restarted.AttachInfo(context.Background(), arranged.ref.Task)
	if err != nil {
		t.Fatalf("AttachInfo: %v", err)
	}
	if info.Session != live.Session || info.Window != live.Window || info.Pane != live.Pane {
		t.Errorf("attach info = %+v, want %+v", info, live)
	}
}

// TestRestartMarksAMissingTerminalStoppedWithoutRestartingIt keeps recovery
// honest: the daemon reports what it found and does not invent a process the
// user did not ask it to start again.
func TestRestartMarksAMissingTerminalStoppedWithoutRestartingIt(t *testing.T) {
	arranged := prepared(t)
	service, _ := withTmux(t, arranged, tmuxtest.New())
	if _, err := service.PrepareTerminal(context.Background(), arranged.ref, placeholder(arranged.home)); err != nil {
		t.Fatalf("PrepareTerminal: %v", err)
	}

	// The computer restarted: the recorded terminal is gone with the server.
	empty := tmuxtest.New()
	restarted, _ := withTmux(t, arranged, empty)
	if _, err := restarted.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconciling: %v", err)
	}

	stored := arranged.reload(t)
	if stored.Session == nil {
		t.Fatal("reconciliation discarded the session record")
	}
	if stored.Session.Process != domain.ProcessStopped {
		t.Errorf("process = %q, want stopped", stored.Session.Process)
	}
	for _, command := range []string{"new-session", "new-window", "respawn-pane"} {
		if empty.Ran(command) {
			t.Errorf("recovery ran %q, but a missing terminal is reported rather than restarted", command)
		}
	}

	event, ok := recorded(events(t, restarted, arranged), domain.EventReconciled)
	if !ok {
		t.Fatal("the missing terminal was not explained on the event stream")
	}
	if !strings.Contains(event.Detail, "not restarted") {
		t.Errorf("event detail = %q, want it to say the terminal was not restarted", event.Detail)
	}
}

// TestReconciliationReportsAConflictingProjectRatherThanGuessing covers the
// rule that missing or conflicting metadata is reported: a window claiming the
// task for another project must not silently rebind it.
func TestReconciliationReportsAConflictingProjectRatherThanGuessing(t *testing.T) {
	arranged := prepared(t)
	service, _ := withTmux(t, arranged, tmuxtest.New())
	task, err := service.PrepareTerminal(context.Background(), arranged.ref, placeholder(arranged.home))
	if err != nil {
		t.Fatalf("PrepareTerminal: %v", err)
	}
	before := task.Session.Tmux

	// The same task id, tagged for a project that does not own it.
	conflicting := tmuxtest.New(tmuxtest.Terminal{
		Project: "other", Task: arranged.ref.Task.String(),
		Session: "$4", Window: "@4", Pane: "%4", Directory: arranged.home,
	})
	restarted, _ := withTmux(t, arranged, conflicting)

	report, err := restarted.Reconcile(context.Background())
	if err != nil {
		// A conflict is one task's problem. It is a finding rather than an
		// error, because failing the pass would take every healthy task's
		// recovery with it, which is the blast radius ADR-037 bounds.
		t.Fatalf("a conflicting terminal must be reported, not fail the pass: %v", err)
	}
	finding, ok := found(report, reconcile.ClassTerminal, reconcile.StatusInconsistent)
	if !ok {
		t.Fatal("reconciliation adopted a terminal belonging to another project")
	}
	if !strings.Contains(finding.Detail, "other") || !strings.Contains(finding.Detail, "app") {
		t.Errorf("detail = %q, want both the claimed and the recorded project", finding.Detail)
	}
	if stored := arranged.reload(t); stored.Session == nil || stored.Session.Tmux != before {
		t.Errorf("stored target = %+v, want the untouched %+v", stored.Session, before)
	}
}

// found returns the first finding of a class and status.
func found(report api.Reconciliation, class reconcile.Class, status reconcile.Status) (api.ReconciliationFinding, bool) {
	for _, finding := range report.Findings {
		if finding.Class == string(class) && finding.Status == string(status) {
			return finding, true
		}
	}
	return api.ReconciliationFinding{}, false
}

// TestReconciliationReportsATaggedTerminalWithNoRecordedTask keeps an orphan
// visible. What to do about it is the user's choice; recovery must not drop
// it.
func TestReconciliationReportsATaggedTerminalWithNoRecordedTask(t *testing.T) {
	arranged := prepared(t)
	orphan := domain.NewTaskID()
	server := tmuxtest.New(tmuxtest.Terminal{
		Project: "app", Task: orphan.String(),
		Session: "$8", Window: "@8", Pane: "%8", Directory: arranged.home,
	})

	service, logs := withTmux(t, arranged, server)
	service.startupReconcile(context.Background())

	report, ok := service.Reconciliation()
	if !ok {
		t.Fatal("no reconciliation report was produced")
	}
	finding, ok := found(report, reconcile.ClassTerminal, reconcile.StatusOrphaned)
	if !ok {
		t.Fatal("the orphaned terminal was dropped rather than reported")
	}
	if finding.TaskID != orphan.String() {
		t.Errorf("orphan finding names task %s, want %s", finding.TaskID, orphan)
	}
	if finding.Action == "" {
		t.Error("an orphan was reported without saying what the user can do about it")
	}
	if !strings.Contains(logs.String(), orphan.String()) {
		t.Errorf("the orphaned terminal was not logged; log:\n%s", logs.String())
	}
}
