package storetest

import (
	"fmt"
	"time"

	"github.com/ma8el/feat/internal/domain"
)

// Identifiers shared by the fixtures. They are neutral names rather than any
// real project's, because nothing project-specific may reach the binary.
const (
	// ProjectID is the fixture project.
	ProjectID = domain.ProjectID("example")
	// PrimaryRepositoryID is the fixture project's editable primary repository.
	PrimaryRepositoryID = domain.RepositoryID("core")
	// SecondaryRepositoryID is a second repository the fixture task binds
	// read-only, so that multi-repository behaviour is exercised everywhere the
	// fixtures are used.
	SecondaryRepositoryID = domain.RepositoryID("schema")
	// TaskID is the fixture task.
	TaskID = domain.TaskID("7f3a1c2e-5b6d-4a80-9c1f-2d3e4f5a6b7c")
	// DraftID is a fixture task that is still a draft, so that the states before
	// and after confirmation can both be exercised. A draft is a task in draft
	// state rather than an entity of its own (ADR-031).
	DraftID = domain.TaskID("2c4e6a80-1b3d-4f52-8a7c-9e0d1f2a3b4c")
)

// Base commits recorded for the fixture task. They are immutable for the
// lifetime of the task, which is what review and cleanup rely on.
const (
	// PrimaryBaseCommit is the base recorded for the primary repository.
	PrimaryBaseCommit = "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d"
	// SecondaryBaseCommit is the base recorded for the second repository.
	SecondaryBaseCommit = "9f8e7d6c5b4a39281706f5e4d3c2b1a09f8e7d6c"
	// PrimaryHeadCommit is the fixture task branch's current commit.
	PrimaryHeadCommit = "0011223344556677889900aabbccddeeff001122"
)

// Origin is the timestamp the fixtures are built from. Every other fixture
// timestamp is a fixed offset from it.
var Origin = time.Date(2026, time.August, 4, 9, 30, 0, 0, time.UTC)

// after returns a timestamp a fixed number of minutes after Origin.
func after(minutes int) time.Time { return Origin.Add(time.Duration(minutes) * time.Minute) }

// Project returns the fixture project.
func Project() *domain.Project {
	project, err := domain.NewProject(ProjectID, "Example", PrimaryRepositoryID, []domain.Repository{
		{
			ID:            PrimaryRepositoryID,
			Name:          "Core",
			HostPath:      "/srv/repositories/core",
			ContainerPath: "/src/core",
			DefaultBranch: "main",
			Remote:        "origin",
			DefaultAccess: domain.DefaultAccessReadWrite,
		},
		{
			ID:            SecondaryRepositoryID,
			Name:          "Schema",
			HostPath:      "/srv/repositories/schema",
			ContainerPath: "/src/schema",
			DefaultBranch: "main",
			Remote:        "origin",
			DefaultAccess: domain.DefaultAccessSelectable,
		},
	}, Origin)
	must(err)

	project.UpdatedAt = after(1)
	return project
}

// Task returns a fixture task that binds two repositories, owns an agent
// session and a runtime, and has reached review.
//
// It is built the way the daemon builds one: a draft is shaped, confirmed, and
// then only observed. Anything the domain would reject is therefore rejected
// here too.
func Task() *domain.Task {
	source := domain.TaskSource{Kind: domain.SourceMarkdown, Reference: "/srv/briefs/example.md"}
	task, err := domain.NewTask(TaskID, ProjectID, "Add a scheduled export", source, Origin)
	must(err)

	must(task.SetTitle("Add a scheduled export job", after(1)))
	must(task.SetBrief(Brief, after(1)))

	must(task.Bind(domain.TaskRepository{
		RepositoryID:  PrimaryRepositoryID,
		Access:        domain.TaskAccessReadWrite,
		Branch:        "feat/7f3a1c2e-add-a-scheduled-export-job",
		WorktreePath:  "/srv/state/worktrees/example/7f3a1c2e/core",
		ContainerPath: "/src/core",
	}, after(2)))
	must(task.Bind(domain.TaskRepository{
		RepositoryID:  SecondaryRepositoryID,
		Access:        domain.TaskAccessReadOnly,
		WorktreePath:  "/srv/state/worktrees/example/7f3a1c2e/schema",
		ContainerPath: "/src/schema",
	}, after(2)))

	must(task.ResolveBase(PrimaryRepositoryID, "origin/main", PrimaryBaseCommit, after(3)))
	must(task.ResolveBase(SecondaryRepositoryID, "origin/main", SecondaryBaseCommit, after(3)))

	// Confirmation. Everything above is frozen from here on.
	must(task.TransitionTo(domain.WorkflowPreparing, after(4)))
	must(task.AttachSession(Session(), after(5)))
	must(task.TransitionTo(domain.WorkflowWorking, after(5)))

	must(task.ObserveRepository(PrimaryRepositoryID, domain.GitObservation{
		Dirty:        true,
		Ahead:        3,
		Behind:       1,
		Merged:       false,
		ChangedFiles: 7,
		ObservedAt:   after(20),
	}, after(20)))
	must(task.ObserveRepository(SecondaryRepositoryID, domain.GitObservation{
		Ahead:        0,
		Behind:       2,
		Merged:       true,
		ChangedFiles: 0,
		ObservedAt:   after(20),
	}, after(20)))

	must(task.AttachRuntime(Runtime(), after(25)))
	must(task.TransitionTo(domain.WorkflowReviewRequested, after(30)))
	must(task.SetAttention(domain.AttentionPossiblyWaiting, after(30)))

	return task
}

