package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ma8el/feat/internal/agent/agenttest"
	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/control"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/review"
	"github.com/ma8el/feat/internal/runtime/compose/runtimetest"
)

// reviewFixture is a host-mode project with review commands and two checks.
//
// The two repositories carry different bases, which is what makes "each
// repository against its own recorded base" a claim a test can distinguish from
// "every repository against one of them".
const reviewFixture = `version: 1

project:
  id: app
  name: Example Application
  primary_repository: api

repositories:
  api:
    host_path: ~/repos/app/api
    container_path: /srv/api
    default_access: read_write
  store:
    host_path: ~/repos/app/store
    container_path: /srv/store
    default_access: read_only

git:
  base_policy: remote
  branch_template: "feat/{task_key}-{slug}"

agent:
  execution:
    mode: host
  claude:
    idle_grace_period: 5s

review:
  diff:
    command: ["git", "diff", "{base_commit}"]
  editor:
    command: ["nvim", "{repository_path}"]
  status:
    command: ["git", "status", "--short", "--branch"]

checks:
  api:
    - id: unit
      command: ["run-tests", "{repository_id}"]
      execution: agent
  store:
    - id: schema
      command: ["check-schema"]
      execution: host
`

// reviewRuntimeFixture is the same project with application services, so that
// "approval stops nothing" is a claim about a runtime that is actually running.
const reviewRuntimeFixture = reviewFixture + `
runtime:
  provider: compose
  compose_files:
    - ~/repos/app/compose.yml
  project_name_template: "feat-{project_id}-{task_key}"
  services:
    - api
`

// fakeChecks answers a gate's checks from a table and records what it ran.
type fakeChecks struct {
	mu       sync.Mutex
	outputs  map[string]review.Output
	errs     map[string]error
	ran      []review.Check
	released chan struct{}
}

func newFakeChecks() *fakeChecks {
	return &fakeChecks{outputs: make(map[string]review.Output), errs: make(map[string]error)}
}

func (f *fakeChecks) Run(ctx context.Context, check review.Check) (review.Output, error) {
	f.mu.Lock()
	f.ran = append(f.ran, check)
	release := f.released
	f.mu.Unlock()

	if release != nil {
		// A check that does not finish until the test says so, which is how the
		// verifying state is observed while it is real.
		select {
		case <-release:
		case <-ctx.Done():
			return review.Output{}, ctx.Err()
		}
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if err, failing := f.errs[check.ID]; failing {
		return review.Output{}, err
	}
	return f.outputs[check.ID], nil
}

func (f *fakeChecks) commands() []review.Check {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]review.Check(nil), f.ran...)
}

// reviewSession is a launched task whose project configures review commands and
// checks.
type reviewSession struct {
	*session
	checks *fakeChecks
}

func launchForReview(t *testing.T, adjust func(*Options)) *reviewSession {
	t.Helper()

	checks := newFakeChecks()
	live := launchWith(t, reviewFixture, installed(), false, func(options *Options) {
		options.Checks = checks
		if adjust != nil {
			adjust(options)
		}
	})
	live.start(t)
	return &reviewSession{session: live, checks: checks}
}

// review performs one review action.
func (s *reviewSession) review(t *testing.T, action api.ReviewAction) api.ReviewResult {
	t.Helper()

	result, err := s.service.Review(context.Background(), s.ref.Task, action)
	if err != nil {
		t.Fatalf("review %s: %v", action, err)
	}
	return result
}

// requestReview writes the review request the generated helper writes, delivers
// it, and waits for the gate it starts.
func (s *reviewSession) requestReview(t *testing.T, payload string) string {
	t.Helper()

	id := s.write(t, control.TypeReviewRequested, payload)
	s.deliver(t)
	s.awaitGate(t)
	return id
}

// awaitGate waits for a background gate run to finish.
func (s *reviewSession) awaitGate(t *testing.T) {
	t.Helper()

	select {
	case <-s.service.gate.done:
	case <-time.After(10 * time.Second):
		t.Fatal("the completion gate did not finish")
	}
}

