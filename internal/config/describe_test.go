package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/config"
)

// render flattens a description into the text a command would print.
func render(sections []config.Section) string {
	var out strings.Builder
	for _, section := range sections {
		out.WriteString(section.Title)
		out.WriteString("\n")
		for _, field := range section.Fields {
			out.WriteString("  " + field.Name + " = " + field.Value)
			if field.Note != "" {
				out.WriteString("  (" + field.Note + ")")
			}
			out.WriteString("\n")
		}
	}
	return out.String()
}

// TestSecretFileContentsNeverAppearInResolvedConfiguration is the slice 3
// acceptance criterion "Secret file contents never appear in diagnostics".
//
// It is checked as a property of the data rather than of a filter: this package
// records the path of an environment file and never opens it, so there is
// nothing to leak and nothing to redact. The unreadable file proves the second
// half — a package that read the file would fail on it.
func TestSecretFileContentsNeverAppearInResolvedConfiguration(t *testing.T) {
	const secret = "ThisValueMustNeverBePrinted"

	opts, home := testOptions(t, nil)
	envFile := filepath.Join(home, "repos", "app", "api", ".env")
	if err := os.MkdirAll(filepath.Dir(envFile), 0o700); err != nil {
		t.Fatalf("creating the environment file's directory: %v", err)
	}
	if err := os.WriteFile(envFile, []byte("API_TOKEN="+secret+"\n"), 0o600); err != nil {
		t.Fatalf("writing the environment file: %v", err)
	}
	if os.Getuid() != 0 {
		// A package that opens the file cannot pass this test by accident.
		if err := os.Chmod(envFile, 0o000); err != nil {
			t.Fatalf("making the environment file unreadable: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(envFile, 0o600) })
	}

	dir := write(t, "app.yaml", fixture(t, "app.yaml"))
	loaded, err := config.Load(dir, "app", opts)
	if err != nil {
		t.Fatalf("an unreadable environment file must not stop a project from loading: %v", err)
	}

	described := render(loaded.Describe())
	if strings.Contains(described, secret) {
		t.Error("the resolved configuration contains the environment file's contents")
	}
	// The path is expected to be there: the file is passed to Compose by path,
	// and a user checking their configuration needs to see which file that is.
	if !strings.Contains(described, envFile) {
		t.Errorf("the resolved configuration does not name the environment file %s:\n%s", envFile, described)
	}
	if !strings.Contains(described, "contents not read") {
		t.Errorf("the resolved configuration does not say the file is left unread:\n%s", described)
	}
}

// TestRepositoryAndContainerPathsAreDescribedAccurately is the slice 3
// acceptance criterion "Repository/container-path mappings are printed
// accurately".
func TestRepositoryAndContainerPathsAreDescribedAccurately(t *testing.T) {
	dir := write(t, "app.yaml", fixture(t, "app.yaml"))
	opts, home := testOptions(t, nil)

	loaded, err := config.Load(dir, "app", opts)
	if err != nil {
		t.Fatalf("loading a valid configuration: %v", err)
	}

	mounts := loaded.Mounts()
	want := []config.Mount{
		{
			RepositoryID: "api",
			HostPath:     filepath.Join(home, "repos", "app", "api"),
			// The two paths differ, and each is used where it belongs: the agent
			// works at one, and the application's own services expect their
			// source at the other (ADR-065 evidence 5).
			AgentPath:       "/srv/api",
			RuntimePath:     "/app",
			RuntimeServices: []string{"app", "worker"},
			DefaultAccess:   "read_write",
			Primary:         true,
		},
		{
			RepositoryID:  "infra",
			HostPath:      filepath.Join(home, "repos", "app", "infra"),
			AgentPath:     "/srv/infra",
			DefaultAccess: "stable_read_only",
		},
		{
			RepositoryID:  "web",
			HostPath:      filepath.Join(home, "repos", "app", "web"),
			AgentPath:     "/srv/web",
			DefaultAccess: "selectable",
		},
	}

	if len(mounts) != len(want) {
		t.Fatalf("described %d mounts, want %d: %+v", len(mounts), len(want), mounts)
	}
	for i, mount := range mounts {
		if !reflect.DeepEqual(mount, want[i]) {
			t.Errorf("mount %d = %+v, want %+v", i, mount, want[i])
		}
	}
}

// TestMountOrderIsStable checks that a printed table does not depend on Go's
// map iteration order, which would make two runs disagree.
func TestMountOrderIsStable(t *testing.T) {
	dir := write(t, "app.yaml", fixture(t, "app.yaml"))
	opts, _ := testOptions(t, nil)

	first, err := config.Load(dir, "app", opts)
	if err != nil {
		t.Fatalf("loading a valid configuration: %v", err)
	}
	baseline := render(first.Describe())

	for i := 0; i < 20; i++ {
		again, err := config.Load(dir, "app", opts)
		if err != nil {
			t.Fatalf("loading a valid configuration: %v", err)
		}
		if got := render(again.Describe()); got != baseline {
			t.Fatalf("the resolved configuration is not printed in a stable order:\n%s\nwant:\n%s", got, baseline)
		}
	}
}

// TestDescribeShowsResolvedValues checks that the output is what Feat will act
// on rather than what the file says. A default a user cannot see is a default
// they cannot check.
func TestDescribeShowsResolvedValues(t *testing.T) {
	dir := write(t, "minimal.yaml", fixture(t, "minimal.yaml"))
	opts, home := testOptions(t, nil)

	loaded, err := config.Load(dir, "minimal", opts)
	if err != nil {
		t.Fatalf("loading the minimal configuration: %v", err)
	}
	described := render(loaded.Describe())

	for _, expected := range []string{
		"base_policy = remote",
		"branch_template = feat/{task_key}-{slug}",
		"capabilities.docker = denied",
		filepath.Join(home, "repos", "only"),
		filepath.Join(home, ".local", "share", "feat", "worktrees"),
	} {
		if !strings.Contains(described, expected) {
			t.Errorf("the resolved configuration does not contain %q:\n%s", expected, described)
		}
	}
	if strings.Contains(described, "~/") {
		t.Errorf("the resolved configuration still contains an unexpanded path:\n%s", described)
	}
	// A project with no runtime has no runtime section, rather than an empty
	// one that suggests services exist.
	if strings.Contains(described, "runtime\n") {
		t.Errorf("a project with no runtime described one:\n%s", described)
	}
}

// TestTheDockerCapabilityIsGlossedForTheModeItAppliesIn is F6-06 for
// `feat project show`.
//
// The capability value is `denied` in both modes and honest in both. What it
// means is not the same, and one gloss covering both has to be false in one of
// them: a host-mode project was told that no Docker socket and no host Docker
// CLI reach its agent, four lines under `execution.mode host (no container
// boundary)`, about a process that runs as the daemon's owner with that user's
// socket on its path.
func TestTheDockerCapabilityIsGlossedForTheModeItAppliesIn(t *testing.T) {
	opts, _ := testOptions(t, nil)

	host := write(t, "minimal.yaml", fixture(t, "minimal.yaml"))
	loaded, err := config.Load(host, "minimal", opts)
	if err != nil {
		t.Fatalf("loading the host-mode configuration: %v", err)
	}
	described := render(loaded.Describe())

	if !strings.Contains(described, "execution.mode = host") {
		t.Fatalf("the fixture is not host-mode, so this test checks nothing:\n%s", described)
	}
	for _, claim := range []string{
		"no Docker socket and no host Docker CLI reach the agent",
		"a launch refuses a container",
	} {
		if strings.Contains(described, claim) {
			t.Errorf("a host-mode project is told %q, and it has no container:\n%s", claim, described)
		}
	}
	if !strings.Contains(described, "the agent runs as the daemon's own user, with that user's Docker") {
		t.Errorf("a host-mode project is not told what its agent can actually reach:\n%s", described)
	}

	container := write(t, "app.yaml", fixture(t, "app.yaml"))
	loaded, err = config.Load(container, "app", opts)
	if err != nil {
		t.Fatalf("loading the devcontainer configuration: %v", err)
	}
	described = render(loaded.Describe())

	if !strings.Contains(described, "Feat mounts no socket and adds no client") {
		t.Errorf("a devcontainer project is not told what Feat does about Docker:\n%s", described)
	}
	if !strings.Contains(described, "a launch refuses a container that has either") {
		t.Errorf("a devcontainer project is not told what a launch refuses:\n%s", described)
	}
}

// TestTheBindAddressIsGlossedForWhatItGovernsAndNotForEveryPublication scopes a
// claim this command cannot make.
//
// `bind_address` is the default for a publication whose own Compose file names
// no address, and not an address applied over one (docs/07). The note read as an
// answer about the project's services, so a project configured 127.0.0.1 whose
// repository publishes "0.0.0.0:3000:3000" was told its services are reachable
// from this machine alone, over a binding every network this machine is joined
// to can open. This command loads configuration and reads no Compose file, so it
// cannot say what each publication is bound on — it says what this value decides
// and stops there.
func TestTheBindAddressIsGlossedForWhatItGovernsAndNotForEveryPublication(t *testing.T) {
	opts, _ := testOptions(t, nil)

	dir := write(t, "app.yaml", fixture(t, "app.yaml"))
	loaded, err := config.Load(dir, "app", opts)
	if err != nil {
		t.Fatalf("loading the configuration: %v", err)
	}
	described := render(loaded.Describe())

	if !strings.Contains(described, "bind_address = 127.0.0.1") {
		t.Fatalf("the fixture does not resolve to the loopback address, so this test checks nothing:\n%s",
			described)
	}
	// The distinction stays: "0.0.0.0" and "127.0.0.1" look alike in a printed
	// field and differ by who can open the service.
	if !strings.Contains(described, "reachable from this machine only") {
		t.Errorf("the resolved bind address does not say who can reach what it governs:\n%s", described)
	}
	// And it is said of what the value governs rather than of the project.
	if !strings.Contains(described, "where a publication names no address") {
		t.Errorf("the bind address is glossed as though every publication used it:\n%s", described)
	}

	// The wide answer is scoped the same way, and is still the wide answer.
	wide := write(t, "app.yaml", strings.Replace(
		fixture(t, "app.yaml"),
		"project_name_template: \"feat-{project_id}-{task_id}\"",
		"project_name_template: \"feat-{project_id}-{task_id}\"\n  bind_address: \"0.0.0.0\"",
		1))
	loaded, err = config.Load(wide, "app", opts)
	if err != nil {
		t.Fatalf("loading the configuration with a wildcard bind address: %v", err)
	}
	described = render(loaded.Describe())

	if !strings.Contains(described, "bind_address = 0.0.0.0") {
		t.Fatalf("the wildcard bind address was not resolved, so this test checks nothing:\n%s", described)
	}
	for _, required := range []string{
		"where a publication names no address",
		"reachable from every network this machine is on, and from every container on it",
	} {
		if !strings.Contains(described, required) {
			t.Errorf("a wildcard bind address does not say %q:\n%s", required, described)
		}
	}
}

// TestTheHostAgentOverrideIsNamedRatherThanGuessed is the other half of F6-06.
//
// FEAT_HOST_AGENT lives in the daemon's environment (ADR-032) and this command
// loads configuration without asking a daemon anything, so the mode it prints is
// the configured one and may not be the one in force. Naming the variable is
// what a reader needs; reading it from this process would be a second wrong
// claim whenever the daemon was started from another shell.
func TestTheHostAgentOverrideIsNamedRatherThanGuessed(t *testing.T) {
	dir := write(t, "app.yaml", fixture(t, "app.yaml"))
	opts, _ := testOptions(t, nil)

	loaded, err := config.Load(dir, "app", opts)
	if err != nil {
		t.Fatalf("loading the devcontainer configuration: %v", err)
	}
	described := render(loaded.Describe())

	if !strings.Contains(described, config.EnvHostAgent) {
		t.Errorf("a devcontainer project's execution mode does not name what overrides it:\n%s", described)
	}
}
