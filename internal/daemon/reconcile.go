package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/config"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/execution/compose"
	"github.com/ma8el/feat/internal/paths"
	"github.com/ma8el/feat/internal/reconcile"
	"github.com/ma8el/feat/internal/store"
	"github.com/ma8el/feat/internal/tmux"
)

// Reconcile compares every persisted task with what the machine actually has,
// and returns what it found.
//
// It is the one recovery workflow ADR-030 said would be combined: tmux,
// worktrees, both kinds of Compose project, control messages, and review state
// are asked the same question in one pass, and the answers land in one report.
//
// Two rules hold everywhere in it. Nothing is repaired, restarted, recreated, or
// adopted — a stopped container is reported as stopped (FR-STATE-004), an
// orphan is reported before anything could adopt it, and every action a report
// suggests is one the user takes. And a step that fails does not end the pass:
// its failure is recorded as a problem and the remaining steps still run, which
// is the same quarantine rule that keeps one damaged terminal from making the
// healthy ones unreachable (ADR-037).
func (s *service) Reconcile(ctx context.Context) (api.Reconciliation, error) {
	report := &reconcile.Report{StartedAt: s.now()}

	// From what claiming the state directory read, not from the record on disk:
	// claiming has already replaced that with this run's own, and re-reading it
	// would report this daemon's start as the previous daemon's end.
	previous, err := s.previousDaemon(ctx)
	switch {
	case err == nil && previous != nil:
		report.PreviousRunEndedCleanly = previous.EndedCleanly
		report.PreviousRunStoppedAt = previous.StoppedAt
	case err == nil:
		// A state directory no daemon has owned yet. There is nothing to say
		// about a previous run, and saying "it crashed" would be wrong.
		report.PreviousRunEndedCleanly = true
	default:
		report.Fail(reconcile.Problem{Class: reconcile.ClassControl,
			Reason: "the durable daemon record could not be read: " + err.Error()})
	}

	tasks, err := s.reconcilableTasks(ctx, report)
	if err != nil {
		return api.Reconciliation{}, err
	}

	// Asked before either pass that says anything about it, and asked once. Two
	// passes report on a task whose record names no environment — the terminal
	// pass, which has to say whether anything of it is lost, and the environment
	// pass, which names what a launch left — and a report whose two halves
	// disagree about the same container is worse than either half alone.
	left := s.unrecordedEnvironments(ctx, tasks)

	s.reconcileTerminals(ctx, tasks, report, left)
	s.reconcileWorktrees(ctx, tasks, report)
	s.reconcileEnvironments(ctx, tasks, report, left)
	s.reconcileRuntimes(ctx, tasks, report)
	s.reconcileControl(ctx, tasks, report)
	s.reconcileReviews(ctx, tasks, report)

	report.FinishedAt = s.now()
	report.Sort()
	s.setReport(report)
	return renderReport(report), nil
}

