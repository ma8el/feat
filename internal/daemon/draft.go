package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"time"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/config"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/git"
	"github.com/ma8el/feat/internal/store"
	"github.com/ma8el/feat/internal/tmux"
)

// maxBriefBytes bounds a task brief.
//
// The brief is a document the user wrote, imported or typed, and it is stored,
// mounted, and handed to an agent. A bound well below the API's own body limit
// means an over-large brief is refused with an explanation rather than as a
// truncated request.
const maxBriefBytes = 256 << 10

// keyAttempts is how many task identifiers are generated before Feat gives up
// on finding one whose short key is free within the project.
//
// The key is the first eight hex characters of a UUID, so a collision needs
// roughly a hundred thousand tasks in one project to become likely. Retrying is
// how ADR-026 resolves one; a bound stops an impossible situation from becoming
// an endless loop.
const keyAttempts = 8

// CreateDraft records a new task draft.
//
// It creates a task record and nothing else: no worktree, no branch, no
// terminal, and no container. FR-TASK-003 puts every one of those behind an
// explicit confirmation, and the draft is what the user confirms.
//
// Nothing in the request names a filesystem path the daemon would open. An
// imported Markdown brief is read by the client and arrives as content, so no
// caller-supplied path crosses the socket (ADR-028).
func (s *service) CreateDraft(ctx context.Context, request api.DraftRequest) (*domain.Task, error) {
	if err := request.Project.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", api.ErrInvalid, err)
	}
	if err := checkBrief(request.Brief); err != nil {
		return nil, err
	}
	if err := s.checkTicket(request.Source.Ticket); err != nil {
		return nil, err
	}
	if _, err := s.store.Projects().Load(ctx, request.Project); err != nil {
		return nil, translate(err, "no project "+request.Project.String()+" is registered")
	}
	cfg, err := config.Load(s.layout.ProjectConfigDir(), request.Project.String(), s.configOptions())
	if err != nil {
		return nil, translateConfig(err)
	}

	id, err := s.freeTaskID(ctx, request.Project)
	if err != nil {
		return nil, err
	}

	now := s.now()
	task, err := domain.NewTask(id, request.Project, request.Title, request.Source, now)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", api.ErrInvalid, err)
	}
	if err := task.SetBrief(request.Brief, now); err != nil {
		return nil, fmt.Errorf("%w: %w", api.ErrInvalid, err)
	}
	// The default selection is a starting point the user changes, not a
	// decision made for them: a repository the project marks selectable,
	// stable read-only, or omitted is left out until they say otherwise.
	if err := s.selectRepositories(task, cfg, DefaultSelection(cfg), now); err != nil {
		return nil, err
	}
	if err := s.store.Tasks().Save(ctx, task); err != nil {
		return nil, err
	}

	s.record(ctx, task, domain.Event{
		Type:   domain.EventTaskCreated,
		To:     string(domain.WorkflowDraft),
		Detail: "task draft created from a " + string(request.Source.Kind) + " source",
	})
	s.logger.InfoContext(ctx, "task draft created",
		slog.String("project", request.Project.String()), slog.String("task", task.ID.String()))
	return task, nil
}

// UpdateDraft replaces a draft's title, brief, and repository selection.
//
// It is a replacement rather than a patch because that is what the preparation
// screen holds: the user edits a whole draft and saves it, and a partial update
// would make the recorded selection depend on the order the fields arrived in.
//
// Changing the selection discards a plan resolved for the previous one, because
// the bases, branches, and paths in it belonged to repositories the task may no
// longer select. The user resolves the draft again, which is also what makes the
// fingerprint they confirm describe the selection they see.
func (s *service) UpdateDraft(
	ctx context.Context, id domain.TaskID, request api.DraftUpdate,
) (*domain.Task, error) {
	ref, err := s.draftRef(ctx, id)
	if err != nil {
		return nil, err
	}
	task, cfg, err := s.loadDraft(ctx, ref)
	if err != nil {
		return nil, err
	}
	if err := checkBrief(request.Brief); err != nil {
		return nil, err
	}

	now := s.now()
	if err := task.SetTitle(request.Title, now); err != nil {
		return nil, fmt.Errorf("%w: %w", api.ErrInvalid, err)
	}
	if err := task.SetBrief(request.Brief, now); err != nil {
		return nil, fmt.Errorf("%w: %w", api.ErrInvalid, err)
	}
	if err := s.selectRepositories(task, cfg, selected(request.Repositories), now); err != nil {
		return nil, err
	}
	if err := s.store.Tasks().Save(ctx, task); err != nil {
		return nil, err
	}

	s.record(ctx, task, domain.Event{Type: domain.EventTaskUpdated, Detail: "task draft edited"})
	return task, nil
}

