package daemon

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/paths"
	"github.com/ma8el/feat/internal/reconcile"
	"github.com/ma8el/feat/internal/tmux/tmuxtest"
)

// withClock returns a daemon whose clock moves, which a restart's does.
func withClock(t *testing.T, arranged *preparation, server *tmuxtest.Server, now func() time.Time) *service {
	t.Helper()

	instance, err := New(Options{
		Layout:      arranged.service.layout,
		Environment: arranged.env,
		Build:       testBuild,
		Git:         arranged.fake,
		Tmux:        server,
		Logger:      slog.New(slog.DiscardHandler),
		Now:         now,
	})
	if err != nil {
		t.Fatalf("creating a daemon with a moving clock: %v", err)
	}
	return instance.service
}

// reconciled runs a pass and returns the report.
func reconciled(t *testing.T, service *service) api.Reconciliation {
	t.Helper()

	report, err := service.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	return report
}

// findings returns every finding of a class.
func findings(report api.Reconciliation, class reconcile.Class) []api.ReconciliationFinding {
	var found []api.ReconciliationFinding
	for _, finding := range report.Findings {
		if finding.Class == string(class) {
			found = append(found, finding)
		}
	}
	return found
}

// TestDaemonRestartLosesNoTaskIdentity is slice 12's first acceptance criterion.
//
// Identity is the task's own record and the metadata on the live tmux objects,
// neither of which the daemon holds in memory. So the check is that a second
// daemon over the same state directory and the same terminal server recovers the
// same identifiers, having been given a record whose stored copy was wrong.
func TestDaemonRestartLosesNoTaskIdentity(t *testing.T) {
	arranged := prepared(t)
	server := tmuxtest.New()
	service, _ := withTmux(t, arranged, server)

	task, err := service.PrepareTerminal(context.Background(), arranged.ref, placeholder(arranged.home))
	if err != nil {
		t.Fatalf("PrepareTerminal: %v", err)
	}
	live := task.Session.Tmux
	identity := task.ID
	branch := task.Repositories[0].Branch
	base := task.Repositories[0].BaseCommit

	// The stored target is stale but structurally valid, so only the metadata on
	// the live objects can repair it.
	task.Session.Tmux = domain.TmuxTarget{
		Socket: live.Socket, Session: "$999", Window: "@999", Pane: "%999",
	}
	if err := service.store.Tasks().Save(context.Background(), task); err != nil {
		t.Fatalf("saving the stale target: %v", err)
	}

	restarted, _ := withTmux(t, arranged, server)
	reconciled(t, restarted)

	recovered := arranged.reload(t)
	switch {
	case recovered.ID != identity:
		t.Errorf("task identity = %s, want %s", recovered.ID, identity)
	case recovered.Session == nil:
		t.Fatal("the restart discarded the session record")
	case recovered.Session.Tmux != live:
		t.Errorf("recovered target = %+v, want the live %+v", recovered.Session.Tmux, live)
	}
	if recovered.Repositories[0].Branch != branch {
		t.Errorf("branch = %q, want %q", recovered.Repositories[0].Branch, branch)
	}
	if recovered.Repositories[0].BaseCommit != base {
		t.Errorf("base commit = %q, want the immutable %q", recovered.Repositories[0].BaseCommit, base)
	}
}