// previousDaemon returns what the last run left behind.
//
// It prefers what claiming the state directory read, because by the time a pass
// runs the record on disk describes this run. A nil record and a nil error mean
// no daemon has owned this directory before.
func (s *service) previousDaemon(ctx context.Context) (*domain.DaemonRecord, error) {
	if s.startedRecord != nil {
		return s.previousRun, nil
	}
	previous, err := s.store.Daemons().Load(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return previous, nil
}

// reconcilableTasks lists every task of every project.
//
// A project whose tasks cannot be listed is a problem rather than the end of
// the pass: the other projects' tasks are still recoverable, and a user with
// one unreadable project should not lose the recovery report for the rest.
func (s *service) reconcilableTasks(ctx context.Context, report *reconcile.Report) ([]*domain.Task, error) {
	projects, err := s.store.Projects().List(ctx)
	if err != nil {
		return nil, err
	}

	var tasks []*domain.Task
	for _, project := range projects {
		owned, err := s.store.Tasks().List(ctx, project.ID)
		if err != nil {
			report.Fail(reconcile.Problem{Project: project.ID, Reason: fmt.Sprintf(
				"the tasks of project %s could not be listed: %s", project.ID, err)})
			continue
		}
		for _, task := range owned {
			// An archived task owns nothing Feat still tracks: cleanup refuses
			// to archive one that does. Reporting its removed worktrees as
			// missing every startup would be reporting the past.
			if task.Workflow == domain.WorkflowArchived {
				continue
			}
			tasks = append(tasks, task)
		}
	}
	return tasks, nil
}

// reconcileTerminals compares stored sessions with tagged tmux objects.
//
// It replaces the tmux-only pass that came before it, and keeps its behaviour: a
// stale stored identifier is repaired from live metadata, a missing terminal is
// marked stopped without anything being restarted, and a terminal whose
// metadata claims another project is reported rather than adopted. What it adds
// is that a damaged terminal is now a finding rather than the end of discovery.
func (s *service) reconcileTerminals(
	ctx context.Context, tasks []*domain.Task, report *reconcile.Report, left leftovers,
) {
	found, err := s.terminals.Discover(ctx)
	if err != nil {
		report.Fail(reconcile.Problem{Class: reconcile.ClassTerminal, Reason: fmt.Sprintf(
			"the dedicated tmux server on %s could not be read: %s", s.terminals.Socket(), err)})
		return
	}

	for _, damaged := range found.Damaged {
		report.Add(reconcile.Finding{
			Class: reconcile.ClassTerminal, Status: reconcile.StatusDamaged,
			Project: damaged.Project, Task: damaged.Task, Identity: damaged.ID,
			Detail: damaged.Reason,
			Action: "attach to the tmux server to look at it, or remove the task's terminal with `feat task cleanup`. " +
				"Other tasks are unaffected",
		})
	}

	targets := make(map[domain.TaskID]tmux.Terminal, len(found.Terminals))
	duplicated := make(map[domain.TaskID]bool)
	for _, terminal := range found.Terminals {
		if previous, exists := targets[terminal.Task]; exists {
			duplicated[terminal.Task] = true
			report.Add(reconcile.Finding{
				Class: reconcile.ClassTerminal, Status: reconcile.StatusInconsistent,
				Project: terminal.Project, Task: terminal.Task, Identity: terminal.Target.Window,
				Detail: fmt.Sprintf("tmux windows %s and %s both claim this task",
					previous.Target.Window, terminal.Target.Window),
				Action: "remove one of the two windows in tmux; Feat will not guess which is the task's",
			})
			continue
		}
		targets[terminal.Task] = terminal
	}

	known := make(map[domain.TaskID]bool, len(tasks))
	for _, task := range tasks {
		known[task.ID] = true
		if duplicated[task.ID] {
			continue
		}
		if task.Session == nil && task.Workflow != domain.WorkflowPreparing && task.Workflow != domain.WorkflowFailed {
			continue
		}
		if err := s.reconcileTerminal(ctx, task, targets, report, left); err != nil {
			report.Fail(reconcile.Problem{Class: reconcile.ClassTerminal, Project: task.ProjectID, Task: task.ID,
				Reason: "the task's terminal could not be reconciled: " + err.Error()})
		}
	}

	for _, terminal := range found.Terminals {
		if known[terminal.Task] {
			continue
		}
		report.Add(reconcile.Finding{
			Class: reconcile.ClassTerminal, Status: reconcile.StatusOrphaned,
			Project: terminal.Project, Task: terminal.Task, Identity: terminal.Target.Window,
			Detail: fmt.Sprintf("tmux window %s is tagged for task %s, which no registered project records",
				terminal.Target.Window, terminal.Task),
			Action: "register the project that owns it, or close the window in tmux. " +
				"Feat will not adopt or remove it on its own",
		})
	}
}

// reconcileTerminal reconciles one task's terminal under its own lock.
func (s *service) reconcileTerminal(
	ctx context.Context, task *domain.Task, targets map[domain.TaskID]tmux.Terminal, report *reconcile.Report,
	left leftovers,
) error {
	defer s.locks.lock(task.ID)()

	// Re-read under the lock. Nothing else writes during startup, but the
	// on-demand pass runs beside every other writer, and a load-change-save
	// cycle that started from a copy taken outside the lock is the defect
	// ADR-036 evidence 9 records.
	current, err := s.store.Tasks().Load(ctx, store.Ref(task))
	if err != nil {
		return err
	}

	terminal, live := targets[current.ID]
	if live && terminal.Project != current.ProjectID {
		report.Add(reconcile.Finding{
			Class: reconcile.ClassTerminal, Status: reconcile.StatusInconsistent,
			Project: current.ProjectID, Task: current.ID, Identity: terminal.Target.Window,
			Detail: fmt.Sprintf("the tmux window claims project %s and the task belongs to %s",
				terminal.Project, current.ProjectID),
			Action: "close the window in tmux; Feat will not adopt a terminal whose metadata disagrees with its record",
		})
		return nil
	}

	if !live {
		if current.Session == nil {
			report.Add(reconcile.Finding{
				Class: reconcile.ClassTerminal, Status: reconcile.StatusMissing,
				Project: current.ProjectID, Task: current.ID,
				Detail: "the task was confirmed but has no terminal",
				Action: confirmedWithoutTerminal(left[current.ID]),
			})
			return nil
		}
		if current.Session.Process != domain.ProcessStopped {
			if err := s.markSessionEnded(ctx, current, terminalGone); err != nil {
				return err
			}
		}
		report.Add(reconcile.Finding{
			Class: reconcile.ClassTerminal, Status: reconcile.StatusMissing,
			Project: current.ProjectID, Task: current.ID, Identity: current.Session.Tmux.Window,
			Detail: "the recorded tmux terminal is gone; the session is recorded as stopped and was not restarted",
			Action: s.resumeAction(current),
		})
		return nil
	}

	from := domain.ProcessStarting
	if current.Session == nil {
		cfg, err := config.Load(s.layout.ProjectConfigDir(), current.ProjectID.String(), s.configOptions())
		if err != nil {
			return err
		}
		session, err := domain.NewAgentSession(
			cfg.Agent.Provider, domain.ExecutionMode(cfg.Agent.Execution.Mode),
			terminal.Target, "", s.now(),
		)
		if err != nil {
			return err
		}
		if err := current.AttachSession(session, s.now()); err != nil {
			return err
		}
	} else {
		from = current.Session.Process
	}

	changed := current.Session.Tmux != terminal.Target || from != terminal.ProcessState()
	if err := current.Session.ReconcileTerminal(terminal.Target, terminal.ProcessState(), current.ID, s.now()); err != nil {
		return err
	}
	if err := s.store.Tasks().Save(ctx, current); err != nil {
		return err
	}
	if changed {
		s.record(ctx, current, domain.Event{
			Type: domain.EventReconciled, From: string(from), To: string(current.Session.Process),
			Detail: "rediscovered tagged tmux terminal " + terminal.Target.Session + "/" +
				terminal.Target.Window + "/" + terminal.Target.Pane,
		})
	}

	status := reconcile.StatusPresent
	detail := "the tagged tmux terminal is there and the recorded target matches it"
	action := ""
	if current.Session.Process == domain.ProcessFailed {
		status = reconcile.StatusInconsistent
		detail = "the agent process in the task's pane has exited with a failure"
		action = s.resumeAction(current)
	}
	report.Add(reconcile.Finding{
		Class: reconcile.ClassTerminal, Status: status,
		Project: current.ProjectID, Task: current.ID, Identity: terminal.Target.Window,
		Detail: detail, Action: action,
	})
	return nil
}

// terminalGone is why a session ended when its tmux terminal is not there.
const terminalGone = "the recorded tmux terminal was not found; it was not restarted"

// markSessionEnded records that a task's agent process is over.
//
// It is the one place that writes this observation, because three callers make
// it and they must not describe it differently: a reconciliation pass that found
// nothing where the record said, a resume that asked tmux the same question
// before acting on the answer, and a resume that found the terminal but not the
// container the agent was running in. The reason is the caller's, because only
// the caller knows which of the three it is; the state, the event, and the order
// they are written in are not.
//
// Nothing is restarted here — what the caller does next is the caller's, and
// only one of them does anything at all.
func (s *service) markSessionEnded(ctx context.Context, task *domain.Task, reason string) error {
	from := task.Session.Process
	if err := task.Session.Observe(domain.ProcessStopped, s.now()); err != nil {
		return err
	}
	if err := s.store.Tasks().Save(ctx, task); err != nil {
		return err
	}
	s.record(ctx, task, domain.Event{
		Type: domain.EventReconciled, From: string(from), To: string(domain.ProcessStopped),
		Detail: reason,
	})
	return nil
}

// resumeAction describes the recovery a dead session is eligible for.
//
// Recovery is offered and never performed: a report that restarted what it
// found would be deciding for the user, and ADR-032 deferred the decision here
// precisely so that it could be made once for every resource class.
//
// An action names something a user can actually do. The no-session branch used
// to say "start the task again from the dashboard", and there is no such
// command: nothing launches a task that is past draft. What is true is that a
// task with no recorded session has never held an agent conversation, so
// cleaning it up and preparing another loses nothing but the brief.
//
// The command is named beside the key because the key was the only route for a
// while, and a recovery a user can read about but not reach from where they are
// reading is not one the product offers (ADR-057).
func (s *service) resumeAction(task *domain.Task) string {
	if task.Session == nil || task.Session.ProviderSessionID == "" {
		return "clean it up and prepare the task again. Feat recorded no provider session to " +
			"continue, which also means no agent ever reported working in this one"
	}
	return "resume it with `feat task resume " + task.Key().String() + "`, or z in the dashboard, " +
		"which continues the recorded " + task.Session.Provider +
		" session rather than starting an empty one. Feat does not restart it on its own"
}

// reconcileWorktrees asks Git whether each task's recorded worktrees exist.
//
// Whether a worktree exists is observed rather than stored (ADR-029), so this
// asks rather than trusting a flag. Nothing is recreated: a worktree the user
// removed by hand stays removed, and the record that named it is what makes the
// removal explainable.
func (s *service) reconcileWorktrees(ctx context.Context, tasks []*domain.Task, report *reconcile.Report) {
	claimed := make(map[string]bool)
	roots := make(map[string]domain.ProjectID)

	for _, task := range tasks {
		if len(task.Repositories) == 0 {
			continue
		}
		cfg, err := config.Load(s.layout.ProjectConfigDir(), task.ProjectID.String(), s.configOptions())
		if err != nil {
			report.Fail(reconcile.Problem{Class: reconcile.ClassWorktrees, Project: task.ProjectID, Task: task.ID,
				Reason: "the project's configuration could not be read: " + err.Error()})
			continue
		}
		if root := config.StaticPrefix(cfg.Git.WorktreeRoot); root != "" {
			roots[root] = task.ProjectID
		}

		for _, binding := range task.Repositories {
			if binding.WorktreePath == "" {
				continue
			}
			claimed[filepath.Clean(binding.WorktreePath)] = true

			info, err := os.Lstat(binding.WorktreePath)
			switch {
			case err == nil && info.IsDir():
				report.Add(reconcile.Finding{
					Class: reconcile.ClassWorktrees, Status: reconcile.StatusPresent,
					Project: task.ProjectID, Task: task.ID, Identity: binding.WorktreePath,
					Detail: "the worktree of " + binding.RepositoryID.String() + " is there",
				})
			case err == nil:
				report.Add(reconcile.Finding{
					Class: reconcile.ClassWorktrees, Status: reconcile.StatusInconsistent,
					Project: task.ProjectID, Task: task.ID, Identity: binding.WorktreePath,
					Detail: "the recorded worktree of " + binding.RepositoryID.String() + " is not a directory",
					Action: "look at what is at that path; Feat will not remove something it did not create",
				})
			case os.IsNotExist(err):
				report.Add(reconcile.Finding{
					Class: reconcile.ClassWorktrees, Status: reconcile.StatusMissing,
					Project: task.ProjectID, Task: task.ID, Identity: binding.WorktreePath,
					Detail: "the worktree of " + binding.RepositoryID.String() + " is gone",
					Action: "the task's branch and its record are still here; `feat task cleanup` can tidy them",
				})
			default:
				report.Fail(reconcile.Problem{Class: reconcile.ClassWorktrees, Project: task.ProjectID, Task: task.ID,
					Reason: fmt.Sprintf("the worktree %s could not be examined: %s", binding.WorktreePath, err)})
			}
		}
	}

	s.reportOrphanWorktrees(roots, claimed, report)
}

// reportOrphanWorktrees names directories under a project's worktree root that
// no task claims.
//
// They are reported and never removed. A directory Feat created and lost track
// of and a directory the user put there look identical from here, and only one
// of them is safe to delete — so neither is.
//
// The scan descends only where a task's own paths lead. A worktree root of
// `…/worktrees/{project_id}/{task_id}` puts the interesting directories two
// levels down, and a scan that listed one level would report a live project's
// directory as an orphan while missing the stale task directory inside it —
// which is both halves of the mistake at once. Descending only into directories
// that hold something a task records bounds the walk by the template's own
// depth rather than by a number somebody chose.
func (s *service) reportOrphanWorktrees(
	roots map[string]domain.ProjectID, claimed map[string]bool, report *reconcile.Report,
) {
	for root, project := range roots {
		s.scanForOrphans(root, project, claimed, report)
	}
}

// scanForOrphans reports the directories under one root that no task claims.
func (s *service) scanForOrphans(
	dir string, project domain.ProjectID, claimed map[string]bool, report *reconcile.Report,
) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			// No worktree root yet is not a problem: it is created with the
			// first task.
			return
		}
		report.Fail(reconcile.Problem{Class: reconcile.ClassWorktrees, Project: project,
			Reason: fmt.Sprintf("the worktree root %s could not be listed: %s", dir, err)})
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		switch {
		case claimed[path]:
			// A worktree a task records. Whether it is there is reported by the
			// pass over that task's own bindings.
		case holdsAClaimedWorktree(path, claimed):
			// On the way to one, so it belongs to a task even though no task
			// names it. Keep looking inside it.
			s.scanForOrphans(path, project, claimed, report)
		default:
			report.Add(reconcile.Finding{
				Class: reconcile.ClassWorktrees, Status: reconcile.StatusOrphaned,
				Project: project, Identity: path,
				Detail: "a directory under the project's worktree root that no task records",
				Action: "look at it and remove it yourself if it is stale. " +
					"Feat never removes a directory no task claims",
			})
		}
	}
}

