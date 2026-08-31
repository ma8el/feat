package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/paths"
	"github.com/ma8el/feat/internal/reconcile"
	"github.com/ma8el/feat/internal/runtime"
	"github.com/ma8el/feat/internal/runtime/compose/runtimetest"
	"github.com/ma8el/feat/internal/store"
)

// runtimeFixture is the preparation fixture with application services added.
//
// The services mount the repositories at the same container paths the
// devcontainer uses, which is what a project whose Compose files define both is
// like — and what ADR-034's post-start inspection exists to check rather than
// assume.
var runtimeFixture = contributing(prepareFixture) + `
runtime:
  provider: compose
  start_policy: manual
  static_overrides:
    - ~/repos/app/compose.dev.yml
  env_files:
    - ~/repos/app/.env
  project_name_template: "feat-{project_id}-{task_key}"
`

// contributing adds the repositories' parts of the application to a fixture
// that has none: the Compose files api brings, where each repository's code is
// expected inside the services that run it, and the service Feat manages.
//
// Both repositories contribute, and only one brings a file. store's code runs
// in api's service — a library the application depends on — which is what a
// per-repository container path is for: two repositories' worktrees reach one
// service at two paths of their own.
//
// It is spliced rather than appended because a repository's contribution lives
// on the repository, which is the whole point of the shape: what an application
// is made of is answered where its code is (ADR-065).
func contributing(fixture string) string {
	fixture = strings.Replace(fixture, "  store:\n",
		"    runtime:\n"+
			"      compose_files:\n"+
			"        - ~/repos/app/compose.yml\n"+
			"      container_path: /srv/api\n"+
			"      services:\n"+
			"        - api\n"+
			"  store:\n", 1)
	return strings.Replace(fixture, "    default_access: read_only\n",
		"    runtime:\n"+
			"      container_path: /srv/store\n"+
			"      services:\n"+
			"        - api\n"+
			"    default_access: read_only\n", 1)
}

// runtimeDockerFunc lets a test swap the application runtime's fake Docker after
// the daemon was built.
type runtimeDockerFunc func() *runtimetest.Docker

func (f runtimeDockerFunc) Run(ctx context.Context, invocation runtime.Invocation) (runtime.Output, error) {
	return f().Run(ctx, invocation)
}

func (f runtimeDockerFunc) Look(name string) (string, error) { return f().Look(name) }

// runtimeDocker is a fake Docker that answers for the fixture's own services and
// Compose project name.
func runtimeDocker() *runtimetest.Docker {
	return runtimetest.New()
}

// answerFor arranges the fixture project's answers for one task's Compose
// project, whose name carries the task key.
func (d *drafting) answerFor(task *domain.Task, state, status string) {
	d.answerExitedFor(task, state, status, 0)
}

// answerExitedFor is answerFor for a container that ended with a particular exit
// status, which is what separates a failed runtime from a stopped one.
func (d *drafting) answerExitedFor(task *domain.Task, state, status string, exitCode int) {
	identity := "feat-app-" + task.Key().String()
	d.runtimes.
		Answer("ps --all --format json",
			runtimetest.ExitedContainer("api", "c0ffee", state, status, exitCode)).
		Answer("network ls --filter label=com.docker.compose.project="+identity+" --format {{.Name}}",
			identity+"_default").
		Answer("volume ls --filter label=com.docker.compose.project="+identity+" --format {{.Name}}",
			identity+"_pgdata")
}

// act performs one runtime action and fails the test if it is refused.
func (d *drafting) act(t *testing.T, id domain.TaskID, action api.RuntimeAction) api.RuntimeResult {
	t.Helper()

	result, err := d.service.Runtime(context.Background(), id, action)
	if err != nil {
		t.Fatalf("runtime %s: %v", action, err)
	}
	return result
}

// TestStartingOneTaskDoesNotAffectAnother is the first acceptance criterion.
//
// It is checked at the adapter rather than at the outcome: every Compose command
// carries a project name, and a command that carries one task's name cannot
// reach another task's services whatever the container runtime does with it.
func TestStartingOneTaskDoesNotAffectAnother(t *testing.T) {
	arranged := arrangeConfigured(t, runtimeFixture)

	first := arranged.launched(t, "Add a rate limit")
	second := arranged.launched(t, "Add an export job")
	arranged.answerFor(first, "running", "Up 2 seconds")
	arranged.answerFor(second, "running", "Up 2 seconds")

	arranged.act(t, first.ID, api.RuntimeStart)
	firstProject := "feat-app-" + first.Key().String()
	secondProject := "feat-app-" + second.Key().String()

	for _, vector := range arranged.runtimes.Vectors() {
		joined := strings.Join(vector, " ")
		if strings.Contains(joined, secondProject) {
			t.Fatalf("starting one task ran a command naming the other task's Compose project: %v", vector)
		}
		// Every command that acts on a Compose project names this task's. The two
		// that do not act on one — asking Docker its version, and inspecting a
		// container by identifier — are scoped by something else entirely.
		if slices.Contains(vector, "--project-name") && !slices.Contains(vector, firstProject) {
			t.Errorf("a project-scoped command does not name the started task's project: %v", vector)
		}
	}

	// And the other task's record is exactly as it was: nothing observed it,
	// because nothing asked about it.
	reloaded := arranged.reload(t, second.ID)
	if reloaded.Runtime != nil {
		t.Errorf("starting one task gave the other a runtime record: %+v", reloaded.Runtime)
	}

	// Stopping the second task must not reach into the first either.
	arranged.act(t, second.ID, api.RuntimeStop)
	stillRunning := arranged.reload(t, first.ID)
	if stillRunning.Runtime == nil || stillRunning.Runtime.State != domain.RuntimeRunning {
		t.Fatalf("stopping one task changed the other's recorded state: %+v", stillRunning.Runtime)
	}
}

