package daemon

import (
	"context"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/agent"
	"github.com/ma8el/feat/internal/agent/claude"
	"github.com/ma8el/feat/internal/control"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/tmux/tmuxtest"
)

// dead arranges a launched host-execution task whose agent recorded a provider
// session and then died, which is the state a resume is offered for.
//
// It is a real launch rather than a hand-written record, because what a resume
// has to produce is a second launch: a fixture that skipped the first would not
// exercise the path that differs.
func dead(t *testing.T, sessionID string) *session {
	t.Helper()

	live := launch(t, hostFixture, installed(), false)

	task, err := live.service.store.Tasks().Load(context.Background(), live.ref)
	if err != nil {
		t.Fatalf("loading the launched task: %v", err)
	}
	if task.Session == nil {
		t.Fatal("the launched task has no session")
	}
	task.Session.ProviderSessionID = sessionID
	if err := task.Session.Observe(domain.ProcessFailed, reconcileTime); err != nil {
		t.Fatalf("recording the failed process: %v", err)
	}
	if err := live.service.store.Tasks().Save(context.Background(), task); err != nil {
		t.Fatalf("saving the failed session: %v", err)
	}
	if err := live.service.transition(context.Background(), task, domain.WorkflowFailed,
		"the agent process exited with status 1"); err != nil {
		t.Fatalf("failing the task: %v", err)
	}
	return live
}

// TestAFailedSessionIsResumedOnlyByExplicitUserAction is slice 12's third
// acceptance criterion, first half.
//
// It is checked by counting the commands every automatic path produces: that
// reconciliation ran no tmux command at all is a stronger statement than that a
// task happens to still be failed.
func TestAFailedSessionIsResumedOnlyByExplicitUserAction(t *testing.T) {
	live := dead(t, "e3f1a0c2-0000-4000-8000-1234567890ab")

	// The computer restarted: the terminal is gone with the tmux server, and
	// the recorded session is all that is left. This is the case a user actually
	// meets, and the one where a recovery pass could most easily overreach.
	empty := tmuxtest.New()
	restarted, _ := withTmux(t, live.preparation, empty)
	report := reconciled(t, restarted)

	for _, call := range empty.Calls() {
		for _, command := range []string{"new-session", "new-window", "respawn-pane"} {
			if len(call) > 0 && call[0] == command {
				t.Errorf("reconciliation ran %q; a dead session is reported, never restarted", command)
			}
		}
	}
	task := live.task(t)
	if task.Workflow != domain.WorkflowFailed {
		t.Errorf("workflow = %q after reconciliation, want it left failed", task.Workflow)
	}
	if task.Session.Process == domain.ProcessRunning {
		t.Error("reconciliation reported a process that is not there as running")
	}

	// What reconciliation does instead is offer the action, in words.
	var offered bool
	for _, finding := range report.Findings {
		if strings.Contains(finding.Action, "resume") {
			offered = true
		}
	}
	if !offered {
		t.Errorf("a dead session was reported without offering the recovery that exists for it: %+v",
			report.Findings)
	}
}

// TestResumingContinuesTheRecordedProviderSession is the criterion's second
// half: a resume that opened a new session would have lost the task's history
// and would look identical from the outside.
//
// It is checked on the argument vector the terminal was given, because that is
// where the difference is: a new session and a continued one differ by two
// elements of a command line and by nothing else Feat can observe.
func TestResumingContinuesTheRecordedProviderSession(t *testing.T) {
	const sessionID = "e3f1a0c2-0000-4000-8000-1234567890ab"
	live := dead(t, sessionID)

	if _, err := live.service.Resume(context.Background(), live.ref.Task); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	launches := live.tmux.Launches()
	if len(launches) < 2 {
		t.Fatalf("resuming produced %d launches, want the original and the resumed one", len(launches))
	}
	arguments := strings.Join(launches[len(launches)-1].Command, " ")
	if !strings.Contains(arguments, "--resume "+sessionID) {
		t.Errorf("the resumed launch is %q, want it to continue session %s", arguments, sessionID)
	}
	// A resumed session already holds the conversation, so it is given no
	// prompt. One invented here would be Feat putting words in the user's mouth.
	if strings.Contains(arguments, "Read the task brief") {
		t.Errorf("the resumed launch carries an initial prompt: %q", arguments)
	}

	// The original launch did carry a prompt, so the absence above is this
	// launch's rather than a fixture that never had one.
	if !strings.Contains(strings.Join(launches[0].Command, " "), "--settings") {
		t.Fatalf("the original launch is not a Claude launch: %v", launches[0].Command)
	}
	if strings.Contains(strings.Join(launches[0].Command, " "), "--resume") {
		t.Error("the original launch already carried --resume")
	}

	// The task goes back through preparing, so it stops claiming a state it is
	// not in while the agent starts.
	task := live.task(t)
	if task.Workflow != domain.WorkflowPreparing {
		t.Errorf("workflow = %q after resuming, want preparing until the agent reports starting", task.Workflow)
	}
}