// holdsAClaimedWorktree reports whether a directory is, or contains, a worktree
// some task records.
func holdsAClaimedWorktree(path string, claimed map[string]bool) bool {
	if claimed[path] {
		return true
	}
	for worktree := range claimed {
		if paths.Under(path, worktree) {
			return true
		}
	}
	return false
}

// reconcileEnvironments observes each task's agent container.
//
// Observing is all it does. A stopped devcontainer stays stopped: bringing one
// up is what a resume or a launch does, and both are things a user asked for
// (FR-STATE-004).
func (s *service) reconcileEnvironments(
	ctx context.Context, tasks []*domain.Task, report *reconcile.Report, left leftovers,
) {
	for _, task := range tasks {
		if task.Session == nil || task.Session.Execution == nil {
			reportUnrecordedEnvironment(task, report, left[task.ID])
			continue
		}
		recorded := task.Session.Execution

		environment, err := s.environmentFor(task)
		if err != nil {
			report.Fail(reconcile.Problem{Class: reconcile.ClassAgentContainers, Project: task.ProjectID, Task: task.ID,
				Reason: "the task's agent environment could not be resolved: " + err.Error()})
			continue
		}
		state, err := environment.Observe(ctx)
		if err != nil {
			report.Fail(reconcile.Problem{Class: reconcile.ClassAgentContainers, Project: task.ProjectID, Task: task.ID,
				Reason: fmt.Sprintf("Compose project %s could not be observed: %s", recorded.Identity, err)})
			continue
		}

		if err := s.recordObservedEnvironment(ctx, task, state); err != nil {
			report.Fail(reconcile.Problem{Class: reconcile.ClassAgentContainers, Project: task.ProjectID, Task: task.ID,
				Reason: "the observation could not be recorded: " + err.Error()})
			continue
		}

		switch {
		case state.Running:
			report.Add(reconcile.Finding{
				Class: reconcile.ClassAgentContainers, Status: reconcile.StatusPresent,
				Project: task.ProjectID, Task: task.ID, Identity: recorded.Identity,
				Detail: "the agent container is " + state.Status,
			})
		case state.Present:
			report.Add(reconcile.Finding{
				Class: reconcile.ClassAgentContainers, Status: reconcile.StatusInconsistent,
				Project: task.ProjectID, Task: task.ID, Identity: recorded.Identity,
				Detail: "the agent container exists and is " + state.Status + "; Feat did not restart it",
				Action: "resume the task to start it again, or clean it up",
			})
		default:
			report.Add(reconcile.Finding{
				Class: reconcile.ClassAgentContainers, Status: reconcile.StatusMissing,
				Project: task.ProjectID, Task: task.ID, Identity: recorded.Identity,
				Detail: "the recorded agent Compose project has no container",
				Action: "resume the task to create it again, or clean it up",
			})
		}
	}
}

