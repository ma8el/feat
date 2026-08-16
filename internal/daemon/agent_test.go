package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ma8el/feat/internal/agent"
	"github.com/ma8el/feat/internal/agent/agenttest"
	"github.com/ma8el/feat/internal/agent/claude"
	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/config"
	"github.com/ma8el/feat/internal/control"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/git"
	"github.com/ma8el/feat/internal/store"
	"github.com/ma8el/feat/internal/tmux/tmuxtest"
)

// hostFixture is a project that runs its agent on the host, which is the mode
// slice 7 can launch without the execution environment slice 8 delivers.
const hostFixture = `version: 1

project:
  id: app
  name: Example Application
  primary_repository: api

repositories:
  api:
    host_path: ~/repos/app/api
    default_access: read_write
  store:
    host_path: ~/repos/app/store
    default_access: read_only

git:
  base_policy: remote
  branch_template: "feat/{task_key}-{slug}"

agent:
  execution:
    mode: host
  claude:
    idle_grace_period: 5s
`

// testTimer is a Timer whose scheduled work runs when a test says so.
//
// The idle grace period is an acceptance criterion, and a test that proved it by
// sleeping would prove only that the machine was slow enough.
type testTimer struct {
	mu      sync.Mutex
	pending map[int]*scheduled
	next    int
}

type scheduled struct {
	after   time.Duration
	run     func()
	stopped bool
}

func newTestTimer() *testTimer { return &testTimer{pending: make(map[int]*scheduled)} }

func (t *testTimer) After(d time.Duration, run func()) func() {
	t.mu.Lock()
	defer t.mu.Unlock()

	id := t.next
	t.next++
	t.pending[id] = &scheduled{after: d, run: run}
	return func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		if entry, ok := t.pending[id]; ok {
			entry.stopped = true
		}
	}
}

// armed returns the delay of the one pending transition, if there is one.
func (t *testTimer) armed() (time.Duration, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	for _, entry := range t.pending {
		if !entry.stopped {
			return entry.after, true
		}
	}
	return 0, false
}

// fire runs every pending transition that has not been stopped.
func (t *testTimer) fire() {
	t.mu.Lock()
	var due []func()
	for id, entry := range t.pending {
		if !entry.stopped {
			due = append(due, entry.run)
		}
		delete(t.pending, id)
	}
	t.mu.Unlock()

	for _, run := range due {
		run()
	}
}

// selectDraftRepositories gives an arranged draft the repository selection a
// user would have made on the preparation screen.
func selectDraftRepositories(t *testing.T, service *service, arranged *preparation) error {
	t.Helper()

	_, err := service.UpdateDraft(context.Background(), arranged.ref.Task, api.DraftUpdate{
		Title: "Add a rate limit",
		Brief: "Add a rate limit to the public API.",
		Repositories: []api.DraftSelection{
			{Repository: "api", Access: domain.TaskAccessReadWrite},
			{Repository: "store", Access: domain.TaskAccessReadOnly},
		},
	})
	return err
}

// session is a launched task, its daemon, and the fakes behind both.
type session struct {
	*preparation
	service  *service
	timer    *testTimer
	runner   *agenttest.Runner
	tmux     *tmuxtest.Server
	notifier *fakeNotifier
}

// installed is a runner describing a machine with Claude and an authenticated
// glab on it.
func installed() *agenttest.Runner {
	return agenttest.New().
		Succeed(claude.Verified()+" (Claude Code)", "claude", "--version").
		Succeed("Logged in", "glab", "auth", "status").
		Succeed("Logged in", "gh", "auth", "status")
}

// launch arranges a project, confirms its draft, and launches it.
func launch(t *testing.T, fixture string, runner *agenttest.Runner, hostAgent bool) *session {
	t.Helper()
	return launchWith(t, fixture, runner, hostAgent, nil)
}

// launchWith is launch for a test that needs to change the daemon's options.
//
// Every launch installs a fake notifier, whatever else it changes. A test must
// never reach the real desktop: a suite that showed a notification for every
// task it launched would be a suite nobody could run twice.
func launchWith(
	t *testing.T, fixture string, runner *agenttest.Runner, hostAgent bool, adjust func(*Options),
) *session {
	t.Helper()

	arranged := arrangeTaskWith(t, newFakeGit(), fixture)
	server := tmuxtest.New()
	timer := newTestTimer()
	notifier := newFakeNotifier()

	env := arranged.env
	if hostAgent {
		env.Getenv = func(name string) string {
			if name == config.EnvHostAgent {
				return "1"
			}
			return ""
		}
	}

	options := Options{
		Layout:      arranged.service.layout,
		Environment: env,
		Build:       testBuild,
		Git:         arranged.fake,
		Tmux:        server,
		Agent:       runner,
		Docker:      workingDocker(),
		Notifier:    notifier,
		Timer:       timer,
		Now:         func() time.Time { return reconcileTime },
	}
	if adjust != nil {
		adjust(&options)
	}

	instance, err := New(options)
	if err != nil {
		t.Fatalf("creating the daemon: %v", err)
	}
	service := instance.service
	// What Serve does on the way out, for a test that never called it. A review
	// request starts a gate in the background, and a goroutine still writing a
	// task's control workspace while the testing package removes its temporary
	// directory fails the test that started it — which is how this was found, on
	// Linux, in three tests that were only ever about notifications and
	// resources. Registered after the temporary directory exists, so cleanup
	// order runs this first.
	t.Cleanup(service.gate.stopAll)

	if err := selectDraftRepositories(t, service, arranged); err != nil {
		t.Fatalf("selecting the draft's repositories: %v", err)
	}
	if _, err := service.planDraft(context.Background(), arranged.ref); err != nil {
		t.Fatalf("planning the draft: %v", err)
	}
	task, err := service.store.Tasks().Load(context.Background(), arranged.ref)
	if err != nil {
		t.Fatalf("loading the planned draft: %v", err)
	}
	if _, err := service.LaunchDraft(context.Background(), task.ID, Fingerprint(task)); err != nil {
		t.Fatalf("launching the draft: %v", err)
	}

	return &session{
		preparation: arranged, service: service, timer: timer,
		runner: runner, tmux: server, notifier: notifier,
	}
}