// TestAnActionNamesSomethingAUserCanDo is the rule a finding lives or dies by.
//
// A task with no recorded provider session cannot be resumed, and the report
// used to send those users to "start the task again from the dashboard" — a
// command Feat has never had: `feat task` offers attach, cleanup, list, and
// review, and nothing launches a task that is past draft. An action naming
// nothing is worse than no action, because it is read as a way out.
//
// What is true is that a task with no recorded session has never held an agent
// conversation, so cleaning it up and preparing another loses only the brief.
func TestAnActionNamesSomethingAUserCanDo(t *testing.T) {
	// Both ways a task arrives with nothing to continue. A session whose agent
	// never reported starting, and a task confirmed by a launch that failed
	// before it had a terminal at all.
	for name, arrange := range map[string]func(*testing.T) *preparation{
		"a session with no provider identifier": func(t *testing.T) *preparation {
			t.Helper()
			return dead(t, "").preparation
		},
		"a confirmed task with no session": func(t *testing.T) *preparation {
			t.Helper()
			return prepared(t)
		},
	} {
		t.Run(name, func(t *testing.T) {
			// A fresh daemon over an empty tmux server, which is what a machine
			// that rebooted leaves: no terminal, and the record is all there is.
			restarted, _ := withTmux(t, arrange(t), tmuxtest.New())
			report := reconciled(t, restarted)

			terminals := findings(report, reconcile.ClassTerminal)
			if len(terminals) == 0 {
				t.Fatal("a missing terminal produced no finding")
			}
			for _, finding := range terminals {
				if finding.Status == "present" {
					continue
				}
				if strings.Contains(finding.Action, "start the task again") ||
					strings.Contains(finding.Action, "launch it again") {
					t.Errorf("the action offers a launch Feat cannot perform: %q", finding.Action)
				}
				if !strings.Contains(finding.Action, "clean it up") {
					t.Errorf("the action does not name what a user can actually do: %q", finding.Action)
				}
			}
		})
	}
}

// TestOneDamagedTerminalLeavesTheHealthyOnesUsable is slice 12's fifth
// acceptance criterion, and the deferral ADR-030 recorded.
//
// Before quarantine, one inconsistent tagged object failed discovery for the
// whole server: every unrelated task became unreachable and startup
// reconciliation stopped before it reached any of them. This fails against that
// behaviour.
func TestOneDamagedTerminalLeavesTheHealthyOnesUsable(t *testing.T) {
	arranged := prepared(t)

	// One window tagged with a metadata schema this build does not read, beside
	// the healthy terminal the arranged task is about to get. A future Feat
	// writing a newer schema is exactly how this arises in the field, and before
	// quarantine it failed discovery for the whole server.
	server := tmuxtest.New(tmuxtest.Terminal{
		Project: "app", Task: domain.NewTaskID().String(), Schema: "99",
		Session: "$7", Window: "@7", Pane: "%7", Directory: arranged.home,
	})
	service, _ := withTmux(t, arranged, server)
	if _, err := service.PrepareTerminal(context.Background(), arranged.ref, placeholder(arranged.home)); err != nil {
		t.Fatalf("PrepareTerminal alongside a damaged terminal: %v", err)
	}

	restarted, _ := withTmux(t, arranged, server)
	report := reconciled(t, restarted)

	// The damage is reported rather than swallowed.
	var damaged bool
	for _, finding := range findings(report, reconcile.ClassTerminal) {
		if finding.Status == string(reconcile.StatusDamaged) {
			damaged = true
			if finding.Detail == "" {
				t.Error("a damaged terminal was reported without saying what is wrong")
			}
			if finding.Action == "" {
				t.Error("a damaged terminal was reported without saying what the user can do")
			}
		}
	}
	if !damaged {
		t.Fatal("the damaged tmux object was not reported")
	}

	// And the healthy task is still recovered, which is the whole point.
	recovered := arranged.reload(t)
	if recovered.Session == nil {
		t.Fatal("one damaged terminal discarded a healthy task's session")
	}
	if recovered.Session.Process == domain.ProcessStopped {
		t.Error("a healthy task was marked stopped because another terminal was damaged")
	}

	// The healthy task is still reachable, which a whole-server discovery
	// failure would have prevented.
	if _, err := restarted.AttachInfo(context.Background(), arranged.ref.Task); err != nil {
		t.Errorf("a healthy task became unattachable because another terminal was damaged: %v", err)
	}
}