// TestAContinuedSessionStartCompletesAResume is the narrowing ADR-037 makes to
// ADR-032's suppression.
//
// A session start that resumed is the same session carrying on, so it must not
// move a task that is already working — otherwise /clear would. It must move a
// task in preparing, because that task has just been resumed and is waiting for
// exactly this event. The wider rule left a resumed task in preparing with a
// running agent, looking broken.
func TestAContinuedSessionStartCompletesAResume(t *testing.T) {
	live := dead(t, "e3f1a0c2-0000-4000-8000-1234567890ab")
	service := live.service

	task := live.task(t)
	if err := service.transition(context.Background(), task, domain.WorkflowPreparing,
		"resuming at the user's request"); err != nil {
		t.Fatalf("returning the task to preparing: %v", err)
	}

	resumed := agent.Event{
		Kind:       agent.KindSessionStarted,
		Continued:  true,
		OccurredAt: reconcileTime,
	}
	if err := service.applyAgentEvent(context.Background(), task, resumed); err != nil {
		t.Fatalf("applying the resumed session start: %v", err)
	}
	if task.Workflow != domain.WorkflowWorking {
		t.Errorf("workflow = %q after a resumed session started, want working", task.Workflow)
	}

	// And the rule it narrows still holds: the same event on a working task
	// changes nothing, so a user typing /clear does not move their task.
	if err := service.applyAgentEvent(context.Background(), task, resumed); err != nil {
		t.Fatalf("applying a second resumed session start: %v", err)
	}
	if task.Workflow != domain.WorkflowWorking {
		t.Errorf("workflow = %q, want a continued session start on a working task to change nothing", task.Workflow)
	}
}

// TestResumingATaskWhoseWorkflowNeverMoved is the defect a real task produced.
//
// A process that dies while no daemon is watching leaves the workflow where it
// was: reconciliation reports the dead process and does not move it, because
// reporting instead of repairing is the whole rule. So the ordinary state of a
// task whose container was killed overnight is `working` with a failed process —
// and the first version of the resume transitioned unconditionally to
// `preparing`, which `working` has no edge to. The one task that most needed
// recovery was the one that could not have it.
func TestResumingATaskWhoseWorkflowNeverMoved(t *testing.T) {
	live := launch(t, hostFixture, installed(), false)
	live.start(t)

	task := live.task(t)
	if task.Workflow != domain.WorkflowWorking {
		t.Fatalf("workflow = %q, want a working task to start from", task.Workflow)
	}
	// The agent died and nothing was watching, so only the process moved.
	task.Session.ProviderSessionID = "e3f1a0c2-0000-4000-8000-1234567890ab"
	if err := task.Session.Observe(domain.ProcessFailed, reconcileTime); err != nil {
		t.Fatalf("recording the failed process: %v", err)
	}
	if err := live.service.store.Tasks().Save(context.Background(), task); err != nil {
		t.Fatalf("saving: %v", err)
	}

	resumed, err := live.service.Resume(context.Background(), live.ref.Task)
	if err != nil {
		t.Fatalf("a working task with a dead agent could not be resumed: %v", err)
	}
	if resumed.Workflow != domain.WorkflowWorking {
		t.Errorf("workflow = %q, want it left working: the work did not change", resumed.Workflow)
	}

	launches := live.tmux.Launches()
	if len(launches) < 2 {
		t.Fatalf("resuming produced %d launches, want the original and the resumed one", len(launches))
	}
	if !strings.Contains(strings.Join(launches[len(launches)-1].Command, " "), "--resume ") {
		t.Errorf("the resumed launch does not continue the recorded session: %v", launches[len(launches)-1].Command)
	}
}

// TestResumingIsRefusedWithoutARecordedSession keeps the offer honest.
//
// A task whose agent never reported starting has no provider session to
// continue, and resuming it would open an empty session that looked like the old
// one — the failure shape ADR-032's evidence 4 describes.
func TestResumingIsRefusedWithoutARecordedSession(t *testing.T) {
	live := dead(t, "")

	before := len(live.tmux.Calls())
	_, err := live.service.Resume(context.Background(), live.ref.Task)
	if err == nil {
		t.Fatal("a task with no recorded provider session was resumed into an empty one")
	}
	if !strings.Contains(err.Error(), "empty session") {
		t.Errorf("error = %v, want it to say what resuming would actually do", err)
	}
	for _, call := range live.tmux.Calls()[before:] {
		if len(call) > 0 && call[0] == "respawn-pane" {
			t.Error("a refused resume still started a program")
		}
	}
}