// TestLogsOpenNormalComposeOutput is the second acceptance criterion at the
// daemon: what a client receives is the ordinary Compose logs command for that
// task's own project, not output Feat collected.
func TestLogsOpenNormalComposeOutput(t *testing.T) {
	arranged := arrangeConfigured(t, runtimeFixture)
	task := arranged.launched(t)
	arranged.answerFor(task, "running", "Up 2 seconds")

	command, err := arranged.service.RuntimeLogs(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("RuntimeLogs: %v", err)
	}

	if filepath.Base(command.Program) != "docker" {
		t.Errorf("the logs command runs %s, want docker", command.Program)
	}
	joined := strings.Join(command.Arguments, " ")
	for _, required := range []string{
		"compose", "--project-name feat-app-" + task.Key().String(), "logs --follow",
	} {
		if !strings.Contains(joined, required) {
			t.Errorf("the logs command does not contain %q: %v", required, command.Arguments)
		}
	}
	if arranged.runtimes.Ran("logs --follow") {
		t.Error("the daemon ran the logs command; the client runs it with its own terminal")
	}
}

// TestADestroyPlanReachesNothingOutsideTheTasksOwnProject is the third
// acceptance criterion.
//
// The criterion was written about external database resources, and what makes it
// hold is not a rule that excludes them: a destroy addresses the task's own
// Compose project and names nothing, so anything Feat does not own is beyond its
// reach by construction. A resource on a server Feat has never contacted is the
// extreme case of that, and ADR-048 removed the declaration that used to restate
// it. The volumes are checked here because they are the other half of what a
// destroy must leave alone, and the half that is inside the project.
func TestADestroyPlanReachesNothingOutsideTheTasksOwnProject(t *testing.T) {
	arranged := arrangeConfigured(t, runtimeFixture)
	task := arranged.launched(t)
	arranged.answerFor(task, "running", "Up 2 minutes")

	arranged.act(t, task.ID, api.RuntimeStart)
	result := arranged.act(t, task.ID, api.RuntimeDestroy)

	vector, found := arranged.runtimes.Vector("down")
	if !found {
		t.Fatalf("no destroy command was run: %v", arranged.runtimes.Calls())
	}
	for _, forbidden := range []string{"--volumes", "-v", "--remove-orphans"} {
		if slices.Contains(vector, forbidden) {
			t.Errorf("the destroy command carries %q: %v", forbidden, vector)
		}
	}
	if last := vector[len(vector)-1]; last != "down" {
		t.Errorf("the destroy command names %q after down, so it reaches past the task's own Compose "+
			"project: %v", last, vector)
	}

	recorded := result.Task.Runtime
	if !slices.Contains(recorded.Volumes, "feat-app-"+task.Key().String()+"_pgdata") {
		t.Errorf("destroy did not report the volume it retained: %v", recorded.Volumes)
	}
}

// TestTheRuntimeKeepsRunningThroughReview is the fourth acceptance criterion.
//
// A task reaching review is a statement about the work, not about the services
// the user is testing it with. Nothing in the workflow may stop them, and the
// check is that no Compose command ran at all rather than that the recorded
// state happens to be unchanged.
func TestTheRuntimeKeepsRunningThroughReview(t *testing.T) {
	arranged := arrangeConfigured(t, runtimeFixture)
	task := arranged.launched(t)
	arranged.answerFor(task, "running", "Up 2 minutes")

	arranged.act(t, task.ID, api.RuntimeStart)
	before := len(arranged.runtimes.Calls())

	// The task goes to work and then asks for review, which is the whole path a
	// task takes past the point where something might have tidied up after it.
	for _, next := range []domain.WorkflowState{domain.WorkflowWorking, domain.WorkflowReviewRequested} {
		if err := arranged.service.transition(context.Background(),
			arranged.reload(t, task.ID), next, "test"); err != nil {
			t.Fatalf("transition to %s: %v", next, err)
		}
	}

	if after := len(arranged.runtimes.Calls()); after != before {
		t.Fatalf("reaching review ran %d Compose commands: %v",
			after-before, arranged.runtimes.Calls()[before:])
	}
	reloaded := arranged.reload(t, task.ID)
	if reloaded.Workflow != domain.WorkflowReviewRequested {
		t.Fatalf("workflow = %q, want review_requested", reloaded.Workflow)
	}
	if reloaded.Runtime.State != domain.RuntimeRunning {
		t.Errorf("the runtime is %q after review was requested, want running", reloaded.Runtime.State)
	}
}

// TestReachingAReviewStateNeverStopsTheRuntime is the daemon half of the fifth
// acceptance criterion.
//
// A task's services are the user's to keep or to end, and no workflow transition
// touches them: the environment somebody is testing in outlives the state the
// work is in. This used to be phrased about approval, which was the furthest a
// task could get without cleanup; `ready_for_review` is that end of the
// lifecycle now (ADR-086).
func TestReachingAReviewStateNeverStopsTheRuntime(t *testing.T) {
	arranged := arrangeConfigured(t, runtimeFixture)
	task := arranged.launched(t)
	arranged.answerFor(task, "running", "Up 2 minutes")

	arranged.act(t, task.ID, api.RuntimeStart)
	before := len(arranged.runtimes.Calls())

	for _, next := range []domain.WorkflowState{
		domain.WorkflowWorking, domain.WorkflowReviewRequested, domain.WorkflowReadyForReview,
	} {
		if err := arranged.service.transition(context.Background(),
			arranged.reload(t, task.ID), next, "test"); err != nil {
			t.Fatalf("transition to %s: %v", next, err)
		}
	}

	if after := len(arranged.runtimes.Calls()); after != before {
		t.Fatalf("moving a task through the review states ran %d Compose commands: %v",
			after-before, arranged.runtimes.Calls()[before:])
	}
	if state := arranged.reload(t, task.ID).Runtime.State; state != domain.RuntimeRunning {
		t.Errorf("the runtime is %q once the task is ready for review, want running", state)
	}
}