// workspace returns the launched task's control workspace.
func (s *session) workspace(t *testing.T) *control.Workspace {
	t.Helper()

	task, err := s.service.store.Tasks().Load(context.Background(), s.ref)
	if err != nil {
		t.Fatalf("loading the task: %v", err)
	}
	workspace, err := s.service.controlWorkspace(task)
	if err != nil {
		t.Fatalf("opening the control workspace: %v", err)
	}
	return workspace
}

// emit writes a message into the outbox the way a hook or the helper would, and
// delivers it.
func (s *session) emit(t *testing.T, kind control.MessageType, payload string) {
	t.Helper()

	s.write(t, kind, payload)
	s.deliver(t)
}

// write puts a message in the outbox without delivering it.
func (s *session) write(t *testing.T, kind control.MessageType, payload string) string {
	t.Helper()

	workspace := s.workspace(t)
	id := "feat-" + domain.NewTaskID().Key().String()
	document := map[string]any{
		"schema_version": control.SchemaVersion,
		"id":             id,
		"task_id":        s.ref.Task.String(),
		"type":           string(kind),
		"occurred_at":    reconcileTime.Format(time.RFC3339),
		"payload":        json.RawMessage(payload),
	}
	body, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("building a control message: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace.OutboxDir(), id+".json"), body, 0o600); err != nil {
		t.Fatalf("writing a control message: %v", err)
	}
	return id
}

// hook writes what a generated hook would have written.
func (s *session) hook(t *testing.T, name, event string) {
	t.Helper()
	s.emit(t, control.TypeProviderEvent, `{"hook":"`+name+`","event":`+event+`}`)
}

// deliver reads and applies whatever the outbox holds.
func (s *session) deliver(t *testing.T) {
	t.Helper()

	task, err := s.service.store.Tasks().Load(context.Background(), s.ref)
	if err != nil {
		t.Fatalf("loading the task: %v", err)
	}
	if err := s.service.deliverControl(context.Background(), task); err != nil {
		t.Fatalf("delivering control messages: %v", err)
	}
}

// task returns the task as recorded on disk.
func (s *session) task(t *testing.T) *domain.Task {
	t.Helper()
	return s.reload(t)
}

// start takes the launched task to working, the way a real session start does.
func (s *session) start(t *testing.T) {
	t.Helper()
	s.hook(t, "SessionStart", `{"session_id":"claude-session-1","source":"startup"}`)
}

// TestEveryAgentLaunchTurnsOffAutostash covers both execution modes, because a
// guard that holds in one of them is a guard the user cannot rely on.
//
// A task's worktrees share one Git directory with the user's checkout and with
// every other task, and the stash is one stack across all of them. An agent can
// be told not to stash; `rebase.autoStash` and `merge.autoStash` are read from
// the shared configuration, which is the user's, so a session obeying the
// instruction perfectly would still stash onto that stack from a rebase. The
// settings travel as environment rather than as a configuration write, so
// nothing outside the session's own processes changes (ADR-056).
func TestEveryAgentLaunchTurnsOffAutostash(t *testing.T) {
	t.Run("host", func(t *testing.T) {
		live := launch(t, hostFixture, installed(), false)

		command := agentCommand(t, live.tmux.Calls(), claude.Executable)
		for _, entry := range git.WorktreeEnvironment() {
			if !slices.Contains(command, entry) {
				t.Errorf("the host launch does not carry %s: %v", entry, command)
			}
		}
	})

	t.Run("devcontainer", func(t *testing.T) {
		arranged := arrangeDrafting(t)
		arranged.launched(t)

		// The agent runs through `docker compose exec`, which takes each entry
		// as the value of its own --env flag.
		command := agentCommand(t, arranged.tmux.Calls(), claude.Executable)
		for _, entry := range git.WorktreeEnvironment() {
			if !slices.Contains(command, entry) {
				t.Errorf("the devcontainer launch does not carry %s: %v", entry, command)
			}
		}
	})
}

// TestAnAdapterCannotQuietlyReplaceFeatsGitSettings pins the conflict rule.
//
// A provider adapter is free to ask for its own environment, and nothing stops
// a future one asking for a Git setting. If it named one of Feat's, taking
// either value silently would decide, on nobody's behalf, whose work may be
// lost — so the launch stops and says which name is in dispute.
func TestAnAdapterCannotQuietlyReplaceFeatsGitSettings(t *testing.T) {
	values, err := agentVariables([]string{"CLAUDE_CODE_EXAMPLE=1"})
	if err != nil {
		t.Fatalf("an adapter's own variable was refused: %v", err)
	}
	if values["CLAUDE_CODE_EXAMPLE"] != "1" {
		t.Errorf("the adapter's variable did not survive: %v", values)
	}
	if values["GIT_CONFIG_COUNT"] == "" {
		t.Errorf("Feat's own settings were dropped: %v", values)
	}

	_, err = agentVariables([]string{"GIT_CONFIG_COUNT=9"})
	if err == nil {
		t.Fatal("an adapter replaced a setting Feat applies to every task worktree")
	}
	if !errors.Is(err, api.ErrInvalid) {
		t.Errorf("the conflict is reported as %v, want an invalid-request error", err)
	}
	if !strings.Contains(err.Error(), "GIT_CONFIG_COUNT") {
		t.Errorf("the error does not name the variable in dispute: %v", err)
	}
}

// agentCommand returns the tmux call that started the agent, whole.
//
// Launches() reports the program and its arguments, which is what most tests
// want and is exactly what this one cannot use: the environment reaches the
// pane as flags before the program, so the assertion has to see the call as it
// was sent.
func agentCommand(t *testing.T, calls [][]string, program string) []string {
	t.Helper()
	for _, call := range calls {
		if len(call) > 0 && call[0] == "respawn-pane" && slices.Contains(call, program) {
			return call
		}
	}
	t.Fatalf("no tmux call started %s: %v", program, calls)
	return nil
}

