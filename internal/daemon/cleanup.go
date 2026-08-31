package daemon

import (
	"context"
	"errors"
	"fmt"
	iofs "io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/config"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/git"
	"github.com/ma8el/feat/internal/paths"
	"github.com/ma8el/feat/internal/reconcile"
	"github.com/ma8el/feat/internal/runtime"
	"github.com/ma8el/feat/internal/tmux"
)

// CleanupPlan resolves the exact set of resources one task owns.
//
// It observes and removes nothing, which is what makes it safe to call from a
// screen a user is reading. Every target comes from the task's own record, so a
// resource Feat did not write down is never proposed for removal (FR-CLEAN-001);
// a recorded path that is not one Feat may remove becomes a problem rather than
// a target, because a record that decides what gets deleted has stopped being a
// record (ADR-029).
func (s *service) CleanupPlan(ctx context.Context, id domain.TaskID) (api.CleanupPlan, error) {
	task, err := s.Task(ctx, id)
	if err != nil {
		return api.CleanupPlan{}, err
	}
	plan, err := s.resolveCleanup(ctx, task)
	if err != nil {
		return api.CleanupPlan{}, err
	}
	return s.renderCleanupPlan(task, plan), nil
}

// resolveCleanup builds the inventory.
func (s *service) resolveCleanup(ctx context.Context, task *domain.Task) (*reconcile.Plan, error) {
	plan := &reconcile.Plan{Project: task.ProjectID, Task: task.ID}

	cfg, err := config.Load(s.layout.ProjectConfigDir(), task.ProjectID.String(), s.configOptions())
	if err != nil {
		// Without configuration the Git root cannot be resolved, and without the
		// root no worktree may be removed. Everything else is addressed by the
		// task's own record and is still resolvable, so this narrows the plan
		// rather than failing it.
		plan.Problems = append(plan.Problems,
			"the project's configuration could not be read, so no worktree or branch can be resolved: "+err.Error())
	} else {
		s.resolveGitCleanup(ctx, cfg, task, plan)
	}

	s.resolveTerminalCleanup(ctx, task, plan)
	s.resolveEnvironmentCleanup(ctx, task, plan)
	s.resolveRuntimeCleanup(ctx, task, plan)
	s.resolveControlCleanup(task, plan)

	plan.Sort()
	return plan, nil
}

// resolveGitCleanup fills the worktree and branch targets.
//
// The inventory itself is internal/git's, because what is dirty, unpushed, or
// unmerged is Git's own answer about a worktree, and this is the caller that
// acts on it.
func (s *service) resolveGitCleanup(
	ctx context.Context, cfg *config.Config, task *domain.Task, plan *reconcile.Plan,
) {
	repositories := make([]git.CleanupRepository, 0, len(task.Repositories))
	for _, binding := range task.Repositories {
		repository, known := cfg.Repositories[binding.RepositoryID.String()]
		if !known {
			plan.Problems = append(plan.Problems, fmt.Sprintf(
				"repository %s is no longer configured by project %s, so its worktree and branch cannot be resolved",
				binding.RepositoryID, task.ProjectID))
			continue
		}
		repositories = append(repositories, git.CleanupRepository{
			ID:           binding.RepositoryID,
			HostPath:     repository.HostPath,
			Remote:       repository.Remote,
			WorktreePath: binding.WorktreePath,
			Branch:       binding.Branch,
			BaseRef:      binding.BaseRef,
			BaseCommit:   binding.BaseCommit,
		})
	}

	inventory, err := s.git.CleanupPlanFor(ctx, git.CleanupRequest{
		Project:      task.ProjectID,
		Task:         task.ID,
		Root:         config.StaticPrefix(cfg.Git.WorktreeRoot),
		Repositories: repositories,
	})
	if err != nil {
		plan.Problems = append(plan.Problems, "the task's Git resources could not be resolved: "+err.Error())
		return
	}

	for _, worktree := range inventory.Worktrees {
		plan.Targets = append(plan.Targets, reconcile.Target{
			Class:      reconcile.ClassWorktrees,
			Identity:   worktree.Path,
			Repository: worktree.Repository,
			Detail:     "the task worktree of " + worktree.Repository.String(),
			Present:    worktree.Present,
			Warnings:   worktree.Warnings,
		})
	}
	for _, branch := range inventory.Branches {
		plan.Targets = append(plan.Targets, reconcile.Target{
			Class:      reconcile.ClassBranches,
			Identity:   branch.Name,
			Repository: branch.Repository,
			Detail:     "the task branch in " + branch.HostPath,
			Present:    branch.Present,
			Warnings:   branch.Warnings,
		})
	}
	for _, problem := range inventory.Problems {
		plan.Problems = append(plan.Problems, problem.Reason)
	}
}

