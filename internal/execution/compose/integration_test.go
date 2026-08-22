package compose_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/execution"
	"github.com/ma8el/feat/internal/execution/compose"
	"github.com/ma8el/feat/internal/integrationtest"
)

// The opt-in tests below drive real Docker. They are the ones that can answer
// the questions a fake cannot: whether Compose merges mounts the way the whole
// design assumes, whether !reset really removes a container name and a published
// port, and whether two containers built from one file can run at once.
//
// They run under FEAT_INTEGRATION, as every TestReal suite in this repository
// does. A machine with no Docker daemon — which macOS CI runners do not have —
// skips them unless the run demanded Docker, in which case it fails: a
// skipped package still prints "ok", so the demand is the only thing that
// stops a stopped Docker Desktop from taking every proof below out of the
// gate quietly (G6-05).

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

// agentUser is the uid the agent runs as inside the fixture container.
//
// It is the uid of whoever runs the test rather than a fixed number, because a
// Linux bind mount carries the host's own ownership: a worktree this test wrote
// belongs to the user running it, and a container user with a different uid
// cannot write it. That is not a defect in the test — it is exactly what Check
// refuses a launch over, and what agent.execution.user exists to let a project
// get right — so the fixture arranges what a real Linux project must. A machine
// whose Docker maps ownership instead, as Docker Desktop does, is unaffected by
// which uid this is.
func agentUser(t *testing.T) string {
	t.Helper()

	uid := os.Getuid()
	if uid == 0 {
		// The agent may not be root, so a root-owned checkout cannot be the
		// subject of these tests: there is no non-root uid that could write it.
		//
		// Through the demand rather than as a bare skip, because realTask reaches
		// this and almost every TestReal below reaches realTask: a run as root
		// dropped the whole container-boundary suite while Docker was demanded and
		// answering, and printed "ok".
		integrationtest.Unavailable(t, integrationtest.Docker,
			"this run is root, and these tests need a non-root user "+
				"because the agent's container user must not be root")
	}
	return strconv.Itoa(uid)
}

// realTask arranges one task's environment over the awkward fixture.
//
// The returned environment is torn down when the test ends, whatever it did:
// a test that leaves containers behind on the machine running it has broken the
// rule it exists to check.
func realTask(t *testing.T, id domain.TaskID) (*compose.Environment, execution.Spec, string) {
	t.Helper()

	return realTaskFrom(t, id, "devcontainer.yaml")
}

// realTaskFrom arranges the same task over a named fixture, for the questions
// that turn on what a project's own Compose file says.
func realTaskFrom(t *testing.T, id domain.TaskID, fixtureName string) (*compose.Environment, execution.Spec, string) {
	t.Helper()

	root := t.TempDir()
	project := filepath.Join(root, "devcontainer")
	worktree := filepath.Join(root, "worktrees", "api")
	readOnly := filepath.Join(root, "worktrees", "store")
	control := filepath.Join(root, "control")

	for _, dir := range []string{
		project, worktree, readOnly, control,
		// The two directories the agent reports through, which the daemon mounts
		// read-write over the read-only workspace.
		filepath.Join(control, "outbox"), filepath.Join(control, "reports"),
		filepath.Join(project, "base-mount"),
	} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("creating %s: %v", dir, err)
		}
	}

	fixture, err := os.ReadFile(filepath.Join("testdata", fixtureName))
	if err != nil {
		t.Fatalf("reading the fixture: %v", err)
	}
	composeFile := filepath.Join(project, "docker-compose.yml")
	if err := os.WriteFile(composeFile, fixture, 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}

	// A file in each place, so that what the container sees can be compared
	// with what the host put there.
	for name, dir := range map[string]string{
		"task-worktree.txt": worktree, "read-only.txt": readOnly,
		"control.txt": control, "base-mount.txt": filepath.Join(project, "base-mount"),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}

	spec := execution.Spec{
		Project:          "app",
		Task:             id,
		Identity:         "feat-agent-app-" + string(id),
		Files:            []string{composeFile},
		Directory:        project,
		OverridePath:     filepath.Join(root, "execution", "compose.override.yaml"),
		Service:          "dev",
		User:             agentUser(t),
		WorkingDirectory: "/srv/api",
		Mounts: []execution.Mount{
			{Source: worktree, Target: "/srv/api", Description: "the api task worktree"},
			{Source: readOnly, Target: "/srv/store", ReadOnly: true, Description: "the store task worktree"},
			// As the daemon mounts it: the workspace read-only, with the two
			// directories the agent reports through mounted read-write over it.
			{Source: control, Target: "/feat", ReadOnly: true, Description: "the control workspace, read-only"},
			{Source: filepath.Join(control, "outbox"), Target: "/feat/outbox", Description: "the control outbox"},
			{Source: filepath.Join(control, "reports"), Target: "/feat/reports", Description: "the control reports"},
		},
		ForbiddenSources: []execution.ForbiddenSource{
			{Path: filepath.Join(root, "repos", "api"), Kind: execution.ForbiddenCheckout},
		},
	}

	environment, err := compose.New(spec, compose.Options{ReadyTimeout: 90 * time.Second})
	if err != nil {
		t.Fatalf("building the environment: %v", err)
	}
	t.Cleanup(func() {
		// Only this task's own Compose project, named explicitly.
		down := exec.Command(compose.Executable, "compose",
			"--project-name", spec.Identity, "--project-directory", spec.Directory,
			"--file", composeFile, "--file", spec.OverridePath,
			"down", "--timeout", "1")
		if output, err := down.CombinedOutput(); err != nil {
			t.Logf("cleaning up %s: %v\n%s", spec.Identity, err, output)
		}
	})
	return environment, spec, worktree
}

