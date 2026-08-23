package daemon

import (
	"context"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/reconcile"
)

// What `docker compose ps --format json` prints for this fixture's service in
// each of the three states the lifecycle moves it between.
const (
	// The container identifier is the fake's own, so that the mount and
	// environment inspections a launch runs are answered as they already are for
	// a healthy container: what these tests vary is the state, and nothing else.
	psExited = `[{"ID":"c0ffee","Name":"feat-dev-1","Service":"dev",` +
		`"State":"exited","Status":"Exited (137) 2 minutes ago"}]`
	psStopped = `[{"ID":"c0ffee","Name":"feat-dev-1","Service":"dev",` +
		`"State":"exited","Status":"Exited (0) 1 second ago"}]`
	psRunning = `[{"ID":"c0ffee","Name":"feat-dev-1","Service":"dev",` +
		`"State":"running","Status":"Up 2 seconds"}]`
)

// observePS is the shortened Compose command a state observation runs, which is
// how composetest keys an arranged answer.
const observePS = "ps --all --format json " + fixtureService

// sessionFailed is the phrase notify.Compose puts in the body of the condition
// this lifecycle raises.
const sessionFailed = "the agent session failed"

// recordProviderSession writes the identifier an agent reports at session start.
//
// The fake provider never sends one, and without it a resume is refused a step
// earlier on a different rule — which would make every test below pass for a
// reason that has nothing to do with what it is checking.
func (d *drafting) recordProviderSession(t *testing.T, task *domain.Task) {
	t.Helper()

	current := d.reload(t, task.ID)
	current.Session.ProviderSessionID = "e3f1a0c2-0000-4000-8000-1234567890ab"
	if err := d.service.store.Tasks().Save(context.Background(), current); err != nil {
		t.Fatalf("recording the provider session: %v", err)
	}
}

// TestADeadContainerEndsTheSessionItWasRunning is the invariant that ties a
// session to the environment it runs in: an agent process cannot be alive while
// its container is not running.
//
// It is checked here rather than only through resume because it is the record
// that was wrong. Reconciliation wrote what it saw onto the execution record and
// left the session's process state alone, and a tmux window outlives the
// container it ran a command in — so nothing in a pass that had just seen a dead
// container corrected the claim that an agent was running in it (ADR-057).
func TestADeadContainerEndsTheSessionItWasRunning(t *testing.T) {
	arranged := arrangeDrafting(t)
	arranged.service.notifiable.Store(true)
	task := arranged.launched(t)

	if before := arranged.reload(t, task.ID); !before.Session.Process.Alive() {
		t.Fatalf("the launched session is %s, want a record claiming a live process", before.Session.Process)
	}
	arranged.docker.Answer(observePS, psExited)

	reconciled(t, arranged.service)

	after := arranged.reload(t, task.ID)
	if after.Session.Process != domain.ProcessFailed {
		t.Errorf("the process is %s after its container died, want %s",
			after.Session.Process, domain.ProcessFailed)
	}
	if after.Session.Execution.Running {
		t.Error("the execution record still claims a running container")
	}

	// And the user is told. A session that died while nobody was attached is
	// exactly what this condition exists for, and the dogfood report that found
	// this was somebody discovering it by hand hours later.
	var told bool
	for _, sent := range arranged.notifier.sent() {
		if strings.Contains(sent.Body, sessionFailed) && strings.Contains(sent.Title, task.Key().String()) {
			told = true
		}
	}
	if !told {
		t.Errorf("no notification named the failed session of %s; sent: %v",
			task.Key(), arranged.notifier.sent())
	}
}