// TestStoppedContainersAreNotRestarted is slice 12's second acceptance
// criterion, checked by counting the commands a pass produces.
//
// That no container command ran at all is a stronger statement than that a
// container happens to still be stopped.
func TestStoppedContainersAreNotRestarted(t *testing.T) {
	arranged := prepared(t)
	server := tmuxtest.New()
	service, _ := withTmux(t, arranged, server)
	if _, err := service.PrepareTerminal(context.Background(), arranged.ref, placeholder(arranged.home)); err != nil {
		t.Fatalf("PrepareTerminal: %v", err)
	}

	// A recorded runtime whose services are not running. The fixture project
	// configures none, so the record is written directly: what is under test is
	// what reconciliation does with one, not how it got there.
	task := arranged.reload(t)
	task.Runtime = &domain.RuntimeEnvironment{
		Provider: runtimeProvider,
		Identity: "app-" + task.ID.Key().String(),
		State:    domain.RuntimeStopped,
		Health:   domain.HealthUnknown,
	}
	if err := service.store.Tasks().Save(context.Background(), task); err != nil {
		t.Fatalf("saving the stopped runtime: %v", err)
	}

	restarted, _ := withTmux(t, arranged, server)
	before := len(server.Calls())
	reconciled(t, restarted)

	// Only what the pass itself ran. Counting the whole server's history would
	// include the launch that created the terminal in the first place.
	for _, call := range server.Calls()[before:] {
		for _, command := range []string{"new-session", "new-window", "respawn-pane", "kill-window", "kill-session"} {
			if len(call) > 0 && call[0] == command {
				t.Errorf("reconciliation ran %q; recovery reports and never restarts", command)
			}
		}
	}
	if after := arranged.reload(t); after.Runtime != nil && after.Runtime.State == domain.RuntimeRunning {
		t.Error("reconciliation started a stopped runtime")
	}
}

// TestOrphanResourcesAreReportedBeforeAdoptionOrRemoval is slice 12's fourth
// acceptance criterion.
func TestOrphanResourcesAreReportedBeforeAdoptionOrRemoval(t *testing.T) {
	arranged := prepared(t)
	orphan := domain.NewTaskID()
	server := tmuxtest.New(tmuxtest.Terminal{
		Project: "app", Task: orphan.String(),
		Session: "$8", Window: "@8", Pane: "%8", Directory: arranged.home,
	})

	service, _ := withTmux(t, arranged, server)
	report := reconciled(t, service)

	var reported bool
	for _, finding := range findings(report, reconcile.ClassTerminal) {
		if finding.Status == string(reconcile.StatusOrphaned) && finding.TaskID == orphan.String() {
			reported = true
			if finding.Action == "" {
				t.Error("an orphan was reported with no action for the user")
			}
		}
	}
	if !reported {
		t.Fatal("an orphaned terminal was not reported")
	}

	// Neither adopted nor removed.
	if server.Ran("kill-window") || server.Ran("kill-session") {
		t.Error("reconciliation removed an orphan")
	}
	if _, err := service.Task(context.Background(), orphan); err == nil {
		t.Error("reconciliation adopted an orphaned terminal as a task")
	}
}

// TestALiveTasksOwnDirectoryIsNotAnOrphan is the defect a real task's report
// produced.
//
// A configured worktree root of `…/work/{task_id}` puts each task's worktrees
// one level below the fixed prefix the orphan scan lists, so what the scan sees
// are task directories rather than worktrees. Comparing only for equality
// reported every live task's own directory as a directory "no task records", and
// told the user to delete it if it looked stale — which is the one
// recommendation a recovery report must never make wrongly.
func TestALiveTasksOwnDirectoryIsNotAnOrphan(t *testing.T) {
	arranged := prepared(t)
	server := tmuxtest.New()
	service, _ := withTmux(t, arranged, server)
	if _, err := service.PrepareTerminal(context.Background(), arranged.ref, placeholder(arranged.home)); err != nil {
		t.Fatalf("PrepareTerminal: %v", err)
	}

	task := arranged.reload(t)
	worktree := task.Repositories[0].WorktreePath
	if worktree == "" {
		t.Fatal("the fixture task records no worktree")
	}
	owned := filepath.Dir(worktree)

	report := reconciled(t, service)
	for _, finding := range findings(report, reconcile.ClassWorktrees) {
		if finding.Status != string(reconcile.StatusOrphaned) {
			continue
		}
		if finding.Identity == owned || strings.HasPrefix(worktree, finding.Identity+string(filepath.Separator)) {
			t.Errorf("a live task's own directory %s was reported as an orphan a user should delete", finding.Identity)
		}
	}

	// A directory that really is nobody's is still reported, so the rule
	// narrows the scan rather than switching it off. It sits beside the task's
	// own directory, which is where an abandoned task's would be.
	stale := filepath.Join(filepath.Dir(owned), "left-behind")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatalf("creating a stale directory: %v", err)
	}

	var reported bool
	for _, finding := range findings(reconciled(t, service), reconcile.ClassWorktrees) {
		if finding.Status == string(reconcile.StatusOrphaned) && finding.Identity == stale {
			reported = true
		}
	}
	if !reported {
		t.Error("a directory no task records was not reported as an orphan")
	}
}

