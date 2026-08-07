package project_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ma8el/feat/internal/agent/claude"
	"github.com/ma8el/feat/internal/config"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/execution/compose"
	"github.com/ma8el/feat/internal/paths"
	"github.com/ma8el/feat/internal/project"
)

// fakeRunner answers diagnostic commands from a script, so that a test can
// arrange a machine without Git, or a checkout without a remote, without
// changing the machine it runs on.
type fakeRunner struct {
	// missing are executables that are not installed.
	missing map[string]bool
	// failing are command lines, joined by spaces, that exit non-zero.
	failing map[string]bool
	// output maps a command line to what it prints.
	output map[string]string
	// calls records every command line, in order.
	calls []string
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{
		missing: map[string]bool{},
		failing: map[string]bool{},
		output: map[string]string{
			"git --version":          "git version 2.51.0",
			"tmux -V":                "tmux 3.5a",
			"docker compose version": "Docker Compose version v2.40.0",
		},
	}
}

func (f *fakeRunner) Look(name string) (string, error) {
	if f.missing[name] {
		return "", fmt.Errorf("%s is %w", name, project.ErrNotInstalled)
	}
	return "/usr/bin/" + name, nil
}

func (f *fakeRunner) Run(_ context.Context, _, name string, args ...string) (string, error) {
	line := strings.TrimSpace(name + " " + strings.Join(args, " "))
	f.calls = append(f.calls, line)

	if f.missing[name] {
		return "", fmt.Errorf("%s is %w", name, project.ErrNotInstalled)
	}
	if f.failing[line] {
		return "", fmt.Errorf("%s: exit status 1", name)
	}
	return f.output[line], nil
}

// world is a machine arranged for a diagnostic run.
type world struct {
	configDir string
	home      string
	runner    *fakeRunner
	opts      project.Options
}

// arrange builds a machine with a valid configuration and everything the
// configuration refers to, so that a test can break exactly one thing.
func arrange(t *testing.T) *world {
	t.Helper()

	home := t.TempDir()
	configDir := filepath.Join(home, ".config", "feat", "projects")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("creating the configuration directory: %v", err)
	}

	body, err := os.ReadFile(filepath.Join("testdata", "app.yaml"))
	if err != nil {
		t.Fatalf("reading the fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "app.yaml"), body, 0o600); err != nil {
		t.Fatalf("writing the configuration: %v", err)
	}

	for _, dir := range []string{"api", "web", "infra"} {
		if err := os.MkdirAll(filepath.Join(home, "repos", "app", dir), 0o700); err != nil {
			t.Fatalf("creating the repository directory: %v", err)
		}
	}
	for _, file := range []string{
		filepath.Join(home, "repos", "app", "infra", "docker-compose.yml"),
		filepath.Join(home, "repos", "app", "api", "docker-compose.yml"),
		filepath.Join(home, "repos", "app", "api", ".env"),
	} {
		if err := os.WriteFile(file, []byte("# fixture\n"), 0o600); err != nil {
			t.Fatalf("writing %s: %v", file, err)
		}
	}

	runner := newFakeRunner()
	runner.output["docker compose --file "+
		filepath.Join(home, "repos", "app", "infra", "docker-compose.yml")+
		" config --services"] = "dev\n"
	runner.output["docker compose --file "+
		filepath.Join(home, "repos", "app", "api", "docker-compose.yml")+
		" config --services"] = "app\nworker\n"

	env := paths.Environment{
		Getenv: func(key string) string {
			if key == "EDITOR" {
				return "nvim"
			}
			return ""
		},
		Home: home,
		UID:  501,
		GOOS: "darwin",
	}

	return &world{
		configDir: configDir,
		home:      home,
		runner:    runner,
		opts: project.Options{
			ConfigDir: configDir,
			Resolve: config.Options{
				Env:      env,
				StateDir: filepath.Join(home, ".local", "share", "feat"),
			},
			Runner: runner,
		},
	}
}

// diagnose runs diagnostics against the arranged machine.
func (w *world) diagnose(t *testing.T) project.Report {
	t.Helper()
	report, err := project.Diagnose(context.Background(), w.opts)
	if err != nil {
		t.Fatalf("diagnosing: %v", err)
	}
	return report
}

