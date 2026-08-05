//go:build manual

// This file is a hand-driven probe, not part of the test suite: the `manual`
// build tag keeps it out of `go test ./...`, `make check`, and CI. It exists so
// that slice 4 can be exercised against a real project before slice 6 delivers
// the command that would do it properly.
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
	"strings"
	"testing"
	"time"

	"github.com/ma8el/feat/internal/config"
	"github.com/ma8el/feat/internal/domain"
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
		title = "Manual slice 4 probe"
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

	fmt.Printf("\ncleanup plan (nothing is removed by this slice)\n")
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