// reportUnrecordedEnvironment reports the containers of a task whose record
// names none.
//
// It is the half of this pass F2-15 found missing. A launch that fails after its
// container exists records no environment, this pass asked only about the tasks
// that have one, and the containers and networks it left were therefore in no
// report and in no cleanup plan: the product could not see them, and nothing in
// it could name them.
//
// Nothing is adopted and nothing is removed. The finding names the task the
// resources were created for — they are an orphan of the record rather than of
// the machine, since the Compose project name is this task's own — and the
// action is the cleanup that now resolves them.
//
// It describes one moment, as every finding here does. A launch that is in
// flight has its container before it has its session, so a pass requested during
// those seconds reports what is true then; the next one, once the session
// exists, reports the same container as present.
//
// The moment is the one unrecordedEnvironments read, rather than a second look
// of this function's own: the terminal pass has already told the user what this
// task left, and two Docker queries a few milliseconds apart are two moments a
// report could describe differently.
func reportUnrecordedEnvironment(task *domain.Task, report *reconcile.Report, found leftover) {
	switch {
	case !found.applies:
		return
	case found.failure != nil:
		report.Fail(reconcile.Problem{Class: reconcile.ClassAgentContainers, Project: task.ProjectID, Task: task.ID,
			Reason: found.failure.Error()})
	case found.remains.Empty():
		return
	default:
		report.Add(reconcile.Finding{
			Class: reconcile.ClassAgentContainers, Status: reconcile.StatusOrphaned,
			Project: task.ProjectID, Task: task.ID, Identity: found.identity,
			Detail: "a launch of this task left " + found.remains.Describe() +
				", and the task records no agent session to own them",
			Action: "clean the task up with `feat task cleanup " + task.Key().String() +
				"`, which resolves them by the Compose project name this task derives",
		})
	}
}