// TestEachRepositoryUsesItsOwnRecordedBaseCommit is slice 11's first acceptance
// criterion.
//
// It is checked on the expanded commands as well as on the summaries, because a
// review that produced the right numbers while offering a diff against the wrong
// commit would pass an assertion about numbers alone — and the diff is what the
// user actually reads.
func TestEachRepositoryUsesItsOwnRecordedBaseCommit(t *testing.T) {
	live := launchForReview(t, nil)

	task := live.task(t)
	bases := make(map[string]string, len(task.Repositories))
	for _, binding := range task.Repositories {
		bases[binding.RepositoryID.String()] = binding.BaseCommit
	}
	if len(bases) != 2 {
		t.Fatalf("the task binds %d repositories, want two", len(bases))
	}

	result := live.review(t, api.ReviewObserve)

	for id, base := range bases {
		change, found := result.Review.Repository(domain.RepositoryID(id))
		if !found {
			t.Fatalf("no summary was recorded for %s", id)
		}
		if change.BaseCommit != base {
			t.Errorf("%s was compared against %s, want its own recorded base %s", id, change.BaseCommit, base)
		}
	}

	for _, command := range result.Commands {
		if command.Kind != api.ReviewCommandKindDiff {
			continue
		}
		base := bases[command.RepositoryID]
		if !containsArgument(command.Arguments, base) {
			t.Errorf("the diff of %s is %v, and it does not name that repository's base %s",
				command.RepositoryID, command.Arguments, base)
		}
	}
}

// TestTheEditorOpensInTheSelectedTaskRepository is the second acceptance
// criterion.
//
// FR-REV-003 words it around the editor because that is the reference project's
// case, and what it is really about is that a command opens the repository the
// user selected: the working directory is that repository's own task worktree,
// and never the ordinary checkout the task exists to leave alone.
func TestTheEditorOpensInTheSelectedTaskRepository(t *testing.T) {
	live := launchForReview(t, nil)
	result := live.review(t, api.ReviewObserve)

	worktrees := make(map[string]string)
	for _, binding := range result.Task.Repositories {
		worktrees[binding.RepositoryID.String()] = binding.WorktreePath
	}

	found := 0
	for _, command := range result.Commands {
		if command.Kind != api.ReviewCommandKindEditor {
			continue
		}
		found++
		worktree := worktrees[command.RepositoryID]
		if command.Directory != worktree {
			t.Errorf("the editor of %s would run in %q, want its worktree %q",
				command.RepositoryID, command.Directory, worktree)
		}
		if !containsArgument(command.Arguments, worktree) {
			t.Errorf("the editor of %s is opened on %v, want its worktree %q",
				command.RepositoryID, command.Arguments, worktree)
		}
		if strings.Contains(command.Directory, "repos/app") {
			t.Errorf("the editor of %s would open the ordinary checkout %q", command.RepositoryID, command.Directory)
		}
	}
	if found != len(worktrees) {
		t.Errorf("%d editor commands were expanded, want one per repository", found)
	}
}

// TestCommandsCannotEscapeConfiguredTaskPaths is the third acceptance criterion
// at the daemon.
//
// internal/review decides what an expansion may turn into; what is checked here
// is that the daemon hands it one task's own worktrees and reports a refusal
// rather than running the command anyway. The refusal is a note, because the
// other repositories' commands are still usable and a user who cannot open one
// is better served by being told why than by an empty screen.
func TestCommandsCannotEscapeConfiguredTaskPaths(t *testing.T) {
	live := launchForReview(t, nil)

	// A recorded worktree path that has been edited into something Feat would
	// never have created. It is the shape ADR-029 refused for cleanup: the
	// moment a path from a record decides what runs, the record has stopped
	// being a record.
	task := live.task(t)
	task.Repositories[0].WorktreePath = "/etc"
	if err := live.service.store.Tasks().Save(context.Background(), task); err != nil {
		t.Fatalf("saving the task: %v", err)
	}

	result := live.review(t, api.ReviewObserve)

	for _, command := range result.Commands {
		if command.Directory == "/etc" {
			t.Fatalf("a %s command would run in %q", command.Kind, command.Directory)
		}
	}
	if !anyNote(result.Notes, "shared system directory") {
		t.Errorf("the refusal was not reported to the user: %v", result.Notes)
	}
}

