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
	"time"

	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/execution"
	"github.com/ma8el/feat/internal/execution/compose"
	"github.com/ma8el/feat/internal/execution/compose/composetest"
)

var update = flag.Bool("update", false, "rewrite golden files")

const (
	task    = domain.TaskID("11111111-1111-4111-8111-111111111111")
	project = domain.ProjectID("app")
)

// arrange returns an environment over a fake Docker, with the override written
// into a temporary directory.
func arrange(t *testing.T, docker *composetest.Docker) (*compose.Environment, execution.Spec) {
	t.Helper()

	state := t.TempDir()
	spec := execution.Spec{
		Project:          project,
		Task:             task,
		Identity:         "feat-agent-app-" + string(task),
		Files:            []string{"/repos/app/infra/docker-compose.yml"},
		Directory:        "/repos/app/infra",
		OverridePath:     filepath.Join(state, "execution", "compose.override.yaml"),
		Service:          "dev",
		User:             "dev",
		WorkingDirectory: "/srv/api",
		Mounts: []execution.Mount{
			{Source: "/worktrees/api", Target: "/srv/api", Description: "the api task worktree"},
			{Source: "/worktrees/store", Target: "/srv/store", ReadOnly: true,
				Description: "the store task worktree, read-only"},
			{Source: filepath.Join(state, "control"), Target: "/feat", Description: "the control workspace"},
		},
		Volumes:          []execution.Volume{{Name: "feat-claude", Target: "/feat-claude"}},
		Variables:        map[string]string{"CLAUDE_CONFIG_DIR": "/feat-claude"},
		ForbiddenSources: []string{"/repos/app/api", "/repos/app/store"},
	}

	environment, err := compose.New(spec, compose.Options{Runner: docker, PollInterval: time.Nanosecond})
	if err != nil {
		t.Fatalf("building the environment: %v", err)
	}
	return environment, spec
}

// TestTheGeneratedOverrideIsPinned holds the document that decides what the
// agent's container mounts to a golden file.
//
// It is pinned rather than described because every line of it is a decision:
// which paths are writable, which are not, what is reset, and what is labelled.
// A change here should be visible in a diff rather than inferred from a passing
// test.
func TestTheGeneratedOverrideIsPinned(t *testing.T) {
	docker := composetest.New()
	environment, spec := arrange(t, docker)

	if err := environment.Prepare(context.Background()); err != nil {
		t.Fatalf("preparing: %v", err)
	}

	compare(t, spec, "override.golden")
}