// TestClaudeLaunchesInTheTaskWorkingDirectoryWithTheFinalBrief is the first
// slice 7 acceptance criterion.
func TestClaudeLaunchesInTheTaskWorkingDirectoryWithTheFinalBrief(t *testing.T) {
	live := launch(t, hostFixture, installed(), false)

	// The pane runs Claude, in the task's own primary worktree, and not the
	// user's ordinary checkout.
	launched, ok := live.tmux.Launched()
	if !ok {
		t.Fatal("no program was launched in the agent pane")
	}
	if launched.Program() != claude.Executable {
		t.Errorf("the agent pane runs %v, want %s", launched.Command, claude.Executable)
	}
	worktree := live.task(t).Repositories[0].WorktreePath
	if launched.Directory != worktree {
		t.Errorf("the agent pane opened in %q, want the task worktree %q", launched.Directory, worktree)
	}

	// The brief the user confirmed is what the session starts from.
	workspace := live.workspace(t)
	brief, err := os.ReadFile(workspace.BriefPath())
	if err != nil {
		t.Fatalf("reading the written brief: %v", err)
	}
	if string(brief) != "Add a rate limit to the public API." {
		t.Errorf("brief = %q, want the confirmed brief", brief)
	}

	// Launching does not claim a running agent. The task reaches working only
	// when the session says it started, which is the edge ADR-031 reserved.
	task := live.task(t)
	if task.Workflow != domain.WorkflowPreparing {
		t.Errorf("workflow after launch = %q, want preparing until the agent reports it started", task.Workflow)
	}
	if task.Session == nil || task.Session.ControlPath != workspace.Root() {
		t.Errorf("session control path = %+v, want the control workspace", task.Session)
	}

	live.start(t)
	task = live.task(t)
	if task.Workflow != domain.WorkflowWorking {
		t.Errorf("workflow after the session started = %q, want working", task.Workflow)
	}
	if task.Session.ProviderSessionID != "claude-session-1" {
		t.Errorf("provider session id = %q, want the one the session reported", task.Session.ProviderSessionID)
	}
}

// TestANormalEndOfTurnBecomesIdleOnlyAfterTheGracePeriod is the second slice 7
// acceptance criterion.
func TestANormalEndOfTurnBecomesIdleOnlyAfterTheGracePeriod(t *testing.T) {
	live := launch(t, hostFixture, installed(), false)
	live.start(t)

	live.hook(t, "Stop", `{"session_id":"claude-session-1","stop_hook_active":false}`)

	// Nothing has happened yet. An end of turn arms the grace period and
	// changes nothing else: not the process state, not attention, and above all
	// not the workflow.
	task := live.task(t)
	if task.Session.Process != domain.ProcessRunning {
		t.Errorf("process immediately after a turn ended = %q, want running until the grace period passes",
			task.Session.Process)
	}
	if task.Workflow != domain.WorkflowWorking {
		t.Errorf("workflow after a turn ended = %q, want working", task.Workflow)
	}
	grace, armed := live.timer.armed()
	if !armed {
		t.Fatal("ending a turn armed no grace period")
	}
	if grace != 5*time.Second {
		t.Errorf("grace period = %s, want the configured 5s", grace)
	}

	live.timer.fire()

	task = live.task(t)
	if task.Session.Process != domain.ProcessIdle {
		t.Errorf("process after the grace period = %q, want idle", task.Session.Process)
	}
	// Idle is conservative: Feat cannot tell a finished turn from a question.
	if task.Attention != domain.AttentionPossiblyWaiting {
		t.Errorf("attention after the grace period = %q, want possibly_waiting", task.Attention)
	}
	if task.Workflow != domain.WorkflowWorking {
		t.Errorf("workflow after the grace period = %q, want working: idle never means complete", task.Workflow)
	}
}

func TestActivityWithinTheGracePeriodCancelsIdle(t *testing.T) {
	live := launch(t, hostFixture, installed(), false)
	live.start(t)

	live.hook(t, "Stop", `{"session_id":"claude-session-1"}`)
	if _, armed := live.timer.armed(); !armed {
		t.Fatal("ending a turn armed no grace period")
	}

	// A turn that ends and immediately continues is not a session waiting for
	// anybody, which is the whole reason FR-AGENT-007 asks for a delay.
	live.hook(t, "UserPromptSubmit", `{"session_id":"claude-session-1"}`)
	if _, armed := live.timer.armed(); armed {
		t.Error("a prompt during the grace period left the idle transition pending")
	}

	live.timer.fire()
	if task := live.task(t); task.Session.Process != domain.ProcessRunning {
		t.Errorf("process = %q, want running: the session did not go quiet", task.Session.Process)
	}
}

// TestASubmittedRevisionPromptChangesReviewStateConservatively is the third
// slice 7 acceptance criterion.
func TestASubmittedRevisionPromptChangesReviewStateConservatively(t *testing.T) {
	live := launch(t, hostFixture, installed(), false)
	live.start(t)

	live.emit(t, control.TypeReviewRequested, `{"summary":"Ready for review."}`)
	if task := live.task(t); task.Workflow != domain.WorkflowReviewRequested {
		t.Fatalf("workflow = %q, want review_requested", task.Workflow)
	}

	live.hook(t, "UserPromptSubmit", `{"session_id":"claude-session-1"}`)

	task := live.task(t)
	// Back to working, and never forward: a prompt is the user saying something,
	// not the agent saying it is done again.
	if task.Workflow != domain.WorkflowWorking {
		t.Errorf("workflow after a revision prompt = %q, want working", task.Workflow)
	}
	if task.Attention != domain.AttentionNone {
		t.Errorf("attention after a revision prompt = %q, want none", task.Attention)
	}
	if task.Session.Process != domain.ProcessRunning {
		t.Errorf("process after a revision prompt = %q, want running", task.Session.Process)
	}
}

