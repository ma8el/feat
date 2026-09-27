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
	"github.com/ma8el/feat/internal/integrationtest"
	"github.com/ma8el/feat/internal/project"
)

// requireRealTools ends the test unless the run is opted in.
func requireRealTools(t *testing.T) {
	t.Helper()
	if !integrationtest.Enabled() {
		t.Skipf("set %s=1 to run the tests that use the real tools", integrationtest.Env)
	}
}

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
	requireRealTools(t)
	if _, err := exec.LookPath("git"); err != nil {
		integrationtest.Unavailable(t, integrationtest.Git, "git is not installed")
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
		// a host-mode project that declared one is rejected (ADR-033). The
		// agent's own container paths go for the same reason: there is no agent
		// container to mount a worktree in.
		{"    config_volume: example-claude-config\n", ""},
		{"    agent:\n      container_path: /srv/api\n", ""},
		{"    agent:\n      container_path: /srv/web\n", ""},
		{"    agent:\n      container_path: /srv/infra\n", ""},
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
	requireRealTools(t)
	if _, err := exec.LookPath("git"); err != nil {
		integrationtest.Unavailable(t, integrationtest.Git, "git is not installed")
	}

	// Symbolic links are resolved rather than assumed away: Git answers with the
	// real path of a working tree, and on macOS the temporary directory is
	// reached through one. The expectation is therefore the resolved path, which
	// is also what a configuration ends up holding.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolving the temporary directory: %v", err)
	}
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
	// Where that remote points, which is what the forge question proposes from.
	// Asked of Git rather than assumed, because `remote get-url` is the command
	// whose output shape this reads (ADR-100).
	if checkout.RemoteURL != origin {
		t.Errorf("the remote URL is %q, want %q", checkout.RemoteURL, origin)
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
	if solo.RemoteURL != "" {
		t.Errorf("a repository with no remote answers with the URL %q", solo.RemoteURL)
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
	requireRealTools(t)
	if _, err := exec.LookPath("docker"); err != nil {
		integrationtest.Unavailable(t, integrationtest.Docker, "docker is not installed")
	}
	if err := exec.Command("docker", "compose", "version").Run(); err != nil {
		integrationtest.Unavailable(t, integrationtest.Docker, "the Docker Compose CLI is not available: %v", err)
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
// The fake runner decides that for itself. Docker 29.5.2 writes "executable file not
// found in $PATH" to standard output and exits 127 with an empty standard error, so a
// diagnostic reading only standard error sees "exit status 127", which names no cause
// and matches no rule. This test is here so a runtime changing its mind about which
// stream carries the reason fails rather than quietly turning the check into a
// warning.
func TestRealTheDockerCapabilityIsProbedInALiveContainer(t *testing.T) {
	requireRealTools(t)
	if _, err := exec.LookPath("docker"); err != nil {
		integrationtest.Unavailable(t, integrationtest.Docker, "docker is not installed")
	}

	w := arrange(t)
	w.opts.Runner = project.HostRunner{}
	// nobody, because the image is a plain alpine and the check runs as the
	// user the agent would be.
	rewrite(t, w, "    user: developer", "    user: nobody")
	// A project identifier no other package can produce. Containers are the one thing
	// these tests share with every other test on the machine, and the fixture's own id
	// is "app", which internal/execution/compose's integration tests also use for the
	// containers they start. `go test ./...` runs packages in parallel, so doctor would
	// find whichever of the two Docker listed first and this test would pass or fail by
	// timing (F5-01).
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
		// Docker answered the probe and then failed to start a container: the proof
		// is gone while the machine still looks equipped, which a gate must not
		// report as ok.
		integrationtest.Unavailable(t, integrationtest.Docker, "starting a container: %v", err)
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
//
// Both halves go: the project's own runtime settings, and the repository
// contribution that composes it. A contribution to a runtime the project no
// longer configures is a configuration error rather than a leftover, which is
// the point of putting the two together (ADR-065).
func dropRuntimeSection(t *testing.T, w *world) {
	t.Helper()
	file := filepath.Join(w.configDir, "app.yaml")
	body, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("reading the configuration: %v", err)
	}
	text := string(body)
	start := strings.Index(text, "\nruntime:\n")
	end := strings.Index(text, "\nchecks:\n")
	if start < 0 || end < 0 || end < start {
		t.Fatal("the fixture no longer has a runtime section followed by a checks section")
	}
	text = text[:start] + text[end:]

	contribution := strings.Index(text, "    runtime:\n")
	repository := strings.Index(text, "    default_branch: main\n")
	if contribution < 0 || repository < contribution {
		t.Fatal("the fixture no longer has a repository contributing to the runtime")
	}
	text = text[:contribution] + text[repository:]

	if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
		t.Fatalf("writing the configuration: %v", err)
	}
}

// TestRealWorktreeMountsAreAskedOfGit runs the mount pre-flight against a real
// repository with a real `.gitignore`.
//
// The unit tests decide what Git says about a path. This one asks it, which is
// the only way to find out that `ls-files --error-unmatch` is still how a
// repository is asked whether it tracks something, that an ignored file answers
// the way the check assumes, and that the pathspec survives the symbolic links
// between a temporary directory and where it really is.
//
// The same ignored file is bound to two targets and only the one inside the container
// path is reported. Its twin outside is a mount of the ordinary checkout, where the
// file is, which is why the source-side question went (ADR-104).
func TestRealWorktreeMountsAreAskedOfGit(t *testing.T) {
	requireRealTools(t)
	if _, err := exec.LookPath("git"); err != nil {
		integrationtest.Unavailable(t, integrationtest.Git, "git is not installed")
	}

	w := arrange(t)
	w.opts.Runner = project.HostRunner{}
	// The runtime section needs Docker; the agent's own files are enough to ask
	// this question, and they are the half the evidence for it came from.
	dropRuntimeSection(t, w)

	api := filepath.Join(w.home, "repos", "app", "api")
	if err := os.WriteFile(filepath.Join(api, ".gitignore"), []byte(".env\n"), 0o600); err != nil {
		t.Fatalf("writing the ignore file: %v", err)
	}
	git(t, api, "init", "--initial-branch=main")
	git(t, api, "config", "user.email", "doctor@example.invalid")
	git(t, api, "config", "user.name", "Doctor")
	git(t, api, "add", ".gitignore", "docker-compose.yml")
	git(t, api, "commit", "--message", "initial")

	// The agent's own Compose files bind three paths out of that repository: one
	// the commit above tracks, and the ignored one beside it — which the fixture
	// wrote and Git has never heard of — written to two different targets, so
	// that real Git answers for both halves of the check at once.
	w.composeFile(t, filepath.Join(w.home, "repos", "app", "infra", "docker-compose.yml"),
		`services:
  dev:
    image: alpine
    volumes:
      - ../api:/srv/api
      - ../api/.env:/srv/api/.env
      - ../api/.env:/etc/app/env:ro
      - ../api/docker-compose.yml:/srv/api/docker-compose.yml:ro
`)

	findings := w.only(t, diagnose(t, w)).Findings

	var reported []project.Finding
	for _, found := range findings {
		if found.Check == "repositories.api.agent.mounts" {
			reported = append(reported, found)
		}
	}
	if len(reported) != 1 {
		t.Fatalf("real Git reported %d mounts, want the ignored one written into the container "+
			"path:%s", len(reported), render(findings))
	}
	target := naming(t, reported, "/srv/api/.env")

	// Real Git says the path is not tracked, so a worktree would not hold it. Whether
	// that stops a task is the runtime's to say, and Feat's expectation of the runtime
	// in front of this test is what its severity has to match. This test demands Git
	// and not Docker, so asserting an error outright would assert something about a
	// machine it says nothing about.
	want := project.SeverityWarning
	if project.RefusesFileMountPoint(t.Context(), project.HostRunner{}) {
		want = project.SeverityError
	}
	if target.Severity != want {
		t.Errorf("the target finding is %q, want %q for the runtime Feat sees here",
			target.Severity, want)
	}
	// And the same ignored file bound outside the container path is nothing to act on,
	// with real Git answering: the entry mounts the ordinary checkout, which has it.
	for _, found := range findings {
		if found.Severity == project.SeverityOK {
			continue
		}
		if strings.Contains(found.Summary, "/etc/app/env") {
			t.Errorf("a mount resolving into the ordinary checkout was reported: %q", found.Summary)
		}
	}
	// The tracked file is recognised by its path in the repository rather than
	// by its base name, so neither finding is about it — by its source and by
	// its target alike. Every summary names the Compose file it was written in,
	// which shares that base name and is why this asks about whole paths.
	for _, found := range reported {
		for _, tracked := range []string{filepath.Join(api, "docker-compose.yml"),
			"/srv/api/docker-compose.yml"} {
			if strings.Contains(found.Summary, tracked) {
				t.Errorf("a tracked file was reported as one a worktree will not hold: %q",
					found.Summary)
			}
		}
	}
}

// TestRealTrackerCommandIsRunAndValidated runs a project's tracker command as a
// real process and checks what diagnostics make of it.
//
// The unit tests decide what a tracker printed. This one asks a process, which
// is the only way to find out that the configured argument vector never becomes
// a command, that it runs somewhere it cannot, or that what a program actually
// writes to standard output is not what the validator is given (ADR-071).
//
// It needs no tracker and no account: the command is a shell script the test
// writes, which is a program every machine Feat targets has.
func TestRealTrackerCommandIsRunAndValidated(t *testing.T) {
	requireRealTools(t)

	const conforming = `[{"reference":"ACME-14","title":"Reset links expire",` +
		`"body":"After five minutes.","url":"https://example.test/14","state":"open"}]`
	// What `gh issue list --json number,…` prints with no mapping at all, which
	// is the mistake `feat doctor` exists to find.
	const unmapped = `[{"number":14,"title":"Reset links expire",` +
		`"body":"","url":"https://example.test/14","state":"open"}]`

	for _, testCase := range []struct {
		name     string
		prints   string
		severity project.Severity
		says     string
	}{
		{
			name: "output that conforms", prints: conforming,
			severity: project.SeverityOK, says: "1 ticket",
		},
		{
			name: "the tracker's own field names", prints: unmapped,
			severity: project.SeverityError, says: `"number"`,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			w := arrange(t)
			w.opts.Runner = project.HostRunner{}
			// No fake tracker: the command below is run for real.
			w.configureTrackerCommand(t, writeTrackerScript(t, testCase.prints))

			found := finding(t, w.only(t, w.diagnose(t)).Findings, "tracker.command")
			if found.Severity != testCase.severity {
				t.Fatalf("the finding is %q, want %q: %s", found.Severity, testCase.severity, found.Summary)
			}
			if !strings.Contains(found.Summary, testCase.says) {
				t.Errorf("the finding does not say %q: %s", testCase.says, found.Summary)
			}
		})
	}
}

// writeTrackerScript writes an executable that prints what it is given, and
// returns its path.
//
// It prints to standard output and writes a line to standard error as well,
// because a tool that says something while succeeding is ordinary and only what
// it printed on standard output is the document.
func writeTrackerScript(t *testing.T, prints string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "tickets-for-me")
	script := "#!/bin/sh\necho 'reading tickets' >&2\ncat <<'TICKETS'\n" + prints + "\nTICKETS\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil { // #nosec G306 -- it is run
		t.Fatalf("writing the tracker command: %v", err)
	}
	return path
}