// only returns the single project report.
func (w *world) only(t *testing.T, report project.Report) project.Diagnosis {
	t.Helper()
	if len(report.Projects) != 1 {
		t.Fatalf("diagnosed %d projects, want 1", len(report.Projects))
	}
	return report.Projects[0]
}

// finding returns the finding recorded for a check.
func finding(t *testing.T, findings []project.Finding, check string) project.Finding {
	t.Helper()
	for _, candidate := range findings {
		if candidate.Check == check {
			return candidate
		}
	}
	t.Fatalf("no finding for %q; got %s", check, render(findings))
	return project.Finding{}
}

func render(findings []project.Finding) string {
	var out strings.Builder
	for _, f := range findings {
		fmt.Fprintf(&out, "\n  %-8s %-40s %s", f.Severity, f.Check, f.Summary)
		if f.Action != "" {
			out.WriteString(" -> " + f.Action)
		}
	}
	return out.String()
}

// TestHealthyProjectPassesEveryCheckItCanRun checks the baseline: a correct
// configuration on a correct machine produces no errors.
func TestHealthyProjectPassesEveryCheckItCanRun(t *testing.T) {
	w := arrange(t)
	report := w.diagnose(t)

	if report.Failed() {
		t.Errorf("a healthy project reported errors:%s%s", render(report.Host), render(w.only(t, report).Findings))
	}
	project := w.only(t, report)
	if project.Config == nil {
		t.Fatal("a healthy project reported no configuration")
	}
	if got := finding(t, project.Findings, "configuration").Severity; got != "ok" {
		t.Errorf("configuration finding is %q, want ok", got)
	}
}

// TestMissingToolsProduceActionableDiagnostics is part of the slice 3
// acceptance criterion "Missing tools/files/services produce actionable
// diagnostics".
//
// Actionable is asserted rather than assumed: every error and warning must say
// what to do, not only what is wrong.
func TestMissingToolsProduceActionableDiagnostics(t *testing.T) {
	for _, tool := range []struct {
		executable string
		check      string
	}{
		{"git", "git"},
		{"tmux", "tmux"},
		{"docker", "docker compose"},
	} {
		t.Run(tool.executable, func(t *testing.T) {
			w := arrange(t)
			w.runner.missing[tool.executable] = true

			report := w.diagnose(t)
			found := finding(t, report.Host, tool.check)

			if found.Severity != project.SeverityError {
				t.Errorf("%s missing is %q, want error:%s", tool.executable, found.Severity, render(report.Host))
			}
			if !strings.Contains(found.Summary, tool.executable) {
				t.Errorf("summary %q does not name the missing tool", found.Summary)
			}
			if found.Action == "" {
				t.Errorf("finding for %q says what is wrong but not what to do", tool.check)
			}
		})
	}
}

// TestDockerIsOnlyRequiredWhenAProjectUsesIt keeps a diagnostic from demanding
// a tool the configured projects never reach for.
func TestDockerIsOnlyRequiredWhenAProjectUsesIt(t *testing.T) {
	w := arrange(t)
	w.runner.missing["docker"] = true

	body := `version: 1
project:
  id: app
  primary_repository: api
repositories:
  api:
    host_path: ~/repos/app/api
    default_access: read_write
agent:
  execution:
    mode: host
`
	if err := os.WriteFile(filepath.Join(w.configDir, "app.yaml"), []byte(body), 0o600); err != nil {
		t.Fatalf("writing the configuration: %v", err)
	}

	report := w.diagnose(t)
	found := finding(t, report.Host, "docker compose")
	if found.Severity != project.SeverityWarning {
		t.Errorf("docker missing is %q for a host-native project, want warning:%s", found.Severity, render(report.Host))
	}
	if report.Failed() {
		t.Error("a host-native project without Docker reported an error")
	}
}

