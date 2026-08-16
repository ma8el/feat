package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/config"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/git"
	"github.com/ma8el/feat/internal/store"
)

// Selection is one repository's part in a task, as the user chose it.
//
// It is the output of the repository-access step of task preparation
// (FR-TASK-003). Slice 4 takes it as an argument; the draft API that collects it
// from the user arrives with slice 6.
type Selection struct {
	// Repository identifies the repository within the project.
	Repository domain.RepositoryID
	// Access is the access the task should have to it.
	Access domain.TaskAccess
	// Ref is the revision to start from, for a project whose base policy is
	// explicit. It is ignored by every other policy.
	Ref string
}

// DefaultSelection returns the repositories a project includes in a task
// without being asked.
//
// A repository the configuration marks selectable, stable read-only, or omitted
// is left out: each of those says the user decides, and choosing on their behalf
// would put an agent in a repository nobody selected.
func DefaultSelection(cfg *config.Config) []Selection {
	var selection []Selection
	for _, id := range cfg.RepositoryIDs() {
		repository, _ := cfg.Repository(id)
		switch domain.DefaultAccess(repository.DefaultAccess) {
		case domain.DefaultAccessReadWrite:
			selection = append(selection, Selection{
				Repository: domain.RepositoryID(id), Access: domain.TaskAccessReadWrite,
			})
		case domain.DefaultAccessReadOnly:
			selection = append(selection, Selection{
				Repository: domain.RepositoryID(id), Access: domain.TaskAccessReadOnly,
			})
		}
	}
	return selection
}

// PrepareTask records a selection, resolves it, and creates the Git resources
// of the resulting plan.
//
// It is the Git half of a launch, run end to end with the confirmation supplied
// by the plan that was just resolved. `LaunchDraft` is the same half preceded by
// the user's own confirmation and followed by the task terminal; the API drives
// the steps separately because between the plan a user reads and the key they
// press a fetch can move a remote-tracking ref, and the task would then start
// from a commit nobody was shown (ADR-031).
//
// The order is what makes an interruption survivable, and it is the reason this
// lives in the daemon rather than in the Git adapter:
//
//  1. plan, which resolves every base to an immutable commit and proposes every
//     branch and worktree path without creating anything;
//  2. record the plan on the task, so that every resource that could exist
//     afterwards is already written down, and leave draft on confirmation;
//  3. create them one repository at a time, recording each before the next
//     begins.
//
// A failure at any point therefore leaves a record that names a superset of what
// exists, and nothing exists that the record cannot name. Nothing is undone: a
// worktree that was created may already have been written to, and removing it to
// tidy up a failed launch is a destructive act the user did not ask for. The
// task is left failed, which the workflow can resume from.
func (s *service) PrepareTask(ctx context.Context, ref store.TaskRef, selection []Selection) (*domain.Task, error) {
	task, cfg, err := s.loadDraft(ctx, ref)
	if err != nil {
		return nil, err
	}
	if task.Brief == "" {
		// Checked before anything is fetched: a task that cannot launch should
		// not cause network calls on the user's repositories first.
		return nil, fmt.Errorf("%w: task %s has no brief", api.ErrInvalid, task.ID)
	}
	if err := s.selectRepositories(task, cfg, selection, s.now()); err != nil {
		return nil, err
	}
	if err := s.store.Tasks().Save(ctx, task); err != nil {
		return nil, err
	}

	plan, err := s.planDraft(ctx, ref)
	if err != nil {
		return nil, err
	}
	prepared, _, err := s.confirmDraft(ctx, ref, plan.Fingerprint)
	return prepared, err
}

// recordPlan writes the plan onto the task, which stays a draft.
//
// This is the record that makes a later failure recoverable, so it is saved
// before anything is created. The task stays a draft because nothing has been
// confirmed yet: the base commits, branches, and paths become immutable when the
// user confirms them and the task leaves draft (invariant 8).
func (s *service) recordPlan(ctx context.Context, task *domain.Task, plan *git.Plan) error {
	now := s.now()

	for _, binding := range append([]domain.TaskRepository(nil), task.Repositories...) {
		if err := task.Unbind(binding.RepositoryID, now); err != nil {
			return err
		}
	}
	for _, repository := range plan.Repositories {
		if err := task.Bind(domain.TaskRepository{
			RepositoryID:  repository.ID,
			Access:        repository.Access,
			BaseRef:       repository.BaseRef,
			BaseCommit:    repository.BaseCommit,
			Branch:        repository.Branch,
			WorktreePath:  repository.WorktreePath,
			ContainerPath: repository.ContainerPath,
		}, now); err != nil {
			return err
		}
	}
	return s.store.Tasks().Save(ctx, task)
}

// transition moves the task to a workflow state, persists it, and records the
// change in the task's history.
func (s *service) transition(ctx context.Context, task *domain.Task, next domain.WorkflowState, detail string) error {
	from := task.Workflow
	// The detail is the reason when the state is `failed`, so it is recorded on
	// the task and not only on the event. Every path into that state comes
	// through here — a launch, a Git apply, a resume, a terminal, and a session
	// the provider reported as failed — which is why one branch covers them all.
	move := func() error { return task.TransitionTo(next, s.now()) }
	if next == domain.WorkflowFailed {
		move = func() error { return task.FailWith(detail, s.now()) }
	}
	if err := move(); err != nil {
		return err
	}
	if err := s.store.Tasks().Save(ctx, task); err != nil {
		return err
	}
	s.record(ctx, task, domain.Event{
		Type:       domain.EventWorkflowChanged,
		From:       string(from),
		To:         string(next),
		Detail:     detail,
		OccurredAt: s.now(),
	})
	return nil
}