// selected maps the API's repository selection onto the daemon's.
func selected(repositories []api.DraftSelection) []Selection {
	selection := make([]Selection, 0, len(repositories))
	for _, repository := range repositories {
		selection = append(selection, Selection{
			Repository: repository.Repository,
			Access:     repository.Access,
			Ref:        repository.Ref,
		})
	}
	return selection
}

// draftRef resolves a task identifier to the reference storage addresses a task
// by.
//
// The local API addresses a task by task alone and the daemon resolves the
// owning project, which is ADR-027's rule and the reason it is done here rather
// than in storage.
func (s *service) draftRef(ctx context.Context, id domain.TaskID) (store.TaskRef, error) {
	task, err := s.Task(ctx, id)
	if err != nil {
		return store.TaskRef{}, err
	}
	return store.Ref(task), nil
}

// PlanDraft resolves every selected repository's base and proposes the branches
// and worktree paths the task would receive.
//
// It creates nothing. The only change it makes to the machine is the
// remote-tracking refs a fetch updates, which is why it is a request of its own:
// a network call against the user's repositories should follow a key they
// pressed rather than a field they edited.
//
// The result is recorded on the draft, which stays a draft. Recording it is what
// lets the confirmation that follows create exactly what was displayed.
func (s *service) PlanDraft(ctx context.Context, id domain.TaskID) (api.ResolvedDraft, error) {
	ref, err := s.draftRef(ctx, id)
	if err != nil {
		return api.ResolvedDraft{}, err
	}
	return s.planDraft(ctx, ref)
}

func (s *service) planDraft(ctx context.Context, ref store.TaskRef) (api.ResolvedDraft, error) {
	task, cfg, err := s.loadDraft(ctx, ref)
	if err != nil {
		return api.ResolvedDraft{}, err
	}

	request, err := gitRequest(cfg, task, selectionOf(task))
	if err != nil {
		return api.ResolvedDraft{}, fmt.Errorf("%w: %w", api.ErrInvalid, err)
	}

	plan, err := s.git.Plan(ctx, request)
	if err != nil {
		// Planning creates nothing, so a draft whose plan does not hold is still
		// a draft the user can change (FR-TASK-003).
		return api.ResolvedDraft{}, err
	}

	notes := make([]string, 0, len(plan.Notes))
	for _, note := range plan.Notes {
		summary := note.Summary
		if note.Repository != "" {
			summary = note.Repository.String() + ": " + summary
		}
		notes = append(notes, summary)
		s.logger.WarnContext(ctx, "resolving a task draft",
			slog.String("task", task.ID.String()), slog.String("note", summary))
	}

	if err := s.recordPlan(ctx, task, plan); err != nil {
		return api.ResolvedDraft{}, err
	}
	s.record(ctx, task, domain.Event{
		Type:   domain.EventTaskUpdated,
		Detail: fmt.Sprintf("resolved %d repositor%s", len(plan.Repositories), plural(len(plan.Repositories))),
	})

	return api.ResolvedDraft{Task: task, Notes: notes, Fingerprint: Fingerprint(task)}, nil
}

