//go:build manual

// This file is a hand-driven probe, not part of the test suite: the `manual`
// build tag keeps it out of `go test ./...`, `make check`, and CI. It exists so
// that the Git and devcontainer lifecycles can be driven against a real project
// by hand, without going through the commands that do it properly.
//
// Dry run — resolves bases and proposes names, and creates nothing. It fetches
// the configured remotes, which updates remote-tracking refs and never touches
// a working tree, an index, or a checked-out branch:
//
//	FEAT_PROJECT=jobharbor-dev \
//	  go test -tags manual -run TestManualPrepare ./internal/daemon/ -v
//
// Choose the repositories and their access, instead of the project's defaults:
//
//	FEAT_PROJECT=jobharbor-dev FEAT_REPOS=api:rw,frontend:ro \
//	  go test -tags manual -run TestManualPrepare ./internal/daemon/ -v
//
// Real run — creates the branches and worktrees, then prints the cleanup plan
// and the commands that undo it:
//
//	FEAT_PROJECT=jobharbor-dev FEAT_REPOS=api:rw,frontend:ro FEAT_APPLY=1 \
//	  go test -tags manual -run TestManualPrepare ./internal/daemon/ -v
//
// Task snapshots go to a temporary state directory, so nothing is left in
// ~/.local/share/feat. Worktrees go wherever git.worktree_root resolves to,
// which is a real location the project chose; the undo commands cover it.
package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/config"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/execution/compose"
	"github.com/ma8el/feat/internal/git"
	"github.com/ma8el/feat/internal/paths"
	"github.com/ma8el/feat/internal/project"
	"github.com/ma8el/feat/internal/store"
)

