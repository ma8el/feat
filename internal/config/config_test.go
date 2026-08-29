package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/goccy/go-yaml"

	"github.com/ma8el/feat/internal/config"
	"github.com/ma8el/feat/internal/paths"
)

// testEnvironment returns an environment rooted at a temporary home, so that a
// test never expands a path into the developer's own home directory.
func testEnvironment(t *testing.T, variables map[string]string) (paths.Environment, string) {
	t.Helper()
	home := t.TempDir()
	return paths.Environment{
		Getenv: func(key string) string { return variables[key] },
		Home:   home,
		UID:    501,
		GOOS:   "darwin",
	}, home
}

// testOptions returns resolution options rooted at a temporary home.
func testOptions(t *testing.T, variables map[string]string) (config.Options, string) {
	t.Helper()
	env, home := testEnvironment(t, variables)
	return config.Options{Env: env, StateDir: filepath.Join(home, ".local", "share", "feat")}, home
}

// write puts a configuration file in a temporary directory and returns the
// directory.
func write(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
	return dir
}

// fixture reads one of the testdata configurations.
func fixture(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "projects", name))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	return string(body)
}

// configError asserts that err is a configuration error and returns it.
func configError(t *testing.T, err error) *config.Error {
	t.Helper()
	if err == nil {
		t.Fatal("expected the configuration to be rejected, but it was accepted")
	}
	var invalid *config.Error
	if !errors.As(err, &invalid) {
		t.Fatalf("expected a *config.Error, got %T: %v", err, err)
	}
	return invalid
}

// problemAt returns the problem reported for a configuration path.
func problemAt(t *testing.T, err *config.Error, path string) config.Problem {
	t.Helper()
	for _, problem := range err.Problems {
		if problem.Path == path {
			return problem
		}
	}
	t.Fatalf("no problem reported for %q; got %s", path, err)
	return config.Problem{}
}

// TestUnknownFieldFailsWithLocationAndMessage covers the requirement that
// unknown YAML fields fail with a useful location and message.
//
// Useful means three things, and each is asserted separately: the message names
// the field, it says where the field is, and the file is not loaded anyway.
func TestUnknownFieldFailsWithLocationAndMessage(t *testing.T) {
	const body = `version: 1
project:
  id: app
  primary_repository: api
repositories:
  api:
    host_path: ~/repos/api
    default_acess: read_write
agent:
  execution:
    mode: host
`
	dir := write(t, "app.yaml", body)
	opts, _ := testOptions(t, nil)

	_, err := config.Load(dir, "app", opts)
	invalid := configError(t, err)

	message := invalid.Error()
	if !strings.Contains(message, `unknown field "default_acess"`) {
		t.Errorf("the message does not name the unknown field:\n%s", message)
	}
	// The mistyped field is on line 8, column 5. A message that says only that
	// something is wrong leaves the user to find it in a nested document.
	if !strings.Contains(message, "[8:5]") {
		t.Errorf("the message does not locate the unknown field:\n%s", message)
	}

	// The annotated form is what a terminal prints, and it shows the line in
	// place rather than making the user count lines.
	annotated := invalid.Annotated()
	if !strings.Contains(annotated, "default_acess: read_write") {
		t.Errorf("the annotated message does not show the offending line:\n%s", annotated)
	}
	if !strings.Contains(annotated, dir) {
		t.Errorf("the annotated message does not name the file:\n%s", annotated)
	}
}

// TestUnknownFieldIsRejectedAtEveryDepth keeps strictness from being a property
// of the top level only. A typo in a nested mapping is the more likely one.
func TestUnknownFieldIsRejectedAtEveryDepth(t *testing.T) {
	base := fixture(t, "app.yaml")

	for name, replacement := range map[string][2]string{
		"top level":     {"version: 1", "version: 1\nunexpected: true"},
		"project":       {"  name: Example Application", "  name: Example Application\n  owner: someone"},
		"repository":    {"    container_path: /srv/api", "    container_path: /srv/api\n    mount: rw"},
		"execution":     {"    service: dev", "    service: dev\n    privileged: true"},
		"capabilities":  {"    docker: denied", "    docker: denied\n    kubernetes: allowed"},
		"runtime":       {"  start_policy: manual", "  start_policy: manual\n  restart: always"},
		"check element": {"      execution: agent", "      execution: agent\n      timeout: 30"},
	} {
		t.Run(name, func(t *testing.T) {
			body := strings.Replace(base, replacement[0], replacement[1], 1)
			if body == base {
				t.Fatalf("the fixture no longer contains %q", replacement[0])
			}
			dir := write(t, "app.yaml", body)
			opts, _ := testOptions(t, nil)

			_, err := config.Load(dir, "app", opts)
			invalid := configError(t, err)
			if !strings.Contains(invalid.Error(), "unknown field") {
				t.Errorf("expected an unknown-field error, got:\n%s", invalid)
			}
		})
	}
}

