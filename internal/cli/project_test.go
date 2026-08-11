package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ma8el/feat/internal/daemon"
	"github.com/ma8el/feat/internal/paths"
	"github.com/ma8el/feat/internal/project"
)

// workingHost answers diagnostic commands as a machine with everything
// installed, so that a test of `feat doctor`'s output does not depend on which
// tools happen to be on the machine running it.
type workingHost struct{}

func (workingHost) Look(name string) (string, error) { return "/usr/bin/" + name, nil }

func (workingHost) Run(_ context.Context, _, name string, args ...string) (string, error) {
	line := strings.TrimSpace(name + " " + strings.Join(args, " "))
	switch {
	case line == "git --version":
		return "git version 2.51.0", nil
	case line == "tmux -V":
		return "tmux 3.5a", nil
	case line == "docker compose version":
		return "Docker Compose version v2.40.0", nil
	case strings.HasSuffix(line, "config --services"):
		return "dev\napp\nworker", nil
	default:
		// Every other command is a Git query about a repository, and answering
		// it as success is what a healthy checkout would do.
		return "", nil
	}
}

// projectFixture is a complete configuration. The repository names are generic:
// nothing about the reference project may reach the binary (CLAUDE.md scope
// rule 3).
const projectFixture = `version: 1

project:
  id: app
  name: Example Application
  primary_repository: api

repositories:
  api:
    host_path: ~/repos/app/api
    container_path: /srv/api
    default_access: read_write
  store:
    host_path: ~/repos/app/store
    container_path: /srv/store
    default_access: selectable

git:
  worktree_root: "~/.local/share/feat/worktrees/{project_id}/{task_id}"

agent:
  execution:
    mode: devcontainer
    compose_files:
      - ~/repos/app/compose.yml
    service: dev
    user: developer
    working_directory: /srv/api
    control_path: /feat
  capabilities:
    gitlab_cli: required

runtime:
  compose_files:
    - ~/repos/app/compose.yml
  env_files:
    - ~/repos/app/.env
  services:
    - app
`

// machine is a temporary home with a configuration directory and the files a
// configuration refers to.
type machine struct {
	layout paths.Layout
	env    paths.Environment
	home   string
}

// prepare builds an isolated machine for a command test.
func prepare(t *testing.T) *machine {
	t.Helper()

	home := t.TempDir()
	runtimeDir := shortDir(t)
	layout := paths.Layout{
		Config:  filepath.Join(home, ".config", "feat"),
		State:   filepath.Join(home, ".local", "share", "feat"),
		Runtime: runtimeDir,
		Socket:  filepath.Join(runtimeDir, "feat.sock"),
	}

	if err := os.MkdirAll(layout.ProjectConfigDir(), 0o700); err != nil {
		t.Fatalf("creating the configuration directory: %v", err)
	}
	for _, dir := range []string{"api", "store"} {
		if err := os.MkdirAll(filepath.Join(home, "repos", "app", dir), 0o700); err != nil {
			t.Fatalf("creating a repository directory: %v", err)
		}
	}
	for _, file := range []string{"compose.yml", ".env"} {
		if err := os.WriteFile(filepath.Join(home, "repos", "app", file), []byte("# fixture\n"), 0o600); err != nil {
			t.Fatalf("writing %s: %v", file, err)
		}
	}

	return &machine{
		layout: layout,
		home:   home,
		env: paths.Environment{
			Getenv: func(key string) string {
				if key == "EDITOR" {
					return "nvim"
				}
				return ""
			},
			Home: home,
			UID:  os.Getuid(),
			GOOS: "darwin",
		},
	}
}

// configure writes a project configuration onto the machine.
func (m *machine) configure(t *testing.T, id, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(m.layout.ProjectConfigDir(), id+".yaml"), []byte(body), 0o600); err != nil {
		t.Fatalf("writing the configuration: %v", err)
	}
}