// LaunchDraft confirms a draft and creates what was displayed.
//
// The fingerprint is the confirmation. A draft that was edited or resolved again
// since the user read it produces a different one, and the launch is refused
// rather than silently applied: between a displayed plan and a pressed key, a
// fetch is enough to move a remote-tracking ref, and the task would then start
// from a commit nobody was shown (ADR-031).
//
// What follows is ADR-029's order, with the confirmation in front of it: the
// plan is already recorded, so every path and branch that can exist afterwards
// is written down before anything is created, and a failure part way through
// leaves the task failed with a record naming a superset of what exists.
func (s *service) LaunchDraft(ctx context.Context, id domain.TaskID, fingerprint string) (*domain.Task, error) {
	// A launch that has to create a container is not a request that answers in
	// an ordinary request's budget, and the day it does not is the day the
	// project's own Compose file changed and the service has to be recreated.
	// One number bounds it at both ends (api.AgentTimeout).
	ctx, cancel := context.WithTimeout(ctx, s.agentBudget())
	defer cancel()

	ref, err := s.draftRef(ctx, id)
	if err != nil {
		return nil, err
	}

	task, cfg, err := s.confirmDraft(ctx, ref, fingerprint)
	if err != nil {
		return task, err
	}

	plan, err := s.planLaunch(ctx, cfg, task)
	if err != nil {
		// Nothing has been started. The worktrees exist, because they were
		// created before this point and ADR-029 does not undo a partial launch,
		// but no terminal and no session were created for an agent that could
		// not run.
		if transitionErr := s.transition(ctx, task, domain.WorkflowFailed, err.Error()); transitionErr != nil {
			return nil, errors.Join(err, transitionErr)
		}
		return task, err
	}
	return s.ensureTerminal(ctx, task, cfg, plan)
}

// confirmDraft verifies the confirmation and creates the draft's Git resources.
//
// It is the half of a launch that PrepareTask also performs, so that the
// ordering the recoverability criterion rests on has one implementation.
func (s *service) confirmDraft(
	ctx context.Context, ref store.TaskRef, fingerprint string,
) (*domain.Task, *config.Config, error) {
	task, cfg, err := s.loadDraft(ctx, ref)
	if err != nil {
		return nil, nil, err
	}
	if task.Brief == "" {
		// Checked before anything is created: a task that cannot be worked on
		// should not leave worktrees behind first.
		return nil, nil, fmt.Errorf("%w: task %s has no brief", api.ErrInvalid, task.ID)
	}
	if len(task.Repositories) == 0 {
		return nil, nil, fmt.Errorf("%w: task %s selects no repository", api.ErrInvalid, task.ID)
	}
	for _, binding := range task.Repositories {
		if binding.BaseCommit == "" || binding.WorktreePath == "" {
			return nil, nil, fmt.Errorf(
				"%w: task %s has not been resolved since repository %s was selected; resolve the draft before confirming it",
				api.ErrInvalid, task.ID, binding.RepositoryID)
		}
	}
	if current := Fingerprint(task); current != fingerprint {
		return nil, nil, fmt.Errorf(
			"%w: task %s changed after the plan you confirmed was displayed; resolve the draft again and review it before confirming",
			api.ErrInvalid, task.ID)
	}

	plan, err := recordedPlan(cfg, task)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", api.ErrInvalid, err)
	}

	if err := s.transition(ctx, task, domain.WorkflowPreparing, "confirmed by the user"); err != nil {
		return nil, nil, err
	}

	journal := &taskJournal{service: s, task: task, ref: ref}
	if _, applyErr := s.git.Apply(ctx, plan, journal); applyErr != nil {
		if err := s.transition(ctx, task, domain.WorkflowFailed, applyErr.Error()); err != nil {
			return nil, nil, errors.Join(applyErr, err)
		}
		return task, nil, applyErr
	}
	s.logger.InfoContext(ctx, "task repositories prepared",
		slog.String("project", ref.Project.String()),
		slog.String("task", task.ID.String()),
		slog.Int("repositories", len(plan.Repositories)))
	return task, cfg, nil
}

// CancelDraft abandons a draft.
//
// The record is archived rather than removed: it explains what the user started
// and decided against, which is the same reason cleanup archives task metadata.
// Nothing was created for a draft, so nothing is destroyed by cancelling one.
func (s *service) CancelDraft(ctx context.Context, id domain.TaskID) (*domain.Task, error) {
	ref, err := s.draftRef(ctx, id)
	if err != nil {
		return nil, err
	}
	task, _, err := s.loadDraft(ctx, ref)
	if err != nil {
		return nil, err
	}
	if err := s.transition(ctx, task, domain.WorkflowArchived, "the draft was cancelled before confirmation"); err != nil {
		return nil, err
	}
	s.logger.InfoContext(ctx, "task draft cancelled",
		slog.String("project", ref.Project.String()), slog.String("task", task.ID.String()))
	return task, nil
}