// source returns the host directory one of a specification's mounts comes from.
func source(t *testing.T, spec execution.Spec, target string) string {
	t.Helper()

	for _, mount := range spec.Mounts {
		if mount.Target == target {
			return mount.Source
		}
	}
	t.Fatalf("the specification mounts nothing at %s", target)
	return ""
}

// dependencyContainer returns the container Compose started for the service Feat
// never named, in one task's own Compose project.
//
// It is asked of Docker by Compose's own project and service labels rather than
// of the adapter, because the adapter observes the service the agent runs in and
// this is deliberately the other one: what has to be proved is that a service
// nothing in Feat mentions is still this task's alone.
func dependencyContainer(t *testing.T, identity string) string {
	t.Helper()

	output, err := exec.Command(compose.Executable, "ps",
		"--filter", "label=com.docker.compose.project="+identity,
		"--filter", "label=com.docker.compose.service=db",
		"--format", "{{.ID}}").Output()
	if err != nil {
		t.Fatalf("listing the dependency container of %s: %v", identity, err)
	}
	id := strings.TrimSpace(string(output))
	if id == "" {
		t.Fatalf("Compose project %s has no container for the service its devcontainer depends on", identity)
	}
	return id
}

// inside runs a command in the container and returns its output.
func inside(t *testing.T, environment *compose.Environment, program string, args ...string) execution.Output {
	t.Helper()

	output, err := environment.Run(context.Background(), execution.Command{Program: program, Arguments: args})
	if err != nil {
		t.Fatalf("running %s in the container: %v", program, err)
	}
	return output
}

// TestRealTaskWorktreesAppearAtTheirContainerPaths is acceptance criteria 1, 2,
// and 3 against a real container.
//
// Each is checked from inside: the task's own files are where the configuration
// said they would be, a read-only selection cannot be written to, and the agent's
// process is not root. The last two are properties of the running system that no
// amount of correct generated YAML would prove on its own.
func TestRealTaskWorktreesAppearAtTheirContainerPaths(t *testing.T) {
	realDocker(t)

	environment, spec, worktree := realTask(t, domain.NewTaskID())
	if err := environment.Prepare(context.Background()); err != nil {
		t.Fatalf("preparing the environment: %v", err)
	}

	// Acceptance criterion 1: each worktree at its configured container path.
	// The base file mounts something else at /srv/api, so this also proves the
	// merge-by-target replacement the design depends on.
	if got := inside(t, environment, "cat", "/srv/api/task-worktree.txt"); !got.Succeeded() {
		t.Errorf("the task worktree is not mounted at /srv/api: %s", got.Stderr)
	}
	if got := inside(t, environment, "cat", "/srv/store/read-only.txt"); !got.Succeeded() {
		t.Errorf("the read-only worktree is not mounted at /srv/store: %s", got.Stderr)
	}
	if got := inside(t, environment, "cat", "/feat/control.txt"); !got.Succeeded() {
		t.Errorf("the control workspace is not mounted at /feat: %s", got.Stderr)
	}

	// The control workspace is mounted the way its layout is split, and a
	// nested read-write mount inside a read-only one is the part of that no
	// generated YAML can prove: only the container runtime decides it.
	if got := inside(t, environment, "touch", "/feat/written-by-the-agent"); got.Succeeded() {
		t.Error("the control workspace is writable, so the agent owns the hooks it runs under " +
			"and the record of which of its messages have been applied")
	}
	for _, directory := range []string{"/feat/outbox", "/feat/reports"} {
		if got := inside(t, environment, "touch", directory+"/written-by-the-agent"); !got.Succeeded() {
			t.Errorf("%s is not writable, so the agent could not report anything: %s", directory, got.Stderr)
		}
	}
	if _, err := os.Stat(filepath.Join(source(t, spec, "/feat/outbox"), "written-by-the-agent")); err != nil {
		t.Errorf("what the container wrote to the outbox did not reach the host: %v", err)
	}
	if got := inside(t, environment, "cat", "/srv/api/base-mount.txt"); got.Succeeded() {
		t.Error("the base file's own mount is still at /srv/api, so the agent holds both it and the task worktree")
	}

	// Acceptance criterion 2 in its general form: a read-only selection is
	// mounted read-only, and the container cannot write through it.
	if got := inside(t, environment, "touch", "/srv/store/written-by-the-agent"); got.Succeeded() {
		t.Error("a read-only mount was writable from inside the container")
	}
	// While the writable one genuinely is writable, or the agent could not work.
	if got := inside(t, environment, "touch", "/srv/api/written-by-the-agent"); !got.Succeeded() {
		t.Errorf("the task worktree is not writable from inside the container: %s", got.Stderr)
	}
	if _, err := os.Stat(filepath.Join(worktree, "written-by-the-agent")); err != nil {
		t.Errorf("what the container wrote did not reach the host worktree: %v", err)
	}

	// Acceptance criterion 3: the agent's process is not root.
	uid := strings.TrimSpace(inside(t, environment, "id", "-u").Stdout)
	if uid == "0" {
		t.Error("the agent runs as root in the container")
	}
	if uid != spec.User {
		t.Errorf("the agent runs as uid %q, want the configured %s", uid, spec.User)
	}
}