// TestTheRuntimeIsRecordedBeforeItExists is ADR-029's ordering, applied to
// application services.
//
// The check is made from inside the operation that creates them: when Compose is
// asked to bring the services up, the snapshot on disk must already name the
// Compose project that is about to exist. Otherwise an interruption leaves
// resources no record can name.
func TestTheRuntimeIsRecordedBeforeItExists(t *testing.T) {
	arranged := arrangeConfigured(t, runtimeFixture)
	task := arranged.launched(t)
	arranged.answerFor(task, "running", "Up 2 seconds")

	var recorded *domain.RuntimeEnvironment
	arranged.runtimes.Before("up --detach api", func() {
		recorded = arranged.reload(t, task.ID).Runtime
	})

	arranged.act(t, task.ID, api.RuntimeStart)

	if recorded == nil {
		t.Fatal("the task's stored snapshot named no runtime when Compose was asked to start one")
	}
	if want := "feat-app-" + task.Key().String(); recorded.Identity != want {
		t.Errorf("the recorded identity is %q, want %q", recorded.Identity, want)
	}
	if recorded.GeneratedOverridePath == "" {
		t.Error("the record does not name the generated override, which a later action reads")
	}
}

// TestTheGeneratedOverrideIsNotWhereAnAgentCanReachIt keeps the document that
// decides what the application mounts out of everything the agent can write.
func TestTheGeneratedOverrideIsNotWhereAnAgentCanReachIt(t *testing.T) {
	arranged := arrangeConfigured(t, runtimeFixture)
	task := arranged.launched(t)
	arranged.answerFor(task, "running", "Up 2 seconds")
	arranged.act(t, task.ID, api.RuntimeStart)

	override := arranged.reload(t, task.ID).Runtime.GeneratedOverridePath
	if _, err := os.Stat(override); err != nil {
		t.Fatalf("the generated override was not written: %v", err)
	}

	root := arranged.layout.RuntimeRoot()
	if !strings.HasPrefix(override, root+string(filepath.Separator)) {
		t.Errorf("the generated override is at %s, which is outside the runtime root %s", override, root)
	}
	for _, reachable := range []string{arranged.layout.ControlRoot(), arranged.layout.ExecutionRoot()} {
		if strings.HasPrefix(override, reachable+string(filepath.Separator)) {
			t.Errorf("the generated override is under %s", reachable)
		}
	}
}

// TestTheApplicationIsComposedOfItsRepositories is ADR-065's rule at the daemon:
// a project's application runs from a generated include, with no hand-written
// combined Compose file anywhere.
//
// Each entry carries the repository's own checkout as its project directory, so
// that repository's build contexts and relative mounts resolve against it. A
// single file list would resolve every relative path against one directory, and
// the second repository's services would be built from the first (ADR-065
// evidence 2).
func TestTheApplicationIsComposedOfItsRepositories(t *testing.T) {
	arranged := arrangeConfigured(t, runtimeFixture)
	task := arranged.launched(t)
	arranged.answerFor(task, "running", "Up 2 seconds")
	arranged.act(t, task.ID, api.RuntimeStart)

	record := arranged.reload(t, task.ID).Runtime
	if len(record.Composition) != 1 {
		t.Fatalf("the runtime is composed of %d repositories, want the one that brings a file: %+v",
			len(record.Composition), record.Composition)
	}
	source := record.Composition[0]
	if source.Repository != "api" {
		t.Errorf("the contribution is recorded against %q, want api", source.Repository)
	}
	if want := filepath.Join(arranged.env.Home, "repos", "app", "api"); source.Directory != want {
		t.Errorf("the project directory of api's entry is %q, want its own checkout %q",
			source.Directory, want)
	}

	document, err := os.ReadFile(record.GeneratedIncludePath)
	if err != nil {
		t.Fatalf("reading the generated include: %v", err)
	}
	written := string(document)
	if !strings.Contains(written, "include:") {
		t.Errorf("the generated document is not an include:\n%s", written)
	}
	if !strings.Contains(written, `project_directory: "`+source.Directory+`"`) {
		t.Errorf("the include does not carry api's own directory:\n%s", written)
	}
	// And it is Feat's, under the runtime root rather than in a repository the
	// user maintains by hand.
	root := arranged.layout.RuntimeRoot()
	if !strings.HasPrefix(record.GeneratedIncludePath, root+string(filepath.Separator)) {
		t.Errorf("the generated include is at %s, outside the runtime root %s",
			record.GeneratedIncludePath, root)
	}
}

// TestAServiceHoldsTheCodeItRuns covers which worktrees reach which service.
//
// A repository's runtime container path is where its own services expect its
// source, so its worktree goes to those services and no others. Mounting every
// worktree into every service would make two repositories expecting their source
// at the same path a collision rather than the ordinary arrangement it is.
func TestAServiceHoldsTheCodeItRuns(t *testing.T) {
	// A second managed service, brought by the api repository and running only
	// its code: the store repository's worktree must not reach it.
	fixture := strings.Replace(runtimeFixture,
		"      container_path: /srv/api\n      services:\n        - api\n",
		"      container_path: /srv/api\n      services:\n        - api\n        - worker\n", 1)

	arranged := arrangeConfigured(t, fixture)
	task := arranged.launched(t)
	arranged.answerFor(task, "running", "Up 2 seconds")
	arranged.runtimes.
		Answer("config --services", "api\nworker").
		Answer("up --detach api worker", "")
	arranged.act(t, task.ID, api.RuntimeStart)

	document, err := os.ReadFile(arranged.reload(t, task.ID).Runtime.GeneratedOverridePath)
	if err != nil {
		t.Fatalf("reading the generated override: %v", err)
	}
	written := string(document)

	worker := written[strings.Index(written, `"worker":`):]
	if strings.Contains(worker, "/srv/store") {
		t.Errorf("the worker service holds the store worktree, which is not code it runs:\n%s", worker)
	}
	if !strings.Contains(worker, "/srv/api") {
		t.Errorf("the worker service does not hold the code it does run:\n%s", worker)
	}
}

