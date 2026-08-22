package compose_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/integrationtest"
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
// does. A machine with no Docker daemon skips them unless the run demanded
// Docker, in which case it fails: a skipped package still prints "ok", so the
// demand is the only thing that stops a stopped Docker Desktop from taking
// every proof below out of the gate quietly (G6-05).

// realDocker ends the test unless this machine can run the tests below.
func realDocker(t *testing.T) {
	t.Helper()

	if !integrationtest.Enabled() {
		t.Skipf("set %s=1 to run the tests that drive real Docker", integrationtest.Env)
	}
	if _, err := exec.LookPath(compose.Executable); err != nil {
		integrationtest.Unavailable(t, integrationtest.Docker, "Docker is not installed on this machine")
	}
	if err := exec.Command(compose.Executable, "info").Run(); err != nil {
		integrationtest.Unavailable(t, integrationtest.Docker, "no Docker daemon is reachable from this machine: %v", err)
	}
}

// realRuntime arranges one task's application services over the awkward fixture.
//
// Whatever the test does, the Compose project is removed when it ends: a test
// that leaves containers behind on the machine running it has broken the rule it
// exists to check.
// A host port may be given, which is what Feat allocates for a task's one
// reachable service. Without one the fixture's own fixed publications are simply
// removed, which is what every service the project did not declare reachable
// gets.
func realRuntime(t *testing.T, id domain.TaskID, publish ...int) (*compose.Runtime, runtime.Spec, string) {
	t.Helper()

	root := t.TempDir()
	// Two repositories, because an application is composed of the repositories
	// that bring it. Everything relative in the second one resolves into the
	// first if the include document is wrong.
	api := filepath.Join(root, "api")
	web := filepath.Join(root, "web")
	worktree := filepath.Join(root, "worktrees", "api")

	for _, dir := range []string{api, web, worktree,
		filepath.Join(api, "base-mount"), filepath.Join(web, "content")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("creating %s: %v", dir, err)
		}
	}

	// The first repository: the Compose file, the Dockerfile its one-shot
	// dependency is built from, and the directory its base file mounts.
	apiCompose := filepath.Join(api, "docker-compose.yml")
	copyFixture(t, "application.yaml", apiCompose)
	copyFixture(t, "prepare.Dockerfile", filepath.Join(api, "prepare.Dockerfile"))

	// The second repository: its own Compose file, its own Dockerfile, and its
	// own content.
	webCompose := filepath.Join(web, "docker-compose.yml")
	copyFixture(t, "web.yaml", webCompose)
	copyFixture(t, "web.Dockerfile", filepath.Join(web, "web.Dockerfile"))

	// A file in each place, so that what a container sees can be compared with
	// what the host put there. Each repository has its own origin.txt, so an
	// image built from the wrong context is an image that says which one.
	for name, dir := range map[string]string{
		"task-worktree.txt": worktree,
		"base-mount.txt":    filepath.Join(api, "base-mount"),
		"from-web.txt":      filepath.Join(web, "content"),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	for dir, origin := range map[string]string{api: "api", web: "web"} {
		if err := os.WriteFile(filepath.Join(dir, "origin.txt"), []byte(origin), 0o600); err != nil {
			t.Fatalf("writing the origin marker of %s: %v", origin, err)
		}
	}

	// Two environment files, because Compose has to accept both --env-file flags
	// and because Feat passes them by path and never reads them.
	var envFiles []string
	for i, body := range []string{"FEAT_TEST_ONE=1\n", "FEAT_TEST_TWO=2\n"} {
		path := filepath.Join(api, ".env."+string(rune('a'+i)))
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
		envFiles = append(envFiles, path)
	}

	generated := filepath.Join(root, "runtime")
	spec := runtime.Spec{
		Project:  "app",
		Task:     id,
		Identity: "feat-app-" + strings.ToLower(id.Key().String()),
		Includes: []runtime.Include{
			{Repository: "api", Directory: api, Files: []string{apiCompose}},
			{Repository: "web", Directory: web, Files: []string{webCompose}},
		},
		IncludePath:  filepath.Join(generated, "compose.include.yaml"),
		Directory:    generated,
		OverridePath: filepath.Join(generated, "compose.override.yaml"),
		EnvFiles:     envFiles,
		Services:     []string{"api", "web"},
		Mounts: []runtime.Mount{
			{Services: []string{"api"}, Source: worktree, Target: "/srv/api",
				Description: "the api task worktree, read-write"},
		},
		Variables:        map[string]string{"FEAT_TASK_KEY": id.Key().String()},
		ForbiddenSources: []string{filepath.Join(root, "repos", "api")},
	}
	for _, port := range publish {
		// On the loopback address, which is what the daemon resolves for a
		// project that named none: this fixture is the real-Docker counterpart
		// of what the allocator produces, so a binding it did not carry would be
		// a binding no integration test ever made Docker perform.
		spec.Publications = append(spec.Publications, runtime.Publication{
			Service: "api", ContainerPort: 80, HostPort: port, Protocol: "tcp", HostIP: "127.0.0.1",
			Description: "allocated for this task",
		})
		spec.Variables["FEAT_URL_API"] = "http://localhost:" + strconv.Itoa(port)
		spec.Variables["FEAT_PORT_API"] = strconv.Itoa(port)
	}

	services, err := compose.New(spec, compose.Options{})
	if err != nil {
		t.Fatalf("building the runtime: %v", err)
	}
	t.Cleanup(func() {
		// Only this task's own Compose project, named explicitly, and with its
		// volumes — the fixture creates none, and a test that left one behind
		// would be leaving it on somebody's machine. `--rmi local` removes the
		// image the fixture builds, which is named after this task's Compose
		// project and is of no use to anything else; an image the fixture names
		// itself, such as alpine, carries a tag of its own and is left alone.
		down := exec.Command(compose.Executable, "compose",
			"--project-name", spec.Identity, "--project-directory", spec.Directory,
			"--file", spec.IncludePath, "--file", spec.OverridePath,
			"down", "--volumes", "--rmi", "local", "--timeout", "1")
		if output, err := down.CombinedOutput(); err != nil {
			t.Logf("cleaning up %s: %v\n%s", spec.Identity, err, output)
		}
	})
	return services, spec, worktree
}