// TestRealAReadOnlyMountIsObservedReadOnly pins the field the read-only check
// rests on.
//
// `docker inspect` reports RW per mount, and the whole of invariant 6's
// enforcement is the assumption that RW: false is what a read-only bind produces
// and RW: true is what a writable one produces. That is a statement about Docker
// rather than about Feat, so it belongs here rather than in a fake.
func TestRealAReadOnlyMountIsObservedReadOnly(t *testing.T) {
	realDocker(t)

	environment, _, _ := realTask(t, domain.NewTaskID())
	if err := environment.Prepare(context.Background()); err != nil {
		t.Fatalf("preparing the environment: %v", err)
	}
	state, err := environment.Observe(context.Background())
	if err != nil {
		t.Fatalf("observing the environment: %v", err)
	}
	mounts, err := environment.Mounts(context.Background(), state.Container)
	if err != nil {
		t.Fatalf("inspecting the mounts: %v", err)
	}

	writable := map[string]bool{}
	for _, mount := range mounts {
		writable[mount.Destination] = mount.Writable
	}
	for destination, want := range map[string]bool{
		"/srv/api":      true,
		"/srv/store":    false,
		"/feat":         false,
		"/feat/outbox":  true,
		"/feat/reports": true,
	} {
		got, found := writable[destination]
		if !found {
			t.Errorf("the container reports no mount at %s: %v", destination, mounts)
			continue
		}
		if got != want {
			t.Errorf("the container reports %s writable=%t, want %t", destination, got, want)
		}
	}
	if err := environment.CheckMounts(mounts); err != nil {
		t.Errorf("the container Feat generated was refused by its own check: %v", err)
	}
}

// TestRealAReadOnlyPathIsNeverQuietlyWritable is the question only Docker can
// answer: what a second spelling of a target actually produces.
//
// Compose merges a service's volumes by target, and the whole mount design rests
// on that replacement. What it does with a target that means the same path
// written differently is a property of the tool, and the three outcomes are all
// acceptable — the project's file may be refused outright, the two entries may
// fold into one read-only mount, or a second writable mount may appear and be
// refused by the check. What must not happen is the fourth: a path the task
// holds read-only, writable, in a container Feat then starts an agent in.
func TestRealAReadOnlyPathIsNeverQuietlyWritable(t *testing.T) {
	realDocker(t)

	environment, _, _ := realTaskFrom(t, domain.NewTaskID(), "devcontainer-second-target.yaml")
	if err := environment.Prepare(context.Background()); err != nil {
		t.Logf("the project's own file was refused rather than merged: %v", err)
		return
	}
	state, err := environment.Observe(context.Background())
	if err != nil {
		t.Fatalf("observing the environment: %v", err)
	}
	mounts, err := environment.Mounts(context.Background(), state.Container)
	if err != nil {
		t.Fatalf("inspecting the mounts: %v", err)
	}
	t.Logf("the two spellings produced %d mounts: %v", len(mounts), compose.Sources(mounts))

	refused := environment.CheckMounts(mounts) != nil
	written := inside(t, environment, "touch", "/srv/store/written-by-the-agent").Succeeded()
	if written && !refused {
		t.Errorf("the container can write %s, which this task holds read-only, and the check accepted it: %v",
			"/srv/store", compose.Sources(mounts))
	}
}

