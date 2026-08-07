package compose_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/runtime"
	"github.com/ma8el/feat/internal/runtime/compose"
)

// The opt-in tests below drive real Docker. They are the ones that can answer
// the questions a fake cannot: whether Compose merges mounts the way the design
// assumes, whether !reset really removes a container name, whether several
// --env-file flags are accepted, what `ps --format json` actually contains, and
// whether two tasks can run the same services at once.
//
// ADR-033 evidence 10 to 14 were all Feat asking a correct question and reading
// the answer wrongly, and none of them was reachable from a fixture. This suite
// is where that class of defect is caught.
//
// They run under FEAT_INTEGRATION, as every TestReal suite in this repository
// does, and they skip when this machine has no Docker daemon. A skipped test
// says so rather than passing quietly.

// realDocker reports whether this machine can run the tests below.
func realDocker(t *testing.T) {
	t.Helper()

	if os.Getenv("FEAT_INTEGRATION") == "" {
		t.Skip("set FEAT_INTEGRATION=1 to run the tests that drive real Docker")
	}
	if _, err := exec.LookPath(compose.Executable); err != nil {
		t.Skip("Docker is not installed on this machine")
	}
	if err := exec.Command(compose.Executable, "info").Run(); err != nil {
		t.Skip("no Docker daemon is reachable from this machine")
	}
}

// realRuntime arranges one task's application services over the awkward fixture.
//
// Whatever the test does, the Compose project is removed when it ends: a test
// that leaves containers behind on the machine running it has broken the rule it
// exists to check.
func realRuntime(t *testing.T, id domain.TaskID) (*compose.Runtime, runtime.Spec, string) {
	t.Helper()

	root := t.TempDir()
	project := filepath.Join(root, "application")
	worktree := filepath.Join(root, "worktrees", "api")

	for _, dir := range []string{project, worktree, filepath.Join(project, "base-mount")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("creating %s: %v", dir, err)
		}
	}

	fixture, err := os.ReadFile(filepath.Join("testdata", "application.yaml"))
	if err != nil {
		t.Fatalf("reading the fixture: %v", err)
	}
	composeFile := filepath.Join(project, "docker-compose.yml")
	if err := os.WriteFile(composeFile, fixture, 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}

	// A file in each place, so that what the container sees can be compared with
	// what the host put there.
	for name, dir := range map[string]string{
		"task-worktree.txt": worktree,
		"base-mount.txt":    filepath.Join(project, "base-mount"),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}

	// Two environment files, because Compose has to accept both --env-file flags
	// and because Feat passes them by path and never reads them.
	var envFiles []string
	for i, body := range []string{"FEAT_TEST_ONE=1\n", "FEAT_TEST_TWO=2\n"} {
		path := filepath.Join(project, ".env."+string(rune('a'+i)))
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
		envFiles = append(envFiles, path)
	}

	spec := runtime.Spec{
		Project:      "app",
		Task:         id,
		Identity:     "feat-app-" + strings.ToLower(id.Key().String()),
		Files:        []string{composeFile},
		Directory:    project,
		OverridePath: filepath.Join(root, "runtime", "compose.override.yaml"),
		EnvFiles:     envFiles,
		Services:     []string{"api"},
		Mounts: []runtime.Mount{
			{Source: worktree, Target: "/srv/api", Description: "the api task worktree, read-write"},
		},
		Variables: map[string]string{"FEAT_TASK_KEY": id.Key().String()},
		External: []runtime.ExternalBinding{
			{ID: "staging_db", Kind: "postgres", Variable: "FEAT_STAGING_SCHEMA", Selector: id.Key().String()},
		},
		ForbiddenSources: []string{filepath.Join(root, "repos", "api")},
	}
	spec.Variables["FEAT_STAGING_SCHEMA"] = id.Key().String()

	services, err := compose.New(spec, compose.Options{})
	if err != nil {
		t.Fatalf("building the runtime: %v", err)
	}
	t.Cleanup(func() {
		// Only this task's own Compose project, named explicitly, and with its
		// volumes — the fixture creates none, and a test that left one behind
		// would be leaving it on somebody's machine.
		down := exec.Command(compose.Executable, "compose",
			"--project-name", spec.Identity, "--project-directory", spec.Directory,
			"--file", composeFile, "--file", spec.OverridePath,
			"down", "--volumes", "--timeout", "1")
		if output, err := down.CombinedOutput(); err != nil {
			t.Logf("cleaning up %s: %v\n%s", spec.Identity, err, output)
		}
	})
	return services, spec, worktree
}

