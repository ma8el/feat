package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/ma8el/feat/internal/agent"
	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/config"
	"github.com/ma8el/feat/internal/control"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/execution"
	"github.com/ma8el/feat/internal/git"
	"github.com/ma8el/feat/internal/review"
	"github.com/ma8el/feat/internal/store"
)

// Review performs one review action for a task.
//
// Every action is a user's explicit request, as every runtime action is. Nothing
// here starts, stops, or removes anything: approving a task is a statement about
// the work, and the environment the user was testing it in is theirs to keep or
// to end (FR-REV-004, docs/02-user-workflows.md §7).
func (s *service) Review(
	ctx context.Context, id domain.TaskID, action api.ReviewAction,
) (api.ReviewResult, error) {
	if !action.Valid() {
		return api.ReviewResult{}, fmt.Errorf("%w: %q is not a review action", api.ErrInvalid, action)
	}

	// Held for the whole action, because every one of them reads the task and
	// its review, changes part of them, and saves: a gate finishing in the
	// middle of that would have its results overwritten by the copy this
	// request loaded first (ADR-036).
	defer s.locks.lock(id)()

	task, cfg, err := s.reviewTask(ctx, id)
	if err != nil {
		return api.ReviewResult{}, err
	}

	switch action {
	case api.ReviewObserve:
		// Observing is the only action that asks Git anything, and it is what
		// opening review does.
	case api.ReviewVerify:
		if err := s.verifyNow(ctx, task); err != nil {
			return api.ReviewResult{}, err
		}
	default:
		if err := s.decide(ctx, task, action); err != nil {
			return api.ReviewResult{}, err
		}
	}

	return s.reviewResult(ctx, cfg, task)
}