// TestApprovalDoesNotStopOrDestroyRuntime is the fourth acceptance criterion.
//
// It is checked by counting the commands an approval produces rather than by
// asserting that a recorded state did not change: that no container command ran
// at all is a stronger statement than that a container happens to still be there
// (FR-REV-004, docs/02-user-workflows.md §7).
func TestApprovalDoesNotStopOrDestroyRuntime(t *testing.T) {
	docker := runtimetest.New()
	checks := newFakeChecks()
	live := launchWith(t, reviewRuntimeFixture, installed(), false, func(options *Options) {
		options.Checks = checks
		options.RuntimeDocker = docker
		// The observation poller would run commands of its own, and what this
		// test counts is the commands approving produced.
		options.RuntimeInterval = -1
	})
	live.start(t)
	session := &reviewSession{session: live, checks: checks}

	// The task's own Compose project, whose name carries its key.
	identity := "feat-app-" + live.ref.Task.Key().String()
	docker.
		Answer("network ls --filter label=com.docker.compose.project="+identity+" --format {{.Name}}", "").
		Answer("volume ls --filter label=com.docker.compose.project="+identity+" --format {{.Name}}", "")

	// Services the user started, and which are still running when they approve.
	if _, err := live.service.Runtime(context.Background(), live.ref.Task, api.RuntimeStart); err != nil {
		t.Fatalf("starting the application services: %v", err)
	}
	before := len(docker.Vectors())

	session.emit(t, control.TypeReviewRequested, `{"summary":"ready"}`)
	session.awaitGate(t)
	result := session.review(t, api.ReviewApprove)

	if result.Task.Workflow != domain.WorkflowApproved {
		t.Fatalf("workflow after approval = %q, want approved", result.Task.Workflow)
	}
	if result.Review.Status != domain.ReviewApproved {
		t.Errorf("review status = %q, want approved", result.Review.Status)
	}
	if after := docker.Vectors(); len(after) != before {
		t.Errorf("approving ran %v, and approval stops and destroys nothing", after[before:])
	}
	if result.Task.Runtime == nil || result.Task.Runtime.State != domain.RuntimeRunning {
		t.Errorf("the task's services are %+v after approval, want them still running", result.Task.Runtime)
	}
}

// TestReviewDecisionsSurviveARestart checks the slice's work item that review
// state is preserved across a restart.
//
// The aggregate is stored, so what this really checks is that the decision goes
// to disk rather than into the running process: a daemon restarted after an
// approval must not present the task as waiting for one.
func TestReviewDecisionsSurviveARestart(t *testing.T) {
	live := launchForReview(t, nil)
	live.emit(t, control.TypeReviewRequested, `{"summary":"ready"}`)
	live.awaitGate(t)

	live.review(t, api.ReviewApprove)

	restarted := restart(t, live.session)
	record, err := restarted.store.Reviews().Load(context.Background(), live.ref)
	if err != nil {
		t.Fatalf("loading the review after a restart: %v", err)
	}
	if record.Status != domain.ReviewApproved {
		t.Errorf("review status after a restart = %q, want approved", record.Status)
	}
	if record.DecidedAt.IsZero() {
		t.Error("the decision has no time after a restart")
	}

	task, err := restarted.Task(context.Background(), live.ref.Task)
	if err != nil {
		t.Fatalf("loading the task after a restart: %v", err)
	}
	if task.Workflow != domain.WorkflowApproved {
		t.Errorf("workflow after a restart = %q, want approved", task.Workflow)
	}
}