// insideService runs a command in a service's container.
func insideService(t *testing.T, identity, directory, service string, args ...string) (string, bool) {
	t.Helper()

	vector := append([]string{
		"compose", "--project-name", identity, "--project-directory", directory,
		"exec", "--no-TTY", service,
	}, args...)
	// #nosec G204 -- a test running the container tool with a fixed vector.
	output, err := exec.Command(compose.Executable, vector...).CombinedOutput()
	return string(output), err == nil
}

// TestRealTheLifecycleIsManualAndComplete drives every action FR-RUN-005 names
// against real Docker, in the order a user would.
func TestRealTheLifecycleIsManualAndComplete(t *testing.T) {
	realDocker(t)

	services, spec, worktree := realRuntime(t, domain.NewTaskID())
	ctx := context.Background()

	if err := services.Validate(ctx); err != nil {
		t.Fatalf("validating: %v", err)
	}

	// Create: the containers exist and are not running. It is the state that
	// makes "stopped containers are never restarted" something a user can
	// arrange deliberately.
	created, err := services.Create(ctx)
	if err != nil {
		t.Fatalf("creating: %v", err)
	}
	if created.Lifecycle != domain.RuntimeStopped {
		t.Errorf("after create the runtime is %q, want stopped: %+v", created.Lifecycle, created.Services)
	}

	// Start: up, and running the task's own code rather than the base file's.
	started, err := services.Start(ctx)
	if err != nil {
		t.Fatalf("starting: %v", err)
	}
	if started.Lifecycle != domain.RuntimeRunning {
		t.Fatalf("after start the runtime is %q, want running: %+v", started.Lifecycle, started.Services)
	}
	if started.Health != domain.HealthUnknown {
		t.Errorf("a service with no health check reports %q, and the honest answer is unknown", started.Health)
	}

	if out, ok := insideService(t, spec.Identity, spec.Directory, "api", "cat", "/srv/api/task-worktree.txt"); !ok {
		t.Errorf("the task worktree is not mounted at /srv/api: %s", out)
	}
	if out, ok := insideService(t, spec.Identity, spec.Directory, "api", "cat", "/srv/api/base-mount.txt"); ok {
		t.Errorf("the base file's own mount survived at /srv/api, so the service runs the wrong code: %s", out)
	}
	// The generated variables reach the service, and the external resource's
	// selector with them.
	if out, ok := insideService(t, spec.Identity, spec.Directory, "api", "printenv", "FEAT_STAGING_SCHEMA"); !ok {
		t.Errorf("the generated selector did not reach the service: %s", out)
	}
	// And what the service writes reaches the host worktree.
	if _, ok := insideService(t, spec.Identity, spec.Directory, "api",
		"touch", "/srv/api/written-by-the-service"); !ok {
		t.Error("the task worktree is not writable from inside the service")
	}
	if _, err := os.Stat(filepath.Join(worktree, "written-by-the-service")); err != nil {
		t.Errorf("what the service wrote did not reach the host worktree: %v", err)
	}

	// Logs: the ordinary command, which runs. The vector is the adapter's own,
	// with --follow dropped so that this test ends: what is under test is that
	// Compose accepts the command Feat hands the client, files and all.
	logs, err := services.Logs(ctx)
	if err != nil {
		t.Fatalf("building the logs command: %v", err)
	}
	arguments := make([]string, 0, len(logs.Arguments))
	for _, argument := range logs.Arguments {
		if argument == "--follow" {
			continue
		}
		arguments = append(arguments, argument)
	}
	// #nosec G204 -- the vector the adapter built, run by a test.
	if output, err := exec.Command(logs.Program, arguments...).CombinedOutput(); err != nil {
		t.Errorf("the logs command failed: %v\n%s", err, output)
	}

	// Stop: not running, containers retained.
	stopped, err := services.Stop(ctx)
	if err != nil {
		t.Fatalf("stopping: %v", err)
	}
	if stopped.Lifecycle != domain.RuntimeStopped {
		t.Errorf("after stop the runtime is %q, want stopped: %+v", stopped.Lifecycle, stopped.Services)
	}
	if !stopped.Present {
		t.Error("stopping removed the containers; stop keeps them")
	}

	// Destroy: gone, and nothing else.
	destroyed, err := services.Destroy(ctx)
	if err != nil {
		t.Fatalf("destroying: %v", err)
	}
	if destroyed.Present {
		t.Errorf("after destroy the containers still exist: %+v", destroyed.Services)
	}
}