// reviewTask loads a task and its project configuration for a review action.
func (s *service) reviewTask(ctx context.Context, id domain.TaskID) (*domain.Task, *config.Config, error) {
	task, err := s.Task(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if task.Workflow == domain.WorkflowDraft {
		return nil, nil, fmt.Errorf("%w: task %s is still a draft, and nothing has been created for it. "+
			"Confirm it first", api.ErrInvalid, id)
	}

	cfg, err := config.Load(s.layout.ProjectConfigDir(), task.ProjectID.String(), s.configOptions())
	if err != nil {
		return nil, nil, translateConfig(err)
	}
	return task, cfg, nil
}

// reviewResult observes every repository and returns what review shows.
//
// The comparison is taken now rather than read from the record, because a review
// screen that showed what Feat saw an hour ago would be describing a worktree the
// agent has been writing to since. What is recorded is what was just observed, so
// the summary survives a restart and the dashboard's file counts follow it.
func (s *service) reviewResult(
	ctx context.Context, cfg *config.Config, task *domain.Task,
) (api.ReviewResult, error) {
	record, err := s.loadReview(ctx, task)
	if err != nil {
		return api.ReviewResult{}, err
	}

	var notes []string
	for i := range task.Repositories {
		binding := &task.Repositories[i]
		comparison, err := s.git.Compare(ctx, git.ObserveRequest{
			WorktreePath: binding.WorktreePath,
			BaseRef:      binding.BaseRef,
			BaseCommit:   binding.BaseCommit,
			Now:          s.now(),
		})
		if err != nil {
			// One repository nobody can read must not take the review of the
			// others with it: a worktree that has been removed by hand is
			// exactly what a user opens review to find out about.
			notes = append(notes, fmt.Sprintf("repository %s could not be compared against its base: %v",
				binding.RepositoryID, err))
			continue
		}

		if err := task.ObserveRepository(binding.RepositoryID, comparison.Observation, s.now()); err != nil {
			return api.ReviewResult{}, err
		}
		if err := record.SummarizeRepository(domain.RepositoryChange{
			RepositoryID: binding.RepositoryID,
			BaseCommit:   binding.BaseCommit,
			HeadCommit:   comparison.HeadCommit,
			ChangedFiles: comparison.Observation.ChangedFiles,
			Insertions:   comparison.Insertions,
			Deletions:    comparison.Deletions,
			Dirty:        comparison.Observation.Dirty,
			SummarizedAt: s.now(),
		}, s.now()); err != nil {
			return api.ReviewResult{}, err
		}
		if comparison.Untracked > 0 {
			notes = append(notes, fmt.Sprintf(
				"%s has %d untracked file(s): they are counted as changed and their lines are not, "+
					"because counting them would mean adding them to the index",
				binding.RepositoryID, comparison.Untracked))
		}
	}

	if err := s.store.Tasks().Save(ctx, task); err != nil {
		return api.ReviewResult{}, err
	}
	if err := s.store.Reviews().Save(ctx, store.Ref(task), record); err != nil {
		return api.ReviewResult{}, err
	}

	commands, commandNotes := s.reviewCommands(cfg, task)
	return api.ReviewResult{
		Task:     task,
		Review:   record,
		Commands: commands,
		Notes:    append(notes, commandNotes...),
	}, nil
}

// loadReview returns a task's review, creating a pending one for a task that has
// never had one.
//
// A task nobody has reviewed and a task somebody left pending are the same
// state, and the aggregate is where the per-repository summaries go whether or
// not the agent has asked for anything.
func (s *service) loadReview(ctx context.Context, task *domain.Task) (*domain.Review, error) {
	record, err := s.store.Reviews().Load(ctx, store.Ref(task))
	if errors.Is(err, store.ErrNotFound) {
		return domain.NewReview(task.ID, s.now())
	}
	if err != nil {
		return nil, err
	}
	return record, nil
}

// reviewCommands expands the project's configured diff, editor, and status
// commands for every repository the task holds.
//
// Expansion happens here because the placeholder vocabulary belongs to
// internal/config, which validates it (ADR-029); whether the result may run is
// internal/review's, which is where slice 11's third acceptance criterion is
// checked. A command that is refused becomes a note rather than a failure: the
// other repositories' commands are still usable, and a user who cannot open a
// diff is better served by being told why than by an empty screen.
func (s *service) reviewCommands(cfg *config.Config, task *domain.Task) ([]api.ReviewCommand, []string) {
	worktrees := make([]string, 0, len(task.Repositories))
	for _, binding := range task.Repositories {
		if binding.WorktreePath != "" {
			worktrees = append(worktrees, binding.WorktreePath)
		}
	}

	var commands []api.ReviewCommand
	var notes []string

	for _, binding := range task.Repositories {
		for _, configured := range []struct {
			kind  review.Kind
			value config.Command
		}{
			{review.KindDiff, cfg.Review.Diff},
			{review.KindEditor, cfg.Review.Editor},
			{review.KindStatus, cfg.Review.Status},
		} {
			if configured.value.Empty() {
				// An unconfigured editor is the documented case: it defaults to
				// $EDITOR, which the client resolves because the daemon's
				// environment is not the user's terminal's (FR-REV-003).
				continue
			}

			vector, err := expandCommand(configured.value.Command, task, binding)
			if err != nil {
				notes = append(notes, fmt.Sprintf("the %s command of %s could not be expanded: %v",
					configured.kind, binding.RepositoryID, err))
				continue
			}
			command, err := review.New(review.Request{
				Kind:         configured.kind,
				RepositoryID: binding.RepositoryID,
				Vector:       vector,
				Directory:    binding.WorktreePath,
				Worktrees:    worktrees,
			})
			if err != nil {
				notes = append(notes, err.Error())
				continue
			}
			commands = append(commands, api.ReviewCommand{
				Kind:         string(command.Kind),
				RepositoryID: command.RepositoryID.String(),
				Program:      command.Program,
				Arguments:    command.Arguments,
				Directory:    command.Directory,
			})
		}
	}
	return commands, notes
}

// expandCommand fills one configured command's placeholders for one repository.
//
// The program is never expanded, which configuration validation already refuses
// to allow: an expanded value deciding which executable runs is the one thing a
// template must not be able to do.
func expandCommand(template []string, task *domain.Task, binding domain.TaskRepository) ([]string, error) {
	values := config.Values{
		ProjectID:      task.ProjectID.String(),
		TaskID:         task.ID.String(),
		TaskKey:        task.Key().String(),
		RepositoryID:   binding.RepositoryID.String(),
		Slug:           config.Slug(task.Title),
		RepositoryPath: binding.WorktreePath,
		BaseCommit:     binding.BaseCommit,
		Branch:         binding.Branch,
	}

	expanded := make([]string, 0, len(template))
	for i, argument := range template {
		if i == 0 {
			expanded = append(expanded, argument)
			continue
		}
		value, err := config.Expand(argument, values)
		if err != nil {
			return nil, err
		}
		expanded = append(expanded, value)
	}
	return expanded, nil
}

// decide records the user's review decision.
//
// The three decisions are FR-REV-004's, and each moves two things that are
// deliberately separate: the review's own status, which is what the user said,
// and the task's workflow state, which is where the work now is. Nothing else
// moves — no container is stopped, no service is started, and no resource is
// removed.
func (s *service) decide(ctx context.Context, task *domain.Task, action api.ReviewAction) error {
	record, err := s.loadReview(ctx, task)
	if err != nil {
		return err
	}

	var status domain.ReviewStatus
	var workflow domain.WorkflowState
	var detail string
	switch action {
	case api.ReviewApprove:
		status, workflow = domain.ReviewApproved, domain.WorkflowApproved
		detail = "approved by the user"
	case api.ReviewRequestChanges:
		status, workflow = domain.ReviewChangesRequested, domain.WorkflowChangesRequested
		detail = "the user asked for changes"
	case api.ReviewLeavePending:
		status, detail = domain.ReviewPending, "left pending by the user"
	default:
		return fmt.Errorf("%w: %q is not a review decision", api.ErrInvalid, action)
	}

	if workflow != "" && task.Workflow != workflow {
		if !task.Workflow.CanTransitionTo(workflow) {
			return fmt.Errorf("%w: task %s is %s, and a review decision applies to a task whose agent has asked "+
				"for review. Nothing was recorded",
				api.ErrInvalid, task.ID, task.Workflow)
		}
		if err := s.transition(ctx, task, workflow, detail); err != nil {
			return fmt.Errorf("%w: %w", api.ErrInvalid, err)
		}
	}

	previous := record.Status
	if err := record.Decide(status, s.now()); err != nil {
		return err
	}
	if err := s.store.Reviews().Save(ctx, store.Ref(task), record); err != nil {
		return err
	}
	s.record(ctx, task, domain.Event{
		Type:   domain.EventReviewChanged,
		From:   string(previous),
		To:     string(status),
		Detail: detail,
	})
	return nil
}

// verifyNow runs the completion gate because the user asked for it.
//
// It is the recovery path as much as a convenience: a gate interrupted by a
// daemon restart leaves a task back in review_requested with an event saying so,
// and this is how it is run again. Recovery is offered and never automatic,
// which is the rule every other lifecycle in Feat follows.
func (s *service) verifyNow(ctx context.Context, task *domain.Task) error {
	switch task.Workflow {
	case domain.WorkflowReviewRequested, domain.WorkflowVerificationFailed:
	case domain.WorkflowVerifying:
		return fmt.Errorf("%w: task %s is already verifying", api.ErrInvalid, task.ID)
	default:
		return fmt.Errorf("%w: task %s is %s, and the checks run for a task whose agent has asked for review",
			api.ErrInvalid, task.ID, task.Workflow)
	}

	if task.Workflow == domain.WorkflowVerificationFailed {
		// Back to where the request was, so that the gate starts from the state
		// it always starts from. The edge exists for exactly this and for the
		// agent that fixed what the gate caught (ADR-036).
		if err := s.transition(ctx, task, domain.WorkflowReviewRequested,
			"the user asked for the configured checks to run again"); err != nil {
			return fmt.Errorf("%w: %w", api.ErrInvalid, err)
		}
	}
	if !s.startGate(ctx, task, "") {
		return fmt.Errorf("%w: the checks of task %s are already running", api.ErrInvalid, task.ID)
	}
	return nil
}

// gates records which tasks have a gate running, so that two review requests in
// quick succession do not run a project's test suite twice at once.
type gates struct {
	mu      sync.Mutex
	running map[domain.TaskID]bool
	// done is closed once per finished run, for a test that needs to wait for
	// one without sleeping.
	done chan domain.TaskID
}

func newGates() *gates {
	return &gates{running: make(map[domain.TaskID]bool), done: make(chan domain.TaskID, 16)}
}

// claim reserves the gate of one task, reporting false when one is already
// running.
func (g *gates) claim(id domain.TaskID) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.running[id] {
		return false
	}
	g.running[id] = true
	return true
}