// leftovers is what a pass found of the tasks whose record names no
// environment, keyed by task.
//
// A task is absent from it when the question does not apply to that task at all.
type leftovers map[domain.TaskID]leftover

// leftover is what one such task still has, or why nobody can say.
type leftover struct {
	// applies reports whether the question was one this task could be asked.
	applies bool
	// identity is the Compose project name that was asked about.
	identity string
	// remains is what it still has, empty when it has nothing.
	remains compose.Remains
	// failure is why the question could not be answered, nil when it was.
	failure error
}

// unrecordedEnvironments asks what the tasks with no recorded environment still
// have on the machine.
//
// Once per task per pass, and the answer is shared: see Reconcile. It is a
// derivation and not a scan — the Compose project name comes from the task's own
// two identifiers — so what it finds belongs to that task by construction
// (ADR-059). Nothing is adopted, started, or removed here or in either pass that
// reads it.
func (s *service) unrecordedEnvironments(ctx context.Context, tasks []*domain.Task) leftovers {
	found := make(leftovers)
	for _, task := range tasks {
		if task.Session != nil && task.Session.Execution != nil {
			continue
		}
		project, err := s.agentProject(task)
		switch {
		case err != nil:
			found[task.ID] = leftover{applies: true, failure: err}
		case project == nil:
			continue
		default:
			remains, err := project.Remains(ctx)
			if err != nil {
				err = fmt.Errorf("the Compose project %s could not be observed: %w", project.Identity(), err)
			}
			found[task.ID] = leftover{
				applies: true, identity: project.Identity(), remains: remains, failure: err,
			}
		}
	}
	return found
}