func TestManualPrepare(t *testing.T) {
	project := os.Getenv("FEAT_PROJECT")
	if project == "" {
		t.Skip("set FEAT_PROJECT to the identifier of a project in ~/.config/feat/projects")
	}

	env, err := paths.Current()
	if err != nil {
		t.Fatalf("resolving the environment: %v", err)
	}
	real, err := paths.Resolve(env)
	if err != nil {
		t.Fatalf("resolving the layout: %v", err)
	}

	// The configuration is the user's own; the state is not, so a probe leaves
	// no task behind in the real state directory.
	layout := paths.Layout{
		Config:  real.Config,
		State:   t.TempDir(),
		Runtime: t.TempDir(),
	}
	layout.Socket = filepath.Join(layout.Runtime, "feat.sock")

	instance, err := New(Options{Layout: layout, Environment: env, Build: testBuild})
	if err != nil {
		t.Fatalf("preparing the daemon: %v", err)
	}
	service := instance.service

	cfg, err := config.Load(layout.ProjectConfigDir(), project, service.configOptions())
	if err != nil {
		t.Fatalf("loading the project configuration:\n%v", err)
	}

	title := os.Getenv("FEAT_TITLE")
	if title == "" {
		title = "Manual Git lifecycle probe"
	}
	task, err := domain.NewTask(domain.NewTaskID(), domain.ProjectID(project), title,
		domain.TaskSource{Kind: domain.SourcePrompt}, time.Now())
	if err != nil {
		t.Fatalf("creating the draft: %v", err)
	}
	if err := task.SetBrief("A probe of the Git lifecycle. Nothing runs an agent yet.", time.Now()); err != nil {
		t.Fatalf("setting the brief: %v", err)
	}

	selected, err := manualSelection(cfg)
	if err != nil {
		t.Fatalf("%v", err)
	}

	fmt.Printf("\nproject   %s (%s)\ntask      %s  key %s\ntitle     %s\nselection %s\n\n",
		cfg.Project.Name, project, task.ID, task.Key(), title, renderSelection(selected))

	// The dry run: what a draft would show the user. It writes no state.
	request, err := gitRequest(cfg, task, selected)
	if err != nil {
		t.Fatalf("building the Git request: %v", err)
	}

	fmt.Printf("worktree root Feat may own: %s\nfetch before resolving:     %t\n\n",
		request.Root, request.Fetch)

	plan, planErr := git.Host().Plan(context.Background(), request)
	for _, note := range plan.Notes {
		fmt.Printf("note  %s: %s\n", note.Repository, note.Summary)
	}
	for _, repository := range plan.Repositories {
		fmt.Printf("repository %s (%s)\n  base      %s\n            %s\n  branch    %s\n  worktree  %s\n  container %s\n",
			repository.ID, repository.Access, repository.BaseRef, repository.BaseCommit,
			orNone(repository.Branch), repository.WorktreePath, orNone(repository.ContainerPath))
	}
	if planErr != nil {
		fmt.Printf("\nthis task cannot be prepared:\n%v\n", planErr)
		t.Fatal("the plan has problems; nothing was created")
	}

	if os.Getenv("FEAT_APPLY") == "" {
		fmt.Printf("\nnothing was created. Set FEAT_APPLY=1 to create these branches and worktrees.\n")
		return
	}

	// The real run. The task is stored first, which is what makes the next step
	// recoverable, and PrepareTask repeats the plan for itself.
	if err := service.store.Projects().Save(context.Background(), mustProject(t, cfg)); err != nil {
		t.Fatalf("recording the project: %v", err)
	}
	if err := service.store.Tasks().Save(context.Background(), task); err != nil {
		t.Fatalf("saving the draft: %v", err)
	}

	ref := store.Ref(task)
	prepared, err := service.PrepareTask(context.Background(), ref, selected)
	if err != nil {
		fmt.Printf("\npreparation failed: %v\n", err)
	}
	fmt.Printf("\ntask is now %s\n", prepared.Workflow)

	for _, binding := range prepared.Repositories {
		state := "not created"
		if binding.Observation != nil {
			state = fmt.Sprintf("created, %d changed file(s), dirty=%t",
				binding.Observation.ChangedFiles, binding.Observation.Dirty)
		}
		fmt.Printf("  %-12s %s\n               %s\n", binding.RepositoryID, state, binding.WorktreePath)
	}

	// What cleanup would find, and what to type to undo this by hand.
	cleanup, err := git.Host().CleanupPlanFor(context.Background(), cleanupRequest(request, prepared))
	if err != nil {
		t.Fatalf("planning cleanup: %v", err)
	}

	fmt.Printf("\ncleanup plan (nothing is removed by this probe)\n")
	for _, worktree := range cleanup.Worktrees {
		fmt.Printf("  worktree %-12s present=%t registered=%t dirty=%t %v\n",
			worktree.Repository, worktree.Present, worktree.Registered, worktree.Dirty, worktree.Warnings)
	}
	for _, branch := range cleanup.Branches {
		fmt.Printf("  branch   %-12s %s present=%t merged=%t unpushed=%d %v\n",
			branch.Repository, branch.Name, branch.Present, branch.Merged, branch.Unpushed, branch.Warnings)
	}
	for _, problem := range cleanup.Problems {
		fmt.Printf("  refused  %s: %s\n", problem.Repository, problem.Reason)
	}

	fmt.Printf("\nundo:\n")
	for _, repository := range request.Repositories {
		fmt.Printf("  git -C %s worktree remove %s\n", repository.HostPath, repository.WorktreePath)
		if repository.Branch != "" {
			fmt.Printf("  git -C %s branch -D %s\n", repository.HostPath, repository.Branch)
		}
	}
}

// manualSelection reads FEAT_REPOS, or falls back to the project's defaults.
func manualSelection(cfg *config.Config) ([]Selection, error) {
	raw := os.Getenv("FEAT_REPOS")
	if raw == "" {
		selected := DefaultSelection(cfg)
		if len(selected) == 0 {
			return nil, fmt.Errorf("this project selects no repository by default; set FEAT_REPOS, for example %s",
				"FEAT_REPOS="+strings.Join(cfg.RepositoryIDs(), ":rw,")+":rw")
		}
		return selected, nil
	}

	var selection []Selection
	for _, entry := range strings.Split(raw, ",") {
		id, access, found := strings.Cut(strings.TrimSpace(entry), ":")
		if !found {
			access = "rw"
		}
		mode := domain.TaskAccessReadWrite
		switch access {
		case "rw", "read_write":
		case "ro", "read_only":
			mode = domain.TaskAccessReadOnly
		default:
			return nil, fmt.Errorf("%q is not an access mode: use rw or ro", access)
		}
		selection = append(selection, Selection{Repository: domain.RepositoryID(id), Access: mode})
	}
	return selection, nil
}