// resolveTerminalCleanup fills the tmux target from live metadata.
//
// From metadata rather than from the record, because the record holds what Feat
// asked for and the window identifier is what tmux has now — and because a
// damaged terminal has no usable record at all while still being a thing the
// user may want gone.
func (s *service) resolveTerminalCleanup(ctx context.Context, task *domain.Task, plan *reconcile.Plan) {
	found, err := s.terminals.Discover(ctx)
	if err != nil {
		plan.Problems = append(plan.Problems, "the dedicated tmux server could not be read: "+err.Error())
		return
	}
	if terminal, ok := found.Terminal(task.ProjectID, task.ID); ok {
		plan.Targets = append(plan.Targets, reconcile.Target{
			Class:    reconcile.ClassTerminal,
			Identity: terminal.Target.Window,
			Detail:   "the task's tmux window and its panes",
			Present:  true,
		})
		return
	}
	for _, damaged := range found.DamageFor(task.ProjectID, task.ID) {
		if damaged.Kind != tmux.DamagedTerminal || damaged.ID == "" {
			continue
		}
		plan.Targets = append(plan.Targets, reconcile.Target{
			Class:    reconcile.ClassTerminal,
			Identity: damaged.ID,
			Detail:   "the task's tmux window, which Feat cannot otherwise use: " + damaged.Reason,
			Present:  true,
		})
		return
	}
	if task.Session != nil && task.Session.Tmux.Window != "" {
		plan.Targets = append(plan.Targets, reconcile.Target{
			Class:    reconcile.ClassTerminal,
			Identity: task.Session.Tmux.Window,
			Detail:   "the task's recorded tmux window, which is no longer there",
			Present:  false,
		})
	}
}

// resolveEnvironmentCleanup fills the agent container and volume targets.
func (s *service) resolveEnvironmentCleanup(ctx context.Context, task *domain.Task, plan *reconcile.Plan) {
	if task.Session == nil || task.Session.Execution == nil {
		s.resolveUnrecordedEnvironmentCleanup(ctx, task, plan)
		return
	}
	recorded := task.Session.Execution

	environment, err := s.environmentFor(task)
	if err != nil {
		plan.Problems = append(plan.Problems,
			"the task's agent environment could not be resolved, so its containers cannot be removed: "+err.Error())
		return
	}
	state, err := environment.Observe(ctx)
	if err != nil {
		plan.Problems = append(plan.Problems, fmt.Sprintf(
			"Compose project %s could not be observed: %s", recorded.Identity, err))
		return
	}
	plan.Targets = append(plan.Targets, reconcile.Target{
		Class:    reconcile.ClassAgentContainers,
		Identity: recorded.Identity,
		Detail:   "the containers and networks of the agent's Compose project",
		Present:  state.Present,
	})

	volumes, err := environment.Volumes(ctx)
	if err != nil {
		plan.Problems = append(plan.Problems, fmt.Sprintf(
			"the volumes of Compose project %s could not be listed: %s", recorded.Identity, err))
		return
	}
	for _, volume := range volumes {
		plan.Targets = append(plan.Targets, reconcile.Target{
			Class:    reconcile.ClassVolumes,
			Identity: volume,
			Detail:   "a volume of the agent's Compose project",
			Present:  true,
			// The warning is the policy's own, because the policy requires it:
			// the volume class is removable only when this exact warning was
			// confirmed, so retention by default does not depend on the daemon
			// remembering to attach it (FR-CLEAN-004).
			Warnings: []string{reconcile.WarningVolume},
		})
	}
}

// resolveUnrecordedEnvironmentCleanup names the containers, networks, and
// volumes of a task whose record carries no environment.
//
// A launch that fails after its container exists leaves exactly that. The
// container and its network are on the machine, the session that would have
// recorded them is never created, and this function's absence is what made the
// first `cleanup/execute` of such a task plan nothing, report success, and
// archive the task over resources nothing could then remove.
//
// It is a derivation and not a scan. The Compose project name comes from this
// task's own two identifiers, so what it finds belongs to this task by
// construction — which is the exactness FR-CLEAN-001 asks for, reached without
// the record that would ordinarily supply it. Nothing else on the machine is
// looked at, and a task that never had a container finds nothing rather than
// finding somebody else's.
func (s *service) resolveUnrecordedEnvironmentCleanup(
	ctx context.Context, task *domain.Task, plan *reconcile.Plan,
) {
	project, err := s.agentProject(task)
	if err != nil {
		// Not silence, and not an empty plan. A plan that cannot see a task's
		// containers is the plan the dogfood run archived a task over, so the
		// question that could not be asked is carried as a problem: a plan with
		// one is rendered un-archivable, and the user reads what Feat could not
		// see rather than an empty plan reporting success (ADR-059).
		plan.Problems = append(plan.Problems, err.Error())
		return
	}
	if project == nil {
		return
	}

	remains, err := project.Remains(ctx)
	if err != nil {
		plan.Problems = append(plan.Problems, fmt.Sprintf(
			"Compose project %s could not be observed: %s", project.Identity(), err))
		return
	}
	if !remains.Empty() {
		plan.Targets = append(plan.Targets, reconcile.Target{
			Class:    reconcile.ClassAgentContainers,
			Identity: project.Identity(),
			Detail: "the containers and networks of the agent's Compose project, which a launch left behind " +
				"and no session records: " + remains.Describe(),
			Present: true,
		})
	}

	volumes, err := project.Volumes(ctx)
	if err != nil {
		plan.Problems = append(plan.Problems, fmt.Sprintf(
			"the volumes of Compose project %s could not be listed: %s", project.Identity(), err))
		return
	}
	for _, volume := range volumes {
		plan.Targets = append(plan.Targets, reconcile.Target{
			Class:    reconcile.ClassVolumes,
			Identity: volume,
			Detail:   "a volume of the agent's Compose project",
			Present:  true,
			Warnings: []string{reconcile.WarningVolume},
		})
	}
}