// TestTheTasksOwnCodeIsWhatTheServicesRun covers the mounts the generated
// override carries.
func TestTheTasksOwnCodeIsWhatTheServicesRun(t *testing.T) {
	arranged := arrangeConfigured(t, runtimeFixture)
	task := arranged.launched(t)
	arranged.answerFor(task, "running", "Up 2 seconds")
	arranged.act(t, task.ID, api.RuntimeStart)

	document, err := os.ReadFile(arranged.reload(t, task.ID).Runtime.GeneratedOverridePath)
	if err != nil {
		t.Fatalf("reading the generated override: %v", err)
	}
	written := string(document)

	for _, binding := range arranged.reload(t, task.ID).Repositories {
		if !strings.Contains(written, binding.WorktreePath) {
			t.Errorf("the override does not mount the %s task worktree: %s", binding.RepositoryID, written)
		}
		if !strings.Contains(written, binding.ContainerPath) {
			t.Errorf("the override does not use the configured container path for %s: %s",
				binding.RepositoryID, written)
		}
	}
	// The read-only repository stays read-only in the application too: a task
	// that may not write the code may not write it through a service either.
	if !strings.Contains(written, "read_only: true") {
		t.Errorf("a read-only repository is mounted writable: %s", written)
	}
	// And the task key is generated, non-secret, and present. It is what an
	// application names its share of an external resource by, and the only thing
	// Feat contributes to one (ADR-048).
	if !strings.Contains(written, "FEAT_TASK_KEY") {
		t.Errorf("the override does not carry the task key: %s", written)
	}
}

// TestAHostExecutionProjectStillMountsItsWorktrees is the defect a real run
// found, as a test.
//
// A task binding records a container path only for devcontainer execution,
// because that field says where the *agent's* container mounts the worktree and
// a host-native agent has no container. An application runtime has containers
// whatever the agent does, so reading the mount from the binding gave a
// host-execution project a generated override with no volumes at all: its
// services ran the user's ordinary checkout, every record Feat kept was correct,
// and the only thing that said so was the note added for the other half of the
// same problem.
func TestAHostExecutionProjectStillMountsItsWorktrees(t *testing.T) {
	hostFixture := strings.Replace(runtimeFixture, "mode: devcontainer", "mode: host", 1)
	// Host execution sets these itself, and configuration refuses them. The
	// agent's own container paths go with them: there is no agent container to
	// mount anything in, which is exactly why reading the application's mounts
	// from that field left this project mounting nothing.
	for _, line := range []string{
		"    compose_files:\n      - ~/repos/app/compose.yml\n",
		"    service: dev\n", "    user: developer\n",
		"    working_directory: /srv/api\n", "    control_path: /feat\n",
		"    agent:\n      container_path: /srv/api\n",
		"    agent:\n      container_path: /srv/store\n",
	} {
		hostFixture = strings.Replace(hostFixture, line, "", 1)
	}

	arranged := arrangeConfigured(t, hostFixture)
	task := arranged.launched(t)
	arranged.answerFor(task, "running", "Up 2 seconds")
	arranged.act(t, task.ID, api.RuntimeStart)

	document, err := os.ReadFile(arranged.reload(t, task.ID).Runtime.GeneratedOverridePath)
	if err != nil {
		t.Fatalf("reading the generated override: %v", err)
	}
	written := string(document)

	if !strings.Contains(written, "volumes:") {
		t.Fatalf("a host-execution project's services mount nothing, so they run the ordinary "+
			"checkout:\n%s", written)
	}
	for _, binding := range arranged.reload(t, task.ID).Repositories {
		if binding.ContainerPath != "" {
			t.Errorf("a host-execution task recorded a container path for %s, which nothing mounts",
				binding.RepositoryID)
		}
		if !strings.Contains(written, binding.WorktreePath) {
			t.Errorf("the override does not mount the %s task worktree:\n%s", binding.RepositoryID, written)
		}
	}
	// At the paths the project configures for its containers, which is where the
	// application's own Compose files put that repository.
	for _, target := range []string{"/srv/api", "/srv/store"} {
		if !strings.Contains(written, target) {
			t.Errorf("the override does not use the configured container path %s:\n%s", target, written)
		}
	}
}

// TestServicesRunningTheOrdinaryCheckoutAreReported is the note ADR-034 chose
// over a refusal.
//
// The application runtime is inside the trusted host zone, so a checkout mounted
// into it is a correctness problem rather than a boundary breach: the user's
// change has no effect and nothing says why. Feat says why, and leaves the
// services running.
func TestServicesRunningTheOrdinaryCheckoutAreReported(t *testing.T) {
	arranged := arrangeConfigured(t, runtimeFixture)
	task := arranged.launched(t)
	arranged.answerFor(task, "running", "Up 2 seconds")
	arranged.runtimes.Answer("inspect --type container --format {{json .Mounts}} c0ffee",
		`[{"Type":"bind","Source":"`+filepath.Join(arranged.env.Home, "repos", "app", "api")+
			`","Destination":"/app","RW":true}]`)

	result := arranged.act(t, task.ID, api.RuntimeStart)

	if len(result.Notes) != 1 {
		t.Fatalf("%d notes were reported, want 1: %v", len(result.Notes), result.Notes)
	}
	if !strings.Contains(result.Notes[0], "container_path") {
		t.Errorf("the note does not say what to change: %s", result.Notes[0])
	}
	// Reported, not refused: the services are up and the record says so.
	if state := arranged.reload(t, task.ID).Runtime.State; state != domain.RuntimeRunning {
		t.Errorf("the runtime is %q; a note must not stop the services", state)
	}
}

// TestAProjectWithNoRuntimeIsRefusedByName keeps a missing section from
// producing a failure about something else.
func TestAProjectWithNoRuntimeIsRefusedByName(t *testing.T) {
	arranged := arrangeDrafting(t) // the fixture without a runtime section
	task := arranged.launched(t)

	_, err := arranged.service.Runtime(context.Background(), task.ID, api.RuntimeStart)
	if err == nil {
		t.Fatal("a project with no application runtime started one")
	}
	for _, expected := range []string{"configures no application runtime", "runtime section"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("the message does not explain what is missing: %v", err)
		}
	}
	if calls := arranged.runtimes.Calls(); len(calls) != 0 {
		t.Errorf("a project with no runtime ran %d Compose commands: %v", len(calls), calls)
	}
}

// TestADraftHasNothingToRun keeps an action off a task that has no worktrees.
func TestADraftHasNothingToRun(t *testing.T) {
	arranged := arrangeConfigured(t, runtimeFixture)
	draft := arranged.draft(t, "Add a rate limit")

	_, err := arranged.service.Runtime(context.Background(), draft.ID, api.RuntimeStart)
	if err == nil {
		t.Fatal("a draft started application services")
	}
	if !strings.Contains(err.Error(), "still a draft") {
		t.Errorf("the message does not say why: %v", err)
	}
}