// compare holds the generated override against a golden file.
//
// The temporary directory changes per run, so the two paths that carry it are
// normalised before comparison.
func compare(t *testing.T, spec execution.Spec, name string) string {
	t.Helper()

	written, err := os.ReadFile(spec.OverridePath)
	if err != nil {
		t.Fatalf("reading the generated override: %v", err)
	}
	normalised := strings.ReplaceAll(string(written), filepath.Dir(spec.OverridePath), "<state>")
	normalised = strings.ReplaceAll(normalised, filepath.Dir(filepath.Dir(spec.OverridePath)), "<state>")

	golden := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(golden, []byte(normalised), 0o600); err != nil {
			t.Fatalf("updating the golden file: %v", err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("reading the golden file: %v", err)
	}
	if normalised != string(want) {
		t.Errorf("the generated override changed\n got:\n%s\nwant:\n%s", normalised, want)
	}
	return normalised
}

// dependent arranges a project whose devcontainer service needs two others,
// which is the ordinary shape of one: a database to develop against and a cache
// the application expects to be there.
//
// Compose lists them in the order of the project's files, so the fixture answers
// out of alphabetical order: the generated document sorts them, because it is
// rewritten on every start and a document that reordered itself would show a
// diff nobody made.
func dependent(t *testing.T) (*compose.Environment, execution.Spec, *composetest.Docker) {
	t.Helper()

	docker := composetest.New().Answer("config --services", "db\ndev\ncache\n")
	environment, spec := arrange(t, docker)
	return environment, spec, docker
}

// TestTheGeneratedOverrideCoversEveryServiceInTheProject pins what Feat writes
// for a service the agent does not run in.
//
// `up --detach <service>` starts that service's whole depends_on closure, and
// every container it starts is in this task's Compose project. A base file's
// fixed container_name is global to the Docker daemon and its published port is
// global to the host, so leaving either on a dependency puts the project back to
// one task per machine — the thing the generated override exists to prevent,
// arriving one service over (F7-01, F3-22).
//
// What such a service gets is those two resets and nothing else: no worktree, no
// generated variable, and no ownership label, because the agent does not run in
// it and Feat's labels are how the container the agent does run in is found.
func TestTheGeneratedOverrideCoversEveryServiceInTheProject(t *testing.T) {
	environment, spec, _ := dependent(t)

	if err := environment.Prepare(context.Background()); err != nil {
		t.Fatalf("preparing: %v", err)
	}
	document := compare(t, spec, "override-dependencies.golden")

	// The properties the golden happens to show, said as themselves, so that a
	// deliberate update to the document cannot quietly drop one.
	for _, required := range []string{"container_name: !reset null", "ports: !reset []"} {
		if count := strings.Count(document, required); count != 3 {
			t.Errorf("%d services have %q, want 3: a dependency that keeps either is a second task "+
				"that cannot start\n%s", count, required, document)
		}
	}
	for _, service := range []string{`"cache"`, `"db"`} {
		for _, unwanted := range []string{"volumes:", "labels:", "environment:", "working_dir:"} {
			if strings.Contains(document, service+":\n    "+unwanted) {
				t.Errorf("the override gives %s a %s, which belongs to the service the agent runs in:\n%s",
					service, unwanted, document)
			}
		}
	}
}

// TestTheServicesAreReadWithoutTheGeneratedOverride pins how the question is
// asked.
//
// Two reasons, and each is a defect avoided rather than a preference. The
// override does not exist on a first launch, so passing it would make the first
// thing every task does fail with a Compose error about a file Feat generates;
// and a stale one would reintroduce a service the project has since removed,
// which is the ADR-034 evidence-11 shape. It reads names and nothing else, so no
// value from an environment file is rendered (ADR-028).
func TestTheServicesAreReadWithoutTheGeneratedOverride(t *testing.T) {
	environment, spec, docker := dependent(t)

	if err := environment.Prepare(context.Background()); err != nil {
		t.Fatalf("preparing: %v", err)
	}

	vector, found := docker.Vector("config --services")
	if !found {
		t.Fatalf("nothing asked which services the project defines; calls: %v", docker.Calls())
	}
	want := []string{
		"compose",
		"--project-name", spec.Identity,
		"--project-directory", spec.Directory,
		"--file", spec.Files[0],
		"config", "--services",
	}
	if !slices.Equal(vector, want) {
		t.Errorf("the Compose invocation changed\n got: %v\nwant: %v", vector, want)
	}
}

// TestAProjectWhoseServicesCannotBeReadStartsNothing keeps a launch that cannot
// establish isolation from proceeding without it.
//
// Feat cannot reset what it cannot enumerate, so a project it could not read is
// refused where the message can still name it rather than one container later,
// when the collision it would have prevented has already happened.
func TestAProjectWhoseServicesCannotBeReadStartsNothing(t *testing.T) {
	docker := composetest.New().
		Fail("config --services", "services.dev.depends_on contains an invalid type", 15)
	environment, spec := arrange(t, docker)

	err := environment.Prepare(context.Background())
	if err == nil {
		t.Fatal("a project whose services could not be read was started anyway")
	}
	for _, named := range []string{string(task), spec.Identity, "invalid type"} {
		if !strings.Contains(err.Error(), named) {
			t.Errorf("the message does not name %q: %v", named, err)
		}
	}
	if _, statErr := os.Stat(spec.OverridePath); statErr == nil {
		t.Error("a refused environment still wrote its override")
	}
	if docker.Ran("up --detach dev") {
		t.Error("a refused environment still started a service")
	}
}

// TestTheOverrideResetsWhatWouldPreventASecondTask is ADR-033 evidence 3 as a
// test.
//
// A container name is global to the Docker daemon and a published port is global
// to the host, so a base file carrying either can be brought up once. Acceptance
// criterion 6 is three tasks at once, and it is these two lines that make it
// possible.
func TestTheOverrideResetsWhatWouldPreventASecondTask(t *testing.T) {
	docker := composetest.New()
	environment, spec := arrange(t, docker)

	if err := environment.Prepare(context.Background()); err != nil {
		t.Fatalf("preparing: %v", err)
	}
	written, err := os.ReadFile(spec.OverridePath)
	if err != nil {
		t.Fatalf("reading the generated override: %v", err)
	}

	for _, required := range []string{"container_name: !reset null", "ports: !reset []"} {
		if !strings.Contains(string(written), required) {
			t.Errorf("the generated override does not contain %q, so a second task could not start", required)
		}
	}
}

// TestEveryComposeCommandNamesTheTasksOwnProject pins the flags that make an
// action affect one task and no other.
//
// A missing --project-name would act on whatever project the working directory
// implied, which for a devcontainer definition is the user's own manually
// started container.
func TestEveryComposeCommandNamesTheTasksOwnProject(t *testing.T) {
	docker := composetest.New()
	environment, spec := arrange(t, docker)

	if err := environment.Prepare(context.Background()); err != nil {
		t.Fatalf("preparing: %v", err)
	}
	if _, err := environment.Observe(context.Background()); err != nil {
		t.Fatalf("observing: %v", err)
	}

	vector, found := docker.Vector("up --detach dev")
	if !found {
		t.Fatalf("no service was brought up; calls: %v", docker.Calls())
	}

	want := []string{
		"compose",
		"--project-name", spec.Identity,
		"--project-directory", spec.Directory,
		"--file", spec.Files[0],
		"--file", spec.OverridePath,
		"up", "--detach", "dev",
	}
	if !slices.Equal(vector, want) {
		t.Errorf("the Compose invocation changed\n got: %v\nwant: %v", vector, want)
	}
}

// TestAnAgentCommandRunsAsTheConfiguredUser pins the exec vector.
//
// Three things in it are load-bearing: the user, because the security model
// requires a non-root agent; the working directory, because the agent must start
// in its own worktree; and the ordering, because a flag taking a list
// immediately before a positional argument swallows it, which is the defect
// ADR-032 evidence 12 records.
func TestAnAgentCommandRunsAsTheConfiguredUser(t *testing.T) {
	docker := composetest.New()
	environment, _ := arrange(t, docker)

	invocation, err := environment.Command(context.Background(), execution.Command{
		Program:     "claude",
		Arguments:   []string{"--settings", "/feat/agent/settings.json", "read the brief"},
		Interactive: true,
		Variables:   map[string]string{"FEAT_TASK": string(task)},
	})
	if err != nil {
		t.Fatalf("building the command: %v", err)
	}

	arguments := invocation.Arguments
	position := slices.Index(arguments, "exec")
	if position < 0 {
		t.Fatalf("the invocation does not exec: %v", arguments)
	}
	want := []string{
		"exec", "--user", "dev", "--workdir", "/srv/api",
		"--env", "FEAT_TASK=" + string(task),
		"dev", "claude", "--settings", "/feat/agent/settings.json", "read the brief",
	}
	if !slices.Equal(arguments[position:], want) {
		t.Errorf("the exec vector changed\n got: %v\nwant: %v", arguments[position:], want)
	}
	if !filepath.IsAbs(invocation.Program) {
		t.Errorf("the program %q is not absolute; a task terminal inherits the daemon's PATH", invocation.Program)
	}
}

// TestAnInteractiveCommandKeepsItsTerminal is ADR-033 evidence 7 at the level a
// unit test can reach.
//
// `docker compose exec` registers --no-TTY with a documented default of true. A
// probe says so explicitly; an agent session must not, or the native Claude
// interface would start with no terminal. Whether it genuinely gets one is
// proved by the real test in a real pane.
func TestAnInteractiveCommandKeepsItsTerminal(t *testing.T) {
	docker := composetest.New()
	environment, _ := arrange(t, docker)

	interactive, err := environment.Command(context.Background(),
		execution.Command{Program: "claude", Interactive: true})
	if err != nil {
		t.Fatalf("building the interactive command: %v", err)
	}
	if slices.Contains(interactive.Arguments, "--no-TTY") {
		t.Error("an agent session was given --no-TTY, which would start the native interface without a terminal")
	}

	probe, err := environment.Command(context.Background(), execution.Command{Program: "id"})
	if err != nil {
		t.Fatalf("building the probe: %v", err)
	}
	if !slices.Contains(probe.Arguments, "--no-TTY") {
		t.Error("a probe was not given --no-TTY, so its answer would depend on the daemon's own standard input")
	}
}

// TestAServiceThatDoesNotStayRunningIsExplained covers the devcontainer mistake
// most likely to be made once: a service whose command exits immediately.
//
// `up --detach` succeeds for such a service, and every later probe then fails
// with "container is not running", which describes the symptom and not the
// cause.
func TestAServiceThatDoesNotStayRunningIsExplained(t *testing.T) {
	docker := composetest.New().
		Answer("ps --all --format json dev",
			`{"ID":"c0ffee","Service":"dev","State":"exited","Status":"Exited (0) 1 second ago"}`)
	environment, _ := arrange(t, docker)

	err := environment.Prepare(context.Background())
	if err == nil {
		t.Fatal("a service that exited immediately was accepted")
	}
	for _, expected := range []string{"did not stay running", "sleep infinity", "logs"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("the message does not mention %q: %v", expected, err)
		}
	}
}

