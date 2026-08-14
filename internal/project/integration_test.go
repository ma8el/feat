package project_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ma8el/feat/internal/execution/compose"
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

// TestRealCheckoutIsInspected runs the discovery `feat project init` proposes
// from against real repositories.
//
// The unit tests decide what Git says. This one asks it, which is the only way
// to find out that "symbolic-ref --short refs/remotes/origin/HEAD" is still how
// a clone reports its default branch, and that a repository without one still
// answers something usable.
func TestRealCheckoutIsInspected(t *testing.T) {
	if os.Getenv(envIntegration) == "" {
		t.Skipf("set %s=1 to run the tests that use the real tools", envIntegration)
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	clone := filepath.Join(root, "clone")
	solitary := filepath.Join(root, "solitary")

	git(t, "", "init", "--bare", "--initial-branch=trunk", origin)
	git(t, "", "init", "--initial-branch=trunk", solitary)
	git(t, solitary, "config", "user.email", "wizard@example.invalid")
	git(t, solitary, "config", "user.name", "Wizard")
	git(t, solitary, "commit", "--allow-empty", "--message", "initial")
	git(t, solitary, "push", "--quiet", origin, "trunk")
	git(t, "", "clone", "--quiet", origin, clone)

	ctx := context.Background()

	// A clone knows both answers, and it knows the branch from the remote
	// rather than from what happens to be checked out.
	git(t, clone, "checkout", "--quiet", "-b", "a-feature")
	checkout, err := project.Inspect(ctx, project.HostRunner{}, clone)
	if err != nil {
		t.Fatalf("inspecting a clone: %v", err)
	}
	if checkout.Root != clone {
		t.Errorf("the root is %q, want %q", checkout.Root, clone)
	}
	if checkout.Remote != "origin" {
		t.Errorf("the remote is %q, want %q", checkout.Remote, "origin")
	}
	if checkout.DefaultBranch != "trunk" {
		t.Errorf("the default branch is %q, want the branch the remote publishes", checkout.DefaultBranch)
	}

	// A subdirectory is answered with the working tree it is in, because that
	// is the path a repository is configured by.
	nested := filepath.Join(clone, "nested")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatalf("creating a subdirectory: %v", err)
	}
	if inside, err := project.Inspect(ctx, project.HostRunner{}, nested); err != nil {
		t.Errorf("inspecting a subdirectory: %v", err)
	} else if inside.Root != clone {
		t.Errorf("a subdirectory answers with root %q, want %q", inside.Root, clone)
	}

	// A repository with no remote answers with the branch it is on, and no
	// remote, which is what makes the wizard resolve bases locally.
	solo, err := project.Inspect(ctx, project.HostRunner{}, solitary)
	if err != nil {
		t.Fatalf("inspecting a repository with no remote: %v", err)
	}
	if solo.Remote != "" {
		t.Errorf("a repository with no remote answers with %q", solo.Remote)
	}
	if solo.DefaultBranch != "trunk" {
		t.Errorf("the default branch is %q, want the branch that is checked out", solo.DefaultBranch)
	}

	// A directory that is in no repository is the one answer that is an error:
	// there is nothing to configure.
	outside := t.TempDir()
	if _, err := project.Inspect(ctx, project.HostRunner{}, outside); err == nil {
		t.Errorf("%s was inspected as a repository", outside)
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

// TestRealTheDockerCapabilityIsProbedInALiveContainer runs the Docker
// capability check against a real container, which is the only thing that can
// answer the question the check rests on: how a container runtime reports an
// executable that is not there.
//
// The fake runner decides that for itself. Docker 29.5.2 writes
// "executable file not found in $PATH" to *standard output* and exits 127 with
// an empty standard error, so a diagnostic reading only standard error saw
// "exit status 127" — no cause, matching no rule, and every absent client
// reported as a question that could not be asked rather than as an answer. This
// test failed before HostRunner.Run was taught to fall back to standard output,
// and it is here so that a runtime changing its mind about which stream carries
// the reason fails rather than quietly turning the check back into a warning.
func TestRealTheDockerCapabilityIsProbedInALiveContainer(t *testing.T) {
	if os.Getenv(envIntegration) == "" {
		t.Skipf("set %s=1 to run the tests that use the real tools", envIntegration)
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is not installed")
	}

	w := arrange(t)
	w.opts.Runner = project.HostRunner{}
	// nobody, because the image is a plain alpine and the check runs as the
	// user the agent would be.
	rewrite(t, w, "    user: developer", "    user: nobody")
	// A project identifier no other package can produce. Containers are the one
	// thing these tests share with every other test on the machine, and the
	// fixture's own id is "app" — which internal/execution/compose's integration
	// tests also use for the containers they start. `go test ./...` runs
	// packages in parallel, so doctor would find whichever of the two Docker
	// listed first and this test would pass or fail by timing. That is F5-01
	// arriving in the suite rather than in the product, and the honest way to
	// keep it out of this test is not to share the label.
	id := renameProject(t, w)

	// A container wearing Feat's ownership labels, which is how a diagnostic
	// with no daemon finds one. It is started here rather than by Feat: ADR-028
	// forbids doctor from starting anything, and this test would not detect that
	// rule breaking if it relied on it.
	container := runContainer(t, "--label", compose.LabelOwner+"="+compose.OwnerValue,
		"--label", compose.LabelProject+"="+id)

	found := finding(t, w.only(t, diagnose(t, w)).Findings, "agent.capabilities.docker")
	if found.Severity != project.SeverityOK {
		t.Fatalf("a container with no container client is %q, want ok: %s", found.Severity, found.Summary)
	}
	for _, client := range compose.ContainerClients {
		if !strings.Contains(found.Summary, client) {
			t.Errorf("the finding does not say %s was looked for: %q", client, found.Summary)
		}
	}

	// The same container with a client on its path. Any executable of that name
	// is the capability: what matters is that the image has one, not what it
	// does when run.
	install(t, container, "nobody", "podman")

	found = finding(t, w.only(t, diagnose(t, w)).Findings, "agent.capabilities.docker")
	if found.Severity != project.SeverityError {
		t.Errorf("a container carrying podman is %q, want an error: a launch refuses it", found.Severity)
	}
	if !strings.Contains(found.Summary, "podman") {
		t.Errorf("the finding does not name what was found: %q", found.Summary)
	}
}

// renameProject gives the arranged configuration an identifier unique to this
// run, and returns it.
//
// The file name carries the identifier, so both move together: config.Find
// resolves a project by the name of its file.
func renameProject(t *testing.T, w *world) string {
	t.Helper()

	id := "probe" + strconv.FormatInt(time.Now().UnixNano(), 10)
	rewrite(t, w, "  id: app", "  id: "+id)
	if err := os.Rename(filepath.Join(w.configDir, "app.yaml"),
		filepath.Join(w.configDir, id+".yaml")); err != nil {
		t.Fatalf("renaming the configuration: %v", err)
	}
	return id
}

// runContainer starts a container for a test and removes it afterwards.
func runContainer(t *testing.T, options ...string) string {
	t.Helper()

	args := append([]string{"run", "--detach"}, options...)
	args = append(args, "alpine:3", "sleep", "300")
	output, err := exec.Command("docker", args...).Output()
	if err != nil {
		t.Skipf("starting a container: %v", err)
	}
	id := strings.TrimSpace(string(output))
	t.Cleanup(func() {
		if err := exec.Command("docker", "rm", "--force", id).Run(); err != nil {
			t.Errorf("removing container %s: %v", id, err)
		}
	})
	return id
}

// install puts an executable of a given name on a container's path, and
// establishes that the agent's own user can run it.
//
// The second half is what makes a failure of this test readable: if the probe
// then reports the client absent, the difference is the product's and not the
// arrangement's.
func install(t *testing.T, container, user, name string) {
	t.Helper()

	script := "printf '#!/bin/sh\\nexit 0\\n' > /usr/local/bin/" + name + " && chmod 0755 /usr/local/bin/" + name
	if output, err := exec.Command("docker", "exec", container, "sh", "-c", script).CombinedOutput(); err != nil {
		t.Fatalf("installing %s in the container: %v\n%s", name, err, output)
	}
	if output, err := exec.Command("docker", "exec", "--user", user, container,
		name, "--version").CombinedOutput(); err != nil {
		t.Fatalf("%s is installed but %s cannot run it, so this test would prove nothing: %v\n%s",
			name, user, err, output)
	}
}

// diagnose runs a diagnosis for an integration test.
func diagnose(t *testing.T, w *world) project.Report {
	t.Helper()
	report, err := project.Diagnose(context.Background(), w.opts)
	if err != nil {
		t.Fatalf("diagnosing: %v", err)
	}
	return report
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