// TestResumingATerminalKilledFromTmux is the state a user produces with one
// tmux command, and the one the refusal used to be wrong about.
//
// Nothing watches tmux continuously, so a window killed from inside it leaves
// the recorded process saying running until a reconciliation pass or the
// provider's own end-of-session hook says otherwise. Deciding from that record
// answered "attach to it instead" for a terminal there was nothing to attach
// to, and the task with the most need of recovery was the one that could not
// have it. Reported by the maintainer after :kill-window on a live task.
func TestResumingATerminalKilledFromTmux(t *testing.T) {
	live := launch(t, hostFixture, installed(), false)
	live.start(t)

	task := live.task(t)
	if !task.Session.Process.Alive() {
		t.Fatalf("the launched session is %s, want a record claiming a live process", task.Session.Process)
	}
	// Killed the way a user kills it: through tmux, with nothing told about it.
	if _, err := live.tmux.Run(context.Background(), task.Session.Tmux.Socket,
		"kill-window", "-t", task.Session.Tmux.Window); err != nil {
		t.Fatalf("killing the task's window: %v", err)
	}

	before := len(live.tmux.Launches())
	if _, err := live.service.Resume(context.Background(), live.ref.Task); err != nil {
		t.Fatalf("Resume after the window was killed: %v", err)
	}

	// A terminal exists again, and it continues the recorded session rather than
	// opening an empty one beside the work that was done in it.
	if _, found, err := live.service.terminals.Find(
		context.Background(), task.ProjectID, task.ID); err != nil || !found {
		t.Fatalf("Find after resuming = %v, %v; want the rebuilt terminal", found, err)
	}
	launches := live.tmux.Launches()
	if len(launches) <= before {
		t.Fatal("resuming started no program")
	}
	if arguments := strings.Join(launches[len(launches)-1].Command, " "); !strings.Contains(arguments, "--resume") {
		t.Errorf("the rebuilt terminal runs %q, want it to continue the recorded session", arguments)
	}

	// And the record that disagreed with the machine was corrected on the way,
	// so the history says why a resume was needed rather than only that one
	// happened.
	var corrected bool
	for _, event := range events(t, live.service, live.preparation) {
		if event.Type == domain.EventReconciled && strings.Contains(event.Detail, "was not found") {
			corrected = true
		}
	}
	if !corrected {
		t.Error("the record claimed a live process and was never corrected")
	}
}

// TestResumingIsRefusedWhileTheSessionIsAlive stops a resume from becoming a
// second agent in one task's terminal.
func TestResumingIsRefusedWhileTheSessionIsAlive(t *testing.T) {
	live := launch(t, hostFixture, installed(), false)

	task := live.task(t)
	task.Session.ProviderSessionID = "e3f1a0c2-0000-4000-8000-1234567890ab"
	if err := task.Session.Observe(domain.ProcessRunning, reconcileTime); err != nil {
		t.Fatalf("recording a running process: %v", err)
	}
	if err := live.service.store.Tasks().Save(context.Background(), task); err != nil {
		t.Fatalf("saving the running session: %v", err)
	}

	_, err := live.service.Resume(context.Background(), live.ref.Task)
	if err == nil {
		t.Fatal("a live session was resumed")
	}
	if !strings.Contains(err.Error(), "Attach") {
		t.Errorf("error = %v, want it to name what the user probably wants instead", err)
	}
}

// TestAResumeIdentifierIsCheckedBeforeItReachesTheCLI is the rule that a value
// an agent could have authored is not passed through unexamined.
//
// The provider session identifier comes from a control message, and control
// messages are validated before anything acts on them (docs/05-security-model.md).
func TestAResumeIdentifierIsCheckedBeforeItReachesTheCLI(t *testing.T) {
	adapter := claude.New()
	workspace, err := control.Open(t.TempDir(), "app", domain.NewTaskID(), control.Options{})
	if err != nil {
		t.Fatalf("opening a control workspace: %v", err)
	}
	if err := workspace.Create(); err != nil {
		t.Fatalf("creating the control workspace: %v", err)
	}

	task, err := domain.NewTask(domain.NewTaskID(), "app", "Add a rate limit",
		domain.TaskSource{Kind: domain.SourcePrompt}, reconcileTime)
	if err != nil {
		t.Fatalf("creating a task: %v", err)
	}
	if err := task.SetBrief("Add a rate limit.", reconcileTime); err != nil {
		t.Fatalf("setting the brief: %v", err)
	}

	request := agent.PrepareRequest{
		Task:      task,
		Workspace: agent.Workspace{WorkingDirectory: "/work/api", ControlPath: workspace.Root()},
		Control:   workspace,
		Environment: agent.Environment{
			Mode: domain.ExecutionHost, Runner: refusingRunner{},
		},
	}

	for _, id := range []string{
		"--dangerously-skip-permissions",
		"-r",
		"a b",
		"a\nb",
		strings.Repeat("a", 200),
	} {
		request.Resume = id
		if _, err := adapter.Prepare(context.Background(), request); err == nil {
			t.Errorf("the session identifier %q was passed to the CLI unchecked", id)
		}
	}

	// A real one is accepted.
	request.Resume = "e3f1a0c2-0000-4000-8000-1234567890ab"
	spec, err := adapter.Prepare(context.Background(), request)
	if err != nil {
		t.Fatalf("a valid session identifier was refused: %v", err)
	}
	if !strings.Contains(strings.Join(spec.Arguments, " "), "--resume "+request.Resume) {
		t.Errorf("arguments = %v, want the recorded session", spec.Arguments)
	}
}

// refusingRunner answers no probe. Prepare asks none, so it is never used; it
// exists because an environment requires one.
type refusingRunner struct{}

func (refusingRunner) Run(context.Context, agent.Command) (agent.Output, error) {
	return agent.Output{}, context.Canceled
}