// TestAFailedCheckReturnsTheTaskToTheAgentLoop is the fifth acceptance
// criterion.
//
// Three things are checked together because they are one property: the task
// never reaches ready_for_review, the failure is written where the waiting agent
// reads it, and the agent's own claim about the same check is still there and
// still marked as a claim.
func TestAFailedCheckReturnsTheTaskToTheAgentLoop(t *testing.T) {
	live := launchForReview(t, nil)
	live.checks.outputs["unit"] = review.Output{Stdout: "2 failed, 82 passed", ExitCode: 1}
	live.checks.outputs["schema"] = review.Output{}

	request := live.requestReview(t,
		`{"summary":"ready","checks":[{"id":"unit","status":"passed","detail":"all good"}]}`)

	task := live.task(t)
	if task.Workflow != domain.WorkflowVerificationFailed {
		t.Fatalf("workflow = %q, want verification_failed", task.Workflow)
	}

	// The whole history, because the criterion is that the task never reached
	// ready_for_review rather than that it is not there now.
	for _, event := range history(t, live.session) {
		if event.Type == domain.EventWorkflowChanged && event.To == string(domain.WorkflowReadyForReview) {
			t.Fatal("a task whose configured check failed reached ready_for_review")
		}
	}

	// The verdict the helper is waiting for, in the inbox, saying what failed.
	verdict := readVerdict(t, live.session, request)
	if !strings.HasPrefix(verdict, "feat-verification 1 failed") {
		t.Fatalf("the verdict is %q, want a failed one", firstLine(verdict))
	}
	if !strings.Contains(verdict, "2 failed, 82 passed") {
		t.Error("the verdict does not carry what the check printed, so the agent cannot act on it")
	}

	// The gate's result and the agent's claim about the same check are both
	// recorded, and they do not read alike.
	record, err := live.service.store.Reviews().Load(context.Background(), live.ref)
	if err != nil {
		t.Fatalf("loading the review: %v", err)
	}
	gated, found := checkResult(record, "unit")
	if !found {
		t.Fatal("the gate recorded no result for the check it ran")
	}
	if gated.Reporter != domain.ReporterProvider || gated.Status != domain.CheckFailed {
		t.Errorf("the gate's result is %s/%s, want a failed provider result", gated.Reporter, gated.Status)
	}
	verification, ok := api.NewVerification(record)
	if !ok || verification.Source != string(domain.ReporterProvider) {
		t.Errorf("the published verification is %+v, want one attributed to the gate", verification)
	}
}

// TestAPassingGateReachesReadyForReview is the other half of the fifth
// criterion: the states this slice makes reachable are reached, and only by
// running the checks.
func TestAPassingGateReachesReadyForReview(t *testing.T) {
	live := launchForReview(t, nil)
	live.checks.outputs["unit"] = review.Output{Stdout: "84 passed"}
	live.checks.outputs["schema"] = review.Output{}

	request := live.requestReview(t, `{"summary":"ready"}`)

	if got := live.task(t).Workflow; got != domain.WorkflowReadyForReview {
		t.Fatalf("workflow = %q, want ready_for_review", got)
	}

	var verifying bool
	for _, event := range history(t, live.session) {
		if event.Type == domain.EventWorkflowChanged && event.To == string(domain.WorkflowVerifying) {
			verifying = true
		}
	}
	if !verifying {
		t.Error("the task reached ready_for_review without passing through verifying")
	}

	if verdict := readVerdict(t, live.session, request); !strings.HasPrefix(verdict, "feat-verification 1 passed") {
		t.Errorf("the verdict is %q, want a passing one", firstLine(verdict))
	}
}

// TestAGateRunsEachCheckWhereItsConfigurationSays checks that the execution
// field decides where a check runs, and that a repository the task holds
// read-only is skipped rather than silently dropped.
func TestAGateRunsEachCheckWhereItsConfigurationSays(t *testing.T) {
	live := launchForReview(t, nil)
	live.checks.outputs["unit"] = review.Output{}

	live.requestReview(t, `{"summary":"ready"}`)

	ran := live.checks.commands()
	if len(ran) != 1 || ran[0].ID != "unit" {
		t.Fatalf("the gate ran %+v, want only the read-write repository's check", ran)
	}
	if ran[0].OnHost {
		t.Error("a check configured to run in the agent's environment ran on the host")
	}
	worktree := live.task(t).Repositories[0].WorktreePath
	if ran[0].Directory != worktree {
		t.Errorf("the check ran in %q, want the task worktree %q", ran[0].Directory, worktree)
	}

	record, err := live.service.store.Reviews().Load(context.Background(), live.ref)
	if err != nil {
		t.Fatalf("loading the review: %v", err)
	}
	skipped, found := checkResult(record, "schema")
	if !found {
		t.Fatal("the check of a read-only repository was dropped rather than skipped")
	}
	if skipped.Status != domain.CheckSkipped {
		t.Errorf("the check of a read-only repository is %q, want skipped", skipped.Status)
	}
	if !strings.Contains(skipped.Detail, "read-only") {
		t.Errorf("the skip does not say why: %q", skipped.Detail)
	}
}