// TestRealABindSourceReachesTheOrdinaryCheckout measures the premise ADR-081's
// source-side mount check rested on, and which ADR-104 removed the check over.
//
// That check said a bind of an untracked path names something a task's worktree will
// not hold. Feat rewrites no bind source, so such an entry resolves against the
// ordinary checkout, where the untracked file is. The two cases are the two places the
// entry can write and neither is the failure the check described:
//
//   - outside the container path, the container starts and the application reads the
//     checkout's own file. Nothing is absent and nothing is empty;
//   - inside it, the entry is the target-side finding ADR-098 already makes. This is
//     also the shape ADR-081's evidence 5 described — "a mount over a file that is
//     simply created empty succeeds, and the application then misbehaves" — reached on
//     a second attempt rather than by a mechanism of its own: the attempt that is
//     refused leaves the mount point behind, and what the application then reads is the
//     empty file the mask itself created.
//
// It asserts no platform, for the reason its sibling below does not. A runtime Feat
// expects to refuse a file mount point needs two attempts to reach that state and one
// that does not needs one; the state reached is the same either way.
func TestRealABindSourceReachesTheOrdinaryCheckout(t *testing.T) {
	requireRealTools(t)
	if _, err := exec.LookPath("docker"); err != nil {
		integrationtest.Unavailable(t, integrationtest.Docker, "docker is not installed")
	}

	// A checkout holding a tracked file and an ignored one beside it, and a worktree
	// holding only the tracked one, which is what Git would have given a task.
	root := t.TempDir()
	// A mount point the runtime created belongs to the container's user, so the test's
	// own cleanup cannot remove it. This runs first, because TempDir registered its
	// cleanup before this one and they unwind in reverse.
	t.Cleanup(func() {
		_ = exec.Command("docker", "run", "--rm", "--volume", root+":/scratch",
			"alpine:3", "sh", "-c", "rm -rf /scratch/*").Run()
	})
	checkout, worktree := filepath.Join(root, "api"), filepath.Join(root, "worktree")
	for _, dir := range []string{checkout, worktree} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("creating %s: %v", dir, err)
		}
	}
	const secret = "SECRET=from-the-ordinary-checkout"
	for path, body := range map[string]string{
		filepath.Join(checkout, ".env"):         secret + "\n",
		filepath.Join(checkout, "tracked.conf"): "x\n",
		filepath.Join(worktree, "tracked.conf"): "x\n",
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
	}

	t.Run("a target outside the container path", func(t *testing.T) {
		// The entry the removed check warned about, with the worktree mounted beside
		// it exactly as a task mounts one.
		output, err := exec.Command("docker", "run", "--rm",
			"--volume", worktree+":/srv/api",
			"--volume", filepath.Join(checkout, ".env")+":/etc/app/env:ro",
			"alpine:3", "cat", "/etc/app/env").CombinedOutput()
		if err != nil {
			t.Fatalf("a bind of an untracked path beside a worktree did not start: %s", output)
		}
		if strings.TrimSpace(string(output)) != secret {
			t.Errorf("the application read %q, want the ordinary checkout's own file: the source "+
				"resolves there and Feat rewrites it nowhere, so there is nothing for a worktree "+
				"to fail to hold", strings.TrimSpace(string(output)))
		}
	})

	t.Run("a target inside the container path", func(t *testing.T) {
		// Its own worktree, because the first attempt may leave a mount point in it.
		masked := filepath.Join(root, "masked")
		if err := os.MkdirAll(masked, 0o700); err != nil {
			t.Fatalf("creating %s: %v", masked, err)
		}
		refuses := project.RefusesFileMountPoint(t.Context(), project.HostRunner{})
		t.Logf("Feat expects this runtime to refuse a file mount point: %t", refuses)

		read := func() (string, error) {
			output, err := exec.Command("docker", "run", "--rm",
				"--volume", masked+":/srv/api",
				"--volume", "/dev/null:/srv/api/.env:ro",
				"alpine:3", "cat", "/srv/api/.env").CombinedOutput()
			return string(output), err
		}

		output, err := read()
		if refuses != (err != nil) {
			t.Fatalf("Feat expects this runtime to refuse a file mount point (%t) and it did not "+
				"agree, so what the mount check says about it is wrong in one direction or the "+
				"other: %s", refuses, output)
		}
		if err != nil {
			// The refusal is not inert: the daemon created the mount point on its way
			// to failing, which is what makes evidence 5's state reachable at all and
			// what makes resuming a task enough to recover.
			if _, statErr := os.Stat(filepath.Join(masked, ".env")); statErr != nil {
				t.Fatalf("the refused attempt left no mount point behind, so evidence 5's shape "+
					"is not this one after all: %v", statErr)
			}
			if output, err = read(); err != nil {
				t.Fatalf("the second attempt with the same entry did not start: %s", output)
			}
		}
		// Evidence 5's misbehaviour, named: the mask is over the empty file it caused
		// to exist, so the application reads nothing where the checkout has content.
		if strings.TrimSpace(output) != "" {
			t.Errorf("the application read %q through the mask, want nothing", strings.TrimSpace(output))
		}
	})
}