// TestMissingFilesAndServicesProduceActionableDiagnostics is the rest of the
// "missing tools/files/services" criterion.
func TestMissingFilesAndServicesProduceActionableDiagnostics(t *testing.T) {
	for name, testCase := range map[string]struct {
		arrange  func(t *testing.T, w *world)
		check    string
		severity project.Severity
		contains string
	}{
		"repository is not there": {
			arrange: func(t *testing.T, w *world) {
				t.Helper()
				if err := os.RemoveAll(filepath.Join(w.home, "repos", "app", "web")); err != nil {
					t.Fatalf("removing the repository: %v", err)
				}
			},
			check: "repositories.web", severity: project.SeverityError, contains: "does not exist",
		},
		"repository is not a git repository": {
			arrange: func(_ *testing.T, w *world) {
				w.runner.failing["git rev-parse --git-dir"] = true
			},
			check: "repositories.api", severity: project.SeverityError, contains: "not a Git repository",
		},
		"remote is missing": {
			arrange: func(_ *testing.T, w *world) {
				w.runner.failing["git remote get-url origin"] = true
			},
			check: "repositories.api.remote", severity: project.SeverityError, contains: "no remote",
		},
		"default branch is missing": {
			arrange: func(_ *testing.T, w *world) {
				w.runner.failing["git rev-parse --verify --quiet refs/remotes/origin/main"] = true
			},
			check: "repositories.api.default_branch", severity: project.SeverityWarning, contains: "has no refs/remotes",
		},
		"compose file is missing": {
			arrange: func(t *testing.T, w *world) {
				t.Helper()
				if err := os.Remove(filepath.Join(w.home, "repos", "app", "infra", "docker-compose.yml")); err != nil {
					t.Fatalf("removing the Compose file: %v", err)
				}
			},
			check: "agent.execution.compose_files[0]", severity: project.SeverityError, contains: "cannot be read",
		},
		"compose service is missing": {
			arrange: func(_ *testing.T, w *world) {
				for line := range w.runner.output {
					if strings.Contains(line, "infra") {
						w.runner.output[line] = "other\n"
					}
				}
			},
			check: "agent.execution.service", severity: project.SeverityError, contains: `no service "dev"`,
		},
		"environment file is missing": {
			arrange: func(t *testing.T, w *world) {
				t.Helper()
				if err := os.Remove(filepath.Join(w.home, "repos", "app", "api", ".env")); err != nil {
					t.Fatalf("removing the environment file: %v", err)
				}
			},
			check: "runtime.env_files[0]", severity: project.SeverityWarning, contains: "cannot be read",
		},
		"editor is not installed": {
			arrange: func(_ *testing.T, w *world) {
				w.runner.missing["nvim"] = true
			},
			check: "review.editor.command", severity: project.SeverityWarning, contains: "nvim",
		},
	} {
		t.Run(name, func(t *testing.T) {
			w := arrange(t)
			testCase.arrange(t, w)

			report := w.diagnose(t)
			found := finding(t, w.only(t, report).Findings, testCase.check)

			if found.Severity != testCase.severity {
				t.Errorf("%s is %q, want %q", testCase.check, found.Severity, testCase.severity)
			}
			if !strings.Contains(found.Summary, testCase.contains) {
				t.Errorf("summary %q does not explain %q", found.Summary, testCase.contains)
			}
			if found.Action == "" {
				t.Errorf("finding for %q says what is wrong but not what to do", testCase.check)
			}
		})
	}
}

// TestEveryProblemNamesAnAction states the rule the cases above check one at a
// time, over a whole report, so that a check added later cannot forget it.
func TestEveryProblemNamesAnAction(t *testing.T) {
	w := arrange(t)
	w.runner.missing["git"] = true
	w.runner.failing["git remote get-url origin"] = true
	if err := os.RemoveAll(filepath.Join(w.home, "repos", "app", "web")); err != nil {
		t.Fatalf("removing a repository: %v", err)
	}

	report := w.diagnose(t)
	all := append(append([]project.Finding{}, report.Host...), w.only(t, report).Findings...)

	for _, found := range all {
		if found.Severity == project.SeverityOK {
			continue
		}
		if found.Action == "" && found.Severity != project.SeveritySkipped {
			t.Errorf("%s finding for %q has no action: %q", found.Severity, found.Check, found.Summary)
		}
		if found.Summary == "" {
			t.Errorf("finding for %q has no summary", found.Check)
		}
	}
}