// TestAnInterruptedGateDoesNotClaimToBeRunning checks the recovery ADR-036
// requires: a gate does not outlive the process that started it, so a task
// recorded as verifying after a restart is a task claiming that checks are
// running when nothing is.
func TestAnInterruptedGateDoesNotClaimToBeRunning(t *testing.T) {
	live := launchForReview(t, nil)
	live.checks.released = make(chan struct{})

	live.emit(t, control.TypeReviewRequested, `{"summary":"ready"}`)

	// The gate is running and the task says so.
	waitFor(t, func() bool { return live.task(t).Workflow == domain.WorkflowVerifying })

	// A restart, while the checks are still held.
	restarted := restart(t, live.session)
	restarted.recoverGates(context.Background())

	task, err := restarted.Task(context.Background(), live.ref.Task)
	if err != nil {
		t.Fatalf("loading the task: %v", err)
	}
	if task.Workflow != domain.WorkflowReviewRequested {
		t.Errorf("workflow after a restart = %q, want the review request it came from", task.Workflow)
	}

	explained := false
	for _, event := range history(t, live.session) {
		if event.Type == domain.EventWorkflowChanged && strings.Contains(event.Detail, "interrupted") {
			explained = true
		}
	}
	if !explained {
		t.Error("the interruption was not explained in the task's history")
	}

	close(live.checks.released)
	live.awaitGate(t)
}

// TestVerifyingAgainIsAUserAction checks that a task whose checks failed can be
// verified again, and that recovery is something a user asks for rather than
// something that happens on its own.
func TestVerifyingAgainIsAUserAction(t *testing.T) {
	live := launchForReview(t, nil)
	live.checks.outputs["unit"] = review.Output{Stdout: "2 failed", ExitCode: 1}

	live.requestReview(t, `{"summary":"ready"}`)
	if got := live.task(t).Workflow; got != domain.WorkflowVerificationFailed {
		t.Fatalf("workflow = %q, want verification_failed", got)
	}

	// The agent fixed it. Running the checks again is one action.
	live.checks.outputs["unit"] = review.Output{Stdout: "84 passed"}
	live.review(t, api.ReviewVerify)
	live.awaitGate(t)

	if got := live.task(t).Workflow; got != domain.WorkflowReadyForReview {
		t.Errorf("workflow after a second run = %q, want ready_for_review", got)
	}
}

// TestAReviewRequestFromAFailedGateRunsTheChecksAgain checks the other way back
// into the loop: the agent fixed what the gate caught and asked again itself.
func TestAReviewRequestFromAFailedGateRunsTheChecksAgain(t *testing.T) {
	live := launchForReview(t, nil)
	live.checks.outputs["unit"] = review.Output{Stdout: "2 failed", ExitCode: 1}
	live.requestReview(t, `{"summary":"ready"}`)

	live.checks.outputs["unit"] = review.Output{Stdout: "84 passed"}
	live.requestReview(t, `{"summary":"fixed it"}`)

	if got := live.task(t).Workflow; got != domain.WorkflowReadyForReview {
		t.Errorf("workflow = %q, want ready_for_review after a second request that passed", got)
	}
	if runs := len(live.checks.commands()); runs != 2 {
		t.Errorf("the gate ran %d times, want one run per review request", runs)
	}
}