// release ends one task's run and announces it.
func (g *gates) release(id domain.TaskID) {
	g.mu.Lock()
	delete(g.running, id)
	g.mu.Unlock()

	select {
	case g.done <- id:
	default:
	}
}

// startGate runs a task's configured checks in the background.
//
// It is background work because a check is a test suite: running it inside the
// control poller would stop every other task's messages being read for as long
// as it took. The run therefore outlives the request that started it, which is
// what makes verifying a state a user can see rather than a pause.
func (s *service) startGate(ctx context.Context, task *domain.Task, request string) bool {
	if !s.gate.claim(task.ID) {
		return false
	}

	id := task.ID
	// A fresh context: the one that delivered the review request is finished
	// long before a test suite is.
	background := context.WithoutCancel(ctx)
	go func() {
		defer s.gate.release(id)

		bounded, cancel := context.WithTimeout(background, review.GateTimeout+time.Minute)
		defer cancel()

		if err := s.runGate(bounded, id, request); err != nil {
			s.logger.ErrorContext(bounded, "running a task's configured checks",
				slog.String("task", id.String()), slog.Any("error", err))
		}
	}()
	return true
}

// runGate moves a task through verifying and records what the checks reported.
//
// The lock is taken twice rather than held throughout: what it protects is a
// load-change-save cycle, and the checks between the two cycles are a test suite
// that runs for minutes. Everything the second half acts on is therefore read
// again, because a task can move while its checks run — a user can approve it,
// and the agent can carry on working.
func (s *service) runGate(ctx context.Context, id domain.TaskID, request string) error {
	checks, skipped, ok, err := s.beginGate(ctx, id, request)
	if err != nil || !ok {
		return err
	}

	task, err := s.Task(ctx, id)
	if err != nil {
		return err
	}
	results := s.gateRunner(ctx, task).Run(ctx, checks)
	results = append(results, skipped...)

	return s.finishGate(ctx, id, request, results)
}

