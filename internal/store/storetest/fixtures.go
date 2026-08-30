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
	// FailedID is a fixture task whose launch failed. It is a fixture of its own
	// because the state excludes the one above: a task carries the reason it
	// failed only while it is failed, so no single fixture can cover both.
	FailedID = domain.TaskID("5d9b0e14-7c2a-42f6-8b31-0a4c6e8d1f35")
	// PublishedID is a fixture task that came from a ticket and has been
	// published. It is a fixture of its own for the same reason as the one
	// above: a brief comes from one source, so a task built from a Markdown
	// file can never also carry the ticket it was composed from.
	PublishedID = domain.TaskID("3e5a7c91-8d2b-4e60-b1f3-6c8a0d2e4f68")
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
	// SecondaryHeadCommit is the second repository's task branch commit, which
	// a publication describes separately from the first: one merge request per
	// repository means one commit per repository.
	SecondaryHeadCommit = "aabbccddeeff00112233445566778899aabbccdd"
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

// Failed returns a fixture task whose launch failed after its container
// existed.
//
// It is the state a user meets and can do least about: the task is `failed`, no
// session was ever recorded, and the only thing that explains it is the reason
// the transition carried. The reason here is a real one, verbatim from a launch
// refused by the mount rules, because a fixture with a tidy sentence in it would
// not show what a panel has to render.
//
// It is also the fixture that carries the plan-first mode, because this is the
// task the mode has to survive on: the value is recorded at confirmation and
// read when the session is built, and a launch that failed between the two is
// resumed by a retry that reads the record.
func Failed() *domain.Task {
	task, err := domain.NewTask(FailedID, ProjectID, "Add a rate limit",
		domain.TaskSource{Kind: domain.SourcePrompt}, Origin)
	must(err)

	must(task.SetBrief(Brief, after(1)))
	must(task.Bind(domain.TaskRepository{
		RepositoryID:  PrimaryRepositoryID,
		Access:        domain.TaskAccessReadWrite,
		Branch:        "feat/5d9b0e14-add-a-rate-limit",
		WorktreePath:  "/srv/state/worktrees/example/5d9b0e14/core",
		ContainerPath: "/src/core",
	}, after(2)))
	must(task.ResolveBase(PrimaryRepositoryID, "origin/main", PrimaryBaseCommit, after(3)))
	must(task.SetPlanFirst(true, after(3)))

	must(task.TransitionTo(domain.WorkflowPreparing, after(4)))
	must(task.FailWith("task 5d9b0e14 cannot run its agent in service dev of feat-agent-example-5d9b0e14: "+
		"the container mounts the home directory of the user the daemon runs as at /host-home, which reaches "+
		"the credentials the security model says Feat must not give an agent", after(5)))
	must(task.SetAttention(domain.AttentionNeedsInput, after(5)))

	return task
}

