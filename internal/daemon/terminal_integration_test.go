package daemon

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/tmux"
)

// TestRealDaemonRestartRediscoversTaggedTerminal is the Slice 5 restart
// acceptance criterion at the daemon boundary. A new daemon instance ignores a
// deliberately stale stored target and recovers the live IDs from tmux
// metadata.
func TestRealDaemonRestartRediscoversTaggedTerminal(t *testing.T) {
	if os.Getenv(envIntegration) == "" {
		t.Skipf("set %s=1 to run tests against real tmux", envIntegration)
	}
	if _, err := exec.LookPath(tmux.Executable); err != nil {
		t.Skip("tmux is not installed")
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
	if err := restarted.service.reconcileTmux(ctx); err != nil {
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