// TestAProjectWithNoChecksLeavesTheRequestWithTheUser checks what
// docs/02-user-workflows.md §6 describes for a project with no completion gate:
// the review request stands, with the agent's own report beside it, and no state
// claims a verification that did not happen.
func TestAProjectWithNoChecksLeavesTheRequestWithTheUser(t *testing.T) {
	live := launchWith(t, hostFixture, installed(), false, nil)
	live.start(t)

	live.emit(t, control.TypeReviewRequested,
		`{"summary":"ready","checks":[{"id":"unit","status":"passed"}]}`)

	if got := live.task(t).Workflow; got != domain.WorkflowReviewRequested {
		t.Errorf("workflow = %q, want review_requested", got)
	}
	record, err := live.service.store.Reviews().Load(context.Background(), live.ref)
	if err != nil {
		t.Fatalf("loading the review: %v", err)
	}
	if record.Gated() {
		t.Error("a task with no configured checks reports a gated result")
	}
	verification, ok := api.NewVerification(record)
	if !ok || verification.Source != string(domain.ReporterAgent) {
		t.Errorf("the published verification is %+v, want the agent's claim", verification)
	}
}

// TestAGatedTaskIsAnnouncedOnceItHasBeenChecked checks that a user is
// interrupted when the task reaches them rather than when the agent asks.
//
// A gate takes as long as the project's suite does, and a task whose checks are
// running has not arrived with the user yet. Telling them twice for one arrival
// is how a notification becomes something people learn to dismiss (ADR-035's
// rule, applied to the state this slice adds).
func TestAGatedTaskIsAnnouncedOnceItHasBeenChecked(t *testing.T) {
	live := launchForReview(t, nil)
	live.watchable()
	live.checks.outputs["unit"] = review.Output{Stdout: "84 passed"}

	live.requestReview(t, `{"summary":"ready"}`)

	delivered := live.notifier.sent()
	if len(delivered) != 1 {
		t.Fatalf("a gated review request produced %d notifications, want the one about the result: %+v",
			len(delivered), delivered)
	}
	if !strings.Contains(strings.ToLower(delivered[0].Body+delivered[0].Title), "ready") {
		t.Errorf("the notification is %+v, want the one about a task that is ready", delivered[0])
	}
}

// TestAnUngatedReviewRequestIsAnnouncedWhenItArrives checks the other half:
// where no gate will run, the request itself is what reaches the user, because
// nothing else is going to.
func TestAnUngatedReviewRequestIsAnnouncedWhenItArrives(t *testing.T) {
	live := launchWith(t, hostFixture, installed(), false, nil)
	live.start(t)
	live.watchable()

	live.emit(t, control.TypeReviewRequested, `{"summary":"ready"}`)

	if delivered := live.notifier.sent(); len(delivered) != 1 {
		t.Fatalf("an ungated review request produced %d notifications, want one: %+v",
			len(delivered), delivered)
	}
}

// TestReviewingADraftIsRefused checks that review reports what a draft is rather
// than reading worktrees that do not exist.
func TestReviewingADraftIsRefused(t *testing.T) {
	arranged := arrangeTaskWith(t, newFakeGit(), reviewFixture)

	_, err := arranged.service.Review(context.Background(), arranged.ref.Task, api.ReviewObserve)
	if err == nil {
		t.Fatal("a draft was reviewed")
	}
	if !strings.Contains(err.Error(), "draft") {
		t.Errorf("the refusal is %q, and it should say what a draft is", err)
	}
}

// history returns the task's recorded events.
func history(t *testing.T, live *session) []domain.Event {
	t.Helper()

	log, err := live.service.store.Events().Replay(context.Background(), live.ref)
	if err != nil {
		t.Fatalf("reading the task's history: %v", err)
	}
	return log.Events
}

// restart builds a second daemon over the same state directory, the way a
// restarted process reads what the previous one wrote.
func restart(t *testing.T, live *session) *service {
	t.Helper()

	instance, err := New(Options{
		Layout:      live.service.layout,
		Environment: live.env,
		Build:       testBuild,
		Git:         live.fake,
		Tmux:        live.tmux,
		Agent:       agenttest.New(),
		Notifier:    newFakeNotifier(),
		Now:         func() time.Time { return reconcileTime },
	})
	if err != nil {
		t.Fatalf("restarting the daemon: %v", err)
	}
	return instance.service
}