// TestRealTheAgentHasNoDockerAccess is acceptance criterion 4 against a real
// container.
//
// Both halves are checked where they are true rather than where they were
// configured: no Docker socket is mounted, and there is no client that could use
// one. The report and its judgement are the same pair a launch refuses over.
func TestRealTheAgentHasNoDockerAccess(t *testing.T) {
	realDocker(t)

	environment, _, _ := realTask(t, domain.NewTaskID())
	if err := environment.Prepare(context.Background()); err != nil {
		t.Fatalf("preparing the environment: %v", err)
	}

	if got := inside(t, environment, "ls", "/var/run/docker.sock"); got.Succeeded() {
		t.Error("a Docker socket is present in the agent's container")
	}

	report, err := environment.Inspect(context.Background(), []string{"/feat/outbox", "/feat/reports", "/srv/api"})
	if err != nil {
		t.Fatalf("inspecting the container: %v", err)
	}
	if len(report.DockerClients) > 0 {
		t.Errorf("the container has a client that speaks the Docker API: %v", report.DockerClients)
	}
	if len(report.DockerVariables) > 0 {
		t.Errorf("the container's environment points a client at a container daemon: %v", report.DockerVariables)
	}
	for _, mount := range report.Mounts {
		if strings.Contains(mount.Source, "docker.sock") || strings.Contains(mount.Destination, "docker.sock") {
			t.Errorf("the container mounts %s at %s", mount.Source, mount.Destination)
		}
	}
	if len(report.MissingTools) > 0 {
		t.Errorf("the container is missing %v, which the generated hooks need", report.MissingTools)
	}
	if len(report.Unwritable) > 0 {
		t.Errorf("the agent cannot write to %v", report.Unwritable)
	}
	if err := environment.Check(report); err != nil {
		t.Errorf("a container that meets every requirement was refused: %v", err)
	}
}

// TestRealAMountOfTheHomeDirectoryIsRefused is the rule that needs a real path
// to mean anything.
//
// Every other test of this rule states the home directory itself, which proves
// the comparison and not the thing the comparison is about: whether a container
// runtime, asked what it mounts, names the host's home directory in a form Feat
// recognises. Docker Desktop reports a bind source through its own file-sharing
// layer, so the answer is the runtime's rather than the specification's.
//
// It needs no daemon, no task, and no launch. The fixture's own teardown removes
// what it made, which is why this is the way to exercise the rule by hand.
func TestRealAMountOfTheHomeDirectoryIsRefused(t *testing.T) {
	realDocker(t)

	home, err := os.UserHomeDir()
	if err != nil {
		// Through the demand: this test's whole subject is the one forbidden host
		// path this machine really has, so a machine that cannot name it has not
		// run the proof.
		integrationtest.Unavailable(t, integrationtest.Docker,
			"this machine has no resolvable home directory to refuse a mount of: %v", err)
	}

	_, spec, _ := realTaskFrom(t, domain.NewTaskID(), "devcontainer-home-mount.yaml")
	// What the daemon supplies from the resolved layout, which no fixture can
	// stand in for: this machine's own home directory.
	spec.ForbiddenSources = append(spec.ForbiddenSources,
		execution.ForbiddenSource{Path: home, Kind: execution.ForbiddenHome})

	environment, err := compose.New(spec, compose.Options{ReadyTimeout: 90 * time.Second})
	if err != nil {
		t.Fatalf("building the environment: %v", err)
	}
	if err := environment.Prepare(context.Background()); err != nil {
		t.Fatalf("preparing the environment: %v", err)
	}
	state, err := environment.Observe(context.Background())
	if err != nil {
		t.Fatalf("observing the environment: %v", err)
	}
	mounts, err := environment.Mounts(context.Background(), state.Container)
	if err != nil {
		t.Fatalf("inspecting the mounts: %v", err)
	}
	t.Logf("the container mounts %v", compose.Sources(mounts))

	err = environment.CheckMounts(mounts)
	if err == nil {
		t.Fatal("a container mounting the whole home directory was accepted")
	}
	for _, expected := range []string{home, "does not allow home directory mounts", spec.Service} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("the refusal does not mention %q: %v", expected, err)
		}
	}

	// One problem, not five: the task's own worktrees and control workspace are
	// mounted through the same container and none of them is the home directory.
	if problems := strings.Count(err.Error(), "the container mounts"); problems != 1 {
		t.Errorf("the refusal names %d mounts, want 1: %v", problems, err)
	}
}