// Fingerprint identifies a task's frozen shape.
//
// It covers everything confirmation freezes: the brief the agent will receive,
// and every repository's access, base, branch, and path. It is computed from the
// recorded task rather than stored beside it, because two records of one fact
// can disagree — the reason ADR-026 derives the task key from the task
// identifier rather than storing both.
//
// Fields are written with their lengths, so that no two different drafts can
// produce the same input by moving a separator from one field into another.
func Fingerprint(task *domain.Task) string {
	digest := sha256.New()
	write := func(values ...string) {
		for _, value := range values {
			// A hash writer never fails, and the digest below is only as good
			// as every field having reached it, so a lost write would be worth
			// panicking over rather than ignoring. sha256 documents that it
			// cannot happen.
			_, _ = fmt.Fprintf(digest, "%d:%s", len(value), value)
		}
	}

	write("task", task.ID.String(), task.ProjectID.String())
	write("title", task.Title)
	write("brief", task.Brief)
	write("source", string(task.Source.Kind), task.Source.Reference)

	bindings := append([]domain.TaskRepository(nil), task.Repositories...)
	sort.Slice(bindings, func(i, j int) bool {
		return bindings[i].RepositoryID < bindings[j].RepositoryID
	})
	for _, binding := range bindings {
		write("repository",
			binding.RepositoryID.String(),
			string(binding.Access),
			binding.BaseRef,
			binding.BaseCommit,
			binding.Branch,
			binding.WorktreePath,
			binding.ContainerPath,
		)
	}
	return hex.EncodeToString(digest.Sum(nil))
}

// loadDraft returns a task that is still a draft, with its project's
// configuration.
func (s *service) loadDraft(ctx context.Context, ref store.TaskRef) (*domain.Task, *config.Config, error) {
	if err := ref.Validate(); err != nil {
		return nil, nil, fmt.Errorf("%w: %w", api.ErrInvalid, err)
	}
	task, err := s.store.Tasks().Load(ctx, ref)
	if err != nil {
		return nil, nil, translate(err, "no task draft "+ref.Task.String()+" in project "+ref.Project.String())
	}
	if task.Workflow != domain.WorkflowDraft {
		return nil, nil, fmt.Errorf("%w: task %s is %s, and only a draft can be prepared",
			api.ErrInvalid, task.ID, task.Workflow)
	}
	cfg, err := config.Load(s.layout.ProjectConfigDir(), ref.Project.String(), s.configOptions())
	if err != nil {
		return nil, nil, translateConfig(err)
	}
	return task, cfg, nil
}

// selectRepositories replaces a draft's repository selection.
//
// A repository whose selection is unchanged keeps what was resolved for it, and
// one that was added, dropped, or given different access does not. The
// distinction matters because resolving fetches: making a user re-resolve every
// repository because they fixed a typo in the title would put a network call
// behind an edit that changed nothing about where the task starts.
//
// What must not survive is a resolution that no longer describes the selection.
// A base, branch, and path belong to one repository at one access, and carrying
// them past a change to either would show the user a plan for a task they are
// no longer preparing. Editing the brief or the title does not invalidate a
// plan, and it does change the fingerprint, so a confirmation is still refused
// unless the review screen was read again (ADR-031).
func (s *service) selectRepositories(
	task *domain.Task, cfg *config.Config, selection []Selection, now time.Time,
) error {
	seen := make(map[domain.RepositoryID]bool, len(selection))
	for _, selected := range selection {
		repository, ok := cfg.Repository(selected.Repository.String())
		if !ok {
			return fmt.Errorf("%w: project %s has no repository %s",
				api.ErrInvalid, task.ProjectID, selected.Repository)
		}
		if !selected.Access.Valid() {
			return fmt.Errorf("%w: %q is not a task access mode", api.ErrInvalid, string(selected.Access))
		}
		// Taking less access than the project's default is always allowed;
		// taking more is not. A repository configured read-only must not become
		// writable because one task asked.
		if !domain.DefaultAccess(repository.DefaultAccess).Permits(selected.Access) {
			return fmt.Errorf(
				"%w: repository %s cannot be selected as %s, because the project configures it as %s",
				api.ErrInvalid, selected.Repository, selected.Access, repository.DefaultAccess)
		}
		if seen[selected.Repository] {
			return fmt.Errorf("%w: repository %s is selected twice", api.ErrInvalid, selected.Repository)
		}
		seen[selected.Repository] = true
	}

	previous := make(map[domain.RepositoryID]domain.TaskRepository, len(task.Repositories))
	for _, binding := range append([]domain.TaskRepository(nil), task.Repositories...) {
		previous[binding.RepositoryID] = binding
		if err := task.Unbind(binding.RepositoryID, now); err != nil {
			return err
		}
	}

	for _, selected := range selection {
		binding := domain.TaskRepository{
			RepositoryID: selected.Repository,
			Access:       selected.Access,
			// A revision the user supplied for an explicit base policy is
			// carried here until planning resolves it to a commit.
			BaseRef: selected.Ref,
		}
		if resolved, ok := previous[selected.Repository]; ok && unchanged(resolved, selected) {
			binding = resolved
		}
		if err := task.Bind(binding, now); err != nil {
			return err
		}
	}
	return nil
}