// confirmedWithoutTerminal says what to do about a confirmed task with no
// terminal, and says only what this pass established.
//
// "its agent never ran, so nothing it did is lost" is true of the ordinary case
// and is the reassurance the finding exists to give: a task confirmed and
// interrupted before its terminal existed can be cleaned up without a thought.
// It is not true of a launch that failed after its container existed — also a
// task with no session — and that report named the container in the very next
// finding. One document said both. What a user does about the task depends on
// which of the two it is, so the answer this pass already has is what decides
// the sentence.
func confirmedWithoutTerminal(found leftover) string {
	const action = "clean it up and prepare the task again. Feat has no way to launch a confirmed task a second time"
	switch {
	case found.failure != nil:
		return action + "; whether a launch of it left containers behind could not be established, " +
			"and the problem recorded against its agent containers says why"
	case !found.remains.Empty():
		return action + "; a launch of it left " + found.remains.Describe() +
			" on the machine, which the cleanup removes"
	default:
		return action + "; its agent never ran, so nothing it did is lost"
	}
}

// recordObservedEnvironment writes down what an agent environment turned out to
// be, under the task's own lock.
//
// It also applies the invariant that ties the two halves of a session together:
// an agent process cannot be alive while the environment it runs in is not
// running. The terminal pass cannot see a devcontainer's death on its own,
// because the pane's own process is on the host side of the container and
// outlives it — so recording the container and leaving the session claiming a
// running agent produced both halves of a contradiction in one pass, a finding
// saying to resume and a resume refusing because an agent was already running
// (ADR-057).
//
// The state is failed rather than stopped because a stop the user asked for
// records itself: `Stop` writes the process state it intended before this ever
// observes the container. So an alive process against a container that is not
// running is a death nobody asked for, which is exactly what session_failed
// exists to interrupt somebody about.
//
// None of it is the automatic restart FR-STATE-004 forbids. Nothing is started
// or stopped here; what changes is what the record says about a process that has
// already ended.
func (s *service) recordObservedEnvironment(ctx context.Context, task *domain.Task, state executionState) error {
	defer s.locks.lock(task.ID)()

	current, err := s.store.Tasks().Load(ctx, store.Ref(task))
	if err != nil {
		return err
	}
	if current.Session == nil || current.Session.Execution == nil {
		return nil
	}
	before := current.Session.Execution.Running
	current.Session.Execution.Observe(state.Container, state.Running, state.Status, state.Health, s.now())

	died := !state.Running && current.Session.Process.Alive()
	from := current.Session.Process
	if died {
		if err := current.Session.Observe(domain.ProcessFailed, s.now()); err != nil {
			return err
		}
	}
	if err := s.store.Tasks().Save(ctx, current); err != nil {
		return err
	}

	if before != state.Running {
		s.record(ctx, current, domain.Event{
			Type: domain.EventExecutionChanged, To: current.Session.Execution.Identity,
			Detail: "reconciliation observed the agent container as " + describeRunning(state.Running) +
				"; it was not started or stopped",
		})
	}
	if died {
		s.record(ctx, current, domain.Event{
			Type: domain.EventReconciled, From: string(from), To: string(domain.ProcessFailed),
			Detail: "the agent container is " + describeStatus(state) +
				", so the session running inside it has ended; it was not restarted",
		})
		s.notifyChange(ctx, current, false, true)
	}
	return nil
}