// TestRealADockerEndpointIsFoundInTheContainersOwnEnvironment is the other
// half of the Docker boundary against a real container.
//
// A daemon reached over the network leaves no mount to find and no executable
// to probe for, so the only evidence is the container's own environment. What
// `docker inspect` reports under .Config.Env, and whether it carries what the
// project wrote under `environment:`, is a fact about Docker rather than about
// Feat.
func TestRealADockerEndpointIsFoundInTheContainersOwnEnvironment(t *testing.T) {
	realDocker(t)

	environment, _, _ := realTaskFrom(t, domain.NewTaskID(), "devcontainer-docker-endpoint.yaml")
	if err := environment.Prepare(context.Background()); err != nil {
		t.Fatalf("preparing the environment: %v", err)
	}
	state, err := environment.Observe(context.Background())
	if err != nil {
		t.Fatalf("observing the environment: %v", err)
	}

	found, err := environment.Endpoints(context.Background(), state.Container)
	if err != nil {
		t.Fatalf("reading the container's environment: %v", err)
	}
	for _, want := range []string{"DOCKER_HOST", "DOCKER_TLS_VERIFY"} {
		if !slices.Contains(found, want) {
			t.Errorf("the container's environment sets %s and the check did not see it: %v", want, found)
		}
	}
	// The agent's own view of it, which is what the variable would actually
	// point a client at.
	if got := inside(t, environment, "printenv", "DOCKER_HOST"); !strings.Contains(got.Stdout, "198.51.100.7") {
		t.Errorf("the container does not have the endpoint the fixture set: %q", got.Stdout)
	}

	report, err := environment.Inspect(context.Background(), []string{"/srv/api"})
	if err != nil {
		t.Fatalf("inspecting the container: %v", err)
	}
	err = environment.Check(report)
	if err == nil {
		t.Fatal("a container pointed at a Docker daemon over the network was accepted")
	}
	if !strings.Contains(err.Error(), "DOCKER_HOST") {
		t.Errorf("the refusal does not name the entry to remove: %v", err)
	}
	// Names, not values: this message reaches the daemon log and the API.
	if strings.Contains(err.Error(), "198.51.100.7") {
		t.Errorf("the refusal repeats the value of an environment entry: %v", err)
	}
}

// TestRealThreeTasksRunSideBySide is acceptance criterion 6.
//
// The fixture carries a fixed container name and a published host port, both of
// which are global and both of which would make the second container fail to
// start. That is the whole reason the generated override resets them, and this
// is what proves the reset works against the tool rather than against a golden
// file (ADR-033 evidence 3).
//
// It carries them twice over: on the service Feat starts and on the one Compose
// starts because that service depends on it. The second is the defect F7-01
// names, and it fails here as a launch rather than as an assertion — the second
// task's `up` is refused by Docker over the first task's `db`.
func TestRealThreeTasksRunSideBySide(t *testing.T) {
	realDocker(t)

	const tasks = 3
	environments := make([]*compose.Environment, 0, tasks)
	identities := make([]string, 0, tasks)
	for range tasks {
		environment, spec, _ := realTask(t, domain.NewTaskID())
		if err := environment.Prepare(context.Background()); err != nil {
			t.Fatalf("preparing environment %d of %d: %v", len(environments)+1, tasks, err)
		}
		environments = append(environments, environment)
		identities = append(identities, spec.Identity)
	}

	seen := make(map[string]bool, tasks)
	for i, environment := range environments {
		state, err := environment.Observe(context.Background())
		if err != nil {
			t.Fatalf("observing environment %d: %v", i+1, err)
		}
		if !state.Running {
			t.Errorf("environment %d is not running: %s", i+1, state.Status)
		}
		if seen[state.Container] {
			t.Errorf("environment %d shares container %s with another task", i+1, state.Container)
		}
		seen[state.Container] = true

		// And each one holds its own task's files rather than another's.
		if got := inside(t, environment, "cat", "/srv/api/task-worktree.txt"); !got.Succeeded() {
			t.Errorf("environment %d does not have its own worktree: %s", i+1, got.Stderr)
		}

		// The service nothing in Feat names is this task's own too, rather than
		// one container three tasks are sharing.
		dependency := dependencyContainer(t, identities[i])
		if seen[dependency] {
			t.Errorf("environment %d shares the container %s of the service its devcontainer depends on",
				i+1, dependency)
		}
		seen[dependency] = true
	}
}