// TestRealFileMountPointBehaviourIsWhatFeatExpects holds the target-side mount
// check to what the runtime on this machine actually does.
//
// The check's severity rests on a measurement that is not in any documentation: what
// a container runtime does when a mount's target is missing inside a bind mount. It
// is not the same everywhere. Docker Desktop refuses to create a file mount point and
// the task fails at container creation, while a native Linux daemon creates the file
// and the container starts.
//
// So it asserts no platform. It asks Feat what it expects of this runtime,
// through the same predicate the check uses, and asserts the runtime agrees —
// which makes it a test of the mapping rather than of one machine. It passes on
// Docker Desktop and on a native daemon, and fails on any runtime Feat has
// classified wrongly, including the ones nobody here can try: Colima, Rancher
// Desktop, WSL2, a daemon over a socket.
//
// Both directions are failures worth having. Feat expecting a refusal that does
// not happen is an error reported against a project that works; Feat expecting
// none where the runtime refuses is a launch that fails after a diagnosis that
// only warned.
//
// The bind mount stands in for a task's worktree, and the absent target for a
// path Git does not track.
func TestRealFileMountPointBehaviourIsWhatFeatExpects(t *testing.T) {
	requireRealTools(t)
	if _, err := exec.LookPath("docker"); err != nil {
		integrationtest.Unavailable(t, integrationtest.Docker, "docker is not installed")
	}

	// A source that is a directory and one that is a file, beside a worktree
	// made fresh for each case.
	//
	// Fresh because a refused launch is not inert: the daemon creates the
	// missing mount point in the worktree on its way to failing, so a second
	// case reusing the directory would find the file the first one left and pass
	// for the wrong reason. That is also why a task that failed this way starts
	// when it is resumed.
	root := t.TempDir()
	// A mount point the runtime created belongs to the container's user, so the
	// test's own cleanup cannot remove it. This runs before that one, because
	// TempDir registered its cleanup first and they unwind in reverse.
	t.Cleanup(func() {
		_ = exec.Command("docker", "run", "--rm", "--volume", root+":/scratch",
			"alpine:3", "sh", "-c", "rm -rf /scratch/*").Run()
	})

	directory := filepath.Join(root, "directory")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("creating %s: %v", directory, err)
	}
	file := filepath.Join(root, "file.conf")
	if err := os.WriteFile(file, []byte("x\n"), 0o600); err != nil {
		t.Fatalf("writing %s: %v", file, err)
	}

	// What Feat expects of the runtime it can see, asked exactly as the check
	// asks it. Everything below is asserted against this rather than against a
	// platform.
	refuses := project.RefusesFileMountPoint(t.Context(), project.HostRunner{})
	t.Logf("Feat expects this runtime to refuse a file mount point: %t", refuses)

	for _, c := range []struct {
		name string
		// mount is the entry added beside the worktree.
		mount string
		// needsFile is whether the runtime would have to create a *file* for
		// this entry's mount point, which is the property the reader classifies
		// on (project.mountPointFor) and the only one Feat's expectation is
		// about.
		needsFile bool
	}{
		// The shapes the check reports, where the worktree has nothing and the
		// source is not a directory.
		{"a device masking an absent file", "/dev/null:/srv/api/.env:ro", true},
		{"a file over an absent file", file + ":/srv/api/.env:ro", true},
		// And the shapes it stays silent about on every runtime, each for its own
		// reason: a directory mount point is created, an absent source is created
		// as a directory, and a path already in the worktree needs no mount point
		// created at all.
		{"a directory over an absent path", directory + ":/srv/api/node_modules", false},
		{"an absent source over an absent path", filepath.Join(root, "gone") + ":/srv/api/gone", false},
		{"a device masking a path the worktree holds", "/dev/null:/srv/api/tracked.conf:ro", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			worktree := filepath.Join(root, strings.ReplaceAll(c.name, " ", "-"))
			if err := os.MkdirAll(worktree, 0o700); err != nil {
				t.Fatalf("creating %s: %v", worktree, err)
			}
			if err := os.WriteFile(
				filepath.Join(worktree, "tracked.conf"), []byte("x\n"), 0o600); err != nil {
				t.Fatalf("writing the tracked file: %v", err)
			}

			output, err := exec.Command("docker", "run", "--rm",
				"--volume", worktree+":/srv/api", "--volume", c.mount,
				"alpine:3", "true").CombinedOutput()

			// A file mount point is refused only where Feat said this runtime
			// refuses one. Nothing else is ever refused.
			wantRefused := c.needsFile && refuses
			switch {
			case err != nil && !strings.Contains(string(output), "mountpoint"):
				// It failed for some other reason, which proves nothing about the
				// rule either way and must not be read as proving it.
				integrationtest.Unavailable(t, integrationtest.Docker,
					"starting a container failed for an unrelated reason: %s", output)
			case wantRefused && err == nil:
				t.Errorf("Feat expects this runtime to refuse a file mount point and it created "+
					"one, so checkMountTargets reports an error against a project that works: %s",
					output)
			case !wantRefused && err != nil:
				t.Errorf("this runtime refused a mount point Feat does not expect it to "+
					"(needs a file: %t, Feat expects refusal: %t), so a task fails at container "+
					"creation after a diagnosis that did not say so: %s", c.needsFile, refuses, output)
			}
		})
	}
}