// Draft returns a fixture task that has been resolved but not confirmed.
//
// It is what the preparation screen shows just before the user confirms: a
// brief, a repository selection, resolved immutable bases, and proposed
// branches and worktree paths — none of which exists on the host yet, because
// nothing is created before confirmation (FR-TASK-003).
func Draft() *domain.Task {
	task, err := domain.NewTask(DraftID, ProjectID, "Add a scheduled export job",
		domain.TaskSource{Kind: domain.SourcePrompt}, Origin)
	must(err)

	must(task.SetBrief(Brief, after(1)))
	must(task.Bind(domain.TaskRepository{
		RepositoryID:  PrimaryRepositoryID,
		Access:        domain.TaskAccessReadWrite,
		Branch:        "feat/2c4e6a80-add-a-scheduled-export-job",
		WorktreePath:  "/srv/state/worktrees/example/2c4e6a80/core",
		ContainerPath: "/src/core",
	}, after(2)))
	must(task.ResolveBase(PrimaryRepositoryID, "origin/main", PrimaryBaseCommit, after(3)))

	return task
}

// Brief is the fixture task brief, stored as Markdown beside the snapshot.
const Brief = `# Add a scheduled export job

Export the daily report to the configured bucket.

## Acceptance criteria

- the job runs once per day
- a failure is retried three times
`

// Session returns the fixture agent session.
func Session() *domain.AgentSession {
	session, err := domain.NewAgentSession(
		"claude",
		domain.ExecutionDevcontainer,
		domain.TmuxTarget{Socket: "/run/feat/tmux.sock", Session: "$2", Window: "@7", Pane: "%11"},
		"/srv/state/control/example/7f3a1c2e",
		after(5),
	)
	must(err)

	session.ProviderSessionID = "01J8Z5R2M9WQ6K3T4B7C8D9E0F"
	session.Execution = Execution()
	must(session.Observe(domain.ProcessIdle, after(30)))
	must(session.RecordEvent(12, after(30)))
	return session
}

// Execution returns the fixture agent execution environment.
//
// The session it belongs to is a devcontainer one, so the environment is
// present: a host session records none, which the domain enforces rather than
// leaves to convention.
func Execution() *domain.ExecutionEnvironment {
	environment := &domain.ExecutionEnvironment{
		Provider:              "compose",
		Identity:              "feat-agent-example-7f3a1c2e",
		Files:                 []string{"/srv/repositories/tooling/devcontainer.yaml"},
		GeneratedOverridePath: "/srv/state/projects/example/tasks/7f3a1c2e/execution/compose.override.yaml",
		Service:               "dev",
		User:                  "coder",
	}
	environment.Observe("9f8e7d6c5b4a", true, "Up 4 minutes", domain.HealthHealthy, after(30))
	return environment
}