// Published returns a fixture task composed from a ticket and published to two
// forges, one of which refused it.
//
// It carries the two states the fixtures above cannot: a brief comes from one
// source, so a task imported from Markdown never also holds a ticket, and a
// task that has not been published holds no publication record.
//
// The publication is deliberately a partial one. A failure on one repository
// does not abort the others, and what is left behind is a recorded state rather
// than one to be undone, so the record a user meets after a publication that
// went half way is exactly this one (ADR-073).
func Published() *domain.Task {
	source := domain.TaskSource{Kind: domain.SourceTicket, Ticket: &domain.ExternalTaskReference{
		Provider:  "tracker",
		Reference: "PROJ-482",
		URL:       "https://tracker.example.com/stories/482",
		Snapshot: domain.TicketSnapshot{
			Title:   "Retry the nightly export",
			Body:    "The nightly export gives up on the first timeout.\n",
			State:   "in progress",
			TakenAt: Origin,
		},
		ChangeAvailable: true,
	}}
	task, err := domain.NewTask(PublishedID, ProjectID, "Retry the nightly export", source, Origin)
	must(err)

	must(task.SetBrief(Brief, after(1)))
	// Both repositories read-write, because publication is one merge request
	// per changed repository and a read-only binding has no branch to open one
	// from.
	must(task.Bind(domain.TaskRepository{
		RepositoryID:  PrimaryRepositoryID,
		Access:        domain.TaskAccessReadWrite,
		Branch:        "feat/3e5a7c91-retry-the-nightly-export",
		WorktreePath:  "/srv/state/worktrees/example/3e5a7c91/core",
		ContainerPath: "/src/core",
	}, after(2)))
	must(task.Bind(domain.TaskRepository{
		RepositoryID:  SecondaryRepositoryID,
		Access:        domain.TaskAccessReadWrite,
		Branch:        "feat/3e5a7c91-retry-the-nightly-export",
		WorktreePath:  "/srv/state/worktrees/example/3e5a7c91/schema",
		ContainerPath: "/src/schema",
	}, after(2)))
	must(task.ResolveBase(PrimaryRepositoryID, "origin/main", PrimaryBaseCommit, after(3)))
	must(task.ResolveBase(SecondaryRepositoryID, "origin/main", SecondaryBaseCommit, after(3)))

	must(task.TransitionTo(domain.WorkflowPreparing, after(4)))
	must(task.AttachSession(PublishedSession(), after(5)))
	must(task.TransitionTo(domain.WorkflowWorking, after(5)))
	must(task.TransitionTo(domain.WorkflowReviewRequested, after(30)))
	must(task.TransitionTo(domain.WorkflowReadyForReview, after(35)))

	// The plan is recorded before anything is attempted, and every result
	// before the next repository begins.
	must(task.PlanPublication([]domain.RepositoryPublication{
		{
			RepositoryID: PrimaryRepositoryID,
			Forge:        domain.ForgeGitLab,
			Remote:       "origin",
			BaseBranch:   "main",
			Commit:       PrimaryHeadCommit,
		},
		{
			RepositoryID: SecondaryRepositoryID,
			Forge:        domain.ForgeGitHub,
			Remote:       "origin",
			BaseBranch:   "main",
			Commit:       SecondaryHeadCommit,
		},
	}, after(36)))
	must(task.RecordPublished(PrimaryRepositoryID, domain.MergeRequest{
		Reference: "!128",
		URL:       "https://forge.example.com/example/core/-/merge_requests/128",
	}, after(37)))
	// The refusal is one repository's own — a protected branch or a missing
	// project is true of one forge and not the others — which is why a failure
	// does not abort the repositories after it (ADR-073 evidence 3). It is
	// verbatim from the forge, because a second account of one event helps
	// nobody.
	must(task.RecordPublicationFailure(SecondaryRepositoryID,
		"HTTP 403: Resource not accessible by personal access token "+
			"(https://api.github.com/repos/example/schema/pulls)",
		after(38)))

	return task
}

// PublishedSession returns the agent session of the published fixture task.
//
// It runs on the host, so it records no execution environment: the fixtures
// above cover the devcontainer case, and a host session that carried one would
// be a record of something the domain refuses.
func PublishedSession() *domain.AgentSession {
	session, err := domain.NewAgentSession(
		"claude",
		domain.ExecutionHost,
		domain.TmuxTarget{Socket: "/run/feat/tmux.sock", Session: "$3", Window: "@9", Pane: "%14"},
		"/srv/state/control/example/3e5a7c91",
		after(5),
	)
	must(err)

	must(session.Observe(domain.ProcessStopped, after(35)))
	must(session.RecordEvent(9, after(35)))
	return session
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
		Provider: "compose",
		Identity: "feat-example-7f3a1c2e",
		Composition: []domain.RuntimeSource{{
			Repository: "core",
			Directory:  "/srv/repositories/core",
			Files:      []string{"/srv/repositories/core/compose.yaml"},
		}},
		GeneratedIncludePath:  "/srv/state/runtime/example/7f3a1c2e/compose.include.yaml",
		StaticOverrides:       []string{"/srv/repositories/core/compose.override.yaml"},
		GeneratedOverridePath: "/srv/state/runtime/example/7f3a1c2e/compose.generated.yaml",
		EnvFiles:              []string{"/srv/repositories/core/.env"},
		Services:              []string{"web", "worker", "assets"},
		// One service of each kind there is: one the task's worktree is mounted
		// into, one whose image was built from it and shows a change only when it
		// is built again, and one the task reaches not at all — which is the
		// failure that looks like success, so it is the one a fixture must carry
		// through a round trip (ADR-065).
		Provenance: []domain.ServiceProvenance{
			{Service: "web", Repositories: []string{"core"}, Mounted: []string{"core"}},
			{Service: "worker", Repositories: []string{"core"}, Built: []string{"core"}},
			{Service: "assets", Repositories: []string{"core"}},
		},
		// The allocation is held while the runtime exists and released when it
		// becomes absent, so a round trip that lost it would give a second task a
		// port this one's containers are bound to.
		Allocations: []domain.PortAllocation{
			{Service: "web", ContainerPort: 8080, HostPort: 21000, Protocol: "tcp", HostIP: "127.0.0.1"},
		},
		// The observed binding carries the address the container runtime
		// reported, which is what lets it contradict the allocation above it.
		Ports: []domain.PortAssignment{
			{Service: "web", ContainerPort: 8080, HostPort: 21000, HostIP: "127.0.0.1"},
		},
		Networks: []string{"feat-example-7f3a1c2e_default"},
		Volumes:  []string{"feat-example-7f3a1c2e_cache"},
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