// beginGate records that the checks are running and returns what to run.
//
// It reports false when there is nothing to do: a task that moved between the
// request and this run, or a project with no checks for the repositories this
// task holds — which is what docs/02-user-workflows.md §6 describes as a project
// with no completion gate, where the review request stands and a person decides.
func (s *service) beginGate(
	ctx context.Context, id domain.TaskID, request string,
) (run []review.Check, skipped []domain.Check, ok bool, err error) {
	defer s.locks.lock(id)()

	task, cfg, err := s.reviewTask(ctx, id)
	if err != nil {
		return nil, nil, false, err
	}
	if task.Workflow != domain.WorkflowReviewRequested {
		// Something moved the task between the request and this run. The gate
		// does not drag it back.
		return nil, nil, false, nil
	}

	run, skipped = s.taskChecks(cfg, task)
	if len(run) == 0 && len(skipped) == 0 {
		return nil, nil, false, s.answer(task, request, control.VerificationSkipped,
			"Feat ran no checks: this project configures none for the repositories this task holds. "+
				"Your review request is recorded and a person will look at it.")
	}

	if err := s.answer(task, request, control.VerificationAccepted,
		"Feat is running the project's configured checks."); err != nil {
		return nil, nil, false, err
	}
	if err := s.transition(ctx, task, domain.WorkflowVerifying,
		fmt.Sprintf("running %d configured check(s)", len(run))); err != nil {
		return nil, nil, false, err
	}
	return run, skipped, true, nil
}

// finishGate records what the checks reported and decides where the task lands.
func (s *service) finishGate(
	ctx context.Context, id domain.TaskID, request string, results []domain.Check,
) error {
	defer s.locks.lock(id)()

	task, err := s.Task(ctx, id)
	if err != nil {
		return err
	}
	verdict := review.Decide(results)

	record, err := s.loadReview(ctx, task)
	if err != nil {
		return err
	}
	if err := record.RecordChecks(results, s.now()); err != nil {
		return err
	}
	if err := s.store.Reviews().Save(ctx, store.Ref(task), record); err != nil {
		return err
	}
	s.record(ctx, task, domain.Event{
		Type:   domain.EventReviewChanged,
		To:     string(record.Status),
		Detail: "Feat ran the project's configured checks: " + verdict.Summary,
	})

	status := control.VerificationFailed
	if verdict.Passed {
		status = control.VerificationPassed
	}

	// The results are recorded whatever the task did meanwhile; the transition
	// is only for a task that is still where the gate left it. A user who
	// approved while the suite ran has decided, and a gate must not undo that.
	if task.Workflow == domain.WorkflowVerifying {
		next := domain.WorkflowVerificationFailed
		if verdict.Passed {
			next = domain.WorkflowReadyForReview
		}
		if err := s.transition(ctx, task, next, verdict.Summary); err != nil {
			return err
		}
		s.notifyChange(ctx, task, true, false)
	}

	return s.answer(task, request, status, gateReport(results, verdict))
}