// TestATaskWhoseContainerDiedCanBeResumed is the dead end this work closes, end
// to end: what reconciliation reports about a task and what resume will accept
// have to agree.
//
// Found by the maintainer while dogfooding: a jobharbor-dev task's devcontainer
// exited 137, the pass said "resume the task to start it again", and the resume
// answered "it is running in a terminal that is still there. Attach to it
// instead". The only way out was killing the task's tmux window by hand on
// Feat's own socket.
func TestATaskWhoseContainerDiedCanBeResumed(t *testing.T) {
	arranged := arrangeDrafting(t)
	task := arranged.launched(t)
	arranged.recordProviderSession(t, task)

	arranged.docker.Answer(observePS, psExited)
	report := reconciled(t, arranged.service)

	// The finding tells the user to resume.
	var offered string
	for _, finding := range findings(report, reconcile.ClassAgentContainers) {
		if strings.Contains(finding.Action, "resume") {
			offered = finding.Action
		}
	}
	if offered == "" {
		t.Fatalf("no finding offered a resume; findings: %v", findings(report, reconcile.ClassAgentContainers))
	}

	// The container comes back when it is asked to, which is what a real
	// `docker compose up` does and what the fake has to be told to do.
	arranged.docker.Before("up --detach "+fixtureService, func() {
		arranged.docker.Answer(observePS, psRunning)
	})

	if _, err := arranged.service.Resume(context.Background(), task.ID); err != nil {
		t.Fatalf("Resume after the container died: %v\nthe pass had offered: %s", err, offered)
	}
	if after := arranged.reload(t, task.ID); !after.Session.Execution.Running {
		t.Error("the resumed task's container is not recorded as running")
	}
}

// TestResumingIsRefusedWhileTheContainerIsRunning keeps the protection the
// change above had to move without removing.
//
// A resume that ran beside a live agent would be two agents in one worktree, so
// widening what counts as "there is nothing to attach to" must not widen it to a
// session that is genuinely working.
func TestResumingIsRefusedWhileTheContainerIsRunning(t *testing.T) {
	arranged := arrangeDrafting(t)
	task := arranged.launched(t)
	arranged.recordProviderSession(t, task)

	_, err := arranged.service.Resume(context.Background(), task.ID)
	if err == nil {
		t.Fatal("a live session was resumed")
	}
	if !strings.Contains(err.Error(), "Attach") {
		t.Errorf("error = %v, want it to name what the user probably wants instead", err)
	}
}

// TestStoppingKeepsEverythingTheTaskOwns is the stop half of the lifecycle.
//
// What it asserts is mostly absence: a stop is not a small cleanup, and the
// resources a resume needs — and the work itself — have to survive it.
func TestStoppingKeepsEverythingTheTaskOwns(t *testing.T) {
	arranged := arrangeDrafting(t)
	task := arranged.launched(t)

	before := arranged.reload(t, task.ID)
	worktrees := len(before.Repositories)
	control := before.Session.ControlPath
	window := before.Session.Tmux.Window
	if err := before.SetAttention(domain.AttentionNeedsInput, arranged.now); err != nil {
		t.Fatalf("arranging an attention state: %v", err)
	}
	if err := arranged.service.store.Tasks().Save(context.Background(), before); err != nil {
		t.Fatalf("saving the attention state: %v", err)
	}

	arranged.docker.Before("stop", func() {
		arranged.docker.Answer(observePS, psStopped)
	})

	stoppedTask, err := arranged.service.Stop(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}

	if stoppedTask.Session.Process != domain.ProcessStopped {
		t.Errorf("the process is %s after a stop, want %s", stoppedTask.Session.Process, domain.ProcessStopped)
	}
	if stoppedTask.Session.Execution.Running {
		t.Error("the execution record still claims a running container after a stop")
	}
	// A task with no agent is not waiting for its user.
	if stoppedTask.Attention != domain.AttentionNone {
		t.Errorf("attention is %s after a stop, want %s", stoppedTask.Attention, domain.AttentionNone)
	}

	after := arranged.reload(t, task.ID)
	if len(after.Repositories) != worktrees {
		t.Errorf("the task holds %d repositories after a stop, want %d", len(after.Repositories), worktrees)
	}
	if after.Session.ControlPath != control {
		t.Errorf("the control workspace moved from %q to %q", control, after.Session.ControlPath)
	}
	if after.Session.Tmux.Window != window {
		t.Errorf("the tmux window moved from %q to %q", window, after.Session.Tmux.Window)
	}
	if after.Workflow != before.Workflow {
		t.Errorf("the workflow moved from %s to %s; a stop is not a workflow act",
			before.Workflow, after.Workflow)
	}

	// Nothing was removed. `down` takes the containers and the network with it,
	// and a stop that reached for it would be a cleanup nobody confirmed.
	for _, call := range arranged.docker.Calls() {
		if strings.Contains(call, "down") {
			t.Errorf("a stop ran %q", call)
		}
	}
	// And nothing was said. A stop is something the user just did.
	if sent := arranged.notifier.sent(); len(sent) != 0 {
		t.Errorf("a stop notified the user: %v", sent)
	}
}