// TestReconciliationReportsWhatItCouldNotCheck keeps a pass honest.
//
// An enumeration that failed is a problem rather than an answer of "nothing":
// reporting an unreadable tmux server as an empty one would tell a user their
// terminals are gone.
func TestReconciliationReportsWhatItCouldNotCheck(t *testing.T) {
	arranged := prepared(t)
	server := tmuxtest.New()
	server.Fail("list-sessions", errors.New("connection refused"))

	service, _ := withTmux(t, arranged, server)
	report := reconciled(t, service)

	if len(report.Problems) == 0 {
		t.Fatal("a tmux server that could not be read was reported as an empty one")
	}
	if !strings.Contains(report.Problems[0].Reason, "connection refused") {
		t.Errorf("problem = %q, want the tool's own reason", report.Problems[0].Reason)
	}
	if !report.NeedsAttention {
		t.Error("a pass that could not check something reported nothing to attend to")
	}
}

// TestACrashIsVisibleToTheNextRun is what the durable daemon record is for.
//
// The clean-shutdown flag is written by the run that ends rather than by the one
// that starts, so a daemon that was killed leaves the record saying its run
// never ended. Nothing else in Feat can distinguish the two.
func TestACrashIsVisibleToTheNextRun(t *testing.T) {
	arranged := prepared(t)
	server := tmuxtest.New()

	first, _ := withTmux(t, arranged, server)
	if err := first.claimStateDirectory(context.Background()); err != nil {
		t.Fatalf("claiming the state directory: %v", err)
	}
	// No release: the daemon was killed.

	second, _ := withTmux(t, arranged, server)
	report := reconciled(t, second)
	if report.PreviousRunEndedCleanly {
		t.Error("a daemon that was killed is reported as having shut down cleanly")
	}

	// A run that ends properly is reported as such.
	if err := second.claimStateDirectory(context.Background()); err != nil {
		t.Fatalf("claiming the state directory again: %v", err)
	}
	second.releaseStateDirectory(context.Background())

	third, _ := withTmux(t, arranged, server)
	if report := reconciled(t, third); !report.PreviousRunEndedCleanly {
		t.Error("a daemon that shut down cleanly is reported as having crashed")
	}
}

// TestADaemonCanStartAgainAfterACleanShutdown is the defect a real stop and
// start produced, stated exactly.
//
// The first version of the claim carried the previous run's stop time into the
// new run's record. A record describes one run, and the domain refuses one whose
// stop precedes its own start — so a daemon that shut down cleanly could never
// start again, and only a daemon that had crashed could.
//
// The clock is what matters here, and it is why the suite missed this: every
// other fixture in this file freezes time, so a carried-forward stop and a fresh
// start were the same instant and the invariant held. A daemon that is stopped
// and started has a clock that moved, so this test moves one.
func TestADaemonCanStartAgainAfterACleanShutdown(t *testing.T) {
	arranged := prepared(t)
	server := tmuxtest.New()

	clock := reconcileTime
	advance := func() time.Time {
		clock = clock.Add(time.Minute)
		return clock
	}

	// Three runs, each ending properly. The third is the one the defect made
	// impossible; the second is what made the third's record wrong.
	for run := 1; run <= 3; run++ {
		service := withClock(t, arranged, server, advance)
		if err := service.claimStateDirectory(context.Background()); err != nil {
			t.Fatalf("run %d could not claim the state directory after a clean shutdown: %v", run, err)
		}
		report := reconciled(t, service)
		if run > 1 && !report.PreviousRunEndedCleanly {
			t.Errorf("run %d reports the previous clean shutdown as a crash", run)
		}
		service.releaseStateDirectory(context.Background())
	}

	// And the record on disk describes the run that just ended rather than a
	// mixture of two.
	stored, err := arranged.service.store.Daemons().Load(context.Background())
	if err != nil {
		t.Fatalf("reading the record back: %v", err)
	}
	if err := stored.Validate(); err != nil {
		t.Errorf("the stored record is not internally consistent: %v", err)
	}
	if !stored.EndedCleanly || stored.StoppedAt.Before(stored.StartedAt) {
		t.Errorf("record = %+v, want a clean stop that does not precede its own start", stored)
	}
}