// TestARefusedStartLeavesTheRecordAndTheServices covers a half-finished
// lifecycle.
//
// Nothing is undone: a service that started may already have written to a volume
// or a shared database, and tidying up after a failed start is a destructive act
// nobody asked for (ADR-029, ADR-033). What must survive is the record naming
// what may exist.
func TestARefusedStartLeavesTheRecordAndTheServices(t *testing.T) {
	arranged := arrangeConfigured(t, runtimeFixture)
	task := arranged.launched(t)
	arranged.answerFor(task, "running", "Up 2 seconds")
	arranged.runtimes.Fail("up --detach api",
		"Error response from daemon: Bind for 0.0.0.0:8080 failed: port is already allocated", 1)

	_, err := arranged.service.Runtime(context.Background(), task.ID, api.RuntimeStart)
	if err == nil {
		t.Fatal("a start that Docker refused was reported as a success")
	}
	if !strings.Contains(err.Error(), "8080") || !strings.Contains(err.Error(), "does not allocate ports") {
		t.Errorf("the message does not explain the collision in Feat's terms: %v", err)
	}

	recorded := arranged.reload(t, task.ID).Runtime
	if recorded == nil {
		t.Fatal("a refused start left the task with no record of the Compose project it may have created")
	}
	if want := "feat-app-" + task.Key().String(); recorded.Identity != want {
		t.Errorf("the recorded identity is %q, want %q", recorded.Identity, want)
	}
}

// TestRecordedInputsWinWhileTheServicesExist is what keeps an edited project
// file from pointing an action at a different Compose project.
func TestRecordedInputsWinWhileTheServicesExist(t *testing.T) {
	arranged := arrangeConfigured(t, runtimeFixture)
	task := arranged.launched(t)
	arranged.answerFor(task, "running", "Up 2 seconds")
	arranged.act(t, task.ID, api.RuntimeStart)

	// The user renames the Compose project in their configuration while the
	// services are running.
	renamed := strings.Replace(runtimeFixture,
		`project_name_template: "feat-{project_id}-{task_key}"`,
		`project_name_template: "other-{project_id}-{task_key}"`, 1)
	rewrite(t, arranged.layout, "app", renamed)

	arranged.act(t, task.ID, api.RuntimeStop)

	vector, found := arranged.runtimes.Vector("stop")
	if !found {
		t.Fatalf("no stop command was run: %v", arranged.runtimes.Calls())
	}
	if want := "feat-app-" + task.Key().String(); !slices.Contains(vector, want) {
		t.Errorf("the stop reached a different Compose project than the one that was started: %v", vector)
	}
}

// TestADestroyedRuntimePicksUpAFixedConfiguration is the other half of that
// rule.
//
// Once nothing exists, re-resolving can orphan nothing, and a user who fixed
// their configuration after destroying everything should get the fixed one.
func TestADestroyedRuntimePicksUpAFixedConfiguration(t *testing.T) {
	arranged := arrangeConfigured(t, runtimeFixture)
	task := arranged.launched(t)
	arranged.answerFor(task, "running", "Up 2 seconds")
	arranged.act(t, task.ID, api.RuntimeStart)

	// Destroy, with the fake reporting that nothing is left.
	arranged.runtimes.Answer("ps --all --format json", "")
	arranged.act(t, task.ID, api.RuntimeDestroy)
	if state := arranged.reload(t, task.ID).Runtime.State; state != domain.RuntimeAbsent {
		t.Fatalf("the runtime is %q after destroy, want absent", state)
	}

	renamed := strings.Replace(runtimeFixture,
		`project_name_template: "feat-{project_id}-{task_key}"`,
		`project_name_template: "other-{project_id}-{task_key}"`, 1)
	rewrite(t, arranged.layout, "app", renamed)

	arranged.act(t, task.ID, api.RuntimeStart)
	if want := "other-app-" + task.Key().String(); arranged.reload(t, task.ID).Runtime.Identity != want {
		t.Errorf("the runtime is still %q after everything it owned was destroyed and the configuration "+
			"changed, want %q", arranged.reload(t, task.ID).Runtime.Identity, want)
	}
}

// TestAnObservationPublishesOnlyWhatChanged keeps the poller from driving the
// dashboard.
//
// The dashboard re-reads state on every event, so a poll that published every
// time it looked would make every task with services a permanent source of
// reads. The dashboard has already paid for that shape once.
func TestAnObservationPublishesOnlyWhatChanged(t *testing.T) {
	arranged := arrangeConfigured(t, runtimeFixture)
	task := arranged.launched(t)
	arranged.answerFor(task, "running", "Up 2 seconds")
	arranged.act(t, task.ID, api.RuntimeStart)

	events := arranged.events(t, task.ID)

	// Three polls with nothing changing.
	for range 3 {
		arranged.service.pollRuntimes(context.Background())
	}
	if after := arranged.events(t, task.ID); after != events {
		t.Fatalf("three polls that observed no change recorded %d events", after-events)
	}

	// And one where something did.
	arranged.answerFor(task, "exited", "Exited (0) 1 second ago")
	arranged.service.pollRuntimes(context.Background())

	if after := arranged.events(t, task.ID); after != events+1 {
		t.Fatalf("a poll that observed a change recorded %d events, want 1", after-events)
	}
	if state := arranged.reload(t, task.ID).Runtime.State; state != domain.RuntimeStopped {
		t.Errorf("the observed state is %q, want stopped", state)
	}
}

// TestAStoppedRuntimeIsNeverRestarted is FR-STATE-004 at the daemon.
func TestAStoppedRuntimeIsNeverRestarted(t *testing.T) {
	arranged := arrangeConfigured(t, runtimeFixture)
	task := arranged.launched(t)
	arranged.answerFor(task, "running", "Up 2 seconds")
	arranged.act(t, task.ID, api.RuntimeStart)

	arranged.answerFor(task, "exited", "Exited (0) 1 second ago")
	before := len(arranged.runtimes.Calls())
	arranged.service.pollRuntimes(context.Background())

	for _, call := range arranged.runtimes.Calls()[before:] {
		if strings.HasPrefix(call, "up") || strings.HasPrefix(call, "start") || strings.HasPrefix(call, "create") {
			t.Errorf("observing a stopped runtime ran %q", call)
		}
	}
}