// resolveRuntimeCleanup fills the application container and volume targets.
func (s *service) resolveRuntimeCleanup(ctx context.Context, task *domain.Task, plan *reconcile.Plan) {
	if task.Runtime == nil {
		return
	}
	services, err := s.runtimeAdapterFor(task)
	if err != nil {
		plan.Problems = append(plan.Problems,
			"the task's application services could not be resolved, so they cannot be removed: "+err.Error())
		return
	}

	state, err := services.Observe(ctx)
	if err != nil {
		plan.Problems = append(plan.Problems, fmt.Sprintf(
			"the application services of Compose project %s could not be observed: %s", task.Runtime.Identity, err))
		return
	}
	plan.Targets = append(plan.Targets, reconcile.Target{
		Class:    reconcile.ClassRuntimeContainers,
		Identity: task.Runtime.Identity,
		Detail:   "the containers and networks of the application's Compose project",
		Present:  state.Present,
	})

	volumes, err := services.Volumes(ctx)
	if err != nil {
		plan.Problems = append(plan.Problems, fmt.Sprintf(
			"the volumes of Compose project %s could not be listed: %s", task.Runtime.Identity, err))
		return
	}
	for _, volume := range volumes {
		plan.Targets = append(plan.Targets, reconcile.Target{
			Class:    reconcile.ClassVolumes,
			Identity: volume,
			Detail:   "a volume of the application's Compose project",
			Present:  true,
			Warnings: []string{reconcile.WarningVolume},
		})
	}
}

// resolveControlCleanup fills the control workspace target.
func (s *service) resolveControlCleanup(task *domain.Task, plan *reconcile.Plan) {
	workspace, err := s.controlWorkspace(task)
	if err != nil {
		plan.Problems = append(plan.Problems, "the task's control workspace could not be resolved: "+err.Error())
		return
	}
	if !workspace.Exists() {
		return
	}
	plan.Targets = append(plan.Targets, reconcile.Target{
		Class:    reconcile.ClassControl,
		Identity: workspace.Root(),
		Detail:   "the task brief, the agent's messages, and the generated agent files",
		Present:  true,
		Warnings: []string{
			"the control workspace is the record of what the agent was asked and what it reported, " +
				"and removing it removes that account",
		},
	})
}

// Cleanup removes exactly what a user selected from a plan they were shown.
//
// The plan is resolved again here rather than trusted from the request. The
// token proves the same resources are still named; the warnings are checked
// against what is true now, because a worktree that became dirty since the
// screen was drawn is the thing the confirmation exists to protect
// (FR-CLEAN-003, ADR-037).
func (s *service) Cleanup(
	ctx context.Context, id domain.TaskID, selection api.CleanupSelection,
) (api.CleanupResult, error) {
	// Held across the whole cycle, as every other load-change-save is
	// (ADR-036). A cleanup in particular must not interleave with a gate.
	defer s.locks.lock(id)()

	task, err := s.Task(ctx, id)
	if err != nil {
		return api.CleanupResult{}, err
	}
	if task.Workflow == domain.WorkflowArchived {
		return api.CleanupResult{}, fmt.Errorf("%w: task %s is already archived", api.ErrInvalid, id)
	}

	chosen, err := resolveSelection(selection)
	if err != nil {
		return api.CleanupResult{}, fmt.Errorf("%w: %w", api.ErrInvalid, err)
	}
	plan, err := s.resolveCleanup(ctx, task)
	if err != nil {
		return api.CleanupResult{}, err
	}
	if err := plan.Check(chosen); err != nil {
		return api.CleanupResult{}, fmt.Errorf("%w: %w", api.ErrInvalid, err)
	}

	result := api.CleanupResult{Task: task}
	for _, class := range reconcile.Classes() {
		if !chosen.Chose(class) {
			continue
		}
		removed, err := s.removeClass(ctx, task, plan, class)
		result.Removed = append(result.Removed, removed...)
		if err != nil {
			// Nothing is undone. What was removed is gone, and the record of it
			// is the event log; a partial cleanup is recoverable by asking for
			// the plan again, which will name what is left (ADR-029).
			s.recordCleanup(ctx, task, class, removed, err)
			return result, fmt.Errorf("%w: removing the %s of task %s: %w",
				api.ErrInvalid, class.Title(), task.ID, err)
		}
		s.recordCleanup(ctx, task, class, removed, nil)
	}

	if chosen.Archive {
		if err := s.archiveTask(ctx, task, result.Removed); err != nil {
			return result, err
		}
		result.Archived = true
	}
	result.Task = task
	return result, nil
}