// gateRunner builds the gate for one task.
//
// The host runner is this package's only choice; the agent's is whatever the
// task's own execution environment turns out to be, which is a container for a
// devcontainer task and this host for a host-native one. A task whose
// environment cannot be rebuilt gets a gate with no agent runner, and its agent
// checks are recorded as not having run rather than as having passed.
func (s *service) gateRunner(ctx context.Context, task *domain.Task) review.Gate {
	host := s.checks
	if host == nil {
		host = review.HostRunner{}
	}
	gate := review.Gate{Host: host, Now: s.now}

	if task.Session != nil && task.Session.Execution != nil {
		environment, err := s.environmentFor(task)
		if err != nil {
			s.logger.WarnContext(ctx, "rebuilding a task's execution environment to run its checks",
				slog.String("task", task.ID.String()), slog.Any("error", err))
			return gate
		}
		gate.Agent = containerChecks{environment: environment}
		return gate
	}

	// A host-native agent's environment is this host, so a check configured to
	// run where the agent runs runs here.
	gate.Agent = host
	return gate
}

// gateFor describes the completion gate to the provider adapter.
//
// The adapter needs to know two things: whether a review request will be
// answered at all, and how long the agent should wait for the answer. Both are
// facts about this task rather than about Claude, which is why they are in the
// neutral request rather than in the adapter (ADR-036).
func (s *service) gateFor(cfg *config.Config, task *domain.Task) agent.Gate {
	checks, skipped := s.taskChecks(cfg, task)
	if len(checks) == 0 {
		// A project whose only checks belong to repositories this task holds
		// read-only has nothing to run, so the agent must not wait for a
		// verdict: the request is recorded and a person decides from here. The
		// skipped results are still recorded when review is opened.
		_ = skipped
		return agent.Gate{}
	}

	names := make([]string, 0, len(checks))
	for _, check := range checks {
		names = append(names, check.ID+" ("+check.RepositoryID.String()+")")
	}
	return agent.Gate{
		Configured:  true,
		Acknowledge: gateAcknowledge,
		// The gate's own bound plus enough slack that a run which finished is
		// never missed by the thing waiting for it.
		Verdict:  review.GateTimeout + time.Minute,
		Describe: "It runs: " + strings.Join(names, ", ") + ".",
	}
}

// gateWillRun reports whether a completion gate will answer this task's review
// request.
//
// It is asked by the notification policy, which is the one place that has to
// know before the gate has started: a task whose checks are about to run has not
// arrived with the user yet.
func (s *service) gateWillRun(task *domain.Task) bool {
	cfg, err := config.Load(s.layout.ProjectConfigDir(), task.ProjectID.String(), s.configOptions())
	if err != nil {
		return false
	}
	checks, _ := s.taskChecks(cfg, task)
	return len(checks) > 0
}

// gateAcknowledge is how long the agent waits to hear that Feat has its request
// at all.
//
// It bounds the case where nothing is listening — a daemon that was stopped
// between the launch and the request — so that a session waits for a minute
// rather than for the gate's whole bound before carrying on.
const gateAcknowledge = time.Minute

// taskChecks resolves the project's configured checks for one task.
//
// Only repositories the task holds read-write are checked. A read-only binding
// holds code this task cannot have changed, so running its suite would spend
// minutes to learn nothing — and the check is recorded as skipped naming that
// reason, because a check that did not run is never simply absent (ADR-036).
func (s *service) taskChecks(cfg *config.Config, task *domain.Task) (run []review.Check, skipped []domain.Check) {
	for _, binding := range task.Repositories {
		configured := cfg.Checks[binding.RepositoryID.String()]
		for _, check := range configured {
			resolved := review.Check{
				ID:           check.ID,
				RepositoryID: binding.RepositoryID,
				OnHost:       check.Execution == config.ExecutionHost,
			}
			if binding.Access != domain.TaskAccessReadWrite {
				skipped = append(skipped, review.Skip(resolved,
					"this task holds "+binding.RepositoryID.String()+" read-only, so its checks cannot have been "+
						"affected by the task's work", s.now()))
				continue
			}

			vector, err := expandCommand(check.Command, task, binding)
			if err != nil {
				skipped = append(skipped, review.Skip(resolved,
					"the check command could not be expanded: "+err.Error(), s.now()))
				continue
			}
			resolved.Program = vector[0]
			resolved.Arguments = vector[1:]
			resolved.Directory = s.checkDirectory(binding, resolved.OnHost)
			if resolved.Directory == "" {
				skipped = append(skipped, review.Skip(resolved,
					"repository "+binding.RepositoryID.String()+" has no path in the environment this check runs in",
					s.now()))
				continue
			}
			run = append(run, resolved)
		}
	}
	return run, skipped
}