// TestAFailedStartNamesTheServiceAndTheProject checks that the message a user
// sees identifies what to look at.
func TestAFailedStartNamesTheServiceAndTheProject(t *testing.T) {
	docker := composetest.New().
		Fail("up --detach dev", "no such service: dev", 1)
	environment, spec := arrange(t, docker)

	err := environment.Prepare(context.Background())
	if err == nil {
		t.Fatal("a service that could not be started was accepted")
	}
	for _, expected := range []string{"dev", spec.Identity, "no such service"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("the message does not mention %q: %v", expected, err)
		}
	}
}

// TestObservationReportsWhatComposeSaid checks both output shapes Compose has
// used, because guessing wrong would report every running container as absent.
func TestObservationReportsWhatComposeSaid(t *testing.T) {
	for name, testCase := range map[string]struct {
		output  string
		running bool
		health  domain.HealthState
	}{
		"newline delimited": {
			output:  `{"ID":"c0ffee","Service":"dev","State":"running","Status":"Up 3 minutes"}`,
			running: true, health: domain.HealthUnknown,
		},
		"json array": {
			output:  `[{"ID":"c0ffee","Service":"dev","State":"running","Health":"healthy","Status":"Up 3 minutes"}]`,
			running: true, health: domain.HealthHealthy,
		},
		"several services": {
			output: `{"ID":"aaa","Service":"db","State":"running"}` + "\n" +
				`{"ID":"c0ffee","Service":"dev","State":"running","Status":"Up 1 minute"}`,
			running: true, health: domain.HealthUnknown,
		},
		"stopped": {
			output:  `{"ID":"c0ffee","Service":"dev","State":"exited","Status":"Exited (0)"}`,
			running: false, health: domain.HealthUnknown,
		},
		"nothing at all": {
			output: "", running: false, health: "",
		},
	} {
		t.Run(name, func(t *testing.T) {
			docker := composetest.New().Answer("ps --all --format json dev", testCase.output)
			environment, _ := arrange(t, docker)

			state, err := environment.Observe(context.Background())
			if err != nil {
				t.Fatalf("observing: %v", err)
			}
			if state.Running != testCase.running {
				t.Errorf("running is %t, want %t", state.Running, testCase.running)
			}
			if testCase.output != "" && state.Container != "c0ffee" && state.Present {
				t.Errorf("the observed container is %q, want %q", state.Container, "c0ffee")
			}
			if state.Health != testCase.health {
				t.Errorf("health is %q, want %q", state.Health, testCase.health)
			}
		})
	}
}

