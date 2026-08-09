package compose_test

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/runtime"
	"github.com/ma8el/feat/internal/runtime/compose"
	"github.com/ma8el/feat/internal/runtime/compose/runtimetest"
)

var update = flag.Bool("update", false, "rewrite golden files")

const (
	task    = domain.TaskID("11111111-1111-4111-8111-111111111111")
	project = domain.ProjectID("app")
	// identity is what runtimetest.New arranges answers for.
	identity = "feat-app-11111111"
)

// arrange returns a runtime over a fake Docker, with the override written into a
// temporary directory.
func arrange(t *testing.T, docker *runtimetest.Docker) (*compose.Runtime, runtime.Spec) {
	t.Helper()

	state := t.TempDir()
	spec := runtime.Spec{
		Project:         project,
		Task:            task,
		Identity:        identity,
		Files:           []string{"/repos/app/api/docker-compose.yml"},
		StaticOverrides: []string{"/repos/app/api/docker-compose.dev.yml"},
		Directory:       "/repos/app/api",
		OverridePath:    filepath.Join(state, "runtime", "compose.override.yaml"),
		EnvFiles:        []string{"/repos/app/api/.env"},
		Services:        []string{"api"},
		Mounts: []runtime.Mount{
			{Source: "/worktrees/api", Target: "/srv/api", Description: "the api task worktree, read-write"},
			{Source: "/worktrees/store", Target: "/srv/store", ReadOnly: true,
				Description: "the store task worktree, read-only"},
		},
		Variables: map[string]string{
			"FEAT_TASK_KEY":        "11111111",
			"FEAT_RUNTIME_PROJECT": identity,
		},
		ForbiddenSources: []string{"/repos/app/api", "/repos/app/store"},
	}

	services, err := compose.New(spec, compose.Options{Runner: docker})
	if err != nil {
		t.Fatalf("building the runtime: %v", err)
	}
	return services, spec
}