// TestAStartThatOutlastsItsBudgetSaysWhatHappened is the regression for the
// failure a user met as `context deadline exceeded` on a first start.
//
// The budget is the daemon's own (api.RuntimeTimeout), and what it produces has
// to be a diagnosis rather than a transport error: the action is named, the
// budget is named, and so is the command that says what is on the machine now.
// It is ErrInvalid for the same reason every other Compose failure is — that is
// what carries a message to the user instead of "the daemon could not complete
// the request".
func TestAStartThatOutlastsItsBudgetSaysWhatHappened(t *testing.T) {
	arranged := arrangeConfigured(t, runtimeFixture)
	task := arranged.launched(t)
	arranged.answerFor(task, "running", "Up 2 seconds")

	arranged.service.runtimeOverride = 100 * time.Millisecond
	// A Docker command that outlasts the whole action's budget. Which step is
	// holding the clock when it runs out is not asserted here: the budget is one
	// number for the whole action, and a loaded machine spends part of it on the
	// steps before Docker is reached, so a test that pinned the step would be
	// pinning how fast the machine is. It failed that way on Linux CI and under
	// `make check` while passing when run alone; what every step owes the user is
	// checked below, without a clock.
	arranged.runtimes.Before("config --services", func() { time.Sleep(200 * time.Millisecond) })

	_, err := arranged.service.Runtime(context.Background(), task.ID, api.RuntimeStart)

	if err == nil {
		t.Fatal("a start that outlasted its budget was reported as a success")
	}
	if !errors.Is(err, api.ErrInvalid) {
		t.Errorf("the failure is not carried to the user as an invalid request: %v", err)
	}
	for _, required := range []string{"runtime start", "100ms", "feat runtime status " + task.Key().String()} {
		if !strings.Contains(err.Error(), required) {
			t.Errorf("the failure does not mention %q: %v", required, err)
		}
	}
	if arranged.runtimes.Ran("up --detach api") {
		t.Error("the start ran after its budget had already gone")
	}
	// Nothing was claimed about services nobody managed to look at. The record
	// still names the Compose project, which is what makes what Docker did create
	// findable (ADR-029, ADR-033).
	reloaded := arranged.reload(t, task.ID)
	if state := reloaded.Runtime.State; state != domain.RuntimeAbsent {
		t.Errorf("the runtime is recorded as %q after a start that never finished, want absent", state)
	}
	if reloaded.Runtime.Identity != "feat-app-"+task.Key().String() {
		t.Errorf("the record does not name the Compose project the start may have created: %+v",
			reloaded.Runtime)
	}
}

// TestEveryStepOfARuntimeActionReportsTheBudget is the rule the test above
// stopped depending on the machine for.
//
// A start whose clock goes while the daemon is still asking Docker its version
// failed for the same reason as one whose clock goes inside `up`, and the user
// is owed the same three things either way: the action, the budget, and the
// command that says what is on the machine now. Before this, only the second
// reported them — the first arrived as `cannot manage its application services:
// context deadline exceeded`, which is the transport error the budget exists to
// replace, and which of the two a user met depended on how loaded their machine
// was.
//
// What differs is the one thing that is actually different: whether there is a
// half-finished Compose command to warn about.
func TestEveryStepOfARuntimeActionReportsTheBudget(t *testing.T) {
	arranged := arrangeConfigured(t, runtimeFixture)
	task := arranged.launched(t)

	// Already past, so the reading is exact rather than raced.
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	for name, testCase := range map[string]struct {
		started bool
		expects string
		absent  string
	}{
		"before the action started": {
			started: beforeTheAction,
			expects: "the action had not started",
			absent:  "Docker was stopped part way through",
		},
		"while the action was running": {
			started: duringTheAction,
			expects: "Docker was stopped part way through",
			absent:  "the action had not started",
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := arranged.service.explainRuntime(ctx, task.ID, api.RuntimeStart,
				90*time.Second, testCase.started, context.DeadlineExceeded)

			if !errors.Is(err, api.ErrInvalid) {
				t.Errorf("the failure is not carried to the user as an invalid request: %v", err)
			}
			for _, required := range []string{
				"runtime start", "1m30s", "feat runtime status " + task.Key().String(), testCase.expects,
			} {
				if !strings.Contains(err.Error(), required) {
					t.Errorf("the failure does not mention %q: %v", required, err)
				}
			}
			if strings.Contains(err.Error(), testCase.absent) {
				t.Errorf("the failure claims %q, which is not true of this one: %v", testCase.absent, err)
			}
		})
	}
}

// TestAValidationFailureKeepsItsOwnSentence is the other half of routing every
// step through one explanation: a project whose Docker Compose is too old fails
// for its own reason, and the budget must not take the message over.
func TestAValidationFailureKeepsItsOwnSentence(t *testing.T) {
	arranged := arrangeConfigured(t, runtimeFixture)
	task := arranged.launched(t)
	arranged.runtimes.Answer("version --short", "2.20.0")

	_, err := arranged.service.Runtime(context.Background(), task.ID, api.RuntimeStart)

	if err == nil {
		t.Fatal("a Compose too old to run the project was accepted")
	}
	if !strings.Contains(err.Error(), "cannot manage its application services") {
		t.Errorf("the validation failure lost its own sentence: %v", err)
	}
	if strings.Contains(err.Error(), "did not finish within") {
		t.Errorf("a validation failure was reported as a budget that ran out: %v", err)
	}
}