// TestRealTheOverrideRemovesWhatWouldCollide checks the two !reset entries
// directly, so that a failure says which one stopped working.
//
// A container name is global to the Docker daemon and a published port is global
// to the host. Either surviving the merge would make the second task's launch
// fail with a message about the first task's container.
//
// Both containers are checked, because the override reaches both and only one of
// them is a service Feat was asked about. A dependency that kept either is the
// same failure one service over, and it is the one that arrives as a Compose
// error about something the user did not know Feat was starting (F7-01).
func TestRealTheOverrideRemovesWhatWouldCollide(t *testing.T) {
	realDocker(t)

	environment, spec, _ := realTask(t, domain.NewTaskID())
	if err := environment.Prepare(context.Background()); err != nil {
		t.Fatalf("preparing the environment: %v", err)
	}

	state, err := environment.Observe(context.Background())
	if err != nil {
		t.Fatalf("observing: %v", err)
	}

	for _, container := range []struct {
		what, id, fixedName, port string
	}{
		{"the agent's own service", state.Container, "feat-test-fixed-name", "59317"},
		{"the service it depends on", dependencyContainer(t, spec.Identity),
			"feat-test-dependency-fixed-name", "59318"},
	} {
		name, err := exec.Command(compose.Executable, "inspect", "--format", "{{.Name}}", container.id).Output()
		if err != nil {
			t.Fatalf("inspecting the container of %s: %v", container.what, err)
		}
		if strings.Contains(string(name), container.fixedName) {
			t.Errorf("the container of %s kept the base file's container_name %q, so a second task "+
				"could not start", container.what, strings.TrimSpace(string(name)))
		}
		if !strings.Contains(string(name), spec.Identity) {
			t.Errorf("the container name %q of %s does not belong to this task's Compose project %q",
				strings.TrimSpace(string(name)), container.what, spec.Identity)
		}

		ports, err := exec.Command(compose.Executable, "inspect", "--format", "{{json .NetworkSettings.Ports}}",
			container.id).Output()
		if err != nil {
			t.Fatalf("inspecting the ports of the container of %s: %v", container.what, err)
		}
		if strings.Contains(string(ports), container.port) {
			t.Errorf("the container of %s kept the base file's published port: %s", container.what, ports)
		}
	}
}

// TestRealAStoppedEnvironmentKeepsItsContainerAndComesBack is the lifecycle a
// user drives, against the container runtime that decides it.
//
// The three claims a fake cannot answer: that `stop` leaves the container in
// place rather than removing it as `down` would, that what comes back after a
// second `up` is the same container rather than a new one, and that what the
// agent wrote inside it survives the round trip. The last is what makes a stop
// reversible in the sense a user means — a container that came back empty would
// be a launch wearing a resume's name (ADR-057).
func TestRealAStoppedEnvironmentKeepsItsContainerAndComesBack(t *testing.T) {
	realDocker(t)

	environment, _, _ := realTask(t, domain.NewTaskID())
	if err := environment.Prepare(context.Background()); err != nil {
		t.Fatalf("preparing the environment: %v", err)
	}

	started, err := environment.Observe(context.Background())
	if err != nil {
		t.Fatalf("observing the started environment: %v", err)
	}
	if !started.Running {
		t.Fatalf("the prepared environment is not running: %s", started.Status)
	}
	// Something inside the container's own filesystem, above any mount, so that
	// what survives is the container rather than the host directory under it.
	if got := inside(t, environment, "touch", "/tmp/written-before-the-stop"); !got.Succeeded() {
		t.Fatalf("writing inside the container: %s", got.Stderr)
	}

	stopped, err := environment.Stop(context.Background())
	if err != nil {
		t.Fatalf("stopping: %v", err)
	}
	if stopped.Running {
		t.Errorf("the environment is still running after a stop: %s", stopped.Status)
	}
	// Present rather than gone is the whole difference between this and cleanup.
	if !stopped.Present {
		t.Error("stopping removed the container; `stop` keeps it and `down` is cleanup's")
	}

	if err := environment.Prepare(context.Background()); err != nil {
		t.Fatalf("bringing the stopped environment back: %v", err)
	}
	resumed, err := environment.Observe(context.Background())
	if err != nil {
		t.Fatalf("observing the resumed environment: %v", err)
	}
	if !resumed.Running {
		t.Fatalf("the environment did not come back: %s", resumed.Status)
	}
	if resumed.Container != started.Container {
		t.Errorf("the container is %s after a stop and a start, want the same %s it was before",
			resumed.Container, started.Container)
	}
	if got := inside(t, environment, "cat", "/tmp/written-before-the-stop"); !got.Succeeded() {
		t.Errorf("what was written before the stop did not survive it: %s", got.Stderr)
	}
}