// TestTheGeneratedOverrideIsPinned holds the document that decides what the
// task's services run to a golden file.
//
// It is pinned rather than described because every line of it is a decision:
// which code the services see, which paths are writable, what is reset, what is
// deliberately not reset, and what is labelled.
func TestTheGeneratedOverrideIsPinned(t *testing.T) {
	docker := runtimetest.New()
	services, spec := arrange(t, docker)

	if _, err := services.Start(context.Background()); err != nil {
		t.Fatalf("starting: %v", err)
	}

	written, err := os.ReadFile(spec.OverridePath)
	if err != nil {
		t.Fatalf("reading the generated override: %v", err)
	}

	golden := filepath.Join("testdata", "override.golden")
	if *update {
		if err := os.WriteFile(golden, written, 0o600); err != nil {
			t.Fatalf("updating the golden file: %v", err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("reading the golden file: %v", err)
	}
	if string(written) != string(want) {
		t.Errorf("the generated override changed\n got:\n%s\nwant:\n%s", written, want)
	}
}

// dependent arranges a project whose managed service needs two others, which is
// the ordinary shape of an application: a database, a migration that must finish
// first, and the service the project actually names.
func dependent(t *testing.T) (*compose.Runtime, runtime.Spec, *runtimetest.Docker) {
	t.Helper()

	docker := runtimetest.New().
		Answer("config --services", "postgres\nmigrate\napi\n").
		Answer("ps --all --format json",
			runtimetest.Container("api", "c1", "running", "Up 2 seconds")+"\n"+
				runtimetest.Container("postgres", "c2", "running", "Up 12 seconds")+"\n"+
				runtimetest.Container("migrate", "c3", "exited", "Exited (0) 10 seconds ago")).
		Answer("inspect --type container --format {{json .Mounts}} c2", `[]`).
		Answer("inspect --type container --format {{json .Mounts}} c3", `[]`)

	services, spec := arrange(t, docker)
	return services, spec, docker
}

// TestTheGeneratedOverrideCoversEveryServiceInTheProject pins what Feat writes
// for a service it was not asked to manage.
//
// Compose starts whatever a managed service depends on, and everything it starts
// lands in this task's project. A base file's fixed container_name is global to
// the Docker daemon, so leaving one in place on a dependency puts the whole
// project back to one task per machine — the thing the generated override exists
// to prevent. What such a service gets is exactly that reset and the ownership
// labels: the project did not ask Feat to manage it, so Feat redirects nothing
// about it.
func TestTheGeneratedOverrideCoversEveryServiceInTheProject(t *testing.T) {
	services, spec, _ := dependent(t)

	if _, err := services.Start(context.Background()); err != nil {
		t.Fatalf("starting: %v", err)
	}
	written, err := os.ReadFile(spec.OverridePath)
	if err != nil {
		t.Fatalf("reading the generated override: %v", err)
	}

	golden := filepath.Join("testdata", "override-dependencies.golden")
	if *update {
		if err := os.WriteFile(golden, written, 0o600); err != nil {
			t.Fatalf("updating the golden file: %v", err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("reading the golden file: %v", err)
	}
	if string(written) != string(want) {
		t.Errorf("the generated override changed\n got:\n%s\nwant:\n%s", written, want)
	}

	// The properties the golden happens to show, said as themselves, so that a
	// deliberate update to the document cannot quietly drop one.
	document := string(written)
	if count := strings.Count(document, "container_name: !reset null"); count != 3 {
		t.Errorf("%d services have their container_name reset, want 3: a dependency keeping a fixed "+
			"name is a second task that cannot start\n%s", count, document)
	}
	if strings.Contains(document, `"postgres":`+"\n    volumes:") {
		t.Errorf("the override mounts a task worktree into a service the project does not manage:\n%s", document)
	}
}

// TestTheOverrideResetsTheNameAndKeepsThePorts is the decision ADR-034 records,
// as a test.
//
// A container name is global to the Docker daemon, so a base file carrying one
// could be started for a single task. A published port is how the user reaches
// the application they are testing, and v0 allocates none of its own, so it is
// left exactly as configured and a collision is explained instead.
func TestTheOverrideResetsTheNameAndKeepsThePorts(t *testing.T) {
	docker := runtimetest.New()
	services, spec := arrange(t, docker)

	if _, err := services.Start(context.Background()); err != nil {
		t.Fatalf("starting: %v", err)
	}
	written, err := os.ReadFile(spec.OverridePath)
	if err != nil {
		t.Fatalf("reading the generated override: %v", err)
	}

	if !strings.Contains(string(written), "container_name: !reset null") {
		t.Errorf("the generated override does not reset container_name, so two tasks could not "+
			"run these services at once:\n%s", written)
	}
	if strings.Contains(string(written), "ports:") {
		t.Errorf("the generated override touches published ports, and they are the user's:\n%s", written)
	}
}

// TestTheOverrideCarriesNoSecret checks the property the security model states
// about generated documents: nothing Feat writes carries a value out of an
// environment file, because nothing that reads one reaches the generator.
func TestTheOverrideCarriesNoSecret(t *testing.T) {
	docker := runtimetest.New()
	services, spec := arrange(t, docker)

	if _, err := services.Start(context.Background()); err != nil {
		t.Fatalf("starting: %v", err)
	}
	written, err := os.ReadFile(spec.OverridePath)
	if err != nil {
		t.Fatalf("reading the generated override: %v", err)
	}
	// The environment file is named in the invocation and never opened, so its
	// path may appear in a command but no value from it can appear here.
	if strings.Contains(string(written), "env_file") {
		t.Errorf("the generated override names an environment file:\n%s", written)
	}
	if info, err := os.Stat(spec.OverridePath); err != nil {
		t.Fatalf("stat: %v", err)
	} else if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("the generated override is mode %o, want 600", mode)
	}
}

// TestEveryCommandCarriesTheTasksOwnProject is the first acceptance criterion at
// the adapter: an action can only reach one task's services because every
// invocation names that task's Compose project.
func TestEveryCommandCarriesTheTasksOwnProject(t *testing.T) {
	docker := runtimetest.New()
	services, spec := arrange(t, docker)
	ctx := context.Background()

	if _, err := services.Create(ctx); err != nil {
		t.Fatalf("creating: %v", err)
	}
	if _, err := services.Start(ctx); err != nil {
		t.Fatalf("starting: %v", err)
	}
	if _, err := services.Stop(ctx); err != nil {
		t.Fatalf("stopping: %v", err)
	}
	if _, err := services.Destroy(ctx); err != nil {
		t.Fatalf("destroying: %v", err)
	}

	for _, vector := range docker.Vectors() {
		// Either as its own argument, for a Compose command, or inside the label
		// filter that asks Docker for this project's networks and volumes.
		if !mentions(vector, spec.Identity) {
			t.Fatalf("a command does not name the task's own Compose project %s: %v", spec.Identity, vector)
		}
	}
}

// mentions reports whether an argument vector names the given Compose project.
func mentions(vector []string, identity string) bool {
	for _, argument := range vector {
		if argument == identity || strings.HasSuffix(argument, "="+identity) {
			return true
		}
	}
	return false
}

// TestTheComposeInvocationIsPinned holds the flags every action carries.
//
// The file order is the merge order and the project directory is what keeps the
// project's own relative paths resolving, so neither is an implementation
// detail: getting either wrong produces a working command that acts on the wrong
// thing.
func TestTheComposeInvocationIsPinned(t *testing.T) {
	docker := runtimetest.New()
	services, spec := arrange(t, docker)

	if _, err := services.Start(context.Background()); err != nil {
		t.Fatalf("starting: %v", err)
	}
	vector, found := docker.Vector("up --detach api")
	if !found {
		t.Fatalf("no start command was run; calls were %v", docker.Calls())
	}

	want := []string{
		"compose",
		"--project-name", spec.Identity,
		"--project-directory", "/repos/app/api",
		"--file", "/repos/app/api/docker-compose.yml",
		"--file", "/repos/app/api/docker-compose.dev.yml",
		"--file", spec.OverridePath,
		"--env-file", "/repos/app/api/.env",
		"up", "--detach", "api",
	}
	if !slices.Equal(vector, want) {
		t.Errorf("the start invocation changed\n got: %v\nwant: %v", vector, want)
	}
}

// TestCreateBuildsWhatItIsAboutToCreate holds the one difference between create
// and start.
//
// `docker compose create api` is the command this action is named after and not
// the command it means: Compose builds the image of the service it was given and
// then creates a container for the service that one depends on, whose image it
// never built. On a fresh task, where no image exists yet, the first create a
// user asks for fails with "No such image" while a start of the same services
// succeeds — so the action is `up --no-start`, which builds the whole closure and
// starts none of it (ADR-034 evidence 13).
func TestCreateBuildsWhatItIsAboutToCreate(t *testing.T) {
	docker := runtimetest.New()
	services, spec := arrange(t, docker)

	if _, err := services.Create(context.Background()); err != nil {
		t.Fatalf("creating: %v", err)
	}

	vector, found := docker.Vector("up --no-start api")
	if !found {
		t.Fatalf("the create does not build what it creates; calls were %v", docker.Calls())
	}
	want := []string{
		"compose",
		"--project-name", spec.Identity,
		"--project-directory", "/repos/app/api",
		"--file", "/repos/app/api/docker-compose.yml",
		"--file", "/repos/app/api/docker-compose.dev.yml",
		"--file", spec.OverridePath,
		"--env-file", "/repos/app/api/.env",
		"up", "--no-start", "api",
	}
	if !slices.Equal(vector, want) {
		t.Errorf("the create invocation changed\n got: %v\nwant: %v", vector, want)
	}
	for _, call := range docker.Calls() {
		if strings.HasPrefix(call, "start") || call == "up --detach api" {
			t.Errorf("the create ran %q, and a create starts nothing", call)
		}
	}
}

// TestAskingARuntimeThatWasNeverCreated covers the first thing a user does.
//
// Every command carries the generated override, and that document does not exist
// until something creates or starts the services — so the obvious implementation
// answers "what is running?" with a Compose error about a file Feat generates.
// Found by running the real thing rather than by reasoning about it.
func TestAskingARuntimeThatWasNeverCreated(t *testing.T) {
	docker := runtimetest.New()
	services, spec := arrange(t, docker)

	state, err := services.Observe(context.Background())
	if err != nil {
		t.Fatalf("observing a runtime that was never created: %v", err)
	}
	if state.Lifecycle != domain.RuntimeRunning {
		// The fake answers as though it were running; what is under test is that
		// the command ran at all.
		t.Errorf("the observed state is %q", state.Lifecycle)
	}

	vector, found := docker.Vector("ps --all --format json")
	if !found {
		t.Fatalf("no state was asked for: %v", docker.Calls())
	}
	if slices.Contains(vector, spec.OverridePath) {
		t.Errorf("the command names an override that does not exist yet: %v", vector)
	}

	// And once something has written it, every command carries it again.
	if _, err := services.Start(context.Background()); err != nil {
		t.Fatalf("starting: %v", err)
	}
	started, found := docker.Vector("up --detach api")
	if !found || !slices.Contains(started, spec.OverridePath) {
		t.Errorf("the start does not carry the generated override: %v", started)
	}
}

// TestLogsOpenNormalComposeOutput is the second acceptance criterion.
//
// FR-RUN-006 asks for normal Compose logs rather than something Feat aggregates
// or persists, so what the adapter produces is a command for the client to run.
func TestLogsOpenNormalComposeOutput(t *testing.T) {
	docker := runtimetest.New()
	services, spec := arrange(t, docker)

	invocation, err := services.Logs(context.Background())
	if err != nil {
		t.Fatalf("building the logs command: %v", err)
	}

	if filepath.Base(invocation.Program) != compose.Executable {
		t.Errorf("the logs command runs %s, want %s", invocation.Program, compose.Executable)
	}
	joined := strings.Join(invocation.Arguments, " ")
	for _, required := range []string{"compose", "--project-name " + spec.Identity, "logs --follow"} {
		if !strings.Contains(joined, required) {
			t.Errorf("the logs command does not contain %q: %v", required, invocation.Arguments)
		}
	}
	if docker.Ran("logs --follow") {
		t.Error("the adapter ran the logs command itself; the client runs it with its own terminal")
	}
}

// TestDestroyRetainsVolumesAndReachesNothingOutsideTheProject is the third
// acceptance criterion, checked at the argument vector rather than at the
// outcome.
//
// A volume that survives because the fake never removed it would prove nothing.
// What is checked is that the command cannot remove one, cannot remove an
// orphan, and names nothing at all — so its whole reach is the task's own
// Compose project, which is what puts a resource Feat does not own beyond it by
// construction rather than by exclusion (ADR-048).
func TestDestroyRetainsVolumesAndReachesNothingOutsideTheProject(t *testing.T) {
	docker := runtimetest.New()
	docker.Answer("volume ls --filter label=com.docker.compose.project="+identity+" --format {{.Name}}",
		"feat-app-11111111_pgdata\n")
	services, _ := arrange(t, docker)

	state, err := services.Destroy(context.Background())
	if err != nil {
		t.Fatalf("destroying: %v", err)
	}

	vector, found := docker.Vector("down")
	if !found {
		t.Fatalf("no destroy command was run; calls were %v", docker.Calls())
	}
	for _, forbidden := range []string{"--volumes", "-v", "--remove-orphans"} {
		if slices.Contains(vector, forbidden) {
			t.Errorf("the destroy command carries %q, which would remove something nobody chose: %v",
				forbidden, vector)
		}
	}
	if last := vector[len(vector)-1]; last != "down" {
		t.Errorf("the destroy command names %q after down, so its reach is wider than the task's own "+
			"Compose project: %v", last, vector)
	}
	if !slices.Contains(state.Volumes, "feat-app-11111111_pgdata") {
		t.Errorf("destroy did not report the volume it retained: %v", state.Volumes)
	}
}

// TestStoppingReachesEverythingStartingStarted is the defect a real project
// found, as a test.
//
// A stop that named the managed services stopped exactly the containers Feat had
// asked Compose for and left the ones Compose started to satisfy them: a
// database still up, still holding its published port, absent from every status
// Feat printed, and stopped by nothing short of a destroy. What starting brings
// up, stopping takes down — so a stop names no service and addresses the task's
// whole Compose project (ADR-034 evidence 12).
func TestStoppingReachesEverythingStartingStarted(t *testing.T) {
	services, _, docker := dependent(t)
	ctx := context.Background()

	if _, err := services.Start(ctx); err != nil {
		t.Fatalf("starting: %v", err)
	}
	if _, err := services.Stop(ctx); err != nil {
		t.Fatalf("stopping: %v", err)
	}

	vector, found := docker.Vector("stop")
	if !found {
		t.Fatalf("no stop command was run; calls were %v", docker.Calls())
	}
	if last := vector[len(vector)-1]; last != "stop" {
		t.Errorf("the stop command names %q after the action, so it reaches some of the task's containers "+
			"and not others: %v", last, vector)
	}

	// The same for the logs and the observation: what a user is shown is the
	// project, because the project is what Feat brought into existence.
	invocation, err := services.Logs(ctx)
	if err != nil {
		t.Fatalf("building the logs command: %v", err)
	}
	if last := invocation.Arguments[len(invocation.Arguments)-1]; last != "--follow" {
		t.Errorf("the logs command names %q last, so it hides the service a user needs when a managed "+
			"one will not start: %v", last, invocation.Arguments)
	}
}

// TestAFailedStartIsExplainedInFeatsTerms covers the messages a user acts on.
//
// Each case is a failure the container runtime describes accurately and in terms
// of a resource rather than a decision. Feat knows which decision produced the
// resource, and that is the difference between a message someone can act on and
// one they have to investigate.
func TestAFailedStartIsExplainedInFeatsTerms(t *testing.T) {
	for name, testCase := range map[string]struct {
		stdout   string
		stderr   string
		contains string
	}{
		"a port another task already publishes": {
			stderr:   `Error response from daemon: Bind for 0.0.0.0:8080 failed: port is already allocated`,
			contains: "Host port 8080 is already taken",
		},
		"a container name that survived": {
			stderr:   `Error response from daemon: Conflict. The container name "/api" is already in use`,
			contains: "global to the Docker daemon",
		},
		"a write into a read-only worktree": {
			stderr:   `error mounting "/srv/store/build": read-only file system`,
			contains: "selected that repository read-only",
		},
		"the last line rather than the first": {
			// Compose narrates every step, so a failure begins with progress.
			// Reporting the first line turns a precise error into "Building".
			stdout:   "Image api Building\nContainer api Creating\n",
			stderr:   "Container api Creating\nError response from daemon: Bind for 0.0.0.0:8080 failed: port is already allocated",
			contains: "8080",
		},
	} {
		t.Run(name, func(t *testing.T) {
			docker := runtimetest.New()
			docker.Reply("up --detach api", testCase.stdout, testCase.stderr, 1)
			services, _ := arrange(t, docker)

			_, err := services.Start(context.Background())
			if err == nil {
				t.Fatal("the start succeeded, and it should not have")
			}
			if !strings.Contains(err.Error(), testCase.contains) {
				t.Fatalf("the message does not explain the failure.\n got: %s\nwant it to contain: %s",
					err, testCase.contains)
			}
		})
	}
}

// TestAnAbsentDockerIsReportedWhereItCanBeFixed keeps the failure at
// construction, where the message can still name what to install.
func TestAnAbsentDockerIsReportedWhereItCanBeFixed(t *testing.T) {
	docker := runtimetest.New().Missing(compose.Executable)

	_, err := compose.New(runtime.Spec{
		Project: project, Task: task, Identity: identity,
		Files: []string{"/repos/app/api/docker-compose.yml"}, Directory: "/repos/app/api",
		OverridePath: "/state/runtime/compose.override.yaml", Services: []string{"api"},
	}, compose.Options{Runner: docker})

	if err == nil {
		t.Fatal("a host without Docker built a runtime")
	}
	if !errors.Is(err, runtime.ErrNotInstalled) {
		t.Errorf("the error does not identify an absent tool: %v", err)
	}
	if !strings.Contains(err.Error(), "Install Docker") {
		t.Errorf("the message does not say what to do: %v", err)
	}
}

// TestAnOldComposeIsRefusedBeforeItWritesAYamlError covers the version floor the
// generated override needs.
//
// A Compose below 2.24 does not know the !reset tag and fails with a YAML error
// that says nothing about why Feat wrote that document.
func TestAnOldComposeIsRefusedBeforeItWritesAYamlError(t *testing.T) {
	docker := runtimetest.New().Answer("version --short", "2.20.3")
	services, _ := arrange(t, docker)

	err := services.Validate(context.Background())
	if err == nil {
		t.Fatal("an old Docker Compose was accepted")
	}
	if !strings.Contains(err.Error(), "2.24") || !strings.Contains(err.Error(), "container_name") {
		t.Errorf("the message does not explain what the version is needed for: %v", err)
	}
}

// TestAnUnreadableVersionIsNotTreatedAsTooOld keeps a Docker release from
// becoming an outage.
func TestAnUnreadableVersionIsNotTreatedAsTooOld(t *testing.T) {
	docker := runtimetest.New().Answer("version --short", "docker-compose-plugin (dev build)")
	services, _ := arrange(t, docker)

	if err := services.Validate(context.Background()); err != nil {
		t.Errorf("an unreadable version was refused: %v", err)
	}
}