// TestAStateDirectoryFromANewerBuildIsRefused is the record's other reader, and
// the one that has to exist before it is needed.
//
// An older daemon that wrote over a newer state directory would discard whatever
// the newer schema added, and unlike every other recovery failure that loss is
// silent.
func TestAStateDirectoryFromANewerBuildIsRefused(t *testing.T) {
	arranged := prepared(t)
	server := tmuxtest.New()
	service, _ := withTmux(t, arranged, server)

	future := &domain.DaemonRecord{
		StateSchema:  domain.StateSchemaVersion + 1,
		StartedAt:    reconcileTime,
		StoppedAt:    reconcileTime,
		EndedCleanly: true,
		Version:      "9.9.9",
	}
	if err := service.store.Daemons().Save(context.Background(), future); err != nil {
		t.Fatalf("recording a newer state directory: %v", err)
	}

	err := service.claimStateDirectory(context.Background())
	if err == nil {
		t.Fatal("a state directory written by a newer Feat was claimed and would have been overwritten")
	}
	for _, want := range []string{"newer Feat", "Upgrade Feat", paths.EnvDataHome} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to mention %q", err, want)
		}
	}

	// And the record is untouched, so the newer build still finds its own.
	stored, err := service.store.Daemons().Load(context.Background())
	if err != nil {
		t.Fatalf("reading the record back: %v", err)
	}
	if stored.StateSchema != future.StateSchema {
		t.Errorf("state schema = %d, want the untouched %d", stored.StateSchema, future.StateSchema)
	}
}

// TestReconciliationSerialisesItsWritesWithEveryOtherWriter is ADR-036's
// evidence 9 applied to the pass that runs beside every request.
//
// The startup pass runs before anything is served, but the on-demand one does
// not, and a load-change-save cycle that started from a copy taken outside the
// lock overwrites whatever a request wrote in between.
func TestReconciliationSerialisesItsWritesWithEveryOtherWriter(t *testing.T) {
	arranged := prepared(t)
	server := tmuxtest.New()
	service, _ := withTmux(t, arranged, server)
	if _, err := service.PrepareTerminal(context.Background(), arranged.ref, placeholder(arranged.home)); err != nil {
		t.Fatalf("PrepareTerminal: %v", err)
	}

	// Hold the task's lock, change the record underneath, and let the pass run.
	// It must read what was just written rather than the copy it listed with.
	// The change is the one control delivery makes: an agent message advances
	// the last processed event sequence. Reconciliation writes the same record,
	// so a pass that saved a copy loaded before this would silently replay the
	// message the daemon had already applied.
	release := service.locks.lock(arranged.ref.Task)
	task, err := service.store.Tasks().Load(context.Background(), arranged.ref)
	if err != nil {
		t.Fatalf("loading the task: %v", err)
	}
	if err := task.Session.RecordEvent(42, reconcileTime); err != nil {
		t.Fatalf("recording a control event: %v", err)
	}
	if err := service.store.Tasks().Save(context.Background(), task); err != nil {
		t.Fatalf("saving the applied event: %v", err)
	}
	release()

	reconciled(t, service)

	after := arranged.reload(t)
	if after.Session == nil {
		t.Fatal("reconciliation discarded the session")
	}
	if after.Session.LastEventSequence != 42 {
		t.Errorf("last event sequence = %d, want 42: reconciliation overwrote a change made after "+
			"it listed the tasks, so the daemon would replay a message it had already applied",
			after.Session.LastEventSequence)
	}
}
