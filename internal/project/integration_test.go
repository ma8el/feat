package project_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/project"
)

// envIntegration opts a run in to the tests that use the real tools.
const envIntegration = "FEAT_INTEGRATION"

// TestRealGitRepositoryIsDiagnosed runs the repository checks against a real
// Git repository created for the test.
//
// The fake runner in the unit tests decides what Git would say. This one asks
// it, which is the only way to find out that an argument vector is wrong, that
// a flag was removed, or that the output is not the shape the checks expect.
//
// It is opt-in because it needs Git installed. Set FEAT_INTEGRATION=1 to run
// it; CI does.
func TestRealGitRepositoryIsDiagnosed(t *testing.T) {
	if os.Getenv(envIntegration) == "" {
		t.Skipf("set %s=1 to run the tests that use the real tools", envIntegration)
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	w := arrange(t)
	w.opts.Runner = project.HostRunner{}

	// A host-native project keeps the run to Git: the devcontainer and runtime
	// checks need Docker, which the next test covers separately.
	rewriteAll(t, w, [][2]string{
		{"    mode: devcontainer", "    mode: host"},
		{"    compose_files:\n      - ~/repos/app/infra/docker-compose.yml\n", ""},
		{"    service: dev\n", ""},
		{"    user: developer\n", ""},
		{"    working_directory: /srv/api\n", ""},
		{"    control_path: /feat\n", ""},
		// A Claude configuration volume needs a container to mount it into, so
		// a host-mode project that declared one is rejected (ADR-033).
		{"    config_volume: example-claude-config\n", ""},
	})
	dropRuntimeSection(t, w)

	api := filepath.Join(w.home, "repos", "app", "api")
	origin := filepath.Join(t.TempDir(), "origin.git")
	git(t, "", "init", "--bare", "--initial-branch=main", origin)
	git(t, api, "init", "--initial-branch=main")
	git(t, api, "config", "user.email", "doctor@example.invalid")
	git(t, api, "config", "user.name", "Doctor")
	git(t, api, "commit", "--allow-empty", "--message", "initial")
	git(t, api, "remote", "add", "origin", origin)
	git(t, api, "push", "--quiet", "origin", "main")
	git(t, api, "fetch", "--quiet", "origin")

	report, err := project.Diagnose(context.Background(), w.opts)
	if err != nil {
		t.Fatalf("diagnosing: %v", err)
	}
	findings := w.only(t, report).Findings

	for _, check := range []string{
		"repositories.api",
		"repositories.api.remote",
		"repositories.api.default_branch",
	} {
		if got := finding(t, findings, check).Severity; got != project.SeverityOK {
			t.Errorf("%s is %q against a real repository, want ok:%s", check, got, render(findings))
		}
	}

	// The other two directories are not repositories, so the checks must say
	// so rather than pass because Git answered about some enclosing repository.
	if got := finding(t, findings, "repositories.web").Severity; got != project.SeverityError {
		t.Errorf("a directory that is not a repository is %q, want error", got)
	}
}

// TestRealComposeFileIsDiagnosed runs the Compose checks against the real
// Docker Compose CLI.
func TestRealComposeFileIsDiagnosed(t *testing.T) {
	if os.Getenv(envIntegration) == "" {
		t.Skipf("set %s=1 to run the tests that use the real tools", envIntegration)
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is not installed")
	}
	if err := exec.Command("docker", "compose", "version").Run(); err != nil {
		t.Skip("the Docker Compose CLI is not available")
	}

	w := arrange(t)
	w.opts.Runner = project.HostRunner{}

	// A real Compose file with the service the configuration names, and a
	// second service that is not it.
	const composeFile = `services:
  dev:
    image: alpine:3
    command: ["true"]
  other:
    image: alpine:3
    command: ["true"]
`
	infra := filepath.Join(w.home, "repos", "app", "infra", "docker-compose.yml")
	if err := os.WriteFile(infra, []byte(composeFile), 0o600); err != nil {
		t.Fatalf("writing the Compose file: %v", err)
	}
	api := filepath.Join(w.home, "repos", "app", "api", "docker-compose.yml")
	if err := os.WriteFile(api, []byte("services:\n  app:\n    image: alpine:3\n  worker:\n    image: alpine:3\n"), 0o600); err != nil {
		t.Fatalf("writing the Compose file: %v", err)
	}

	report, err := project.Diagnose(context.Background(), w.opts)
	if err != nil {
		t.Fatalf("diagnosing: %v", err)
	}
	findings := w.only(t, report).Findings

	if got := finding(t, findings, "agent.execution.service").Severity; got != project.SeverityOK {
		t.Errorf("a service that exists is %q, want ok:%s", got, render(findings))
	}

	// A service the Compose files do not define has to be reported, because the
	// alternative is a task that fails at launch.
	rewrite(t, w, "    service: dev", "    service: absent")
	report, err = project.Diagnose(context.Background(), w.opts)
	if err != nil {
		t.Fatalf("diagnosing: %v", err)
	}
	found := finding(t, w.only(t, report).Findings, "agent.execution.service")
	if found.Severity != project.SeverityError {
		t.Errorf("a service that does not exist is %q, want error", found.Severity)
	}
	if !strings.Contains(found.Summary, "absent") {
		t.Errorf("summary %q does not name the missing service", found.Summary)
	}
}

// git runs a Git command for the test's setup.
func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

// rewriteAll applies several edits to the arranged configuration.
func rewriteAll(t *testing.T, w *world, replacements [][2]string) {
	t.Helper()
	for _, replacement := range replacements {
		rewrite(t, w, replacement[0], replacement[1])
	}
}

// dropRuntimeSection removes the runtime configuration, which needs Docker.
func dropRuntimeSection(t *testing.T, w *world) {
	t.Helper()
	file := filepath.Join(w.configDir, "app.yaml")
	body, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("reading the configuration: %v", err)
	}
	text := string(body)
	start := strings.Index(text, "\nruntime:\n")
	end := strings.Index(text, "\nreview:\n")
	if start < 0 || end < 0 || end < start {
		t.Fatal("the fixture no longer has a runtime section followed by a review section")
	}
	if err := os.WriteFile(file, []byte(text[:start]+text[end:]), 0o600); err != nil {
		t.Fatalf("writing the configuration: %v", err)
	}
}