// cleanupRequest asks what the prepared task now owns.
func cleanupRequest(request git.Request, task *domain.Task) git.CleanupRequest {
	cleanup := git.CleanupRequest{Project: task.ProjectID, Task: task.ID, Root: request.Root}
	for _, repository := range request.Repositories {
		binding, bound := task.Repository(repository.ID)
		if !bound {
			continue
		}
		cleanup.Repositories = append(cleanup.Repositories, git.CleanupRepository{
			ID:           repository.ID,
			HostPath:     repository.HostPath,
			Remote:       repository.Remote,
			WorktreePath: binding.WorktreePath,
			Branch:       binding.Branch,
			BaseRef:      binding.BaseRef,
			BaseCommit:   binding.BaseCommit,
		})
	}
	return cleanup
}

func mustProject(t *testing.T, cfg *config.Config) *domain.Project {
	t.Helper()

	registered, err := project.FromConfig(cfg, time.Now())
	if err != nil {
		t.Fatalf("mapping the configuration onto a project: %v", err)
	}
	return registered
}

func orNone(value string) string {
	if value == "" {
		return "(none)"
	}
	return value
}

func renderSelection(selection []Selection) string {
	rendered := make([]string, 0, len(selection))
	for _, one := range selection {
		rendered = append(rendered, fmt.Sprintf("%s:%s", one.Repository, one.Access))
	}
	return strings.Join(rendered, ", ")
}

// TestManualDevcontainer launches real tasks in the project's real devcontainer
// and reports what the containers turned out to be.
//
// It is the devcontainer probe. Everything it drives is real: Git, tmux,
// Docker, and Claude. Task records go to a temporary state directory, but the
// worktrees, the containers, and the tmux windows are not temporary, so it
// prints the commands that undo them.
//
//	FEAT_PROJECT=jobharbor-dev FEAT_REPOS=api:rw,frontend:ro FEAT_APPLY=1 \
//	  go test -tags manual -run TestManualDevcontainer ./internal/daemon/ -v
//
// FEAT_TASKS=3 launches three at once, which is the concurrency criterion.
func TestManualDevcontainer(t *testing.T) {
	project := os.Getenv("FEAT_PROJECT")
	if project == "" {
		t.Skip("set FEAT_PROJECT to the identifier of a project in ~/.config/feat/projects")
	}

	env, err := paths.Current()
	if err != nil {
		t.Fatalf("resolving the environment: %v", err)
	}
	real, err := paths.Resolve(env)
	if err != nil {
		t.Fatalf("resolving the layout: %v", err)
	}

	// The user's own configuration, and state of our own so that a probe leaves
	// no task behind. The runtime directory is ours too, so the tmux server this
	// starts is not the one a running daemon owns.
	layout := paths.Layout{Config: real.Config, State: t.TempDir(), Runtime: t.TempDir()}
	layout.Socket = filepath.Join(layout.Runtime, "feat.sock")

	instance, err := New(Options{Layout: layout, Environment: env, Build: testBuild})
	if err != nil {
		t.Fatalf("preparing the daemon: %v", err)
	}
	service := instance.service
	ctx := context.Background()

	if _, err := service.RegisterProject(ctx, domain.ProjectID(project)); err != nil {
		t.Fatalf("registering the project:\n%v", err)
	}
	cfg, err := config.Load(layout.ProjectConfigDir(), project, service.configOptions())
	if err != nil {
		t.Fatalf("loading the project configuration:\n%v", err)
	}
	if !cfg.Agent.Execution.Devcontainer() {
		t.Fatalf("project %s does not run its agent in a devcontainer", project)
	}

	selected, err := manualSelection(cfg)
	if err != nil {
		t.Fatalf("%v", err)
	}
	count := 1
	if raw := os.Getenv("FEAT_TASKS"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			count = parsed
		}
	}

	fmt.Printf("\nproject   %s (%s)\nservice   %s as %s\nselection %s\ntasks     %d\n\n",
		cfg.Project.Name, project, cfg.Agent.Execution.Service, cfg.Agent.Execution.User,
		renderSelection(selected), count)

	if os.Getenv("FEAT_APPLY") == "" {
		fmt.Printf("nothing was created. Set FEAT_APPLY=1 to launch, which creates worktrees, " +
			"containers, and tmux windows.\n")
		return
	}

	var launched []*domain.Task
	for i := range count {
		task := manualLaunch(t, service, project, selected, i+1)
		if task != nil {
			launched = append(launched, task)
		}
	}

	fmt.Printf("\n=== what the containers turned out to be ===\n")
	for _, task := range launched {
		manualReport(t, service, task)
	}

	fmt.Printf("\nundo:\n")
	for _, task := range launched {
		if task.Session != nil && task.Session.Execution != nil {
			fmt.Printf("  docker compose --project-name %s --project-directory %s --file %s --file %s down\n",
				task.Session.Execution.Identity, filepath.Dir(task.Session.Execution.Files[0]),
				strings.Join(task.Session.Execution.Files, " --file "),
				task.Session.Execution.GeneratedOverridePath)
		}
		for _, binding := range task.Repositories {
			repository, ok := cfg.Repository(binding.RepositoryID.String())
			if !ok || binding.WorktreePath == "" {
				continue
			}
			fmt.Printf("  git -C %s worktree remove --force %s\n", repository.HostPath, binding.WorktreePath)
			if binding.Branch != "" {
				fmt.Printf("  git -C %s branch -D %s\n", repository.HostPath, binding.Branch)
			}
		}
	}
	fmt.Printf("  tmux -S %s kill-server\n", layout.TmuxSocket())
}

