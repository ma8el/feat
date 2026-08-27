package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/client"
	"github.com/ma8el/feat/internal/config"
	"github.com/ma8el/feat/internal/project"
)

// checkoutHost answers the Git questions the wizard asks a directory.
//
// It answers by directory, because that is what the wizard's proposals are
// derived from: a test that answered the same thing everywhere could not tell a
// repository that was inspected from one that was assumed.
type checkoutHost struct {
	workingHost

	// repositories maps a checkout's path to what Git says about it. A path
	// that is not in the map is not a repository.
	repositories map[string]checkoutAnswers
}

// checkoutAnswers is one repository's answers.
type checkoutAnswers struct {
	// remotes are the remote names, in the order `git remote` prints them.
	remotes []string
	// head is the branch the remote publishes, empty when it publishes none.
	head string
	// current is the branch checked out, empty for a detached HEAD.
	current string
}

func (h checkoutHost) Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	line := strings.TrimSpace(name + " " + strings.Join(args, " "))
	answers, known := h.repositories[dir]
	if !known {
		if strings.HasPrefix(line, "git rev-parse") || strings.HasPrefix(line, "git remote") ||
			strings.HasPrefix(line, "git symbolic-ref") {
			return "", &notInstalled{name: "fatal: not a git repository"}
		}
		return h.workingHost.Run(ctx, dir, name, args...)
	}

	switch {
	case line == "git rev-parse --show-toplevel":
		return dir, nil
	case line == "git remote":
		return strings.Join(answers.remotes, "\n"), nil
	case strings.HasPrefix(line, "git symbolic-ref --quiet --short refs/remotes/"):
		if answers.head == "" {
			return "", &notInstalled{name: "no head"}
		}
		return answers.remotes[0] + "/" + answers.head, nil
	case line == "git symbolic-ref --quiet --short HEAD":
		if answers.current == "" {
			return "", &notInstalled{name: "detached"}
		}
		return answers.current, nil
	default:
		return h.workingHost.Run(ctx, dir, name, args...)
	}
}

// wizardMachine is a machine with checkouts on it for the wizard to inspect.
type wizardMachine struct {
	*machine
	runner checkoutHost
}

// prepareWizard builds a machine with two repositories: one with an ordinary
// remote and a Compose file beside it, and one with no remote at all.
func prepareWizard(t *testing.T) *wizardMachine {
	t.Helper()

	m := prepare(t)
	api := filepath.Join(m.home, "repos", "app", "api")
	store := filepath.Join(m.home, "repos", "app", "store")

	compose := "services:\n  dev:\n    image: golang\n  worker:\n    image: golang\n"
	if err := os.WriteFile(filepath.Join(api, "compose.yaml"), []byte(compose), 0o600); err != nil {
		t.Fatalf("writing a Compose file: %v", err)
	}

	return &wizardMachine{
		machine: m,
		runner: checkoutHost{repositories: map[string]checkoutAnswers{
			api:   {remotes: []string{"origin"}, head: "main", current: "feature/x"},
			store: {current: "trunk"},
		}},
	}
}

