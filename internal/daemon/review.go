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
	"github.com/ma8el/feat/internal/notify"
	"github.com/ma8el/feat/internal/review"
	"github.com/ma8el/feat/internal/store"
)

// Review performs one review action for a task. Every action is a user's explicit
// request, as every runtime action is, and nothing here starts, stops, or removes
// anything: approving a task is a statement about the work, and the environment
// it was tested in is the user's to keep (FR-REV-004, docs/02-user-workflows.md §7).
func (s *service) Review(
	ctx context.Context, id domain.TaskID, action api.ReviewAction,
) (api.ReviewResult, error) {
	if !action.Valid() {
		return api.ReviewResult{}, fmt.Errorf("%w: %q is not a review action", api.ErrInvalid, action)
	}

	// Held for the whole action, because every one of them reads the task and its
	// review, changes part of them, and saves. A gate finishing in the middle would
	// have its results overwritten by the copy this request loaded first (ADR-036).
	defer s.locks.lock(id)()

	task, cfg, err := s.reviewTask(ctx, id)
	if err != nil {
		return api.ReviewResult{}, err
	}

	// Observing is the only action that asks Git anything, and it is what opening
	// review does. Verifying is the other, and there is no third: recording a
	// decision left with the states it recorded (ADR-086).
	if action == api.ReviewVerify {
		if err := s.verifyNow(ctx, cfg, task); err != nil {
			return api.ReviewResult{}, err
		}
	}

	return s.reviewResult(ctx, task)
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

// reviewResult observes every repository and returns what review shows. The
// comparison is taken now rather than read from the record, because a screen
// showing what Feat saw an hour ago would describe a worktree the agent has been
// writing to since. What was just observed is then recorded, so the summary
// survives a restart.
func (s *service) reviewResult(ctx context.Context, task *domain.Task) (api.ReviewResult, error) {
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
			// One repository nobody can read must not take the review of the others
			// with it: a worktree removed by hand is exactly what a user opens
			// review to find out about.
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

	commands, commandNotes := s.reviewCommands(task)
	return api.ReviewResult{
		Task:     task,
		Review:   record,
		Commands: commands,
		Notes:    append(notes, commandNotes...),
	}, nil
}

// loadReview returns a task's review, creating a pending one for a task that has
// never had one. A task nobody has reviewed and a task somebody left pending are
// the same state, and the aggregate holds the per-repository summaries whether or
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

// reviewCommands expands the configured diff, editor, and status commands for
// every repository the task holds.
//
// The commands are the machine's rather than the project's, because they are the
// user's own tools and one person opens a diff the same way whichever project it
// belongs to (ADR-079). What is per task is what they expand against: the
// worktree, the base commit, the branch.
//
// Expansion happens here because the placeholder vocabulary belongs to
// internal/config, which validates it (ADR-029); whether the result may run is
// internal/review's. A refused command becomes a note rather than a failure, so
// the other repositories' commands stay usable.
func (s *service) reviewCommands(task *domain.Task) ([]api.ReviewCommand, []string) {
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
			{review.KindDiff, s.settings.Review.Diff},
			{review.KindEditor, s.settings.Review.Editor},
			{review.KindStatus, s.settings.Review.Status},
		} {
			if configured.value.Empty() {
				// An unconfigured editor is the documented case: it defaults to
				// $EDITOR, which the client resolves because the daemon's environment
				// is not the user's terminal's (FR-REV-003).
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
// The program is never expanded, which configuration validation already refuses
// to allow: a template must not decide which executable runs.
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

// verifyNow runs the completion gate because the user asked for it.
//
// It is the recovery path as much as a convenience: a gate interrupted by a
// daemon restart leaves a task back in review_requested with an event saying so,
// and this is how it is run again. Recovery is offered and never automatic.
//
// It runs from either outcome of a gate rather than only from the failing one,
// because a user reading work that passed and changing something has the same
// question as one reading work that failed (ADR-087).
func (s *service) verifyNow(ctx context.Context, cfg *config.Config, task *domain.Task) error {
	switch task.Workflow {
	case domain.WorkflowReviewRequested, domain.WorkflowReadyForReview, domain.WorkflowVerificationFailed:
	case domain.WorkflowVerifying:
		return fmt.Errorf("%w: task %s is already verifying", api.ErrInvalid, task.ID)
	default:
		// The rule first and this task's state second, because the rule is the part
		// the reader does not already have. The dashboard shows the workflow on the
		// panel above this line and `feat review` in the row above; neither shows
		// the rule anywhere.
		return fmt.Errorf("%w: checks can only run for a task whose agent has asked for review, and task %s is %s",
			api.ErrInvalid, task.ID, task.Workflow)
	}

	// Asked before anything moves, because the answer decides whether moving is
	// safe. A project that configures no checks for this task's repositories reaches
	// ready_for_review without a gate, and a run started from there would put the
	// task back in review_requested with nothing to run and no gate to bring it out.
	if run, skipped := s.taskChecks(cfg, task); len(run) == 0 && len(skipped) == 0 {
		return fmt.Errorf("%w: this project configures no checks for the repositories task %s holds, "+
			"so there is nothing to run. Nothing was changed", api.ErrInvalid, task.ID)
	}

	if task.Workflow != domain.WorkflowReviewRequested {
		// Back to where the request was, so the gate starts from the state it always
		// starts from. The edge exists for this and for the agent that fixed what
		// the gate caught (ADR-036, ADR-087).
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

// gates records which tasks have a gate running, so two review requests in quick
// succession do not run a project's test suite twice at once.
//
// It also owns their lifetime. A gate is the only work in the daemon that
// outlives the request that started it and writes a task's records afterwards, so
// it is the only work that can still be writing when everything else has stopped.
// Serve applies the same rule to a pending idle transition.
type gates struct {
	mu      sync.Mutex
	running map[domain.TaskID]context.CancelFunc
	// finished waits for the goroutines themselves, because cancelling a check
	// only asks: the run still has to unwind and record what it found.
	finished sync.WaitGroup
	// done is closed once per finished run, for a test that needs to wait for
	// one without sleeping.
	done chan domain.TaskID
}

func newGates() *gates {
	return &gates{
		running: make(map[domain.TaskID]context.CancelFunc),
		done:    make(chan domain.TaskID, 16),
	}
}

// claim reserves the gate of one task, reporting false when one is already
// running. The cancel it is given ends that run's checks.
func (g *gates) claim(id domain.TaskID, cancel context.CancelFunc) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	if _, running := g.running[id]; running {
		return false
	}
	g.running[id] = cancel
	g.finished.Add(1)
	return true
}

// isRunning reports whether this daemon is running the task's gate. It is what
// tells an interrupted gate from a live one: Reconcile is an API request the
// dashboard makes on a key press, on a resume, on a stop, and after every cleanup
// action, and a task whose checks are running then is recovering from nothing
// (ADR-096).
func (g *gates) isRunning(id domain.TaskID) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	_, running := g.running[id]
	return running
}

// release ends one task's run and announces it.
func (g *gates) release(id domain.TaskID) {
	g.mu.Lock()
	delete(g.running, id)
	g.mu.Unlock()

	g.finished.Done()
	select {
	case g.done <- id:
	default:
	}
}

// stopAll ends every running gate and waits for what they were doing.
//
// Cancelling is what kills the check itself, because a configured check is
// somebody's test suite and a daemon that exited would leave the process behind.
// What is waited for afterwards is the bookkeeping, which is local file writes, so
// this returns in milliseconds.
//
// A gate stopped this way is an interrupted gate: the task is left in verifying,
// and the next startup puts it back where the review request was with an event
// saying why (ADR-036).
func (g *gates) stopAll() {
	g.mu.Lock()
	for _, cancel := range g.running {
		cancel()
	}
	g.mu.Unlock()

	g.finished.Wait()
}

// startGate runs a task's configured checks in the background. A check is a test
// suite, so running it inside the control poller would stop every other task's
// messages being read for as long as it took. The run outlives the request that
// started it, which is what makes verifying a state a user can see.
func (s *service) startGate(ctx context.Context, task *domain.Task, request string) bool {
	// A fresh context: the one that delivered the review request is finished long
	// before a test suite is. It is cancellable all the same, because the run
	// belongs to the daemon's lifetime (gates.stopAll).
	bounded, cancel := context.WithTimeout(
		context.WithoutCancel(ctx), review.GateTimeout+time.Minute)

	id := task.ID
	if !s.gate.claim(id, cancel) {
		cancel()
		return false
	}

	go func() {
		defer s.gate.release(id)
		defer cancel()

		if err := s.runGate(bounded, id, request); err != nil {
			s.logger.ErrorContext(bounded, "running a task's configured checks",
				slog.String("task", id.String()), slog.Any("error", err))
		}
	}()
	return true
}

// runGate moves a task through verifying and records what the checks reported.
// The lock is taken twice rather than held throughout, because it protects a
// load-change-save cycle and the checks between the two cycles run for minutes.
// The second half reads everything again, because a task can move while its
// checks run.
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

	if ctx.Err() != nil {
		// The daemon is stopping, so these results are what a cancelled run produced
		// rather than what the checks reported, and Decide reads them as not
		// passing. Recording them would fail a task because Feat was restarted. It
		// is left verifying, which recoverGates knows how to explain (ADR-036).
		return ctx.Err()
	}
	return s.finishGate(ctx, id, request, results)
}

// beginGate records that the checks are running and returns what to run. It
// reports false when there is nothing to do: a task that moved between the
// request and this run, or a project with no checks for the repositories this
// task holds, which docs/02-user-workflows.md §6 describes as a project with no
// completion gate.
func (s *service) beginGate(
	ctx context.Context, id domain.TaskID, request string,
) (run []review.Check, skipped []domain.Check, ok bool, err error) {
	defer s.locks.lock(id)()

	task, err := s.Task(ctx, id)
	if err != nil {
		return nil, nil, false, err
	}
	if task.Workflow != domain.WorkflowReviewRequested {
		// Something moved the task between the request and this run. The gate
		// does not drag it back.
		return nil, nil, false, nil
	}

	cfg, err := config.Load(s.layout.ProjectConfigDir(), task.ProjectID.String(), s.configOptions())
	if err != nil {
		// The gate cannot start, which is not the same as there being nothing to
		// run. It is recorded against the task rather than only in the daemon's
		// log, because a project file mid-edit leaves a task in review_requested
		// with no verdict and an agent waiting out its acknowledge timeout
		// (ADR-096).
		return nil, nil, false, s.blockGate(ctx, task, request, translateConfig(err))
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

// blockGate records that a task's checks could not be started at all.
//
// It is the landing ADR-055 defined for a run that established nothing, reached
// one step earlier. Nothing was run, so nothing is claimed about the work and the
// review request stands. The user is told rather than the agent, because the
// project's configuration or the checks' environment is theirs to fix.
//
// The waiting agent is answered too. A review request made through the generated
// helper is a command the session is blocked on, and a gate that failed silently
// would leave it there until the acknowledge timeout expires.
func (s *service) blockGate(ctx context.Context, task *domain.Task, request string, cause error) error {
	detail := "Feat could not run the project's configured checks: " + cause.Error() +
		". The review request stands, and nothing was established about the work"
	s.record(ctx, task, domain.Event{Type: domain.EventReviewChanged, Detail: detail})
	s.notifyTask(ctx, task, notify.ConditionVerificationBlocked, 0)
	return s.answer(task, request, control.VerificationBlocked, blockedReport(cause.Error()))
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
	landing := gateLanding(results, verdict)

	// A task the user cleaned up and archived while its checks ran. The worktree
	// the checks ran in is gone, the session that would read the verdict is gone
	// with it, and `answer` recreates the tree it writes into, which left a control
	// workspace behind 115ms after cleanup removed it (ADR-036 evidence 12).
	//
	// It is recorded rather than dropped in silence, because a user who watched a
	// gate start and then archived the task is owed the reason no verdict appeared.
	if task.Workflow == domain.WorkflowArchived {
		s.record(ctx, task, domain.Event{
			Type: domain.EventReviewChanged,
			Detail: "the task was archived while its checks were running, so their results were discarded: " +
				verdict.Summary,
		})
		return nil
	}

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
		Detail: landing.detail,
	})

	// The results are recorded whatever the task did meanwhile, and the transition
	// is only for a task still where the gate left it. A user who approved while the
	// suite ran has decided, and a gate must not undo that.
	if task.Workflow == domain.WorkflowVerifying {
		if err := s.transition(ctx, task, landing.workflow, landing.detail); err != nil {
			return err
		}
		// The condition is named here rather than looked up from the task's new
		// state, because a blocked gate leaves it in review_requested and that
		// state's own condition is the one a gate about to run suppresses.
		s.notifyTask(ctx, task, landing.condition, 0)
	}

	return s.answer(task, request, landing.status, gateReport(results, verdict))
}

// landing is where one gate run leaves the task, and what it says about it.
type landing struct {
	// workflow is the state a task still in verifying moves to.
	workflow domain.WorkflowState
	// condition is what the user is interrupted for.
	condition notify.Condition
	// status is what the waiting agent's helper reads.
	status string
	// detail is the one line the task's history carries. It names the checks that
	// did not run and never what a check printed, because a check's output is
	// bounded into the review record and kept out of the event stream (ADR-036
	// evidence 6).
	detail string
}

// gateLanding decides all four together, because they are one decision.
//
// A check that failed and a check that never ran both produced
// verification_failed until ADR-055, so a project whose check command was missing
// was told its work had failed and the agent was handed a configuration it could
// not fix. A run that established nothing now says so, and lands where a review
// request with no verdict always lands.
func gateLanding(results []domain.Check, verdict review.Verdict) landing {
	switch verdict.Outcome {
	case review.OutcomePassed:
		return landing{
			workflow:  domain.WorkflowReadyForReview,
			condition: notify.ConditionReadyForReview,
			status:    control.VerificationPassed,
			detail:    "Feat ran the project's configured checks: " + verdict.Summary,
		}
	case review.OutcomeBlocked:
		return landing{
			// Back where the review request was. Nothing verified the work, so this is
			// the state docs/02-user-workflows.md §6 describes for a project with no
			// completion gate: the request stands, and a person decides.
			workflow:  domain.WorkflowReviewRequested,
			condition: notify.ConditionVerificationBlocked,
			status:    control.VerificationBlocked,
			detail: "Feat could not run the project's configured checks (" + verdict.Summary +
				"): " + checkNames(review.NotRun(results)) +
				". The review request stands; open review for the reason each one gave",
		}
	default:
		return landing{
			workflow:  domain.WorkflowVerificationFailed,
			condition: notify.ConditionVerificationFailed,
			status:    control.VerificationFailed,
			detail:    "Feat ran the project's configured checks: " + verdict.Summary,
		}
	}
}

// checkNames lists results as a person would name them: the configured check
// identifier and the repository it belongs to, and nothing a check printed.
//
// It is bounded, because an event is one line a user reads in a history and a
// project may configure a great many checks. The count is kept rather than
// dropped, because a list that stops without saying so reads as complete.
func checkNames(results []domain.Check) string {
	const most = 5

	names := make([]string, 0, min(len(results), most)+1)
	for _, result := range results[:min(len(results), most)] {
		name := result.ID
		if result.RepositoryID != "" {
			name += " (" + result.RepositoryID.String() + ")"
		}
		names = append(names, name)
	}
	if len(results) > most {
		names = append(names, fmt.Sprintf("and %d more", len(results)-most))
	}
	return strings.Join(names, ", ")
}

// gateRunner builds the gate for one task. The host runner is this package's only
// choice; the agent's is the task's own execution environment, which is a
// container for a devcontainer task and this host for a host-native one. A task
// whose environment cannot be rebuilt gets a gate with no agent runner, and its
// agent checks are recorded as not having run rather than as having passed.
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

// gateFor describes the completion gate to the provider adapter. It needs two
// things: whether a review request will be answered at all, and how long the
// agent should wait. Both are facts about this task rather than about Claude, so
// they travel in the neutral request rather than in the adapter (ADR-036).
func (s *service) gateFor(cfg *config.Config, task *domain.Task) agent.Gate {
	checks, skipped := s.taskChecks(cfg, task)
	if len(checks) == 0 {
		// A project whose only checks belong to repositories this task holds
		// read-only has nothing to run, so the agent must not wait for a verdict:
		// the request is recorded and a person decides. The skipped results are
		// still recorded when review is opened.
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
// request, and whether Feat can tell at all.
//
// The notification policy asks it, because it is the one place that has to know
// before the gate has started: a task whose checks are about to run has not
// arrived with the user yet.
//
// The second result separates two cases. A project that configures no checks for
// the repositories this task holds is the honest case for announcing the request
// now, because there is no later moment. A configuration Feat cannot read is not:
// the gate reaches the same file a moment later and says so itself (blockGate),
// so a notification here would be the first and wrong one of two (ADR-096).
func (s *service) gateWillRun(task *domain.Task) (will, known bool) {
	cfg, err := config.Load(s.layout.ProjectConfigDir(), task.ProjectID.String(), s.configOptions())
	if err != nil {
		return false, false
	}
	checks, _ := s.taskChecks(cfg, task)
	return len(checks) > 0, true
}

// gateAcknowledge is how long the agent waits to hear that Feat has its request
// at all. It bounds the case where nothing is listening, such as a daemon stopped
// between the launch and the request, so a session waits a minute rather than the
// gate's whole bound before carrying on.
const gateAcknowledge = time.Minute

// taskChecks resolves the project's configured checks for one task. Only
// repositories the task holds read-write are checked, because a read-only binding
// holds code this task cannot have changed. The check is recorded as skipped
// naming that reason, because a check that did not run is never simply absent
// (ADR-036).
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

// checkDirectory is where one check runs, in the terms of whoever runs it. A host
// check runs in the task worktree; an agent check runs at the container path the
// project mounts that worktree at, or in the worktree when the agent is not in a
// container.
func (s *service) checkDirectory(binding domain.TaskRepository, onHost bool) string {
	if onHost || binding.ContainerPath == "" {
		return binding.WorktreePath
	}
	return binding.ContainerPath
}

// answer writes the gate's verdict where a waiting agent will find it.
//
// A review request the agent made through the generated helper is a command the
// agent is still waiting on, and this ends that wait. A run the user asked for
// has nobody waiting, and a verdict named after a request that does not exist
// would be a file nothing reads.
//
// A workspace that is gone is the same case reached the other way. Every write in
// this package creates the directory it writes into, so answering a cleaned-up
// task would rebuild a tree the user had just confirmed the removal of (ADR-036
// evidence 12).
func (s *service) answer(task *domain.Task, request, status, report string) error {
	if request == "" {
		return nil
	}
	workspace, err := s.controlWorkspace(task)
	if err != nil {
		return err
	}
	if !workspace.Exists() {
		return nil
	}
	return workspace.WriteVerification(request, control.Verification{Status: status, Report: report})
}

// gateReport renders what to tell the agent.
//
// It names every check that did not pass and carries what it printed, because the
// agent is about to act on it: a report saying "2 failed" would send the session
// back to run the suite again to find out what.
//
// A blocked run is the one case where the agent is told there is nothing for it
// to do. The helper exits zero on it, so the session is not sent back into its
// loop over a check that never ran, and the report says why rather than leaving
// the model to work out that the failure was not its own (ADR-055).
func gateReport(results []domain.Check, verdict review.Verdict) string {
	var b strings.Builder
	switch verdict.Outcome {
	case review.OutcomePassed:
		return "Feat ran the project's configured checks and they passed: " + verdict.Summary + "."
	case review.OutcomeBlocked:
		b.WriteString(blockedReport(verdict.Summary))
	default:
		fmt.Fprintf(&b, "Feat ran the project's configured checks: %s.\n", verdict.Summary)
	}
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

// blockedReport is what the agent is told when nothing was established. The run
// that established nothing and the run that could not start share it, because the
// agent's position is identical in both: there is no verdict, the reason is not
// its own, and there is nothing to do about it.
func blockedReport(reason string) string {
	return fmt.Sprintf("Feat could not run the project's configured checks: %s.\n"+
		"Nothing has been established about your work, in either direction. This is the "+
		"project's check configuration or the environment the checks run in, which is the "+
		"user's to fix and not yours: do not change the configuration that decides how your "+
		"work is verified. Feat has told them, and your review request stands.\n", reason)
}

// containerChecks runs a check inside a task's execution environment. It is the
// seam ADR-032 left, filled for probes by the execution adapter and used here for
// something that takes minutes: the check runs where the agent runs, as the
// agent's own user. Neither adapter learns about the other, because the shim is
// the daemon's, as containerRunner is.
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