// unchanged reports whether a repository's selection is the same one a previous
// resolution was made for.
//
// The recorded base ref is what the user supplied before resolution and what
// Feat resolved after it, so an empty request ref means "whatever this already
// resolved to" rather than a change to it.
func unchanged(resolved domain.TaskRepository, selected Selection) bool {
	if resolved.Access != selected.Access {
		return false
	}
	return selected.Ref == "" || selected.Ref == resolved.BaseRef
}

// freeTaskID returns a task identifier whose short key no task of the project
// already uses.
//
// The key appears in branch names, worktree paths, and the dashboard, so two
// tasks sharing one would make a user's own shorthand ambiguous. ADR-026 resolves
// a collision by generating another identifier rather than by storing a key
// beside it.
func (s *service) freeTaskID(ctx context.Context, project domain.ProjectID) (domain.TaskID, error) {
	existing, err := s.store.Tasks().List(ctx, project)
	if err != nil {
		return "", err
	}
	taken := make(map[domain.TaskKey]bool, len(existing))
	for _, task := range existing {
		taken[task.Key()] = true
	}

	for range keyAttempts {
		id := domain.NewTaskID()
		if !taken[id.Key()] {
			return id, nil
		}
	}
	return "", fmt.Errorf("could not find an unused task key in project %s after %d attempts",
		project, keyAttempts)
}

// checkBrief bounds the brief before it is stored.
func checkBrief(brief string) error {
	if len(brief) > maxBriefBytes {
		return fmt.Errorf("%w: the task brief is %d bytes, and the limit is %d",
			api.ErrInvalid, len(brief), maxBriefBytes)
	}
	return nil
}

// checkTicket bounds and sanity-checks the ticket a brief was composed from.
//
// The ticket is a record Feat keeps for the life of the task — it is what lets a
// merge request name what it closes, and what a ticket observed again later is
// compared against — so it is validated rather than believed. Whether the
// values make a consistent reference is the domain's to say; what is checked
// here is what the domain cannot know: how large it is, and that it was not
// read in the future.
//
// A whole snapshot is bounded at what a brief is bounded at, because a snapshot
// is text of the same kind from the same place, and the tracker's whole output
// was already bounded when Feat read it (ADR-071).
func (s *service) checkTicket(ticket *domain.ExternalTaskReference) error {
	if ticket == nil {
		return nil
	}
	if size := len(ticket.Snapshot.Title) + len(ticket.Snapshot.Body) + len(ticket.Snapshot.State) +
		len(ticket.Reference) + len(ticket.URL) + len(ticket.Provider); size > maxBriefBytes {
		return fmt.Errorf("%w: the ticket is %d bytes, and the limit is %d",
			api.ErrInvalid, size, maxBriefBytes)
	}
	if taken := ticket.Snapshot.TakenAt; !taken.IsZero() && taken.After(s.now()) {
		return fmt.Errorf("%w: the ticket says it was read at %s, which is in the future",
			api.ErrInvalid, taken.UTC().Format(time.RFC3339))
	}
	return nil
}