// TestACallerThatGoesAwayMidStartIsSaidToHave separates the two ways a Compose
// command is cut short.
//
// A caller that disconnected and a budget that ran out both leave a half-created
// Compose project, and only one of them is Feat's own patience. The daemon logs
// this one as well as reporting it, because the connection that would have
// carried the report is what has gone.
func TestACallerThatGoesAwayMidStartIsSaidToHave(t *testing.T) {
	arranged := arrangeConfigured(t, runtimeFixture)
	task := arranged.launched(t)
	arranged.answerFor(task, "running", "Up 2 seconds")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	arranged.runtimes.Before("config --services", cancel)

	_, err := arranged.service.Runtime(ctx, task.ID, api.RuntimeStart)

	if err == nil {
		t.Fatal("a start whose caller went away was reported as a success")
	}
	if !strings.Contains(err.Error(), "cancelled before it finished") {
		t.Errorf("a cancelled start is not reported as one: %v", err)
	}
	if strings.Contains(err.Error(), "did not finish within") {
		t.Errorf("a cancelled start is reported as Feat running out of patience: %v", err)
	}
}

// unreachedFixture gives the store repository a service of its own and no
// runtime container path.
//
// It is a managed service nothing of the task reaches: no worktree is mounted
// into it, and the fixture's Compose file builds nothing from that repository.
// The project still loads, because the api repository says where its own source
// goes — which is the case a configuration rule cannot catch and this state can.
func unreachedFixture() string {
	return strings.Replace(runtimeFixture,
		"    runtime:\n      container_path: /srv/store\n      services:\n        - api\n",
		"    runtime:\n      services:\n        - worker\n", 1)
}

// TestAServiceThatRunsNoTaskCodeSaysSoBeforeAnythingStarts is ADR-065's rule
// about a service that runs none of the task's work.
//
// A service that mounts no task worktree and builds from no task worktree runs
// whatever the project's own files give it, which is the user's ordinary
// checkout. Everything about that is otherwise silent: the containers start, the
// application serves, and every record Feat keeps stays correct (ADR-065
// evidence 7). It is resolved from configuration when the runtime is, so a
// create says it before a single container exists.
//
// The record is read at the first Compose command of the action, which makes the
// rule observable: the answer is already on the task before Compose has been
// asked anything, so no path to it can be
// `docker compose config` — the command that would render the values of the
// project's environment files (ADR-034 evidence 5).
func TestAServiceThatRunsNoTaskCodeSaysSoBeforeAnythingStarts(t *testing.T) {
	arranged := arrangeConfigured(t, unreachedFixture())
	task := arranged.launched(t)
	arranged.answerFor(task, "created", "Created")
	arranged.runtimes.
		Answer("config --services", "api\nworker").
		Answer("up --no-start --build api worker", "")

	var atCreate *domain.RuntimeEnvironment
	arranged.runtimes.Before("config --services", func() {
		atCreate = arranged.reload(t, task.ID).Runtime
	})
	result := arranged.act(t, task.ID, api.RuntimeCreate)

	if atCreate == nil {
		t.Fatal("the task recorded no runtime when Compose was asked to create one")
	}
	api, known := atCreate.ServiceProvenance("api")
	if !known || !api.RunsTaskCode() {
		t.Errorf("the api service is recorded as running no task code: %+v", api)
	}
	worker, known := atCreate.ServiceProvenance("worker")
	if !known {
		t.Fatalf("the worker service has no provenance at all: %+v", atCreate.Provenance)
	}
	if worker.RunsTaskCode() {
		t.Errorf("a service with no mount and no build context is recorded as running the task's "+
			"code: %+v", worker)
	}
	if !slices.Contains(worker.Repositories, "store") {
		t.Errorf("the worker service does not name the repository whose code it is meant to run: %+v", worker)
	}

	// And it is said in words, on the action a user asked for, rather than left
	// in a record they would have to go looking through.
	if !notesMention(result.Notes, "worker") {
		t.Errorf("the create said nothing about the service that will not run the task's work: %v",
			result.Notes)
	}
}

// TestABuiltServiceIsPointedAtTheTaskWorktree is the trap per-repository
// composition exists for.
//
// A service whose image copies its source in has no mount to replace, so the
// container path decides nothing about it and ADR-034's post-start inspection
// cannot report it: the note looks at mounts and there is no mount. Only the
// build context decides what such a service runs (ADR-065 evidence 4).
func TestABuiltServiceIsPointedAtTheTaskWorktree(t *testing.T) {
	arranged := arrangeConfigured(t, runtimeFixture)
	// The project's own Compose file, in the shape the reference project writes
	// it: a build context of "." beside an interpolated build argument Feat never
	// reads, and no mount anywhere.
	writeCompose(t, filepath.Join(arranged.env.Home, "repos", "app", "compose.yml"), `services:
  api:
    build:
      context: .
      dockerfile: Dockerfile
      args:
        API_BASE_URL: ${API_BASE_URL:-http://localhost:8000}
`)

	task := arranged.launched(t)
	arranged.answerFor(task, "running", "Up 2 seconds")
	arranged.act(t, task.ID, api.RuntimeStart)

	record := arranged.reload(t, task.ID).Runtime
	worktree := worktreeOf(t, arranged.reload(t, task.ID), "api")

	document, err := os.ReadFile(record.GeneratedOverridePath)
	if err != nil {
		t.Fatalf("reading the generated override: %v", err)
	}
	if written := string(document); !strings.Contains(written, `context: "`+worktree+`"`) {
		t.Errorf("the api service is still built from the ordinary checkout:\n%s", written)
	}

	provenance, known := record.ServiceProvenance("api")
	if !known || !slices.Contains(provenance.Built, "api") {
		t.Errorf("the task does not record that the api service builds its image from the api "+
			"worktree: %+v", provenance)
	}
}