// TestInvalidConfigurationIsReportedWithItsLocation checks that `feat doctor`
// carries the configuration error through rather than flattening it.
func TestInvalidConfigurationIsReportedWithItsLocation(t *testing.T) {
	w := arrange(t)
	body, err := os.ReadFile(filepath.Join(w.configDir, "app.yaml"))
	if err != nil {
		t.Fatalf("reading the configuration: %v", err)
	}
	broken := strings.Replace(string(body), "    user: developer", "    user: root", 1)
	if err := os.WriteFile(filepath.Join(w.configDir, "app.yaml"), []byte(broken), 0o600); err != nil {
		t.Fatalf("writing the configuration: %v", err)
	}

	report := w.diagnose(t)
	found := finding(t, w.only(t, report).Findings, "configuration")

	if found.Severity != project.SeverityError {
		t.Errorf("an invalid configuration is %q, want error", found.Severity)
	}
	if !strings.Contains(found.Summary, "agent.execution.user") {
		t.Errorf("summary does not name the field:\n%s", found.Summary)
	}
	if !strings.Contains(found.Summary, "user: root") {
		t.Errorf("summary does not show the offending line:\n%s", found.Summary)
	}
	if !report.Failed() {
		t.Error("an invalid configuration did not fail the report")
	}
}

// TestUncheckableChecksAreSkippedRatherThanPassed keeps `feat doctor` honest
// about its own coverage.
//
// FR-PROJ-004 asks for checks inside the agent's execution environment. For a
// devcontainer project with no task running there is no container to look
// inside, and `feat doctor` will not start one to answer a question — a command
// that reports on a machine should not change it. Reporting them as passing
// would be worse than not reporting them at all.
func TestUncheckableChecksAreSkippedRatherThanPassed(t *testing.T) {
	w := arrange(t)
	report := w.diagnose(t)
	findings := w.only(t, report).Findings

	// These need a container, and each says so and says what to do about it.
	for _, check := range []string{
		"agent.executable",
		"agent.execution.user",
		"agent.capabilities.gitlab_cli",
	} {
		found := finding(t, findings, check)
		if found.Severity != project.SeveritySkipped {
			t.Errorf("%s is %q, want skipped", check, found.Severity)
		}
		if !strings.Contains(found.Summary, "no container of this project is running") {
			t.Errorf("%s does not say why it was skipped: %q", check, found.Summary)
		}
		if !strings.Contains(found.Action, "launch a task") {
			t.Errorf("%s does not say what would let it run: %q", check, found.Action)
		}
	}

	// A configured check that the gate runs in the agent's environment is
	// skipped for the same reason as the rest: from slice 11 on the gate exists,
	// so what decides whether the check can be looked up is whether there is a
	// container to look inside.
	gate := finding(t, findings, "checks.api.test")
	if gate.Severity != project.SeveritySkipped {
		t.Errorf("checks.api.test is %q, want skipped", gate.Severity)
	}
	if !strings.Contains(gate.Summary, "no container of this project is running") {
		t.Errorf("checks.api.test does not say why it was skipped: %q", gate.Summary)
	}
	if !strings.Contains(gate.Action, "launch a task") {
		t.Errorf("checks.api.test does not say what would let it run: %q", gate.Action)
	}

	// An optional provider CLI is skipped for the same reason as the rest: this
	// build cannot look inside the environment where the agent would run it.
	if got := finding(t, findings, "agent.capabilities.github_cli").Severity; got != project.SeveritySkipped {
		t.Errorf("an optional provider CLI is %q, want skipped", got)
	}

	// A capability that is switched off is genuinely checked: there is nothing
	// to validate, so reporting it as skipped would be its own kind of lie.
	disabled := arrange(t)
	rewrite(t, disabled, "    github_cli: optional", "    github_cli: disabled")
	if got := finding(t, disabled.only(t, disabled.diagnose(t)).Findings,
		"agent.capabilities.github_cli").Severity; got != project.SeverityOK {
		t.Errorf("a disabled provider CLI is %q, want ok", got)
	}
}

// rewrite edits the arranged configuration in place.
// hostMode rewrites the fixture to run its agent on the host, which is the
// environment this build can actually look inside.
func hostMode(t *testing.T, w *world) {
	t.Helper()

	rewrite(t, w, `    mode: devcontainer
    compose_files:
      - ~/repos/app/infra/docker-compose.yml
    service: dev
    user: developer
    working_directory: /srv/api
    control_path: /feat`, "    mode: host")
	// A Claude configuration volume needs a container to be mounted into, so a
	// host-mode project that declared one is rejected rather than quietly given
	// the user's own ~/.claude (ADR-033).
	rewrite(t, w, "    config_volume: example-claude-config\n", "")
	w.runner.output["claude --version"] = claude.Verified() + " (Claude Code)"
	w.runner.output["gh auth status"] = "Logged in"
	w.runner.output["glab auth status"] = "Logged in"
}