// TestDuplicateMalformedAndOutOfTaskMessagesDoNotTransition is the daemon half
// of the fourth slice 7 acceptance criterion. The protocol half, which proves
// each is refused, lives in internal/control.
func TestDuplicateMalformedAndOutOfTaskMessagesDoNotTransition(t *testing.T) {
	live := launch(t, hostFixture, installed(), false)
	live.start(t)

	before := live.task(t)
	workspace := live.workspace(t)
	outbox := workspace.OutboxDir()

	// One well-formed review request, delivered twice under two names: the
	// second must change nothing.
	id := "feat-duplicate"
	body := `{"schema_version":1,"id":"` + id + `","task_id":"` + live.ref.Task.String() +
		`","type":"review_requested","occurred_at":"2026-08-05T11:00:00Z","payload":{"summary":"Done."}}`
	if err := os.WriteFile(filepath.Join(outbox, "first.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("writing the first copy: %v", err)
	}
	live.deliver(t)

	requested := live.task(t)
	if requested.Workflow != domain.WorkflowReviewRequested {
		t.Fatalf("workflow = %q, want review_requested", requested.Workflow)
	}

	// A second copy of the same event, and a pile of documents that are wrong in
	// every way the protocol names.
	for name, document := range map[string]string{
		"duplicate.json": body,
		"foreign.json": `{"schema_version":1,"id":"other-task","task_id":"` + domain.NewTaskID().String() +
			`","type":"review_requested","occurred_at":"2026-08-05T11:00:00Z","payload":{}}`,
		"version.json": `{"schema_version":99,"id":"bad-version","task_id":"` + live.ref.Task.String() +
			`","type":"review_requested","occurred_at":"2026-08-05T11:00:00Z","payload":{}}`,
		"type.json": `{"schema_version":1,"id":"bad-type","task_id":"` + live.ref.Task.String() +
			`","type":"approve_everything","occurred_at":"2026-08-05T11:00:00Z","payload":{}}`,
		"runtime.json": `{"schema_version":1,"id":"runtime","task_id":"` + live.ref.Task.String() +
			`","type":"runtime_requested","occurred_at":"2026-08-05T11:00:00Z","payload":{}}`,
		"truncated.json": `{"schema_version":1,"id":"trunc`,
	} {
		if err := os.WriteFile(filepath.Join(outbox, name), []byte(document), 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	// Past the grace that treats an unparseable document as a write in progress.
	live.service.now = func() time.Time { return reconcileTime.Add(time.Minute) }
	live.deliver(t)

	after := live.task(t)
	if after.Workflow != domain.WorkflowReviewRequested {
		t.Errorf("workflow = %q, want it unchanged at review_requested", after.Workflow)
	}
	if after.UpdatedAt != requested.UpdatedAt {
		t.Errorf("the task was written again by messages that should all have been refused")
	}
	if before.Workflow == after.Workflow {
		t.Fatal("the arrangement never transitioned, so this test would pass without the rule it checks")
	}

	// Exactly one review was recorded, by the one message that was valid.
	review, err := live.service.store.Reviews().Load(context.Background(), live.ref)
	if err != nil {
		t.Fatalf("loading the review: %v", err)
	}
	if review.CompletionSummary != "Done." {
		t.Errorf("completion summary = %q, want the one valid message's", review.CompletionSummary)
	}
}

// TestARefusedMessageIsSettledAndToldToTheUser covers what happens to a message
// the protocol refuses, which until now was nothing.
//
// Two things were wrong with that. The file stayed in the outbox and was
// re-read, re-refused, and re-logged on every poll — a quarter of a million log
// lines a day for one bad document — and the refusal reached nobody: Feat
// declining a capability the agent asked for was announced only to the log of a
// background process. A refusal is now recorded on the task, once, and never
// judged again.
func TestARefusedMessageIsSettledAndToldToTheUser(t *testing.T) {
	live := launch(t, hostFixture, installed(), false)
	live.start(t)

	// The capability no agent is granted, and a document claiming to be from
	// another task: one refused for what it asked for, one for what it is.
	live.write(t, control.TypeRuntimeRequested, `{"services":["db"]}`)
	workspace := live.workspace(t)
	foreign := `{"schema_version":1,"id":"from-elsewhere","task_id":"` + domain.NewTaskID().String() +
		`","type":"review_requested","occurred_at":"2026-08-05T11:00:00Z","payload":{}}`
	if err := os.WriteFile(filepath.Join(workspace.OutboxDir(), "foreign.json"), []byte(foreign), 0o600); err != nil {
		t.Fatalf("writing the foreign document: %v", err)
	}

	live.deliver(t)

	refusals := live.refusals(t)
	if len(refusals) != 2 {
		t.Fatalf("refusals recorded on the task = %d, want 2: %v", len(refusals), refusals)
	}
	var told bool
	for _, detail := range refusals {
		if strings.Contains(detail, "runtime_control") && strings.Contains(detail, "inert until the host") {
			told = true
		}
	}
	if !told {
		t.Errorf("refusals = %v, want one naming the capability Feat declined and why", refusals)
	}

	// Every further poll of the same outbox says nothing, because both entries
	// are settled — and they are still there, which is what makes the outbox the
	// account of what the agent sent.
	for range 3 {
		live.deliver(t)
	}
	if again := live.refusals(t); len(again) != len(refusals) {
		t.Errorf("refusals after three more polls = %d, want %d: a settled message was judged again",
			len(again), len(refusals))
	}
	entries, err := os.ReadDir(workspace.OutboxDir())
	if err != nil {
		t.Fatalf("reading the outbox: %v", err)
	}
	if len(entries) != 3 {
		t.Errorf("the outbox holds %d entries, want 3: the session start, the runtime request, and the "+
			"foreign document", len(entries))
	}
}

// refusals returns the detail of every control refusal recorded on the task.
func (s *session) refusals(t *testing.T) []string {
	t.Helper()

	log, err := s.service.store.Events().Replay(context.Background(), s.ref)
	if err != nil {
		t.Fatalf("replaying the task's events: %v", err)
	}
	var details []string
	for _, event := range log.Events {
		if event.Type == domain.EventControlRefused {
			details = append(details, event.Detail)
		}
	}
	return details
}

// TestReviewRequestIsExplicitAndDistinguishableFromIdle is the fifth slice 7
// acceptance criterion, and it is the shape a "Stop means complete" defect
// would take.
func TestReviewRequestIsExplicitAndDistinguishableFromIdle(t *testing.T) {
	live := launch(t, hostFixture, installed(), false)
	live.start(t)

	// Every end-of-turn signal Claude can produce, several times over, followed
	// by the grace period expiring. None of it may reach a review state.
	for range 3 {
		live.hook(t, "Stop", `{"session_id":"claude-session-1","stop_hook_active":false}`)
		live.timer.fire()
	}

	task := live.task(t)
	if task.Workflow != domain.WorkflowWorking {
		t.Fatalf("workflow after three ended turns = %q, want working (invariant 13)", task.Workflow)
	}
	if task.Session.Process != domain.ProcessIdle {
		t.Errorf("process = %q, want idle", task.Session.Process)
	}

	// Idle and review-requested are separately observable, which is what
	// "distinguishable" means: a client can tell the two apart without guessing.
	live.emit(t, control.TypeReviewRequested,
		`{"summary":"Rate limiting added.","checks":[{"id":"test","status":"passed","detail":"84 passed"}]}`)

	task = live.task(t)
	if task.Workflow != domain.WorkflowReviewRequested {
		t.Fatalf("workflow after an explicit request = %q, want review_requested", task.Workflow)
	}
	if task.Session.Process != domain.ProcessIdle {
		t.Errorf("process = %q, want idle: the workflow moved and the process did not", task.Session.Process)
	}

	// The verification the dashboard shows is the agent's claim, attributed.
	review, err := live.service.store.Reviews().Load(context.Background(), live.ref)
	if err != nil {
		t.Fatalf("loading the review: %v", err)
	}
	if len(review.Checks) != 1 {
		t.Fatalf("checks = %d, want 1", len(review.Checks))
	}
	if review.Checks[0].Reporter != domain.ReporterAgent {
		t.Errorf("reporter = %q, want %q: nothing enforced this result",
			review.Checks[0].Reporter, domain.ReporterAgent)
	}
	if review.Checks[0].Status != domain.CheckPassed {
		t.Errorf("status = %q, want passed", review.Checks[0].Status)
	}
}

// TestNoEndOfTurnPathReachesAReviewState pins the normalization table the way
// internal/domain pins the workflow transition table.
//
// The defect it is aimed at is not a wrong line of code but a wrong table entry,
// so the test reads the table rather than driving the daemon.
func TestNoEndOfTurnPathReachesAReviewState(t *testing.T) {
	semantic := map[domain.WorkflowState]bool{
		domain.WorkflowReviewRequested: true,
		domain.WorkflowVerifying:       true,
		domain.WorkflowReadyForReview:  true,
		domain.WorkflowApproved:        true,
	}

	for kind, change := range effects {
		if kind == agent.KindReviewRequested {
			// The one event allowed to reach a review state, and only because an
			// agent authored a message saying so.
			continue
		}
		if change.workflow == nil {
			continue
		}
		for _, from := range []domain.WorkflowState{
			domain.WorkflowPreparing, domain.WorkflowWorking, domain.WorkflowReviewRequested,
			domain.WorkflowReadyForReview, domain.WorkflowVerificationFailed,
			domain.WorkflowChangesRequested, domain.WorkflowFailed,
		} {
			if next := change.workflow(from); semantic[next] {
				t.Errorf("event %q takes a %s task to %s; semantic completion requires an explicit "+
					"review or completion event (FR-AGENT-008, invariant 13)", kind, from, next)
			}
		}
	}

	// And the positive half, so this test cannot pass by the table being empty.
	requested := effects[agent.KindReviewRequested]
	if requested.workflow == nil || requested.workflow(domain.WorkflowWorking) != domain.WorkflowReviewRequested {
		t.Error("an explicit review request does not reach review_requested")
	}
	if effects[agent.KindTurnEnded].workflow != nil {
		t.Error("the end of a turn has a workflow effect; it must have none at all")
	}
	if !effects[agent.KindTurnEnded].arms {
		t.Error("the end of a turn does not arm the idle grace period")
	}
}

// TestMissingRequiredGitLabAuthenticationPreventsLaunch is the sixth slice 7
// acceptance criterion.
func TestMissingRequiredGitLabAuthenticationPreventsLaunch(t *testing.T) {
	fixture := strings.Replace(hostFixture,
		"  claude:\n    idle_grace_period: 5s\n",
		"  claude:\n    idle_grace_period: 5s\n  capabilities:\n    gitlab_cli: required\n", 1)

	runner := agenttest.New().
		Succeed(claude.Verified()+" (Claude Code)", "claude", "--version").
		Fail(1, "not logged in", "glab", "auth", "status")

	arranged := arrangeTaskWith(t, newFakeGit(), fixture)
	server := tmuxtest.New()
	instance, err := New(Options{
		Layout:      arranged.service.layout,
		Environment: arranged.env,
		Build:       testBuild,
		Git:         arranged.fake,
		Tmux:        server,
		Agent:       runner,
		Now:         func() time.Time { return reconcileTime },
	})
	if err != nil {
		t.Fatalf("creating the daemon: %v", err)
	}
	service := instance.service

	if err := selectDraftRepositories(t, service, arranged); err != nil {
		t.Fatalf("selecting the draft's repositories: %v", err)
	}
	if _, err := service.planDraft(context.Background(), arranged.ref); err != nil {
		t.Fatalf("planning the draft: %v", err)
	}
	task, err := service.store.Tasks().Load(context.Background(), arranged.ref)
	if err != nil {
		t.Fatalf("loading the planned draft: %v", err)
	}

	_, err = service.LaunchDraft(context.Background(), task.ID, Fingerprint(task))
	if err == nil {
		t.Fatal("the launch succeeded with a required provider CLI unauthenticated")
	}
	for _, want := range []string{"glab", "glab auth login", "gitlab_cli"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message = %q, want it to mention %q", err, want)
		}
	}

	// Checked at the adapter rather than at the outcome: that no tmux command
	// ran is a stronger statement than that no terminal exists.
	if calls := server.Calls(); len(calls) != 0 {
		t.Errorf("the refused launch still issued %d tmux commands: %v", len(calls), calls)
	}
	stored := arranged.reload(t)
	if stored.Session != nil {
		t.Error("the refused launch recorded an agent session")
	}
	if stored.Workflow != domain.WorkflowFailed {
		t.Errorf("workflow = %q, want failed: a launch that could not start must be explainable", stored.Workflow)
	}
}

// TestADevcontainerProjectRunsItsAgentInTheContainer is the boundary the
// security model exists to describe: the agent runs where the project put it.
//
// Claude is started, but never on this host. The command the terminal runs
// enters the task's own Compose project, and the session records that it is a
// devcontainer session rather than a host one.
func TestADevcontainerProjectRunsItsAgentInTheContainer(t *testing.T) {
	live := launch(t, prepareFixture, installed(), false)

	launched, ok := live.tmux.Launched()
	if !ok {
		t.Fatal("a devcontainer project launched nothing")
	}
	if launched.Program() == claude.Executable {
		t.Error("a devcontainer project launched Claude directly on the host")
	}
	if !strings.Contains(strings.Join(launched.Command, " "), "--project-name feat-agent-app-") {
		t.Errorf("the launch does not enter the task's own Compose project: %v", launched.Command)
	}

	task := live.task(t)
	if task.Session.ExecutionMode != domain.ExecutionDevcontainer {
		t.Errorf("execution mode = %q, want the configured devcontainer", task.Session.ExecutionMode)
	}
	if task.Session.Execution == nil {
		t.Fatal("the session records no execution environment")
	}
	if task.Session.Execution.User == "root" {
		t.Error("the recorded agent user is root")
	}

	// The control workspace exists now, because there is an agent to report
	// through it.
	workspace, err := control.Open(live.service.layout.ControlRoot(), "app", live.ref.Task, control.Options{})
	if err != nil {
		t.Fatalf("opening the control workspace: %v", err)
	}
	if !workspace.Exists() {
		t.Error("no control workspace was created for a launched agent")
	}
}

func TestTheHostAgentOptInComesFromTheDaemonRatherThanARequest(t *testing.T) {
	live := launch(t, prepareFixture, installed(), true)

	// With the opt-in, the same devcontainer project gets a real session.
	launched, ok := live.tmux.Launched()
	if !ok || launched.Program() != claude.Executable {
		t.Fatalf("the opt-in did not launch Claude: %v", live.tmux.Launches())
	}
	task := live.task(t)
	if task.Session.ExecutionMode != domain.ExecutionHost {
		t.Errorf("execution mode = %q, want host: the session records where it actually runs, not where it was configured",
			task.Session.ExecutionMode)
	}

	// The generated system prompt tells the agent it is outside its configured
	// boundary, so nothing in the session believes it is contained.
	var file string
	for i, argument := range launched.Command {
		if argument == "--append-system-prompt-file" && i+1 < len(launched.Command) {
			file = launched.Command[i+1]
		}
	}
	if file == "" {
		t.Fatalf("the launch carries no generated instructions: %v", launched.Command)
	}
	prompt, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("reading the generated instructions: %v", err)
	}
	if !strings.Contains(string(prompt), "directly on the user's host") {
		t.Errorf("instructions = %q, want them to state that the session is not contained", prompt)
	}
}

func TestAFailedSessionLeavesAnExplainableTask(t *testing.T) {
	live := launch(t, hostFixture, installed(), false)
	live.start(t)

	live.hook(t, "StopFailure", `{"session_id":"claude-session-1","error":"the model provider refused the request"}`)

	task := live.task(t)
	// A task whose agent died must not rest in a state that says it is working.
	if task.Workflow != domain.WorkflowFailed {
		t.Errorf("workflow = %q, want failed", task.Workflow)
	}
	if task.Session.Process != domain.ProcessFailed {
		t.Errorf("process = %q, want failed", task.Session.Process)
	}
	if task.Attention != domain.AttentionNeedsInput {
		t.Errorf("attention = %q, want needs_input", task.Attention)
	}

	history := events(t, live.service, live.preparation)
	event, ok := recorded(history, domain.EventWorkflowChanged)
	if !ok {
		t.Fatal("no workflow event explains the failure")
	}
	_ = event

	// The provider session identifier survives the failure, so the resume slice
	// 12 offers can continue this session rather than open an empty one.
	if task.Session.ProviderSessionID != "claude-session-1" {
		t.Errorf("provider session id = %q, want it retained through the failure", task.Session.ProviderSessionID)
	}
}

// TestAnAgentThatNeverReportsStartingIsNotShownAsWorking comes from a real
// launch rather than from reasoning about one.
//
// Claude asks for workspace trust on a directory it has not seen before, and
// every task worktree is such a directory, so the first thing a launched session
// does is wait for a person. No hook fires while it waits, because hooks belong
// to a session that has begun. Feat showed the task as a running process with
// no attention, which is a task that looks like it is getting on with its work.
func TestAnAgentThatNeverReportsStartingIsNotShownAsWorking(t *testing.T) {
	live := launch(t, hostFixture, installed(), false)

	// Nothing has been heard from the agent. Its process is alive, because the
	// terminal is real, and that is precisely the misleading part.
	task := live.task(t)
	if task.Workflow != domain.WorkflowPreparing || task.Attention != domain.AttentionNone {
		t.Fatalf("workflow = %q and attention = %q immediately after launch, want preparing and none",
			task.Workflow, task.Attention)
	}

	grace, armed := live.timer.armed()
	if !armed {
		t.Fatal("a launched agent armed no notice for never reporting that it started")
	}
	if grace != startupGrace {
		t.Errorf("startup grace = %s, want %s", grace, startupGrace)
	}
	live.timer.fire()

	task = live.task(t)
	// Possibly waiting rather than needs input: Feat has heard nothing, which is
	// not the same as the provider reporting that it is blocked.
	if task.Attention != domain.AttentionPossiblyWaiting {
		t.Errorf("attention = %q, want possibly_waiting", task.Attention)
	}
	if task.Workflow != domain.WorkflowPreparing {
		t.Errorf("workflow = %q, want preparing: silence is not progress", task.Workflow)
	}

	history := events(t, live.service, live.preparation)
	event, ok := recorded(history, domain.EventAttentionChanged)
	if !ok {
		t.Fatal("nothing on the event stream explains the silence")
	}
	if !strings.Contains(event.Detail, "attach") {
		t.Errorf("detail = %q, want it to say what the user can do about it", event.Detail)
	}
}

func TestAnAgentThatReportsStartingCancelsTheSilenceNotice(t *testing.T) {
	live := launch(t, hostFixture, installed(), false)
	live.start(t)

	// The session spoke, so the notice no longer applies. Firing every pending
	// timer must not now claim that a working task is waiting for anybody.
	live.timer.fire()

	task := live.task(t)
	if task.Workflow != domain.WorkflowWorking {
		t.Errorf("workflow = %q, want working", task.Workflow)
	}
	if task.Attention != domain.AttentionNone {
		t.Errorf("attention = %q, want none: the agent reported that it started", task.Attention)
	}
}

func TestNotificationMeansTheUserIsNeededRatherThanThatTheTurnEnded(t *testing.T) {
	live := launch(t, hostFixture, installed(), false)
	live.start(t)

	live.hook(t, "Notification", `{"session_id":"claude-session-1","notification_type":"permission_request",`+
		`"message":"Claude needs your permission to use Bash"}`)

	task := live.task(t)
	// This is the one signal that tells a session waiting on a person from one
	// that merely finished speaking.
	if task.Attention != domain.AttentionNeedsInput {
		t.Errorf("attention = %q, want needs_input", task.Attention)
	}
	if task.Workflow != domain.WorkflowWorking {
		t.Errorf("workflow = %q, want working: needing input is not a review", task.Workflow)
	}
}

// TestAttentionClearsWhenTheAgentGetsThroughItsTurn comes from a real launch.
//
// Claude asked for permission mid-turn, which set needs-input correctly. Nothing
// cleared it afterwards, so a task that had once asked a question reported
// needing the user for the rest of its life. An attention state that never
// clears is one nobody reads.
func TestAttentionClearsWhenTheAgentGetsThroughItsTurn(t *testing.T) {
	live := launch(t, hostFixture, installed(), false)
	live.start(t)

	live.hook(t, "Notification", `{"session_id":"claude-session-1","notification_type":"permission_prompt"}`)
	if task := live.task(t); task.Attention != domain.AttentionNeedsInput {
		t.Fatalf("attention = %q, want needs_input", task.Attention)
	}

	// A turn cannot end while the agent is blocked on a dialog, so reaching the
	// end of one is evidence that the question was answered.
	live.hook(t, "Stop", `{"session_id":"claude-session-1"}`)
	if task := live.task(t); task.Attention != domain.AttentionNone {
		t.Errorf("attention after the turn ended = %q, want none", task.Attention)
	}

	// And the idle grace then reports the conservative state, not the strong one.
	live.timer.fire()
	if task := live.task(t); task.Attention != domain.AttentionPossiblyWaiting {
		t.Errorf("attention after the grace period = %q, want possibly_waiting", task.Attention)
	}
}

// TestAReviewRequestDoesNotClaimTheSessionIsWaiting comes from the dogfood runs
// ADR-058 records.
//
// The agent writes its review request in the middle of a turn it then carries
// on with: through the completion gate, and back into its own loop when the gate
// fails. Attention said possibly-waiting for all of it — seven minutes of active
// work on two real tasks — and cleared at the end of the turn, which is the one
// moment it was true. The dashboard counted a working agent into "may need you".
func TestAReviewRequestDoesNotClaimTheSessionIsWaiting(t *testing.T) {
	live := launch(t, hostFixture, installed(), false)
	live.start(t)

	live.emit(t, control.TypeReviewRequested, `{"summary":"Ready for review."}`)

	task := live.task(t)
	if task.Workflow != domain.WorkflowReviewRequested {
		t.Fatalf("workflow = %q, want review_requested", task.Workflow)
	}
	if task.Attention != domain.AttentionNone {
		t.Errorf("attention after a review request = %q, want none: the workflow says the task is in review, "+
			"and nothing has reported that the session is waiting for a person", task.Attention)
	}
	if task.Session.Process != domain.ProcessRunning {
		t.Errorf("process after a review request = %q, want running", task.Session.Process)
	}

	// The end of the turn and the idle grace after it are what decide the
	// attention, here as everywhere else.
	live.hook(t, "Stop", `{"session_id":"claude-session-1"}`)
	live.timer.fire()
	if task := live.task(t); task.Attention != domain.AttentionPossiblyWaiting {
		t.Errorf("attention once the turn ended and the grace passed = %q, want possibly_waiting", task.Attention)
	}
}

// TestAnIdleSessionExplainsTheAttentionItSets covers the other half of ADR-058:
// the state used to be written to the task and left out of its history, so the
// one place a user could ask why a task was counted as needing them said only
// that the process had gone idle.
func TestAnIdleSessionExplainsTheAttentionItSets(t *testing.T) {
	live := launch(t, hostFixture, installed(), false)
	live.start(t)

	live.hook(t, "Stop", `{"session_id":"claude-session-1"}`)
	live.timer.fire()

	if task := live.task(t); task.Attention != domain.AttentionPossiblyWaiting {
		t.Fatalf("attention after the grace period = %q, want possibly_waiting", task.Attention)
	}
	found := false
	for _, event := range events(t, live.service, live.preparation) {
		if event.Type != domain.EventAttentionChanged || event.To != string(domain.AttentionPossiblyWaiting) {
			continue
		}
		found = true
		if event.From != string(domain.AttentionNone) {
			t.Errorf("attention event from = %q, want none: the recorded transition must be the one that happened",
				event.From)
		}
		if event.Detail == "" {
			t.Error("the attention event says nothing about why Feat may be waiting")
		}
	}
	if !found {
		t.Error("nothing on the event stream explains why the task now says it may need the user")
	}
}

// TestSilenceDoesNotRecordATransitionThatDidNotHappen is the third defect in
// ADR-058, seen in a real event log: a task resumed while carrying needs-input
// from its previous life stayed silent for the startup grace, and the history
// gained "none -> needs_input" for a state that had not moved.
func TestSilenceDoesNotRecordATransitionThatDidNotHappen(t *testing.T) {
	live := launch(t, hostFixture, installed(), false)

	// Something already says this task needs a person. Nothing has been heard
	// from the agent either, which is the case the startup grace is about.
	task := live.task(t)
	if err := task.SetAttention(domain.AttentionNeedsInput, reconcileTime); err != nil {
		t.Fatalf("recording what the task already needs: %v", err)
	}
	if err := live.service.store.Tasks().Save(context.Background(), task); err != nil {
		t.Fatalf("saving the task: %v", err)
	}
	before := len(events(t, live.service, live.preparation))

	live.timer.fire()

	if task := live.task(t); task.Attention != domain.AttentionNeedsInput {
		t.Errorf("attention = %q, want needs_input: silence is weaker than a report and must not overwrite one",
			task.Attention)
	}
	history := events(t, live.service, live.preparation)
	for _, event := range history[min(before, len(history)):] {
		if event.Type == domain.EventAttentionChanged {
			t.Errorf("silence recorded %q -> %q, and the attention state did not move",
				event.From, event.To)
		}
	}
}

func TestAClearedSessionDoesNotReRunTheLaunchTransition(t *testing.T) {
	live := launch(t, hostFixture, installed(), false)
	live.start(t)

	live.emit(t, control.TypeReviewRequested, `{"summary":"Ready."}`)
	if task := live.task(t); task.Workflow != domain.WorkflowReviewRequested {
		t.Fatalf("workflow = %q, want review_requested", task.Workflow)
	}

	// A user typing /clear produces a session start. It must not move the task.
	live.hook(t, "SessionStart", `{"session_id":"claude-session-1","source":"clear"}`)

	if task := live.task(t); task.Workflow != domain.WorkflowReviewRequested {
		t.Errorf("workflow after /clear = %q, want it unchanged: a cleared session is the same session",
			task.Workflow)
	}
}

func TestPendingMessagesSurviveARestartAndApplyExactlyOnce(t *testing.T) {
	live := launch(t, hostFixture, installed(), false)
	live.start(t)

	// A turn that ended while the daemon was down: the file is there, and
	// nothing has read it.
	live.write(t, control.TypeReviewRequested, `{"summary":"Finished while you were away."}`)

	// A second daemon over the same state, which is what a restart is.
	restarted, err := New(Options{
		Layout:      live.service.layout,
		Environment: live.env,
		Build:       testBuild,
		Git:         live.fake,
		Tmux:        live.tmux,
		Agent:       live.runner,
		Timer:       newTestTimer(),
		Now:         func() time.Time { return reconcileTime.Add(time.Hour) },
	})
	if err != nil {
		t.Fatalf("restarting the daemon: %v", err)
	}
	restarted.service.pollControl(context.Background())

	task, err := restarted.service.store.Tasks().Load(context.Background(), live.ref)
	if err != nil {
		t.Fatalf("loading the task: %v", err)
	}
	if task.Workflow != domain.WorkflowReviewRequested {
		t.Fatalf("workflow after a restart = %q, want review_requested: the pending message was never read",
			task.Workflow)
	}
	updated := task.UpdatedAt

	// Polling again must not apply it a second time.
	restarted.service.pollControl(context.Background())
	task, err = restarted.service.store.Tasks().Load(context.Background(), live.ref)
	if err != nil {
		t.Fatalf("reloading the task: %v", err)
	}
	if task.UpdatedAt != updated {
		t.Error("a second poll applied the same message again")
	}
}

func TestAnEndedTurnFromBeforeARestartGoesIdleAtOnce(t *testing.T) {
	live := launch(t, hostFixture, installed(), false)
	live.start(t)

	// The turn ended an hour ago; the daemon is only now reading it. Measuring
	// the grace period from now would restart a clock that has long since run
	// out.
	live.write(t, control.TypeProviderEvent, `{"hook":"Stop","event":{"session_id":"claude-session-1"}}`)

	timer := newTestTimer()
	restarted, err := New(Options{
		Layout:      live.service.layout,
		Environment: live.env,
		Build:       testBuild,
		Git:         live.fake,
		Tmux:        live.tmux,
		Agent:       live.runner,
		Timer:       timer,
		Now:         func() time.Time { return reconcileTime.Add(time.Hour) },
	})
	if err != nil {
		t.Fatalf("restarting the daemon: %v", err)
	}
	restarted.service.pollControl(context.Background())

	grace, armed := timer.armed()
	if !armed {
		t.Fatal("the pending end of turn armed no idle transition")
	}
	if grace != 0 {
		t.Errorf("grace period = %s, want none: the turn ended an hour ago", grace)
	}
}

func TestOneTasksDamagedWorkspaceDoesNotStopAnother(t *testing.T) {
	live := launch(t, hostFixture, installed(), false)
	live.start(t)

	// A second task in the same project, launched the same way.
	second := domain.NewTaskID()
	other, err := domain.NewTask(second, "app", "Another task",
		domain.TaskSource{Kind: domain.SourcePrompt}, reconcileTime)
	if err != nil {
		t.Fatalf("creating a second task: %v", err)
	}
	if err := other.SetBrief("Something else.", reconcileTime); err != nil {
		t.Fatalf("setting the brief: %v", err)
	}
	if err := live.service.store.Tasks().Save(context.Background(), other); err != nil {
		t.Fatalf("saving the second task: %v", err)
	}

	// The first task's outbox holds something unreadable.
	workspace := live.workspace(t)
	if err := os.WriteFile(filepath.Join(workspace.OutboxDir(), "broken.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("writing a damaged message: %v", err)
	}
	// And a good one behind it.
	live.write(t, control.TypeReviewRequested, `{"summary":"Still fine."}`)
	live.service.now = func() time.Time { return reconcileTime.Add(time.Minute) }

	live.service.pollControl(context.Background())

	if task := live.task(t); task.Workflow != domain.WorkflowReviewRequested {
		t.Errorf("workflow = %q, want review_requested: one damaged message stopped a healthy one", task.Workflow)
	}
}

func TestControlWorkspaceIsOutsideTheSnapshotDirectory(t *testing.T) {
	live := launch(t, hostFixture, installed(), false)
	workspace := live.workspace(t)

	// The tree slice 8 mounts into a container must not contain the task's own
	// snapshot, event log, or stored brief (ADR-032).
	snapshots := filepath.Join(live.state, "projects", "app", "tasks", live.ref.Task.String())
	if strings.HasPrefix(workspace.Root(), snapshots) {
		t.Fatalf("the control workspace %s is inside the snapshot directory %s", workspace.Root(), snapshots)
	}

	var found []string
	err := filepath.WalkDir(workspace.Root(), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			found = append(found, filepath.Base(path))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the control workspace: %v", err)
	}
	for _, name := range found {
		if name == "task.json" || name == "events.jsonl" || name == "prompt.md" || name == "review.json" {
			t.Errorf("the control workspace holds %q, which belongs to Feat's own state", name)
		}
	}
}

var _ = store.Ref