// removeClass removes one class of resource and reports what went.
func (s *service) removeClass(
	ctx context.Context, task *domain.Task, plan *reconcile.Plan,
	class reconcile.Class,
) ([]api.CleanupRemoval, error) {
	switch class {
	case reconcile.ClassTerminal:
		return s.removeTerminal(ctx, task, plan)
	case reconcile.ClassAgentContainers:
		return s.removeAgentContainers(ctx, task, plan)
	case reconcile.ClassRuntimeContainers:
		return s.removeRuntimeContainers(ctx, task, plan)
	case reconcile.ClassVolumes:
		return s.removeVolumes(ctx, task, plan)
	case reconcile.ClassWorktrees:
		return s.removeWorktrees(ctx, task, plan)
	case reconcile.ClassBranches:
		return s.removeBranches(ctx, task, plan)
	case reconcile.ClassControl:
		return s.removeControl(ctx, task, plan)
	default:
		return nil, fmt.Errorf("%q is not a class of resource Feat removes", class)
	}
}

func (s *service) removeTerminal(
	ctx context.Context, task *domain.Task, plan *reconcile.Plan,
) ([]api.CleanupRemoval, error) {
	var removed []api.CleanupRemoval
	for _, target := range plan.For(reconcile.ClassTerminal) {
		gone, err := s.terminals.RemoveTask(ctx, task.ProjectID, task.ID)
		if err != nil {
			return removed, err
		}
		removed = append(removed, api.CleanupRemoval{
			Class: string(reconcile.ClassTerminal), Identity: target.Identity, Removed: gone,
		})
	}
	if task.Session != nil {
		if err := task.Session.Observe(domain.ProcessStopped, s.now()); err != nil {
			return removed, err
		}
		if err := s.store.Tasks().Save(ctx, task); err != nil {
			return removed, err
		}
	}
	return removed, nil
}

// removeAgentContainers destroys the agent's Compose project and removes what
// Feat generated to define it.
//
// The two are one act rather than two, in the order the resource and its
// definition were created in: the generated override is the document the destroy
// is run against, so it is removed once the project it described is gone
// (ADR-037 evidence 16).
func (s *service) removeAgentContainers(
	ctx context.Context, task *domain.Task, plan *reconcile.Plan,
) ([]api.CleanupRemoval, error) {
	removed, err := s.destroyAgentContainers(ctx, task, plan)
	if err != nil {
		return removed, err
	}

	directory, err := s.executionDirectory(task)
	if err != nil {
		return removed, err
	}
	generated, err := removeGeneratedInputs(directory, s.layout.ExecutionRoot())
	if generated {
		removed = append(removed, api.CleanupRemoval{
			Class: string(reconcile.ClassAgentContainers), Identity: directory, Removed: true,
		})
	}
	return removed, err
}

func (s *service) destroyAgentContainers(
	ctx context.Context, task *domain.Task, plan *reconcile.Plan,
) ([]api.CleanupRemoval, error) {
	targets := plan.For(reconcile.ClassAgentContainers)
	if len(targets) == 0 {
		return nil, nil
	}
	if task.Session == nil || task.Session.Execution == nil {
		return s.removeUnrecordedAgentContainers(ctx, task, targets)
	}
	environment, err := s.environmentFor(task)
	if err != nil {
		return nil, err
	}
	state, err := environment.Destroy(ctx)
	if err != nil {
		return nil, err
	}
	if task.Session != nil && task.Session.Execution != nil {
		task.Session.Execution.Observe(state.Container, state.Running, state.Status, state.Health, s.now())
		if err := s.store.Tasks().Save(ctx, task); err != nil {
			return nil, err
		}
	}
	return []api.CleanupRemoval{{
		Class: string(reconcile.ClassAgentContainers), Identity: targets[0].Identity, Removed: true,
	}}, nil
}

// removeUnrecordedAgentContainers removes what a launch left behind, by name.
//
// There is no record to update afterwards, which is the whole difference from
// the recorded path: the session that would carry the observation does not
// exist. What happened is written to the event log by recordCleanup like every
// other class, so the account of the removal survives the archive.
func (s *service) removeUnrecordedAgentContainers(
	ctx context.Context, task *domain.Task, targets []reconcile.Target,
) ([]api.CleanupRemoval, error) {
	project, err := s.agentProject(task)
	if err != nil {
		return nil, fmt.Errorf("%s was not removed: %w", targets[0].Identity, err)
	}
	if project == nil {
		// The plan is re-resolved immediately before this runs and its token
		// compared, so a target exists only because this same call answered a
		// moment ago. Reaching here means the project stopped being resolvable in
		// between, and removing nothing while reporting a removal is the one
		// outcome that must not happen.
		return nil, fmt.Errorf("the agent Compose project of task %s can no longer be resolved, "+
			"so %s was not removed; ask for the plan again", task.ID, targets[0].Identity)
	}
	if err := project.Destroy(ctx); err != nil {
		return nil, err
	}
	return []api.CleanupRemoval{{
		Class: string(reconcile.ClassAgentContainers), Identity: targets[0].Identity, Removed: true,
	}}, nil
}