// record appends an event to the task's history and publishes it.
//
// A history that cannot be written does not fail the operation that produced it:
// the snapshot is the state of the world and the log is the explanation of how it
// got there. Losing the explanation is worth a loud log line, not a worktree the
// caller believes was never created.
func (s *service) record(ctx context.Context, task *domain.Task, event domain.Event) {
	event.ProjectID = task.ProjectID
	event.TaskID = task.ID
	if event.OccurredAt.IsZero() {
		event.OccurredAt = s.now()
	}

	appended, err := s.store.Events().Append(ctx, store.Ref(task), event)
	if err != nil {
		s.logger.ErrorContext(ctx, "recording a task event",
			slog.String("task", task.ID.String()),
			slog.String("type", string(event.Type)),
			slog.Any("error", err))
		return
	}
	s.Publish(appended)
}

// taskJournal records each repository the Git adapter finishes.
//
// The adapter creates and this writes: the daemon stays the only writer of
// persistent state (ADR-008), and the adapter stays testable without one.
type taskJournal struct {
	service *service
	task    *domain.Task
	ref     store.TaskRef
}

// Created records one finished repository before the next one is started.
func (j *taskJournal) Created(ctx context.Context, created git.Created) error {
	if err := j.task.ObserveRepository(created.Repository, created.Observation, j.service.now()); err != nil {
		return err
	}
	if err := j.service.store.Tasks().Save(ctx, j.task); err != nil {
		return err
	}
	j.service.record(ctx, j.task, domain.Event{
		Type:         domain.EventRepositoryObserved,
		RepositoryID: created.Repository,
		Detail:       "worktree created at " + created.WorktreePath,
	})
	return nil
}

// gitRequest turns configuration, a task, and a repository selection into the
// request the Git adapter takes.
//
// Templates are expanded here rather than in the adapter. The placeholder
// vocabulary belongs to configuration, which validates it, and an adapter that
// had to expand a template would have to learn the shape of a YAML file to
// create a directory.
func gitRequest(cfg *config.Config, task *domain.Task, selection []Selection) (git.Request, error) {
	if len(selection) == 0 {
		return git.Request{}, fmt.Errorf("task %s selects no repository", task.ID)
	}

	request := git.Request{
		Project: task.ProjectID,
		Task:    task.ID,
		Root:    config.StaticPrefix(cfg.Git.WorktreeRoot),
		Fetch:   cfg.Git.FetchesBeforeTask(),
	}

	for _, selected := range selection {
		repository, ok := cfg.Repository(selected.Repository.String())
		if !ok {
			return git.Request{}, fmt.Errorf("project %s has no repository %s",
				task.ProjectID, selected.Repository)
		}
		if !domain.DefaultAccess(repository.DefaultAccess).Permits(selected.Access) {
			return git.Request{}, fmt.Errorf(
				"repository %s cannot be selected as %s, because the project configures it as %s",
				selected.Repository, selected.Access, repository.DefaultAccess)
		}

		values := config.Values{
			ProjectID:    task.ProjectID.String(),
			TaskID:       task.ID.String(),
			TaskKey:      task.Key().String(),
			RepositoryID: selected.Repository.String(),
			Slug:         config.Slug(task.Title),
		}

		var branch string
		if selected.Access == domain.TaskAccessReadWrite {
			expanded, err := config.Expand(cfg.Git.BranchTemplate, values)
			if err != nil {
				return git.Request{}, fmt.Errorf("generating the branch of repository %s: %w",
					selected.Repository, err)
			}
			branch = expanded
		}

		worktree, err := worktreePath(cfg.Git.WorktreeRoot, values)
		if err != nil {
			return git.Request{}, fmt.Errorf("generating the worktree path of repository %s: %w",
				selected.Repository, err)
		}

		// A container path only means something where there is a container. In
		// host execution the agent works in the worktree itself, and recording a
		// mount point nothing mounts would be a claim about the task that is not
		// true.
		container := ""
		if cfg.Agent.Execution.Devcontainer() {
			container = repository.Agent.ContainerPath
		}

		request.Repositories = append(request.Repositories, git.RepositoryRequest{
			ID:            selected.Repository,
			HostPath:      repository.HostPath,
			Remote:        repository.Remote,
			DefaultBranch: repository.DefaultBranch,
			Access:        selected.Access,
			Policy:        git.BasePolicy(cfg.Git.BasePolicy),
			Ref:           selected.Ref,
			Branch:        branch,
			WorktreePath:  worktree,
			ContainerPath: container,
		})
	}
	return request, nil
}

// worktreePath expands the configured worktree root for one repository.
//
// The root names the directory that holds a task's worktrees. A template that
// already names the repository expands to one directory per repository; one that
// does not gets the repository appended, because otherwise every repository of a
// task would share a single directory and the second worktree would fail on the
// first one's files.
func worktreePath(template string, values config.Values) (string, error) {
	expanded, err := config.Expand(template, values)
	if err != nil {
		return "", err
	}
	if config.Uses(template, config.PlaceholderRepositoryID) {
		return filepath.Clean(expanded), nil
	}
	return filepath.Join(expanded, values.RepositoryID), nil
}