// TestARemovedCapabilityFailsNamingItself is the whole migration ADR-080 needs.
//
// No `replaced` entry was added for either key, on ADR-075's reasoning: that
// mechanism earns its place where a field has a successor to name, and these
// have none. What is left has to be enough on its own, so it is asserted rather
// than assumed — a file carrying either key fails to load, and the message
// names the key whose line is to be deleted.
func TestARemovedCapabilityFailsNamingItself(t *testing.T) {
	base := fixture(t, "app.yaml")

	for _, removed := range []struct{ key, line string }{
		{"network", "    network: unrestricted"},
		{"git", "    git: full"},
	} {
		t.Run(removed.key, func(t *testing.T) {
			const anchor = "    docker: denied"
			body := strings.Replace(base, anchor, anchor+"\n"+removed.line, 1)
			if body == base {
				t.Fatalf("the fixture no longer contains %q", anchor)
			}
			dir := write(t, "app.yaml", body)
			opts, _ := testOptions(t, nil)

			_, err := config.Load(dir, "app", opts)
			invalid := configError(t, err)
			if !strings.Contains(invalid.Error(), "unknown field") {
				t.Errorf("expected an unknown-field error, got:\n%s", invalid)
			}
			if !strings.Contains(invalid.Error(), removed.key) {
				t.Errorf("the error does not name %q, so it does not say which line to delete:\n%s",
					removed.key, invalid)
			}
		})
	}
}

// TestDuplicateKeyIsRejected covers the other way a hand-edited file silently
// loses a value: writing the same key twice keeps the last one.
func TestDuplicateKeyIsRejected(t *testing.T) {
	const body = `version: 1
project:
  id: app
  primary_repository: api
  primary_repository: web
repositories:
  api:
    host_path: ~/repos/api
    default_access: read_write
agent:
  execution:
    mode: host
`
	dir := write(t, "app.yaml", body)
	opts, _ := testOptions(t, nil)

	_, err := config.Load(dir, "app", opts)
	invalid := configError(t, err)
	if !strings.Contains(invalid.Error(), "already defined") {
		t.Errorf("expected a duplicate-key error, got:\n%s", invalid)
	}
}

// TestCompleteConfigurationResolves checks that a full configuration loads and
// that resolution produced the values Feat will act on, not the file's text.
func TestCompleteConfigurationResolves(t *testing.T) {
	dir := write(t, "app.yaml", fixture(t, "app.yaml"))
	opts, home := testOptions(t, nil)

	loaded, err := config.Load(dir, "app", opts)
	if err != nil {
		t.Fatalf("loading a valid configuration: %v", err)
	}

	if got, want := loaded.ID(), "app"; got != want {
		t.Errorf("project id = %q, want %q", got, want)
	}
	if got, want := loaded.RepositoryIDs(), []string{"api", "infra", "web"}; !equal(got, want) {
		t.Errorf("repository ids = %v, want %v", got, want)
	}

	// "~" is expanded against the supplied home, so nothing resolves into the
	// developer's own home directory.
	api, _ := loaded.Repository("api")
	if want := filepath.Join(home, "repos", "app", "api"); api.HostPath != want {
		t.Errorf("api host_path = %q, want %q", api.HostPath, want)
	}
	if strings.Contains(loaded.Git.WorktreeRoot, "~") {
		t.Errorf("worktree_root was not expanded: %q", loaded.Git.WorktreeRoot)
	}

	if got, want := loaded.Agent.Claude.IdleGrace(), 5*time.Second; got != want {
		t.Errorf("idle grace = %v, want %v", got, want)
	}
	if !loaded.HasRuntime() {
		t.Error("the configuration declares a runtime, but HasRuntime reports none")
	}

	// The tracker and the forge are separate sections with separate owners:
	// where the code goes and where the tickets come from are different
	// questions, and this project answers both (ADR-071).
	if loaded.Tracker == nil {
		t.Fatal("the configuration declares a tracker and none was loaded")
	}
	if got, want := strings.Join(loaded.Tracker.Command, " "), "tickets-for-me"; got != want {
		t.Errorf("tracker command = %q, want %q", got, want)
	}
	if api.Forge == nil {
		t.Fatal("the api repository declares a forge and none was loaded")
	}
	if got, want := api.Forge.Kind, "gitlab"; got != want {
		t.Errorf("api forge = %q, want %q", got, want)
	}
}