// TestHostModeChecksTheEnvironmentTheAgentWillRunIn is the slice 7 half of
// FR-PROJ-004.
//
// The requirement is worded around the environment where the agent runs, so a
// host-mode project can be checked now and a devcontainer one cannot. What was
// skipped before must genuinely run here, or the wording would be satisfied by
// a check that never looks at anything.
func TestHostModeChecksTheEnvironmentTheAgentWillRunIn(t *testing.T) {
	w := arrange(t)
	hostMode(t, w)
	findings := w.only(t, w.diagnose(t)).Findings

	for _, check := range []string{
		"agent.executable",
		"agent.capabilities.github_cli",
		"agent.capabilities.gitlab_cli",
	} {
		found := finding(t, findings, check)
		if found.Severity == project.SeveritySkipped {
			t.Errorf("%s is still skipped for a host-mode project, where the environment is this machine", check)
		}
		if found.Severity != project.SeverityOK {
			t.Errorf("%s is %q on a machine that has everything: %s", check, found.Severity, found.Summary)
		}
	}

	// The container user is not a question a host-mode project asks at all, so
	// reporting it either way would be inventing a check.
	for _, found := range findings {
		if found.Check == "agent.execution.user" {
			t.Errorf("a host-mode project reported %s, which only a container has", found.Check)
		}
	}
}

func TestAMissingRequiredProviderCLIFailsDoctorAndAnOptionalOneWarns(t *testing.T) {
	w := arrange(t)
	hostMode(t, w)
	w.runner.missing["glab"] = true
	w.runner.missing["gh"] = true

	findings := w.only(t, w.diagnose(t)).Findings

	// The fixture makes glab required and gh optional, and the difference has to
	// show: one stops a launch and the other does not.
	required := finding(t, findings, "agent.capabilities.gitlab_cli")
	if required.Severity != project.SeverityError {
		t.Errorf("a missing required CLI is %q, want an error", required.Severity)
	}
	if !strings.Contains(required.Action, "install glab") {
		t.Errorf("action = %q, want it to name the remedy", required.Action)
	}

	optional := finding(t, findings, "agent.capabilities.github_cli")
	if optional.Severity != project.SeverityWarning {
		t.Errorf("a missing optional CLI is %q, want a warning", optional.Severity)
	}
}

func TestAnUnauthenticatedProviderCLIIsDistinguishedFromAnAbsentOne(t *testing.T) {
	w := arrange(t)
	hostMode(t, w)
	w.runner.failing["glab auth status"] = true

	found := finding(t, w.only(t, w.diagnose(t)).Findings, "agent.capabilities.gitlab_cli")
	if found.Severity != project.SeverityError {
		t.Errorf("an unauthenticated required CLI is %q, want an error", found.Severity)
	}
	// Installed and unauthenticated are different states with different
	// remedies, and a diagnostic that conflated them would send the user to
	// install something they already have.
	if !strings.Contains(found.Summary, "not authenticated") {
		t.Errorf("summary = %q, want it to say the tool is unauthenticated", found.Summary)
	}
	if !strings.Contains(found.Action, "glab auth login") {
		t.Errorf("action = %q, want it to name the login command", found.Action)
	}
}

func TestAnUnverifiedAgentVersionWarnsRatherThanFails(t *testing.T) {
	w := arrange(t)
	hostMode(t, w)
	w.runner.output["claude --version"] = "99.0.1 (Claude Code)"

	found := finding(t, w.only(t, w.diagnose(t)).Findings, "agent.executable")
	if found.Severity != project.SeverityWarning {
		t.Errorf("an unverified agent version is %q, want a warning: a new release is not an outage",
			found.Severity)
	}
	if !strings.Contains(found.Summary, claude.Verified()) {
		t.Errorf("summary = %q, want it to name the version this build was verified against", found.Summary)
	}
}