// TestRealAProjectIsFoundAndRemovedByNameAlone is the claim the whole by-name
// path rests on, asked of the tool rather than of a fake: `ps` and `down` given
// a project name and no Compose file operate on what Docker holds.
//
// It matters because the case it exists for cannot supply the files. A launch
// that failed after its container exists left a task with no record of what it
// created, and the file that would have to be read is often the one whose change
// made the launch slow enough to be interrupted in the first place.
//
// The container is stopped rather than running when it is looked for, which is
// the state the dogfood run found — `Exited (137)`, hours later, with its
// network beside it.
func TestRealAProjectIsFoundAndRemovedByNameAlone(t *testing.T) {
	realDocker(t)

	environment, spec, _ := realTask(t, domain.NewTaskID())
	if err := environment.Prepare(context.Background()); err != nil {
		t.Fatalf("preparing the environment: %v", err)
	}
	if _, err := environment.Stop(context.Background()); err != nil {
		t.Fatalf("stopping the environment: %v", err)
	}

	// From a directory of its own, as the daemon asks from one under Feat's
	// state root: what `ps` and `down` act on must not depend on where the
	// process that runs them happens to stand.
	project, err := compose.ByName(spec.Identity, t.TempDir(), compose.Options{})
	if err != nil {
		t.Fatalf("addressing Compose project %s by name: %v", spec.Identity, err)
	}

	remains, err := project.Remains(context.Background())
	if err != nil {
		t.Fatalf("asking what %s still has: %v", spec.Identity, err)
	}
	if remains.Containers().Empty() {
		t.Fatalf("a stopped container was not found by project name alone: %s", remains.Describe())
	}
	var networks bool
	for _, entry := range remains {
		if entry.Kind == compose.KindNetwork {
			networks = true
		}
	}
	if !networks {
		t.Errorf("the network Compose created was not found by project name alone: %s", remains.Describe())
	}

	if err := project.Destroy(context.Background()); err != nil {
		t.Fatalf("removing %s by name: %v", spec.Identity, err)
	}
	after, err := project.Remains(context.Background())
	if err != nil {
		t.Fatalf("asking what %s has after the removal: %v", spec.Identity, err)
	}
	if !after.Empty() {
		t.Errorf("removing by name left %s", after.Describe())
	}

	// And again over nothing: a removal of what is already absent is what a
	// user asked for, so it succeeds rather than reporting a project that is
	// not there.
	if err := project.Destroy(context.Background()); err != nil {
		t.Errorf("removing an empty project reported a failure: %v", err)
	}
}

// TestRealABindBackedVolumeIsSeenForWhatItIs is G7-01 against the runtime that
// decides it.
//
// The finding turns on a fact about Docker rather than about Feat: a `local`
// volume whose driver options bind a host path is reported with the volume's
// own mountpoint as its source, and never with the device. That is what makes
// the plain `- ${HOME}:/host-home` refused and the same thing through a volume
// accepted, and it is why removing the type filter would not have fixed it —
// there is nothing in the mount record to compare. It was measured by hand on
// 2026-08-19; this is where it stops being a measurement somebody remembers.
//
// The home directory is the device for the reason
// TestRealAMountOfTheHomeDirectoryIsRefused mounts it: it is the one forbidden
// path this machine really has. Both halves are checked — what Docker says,
// and what Feat does about it.
func TestRealABindBackedVolumeIsSeenForWhatItIs(t *testing.T) {
	realDocker(t)

	home, err := os.UserHomeDir()
	if err != nil {
		// Through the demand: this test's whole subject is the one forbidden host
		// path this machine really has, so a machine that cannot name it has not
		// run the proof.
		integrationtest.Unavailable(t, integrationtest.Docker,
			"this machine has no resolvable home directory to refuse a mount of: %v", err)
	}

	_, spec, _ := realTaskFrom(t, domain.NewTaskID(), "devcontainer-bind-backed-volume.yaml")
	// What the daemon supplies from the resolved layout, and what the fixture
	// interpolated into the volume's device: this machine's home directory.
	spec.ForbiddenSources = append(spec.ForbiddenSources,
		execution.ForbiddenSource{Path: home, Kind: execution.ForbiddenHome})

	environment, err := compose.New(spec, compose.Options{ReadyTimeout: 90 * time.Second})
	if err != nil {
		t.Fatalf("building the environment: %v", err)
	}

	// A named volume outlives `down`, which is what the product wants and what
	// makes this test's own leftovers its problem. The containers go first,
	// because a volume in use cannot be removed.
	t.Cleanup(func() {
		down := exec.Command(compose.Executable, "compose",
			"--project-name", spec.Identity, "--project-directory", spec.Directory,
			"--file", filepath.Join(spec.Directory, "docker-compose.yml"), "--file", spec.OverridePath,
			"down", "--timeout", "1")
		if output, err := down.CombinedOutput(); err != nil {
			t.Logf("stopping %s before removing its volumes: %v\n%s", spec.Identity, err, output)
		}
		volumes, err := environment.Volumes(context.Background())
		if err != nil {
			t.Logf("listing the volumes of %s: %v", spec.Identity, err)
			return
		}
		if _, err := environment.RemoveVolumes(context.Background(), volumes); err != nil {
			t.Logf("removing the volumes of %s: %v", spec.Identity, err)
		}
	})

	if err := environment.Prepare(context.Background()); err != nil {
		t.Fatalf("preparing the environment: %v", err)
	}
	state, err := environment.Observe(context.Background())
	if err != nil {
		t.Fatalf("observing the environment: %v", err)
	}
	mounts, err := environment.Mounts(context.Background(), state.Container)
	if err != nil {
		t.Fatalf("inspecting the mounts: %v", err)
	}
	t.Logf("the container mounts %v", compose.Sources(mounts))

	var probe execution.ObservedMount
	for _, mount := range mounts {
		if mount.Destination == "/mnt/probe" {
			probe = mount
		}
	}
	if probe.Destination == "" {
		t.Fatalf("the container has no mount at /mnt/probe: %v", compose.Sources(mounts))
	}

	// What Docker says. If any of this stops being true the fix is aimed at the
	// wrong field, and the refusal below would be passing for another reason.
	if probe.Type != "volume" {
		t.Errorf("Docker reports the bind-backed volume as %q, want \"volume\"", probe.Type)
	}
	if filepath.Clean(probe.Source) == filepath.Clean(home) {
		t.Errorf("Docker now reports the device %s as the mount's source, which the finding measured it "+
			"does not; the type filter alone would be enough and this fix is aimed at the wrong field", home)
	}
	if filepath.Clean(probe.Device) != filepath.Clean(home) {
		t.Errorf("the volume's device was read as %q, want %s: `docker volume inspect` is the only place "+
			"that path appears", probe.Device, home)
	}

	// And what Feat does about it: the same refusal the same path as a bind
	// earns one test up.
	err = environment.CheckMounts(mounts)
	if err == nil {
		t.Fatalf("a volume bound to the home directory was accepted, and the plain bind of it is refused: %v",
			compose.Sources(mounts))
	}
	for _, expected := range []string{home, "does not allow home directory mounts", "hostbind"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("the refusal does not mention %q: %v", expected, err)
		}
	}
}