// TestDefaultsAreFilledIn checks the minimal configuration, where almost
// everything comes from a default. The defaults have to be visible, because
// `feat project show` is how a user checks what Feat will do.
func TestDefaultsAreFilledIn(t *testing.T) {
	dir := write(t, "minimal.yaml", fixture(t, "minimal.yaml"))
	opts, home := testOptions(t, map[string]string{"EDITOR": "nvim"})

	loaded, err := config.Load(dir, "minimal", opts)
	if err != nil {
		t.Fatalf("loading the minimal configuration: %v", err)
	}

	only, _ := loaded.Repository("only")
	for _, field := range []struct {
		name string
		got  string
		want string
	}{
		{"project.name", loaded.Project.Name, "minimal"},
		{"repository name", only.Name, "only"},
		{"remote", only.Remote, "origin"},
		{"default branch", only.DefaultBranch, "main"},
		{"base policy", loaded.Git.BasePolicy, config.PolicyRemote},
		{"branch template", loaded.Git.BranchTemplate, "feat/{task_key}-{slug}"},
		{"agent provider", loaded.Agent.Provider, config.ProviderClaude},
		{"docker capability", loaded.Agent.Capabilities.Docker, config.CapabilityDenied},
	} {
		if field.got != field.want {
			t.Errorf("%s = %q, want %q", field.name, field.got, field.want)
		}
	}

	if !loaded.Git.FetchesBeforeTask() {
		t.Error("fetch_before_task defaults to false; FR-GIT-001 wants remotes fetched")
	}
	if want := filepath.Join(home, ".local", "share", "feat", "worktrees", "{project_id}", "{task_id}"); loaded.Git.WorktreeRoot != want {
		t.Errorf("worktree_root = %q, want %q", loaded.Git.WorktreeRoot, want)
	}
}

// TestDocumentedExampleIsValid keeps the example in docs/examples/project.yaml
// correct.
//
// It is the file a user copies to start from, so a field renamed here without
// updating it would hand every new user a configuration Feat rejects.
func TestDocumentedExampleIsValid(t *testing.T) {
	const example = "../../docs/examples/project.yaml"

	body, err := os.ReadFile(example)
	if err != nil {
		t.Fatalf("reading %s: %v", example, err)
	}

	dir := write(t, "app.yaml", string(body))
	opts, _ := testOptions(t, nil)

	loaded, err := config.Load(dir, "app", opts)
	if err != nil {
		t.Fatalf("the documented example does not validate:\n%v", err)
	}

	// The example claims to show every field with a default. A default the
	// example contradicts would be worse than one it omits.
	for _, field := range []struct {
		name string
		got  string
		want string
	}{
		{"base_policy", loaded.Git.BasePolicy, config.PolicyRemote},
		{"provider", loaded.Agent.Provider, config.ProviderClaude},
		{"docker capability", loaded.Agent.Capabilities.Docker, config.CapabilityDenied},
		{"start policy", loaded.Runtime.StartPolicy, config.StartManual},
	} {
		if field.got != field.want {
			t.Errorf("the example sets %s to %q, and the default is %q", field.name, field.got, field.want)
		}
	}

	// Valid is not the whole of what this file has to be. Every configuration
	// `feat project init` writes points its reader here for the fields it left
	// out, so a section added to the interface and not to the example is one a
	// user following Feat's own instructions cannot discover. `tracker` and
	// `forge` each accumulated exactly that way, because every assertion above
	// is about a field the example already has.
	requireExampleUsesEverySchemaProperty(t, body)
}

// requireExampleUsesEverySchemaProperty fails when the published schema has a
// property the documented example does not use.
//
// It walks the two levels a section is added at — the document's own properties
// and one repository's — and asks the parsed example for each name, so that a
// property counts where the example configures it rather than where a comment
// happens to mention it.
//
// A property that legitimately cannot appear in the example belongs here by
// name, with the reason it cannot, rather than in a walk relaxed until it
// passes: an assertion that cannot fail is what left two sections undocumented.
// There is no such property today, which is why there is no list of them.
func requireExampleUsesEverySchemaProperty(t *testing.T, body []byte) {
	t.Helper()

	root := loadSchema(t)

	var document map[string]any
	if err := yaml.Unmarshal(body, &document); err != nil {
		t.Fatalf("parsing the documented example: %v", err)
	}

	for _, name := range propertyNames(root.Properties) {
		if _, ok := document[name]; !ok {
			t.Errorf("the schema has %q and the documented example does not use it\n"+
				"\tAdd it to docs/examples/project.yaml, which is the file `feat project init` "+
				"tells a user to copy and the one every generated file names as the reference; "+
				"or exempt it here by name, with the reason it cannot appear.", name)
		}
	}

	repositories, ok := document["repositories"].(map[string]any)
	if !ok {
		t.Fatal("the documented example declares no repositories")
	}
	// One repository, not every repository: the example shows a published
	// repository, one that runs no service, and one that is read-only, and each
	// of those is a repository some property is absent from on purpose.
	used := make(map[string]bool)
	for _, value := range repositories {
		repository, ok := value.(map[string]any)
		if !ok {
			continue
		}
		for name := range repository {
			used[name] = true
		}
	}

	shape := additional(t, root, root.Properties["repositories"])
	if shape == nil {
		t.Fatal("the schema describes no shape for a repository")
	}
	for _, name := range propertyNames(shape.Properties) {
		if !used[name] {
			t.Errorf("the schema has repositories.<id>.%s and no repository in the documented example uses it\n"+
				"\tAdd it to one repository in docs/examples/project.yaml, or exempt it here by "+
				"name, with the reason it cannot appear.", name)
		}
	}
}

// propertyNames returns a schema object's property names in a stable order, so
// that two failing runs report them the same way.
func propertyNames(properties map[string]*schema) []string {
	sorted := make([]string, 0, len(properties))
	for name := range properties {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)
	return sorted
}

func equal(got, want []string) bool {
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