// TestStoppingIsRefusedForAHostNativeAgent names what Feat does not own.
//
// The agent of a host-native task is a process in a pane rather than a
// container, and a verb that silently did nothing would be worse than one that
// says so.
func TestStoppingIsRefusedForAHostNativeAgent(t *testing.T) {
	live := launch(t, hostFixture, installed(), false)

	_, err := live.service.Stop(context.Background(), live.ref.Task)
	if err == nil {
		t.Fatal("a host-native agent was stopped")
	}
	if !strings.Contains(err.Error(), "runs on this machine") {
		t.Errorf("error = %v, want it to say where the agent actually runs", err)
	}
}

// TestStoppingATaskWithNoSessionIsRefused covers the other record a stop cannot
// act on.
func TestStoppingATaskWithNoSessionIsRefused(t *testing.T) {
	arranged := arrangeDrafting(t)
	draft := arranged.draft(t, "Not launched yet")

	_, err := arranged.service.Stop(context.Background(), draft.ID)
	if err == nil {
		t.Fatal("a task with no session was stopped")
	}
	if !strings.Contains(err.Error(), "no agent session") {
		t.Errorf("error = %v, want it to say nothing was ever launched", err)
	}
}

// TestStoppingAnAlreadyStoppedAgentSucceeds is the rule cleanup follows for a
// resource that is already gone: a user asking for a state the machine is
// already in should be told they have it.
func TestStoppingAnAlreadyStoppedAgentSucceeds(t *testing.T) {
	arranged := arrangeDrafting(t)
	task := arranged.launched(t)
	arranged.docker.Answer(observePS, psStopped)

	if _, err := arranged.service.Stop(context.Background(), task.ID); err != nil {
		t.Fatalf("first Stop: %v", err)
	}
	if _, err := arranged.service.Stop(context.Background(), task.ID); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
}

// TestAStoppedAgentIsBroughtBackByAResume is the pair, round trip.
//
// It is the whole user-facing lifecycle in one test: an agent environment sleeps
// on a stop and comes back on a resume, and there is no third verb in between.
func TestAStoppedAgentIsBroughtBackByAResume(t *testing.T) {
	arranged := arrangeDrafting(t)
	task := arranged.launched(t)
	arranged.recordProviderSession(t, task)

	arranged.docker.Before("stop", func() {
		arranged.docker.Answer(observePS, psStopped)
	})
	if _, err := arranged.service.Stop(context.Background(), task.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	arranged.docker.Before("up --detach "+fixtureService, func() {
		arranged.docker.Answer(observePS, psRunning)
	})
	resumed, err := arranged.service.Resume(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("Resume after a stop: %v", err)
	}
	if !resumed.Session.Execution.Running {
		t.Error("the resumed task's container is not recorded as running")
	}

	// The same session rather than a new one, which is what makes this a resume.
	launches := arranged.tmux.Launches()
	if len(launches) == 0 {
		t.Fatal("resuming started no program")
	}
	if arguments := strings.Join(launches[len(launches)-1].Command, " "); !strings.Contains(arguments, "--resume") {
		t.Errorf("the restarted terminal runs %q, want it to continue the recorded session", arguments)
	}
}