// checkDirectory is where one check runs, in the terms of whoever runs it.
//
// A host check runs in the task worktree; an agent check runs at the container
// path the project mounts that worktree at, when the agent is in a container,
// and in the worktree when it is not.
func (s *service) checkDirectory(binding domain.TaskRepository, onHost bool) string {
	if onHost || binding.ContainerPath == "" {
		return binding.WorktreePath
	}
	return binding.ContainerPath
}

// answer writes the gate's verdict where a waiting agent will find it.
//
// A review request the agent made through the generated helper is a command the
// agent is still waiting on, and this is what ends that wait. A run the user
// asked for has nobody waiting, and writing a verdict named after a request that
// does not exist would leave a file nothing reads.
func (s *service) answer(task *domain.Task, request, status, report string) error {
	if request == "" {
		return nil
	}
	workspace, err := s.controlWorkspace(task)
	if err != nil {
		return err
	}
	return workspace.WriteVerification(request, control.Verification{Status: status, Report: report})
}

// gateReport renders what to tell the agent.
//
// It names every check that did not pass and carries what it printed, because
// the agent is about to act on it: a report that said "2 failed" and nothing
// else would send the session back to run the suite again to find out what.
func gateReport(results []domain.Check, verdict review.Verdict) string {
	if verdict.Passed {
		return "Feat ran the project's configured checks and they passed: " + verdict.Summary + "."
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Feat ran the project's configured checks: %s.\n", verdict.Summary)
	for _, result := range results {
		if result.Status == domain.CheckPassed || result.Status == domain.CheckSkipped {
			continue
		}
		fmt.Fprintf(&b, "\ncheck %s", result.ID)
		if result.RepositoryID != "" {
			fmt.Fprintf(&b, " (%s)", result.RepositoryID)
		}
		fmt.Fprintf(&b, ": %s\n", result.Status)
		if detail := strings.TrimSpace(result.Detail); detail != "" {
			b.WriteString(detail + "\n")
		}
	}
	return b.String()
}

// recoverGates reports the tasks whose checks were interrupted by a restart.
//
// A gate does not survive the process that started it, so a task recorded as
// verifying is a task claiming that checks are running when nothing is. It goes
// back to where the request was, with an event saying what happened; running
// them again is an action the user takes, because recovery in Feat is offered
// and never automatic (FR-STATE-004's reasoning, ADR-036).
func (s *service) recoverGates(ctx context.Context) {
	tasks, err := s.Tasks(ctx)
	if err != nil {
		s.logger.WarnContext(ctx, "listing tasks to recover interrupted checks", slog.Any("error", err))
		return
	}

	for _, task := range tasks {
		if task.Workflow != domain.WorkflowVerifying {
			continue
		}
		if err := s.transition(ctx, task, domain.WorkflowReviewRequested,
			"the configured checks were interrupted by a daemon restart and did not finish; "+
				"run them again from review"); err != nil {
			s.logger.ErrorContext(ctx, "recovering an interrupted gate",
				slog.String("task", task.ID.String()), slog.Any("error", err))
		}
	}
}

// containerChecks runs a check inside a task's execution environment.
//
// It is the seam ADR-032 left and slice 8 filled for probes, used here for
// something that takes minutes rather than milliseconds: the check runs where
// the agent runs, as the agent's own user, which is what "verified in the
// environment the work was done in" means. Neither adapter learns about the
// other — this shim is the daemon's, as containerRunner is.
type containerChecks struct{ environment execution.Environment }

var _ review.Runner = containerChecks{}

// Run executes one check inside the environment.
func (c containerChecks) Run(ctx context.Context, check review.Check) (review.Output, error) {
	output, err := c.environment.Run(ctx, execution.Command{
		Program:   check.Program,
		Arguments: check.Arguments,
		Directory: check.Directory,
	})
	result := review.Output{Stdout: output.Stdout, Stderr: output.Stderr, ExitCode: output.ExitCode}
	if err != nil {
		if isMissing(err) {
			return result, fmt.Errorf("%s is not installed in the agent's environment", check.Program)
		}
		return result, err
	}
	return result, nil
}