// TestABakedServiceSaysItNeedsBuildingAgain is what a service that runs the
// task's code and will not show a change looks like.
//
// The agent that changed the code is confined to a devcontainer with no Docker
// and can rebuild nothing (ADR-065 evidence 9), so a change that appears only
// after a rebuild is a change the user has to be told about — and told what
// makes it appear.
func TestABakedServiceSaysItNeedsBuildingAgain(t *testing.T) {
	// The store repository brings a service of its own, built from its own
	// checkout and mounting nothing: the shape of a frontend whose image is a
	// multi-stage build ending in a web server. The api repository still says
	// where its own source goes, so the project is one configuration cannot fault.
	fixture := strings.Replace(runtimeFixture,
		"    runtime:\n      container_path: /srv/store\n      services:\n        - api\n",
		"    runtime:\n      compose_files:\n        - ~/repos/app/store/docker-compose.yml\n"+
			"      services:\n        - web\n", 1)

	arranged := arrangeConfigured(t, fixture)
	writeCompose(t, filepath.Join(arranged.env.Home, "repos", "app", "store", "docker-compose.yml"),
		`services:
  web:
    build: .
`)

	task := arranged.launched(t)
	arranged.answerFor(task, "running", "Up 2 seconds")
	arranged.runtimes.
		Answer("config --services", "api\nweb").
		Answer("up --detach api web", "")
	result := arranged.act(t, task.ID, api.RuntimeStart)

	provenance, known := arranged.reload(t, task.ID).Runtime.ServiceProvenance("web")
	if !known || !slices.Equal(provenance.Baked(), []string{"store"}) {
		t.Fatalf("a service that only bakes the repository's code is not recorded as baking it: %+v",
			provenance)
	}
	if !notesMention(result.Notes, "built again") {
		t.Errorf("the start did not say that the service shows a change only once its image is "+
			"built again: %v", result.Notes)
	}
}

// TestAServiceThatMountsAndBakesStillSaysItNeedsBuilding is the shape the
// reference project turned out to have, and the rule it corrected.
//
// A service that mounts a repository and builds from it was reported as current,
// on the reasoning that the mount is what it reads. That holds for an
// application server reloading from its mounted source and not for a web server
// serving what its build produced — the reference project runs one of each, and
// the second one served the task's work only because it had been rebuilt, while
// Feat said nothing about needing to (ADR-065 evidence 15).
func TestAServiceThatMountsAndBakesStillSaysItNeedsBuilding(t *testing.T) {
	arranged := arrangeConfigured(t, runtimeFixture)
	writeCompose(t, filepath.Join(arranged.env.Home, "repos", "app", "compose.yml"), `services:
  api:
    build: .
`)

	task := arranged.launched(t)
	arranged.answerFor(task, "running", "Up 2 seconds")
	result := arranged.act(t, task.ID, api.RuntimeStart)

	provenance, known := arranged.reload(t, task.ID).Runtime.ServiceProvenance("api")
	if !known || len(provenance.Mounted) == 0 || len(provenance.Built) == 0 {
		t.Fatalf("the fixture no longer mounts and builds one service: %+v", provenance)
	}
	if !notesMention(result.Notes, "built again") {
		t.Errorf("a service that bakes the code it also mounts said nothing about needing a "+
			"rebuild: %v", result.Notes)
	}
	if !notesMention(result.Notes, "wherever the image reads it") {
		t.Errorf("the note does not say what the mount is still worth: %v", result.Notes)
	}
}

// writeCompose writes one of a project's own Compose files.
func writeCompose(t *testing.T, path, body string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("creating %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// worktreeOf returns the task worktree of one repository.
func worktreeOf(t *testing.T, task *domain.Task, repository string) string {
	t.Helper()

	binding, ok := task.Repository(domain.RepositoryID(repository))
	if !ok {
		t.Fatalf("the task does not bind repository %s", repository)
	}
	return binding.WorktreePath
}

// notesMention reports whether any note carries the given words.
func notesMention(notes []string, words string) bool {
	for _, note := range notes {
		if strings.Contains(note, words) {
			return true
		}
	}
	return false
}

// events counts the events recorded for a task.
func (d *drafting) events(t *testing.T, id domain.TaskID) int {
	t.Helper()

	log, err := d.service.store.Events().Replay(context.Background(),
		store.TaskRef{Project: "app", Task: id})
	if err != nil {
		t.Fatalf("replaying the event log: %v", err)
	}
	return len(log.Events)
}

// rewrite replaces a registered project's configuration file.
func rewrite(t *testing.T, layout paths.Layout, id, body string) {
	t.Helper()

	if err := os.WriteFile(
		filepath.Join(layout.ProjectConfigDir(), id+".yaml"), []byte(body), 0o600); err != nil {
		t.Fatalf("rewriting the configuration: %v", err)
	}
}

// TestCleanupRemovesTheGeneratedRuntimeInput is ADR-037 evidence 16 at the second of the two
// roots it names.
//
// The application's generated Compose document has the same lifetime as the
// agent's: it is written per task under `<state>/runtime/<project-id>/<task-id>/`,
// it is what the Compose project is defined by, and nothing removed it. This
// directory is also the working directory of every Compose command Feat runs for
// the task, which is why it goes after the destroy rather than before it.
func TestCleanupRemovesTheGeneratedRuntimeInput(t *testing.T) {
	arranged := arrangeConfigured(t, runtimeFixture)
	task := arranged.launched(t)
	arranged.answerFor(task, "running", "Up 2 seconds")
	arranged.act(t, task.ID, api.RuntimeStart)

	directory, err := arranged.service.runtimeDirectory(task)
	if err != nil {
		t.Fatalf("resolving the runtime directory: %v", err)
	}
	if _, err := os.Stat(directory); err != nil {
		t.Fatalf("the start generated no runtime input, so there is nothing to remove: %v", err)
	}

	// The services are gone by the time the cleanup runs, which is the state a
	// destroy leaves and the state the plan is resolved against.
	arranged.runtimes.Answer("ps --all --format json", "")
	plan := arranged.planOf(t, arranged.reload(t, task.ID))
	result, err := arranged.service.Cleanup(context.Background(), task.ID,
		selectAll(plan, reconcile.ClassRuntimeContainers))
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Errorf("the generated runtime input %s is still there: %v", directory, err)
	}
	if _, err := os.Stat(filepath.Dir(directory)); err != nil {
		t.Errorf("the project's own runtime directory was removed with its last task: %v", err)
	}

	var reported bool
	for _, entry := range result.Removed {
		if entry.Identity == directory && entry.Removed {
			reported = true
		}
	}
	if !reported {
		t.Errorf("the cleanup removed %s without reporting it: %+v", directory, result.Removed)
	}
}