// removeRuntimeContainers destroys the application's Compose project and removes
// what Feat generated to define it, for the reason removeAgentContainers does.
//
// The directory is also the working directory of every Compose command Feat runs
// for this task, which is why it goes after the destroy rather than before it.
func (s *service) removeRuntimeContainers(
	ctx context.Context, task *domain.Task, plan *reconcile.Plan,
) ([]api.CleanupRemoval, error) {
	removed, err := s.destroyRuntimeContainers(ctx, task, plan)
	if err != nil {
		return removed, err
	}

	directory, err := s.runtimeDirectory(task)
	if err != nil {
		return removed, err
	}
	generated, err := removeGeneratedInputs(directory, s.layout.RuntimeRoot())
	if generated {
		removed = append(removed, api.CleanupRemoval{
			Class: string(reconcile.ClassRuntimeContainers), Identity: directory, Removed: true,
		})
	}
	return removed, err
}

func (s *service) destroyRuntimeContainers(
	ctx context.Context, task *domain.Task, plan *reconcile.Plan,
) ([]api.CleanupRemoval, error) {
	targets := plan.For(reconcile.ClassRuntimeContainers)
	if len(targets) == 0 {
		return nil, nil
	}
	services, err := s.runtimeAdapterFor(task)
	if err != nil {
		return nil, err
	}
	state, err := services.Destroy(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.recordRuntime(ctx, task, task.Runtime, state, nil, "cleanup"); err != nil {
		return nil, err
	}
	return []api.CleanupRemoval{{
		Class: string(reconcile.ClassRuntimeContainers), Identity: targets[0].Identity, Removed: true,
	}}, nil
}

// removeGeneratedInputs removes one task's directory of generated Compose input,
// and reports whether there was one.
//
// This is the mirror of creation ADR-037 applied to the two roots it did not
// reach. A task's Compose override is generated from the task, written to
// `<root>/<project-id>/<task-id>/`, and never removed, so every task that has
// ever launched leaves a directory behind — 47 of the 48 under the execution
// root of the dogfood machine belonged to tasks that had been cleaned up and
// archived. It is not a target the user chooses about, for the reason the
// directories above a worktree are not: it is where a resource was defined, not
// a resource (ADR-037 evidence 16).
//
// The project's directory is left, exactly as the worktree walk leaves it. A
// project outlives every task in it, and the next task of that project is
// created inside it.
//
// The path is computed from a validated project and task identifier under a root
// the daemon resolved, never one a caller supplied, and it is checked again here
// because this function deletes a directory tree.
func removeGeneratedInputs(directory, root string) (bool, error) {
	cleaned := filepath.Clean(directory)
	if !filepath.IsAbs(cleaned) || paths.Broad(cleaned) {
		return false, fmt.Errorf(
			"refusing to remove the generated Compose input %q: it is not a directory Feat owns", directory)
	}
	// It lives two levels below its root, at <root>/<project-id>/<task-id>, so
	// anything shallower or outside is not one whatever else it may be.
	if !paths.Under(root, cleaned) || paths.Depth(cleaned) != paths.Depth(root)+2 {
		return false, fmt.Errorf(
			"refusing to remove the generated Compose input %q: it is not a task's directory under %s",
			directory, root)
	}

	if _, err := os.Stat(cleaned); err != nil {
		if errors.Is(err, iofs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("reading the generated Compose input %s: %w", cleaned, err)
	}
	if err := os.RemoveAll(cleaned); err != nil {
		return false, fmt.Errorf("removing the generated Compose input %s: %w", cleaned, err)
	}
	return true, nil
}

// removeVolumes removes the volumes of whichever Compose projects the plan
// named them for.
//
// By name, one at a time. The names came from the container runtime's own label
// enumeration, so a resource the project declares external was never in the list
// and is not excluded by a rule anybody has to remember (FR-RUN-008, ADR-037).
func (s *service) removeVolumes(
	ctx context.Context, task *domain.Task, plan *reconcile.Plan,
) ([]api.CleanupRemoval, error) {
	targets := plan.For(reconcile.ClassVolumes)
	if len(targets) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(targets))
	for _, target := range targets {
		names = append(names, target.Identity)
	}

	var removed []string
	switch {
	case task.Session != nil && task.Session.Execution != nil:
		environment, err := s.environmentFor(task)
		if err != nil {
			return nil, err
		}
		gone, err := environment.RemoveVolumes(ctx, names)
		removed = append(removed, gone...)
		if err != nil {
			return volumeRemovals(names, removed), err
		}
	default:
		// A volume of a launch that left no record. It is asked by name for the
		// same reason its containers are: a volume the launch created carries the
		// project's label whether or not anything wrote the project down.
		//
		// A question that could not be asked fails the removal rather than
		// skipping it. Reporting a user's confirmed selection as not removed,
		// with no error and no problem, tells them their choice was declined and
		// not why — and the volumes are still on the machine either way.
		project, err := s.agentProject(task)
		if err != nil {
			return volumeRemovals(names, removed), err
		}
		if project != nil {
			gone, err := project.RemoveVolumes(ctx, names)
			removed = append(removed, gone...)
			if err != nil {
				return volumeRemovals(names, removed), err
			}
		}
	}
	if task.Runtime != nil {
		services, err := s.runtimeAdapterFor(task)
		if err != nil {
			return volumeRemovals(names, removed), err
		}
		// Whatever the first adapter did not remove. Both are asked, because a
		// volume belongs to one of the two Compose projects and neither adapter
		// knows about the other's.
		gone, err := services.RemoveVolumes(ctx, remaining(names, removed))
		removed = append(removed, gone...)
		if err != nil {
			return volumeRemovals(names, removed), err
		}
	}
	return volumeRemovals(names, removed), nil
}

// remaining returns the names not yet removed.
func remaining(names, removed []string) []string {
	gone := make(map[string]bool, len(removed))
	for _, name := range removed {
		gone[name] = true
	}
	var left []string
	for _, name := range names {
		if !gone[name] {
			left = append(left, name)
		}
	}
	return left
}

// volumeRemovals reports one entry per named volume, saying which went.
func volumeRemovals(names, removed []string) []api.CleanupRemoval {
	gone := make(map[string]bool, len(removed))
	for _, name := range removed {
		gone[name] = true
	}
	entries := make([]api.CleanupRemoval, 0, len(names))
	for _, name := range names {
		entries = append(entries, api.CleanupRemoval{
			Class: string(reconcile.ClassVolumes), Identity: name, Removed: gone[name],
		})
	}
	return entries
}

func (s *service) removeWorktrees(
	ctx context.Context, task *domain.Task, plan *reconcile.Plan,
) ([]api.CleanupRemoval, error) {
	request, err := s.gitRemoveRequest(task)
	if err != nil {
		return nil, err
	}

	var removed []api.CleanupRemoval
	for _, target := range plan.For(reconcile.ClassWorktrees) {
		req := request[target.Repository]
		// Forced only where the plan itself said work would be lost, and the
		// selection confirmed it. A worktree with nothing at risk is removed
		// with Git's own safety in place.
		req.Force = target.Risky()

		removal, err := s.git.RemoveWorktree(ctx, target.Identity, req)
		removed = append(removed, api.CleanupRemoval{
			Class: string(reconcile.ClassWorktrees), Identity: target.Identity, Removed: removal.Removed,
		})
		// The generated directories the worktree sat in, when it was the last
		// thing in them. They are reported rather than done quietly: a cleanup
		// says what it removed, and a directory Feat deleted is part of that
		// answer even though no target named it.
		for _, directory := range removal.Directories {
			removed = append(removed, api.CleanupRemoval{
				Class: string(reconcile.ClassWorktrees), Identity: directory, Removed: true,
			})
		}
		if err != nil {
			return removed, err
		}
	}
	// The binding is deliberately left as it is. A recorded worktree path is
	// desired state and whether the directory exists is observed (ADR-029), so
	// clearing it would delete the account of what the task had — which is the
	// thing archiving exists to keep.
	return removed, nil
}

func (s *service) removeBranches(
	ctx context.Context, task *domain.Task, plan *reconcile.Plan,
) ([]api.CleanupRemoval, error) {
	request, err := s.gitRemoveRequest(task)
	if err != nil {
		return nil, err
	}

	var removed []api.CleanupRemoval
	for _, target := range plan.For(reconcile.ClassBranches) {
		req := request[target.Repository]
		req.Force = target.Risky()

		gone, err := s.git.DeleteBranch(ctx, target.Identity, req)
		removed = append(removed, api.CleanupRemoval{
			Class: string(reconcile.ClassBranches), Identity: target.Identity, Removed: gone,
		})
		if err != nil {
			return removed, err
		}
	}
	return removed, nil
}

// gitRemoveRequest builds the per-repository removal context.
//
// Every field of it is what the path check needs, and the check runs again
// inside internal/git immediately before anything is deleted. The project's own
// directory is resolved beside the root because the two bound different things:
// the root is what a worktree must be inside, and the project's directory is
// where the tidy-up above a removed worktree stops.
func (s *service) gitRemoveRequest(task *domain.Task) (map[domain.RepositoryID]git.RemoveRequest, error) {
	cfg, err := config.Load(s.layout.ProjectConfigDir(), task.ProjectID.String(), s.configOptions())
	if err != nil {
		return nil, translateConfig(err)
	}
	root := config.StaticPrefix(cfg.Git.WorktreeRoot)
	projectDir := config.ProjectPrefix(cfg.Git.WorktreeRoot, task.ProjectID.String())

	checkouts := make([]string, 0, len(task.Repositories))
	for _, binding := range task.Repositories {
		if repository, known := cfg.Repositories[binding.RepositoryID.String()]; known {
			checkouts = append(checkouts, repository.HostPath)
		}
	}

	requests := make(map[domain.RepositoryID]git.RemoveRequest, len(task.Repositories))
	for _, binding := range task.Repositories {
		repository, known := cfg.Repositories[binding.RepositoryID.String()]
		if !known {
			continue
		}
		requests[binding.RepositoryID] = git.RemoveRequest{
			HostPath:   repository.HostPath,
			Root:       root,
			ProjectDir: projectDir,
			Checkouts:  checkouts,
		}
	}
	return requests, nil
}

// removeControl removes the task's control workspace, once nothing Feat started
// is still holding it.
func (s *service) removeControl(
	ctx context.Context, task *domain.Task, plan *reconcile.Plan,
) ([]api.CleanupRemoval, error) {
	targets := plan.For(reconcile.ClassControl)
	if len(targets) == 0 {
		return nil, nil
	}
	if err := s.controlWorkspaceReleased(ctx, task); err != nil {
		return nil, err
	}
	workspace, err := s.controlWorkspace(task)
	if err != nil {
		return nil, err
	}
	gone, err := workspace.Remove()
	if err != nil {
		return nil, err
	}
	s.forgetWorkspace(task.ID)

	if task.Session != nil {
		task.Session.ControlPath = ""
		if err := s.store.Tasks().Save(ctx, task); err != nil {
			return nil, err
		}
	}
	return []api.CleanupRemoval{{
		Class: string(reconcile.ClassControl), Identity: targets[0].Identity, Removed: gone,
	}}, nil
}

// controlWorkspaceReleased establishes that no container of the task's agent
// Compose project is still there.
//
// The workspace is a bind-mount source for that container, and ADR-032's
// read-only split made it three of them. On macOS the file-sharing layer holds a
// directory that is an active mount source: the first `cleanup/execute` of a
// task whose container was still running failed with `unlinkat …/outbox:
// permission denied`, and the second, once the container had died, succeeded. So
// this is an ordering rule rather than a permissions bug — destroy the
// containers, establish that they are gone, and only then remove the tree they
// mounted.
//
// The class order alone does not establish it. It removes the agent containers
// before the control workspace, but only when a user chose both, and a launch
// that failed after its container exists leaves a container the record does not
// name in a task whose plan may contain nothing else.
//
// What it asks about is containers rather than mounts, and the difference is
// worth stating: Feat can establish that the containers it started for this task
// are gone, and cannot establish that nothing at all on the machine has the
// directory open. Only the agent's Compose project is asked, because it is the
// only one the workspace is mounted into — the application runtime's generated
// override mounts worktrees and never the control tree (ADR-034).
//
// A container that has stopped holds nothing. ADR-059's evidence is an ordering
// — the removal failed while the container was up and succeeded once it had died
// — so the containers this refuses over are the ones that have not stopped.
// `feat task stop` overnight leaves exited containers on purpose (ADR-057), and
// refusing a control-workspace cleanup over those would refuse in exactly the
// state the removal works in. A container whose state Docker did not report is
// counted as holding it, for the same reason the rest of this function refuses
// on what it cannot establish.
//
// An unanswerable question refuses. Feat cannot say the tree is unheld while
// Docker will not say what it holds — nor while it cannot be asked at all, which
// is what a project switched to host mode after the launch and a Docker that has
// left the daemon's PATH both produce — and a refusal that names why leaves the
// user a workspace they can still remove after fixing it, where proceeding
// leaves a half-removed tree and no account of it.
func (s *service) controlWorkspaceReleased(ctx context.Context, task *domain.Task) error {
	project, err := s.agentProject(task)
	if err != nil {
		return fmt.Errorf("whether a container still mounts the control workspace of task %s "+
			"could not be established: %w", task.ID, err)
	}
	if project == nil {
		return nil
	}

	remains, err := project.Remains(ctx)
	if err != nil {
		return fmt.Errorf("the Compose project %s could not be observed, so Feat cannot establish that no "+
			"container still mounts the control workspace of task %s: %w", project.Identity(), task.ID, err)
	}
	held := remains.Live()
	if held.Empty() {
		return nil
	}
	return fmt.Errorf("the control workspace of task %s is mounted into %s of Compose project %s, "+
		"so removing it now would fail part way through. Remove the agent containers first — a cleanup that "+
		"selects them as well as the control workspace does both in that order",
		task.ID, held.Describe(), project.Identity())
}

// recordCleanup writes what one class removed to the task's event log.
//
// The log is where "Feat can explain what happened later" lives: it is
// append-only, per task, and survives the archive. A failure is recorded too,
// because a cleanup that stopped half way is the case a user most needs an
// account of.
func (s *service) recordCleanup(
	ctx context.Context, task *domain.Task, class reconcile.Class, removed []api.CleanupRemoval, failure error,
) {
	names := make([]string, 0, len(removed))
	for _, entry := range removed {
		if entry.Removed {
			names = append(names, entry.Identity)
		}
	}

	detail := "removed the " + class.Title()
	if len(names) > 0 {
		detail += ": " + strings.Join(names, ", ")
	} else {
		detail = "nothing was left to remove for the " + class.Title()
	}
	if failure != nil {
		detail = "removing the " + class.Title() + " failed after " + detail + ": " + failure.Error()
	}
	s.record(ctx, task, domain.Event{Type: domain.EventCleanedUp, To: string(class), Detail: detail})
}

// archiveTask records the task as archived.
//
// Nothing is deleted from the state directory. The snapshot keeps the branches,
// the bases, and the session it recorded, and the event log keeps what each
// class removed, so what a user asked for and what became of it are both still
// answerable — which is what docs/02-user-workflows.md means by archiving task
// metadata (ADR-037).
func (s *service) archiveTask(ctx context.Context, task *domain.Task, removed []api.CleanupRemoval) error {
	classes := make(map[string]bool, len(removed))
	for _, entry := range removed {
		classes[entry.Class] = true
	}
	names := make([]string, 0, len(classes))
	for _, class := range reconcile.Classes() {
		if classes[string(class)] {
			names = append(names, class.Title())
		}
	}

	detail := "the task was archived and nothing was removed"
	if len(names) > 0 {
		detail = "the task was archived after removing its " + strings.Join(names, ", ")
	}
	return s.transition(ctx, task, domain.WorkflowArchived, detail)
}

// runtimeAdapterFor rebuilds the adapter for a task's recorded application
// services, without touching the task's record.
//
// It differs from runtimeFor, which attaches or refreshes a runtime record as
// part of an action. A cleanup plan must not create a runtime record for a task
// that has none: asking what a task owns should not give it something.
func (s *service) runtimeAdapterFor(task *domain.Task) (runtime.Runtime, error) {
	if task.Runtime == nil {
		return nil, fmt.Errorf("task %s records no application runtime", task.ID)
	}
	cfg, err := config.Load(s.layout.ProjectConfigDir(), task.ProjectID.String(), s.configOptions())
	if err != nil {
		return nil, translateConfig(err)
	}
	if !cfg.HasRuntime() {
		return nil, ErrRuntimeUnconfigured
	}

	spec, err := s.runtimeSpec(cfg, task, readComposition(s.env, cfg))
	if err != nil {
		return nil, err
	}
	return s.runtimes(recordedInputs(spec, task.Runtime, cfg.Runtime.BindAddress))
}

// forgetWorkspace drops the cached control workspace of a task whose workspace
// has been removed, so a later poll opens a fresh one rather than reading the
// applied-event record of a directory that is gone.
func (s *service) forgetWorkspace(id domain.TaskID) {
	s.workspaceMu.Lock()
	defer s.workspaceMu.Unlock()
	delete(s.workspaces, id)
}

// renderCleanupPlan maps a resolved plan onto the wire.
//
// The mapping lives here rather than in internal/api because the transport is a
// third representation and may not import the policy: renaming a field in
// internal/reconcile must not silently change a published surface (ADR-027).
func (s *service) renderCleanupPlan(task *domain.Task, plan *reconcile.Plan) api.CleanupPlan {
	rendered := api.CleanupPlan{
		TaskID:     task.ID.String(),
		TaskKey:    task.ID.Key().String(),
		ProjectID:  task.ProjectID.String(),
		Workflow:   string(task.Workflow),
		Token:      plan.Token(),
		Problems:   plan.Problems,
		Archivable: len(plan.Problems) == 0,
		ResolvedAt: s.now().UTC(),
	}
	for _, class := range plan.Classes() {
		entry := api.CleanupClass{
			Class:    string(class),
			Title:    class.Title(),
			Warnings: plan.Warnings(class),
		}
		for _, target := range plan.For(class) {
			entry.Targets = append(entry.Targets, api.CleanupTarget{
				Identity:   target.Identity,
				Repository: target.Repository.String(),
				Detail:     target.Detail,
				Present:    target.Present,
				Warnings:   target.Warnings,
			})
		}
		rendered.Classes = append(rendered.Classes, entry)
	}
	return rendered
}

// resolveSelection turns a wire selection into the policy's, validating the
// class vocabulary on the way.
func resolveSelection(selection api.CleanupSelection) (reconcile.Selection, error) {
	chosen := reconcile.Selection{Token: selection.Token, Archive: selection.Archive}
	for _, choice := range selection.Classes {
		class := reconcile.Class(choice.Class)
		if !class.Valid() {
			return reconcile.Selection{}, fmt.Errorf("%q is not a class of resource Feat removes", choice.Class)
		}
		chosen.Classes = append(chosen.Classes, reconcile.Choice{
			Class:             class,
			ConfirmedWarnings: choice.ConfirmedWarnings,
		})
	}
	return chosen, nil
}
