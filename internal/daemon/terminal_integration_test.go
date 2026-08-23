package daemon

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/integrationtest"
	"github.com/ma8el/feat/internal/tmux"
)

// TestRealKilledWindowIsReportedAndRebuilt is the recovery a user reaches for
// after killing a task's window from inside tmux.
//
// It exercises the mechanics against the real tool rather than the resume that
// sits on top of them: what a resume adds is a Claude command line, which the
// unit tests assert on and which this machine may not have. What it needs from
// tmux is that a killed window is reported as missing rather than as some other
// absence, and that asking for the terminal again builds a new tagged one.
func TestRealKilledWindowIsReportedAndRebuilt(t *testing.T) {
	if !integrationtest.Enabled() {
		t.Skipf("set %s=1 to run tests against real tmux", integrationtest.Env)
	}
	if _, err := exec.LookPath(tmux.Executable); err != nil {
		integrationtest.Unavailable(t, integrationtest.Tmux, "tmux is not installed")
	}

	arranged := arrangeTask(t, newFakeGit())
	ctx := context.Background()
	if _, err := arranged.service.PrepareTask(ctx, arranged.ref, selection()); err != nil {
		t.Fatalf("PrepareTask: %v", err)
	}

	runner := tmux.HostRunner{Timeout: 10 * time.Second}
	t.Cleanup(func() {
		_, _ = runner.Run(context.Background(), arranged.service.layout.TmuxSocket(), "kill-server")
	})
	command := tmux.CommandSpec{Program: "/usr/bin/yes", Directory: arranged.home}
	task, err := arranged.service.PrepareTerminal(ctx, arranged.ref, command)
	if err != nil {
		t.Fatalf("PrepareTerminal: %v", err)
	}
	killed := task.Session.Tmux

	if _, err := runner.Run(ctx, killed.Socket, "kill-window", "-t", killed.Window); err != nil {
		t.Fatalf("killing the task's window: %v", err)
	}

	// The absence is the one a client can act on, rather than the one it would
	// report for a task identifier that names nothing.
	_, err = arranged.service.AttachInfo(ctx, arranged.ref.Task)
	if !api.IsTerminalMissing(err) {
		t.Fatalf("AttachInfo after the kill = %v, want a missing terminal", err)
	}

	rebuilt, err := arranged.service.PrepareTerminal(ctx, arranged.ref, command)
	if err != nil {
		t.Fatalf("rebuilding the terminal: %v", err)
	}
	if rebuilt.Session.Process != domain.ProcessRunning {
		t.Errorf("process = %q after rebuilding, want running", rebuilt.Session.Process)
	}

	// The record names the terminal that exists rather than the one that was
	// killed. It is checked this way rather than by comparing identifiers with
	// the dead window: killing a session's last window ends the session, and the
	// server that exits with it reissues the same identifiers to the next one.
	info, err := arranged.service.AttachInfo(ctx, arranged.ref.Task)
	if err != nil {
		t.Fatalf("AttachInfo after rebuilding: %v", err)
	}
	if info.Session != rebuilt.Session.Tmux.Session || info.Window != rebuilt.Session.Tmux.Window ||
		info.Pane != rebuilt.Session.Tmux.Pane {
		t.Errorf("the record names %+v and tmux has %+v", rebuilt.Session.Tmux, info)
	}
}

// TestRealDaemonRestartRediscoversTaggedTerminal is the restart rule at the
// daemon boundary. A new daemon instance ignores a
// deliberately stale stored target and recovers the live IDs from tmux
// metadata.
func TestRealDaemonRestartRediscoversTaggedTerminal(t *testing.T) {
	if !integrationtest.Enabled() {
		t.Skipf("set %s=1 to run tests against real tmux", integrationtest.Env)
	}
	if _, err := exec.LookPath(tmux.Executable); err != nil {
		integrationtest.Unavailable(t, integrationtest.Tmux, "tmux is not installed")
	}

	fake := newFakeGit()
	arranged := arrangeTask(t, fake)
	ctx := context.Background()
	if _, err := arranged.service.PrepareTask(ctx, arranged.ref, selection()); err != nil {
		t.Fatalf("PrepareTask: %v", err)
	}

	runner := tmux.HostRunner{Timeout: 10 * time.Second}
	t.Cleanup(func() {
		_, _ = runner.Run(context.Background(), arranged.service.layout.TmuxSocket(), "kill-server")
	})
	task, err := arranged.service.PrepareTerminal(ctx, arranged.ref, tmux.CommandSpec{
		Program: "/usr/bin/yes", Directory: arranged.home,
	})
	if err != nil {
		t.Fatalf("PrepareTerminal: %v", err)
	}
	live := task.Session.Tmux
	if task.Session.Process != domain.ProcessRunning {
		t.Errorf("process = %q, want running", task.Session.Process)
	}

	// The record is stale but structurally valid. Names and indexes would not
	// repair this; only the task metadata on the live objects can.
	task.Session.Tmux = domain.TmuxTarget{
		Socket: live.Socket, Session: "$999", Window: "@999", Pane: "%999",
	}
	if err := arranged.service.store.Tasks().Save(ctx, task); err != nil {
		t.Fatalf("saving stale target: %v", err)
	}

	restarted, err := New(Options{
		Layout:      arranged.service.layout,
		Environment: arranged.env,
		Build:       testBuild,
		Git:         fake,
		Tmux:        runner,
		Now:         func() time.Time { return time.Date(2026, 8, 5, 11, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("creating restarted daemon: %v", err)
	}
	if _, err := restarted.service.Reconcile(ctx); err != nil {
		t.Fatalf("reconcileTmux: %v", err)
	}

	recovered, err := restarted.store.Tasks().Load(ctx, arranged.ref)
	if err != nil {
		t.Fatalf("loading reconciled task: %v", err)
	}
	if recovered.Session == nil || recovered.Session.Tmux != live {
		t.Errorf("recovered target = %+v, want %+v", recovered.Session, live)
	}

	info, err := restarted.service.AttachInfo(ctx, arranged.ref.Task)
	if err != nil {
		t.Fatalf("AttachInfo: %v", err)
	}
	if info.Socket != live.Socket || info.Session != live.Session || info.Window != live.Window || info.Pane != live.Pane {
		t.Errorf("attach info = %+v, want %+v", info, live)
	}

	history, err := restarted.store.Events().Replay(ctx, arranged.ref)
	if err != nil {
		t.Fatalf("replaying events: %v", err)
	}
	var reconciled bool
	for _, event := range history.Events {
		if event.Type == domain.EventReconciled {
			reconciled = true
		}
	}
	if !reconciled {
		t.Error("restart did not record a reconciliation event")
	}
}