// TestRealTwoTasksRunTheSameServicesAtOnce is the first acceptance criterion
// against real Docker.
//
// The fixture carries a fixed container name, which is global to the Docker
// daemon, so this passes only because the generated override resets it. Without
// that line the second task fails and one task per machine is the product.
func TestRealTwoTasksRunTheSameServicesAtOnce(t *testing.T) {
	realDocker(t)

	first, firstSpec, _ := realRuntime(t, domain.NewTaskID())
	second, secondSpec, _ := realRuntime(t, domain.NewTaskID())
	ctx := context.Background()

	if _, err := first.Start(ctx); err != nil {
		t.Fatalf("starting the first task: %v", err)
	}
	if _, err := second.Start(ctx); err != nil {
		t.Fatalf("starting the second task while the first is running: %v", err)
	}

	// Each task's own code, in its own container.
	for _, arranged := range []struct {
		name string
		spec runtime.Spec
	}{{"first", firstSpec}, {"second", secondSpec}} {
		out, ok := insideService(t, arranged.spec.Identity, arranged.spec.Directory, "api",
			"cat", "/srv/api/task-worktree.txt")
		if !ok {
			t.Errorf("the %s task's service does not hold its own worktree: %s", arranged.name, out)
		}
	}

	// And stopping one leaves the other alone, which is what the criterion is.
	if _, err := first.Stop(ctx); err != nil {
		t.Fatalf("stopping the first task: %v", err)
	}
	state, err := second.Observe(ctx)
	if err != nil {
		t.Fatalf("observing the second task: %v", err)
	}
	if state.Lifecycle != domain.RuntimeRunning {
		t.Errorf("stopping one task left the other %q, want running", state.Lifecycle)
	}
}

// TestRealDestroyRetainsVolumes is the volume-retention rule against Docker.
//
// The volume is created outside the fixture and labelled as this project's, so
// that what is checked is the command Feat runs rather than what the fixture
// happens to declare.
func TestRealDestroyRetainsVolumes(t *testing.T) {
	realDocker(t)

	services, spec, _ := realRuntime(t, domain.NewTaskID())
	ctx := context.Background()

	if _, err := services.Start(ctx); err != nil {
		t.Fatalf("starting: %v", err)
	}

	volume := spec.Identity + "_retained"
	// #nosec G204 -- a test creating a volume it removes itself.
	if output, err := exec.Command(compose.Executable, "volume", "create",
		"--label", "com.docker.compose.project="+spec.Identity, volume).CombinedOutput(); err != nil {
		t.Fatalf("creating the volume: %v\n%s", err, output)
	}
	t.Cleanup(func() {
		// #nosec G204 -- the volume this test created.
		_ = exec.Command(compose.Executable, "volume", "rm", "--force", volume).Run()
	})

	state, err := services.Destroy(ctx)
	if err != nil {
		t.Fatalf("destroying: %v", err)
	}

	// #nosec G204 -- inspecting the volume this test created.
	if output, err := exec.Command(compose.Executable, "volume", "inspect", volume).CombinedOutput(); err != nil {
		t.Fatalf("destroy removed a volume, and volumes are retained: %v\n%s", err, output)
	}
	found := false
	for _, retained := range state.Volumes {
		if retained == volume {
			found = true
		}
	}
	if !found {
		t.Errorf("destroy did not report the volume it retained: %v", state.Volumes)
	}
}

// TestRealComposeReportsWhatTheAggregationTableReads is ADR-033 evidence 10 and
// 12 as a standing check.
//
// Every row of the state table depends on fields `docker compose ps --format
// json` prints. If Compose renames one, Feat reports a running application as
// absent, and nothing else in the suite would notice.
func TestRealComposeReportsWhatTheAggregationTableReads(t *testing.T) {
	realDocker(t)

	services, spec, _ := realRuntime(t, domain.NewTaskID())
	ctx := context.Background()

	if _, err := services.Start(ctx); err != nil {
		t.Fatalf("starting: %v", err)
	}

	// #nosec G204 -- the same query the adapter makes, run directly.
	output, err := exec.Command(compose.Executable, "compose",
		"--project-name", spec.Identity, "--project-directory", spec.Directory,
		"ps", "--all", "--format", "json", "api").Output()
	if err != nil {
		t.Fatalf("asking Compose for its state: %v", err)
	}

	for _, field := range []string{`"ID"`, `"Service"`, `"State"`, `"Status"`} {
		if !strings.Contains(string(output), field) {
			t.Errorf("`docker compose ps --format json` no longer prints %s, which the state table reads:\n%s",
				field, output)
		}
	}

	state, err := services.Observe(ctx)
	if err != nil {
		t.Fatalf("observing: %v", err)
	}
	if len(state.Services) != 3 || state.Services[0].Container == "" {
		t.Fatalf("the observed services are %+v, want the managed one and the two Compose started with it",
			state.Services)
	}
	// The one-shot dependency has exited by now, and the application is running
	// all the same. A runtime that called itself degraded every time a migration
	// succeeded would be a state people learn to ignore.
	if state.Lifecycle != domain.RuntimeRunning {
		t.Errorf("a running service was read as %q: %+v", state.Lifecycle, state.Services)
	}
}