// run executes one command against the machine and returns its exit code and
// streams.
func (m *machine) run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()

	var stdout, stderr bytes.Buffer
	code := execute(context.Background(), Options{
		Layout:      &m.layout,
		Environment: &m.env,
		Runner:      workingHost{},
	}, args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// serve starts a daemon on the machine and stops it when the test ends.
func (m *machine) serve(t *testing.T) {
	t.Helper()

	ready := make(chan daemon.Endpoint, 1)
	ctx, stop := context.WithCancel(context.Background())
	served := make(chan error, 1)

	go func() {
		served <- daemon.Run(ctx, daemon.Options{
			Layout:      m.layout,
			Environment: m.env,
			Build:       daemon.Build{Version: "v0.0.0-test"},
			Ready:       func(endpoint daemon.Endpoint) { ready <- endpoint },
		})
	}()

	select {
	case <-ready:
	case err := <-served:
		t.Fatalf("the daemon returned before listening: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("the daemon did not start listening")
	}

	t.Cleanup(func() {
		stop()
		select {
		case err := <-served:
			if err != nil {
				t.Errorf("the daemon failed: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("the daemon did not shut down")
		}
	})
}

// TestProjectAddRegistersThroughTheDaemon checks the whole path a registration
// takes: the command validates the file, the daemon reads and records it, and
// the record is visible to the next command.
func TestProjectAddRegistersThroughTheDaemon(t *testing.T) {
	m := prepare(t)
	m.configure(t, "app", projectFixture)
	m.serve(t)

	code, stdout, stderr := m.run(t, "project", "add", "app")
	if code != ExitOK {
		t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	for _, want := range []string{"registered app", "Example Application", "api", "store"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output does not mention %q:\n%s", want, stdout)
		}
	}

	code, stdout, stderr = m.run(t, "project", "list")
	if code != ExitOK {
		t.Fatalf("list exit code = %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "app") || !strings.Contains(stdout, "PRIMARY") {
		t.Errorf("the registered project is not listed:\n%s", stdout)
	}
	if strings.Contains(stdout, "not registered") {
		t.Errorf("a registered project is reported as unregistered:\n%s", stdout)
	}

	// Running it again updates rather than duplicating or failing.
	code, stdout, _ = m.run(t, "project", "add", "app")
	if code != ExitOK {
		t.Fatalf("re-registering exit code = %d\nstdout: %s", code, stdout)
	}
	if !strings.Contains(stdout, "updated app") {
		t.Errorf("re-registering did not report an update:\n%s", stdout)
	}
}

// TestProjectAddWithoutADaemonReportsIt checks that registration, which only
// the daemon may perform, says so rather than failing obscurely.
func TestProjectAddWithoutADaemonReportsIt(t *testing.T) {
	m := prepare(t)
	m.configure(t, "app", projectFixture)

	code, _, stderr := m.run(t, "project", "add", "app")

	if code != ExitNotRunning {
		t.Errorf("exit code = %d, want %d\nstderr: %s", code, ExitNotRunning, stderr)
	}
	if !strings.Contains(stderr, "feat daemon start") {
		t.Errorf("the error does not say how to start a daemon:\n%s", stderr)
	}
}

// TestProjectAddRejectsAnInvalidConfigurationBeforeTheDaemon checks that the
// command validates first, so the user gets the annotated message.
func TestProjectAddRejectsAnInvalidConfigurationBeforeTheDaemon(t *testing.T) {
	m := prepare(t)
	m.configure(t, "app", strings.Replace(projectFixture, "    user: developer", "    user: root", 1))
	m.serve(t)

	code, _, stderr := m.run(t, "project", "add", "app")

	if code != ExitError {
		t.Fatalf("exit code = %d, want %d\nstderr: %s", code, ExitError, stderr)
	}
	if !strings.Contains(stderr, "agent.execution.user") {
		t.Errorf("the error does not name the field:\n%s", stderr)
	}
	if !strings.Contains(stderr, "user: root") {
		t.Errorf("the error does not show the offending line:\n%s", stderr)
	}

	code, stdout, _ := m.run(t, "project", "list")
	if code != ExitOK {
		t.Fatalf("list exit code = %d", code)
	}
	if !strings.Contains(stdout, "not registered") {
		t.Errorf("a rejected configuration appears to have registered something:\n%s", stdout)
	}
}

// TestProjectListDistinguishesConfiguredFromRegistered checks that the two
// states stay separate: a file you wrote, and a project Feat knows about.
func TestProjectListDistinguishesConfiguredFromRegistered(t *testing.T) {
	m := prepare(t)
	m.configure(t, "app", projectFixture)
	m.serve(t)

	code, stdout, _ := m.run(t, "project", "list")
	if code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}
	if !strings.Contains(stdout, "configured but not registered: app") {
		t.Errorf("a configured project is not reported as pending:\n%s", stdout)
	}
}

// TestProjectShowPrintsResolvedConfiguration is the slice 3 acceptance
// criterion "Repository/container-path mappings are printed accurately", at the
// command that prints them.
func TestProjectShowPrintsResolvedConfiguration(t *testing.T) {
	m := prepare(t)
	m.configure(t, "app", projectFixture)

	code, stdout, stderr := m.run(t, "project", "show", "app")
	if code != ExitOK {
		t.Fatalf("exit code = %d\nstderr: %s", code, stderr)
	}

	for _, want := range []string{
		"CONTAINER PATH",
		filepath.Join(m.home, "repos", "app", "api") + " ",
		"/srv/api",
		filepath.Join(m.home, "repos", "app", "store") + " ",
		"/srv/store",
		"read_write",
		"selectable",
		// Defaults have to be visible: one you cannot see is one you cannot
		// check.
		"base_policy",
		"remote",
		"feat/{task_key}-{slug}",
		"capabilities.docker",
		"denied",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the resolved configuration does not contain %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "~/") {
		t.Errorf("the resolved configuration still contains an unexpanded path:\n%s", stdout)
	}
}

// TestDoctorReportsAHealthyMachine checks the clean case end to end.
func TestDoctorReportsAHealthyMachine(t *testing.T) {
	m := prepare(t)
	m.configure(t, "app", projectFixture)

	code, stdout, stderr := m.run(t, "doctor")
	if code != ExitOK {
		t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}

	for _, want := range []string{
		"host", "git version", "tmux", "Docker Compose",
		"project app", "is valid", "repositories", "/srv/api",
		// The mapping table is the acceptance criterion, so its header is
		// asserted rather than assumed from one path appearing.
		"REPOSITORY", "HOST PATH", "CONTAINER PATH", "DEFAULT ACCESS",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the report does not contain %q:\n%s", want, stdout)
		}
	}
	// The checks that cannot run must be named as skipped, not omitted and not
	// reported as passing.
	if !strings.Contains(stdout, "skipped") {
		t.Errorf("the report claims to have run every check:\n%s", stdout)
	}
	if !strings.Contains(stdout, "skipped checks are not passing checks") {
		t.Errorf("the report does not explain what skipped means:\n%s", stdout)
	}
}

// TestDoctorDescribesTheSkipsItActuallyProduces is F5-09.
//
// The long help said the agent-environment checks are skipped "because nothing
// starts that environment yet", and the summary said each skipped check "says
// which slice delivers it". Neither has been true since the checks moved inside
// a live container: what a skip names is the condition — no container of this
// project is running — and what it offers is launching a task. A user read the
// header saying the capability does not exist, the finding saying to start a
// task, and the footer sending them to look for a slice number no finding
// carries.
func TestDoctorDescribesTheSkipsItActuallyProduces(t *testing.T) {
	m := prepare(t)
	m.configure(t, "app", projectFixture)

	_, help, _ := m.run(t, "doctor", "--help")
	for _, stale := range []string{"nothing starts that environment", "slice"} {
		if strings.Contains(help, stale) {
			t.Errorf("`feat doctor --help` still says %q, which the checks stopped doing:\n%s", stale, help)
		}
	}
	// It has to say where the checks are asked, because that is what decides
	// whether they run at all.
	for _, want := range []string{"on this machine", "running container"} {
		if !strings.Contains(help, want) {
			t.Errorf("`feat doctor --help` does not say %q:\n%s", want, help)
		}
	}

	code, stdout, stderr := m.run(t, "doctor")
	if code != ExitOK {
		t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if strings.Contains(stdout, "which slice delivers it") {
		t.Errorf("the summary sends the reader to look for a slice number:\n%s", stdout)
	}
	if !strings.Contains(stdout, "each one says why it did not run") {
		t.Errorf("the summary does not say what a skipped finding carries:\n%s", stdout)
	}
	// And the findings have to carry it, or the summary is a claim of its own.
	if !strings.Contains(stdout, "no container of this project is running") {
		t.Errorf("no skipped finding names the condition the summary promises:\n%s", stdout)
	}
}

// TestReportColumnsSurviveLongPaths checks that the tables align against real
// data rather than only against short fixtures.
//
// The widest cell in these tables is a filesystem path, and a fixed column
// width lets one long path push every following column out of line. Both bugs
// this test covers were found by running `feat doctor` against real
// repositories, not by the fixtures above.
func TestReportColumnsSurviveLongPaths(t *testing.T) {
	m := prepare(t)
	// A repository whose path is far wider than any guessed column.
	long := filepath.Join(m.home, "repos", "app", strings.Repeat("deeply-nested-", 6)+"repository")
	if err := os.MkdirAll(long, 0o700); err != nil {
		t.Fatalf("creating the repository directory: %v", err)
	}
	m.configure(t, "app", strings.Replace(projectFixture,
		"    host_path: ~/repos/app/store",
		"    host_path: "+long, 1))

	for _, args := range [][]string{{"doctor"}, {"project", "show", "app"}} {
		code, stdout, stderr := m.run(t, args...)
		if code != ExitOK {
			t.Fatalf("%v exit code = %d\nstderr: %s", args, code, stderr)
		}

		// Every row of the mapping table must have its columns at the same
		// offsets, which is what makes the table readable at all.
		rows := tableRows(stdout, "REPOSITORY")
		if len(rows) < 2 {
			t.Fatalf("%v printed no mapping table:\n%s", args, stdout)
		}
		offsets := columnOffsets(rows[0])
		for _, row := range rows[1:] {
			if got := columnOffsets(row); !equalInts(got, offsets) {
				t.Errorf("%v: columns at %v do not line up with the header at %v:\n%s",
					args, got, offsets, strings.Join(rows, "\n"))
				break
			}
		}
		for _, row := range rows {
			if strings.HasSuffix(row, " ") {
				t.Errorf("%v: row %q ends in whitespace", args, row)
			}
		}
	}
}

// TestSummaryCountsReadAsEnglish covers the other formatting bug real data
// found: "6 skippeds" and "45 oks".
func TestSummaryCountsReadAsEnglish(t *testing.T) {
	m := prepare(t)
	m.configure(t, "app", projectFixture)

	_, stdout, _ := m.run(t, "doctor")

	for _, wrong := range []string{"skippeds", "oks", "1 warnings", "1 errors"} {
		if strings.Contains(stdout, wrong) {
			t.Errorf("the summary contains %q:\n%s", wrong, lastLines(stdout, 3))
		}
	}
	if !strings.Contains(stdout, " skipped, ") && !strings.Contains(stdout, " skipped\n") &&
		!strings.Contains(stdout, "and ") {
		t.Errorf("the summary does not read as a sentence:\n%s", lastLines(stdout, 3))
	}
}

// tableRows returns the header row starting with marker and the rows under it.
func tableRows(output, marker string) []string {
	var rows []string
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimLeft(line, " ")
		switch {
		case strings.HasPrefix(trimmed, marker):
			rows = []string{line}
		case len(rows) > 0:
			if strings.TrimSpace(line) == "" {
				return rows
			}
			rows = append(rows, line)
		}
	}
	return rows
}

// columnOffsets returns the index at which each column starts, taking two or
// more spaces as the separator.
func columnOffsets(row string) []int {
	offsets := []int{len(row) - len(strings.TrimLeft(row, " "))}
	for i := offsets[0]; i < len(row)-2; i++ {
		if row[i] == ' ' && row[i+1] == ' ' && row[i+2] != ' ' {
			offsets = append(offsets, i+2)
		}
	}
	return offsets
}

func equalInts(got, want []int) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func lastLines(output string, count int) string {
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	if len(lines) > count {
		lines = lines[len(lines)-count:]
	}
	return strings.Join(lines, "\n")
}

// TestDoctorFailsOnAnInvalidConfiguration is the slice 3 acceptance criterion
// that a broken configuration produces an actionable diagnostic, checked at the
// command and its exit code.
func TestDoctorFailsOnAnInvalidConfiguration(t *testing.T) {
	m := prepare(t)
	m.configure(t, "app", strings.Replace(projectFixture, "    default_access: read_write", "    default_acess: read_write", 1))

	code, stdout, stderr := m.run(t, "doctor")

	if code != ExitError {
		t.Fatalf("exit code = %d, want %d\nstdout: %s", code, ExitError, stdout)
	}
	if !strings.Contains(stdout, `unknown field "default_acess"`) {
		t.Errorf("the report does not name the unknown field:\n%s", stdout)
	}
	if !strings.Contains(stdout, "ERROR") {
		t.Errorf("the report does not mark the problem as an error:\n%s", stdout)
	}
	if !strings.Contains(stderr, "stop Feat from working") {
		t.Errorf("the exit code is not explained:\n%s", stderr)
	}
}

// TestDoctorReportsMissingHostTools checks that a machine without the tools
// Feat drives is diagnosed rather than merely failing later.
func TestDoctorReportsMissingHostTools(t *testing.T) {
	m := prepare(t)
	m.configure(t, "app", projectFixture)

	var stdout, stderr bytes.Buffer
	code := execute(context.Background(), Options{
		Layout:      &m.layout,
		Environment: &m.env,
		Runner:      emptyHost{},
	}, []string{"doctor"}, &stdout, &stderr)

	if code != ExitError {
		t.Fatalf("exit code = %d, want %d\nstdout: %s", code, ExitError, stdout.String())
	}
	for _, want := range []string{"git", "tmux", "install Git", "install tmux"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("the report does not mention %q:\n%s", want, stdout.String())
		}
	}
}

// emptyHost is a machine with none of the tools Feat drives.
type emptyHost struct{}

func (emptyHost) Look(name string) (string, error) {
	return "", &notInstalled{name: name}
}

func (emptyHost) Run(_ context.Context, _, name string, _ ...string) (string, error) {
	return "", &notInstalled{name: name}
}

type notInstalled struct{ name string }

func (e *notInstalled) Error() string { return e.name + " is not installed" }

func (e *notInstalled) Unwrap() error { return project.ErrNotInstalled }

// TestDoctorNeverPrintsSecretFileContents is the slice 3 acceptance criterion
// "Secret file contents never appear in diagnostics", checked at the command
// that prints them.
func TestDoctorNeverPrintsSecretFileContents(t *testing.T) {
	const secret = "ThisValueMustNeverBePrinted"

	m := prepare(t)
	m.configure(t, "app", projectFixture)

	envFile := filepath.Join(m.home, "repos", "app", ".env")
	if err := os.WriteFile(envFile, []byte("API_TOKEN="+secret+"\n"), 0o600); err != nil {
		t.Fatalf("writing the environment file: %v", err)
	}
	if os.Getuid() != 0 {
		// A command that opens the file cannot pass this by accident.
		if err := os.Chmod(envFile, 0o000); err != nil {
			t.Fatalf("making the environment file unreadable: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(envFile, 0o600) })
	}

	for _, args := range [][]string{{"doctor"}, {"project", "show", "app"}} {
		code, stdout, stderr := m.run(t, args...)
		if code != ExitOK {
			t.Fatalf("%v exit code = %d\nstdout: %s\nstderr: %s", args, code, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, secret) {
			t.Errorf("%v printed the environment file's contents:\n%s", args, stdout)
		}
		if !strings.Contains(stdout, envFile) {
			t.Errorf("%v does not name the environment file:\n%s", args, stdout)
		}
		if !strings.Contains(stdout, "contents not read") {
			t.Errorf("%v does not say the file is left unread:\n%s", args, stdout)
		}
	}
}

// TestDoctorChangesNothing checks that diagnostics are diagnostics: no daemon
// is started, and no state is written.
func TestDoctorChangesNothing(t *testing.T) {
	m := prepare(t)
	m.configure(t, "app", projectFixture)

	if code, stdout, stderr := m.run(t, "doctor"); code != ExitOK {
		t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}

	if daemon.Answering(m.layout.Socket) {
		t.Error("`feat doctor` started a daemon")
	}
	if entries, err := os.ReadDir(m.layout.State); err == nil && len(entries) > 0 {
		t.Errorf("`feat doctor` wrote state: %v", entries)
	}
}

// TestDoctorReportsRegistrationWhenADaemonIsRunning checks the finding that
// tells a user their valid configuration is not registered yet.
func TestDoctorReportsRegistrationWhenADaemonIsRunning(t *testing.T) {
	m := prepare(t)
	m.configure(t, "app", projectFixture)
	m.serve(t)

	code, stdout, _ := m.run(t, "doctor")
	if code != ExitOK {
		t.Fatalf("exit code = %d\nstdout: %s", code, stdout)
	}
	if !strings.Contains(stdout, "not registered with the daemon") {
		t.Errorf("the report does not mention registration:\n%s", stdout)
	}
	if !strings.Contains(stdout, "feat project add app") {
		t.Errorf("the report does not say how to register:\n%s", stdout)
	}

	if code, out, errs := m.run(t, "project", "add", "app"); code != ExitOK {
		t.Fatalf("registering: exit %d\n%s\n%s", code, out, errs)
	}

	code, stdout, _ = m.run(t, "doctor")
	if code != ExitOK {
		t.Fatalf("exit code = %d\nstdout: %s", code, stdout)
	}
	if !strings.Contains(stdout, "registered with the daemon") ||
		strings.Contains(stdout, "not registered with the daemon") {
		t.Errorf("the report does not reflect the registration:\n%s", stdout)
	}
}