// describeStatus renders what an environment observation says about a container,
// for a person reading an event log.
func describeStatus(state executionState) string {
	if state.Status != "" {
		return state.Status
	}
	if !state.Present {
		return "gone"
	}
	return "not running"
}

func describeRunning(running bool) string {
	if running {
		return "running"
	}
	return "not running"
}

// reconcileRuntimes observes each task's application services.
//
// It never starts anything, which is the rule FR-STATE-004 states and the
// runtime poller already honours. Reconciliation reuses that
// observation rather than adding a second one, so there is one definition of
// what a task's runtime state is.
func (s *service) reconcileRuntimes(ctx context.Context, tasks []*domain.Task, report *reconcile.Report) {
	for _, task := range tasks {
		if task.Runtime == nil || task.Runtime.State == domain.RuntimeAbsent {
			continue
		}
		state, err := s.observeRuntime(ctx, task)
		if err != nil {
			report.Fail(reconcile.Problem{Class: reconcile.ClassRuntimeContainers, Project: task.ProjectID, Task: task.ID,
				Reason: fmt.Sprintf("the application services of task %s could not be observed: %s", task.ID, err)})
			continue
		}

		status := reconcile.StatusPresent
		detail := "the application services are " + string(state)
		action := ""
		switch state {
		case domain.RuntimeStopped, domain.RuntimeFailed, domain.RuntimeDegraded:
			status = reconcile.StatusInconsistent
			detail = "the application services are " + string(state) + "; Feat did not restart them"
			action = "start them from the dashboard when you want them, or remove them with `feat task cleanup`"
		case domain.RuntimeAbsent:
			status = reconcile.StatusMissing
			detail = "the recorded application Compose project has no containers"
			action = "create them again from the dashboard, or clean up what the task still records"
		}
		report.Add(reconcile.Finding{
			Class: reconcile.ClassRuntimeContainers, Status: status,
			Project: task.ProjectID, Task: task.ID, Identity: task.Runtime.Identity,
			Detail: detail, Action: action,
		})
	}
}

// reconcileControl reports the control messages a restart found waiting.
//
// The messages themselves are applied by the poller that runs immediately
// after, which is what makes a turn that ended while Feat was stopped reach the
// dashboard. What belongs here is the part the poller cannot report: a document
// that will never parse, and a workspace that has gone missing under a task
// that still names it.
func (s *service) reconcileControl(ctx context.Context, tasks []*domain.Task, report *reconcile.Report) {
	for _, task := range tasks {
		if task.Session == nil {
			continue
		}
		workspace, err := s.controlWorkspace(task)
		if err != nil {
			report.Fail(reconcile.Problem{Class: reconcile.ClassControl, Project: task.ProjectID, Task: task.ID,
				Reason: "the task's control workspace could not be resolved: " + err.Error()})
			continue
		}
		if !workspace.Exists() {
			report.Add(reconcile.Finding{
				Class: reconcile.ClassControl, Status: reconcile.StatusMissing,
				Project: task.ProjectID, Task: task.ID, Identity: workspace.Root(),
				Detail: "the task's control workspace is gone, so its agent can no longer report anything",
				Action: "the task cannot be recovered in place; clean it up and start a new one",
			})
			continue
		}

		_, damaged, err := workspace.Pending()
		if err != nil {
			report.Fail(reconcile.Problem{Class: reconcile.ClassControl, Project: task.ProjectID, Task: task.ID,
				Reason: "the task's outbox could not be read: " + err.Error()})
			continue
		}
		for _, entry := range damaged {
			report.Add(reconcile.Finding{
				Class: reconcile.ClassControl, Status: reconcile.StatusDamaged,
				Project: task.ProjectID, Task: task.ID, Identity: workspace.OutboxDir(),
				Detail: "an agent message could not be read: " + entry.Error(),
				Action: "the remaining messages were still applied; this one is kept as an audit record",
			})
		}
		report.Add(reconcile.Finding{
			Class: reconcile.ClassControl, Status: reconcile.StatusPresent,
			Project: task.ProjectID, Task: task.ID, Identity: workspace.Root(),
			Detail: "the control workspace is there",
		})
		_ = ctx
	}
}

// reconcileReviews returns a task whose completion gate a restart interrupted.
//
// A gate does not outlive the process that started it, so a task recorded as
// verifying is claiming that checks are running when nothing is. What
// reconciliation adds is that this is reported rather than only done (ADR-036
// evidence 2).
func (s *service) reconcileReviews(ctx context.Context, tasks []*domain.Task, report *reconcile.Report) {
	for _, task := range tasks {
		if task.Workflow != domain.WorkflowVerifying {
			continue
		}
		if err := s.recoverGate(ctx, task); err != nil {
			report.Fail(reconcile.Problem{Project: task.ProjectID, Task: task.ID,
				Reason: "an interrupted completion gate could not be recovered: " + err.Error()})
			continue
		}
		report.Add(reconcile.Finding{
			Status:  reconcile.StatusInconsistent,
			Project: task.ProjectID, Task: task.ID, Identity: task.ID.String(),
			Detail: "the task's configured checks were interrupted and did not finish, " +
				"so it is back where the review request was",
			Action: "run the checks again from review",
		})
	}
}