// Runtime returns the fixture application runtime, including an external
// resource Feat references but never owns.
func Runtime() *domain.RuntimeEnvironment {
	runtime := &domain.RuntimeEnvironment{
		Provider:              "compose",
		Identity:              "feat-example-7f3a1c2e",
		ComposeFiles:          []string{"/srv/repositories/core/compose.yaml"},
		StaticOverrides:       []string{"/srv/repositories/core/compose.override.yaml"},
		GeneratedOverridePath: "/srv/state/runtime/example/7f3a1c2e/compose.generated.yaml",
		EnvFiles:              []string{"/srv/repositories/core/.env"},
		Services:              []string{"web", "worker"},
		Ports:                 []domain.PortAssignment{{Service: "web", ContainerPort: 8080, HostPort: 18080}},
		Networks:              []string{"feat-example-7f3a1c2e_default"},
		Volumes:               []string{"feat-example-7f3a1c2e_cache"},
		ExternalResources: []domain.ExternalResource{{
			ID:        "reporting-store",
			Kind:      "postgres",
			Lifecycle: domain.LifecycleExternal,
			Selector:  "example_7f3a1c2e",
		}},
	}
	must(runtime.Observe(domain.RuntimeRunning, domain.HealthHealthy, after(26)))
	return runtime
}

// Review returns the fixture review: a requested review whose two repositories
// were each compared against their own recorded base commit, with the check the
// gate ran.
//
// It holds no decision, because a review does not: the fixture task's workflow
// is where approval is recorded (ADR-047).
func Review() *domain.Review {
	review, err := domain.NewReview(TaskID, after(30))
	must(err)

	must(review.RecordRequest("Added the export job and its retry policy.", []domain.Check{{
		ID:           "unit",
		RepositoryID: PrimaryRepositoryID,
		Status:       domain.CheckPassed,
		Reporter:     domain.ReporterProvider,
		Detail:       "42 passed",
		RanAt:        after(29),
	}}, after(30)))

	must(review.SummarizeRepository(domain.RepositoryChange{
		RepositoryID: PrimaryRepositoryID,
		BaseCommit:   PrimaryBaseCommit,
		HeadCommit:   PrimaryHeadCommit,
		ChangedFiles: 7,
		Insertions:   214,
		Deletions:    36,
		Dirty:        true,
		SummarizedAt: after(31),
	}, after(31)))
	must(review.SummarizeRepository(domain.RepositoryChange{
		RepositoryID: SecondaryRepositoryID,
		BaseCommit:   SecondaryBaseCommit,
		ChangedFiles: 1,
		Insertions:   9,
		Deletions:    0,
		SummarizedAt: after(31),
	}, after(31)))

	return review
}

// Events returns a fixture history covering every event dimension. The
// sequences are left unset: the log assigns them.
func Events() []domain.Event {
	event := func(kind domain.EventType, minutes int) domain.Event {
		return domain.Event{
			ProjectID:  ProjectID,
			TaskID:     TaskID,
			Type:       kind,
			OccurredAt: after(minutes),
		}
	}

	created := event(domain.EventTaskCreated, 4)
	created.Detail = "confirmed from a Markdown brief"

	working := event(domain.EventWorkflowChanged, 5)
	working.From = string(domain.WorkflowPreparing)
	working.To = string(domain.WorkflowWorking)

	observed := event(domain.EventRepositoryObserved, 20)
	observed.RepositoryID = PrimaryRepositoryID
	observed.Detail = "7 changed files"

	idle := event(domain.EventProcessChanged, 30)
	idle.From = string(domain.ProcessRunning)
	idle.To = string(domain.ProcessIdle)

	attention := event(domain.EventAttentionChanged, 30)
	attention.From = string(domain.AttentionNone)
	attention.To = string(domain.AttentionPossiblyWaiting)

	runtime := event(domain.EventRuntimeChanged, 26)
	runtime.From = string(domain.RuntimeStarting)
	runtime.To = string(domain.RuntimeRunning)

	review := event(domain.EventReviewChanged, 32)
	review.Detail = "Feat ran the project's configured checks: 1 passed"

	reconciled := event(domain.EventReconciled, 40)
	reconciled.Detail = "session and worktrees rediscovered after a restart"

	return []domain.Event{created, working, observed, runtime, idle, attention, review, reconciled}
}

// must stops a fixture that the domain rejects. A fixture is written by hand,
// so a rejection is a mistake in the fixture rather than a condition a test
// should carry on with.
func must(err error) {
	if err != nil {
		panic(fmt.Sprintf("storetest: the domain rejected a fixture: %v", err))
	}
}