// converse runs one command with a script of answers on its input.
func (m *wizardMachine) converse(t *testing.T, answers string, args ...string) (int, string, string) {
	t.Helper()

	var stdout, stderr bytes.Buffer
	code := execute(context.Background(), Options{
		Layout:      &m.layout,
		Environment: &m.env,
		Runner:      m.runner,
		Interactive: true,
		Input:       strings.NewReader(answers),
	}, args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// repository returns the path of one of the machine's checkouts.
func (m *wizardMachine) repository(name string) string {
	return filepath.Join(m.home, "repos", "app", name)
}

// load reads back what the wizard wrote, the way every other command would.
func (m *wizardMachine) load(t *testing.T, id string) *config.Config {
	t.Helper()

	cfg, err := config.Load(m.layout.ProjectConfigDir(), id, config.Options{
		Env:      m.env,
		StateDir: m.layout.State,
	})
	if err != nil {
		t.Fatalf("the configuration the wizard wrote does not load: %v", err)
	}
	return cfg
}

// answers joins a script of answers, one per question, the way a user types
// them. An empty string is somebody pressing Enter.
func answers(lines ...string) string { return strings.Join(lines, "\n") + "\n" }

// TestProjectInitWritesAConfigurationThatLoads is the whole point of the
// command: the answers become a file that every other command can read.
func TestProjectInitWritesAConfigurationThatLoads(t *testing.T) {
	m := prepareWizard(t)

	code, stdout, stderr := m.converse(t, answers(
		"app",               // project identifier
		"Example",           // display name
		m.repository("api"), // path of the checkout
		"api",               // repository identifier
		"",                  // default access: read_write
		"n",                 // no second repository
		"",                  // execution mode: host
		"",                  // no application services
		"go test ./...",     // verification command
		"",                  // check name
		"",                  // write it
		"n",                 // do not run diagnostics
	), "project", "init")

	if code != ExitOK {
		t.Fatalf("exit code %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	cfg := m.load(t, "app")
	if cfg.Project.Name != "Example" {
		t.Errorf("project.name is %q, want %q", cfg.Project.Name, "Example")
	}
	if cfg.Project.PrimaryRepository != "api" {
		t.Errorf("project.primary_repository is %q, want %q", cfg.Project.PrimaryRepository, "api")
	}
	if cfg.Agent.Execution.Mode != config.ModeHost {
		t.Errorf("agent.execution.mode is %q, want %q", cfg.Agent.Execution.Mode, config.ModeHost)
	}

	repository, ok := cfg.Repository("api")
	if !ok {
		t.Fatalf("the configuration has no repository api: %v", cfg.RepositoryIDs())
	}
	if repository.HostPath != m.repository("api") {
		t.Errorf("host_path is %q, want %q", repository.HostPath, m.repository("api"))
	}
	// Both were read from the checkout rather than asked for, which is what the
	// wizard exists to do.
	if repository.DefaultBranch != "main" {
		t.Errorf("default_branch is %q, want the branch the remote publishes", repository.DefaultBranch)
	}
	if repository.Remote != "origin" {
		t.Errorf("remote is %q, want %q", repository.Remote, "origin")
	}

	checks := cfg.Checks["api"]
	if len(checks) != 1 {
		t.Fatalf("%d checks for api, want 1", len(checks))
	}
	if got := strings.Join(checks[0].Command, " "); got != "go test ./..." {
		t.Errorf("the check runs %q", got)
	}
	if !strings.Contains(stdout, "wrote "+filepath.Join(m.layout.ProjectConfigDir(), "app.yaml")) {
		t.Errorf("the wizard does not say what it wrote:\n%s", stdout)
	}

	// Every group of questions is announced. A conversation is one column of
	// text, so the headings are the only thing that says which part of the file
	// is being answered.
	for _, heading := range []string{
		"Repositories", "Where the agent runs", "Application services", "Verification",
	} {
		if !strings.Contains(stdout, "\n"+heading+"\n") {
			t.Errorf("the conversation does not announce %q:\n%s", heading, stdout)
		}
	}
}

// TestProjectInitConfiguresADevcontainerFromWhatItFinds checks the mode with
// something to discover: the Compose file beside the repository, and the
// services that file defines.
func TestProjectInitConfiguresADevcontainerFromWhatItFinds(t *testing.T) {
	m := prepareWizard(t)

	code, stdout, stderr := m.converse(t, answers(
		"app",                 // project identifier
		"",                    // display name: app
		m.repository("api"),   // first checkout
		"api",                 // repository identifier
		"",                    // read_write
		"y",                   // add another repository
		m.repository("store"), // second checkout
		"store",               // repository identifier
		"",                    // selectable
		"n",                   // no third repository
		"",                    // primary repository: api
		"devcontainer",        // execution mode
		// Named rather than accepted from a proposal: the agent's Compose
		// question is asked before the application section exists, so it has
		// nothing to tell a devcontainer's file from an application's and
		// proposes neither.
		filepath.Join(m.repository("api"), "compose.yaml"),
		"",          // no second Compose file
		"",          // service: dev, which the file defines
		"developer", // container user
		"",          // mount for api: /srv/api
		"",          // mount for store: /srv/store
		"",          // give Claude a volume
		"",          // volume name: feat-claude
		"n",         // no application services
		"",          // no verification command
		"",          // write it
		"n",         // do not run diagnostics
	), "project", "init")

	if code != ExitOK {
		t.Fatalf("exit code %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	cfg := m.load(t, "app")
	execution := cfg.Agent.Execution
	if execution.Mode != config.ModeDevcontainer {
		t.Fatalf("agent.execution.mode is %q", execution.Mode)
	}
	if execution.Service != "dev" {
		t.Errorf("agent.execution.service is %q, want the service the Compose file defines", execution.Service)
	}
	want := filepath.Join(m.repository("api"), "compose.yaml")
	if len(execution.ComposeFiles) != 1 || execution.ComposeFiles[0] != want {
		t.Errorf("agent.execution.compose_files is %v, want [%s]", execution.ComposeFiles, want)
	}
	if execution.User != "developer" {
		t.Errorf("agent.execution.user is %q", execution.User)
	}
	if cfg.Agent.Claude.ConfigVolume != "feat-claude" {
		t.Errorf("agent.claude.config_volume is %q", cfg.Agent.Claude.ConfigVolume)
	}

	store, _ := cfg.Repository("store")
	if store.Agent.ContainerPath != "/srv/store" {
		t.Errorf("the agent mount for store is %q", store.Agent.ContainerPath)
	}
	// The second repository has no remote, so a base cannot be resolved from
	// one. The wizard says so rather than writing a policy that would fail on
	// the first task.
	if cfg.Git.BasePolicy != config.PolicyRemote {
		t.Errorf("git.base_policy is %q, want %q for a project that has a remote", cfg.Git.BasePolicy, config.PolicyRemote)
	}
	if !strings.Contains(stdout, "services defined there: dev, worker") {
		t.Errorf("the wizard does not report the services it found:\n%s", stdout)
	}
}

// TestProjectInitResolvesBasesLocallyWithoutARemote checks the one value the
// wizard decides on the user's behalf, and that it says so.
func TestProjectInitResolvesBasesLocallyWithoutARemote(t *testing.T) {
	m := prepareWizard(t)

	code, stdout, stderr := m.converse(t, answers(
		"app", "", m.repository("store"), "store", "", "n",
		"", // host
		"n",
		"",  // no check
		"",  // write it
		"n", // no diagnostics
	), "project", "init")

	if code != ExitOK {
		t.Fatalf("exit code %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	cfg := m.load(t, "app")
	if cfg.Git.BasePolicy != config.PolicyLocal {
		t.Errorf("git.base_policy is %q, want %q for a project with no remote", cfg.Git.BasePolicy, config.PolicyLocal)
	}
	repository, _ := cfg.Repository("store")
	if repository.DefaultBranch != "trunk" {
		t.Errorf("default_branch is %q, want the branch that is checked out", repository.DefaultBranch)
	}
	if !strings.Contains(stdout, "No repository has a remote") {
		t.Errorf("the wizard does not say why it chose a local base policy:\n%s", stdout)
	}
}

// TestProjectInitAsksAgainRatherThanFailingAtTheEnd checks that an answer Feat
// cannot use is refused where it was typed.
func TestProjectInitAsksAgainRatherThanFailingAtTheEnd(t *testing.T) {
	m := prepareWizard(t)
	missing := filepath.Join(m.home, "repos", "app", "nowhere")

	code, stdout, stderr := m.converse(t, answers(
		"Not An Id",         // rejected: not a project identifier
		"app",               // accepted
		"",                  // display name
		missing,             // rejected: not a Git repository
		m.repository("api"), // accepted
		"api", "", "n", "", "", "n", "", "", "", "n",
	), "project", "init")

	if code != ExitOK {
		t.Fatalf("exit code %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "must start with a lowercase letter") {
		t.Errorf("a rejected identifier does not say why:\n%s", stdout)
	}
	if !strings.Contains(stdout, "is not inside a Git repository") {
		t.Errorf("a rejected path does not say why:\n%s", stdout)
	}
	m.load(t, "app")
}

// TestProjectInitWritesNothingUntilItIsConfirmed checks that the file is the
// user's decision, not the end of the questions.
func TestProjectInitWritesNothingUntilItIsConfirmed(t *testing.T) {
	m := prepareWizard(t)

	code, stdout, stderr := m.converse(t, answers(
		"app", "", m.repository("api"), "api", "", "n", "", "", "n", "",
		"n", // do not write it
	), "project", "init")

	if code != ExitOK {
		t.Fatalf("exit code %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "Nothing was written.") {
		t.Errorf("the wizard does not say that it wrote nothing:\n%s", stdout)
	}
	if _, err := os.Stat(filepath.Join(m.layout.ProjectConfigDir(), "app.yaml")); !os.IsNotExist(err) {
		t.Errorf("a configuration file exists after the user declined: %v", err)
	}
}

// TestProjectInitDryRunWritesNothing checks the flag that exists to see the
// file without having it.
func TestProjectInitDryRunWritesNothing(t *testing.T) {
	m := prepareWizard(t)

	code, stdout, stderr := m.converse(t, answers(
		"app", "", m.repository("api"), "api", "", "n", "", "", "n", "",
	), "project", "init", "--dry-run")

	if code != ExitOK {
		t.Fatalf("exit code %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "this was a dry run") {
		t.Errorf("the dry run does not say it was one:\n%s", stdout)
	}
	if !strings.Contains(stdout, "primary_repository: api") {
		t.Errorf("the dry run does not print the configuration:\n%s", stdout)
	}
	if _, err := os.Stat(filepath.Join(m.layout.ProjectConfigDir(), "app.yaml")); !os.IsNotExist(err) {
		t.Errorf("a dry run wrote a configuration file: %v", err)
	}
}

// TestProjectInitRefusesToReplaceAConfiguration checks that the command a user
// runs twice by mistake cannot cost them the file they already had.
func TestProjectInitRefusesToReplaceAConfiguration(t *testing.T) {
	m := prepareWizard(t)
	const existing = "# hand written\n"
	m.configure(t, "app", existing)

	code, stdout, stderr := m.converse(t, answers("y"), "project", "init", "app")

	if code != ExitError {
		t.Fatalf("exit code %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "is already configured at") {
		t.Errorf("the refusal does not name the file:\n%s", stderr)
	}

	body, err := os.ReadFile(filepath.Join(m.layout.ProjectConfigDir(), "app.yaml"))
	if err != nil {
		t.Fatalf("reading the existing configuration: %v", err)
	}
	if string(body) != existing {
		t.Errorf("the existing configuration was changed to:\n%s", body)
	}
}

// TestProjectInitNeedsATerminal checks that a command made of questions says so
// where there is nobody to answer them, and names the other route.
func TestProjectInitNeedsATerminal(t *testing.T) {
	m := prepareWizard(t)

	var stdout, stderr bytes.Buffer
	code := execute(context.Background(), Options{
		Layout:      &m.layout,
		Environment: &m.env,
		Runner:      m.runner,
	}, []string{"project", "init"}, &stdout, &stderr)

	reported := stderr.String()
	if code != ExitError {
		t.Fatalf("exit code %d\nstderr:\n%s", code, reported)
	}
	if !strings.Contains(reported, "needs a terminal") {
		t.Errorf("the refusal does not say why:\n%s", reported)
	}
	if !strings.Contains(reported, m.layout.ProjectConfigDir()) {
		t.Errorf("the refusal does not say where a hand-written file goes:\n%s", reported)
	}
}

// TestProjectInitStopsWhenTheAnswersRunOut checks that input which ends early
// is not read as agreement with everything that was left.
func TestProjectInitStopsWhenTheAnswersRunOut(t *testing.T) {
	m := prepareWizard(t)

	code, stdout, stderr := m.converse(t, answers("app", "", m.repository("api"), "api"), "project", "init")

	if code != ExitError {
		t.Fatalf("exit code %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "the answers ended") {
		t.Errorf("the failure does not say what happened:\n%s", stderr)
	}
	if _, err := os.Stat(filepath.Join(m.layout.ProjectConfigDir(), "app.yaml")); !os.IsNotExist(err) {
		t.Errorf("a configuration file exists after the answers ran out: %v", err)
	}
}

// TestProjectInitChecksTheProjectAgainstTheMachine checks the offer the wizard
// makes once the file exists: the questions could not ask the host anything,
// and this is where that is answered.
func TestProjectInitChecksTheProjectAgainstTheMachine(t *testing.T) {
	m := prepareWizard(t)

	code, stdout, stderr := m.converse(t, answers(
		"app", "", m.repository("api"), "api", "", "n", "", "", "n", "",
		"",  // write it
		"y", // check it against this machine
	), "project", "init")

	if code != ExitOK {
		t.Fatalf("exit code %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "project app") || !strings.Contains(stdout, "repositories.api") {
		t.Errorf("the wizard did not diagnose the project it wrote:\n%s", stdout)
	}
	// No daemon is running on this machine, so the last thing it says is the
	// two commands that follow.
	if !strings.Contains(stdout, "feat project add app") {
		t.Errorf("the wizard does not say how to register the project:\n%s", stdout)
	}
}

// TestProjectInitRegistersWithARunningDaemon checks the offer that closes the
// whole setup path, from an unconfigured machine to a registered project.
func TestProjectInitRegistersWithARunningDaemon(t *testing.T) {
	m := prepareWizard(t)
	m.serve(t)

	code, stdout, stderr := m.converse(t, answers(
		"app", "", m.repository("api"), "api", "", "n", "", "", "n", "",
		"",  // write it
		"n", // do not diagnose
		"y", // register it
	), "project", "init")

	if code != ExitOK {
		t.Fatalf("exit code %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "registered app") {
		t.Errorf("the wizard does not report the registration:\n%s", stdout)
	}

	code, stdout, _ = m.converse(t, "", "project", "list")
	if code != ExitOK || !strings.Contains(stdout, "app") {
		t.Errorf("the registered project is not listed (exit %d):\n%s", code, stdout)
	}
}

// TestTheBackendBuildsTheWizardTheDashboardAsks checks the dashboard's half of
// the wiring: it drives the questions itself, and everything underneath them —
// the configuration directory, the host that runs Git, the file that gets
// written — is built here (ADR-063).
//
// The questions themselves are internal/wizard's and are tested there. What
// this checks is that a wizard built the way the dashboard builds one composes
// a configuration this machine can load.
func TestTheBackendBuildsTheWizardTheDashboardAsks(t *testing.T) {
	m := prepareWizard(t)
	dashboard := &backend{env: &environment{
		layout:  &m.layout,
		process: &m.env,
		runner:  m.runner,
	}}

	flow, err := dashboard.NewWizard()
	if err != nil {
		t.Fatalf("building the wizard the dashboard asks: %v", err)
	}

	for _, answer := range []string{
		"app",               // project identifier
		"Example",           // display name
		m.repository("api"), // path of the checkout
		"api",               // repository identifier
		"",                  // default access: read_write
		"n",                 // no second repository
		"",                  // execution mode: host
		"n",                 // no application services
		"",                  // no verification command
	} {
		if err := flow.Answer(context.Background(), answer); err != nil {
			t.Fatalf("answering %q: %v", answer, err)
		}
	}
	if !flow.Complete() {
		question, _ := flow.Step()
		t.Fatalf("the flow is still asking %s", question.ID)
	}

	file, err := dashboard.WriteProject(flow)
	if err != nil {
		t.Fatalf("writing the configuration: %v", err)
	}
	if want := filepath.Join(m.layout.ProjectConfigDir(), "app.yaml"); file != want {
		t.Errorf("wrote %s, want %s", file, want)
	}

	cfg := m.load(t, "app")
	if cfg.Project.Name != "Example" {
		t.Errorf("project.name is %q, want %q", cfg.Project.Name, "Example")
	}
	// Read from the checkout rather than asked for, through the host the backend
	// supplied.
	if repository, ok := cfg.Repository("api"); !ok || repository.DefaultBranch != "main" {
		t.Errorf("the repository the dashboard's wizard wrote is %+v", repository)
	}
}

// TestInspectAndComposeDiscoveryFeedTheProposals checks the two discoveries the
// wizard's proposals are built from, against the fake host the tests use.
func TestInspectAndComposeDiscoveryFeedTheProposals(t *testing.T) {
	m := prepareWizard(t)

	checkout, err := project.Inspect(context.Background(), m.runner, m.repository("api"))
	if err != nil {
		t.Fatalf("inspecting a checkout: %v", err)
	}
	if checkout.Remote != "origin" || checkout.DefaultBranch != "main" {
		t.Errorf("inspection found remote %q and branch %q", checkout.Remote, checkout.DefaultBranch)
	}

	files := project.ComposeFiles(m.repository("api"))
	if len(files) != 1 || filepath.Base(files[0]) != "compose.yaml" {
		t.Fatalf("Compose discovery found %v", files)
	}
	if services := project.ComposeServices(files...); strings.Join(services, ",") != "dev,worker" {
		t.Errorf("the Compose file defines %v", services)
	}
}

// TestTheDashboardGetsTheChecksTheCommandRuns is the other half of the
// dashboard's diagnosis: the checks are `feat doctor`'s, and what crosses to the
// screen is data (ADR-064).
func TestTheDashboardGetsTheChecksTheCommandRuns(t *testing.T) {
	m := prepareWizard(t)
	m.configure(t, "app", projectFixture)

	// A client for a socket no daemon is listening on, which is the machine a
	// first diagnosis runs on: registration is one of the things being checked,
	// so a daemon that cannot be reached is an answer rather than a failure.
	caller := client.New(m.layout.Socket)
	defer caller.Close()

	dashboard := &backend{client: caller, env: &environment{
		layout:  &m.layout,
		process: &m.env,
		runner:  m.runner,
	}}

	report, err := dashboard.Diagnose(context.Background(), "app")
	if err != nil {
		t.Fatalf("diagnosing: %v", err)
	}

	if len(report.Projects) != 1 || report.Projects[0].ID != "app" {
		t.Fatalf("the report covers %d projects, want the one that was named", len(report.Projects))
	}
	if len(report.Projects[0].Findings) == 0 {
		t.Error("the project was checked and reported nothing")
	}
	if len(report.Host) == 0 {
		t.Error("the machine itself was not checked")
	}
	// What the checks are true of. They ran in this process, and the daemon that
	// launches agents is another one with an environment of its own.
	if report.Environment == "" {
		t.Error("the report does not say where the checks ran")
	}

	// Every finding arrives with a severity the screen knows how to draw, and an
	// action wherever one is required.
	known := map[string]bool{
		api.SeverityOK: true, api.SeveritySkipped: true,
		api.SeverityWarning: true, api.SeverityError: true,
	}
	for _, finding := range append(report.Host, report.Projects[0].Findings...) {
		if !known[finding.Severity] {
			t.Errorf("%s carries severity %q, which no screen renders", finding.Check, finding.Severity)
		}
		if finding.Severity == api.SeverityError && finding.Action == "" {
			t.Errorf("%s reports a problem with nothing to do about it", finding.Check)
		}
	}
}