// recoverGate returns one interrupted task to its review request, under its own
// lock.
func (s *service) recoverGate(ctx context.Context, task *domain.Task) error {
	defer s.locks.lock(task.ID)()

	current, err := s.store.Tasks().Load(ctx, store.Ref(task))
	if err != nil {
		return err
	}
	if current.Workflow != domain.WorkflowVerifying {
		return nil
	}
	return s.transition(ctx, current, domain.WorkflowReviewRequested,
		"the configured checks were interrupted by a daemon restart and did not finish; "+
			"run them again from review")
}

// setReport stores the most recent pass for the local API to serve.
func (s *service) setReport(report *reconcile.Report) {
	s.reportMu.Lock()
	defer s.reportMu.Unlock()
	s.report = report
}

// Reconciliation returns the most recent pass, and false when none has run.
func (s *service) Reconciliation() (api.Reconciliation, bool) {
	s.reportMu.Lock()
	defer s.reportMu.Unlock()
	if s.report == nil {
		return api.Reconciliation{}, false
	}
	return renderReport(s.report), true
}

// lastReport returns the most recent pass in the policy's own terms, for the
// screens and tests inside the daemon.
func (s *service) lastReport() (*reconcile.Report, bool) {
	s.reportMu.Lock()
	defer s.reportMu.Unlock()
	return s.report, s.report != nil
}

// renderReport maps a pass onto the wire, for the reason renderCleanupPlan
// does: the transport is a third representation and may not import the policy.
func renderReport(report *reconcile.Report) api.Reconciliation {
	rendered := api.Reconciliation{
		Ran:                     true,
		StartedAt:               report.StartedAt.UTC(),
		FinishedAt:              report.FinishedAt.UTC(),
		NeedsAttention:          report.NeedsAttention(),
		PreviousRunEndedCleanly: report.PreviousRunEndedCleanly,
		PreviousRunStoppedAt:    report.PreviousRunStoppedAt.UTC(),
	}
	for _, finding := range report.Findings {
		entry := api.ReconciliationFinding{
			Class:     string(finding.Class),
			Status:    string(finding.Status),
			ProjectID: finding.Project.String(),
			Identity:  finding.Identity,
			Detail:    finding.Detail,
			Action:    finding.Action,
		}
		if finding.Task != "" {
			entry.TaskID = finding.Task.String()
			entry.TaskKey = finding.Task.Key().String()
		}
		rendered.Findings = append(rendered.Findings, entry)
	}
	for _, problem := range report.Problems {
		entry := api.ReconciliationProblem{
			Class:     string(problem.Class),
			ProjectID: problem.Project.String(),
			Reason:    problem.Reason,
		}
		if problem.Task != "" {
			entry.TaskID = problem.Task.String()
		}
		rendered.Problems = append(rendered.Problems, entry)
	}
	return rendered
}

// startupReconcile runs the pass before the daemon serves anything, and logs
// what it found.
//
// A failed pass must not stop the daemon. A user whose recovery report could
// not be produced still needs the dashboard that would show it to them, and the
// last recorded state is still the best answer Feat has.
func (s *service) startupReconcile(ctx context.Context) {
	if _, err := s.Reconcile(ctx); err != nil {
		s.logger.ErrorContext(ctx, "reconciling persisted state", slog.Any("error", err))
		return
	}
	report, ok := s.lastReport()
	if !ok {
		return
	}
	if !report.PreviousRunEndedCleanly {
		s.logger.WarnContext(ctx, "the previous daemon did not record a clean shutdown",
			slog.Time("previous_stop", report.PreviousRunStoppedAt))
	}
	for _, finding := range report.Attention() {
		s.logger.WarnContext(ctx, "reconciliation found a resource that needs attention",
			slog.String("class", string(finding.Class)),
			slog.String("status", string(finding.Status)),
			slog.String("task", finding.Task.String()),
			slog.String("identity", finding.Identity),
			slog.String("detail", finding.Detail))
	}
	for _, problem := range report.Problems {
		s.logger.ErrorContext(ctx, "reconciliation could not check a resource",
			slog.String("class", string(problem.Class)),
			slog.String("task", problem.Task.String()),
			slog.String("reason", problem.Reason))
	}
}