// TestRealAContainerGrantedMoreThanItsMountsIsRefused is G4-04 against a real
// container.
//
// What `docker inspect` reports under .HostConfig, and whether it carries what
// the project wrote under `privileged:` and `cap_add:`, is a fact about Docker
// rather than about Feat — and it is the fact the residual guarantee of the
// read-only control workspace rests on, since that mount holds only while
// CAP_SYS_ADMIN is absent (G7-05).
func TestRealAContainerGrantedMoreThanItsMountsIsRefused(t *testing.T) {
	realDocker(t)

	environment, spec, _ := realTaskFrom(t, domain.NewTaskID(), "devcontainer-privileged.yaml")
	if err := environment.Prepare(context.Background()); err != nil {
		// A daemon that will not run a privileged container at all leaves nothing
		// to inspect, and the refusal did happen one layer down — but it happened
		// somewhere Feat does not control and cannot report on, and this is the
		// only real-Docker proof that Feat's own check reads .HostConfig and
		// refuses what it finds. Through the demand, so a run that asked for
		// Docker and got one which cannot arrange the subject says so instead of
		// printing "ok" (this skip was added one batch after the demand landed and
		// walked straight around it).
		integrationtest.Unavailable(t, integrationtest.Docker,
			"this machine's Docker would not start the privileged container this check inspects: %v", err)
	}
	state, err := environment.Observe(context.Background())
	if err != nil {
		t.Fatalf("observing the environment: %v", err)
	}

	privileges, err := environment.Privileges(context.Background(), state.Container)
	if err != nil {
		t.Fatalf("reading what the container was granted: %v", err)
	}
	if !privileges.Known {
		t.Fatal("the container's grants were reported unread")
	}
	if !privileges.Privileged {
		t.Errorf("the container was started privileged and Docker reports %+v", privileges)
	}
	if !slices.Contains(privileges.Capabilities, "SYS_ADMIN") {
		t.Errorf("the container was granted SYS_ADMIN and the check reads %v", privileges.Capabilities)
	}

	report, err := environment.Inspect(context.Background(), []string{"/srv/api"})
	if err != nil {
		t.Fatalf("inspecting the container: %v", err)
	}
	err = environment.Check(report)
	if err == nil {
		t.Fatal("a privileged container was accepted")
	}
	for _, expected := range []string{"runs privileged", "SYS_ADMIN", spec.Service} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("the refusal does not mention %q: %v", expected, err)
		}
	}
}