func TestAMissingAgentExecutableFailsDoctor(t *testing.T) {
	w := arrange(t)
	hostMode(t, w)
	w.runner.missing["claude"] = true

	found := finding(t, w.only(t, w.diagnose(t)).Findings, "agent.executable")
	if found.Severity != project.SeverityError {
		t.Errorf("a missing agent executable is %q, want an error", found.Severity)
	}
	if !strings.Contains(found.Action, "install") {
		t.Errorf("action = %q, want it to name the remedy", found.Action)
	}
}

func rewrite(t *testing.T, w *world, old, replacement string) {
	t.Helper()
	file := filepath.Join(w.configDir, "app.yaml")
	body, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("reading the configuration: %v", err)
	}
	edited := strings.Replace(string(body), old, replacement, 1)
	if edited == string(body) {
		t.Fatalf("the fixture no longer contains %q", old)
	}
	if err := os.WriteFile(file, []byte(edited), 0o600); err != nil {
		t.Fatalf("writing the configuration: %v", err)
	}
}

// TestSecretFileContentsNeverAppearInDiagnostics is the slice 3 acceptance
// criterion "Secret file contents never appear in diagnostics".
//
// The environment file is made unreadable, so a diagnostic that opened it could
// not pass by accident, and its contents are searched for across the whole
// report.
func TestSecretFileContentsNeverAppearInDiagnostics(t *testing.T) {
	const secret = "ThisValueMustNeverBePrinted"

	w := arrange(t)
	envFile := filepath.Join(w.home, "repos", "app", "api", ".env")
	if err := os.WriteFile(envFile, []byte("API_TOKEN="+secret+"\n"), 0o600); err != nil {
		t.Fatalf("writing the environment file: %v", err)
	}
	if os.Getuid() != 0 {
		if err := os.Chmod(envFile, 0o000); err != nil {
			t.Fatalf("making the environment file unreadable: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(envFile, 0o600) })
	}

	report := w.diagnose(t)
	all := render(report.Host) + render(w.only(t, report).Findings)

	if strings.Contains(all, secret) {
		t.Errorf("diagnostics contain the environment file's contents:%s", all)
	}
	if !strings.Contains(all, envFile) {
		t.Errorf("diagnostics do not name the environment file:%s", all)
	}
	if !strings.Contains(all, "contents not read") {
		t.Errorf("diagnostics do not say the file is left unread:%s", all)
	}

	// docker compose config, without --services, renders the resolved project
	// including values taken from environment files. It must never be run.
	for _, call := range w.runner.calls {
		if strings.Contains(call, "config") && !strings.Contains(call, "--services") {
			t.Errorf("diagnostics ran %q, which resolves environment values", call)
		}
	}
}

// TestOnlyServiceNamesAreReadFromCompose pins the command diagnostics may use,
// so that a later change cannot quietly start rendering resolved Compose files.
func TestOnlyServiceNamesAreReadFromCompose(t *testing.T) {
	w := arrange(t)
	w.diagnose(t)

	var composeCalls int
	for _, call := range w.runner.calls {
		if !strings.HasPrefix(call, "docker compose") || !strings.Contains(call, " config") {
			continue
		}
		composeCalls++
		if !strings.HasSuffix(call, "config --services") {
			t.Errorf("diagnostics ran %q; only `config --services` is allowed", call)
		}
	}
	if composeCalls == 0 {
		t.Error("diagnostics never asked Compose for its services")
	}
}

// TestRepositoryAndContainerPathsAreReported is the slice 3 acceptance
// criterion "Repository/container-path mappings are printed accurately", at the
// level that produces them for `feat doctor`.
func TestRepositoryAndContainerPathsAreReported(t *testing.T) {
	w := arrange(t)
	report := w.diagnose(t)
	mounts := w.only(t, report).Mounts

	want := []config.Mount{
		{RepositoryID: "api", HostPath: filepath.Join(w.home, "repos", "app", "api"),
			ContainerPath: "/srv/api", DefaultAccess: "read_write", Primary: true},
		{RepositoryID: "infra", HostPath: filepath.Join(w.home, "repos", "app", "infra"),
			ContainerPath: "/srv/infra", DefaultAccess: "stable_read_only"},
		{RepositoryID: "web", HostPath: filepath.Join(w.home, "repos", "app", "web"),
			ContainerPath: "/srv/web", DefaultAccess: "selectable"},
	}
	if len(mounts) != len(want) {
		t.Fatalf("reported %d mounts, want %d: %+v", len(mounts), len(want), mounts)
	}
	for i, mount := range mounts {
		if mount != want[i] {
			t.Errorf("mount %d = %+v, want %+v", i, mount, want[i])
		}
	}
}

// TestUnregisteredProjectIsReportedWithoutFailing checks the state a user is in
// the first time they run `feat doctor`: a valid configuration that has not
// been registered yet.
func TestUnregisteredProjectIsReportedWithoutFailing(t *testing.T) {
	w := arrange(t)
	w.opts.Registered = func(string) bool { return false }

	report := w.diagnose(t)
	found := finding(t, w.only(t, report).Findings, "registration")

	if found.Severity != project.SeverityWarning {
		t.Errorf("an unregistered project is %q, want warning", found.Severity)
	}
	if !strings.Contains(found.Action, "feat project add app") {
		t.Errorf("action %q does not say how to register it", found.Action)
	}
	if report.Failed() {
		t.Error("an unregistered project failed the report; it is the normal state before registration")
	}
}

// TestDiagnosisWorksWithNoConfiguredProjects checks that `feat doctor` runs on
// a machine that has nothing configured yet, which is the machine it is most
// useful on.
func TestDiagnosisWorksWithNoConfiguredProjects(t *testing.T) {
	runner := newFakeRunner()
	report, err := project.Diagnose(context.Background(), project.Options{
		ConfigDir: filepath.Join(t.TempDir(), "absent"),
		Runner:    runner,
	})
	if err != nil {
		t.Fatalf("diagnosing an empty machine: %v", err)
	}
	if len(report.Projects) != 0 {
		t.Errorf("diagnosed %d projects on an empty machine", len(report.Projects))
	}
	if report.Failed() {
		t.Errorf("an empty machine with working tools failed:%s", render(report.Host))
	}
	if len(report.Host) == 0 {
		t.Error("an empty machine reported no host findings")
	}
}

// TestConfigurationBecomesADomainProject checks the mapping registration uses.
func TestConfigurationBecomesADomainProject(t *testing.T) {
	w := arrange(t)
	cfg, err := config.Load(w.configDir, "app", w.opts.Resolve)
	if err != nil {
		t.Fatalf("loading the configuration: %v", err)
	}

	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	registered, err := project.FromConfig(cfg, now)
	if err != nil {
		t.Fatalf("mapping the configuration: %v", err)
	}

	if got, want := registered.ID, domain.ProjectID("app"); got != want {
		t.Errorf("project id = %q, want %q", got, want)
	}
	if got, want := registered.PrimaryRepository, domain.RepositoryID("api"); got != want {
		t.Errorf("primary repository = %q, want %q", got, want)
	}
	if len(registered.Repositories) != 3 {
		t.Fatalf("mapped %d repositories, want 3", len(registered.Repositories))
	}
	// The mapping carries the resolved host path, not the "~" the file holds.
	api, ok := registered.Repository("api")
	if !ok {
		t.Fatal("the mapped project has no repository api")
	}
	if want := filepath.Join(w.home, "repos", "app", "api"); api.HostPath != want {
		t.Errorf("api host path = %q, want %q", api.HostPath, want)
	}
	if api.ContainerPath != "/srv/api" {
		t.Errorf("api container path = %q, want /srv/api", api.ContainerPath)
	}
	if !registered.CreatedAt.Equal(now) {
		t.Errorf("created at %v, want %v", registered.CreatedAt, now)
	}
}

// TestUpdatePreservesRegistrationTime checks that re-registering an edited
// configuration does not make a project look newly created.
func TestUpdatePreservesRegistrationTime(t *testing.T) {
	w := arrange(t)
	cfg, err := config.Load(w.configDir, "app", w.opts.Resolve)
	if err != nil {
		t.Fatalf("loading the configuration: %v", err)
	}

	created := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	existing, err := project.FromConfig(cfg, created)
	if err != nil {
		t.Fatalf("mapping the configuration: %v", err)
	}

	edited := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	updated, err := project.Update(existing, cfg, edited)
	if err != nil {
		t.Fatalf("updating the project: %v", err)
	}

	if !updated.CreatedAt.Equal(created) {
		t.Errorf("created at %v, want the original %v", updated.CreatedAt, created)
	}
	if !updated.UpdatedAt.Equal(edited) {
		t.Errorf("updated at %v, want %v", updated.UpdatedAt, edited)
	}
}

// TestDiagnosisSurvivesAnUnreadableConfigurationDirectory checks that a
// diagnostic run reports rather than fails when it cannot look around.
func TestDiagnosisSurvivesAnUnreadableConfigurationDirectory(t *testing.T) {
	w := arrange(t)
	w.opts.Projects = []string{"absent"}

	report := w.diagnose(t)
	found := finding(t, w.only(t, report).Findings, "configuration")
	if found.Severity != project.SeverityError {
		t.Errorf("a missing configuration is %q, want error", found.Severity)
	}
	if !strings.Contains(found.Summary, "absent.yaml") {
		t.Errorf("summary %q does not say where the file belongs", found.Summary)
	}
}

// TestALiveContainerIsCheckedInsteadOfBeingSkipped is the slice 8 half of
// FR-PROJ-004.
//
// The requirement is worded around the environment where the agent runs. Once a
// task of the project has a container, that environment exists and the checks
// stop being skipped — asked of the container, as the agent's own user, rather
// than of this machine.
func TestALiveContainerIsCheckedInsteadOfBeingSkipped(t *testing.T) {
	w := arrange(t)

	// A container carrying Feat's ownership labels, which is how a diagnostic
	// with no daemon and no state directory finds one.
	w.runner.output["docker ps --filter label="+compose.LabelOwner+"="+compose.OwnerValue+
		" --filter label="+compose.LabelProject+"=app --format {{.ID}}"] = "c0ffee\n"
	w.runner.output["docker exec --user developer c0ffee claude --version"] =
		claude.Verified() + " (Claude Code)"
	w.runner.output["docker exec --user developer c0ffee id -u"] = "1000\n"
	w.runner.output["docker exec --user developer c0ffee glab --version"] = "glab 1.42.0"
	w.runner.output["docker exec --user developer c0ffee glab auth status"] = "Logged in"

	findings := w.only(t, w.diagnose(t)).Findings

	for _, check := range []string{"agent.executable", "agent.execution.user", "agent.capabilities.gitlab_cli"} {
		found := finding(t, findings, check)
		if found.Severity == project.SeveritySkipped {
			t.Errorf("%s was skipped although a container of the project is running: %q", check, found.Summary)
		}
	}
	if user := finding(t, findings, "agent.execution.user"); !strings.Contains(user.Summary, "uid 1000") {
		t.Errorf("the agent's identity was not read from the container: %q", user.Summary)
	}
}

// TestARootAgentInALiveContainerFailsDoctor checks the answer configuration
// cannot give.
//
// A project may name a non-root user that the image resolves to uid 0. The
// configuration is then valid and the container still breaks the security
// model, and only the running container can say so.
func TestARootAgentInALiveContainerFailsDoctor(t *testing.T) {
	w := arrange(t)

	w.runner.output["docker ps --filter label="+compose.LabelOwner+"="+compose.OwnerValue+
		" --filter label="+compose.LabelProject+"=app --format {{.ID}}"] = "c0ffee\n"
	w.runner.output["docker exec --user developer c0ffee claude --version"] =
		claude.Verified() + " (Claude Code)"
	w.runner.output["docker exec --user developer c0ffee id -u"] = "0\n"

	found := finding(t, w.only(t, w.diagnose(t)).Findings, "agent.execution.user")
	if found.Severity != project.SeverityError {
		t.Errorf("a root agent is %q, want an error", found.Severity)
	}
	if !strings.Contains(found.Summary, "uid 0") {
		t.Errorf("the finding does not say what was observed: %q", found.Summary)
	}
}

// TestDoctorStartsNoContainer is ADR-028's rule, kept as the checks moved
// inside one.
//
// A diagnostic that started a container to answer a question would make a
// command that changes nothing change something, and it would do it on a machine
// the user was asking about rather than acting on.
func TestDoctorStartsNoContainer(t *testing.T) {
	w := arrange(t)
	w.diagnose(t)

	for _, call := range w.runner.calls {
		for _, forbidden := range []string{"docker compose up", "docker run", "docker start", "docker create"} {
			if strings.HasPrefix(call, forbidden) {
				t.Errorf("feat doctor ran %q, which creates or starts a container", call)
			}
		}
	}
}