// TestObservationNeverStartsAnything is FR-STATE-004 at this layer: a stopped
// container is reported, not restarted.
func TestObservationNeverStartsAnything(t *testing.T) {
	docker := composetest.New().
		Answer("ps --all --format json dev", `{"ID":"c0ffee","Service":"dev","State":"exited"}`)
	environment, _ := arrange(t, docker)

	if _, err := environment.Observe(context.Background()); err != nil {
		t.Fatalf("observing: %v", err)
	}
	for _, call := range docker.Calls() {
		if strings.HasPrefix(call, "up") || strings.HasPrefix(call, "start") ||
			strings.HasPrefix(call, "restart") {
			t.Errorf("observing a stopped container ran %q", call)
		}
	}
}

// TestAnOldComposeIsRefusedBeforeAnythingIsCreated checks the version gate.
//
// Without !reset, the generated override is a YAML error rather than a mount
// policy, and Compose's own message says nothing about why Feat wrote it.
func TestAnOldComposeIsRefusedBeforeAnythingIsCreated(t *testing.T) {
	docker := composetest.New().Answer("version --short", "2.20.3")
	environment, spec := arrange(t, docker)

	err := environment.Validate(context.Background())
	if err == nil {
		t.Fatal("an unsupported Docker Compose was accepted")
	}
	if !strings.Contains(err.Error(), "2.24") {
		t.Errorf("the message does not name the version needed: %v", err)
	}
	if _, statErr := os.Stat(spec.OverridePath); statErr == nil {
		t.Error("a refused environment still wrote its override")
	}
	if docker.Ran("up --detach dev") {
		t.Error("a refused environment still started a service")
	}
}