// manualLaunch drives one task through the whole preparation and launch.
func manualLaunch(t *testing.T, service *service, project string, selected []Selection, n int) *domain.Task {
	t.Helper()
	ctx := context.Background()

	title := fmt.Sprintf("Devcontainer probe %d", n)
	draft, err := service.CreateDraft(ctx, api.DraftRequest{
		Project: domain.ProjectID(project), Title: title,
		Brief:  "A probe of devcontainer execution. Read your task brief and then report with feat-report.",
		Source: domain.TaskSource{Kind: domain.SourcePrompt},
	})
	if err != nil {
		t.Fatalf("creating the draft: %v", err)
	}

	update := api.DraftUpdate{Title: title, Brief: draft.Brief}
	for _, selection := range selected {
		update.Repositories = append(update.Repositories, api.DraftSelection{
			Repository: selection.Repository, Access: selection.Access,
		})
	}
	if _, err := service.UpdateDraft(ctx, draft.ID, update); err != nil {
		t.Fatalf("selecting repositories: %v", err)
	}
	plan, err := service.PlanDraft(ctx, draft.ID)
	if err != nil {
		t.Fatalf("planning the draft: %v", err)
	}

	task, err := service.LaunchDraft(ctx, draft.ID, plan.Fingerprint)
	if err != nil {
		fmt.Printf("task %d (%s) could not launch:\n  %v\n", n, draft.Key(), err)
		return nil
	}
	fmt.Printf("task %d  %s  %s\n", n, task.Key(), task.Workflow)
	return task
}

// manualReport prints what the task's container is, which is the evidence the
// acceptance criteria are about.
func manualReport(t *testing.T, service *service, task *domain.Task) {
	t.Helper()

	if task.Session == nil || task.Session.Execution == nil {
		fmt.Printf("\n%s: no execution environment was recorded\n", task.Key())
		return
	}
	recorded := task.Session.Execution
	fmt.Printf("\n%s  compose project %s\n  service %s as %s, container %s (%s)\n",
		task.Key(), recorded.Identity, recorded.Service, recorded.User,
		recorded.Container, recorded.Status)

	environment, err := service.environmentFor(task)
	if err != nil {
		fmt.Printf("  the environment could not be rebuilt: %v\n", err)
		return
	}
	report, err := environment.Inspect(context.Background(),
		[]string{"/feat/outbox", "/feat/reports"})
	if err != nil {
		fmt.Printf("  the container could not be inspected: %v\n", err)
		return
	}

	fmt.Printf("  uid %d (%s), docker clients %v, docker variables %v, missing tools %v, unwritable %v\n",
		report.UID, report.User, report.DockerClients, report.DockerVariables,
		report.MissingTools, report.Unwritable)
	for _, mount := range compose.Sources(report.Mounts) {
		fmt.Printf("    mount %s\n", mount)
	}
	if err := environment.Check(report); err != nil {
		fmt.Printf("  REFUSED: %v\n", err)
	} else {
		fmt.Printf("  every requirement is met\n")
	}
}