// selectionOf reads a draft's recorded repository selection back out.
func selectionOf(task *domain.Task) []Selection {
	selection := make([]Selection, 0, len(task.Repositories))
	for _, binding := range task.Repositories {
		selection = append(selection, Selection{
			Repository: binding.RepositoryID,
			Access:     binding.Access,
			// A base ref recorded before resolution is the revision the user
			// supplied for an explicit base policy. Every other policy ignores it.
			Ref: binding.BaseRef,
		})
	}
	return selection
}

// recordedPlan rebuilds the Git plan from what the draft recorded.
//
// It reads the branches, bases, and paths from the task rather than expanding
// the templates again, because the point of confirming a plan is that the
// resources created are the ones displayed. Only the checkout paths come from
// configuration, since a task does not record where a repository lives.
//
// Every recorded worktree path is checked again here. A record can be edited,
// restored from a backup, or written by an older version, and this one decides
// where a directory is created (ADR-029).
func recordedPlan(cfg *config.Config, task *domain.Task) (*git.Plan, error) {
	plan := &git.Plan{Project: task.ProjectID, Task: task.ID}

	checkouts := make([]string, 0, len(task.Repositories))
	for _, binding := range task.Repositories {
		repository, ok := cfg.Repository(binding.RepositoryID.String())
		if !ok {
			return nil, fmt.Errorf("project %s no longer configures repository %s, which task %s selected",
				task.ProjectID, binding.RepositoryID, task.ID)
		}
		checkouts = append(checkouts, repository.HostPath)
	}

	root := config.StaticPrefix(cfg.Git.WorktreeRoot)
	for i, binding := range task.Repositories {
		if err := git.CheckWorktreePath(root, binding.WorktreePath, checkouts); err != nil {
			return nil, fmt.Errorf("the worktree recorded for repository %s cannot be used: %w",
				binding.RepositoryID, err)
		}
		plan.Repositories = append(plan.Repositories, git.RepositoryPlan{
			ID:            binding.RepositoryID,
			Access:        binding.Access,
			HostPath:      checkouts[i],
			BaseRef:       binding.BaseRef,
			BaseCommit:    binding.BaseCommit,
			Branch:        binding.Branch,
			WorktreePath:  binding.WorktreePath,
			ContainerPath: binding.ContainerPath,
		})
	}
	return plan, nil
}

// shellCommand is a plain terminal in the task's own worktree.
//
// It is what a task gets when no agent is started: a project that configures a
// container, on a daemon that was not opted in to host execution, waits for its
// container rather than being given an agent somewhere it did not ask for. The
// pane is real, so attach and shell work, and the task stays preparing because
// nothing is running that could be called an agent session (ADR-031, ADR-032).
func (s *service) shellCommand(cfg *config.Config, task *domain.Task) (tmux.CommandSpec, error) {
	directory, err := primaryWorktree(cfg, task)
	if err != nil {
		return tmux.CommandSpec{}, err
	}
	return tmux.CommandSpec{Program: s.shell(), Directory: directory}, nil
}

// shell returns the program a task terminal runs.
//
// It is the daemon owner's own shell, because the terminal is theirs to work in.
// A shell that is not an absolute path is not used: tmux would resolve it
// against a PATH the daemon inherited, and a task terminal should not depend on
// that.
func (s *service) shell() string {
	if s.env.Getenv != nil {
		if shell := s.env.Getenv("SHELL"); filepath.IsAbs(shell) {
			return shell
		}
	}
	return "/bin/sh"
}

// primaryWorktree is where a task's terminals open.
//
// It is the task worktree of the project's primary repository (FR-PROJ-003).
// A task that left the primary repository out gets its first selected
// repository instead, which is the honest answer rather than a directory the
// task has nothing to do with.
func primaryWorktree(cfg *config.Config, task *domain.Task) (string, error) {
	if binding, ok := task.Repository(domain.RepositoryID(cfg.Project.PrimaryRepository)); ok &&
		binding.WorktreePath != "" {
		return binding.WorktreePath, nil
	}
	for _, binding := range task.Repositories {
		if binding.WorktreePath != "" {
			return binding.WorktreePath, nil
		}
	}
	return "", fmt.Errorf("task %s has no worktree to open a terminal in", task.ID)
}

// plural renders the suffix for a count of repositories.
func plural(count int) string {
	if count == 1 {
		return "y"
	}
	return "ies"
}