// TestAnUnreadableComposeVersionDoesNotBlockALaunch keeps the version check
// from becoming an outage of its own: a tool that printed something unfamiliar
// is not evidence that it is too old.
func TestAnUnreadableComposeVersionDoesNotBlockALaunch(t *testing.T) {
	docker := composetest.New().Answer("version --short", "docker-desktop-edge")
	environment, _ := arrange(t, docker)

	if err := environment.Validate(context.Background()); err != nil {
		t.Fatalf("an unreadable version refused the launch: %v", err)
	}
}

// TestAMissingExecutableInTheContainerIsItsOwnError separates the two answers a
// provider CLI probe can give, because they are fixed in different ways.
func TestAMissingExecutableInTheContainerIsItsOwnError(t *testing.T) {
	docker := composetest.New().
		Fail("exec --no-TTY --user dev --workdir /srv/api dev glab auth status",
			`exec: "glab": executable file not found in $PATH`, 126)
	environment, _ := arrange(t, docker)

	_, err := environment.Run(context.Background(), execution.Command{
		Program: "glab", Arguments: []string{"auth", "status"},
	})
	if !errors.Is(err, compose.ErrNotInEnvironment) {
		t.Fatalf("a missing executable was not reported as absent: %v", err)
	}
}

// TestAFailingProbeIsAnAnswerRatherThanAnError keeps "glab said no" from being
// reported as "glab could not be run".
func TestAFailingProbeIsAnAnswerRatherThanAnError(t *testing.T) {
	docker := composetest.New().
		Fail("exec --no-TTY --user dev --workdir /srv/api dev glab auth status",
			"not logged in", 1)
	environment, _ := arrange(t, docker)

	output, err := environment.Run(context.Background(), execution.Command{
		Program: "glab", Arguments: []string{"auth", "status"},
	})
	if err != nil {
		t.Fatalf("a probe that ran and failed was reported as an error: %v", err)
	}
	if output.Succeeded() {
		t.Error("a failing probe reported success")
	}
}

// TestAnAbsentExecutableIsRecognisedOnEitherStream pins a provider behaviour
// that a plausible implementation gets wrong.
//
// Docker Compose reports "no such executable" on standard output rather than
// standard error. Reading only standard error looks correct, passes every test
// written against a fixture, and reports every absent tool as present — so a
// container with no mktemp would launch an agent Feat could never hear from, and
// a required provider CLI that is not installed would pass validation.
//
// It was found by running the real thing (ADR-033).
func TestAnAbsentExecutableIsRecognisedOnEitherStream(t *testing.T) {
	const message = `OCI runtime exec failed: exec failed: unable to start container process: ` +
		`exec: "mktemp": executable file not found in $PATH`

	for name, streams := range map[string]struct{ stdout, stderr string }{
		// What Docker Compose actually does, verified against the installed
		// version: the failure goes to standard output.
		"on standard output": {stdout: message},
		"on standard error":  {stderr: message},
	} {
		t.Run(name, func(t *testing.T) {
			docker := composetest.New().
				Reply(probeKey("mktemp", "-u"), streams.stdout, streams.stderr, 127)
			environment, _ := arrange(t, docker)

			_, err := environment.Run(context.Background(), execution.Command{
				Program: "mktemp", Arguments: []string{"-u"},
			})
			if !errors.Is(err, compose.ErrNotInEnvironment) {
				t.Fatalf("an absent executable reported %s was not recognised: %v", name, err)
			}
		})
	}
}

// TestAFileTheProgramCouldNotOpenIsNotAMissingProgram is the other half of the
// same distinction.
//
// A tool that ran and could not open a file also says "no such file or
// directory". Reading that as an absent executable would tell a user to install
// something they already have, and hide what actually went wrong.
func TestAFileTheProgramCouldNotOpenIsNotAMissingProgram(t *testing.T) {
	docker := composetest.New().
		Reply(probeKey("cat", "/srv/api/missing.txt"),
			"", "cat: can't open '/srv/api/missing.txt': No such file or directory", 1)
	environment, _ := arrange(t, docker)

	output, err := environment.Run(context.Background(), execution.Command{
		Program: "cat", Arguments: []string{"/srv/api/missing.txt"},
	})
	if errors.Is(err, compose.ErrNotInEnvironment) {
		t.Fatal("a file the program could not open was reported as a missing program")
	}
	if err != nil {
		t.Fatalf("a command that ran and failed was reported as an error: %v", err)
	}
	if output.Succeeded() {
		t.Error("a failing command reported success")
	}
}