// TestRealStoppingReachesEverythingStartingStarted is the defect a real project
// found, against the tool that produced it.
//
// A stop that named the managed services stopped what Feat had asked Compose for
// and left what Compose had started to satisfy it: a database still up, still
// holding its published port, and invisible to every status Feat printed. No
// fixture would have shown it, because the fake answered for the services the
// test named.
func TestRealStoppingReachesEverythingStartingStarted(t *testing.T) {
	realDocker(t)

	services, spec, _ := realRuntime(t, domain.NewTaskID())
	ctx := context.Background()

	if _, err := services.Start(ctx); err != nil {
		t.Fatalf("starting: %v", err)
	}
	// #nosec G204 -- asking Docker for the containers this test's project owns.
	running, err := exec.Command(compose.Executable, "ps", "--quiet",
		"--filter", "label=com.docker.compose.project="+spec.Identity).Output()
	if err != nil {
		t.Fatalf("listing the running containers: %v", err)
	}
	if len(strings.Fields(string(running))) < 2 {
		t.Fatalf("the start left fewer than two containers running, so this test proves nothing: %s", running)
	}

	if _, err := services.Stop(ctx); err != nil {
		t.Fatalf("stopping: %v", err)
	}

	// #nosec G204 -- the same query, after the stop.
	left, err := exec.Command(compose.Executable, "ps", "--quiet",
		"--filter", "label=com.docker.compose.project="+spec.Identity).Output()
	if err != nil {
		t.Fatalf("listing what is left running: %v", err)
	}
	if fields := strings.Fields(string(left)); len(fields) != 0 {
		t.Errorf("%d containers of this task are still running after a stop: %v. What starting starts, "+
			"stopping stops", len(fields), fields)
	}
}

// TestRealTheGeneratedOverrideReachesADependency is the other half of that fix.
//
// A service the project does not manage keeps its own fixed container_name
// unless the generated override resets it, and a fixed name is global to the
// Docker daemon: the second task to start collides with the first over a
// container neither of their projects names.
func TestRealTheGeneratedOverrideReachesADependency(t *testing.T) {
	realDocker(t)

	first, firstSpec, _ := realRuntime(t, domain.NewTaskID())
	second, _, _ := realRuntime(t, domain.NewTaskID())
	ctx := context.Background()

	if _, err := first.Start(ctx); err != nil {
		t.Fatalf("starting the first task: %v", err)
	}
	if _, err := second.Start(ctx); err != nil {
		t.Fatalf("starting the second task while the first is running: %v. A service the project does not "+
			"manage kept a container name that is global to the Docker daemon", err)
	}

	// #nosec G204 -- inspecting the container of a service this test started.
	named, err := exec.Command(compose.Executable, "ps", "--all", "--quiet",
		"--filter", "name=feat-runtime-test-dependency").Output()
	if err != nil {
		t.Fatalf("looking for the fixed container name: %v", err)
	}
	if body := strings.TrimSpace(string(named)); body != "" {
		t.Errorf("a container still carries the dependency's fixed name, so one task per machine is the "+
			"product: %s", body)
	}

	// Both tasks' dependencies are labelled as theirs, which is what a later
	// cleanup resolves them by.
	// #nosec G204 -- the label filter for this test's own project.
	owned, err := exec.Command(compose.Executable, "ps", "--all", "--quiet",
		"--filter", "label=dev.feat.task="+firstSpec.Task.String()).Output()
	if err != nil {
		t.Fatalf("listing the first task's containers: %v", err)
	}
	if len(strings.Fields(string(owned))) != 3 {
		t.Errorf("%d containers carry the first task's label, want 3: a resource Feat started and did not "+
			"label is one a cleanup cannot find", len(strings.Fields(string(owned))))
	}
}