// copyFixture writes one testdata file to where a repository keeps it.
func copyFixture(t *testing.T, name, destination string) {
	t.Helper()

	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("reading the fixture %s: %v", name, err)
	}
	if err := os.WriteFile(destination, body, 0o600); err != nil {
		t.Fatalf("writing %s: %v", destination, err)
	}
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
	// The generated variables reach the service. FEAT_TASK_KEY is the one an
	// application names its share of an external resource by (ADR-048).
	if out, ok := insideService(t, spec.Identity, spec.Directory, "api", "printenv", "FEAT_TASK_KEY"); !ok {
		t.Errorf("the generated task key did not reach the service: %s", out)
	}
	// And what the service writes reaches the host worktree.
	if _, ok := insideService(t, spec.Identity, spec.Directory, "api",
		"touch", "/srv/api/written-by-the-service"); !ok {
		t.Error("the task worktree is not writable from inside the service")
	}
	if _, err := os.Stat(filepath.Join(worktree, "written-by-the-service")); err != nil {
		t.Errorf("what the service wrote did not reach the host worktree: %v", err)
	}

	// The second repository runs from its own checkout. Both assertions fail if
	// the two repositories' files are merged rather than included: the image
	// would be built from the first repository's directory, and the bind source
	// would point into it (ADR-065 evidence 2).
	if out, ok := insideService(t, spec.Identity, spec.Directory, "web", "cat", "/origin.txt"); !ok ||
		strings.TrimSpace(out) != "web" {
		t.Errorf("the web service was built from the wrong context: %q", strings.TrimSpace(out))
	}
	if out, ok := insideService(t, spec.Identity, spec.Directory, "web",
		"cat", "/srv/content/from-web.txt"); !ok {
		t.Errorf("the web service's own relative mount did not resolve against its own checkout: %s", out)
	}
	// And it holds no worktree of the other repository. A service runs one
	// repository's code, and mounting every worktree into every service is what
	// makes two repositories expecting their source at one path a collision.
	if out, ok := insideService(t, spec.Identity, spec.Directory, "web", "ls", "/srv/api"); ok {
		t.Errorf("the web service holds the api worktree, which is not its code: %s", out)
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

// TestRealABakedServiceRunsTheTaskWorktree is this slice's first acceptance
// criterion against real Docker, and the failure it exists for.
//
// The web service of the fixture is the shape that defeats every mount-based
// check: its image copies the repository in and it mounts nothing of it, so
// where its code comes from was decided when the image was built. Redirecting
// the build context is the only thing that can point it at the task's own work,
// and the container's own copy of origin.txt is what says which directory it was
// really built from (ADR-065 evidence 4).
//
// The Dockerfile is named relatively in the project's own file and is not
// redirected, so this also measures the claim that a relative `dockerfile:`
// follows the new context.
func TestRealABakedServiceRunsTheTaskWorktree(t *testing.T) {
	realDocker(t)

	// The arranged runtime is discarded and rebuilt below with the redirect: the
	// two would share one Compose project, and what is under test is the one that
	// carries the build context. Its cleanup is registered against the
	// specification, so it still removes what this starts.
	_, spec, _ := realRuntime(t, domain.NewTaskID())
	root := filepath.Dir(spec.Directory)
	worktree := filepath.Join(root, "worktrees", "web")

	// The task's own copy of the second repository: the same Dockerfile, and an
	// origin.txt saying it is the worktree rather than the checkout.
	if err := os.MkdirAll(worktree, 0o700); err != nil {
		t.Fatalf("creating the web worktree: %v", err)
	}
	copyFixture(t, "web.Dockerfile", filepath.Join(worktree, "web.Dockerfile"))
	if err := os.WriteFile(filepath.Join(worktree, "origin.txt"), []byte("web-worktree"), 0o600); err != nil {
		t.Fatalf("writing the worktree's origin marker: %v", err)
	}

	spec.Builds = []runtime.Build{{
		Service: "web", Repository: "web", Context: worktree,
		Description: "the web task worktree, which this service's image is built from",
	}}
	redirected, err := compose.New(spec, compose.Options{})
	if err != nil {
		t.Fatalf("building the runtime: %v", err)
	}
	if _, err := redirected.Start(context.Background()); err != nil {
		t.Fatalf("starting: %v", err)
	}

	out, ok := insideService(t, spec.Identity, spec.Directory, "web", "cat", "/origin.txt")
	if !ok || strings.TrimSpace(out) != "web-worktree" {
		t.Errorf("a service that bakes its code was built from %q, want the task worktree: mounts alone "+
			"cannot reach it, so its build context is the only thing that can", strings.TrimSpace(out))
	}

	// And nothing was mounted to achieve it: this is the service the mount-based
	// check cannot see.
	report, err := redirected.Inspect(context.Background(), mustObserve(t, redirected))
	if err != nil {
		t.Fatalf("inspecting: %v", err)
	}
	for _, mount := range report.Mounts {
		if mount.Service == "web" && mount.Type == "bind" && strings.Contains(mount.Source, "worktrees") {
			t.Errorf("the web service holds a bind mount of the worktree at %s, so this test is no "+
				"longer about a service with none", mount.Destination)
		}
	}
}

// mustObserve reads a runtime's state or fails the test.
func mustObserve(t *testing.T, services *compose.Runtime) runtime.State {
	t.Helper()

	state, err := services.Observe(context.Background())
	if err != nil {
		t.Fatalf("observing: %v", err)
	}
	return state
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

// TestRealAllocatedPortsAreWhatIsPublished is the mechanism this slice rests on,
// against real Docker.
//
// Three things are checked at once because they are one arrangement: two tasks
// whose application publishes a fixed host port both start, each is bound to the
// port allocated for it, and the fixed ports the project wrote are bound by
// neither — not the managed service's, and not the one on the dependency nobody
// named. A host port is global to the machine, so if any of that were wrong the
// second `up` would fail with an address already in use, which is the failure
// this slice removes.
//
// The Compose tags doing the work are !override on the service that has an
// allocation and !reset on every service that has none. Both are read by the
// installed Compose rather than by a fixture, which is the only place their
// behaviour can be established (ADR-033 evidence 10 to 14).
func TestRealAllocatedPortsAreWhatIsPublished(t *testing.T) {
	realDocker(t)

	// Two ports well outside the fixture's own, so that a container bound to a
	// fixed one is unambiguous.
	const firstPort, secondPort = 24990, 24991
	first, firstSpec, _ := realRuntime(t, domain.NewTaskID(), firstPort)
	second, secondSpec, _ := realRuntime(t, domain.NewTaskID(), secondPort)
	ctx := context.Background()

	firstState, err := first.Start(ctx)
	if err != nil {
		t.Fatalf("starting the first task: %v", err)
	}
	secondState, err := second.Start(ctx)
	if err != nil {
		t.Fatalf("starting the second task while the first publishes the same application: %v", err)
	}

	for _, arranged := range []struct {
		name  string
		state runtime.State
		port  int
	}{{"first", firstState, firstPort}, {"second", secondState, secondPort}} {
		var published []domain.PortAssignment
		published = append(published, arranged.state.Ports...)

		if len(published) != 1 {
			t.Fatalf("the %s task publishes %+v, want exactly the port allocated for it",
				arranged.name, published)
		}
		if got := published[0]; got.Service != "api" || got.HostPort != arranged.port ||
			got.ContainerPort != 80 {
			t.Errorf("the %s task published %+v, want api's 80 on host port %d",
				arranged.name, got, arranged.port)
		}
	}

	// And the fixture's own fixed ports are bound by nothing: the first is the
	// managed service's, the second is on a dependency the project never named.
	for _, spec := range []runtime.Spec{firstSpec, secondSpec} {
		state, err := composeRuntime(t, spec).Observe(ctx)
		if err != nil {
			t.Fatalf("observing %s: %v", spec.Identity, err)
		}
		for _, port := range state.Ports {
			if port.HostPort == 24999 || port.HostPort == 24998 {
				t.Errorf("%s bound the fixed host port %d the project wrote, which one task at a "+
					"time can hold: %+v", spec.Identity, port.HostPort, state.Ports)
			}
		}
	}
}

// composeRuntime rebuilds an adapter over an existing specification, so that a
// test can observe a project it started through another one.
func composeRuntime(t *testing.T, spec runtime.Spec) *compose.Runtime {
	t.Helper()

	services, err := compose.New(spec, compose.Options{})
	if err != nil {
		t.Fatalf("rebuilding the runtime of %s: %v", spec.Identity, err)
	}
	return services
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
	// Two managed services, one per repository, and the two Compose started
	// because a managed one depends on them.
	if len(state.Services) != 4 || state.Services[0].Container == "" {
		t.Fatalf("the observed services are %+v, want the two managed ones and the two Compose started "+
			"with them", state.Services)
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
	if len(strings.Fields(string(owned))) != 4 {
		t.Errorf("%d containers carry the first task's label, want 4: a resource Feat started and did not "+
			"label is one a cleanup cannot find", len(strings.Fields(string(owned))))
	}
}