// readVerdict returns the document the gate wrote for one review request.
func readVerdict(t *testing.T, live *session, request string) string {
	t.Helper()

	name, err := control.VerificationName(request)
	if err != nil {
		t.Fatalf("naming the verdict: %v", err)
	}
	document, err := os.ReadFile(filepath.Join(live.workspace(t).InboxDir(), name))
	if err != nil {
		t.Fatalf("reading the verdict the agent waits for: %v", err)
	}
	return string(document)
}

// waitFor polls until the condition holds, or fails the test.
func waitFor(t *testing.T, condition func() bool) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the condition did not hold within the deadline")
}

func checkResult(record *domain.Review, id string) (domain.Check, bool) {
	for _, check := range record.Checks {
		if check.ID == id {
			return check, true
		}
	}
	return domain.Check{}, false
}

func containsArgument(arguments []string, want string) bool {
	for _, argument := range arguments {
		if argument == want {
			return true
		}
	}
	return false
}

func anyNote(notes []string, want string) bool {
	for _, note := range notes {
		if strings.Contains(note, want) {
			return true
		}
	}
	return false
}

func firstLine(document string) string {
	if index := strings.IndexByte(document, '\n'); index >= 0 {
		return document[:index]
	}
	return document
}

// TestAFinishingGateDoesNotLoseItsResultsToAConcurrentRead is the regression
// test for the defect slice 11's end-to-end run found.
//
// The daemon is the only process that writes state and every write is atomic,
// and neither of those makes a load-change-save cycle safe against another one.
// A gate finishing while the review request that started it was still comparing
// repositories left a task recorded as ready_for_review whose review held no
// checks at all: the state said the checks had passed, and the record of what
// passed had been overwritten by a copy loaded a moment earlier.
//
// The interleaving is forced rather than raced: the comparison is held open
// inside Git, the gate is released while it is held, and only then does the
// comparison finish and save.
func TestAFinishingGateDoesNotLoseItsResultsToAConcurrentRead(t *testing.T) {
	held := make(chan struct{})
	release := make(chan struct{})
	comparing := sync.Once{}

	live := launchForReview(t, nil)
	live.checks.outputs["unit"] = review.Output{Stdout: "84 passed"}
	live.checks.released = make(chan struct{})

	live.fake.mu.Lock()
	live.fake.onDiff = func() {
		comparing.Do(func() {
			close(held)
			<-release
		})
	}
	live.fake.mu.Unlock()

	// The gate starts and stops inside its checks, so the task is verifying.
	live.emit(t, control.TypeReviewRequested, `{"summary":"ready"}`)
	waitFor(t, func() bool { return live.task(t).Workflow == domain.WorkflowVerifying })

	// A review opened while the checks run. It reaches Git and stays there.
	observed := make(chan error, 1)
	go func() {
		_, err := live.service.Review(context.Background(), live.ref.Task, api.ReviewObserve)
		observed <- err
	}()
	<-held

	// The checks finish while the comparison is still open, and only then does
	// the comparison save what it read.
	close(live.checks.released)
	time.Sleep(50 * time.Millisecond)
	close(release)

	if err := <-observed; err != nil {
		t.Fatalf("observing while the checks ran: %v", err)
	}
	live.awaitGate(t)

	record, err := live.service.store.Reviews().Load(context.Background(), live.ref)
	if err != nil {
		t.Fatalf("loading the review: %v", err)
	}
	if !record.Gated() {
		t.Fatalf("the gate's results were lost: the review holds %+v", record.Checks)
	}
	if got := live.task(t).Workflow; got != domain.WorkflowReadyForReview {
		t.Errorf("workflow = %q, want ready_for_review", got)
	}
	if len(record.Repositories) == 0 {
		t.Error("the comparison the review made was lost instead")
	}
}
