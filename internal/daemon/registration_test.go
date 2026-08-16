package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/paths"
	"github.com/ma8el/feat/internal/store"
)

// registrationFixture is a complete configuration, written to the daemon's own
// configuration directory. The repository names are generic on purpose: nothing
// about the reference project may reach the binary (CLAUDE.md scope rule 3).
const registrationFixture = `version: 1

project:
  id: app
  name: Example Application
  primary_repository: api

repositories:
  api:
    host_path: ~/repos/app/api
    agent:
      container_path: /srv/api
    default_access: read_write
  store:
    host_path: ~/repos/app/store
    agent:
      container_path: /srv/store
    default_access: selectable

agent:
  execution:
    mode: devcontainer
    compose_files:
      - ~/repos/app/compose.yml
    service: dev
    user: developer
    working_directory: /srv/api
    control_path: /feat
`

// configured writes a project configuration into a daemon's configuration
// directory and returns the environment the daemon resolves it against.
func configured(t *testing.T, layout paths.Layout, id, body string) paths.Environment {
	t.Helper()

	dir := layout.ProjectConfigDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("creating the configuration directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".yaml"), []byte(body), 0o600); err != nil {
		t.Fatalf("writing the configuration: %v", err)
	}

	home := t.TempDir()
	return paths.Environment{
		Getenv: func(string) string { return "" },
		Home:   home,
		UID:    os.Getuid(),
		GOOS:   "darwin",
	}
}

// TestDaemonIsTheOnlyWriterOfProjectState is the behavioural form of the slice 2
// acceptance criterion that the daemon is the sole state writer.
//
// Slice 2 could only check it structurally, as ADR-027 recorded in advance,
// because nothing wrote persistent state yet. Registering a project is the first
// write, and it goes through the socket: the client sends an identifier, and the
// snapshot appears in the daemon's state directory.
func TestDaemonIsTheOnlyWriterOfProjectState(t *testing.T) {
	layout := testLayout(t)
	env := configured(t, layout, "app", registrationFixture)
	live := serve(t, Options{Layout: layout, Environment: env})

	registration, err := live.client(t).RegisterProject(context.Background(), "app")
	if err != nil {
		t.Fatalf("RegisterProject: %v", err)
	}

	if !registration.Created {
		t.Error("registering a new project did not report it as created")
	}
	if registration.Project.ID != "app" {
		t.Errorf("registered project id = %q, want %q", registration.Project.ID, "app")
	}
	if got, want := registration.Project.PrimaryRepository, "api"; got != want {
		t.Errorf("primary repository = %q, want %q", got, want)
	}
	if len(registration.Project.Repositories) != 2 {
		t.Fatalf("registered %d repositories, want 2", len(registration.Project.Repositories))
	}

	// The snapshot is on disk, written by the daemon, in the layout
	// docs/06-technical-architecture.md specifies.
	snapshot := filepath.Join(layout.State, "projects", "app", "project.json")
	body, err := os.ReadFile(snapshot)
	if err != nil {
		t.Fatalf("the daemon did not write %s: %v", snapshot, err)
	}
	if !strings.Contains(string(body), `"schema_version"`) {
		t.Errorf("the snapshot carries no schema version:\n%s", body)
	}

	// And it is readable through the API that could not see it a moment ago.
	projects, err := live.client(t).Projects(context.Background())
	if err != nil {
		t.Fatalf("Projects: %v", err)
	}
	if len(projects) != 1 || projects[0].ID != "app" {
		t.Errorf("the registered project is not listed: %+v", projects)
	}
}

// TestRegistrationResolvesPathsAgainstTheDaemonsEnvironment checks whose home
// directory a "~" in project configuration means.
//
// It is the daemon's, not the caller's. The daemon owns the state it writes, and
// a client that could change what "~" resolves to would decide where another
// user's repositories were looked for.
func TestRegistrationResolvesPathsAgainstTheDaemonsEnvironment(t *testing.T) {
	layout := testLayout(t)
	env := configured(t, layout, "app", registrationFixture)
	live := serve(t, Options{Layout: layout, Environment: env})

	registration, err := live.client(t).RegisterProject(context.Background(), "app")
	if err != nil {
		t.Fatalf("RegisterProject: %v", err)
	}

	want := filepath.Join(env.Home, "repos", "app", "api")
	for _, repository := range registration.Project.Repositories {
		if repository.ID != "api" {
			continue
		}
		if repository.HostPath != want {
			t.Errorf("api host path = %q, want %q", repository.HostPath, want)
		}
		if repository.ContainerPath != "/srv/api" {
			t.Errorf("api container path = %q, want /srv/api", repository.ContainerPath)
		}
		return
	}
	t.Error("the registered project has no repository api")
}

// TestReregisteringPicksUpAnEditedConfiguration checks the behaviour a user gets
// when they fix their YAML and run the command again.
func TestReregisteringPicksUpAnEditedConfiguration(t *testing.T) {
	layout := testLayout(t)
	env := configured(t, layout, "app", registrationFixture)
	live := serve(t, Options{Layout: layout, Environment: env})
	caller := live.client(t)

	first, err := caller.RegisterProject(context.Background(), "app")
	if err != nil {
		t.Fatalf("RegisterProject: %v", err)
	}

	edited := strings.Replace(registrationFixture, "  name: Example Application", "  name: Renamed", 1)
	configured(t, layout, "app", edited)

	second, err := caller.RegisterProject(context.Background(), "app")
	if err != nil {
		t.Fatalf("re-registering: %v", err)
	}

	if second.Created {
		t.Error("re-registering reported the project as newly created")
	}
	if second.Project.Name != "Renamed" {
		t.Errorf("project name = %q, want the edited %q", second.Project.Name, "Renamed")
	}
	// The registration time is when Feat first learned about the project, not
	// when its YAML was last edited.
	if !second.Project.CreatedAt.Equal(first.Project.CreatedAt) {
		t.Errorf("created at %v, want the original %v", second.Project.CreatedAt, first.Project.CreatedAt)
	}
	if second.Project.UpdatedAt.Before(first.Project.UpdatedAt) {
		t.Errorf("updated at %v precedes the first registration at %v",
			second.Project.UpdatedAt, first.Project.UpdatedAt)
	}

	projects, err := caller.Projects(context.Background())
	if err != nil {
		t.Fatalf("Projects: %v", err)
	}
	if len(projects) != 1 {
		t.Errorf("re-registering recorded %d projects, want 1", len(projects))
	}
}

// TestRegisteringAnInvalidConfigurationChangesNothing checks that a rejected
// configuration leaves no partial record behind.
func TestRegisteringAnInvalidConfigurationChangesNothing(t *testing.T) {
	layout := testLayout(t)
	broken := strings.Replace(registrationFixture, "    user: developer", "    user: root", 1)
	env := configured(t, layout, "app", broken)
	live := serve(t, Options{Layout: layout, Environment: env})
	caller := live.client(t)

	_, err := caller.RegisterProject(context.Background(), "app")
	if err == nil {
		t.Fatal("an invalid configuration was registered")
	}
	// The message has to survive the socket: a user fixing their YAML needs to
	// know which field is wrong, not that something was.
	if !strings.Contains(err.Error(), "agent.execution.user") {
		t.Errorf("error does not name the field: %v", err)
	}

	projects, err := caller.Projects(context.Background())
	if err != nil {
		t.Fatalf("Projects: %v", err)
	}
	if len(projects) != 0 {
		t.Errorf("a rejected configuration left %d projects behind", len(projects))
	}
	if _, err := os.Stat(filepath.Join(layout.State, "projects", "app")); err == nil {
		t.Error("a rejected configuration left a project directory behind")
	}
}

// TestRegisteringAnUnconfiguredProjectIsNotFound checks that "no such file" and
// "the file is wrong" stay different answers.
func TestRegisteringAnUnconfiguredProjectIsNotFound(t *testing.T) {
	layout := testLayout(t)
	env := configured(t, layout, "app", registrationFixture)
	live := serve(t, Options{Layout: layout, Environment: env})

	_, err := live.client(t).RegisterProject(context.Background(), "absent")

	if err == nil {
		t.Fatal("a project with no configuration was registered")
	}
	if !notFound(err) {
		t.Errorf("error = %v, want a 404", err)
	}
	if !strings.Contains(err.Error(), "absent.yaml") {
		t.Errorf("error does not say where the file belongs: %v", err)
	}
}

// TestRegistrationRejectsAnIdentifierThatEscapesTheConfigurationDirectory checks
// that a caller cannot use the identifier to name a file of its choosing.
func TestRegistrationRejectsAnIdentifierThatEscapesTheConfigurationDirectory(t *testing.T) {
	layout := testLayout(t)
	env := configured(t, layout, "app", registrationFixture)
	live := serve(t, Options{Layout: layout, Environment: env})

	outside := filepath.Join(t.TempDir(), "elsewhere.yaml")
	if err := os.WriteFile(outside, []byte(registrationFixture), 0o600); err != nil {
		t.Fatalf("writing the outside configuration: %v", err)
	}

	for _, id := range []string{
		"../elsewhere",
		"../../etc/passwd",
		strings.TrimSuffix(outside, ".yaml"),
	} {
		if _, err := live.client(t).RegisterProject(context.Background(), id); err == nil {
			t.Errorf("identifier %q was accepted", id)
		}
	}

	projects, err := live.client(t).Projects(context.Background())
	if err != nil {
		t.Fatalf("Projects: %v", err)
	}
	if len(projects) != 0 {
		t.Errorf("a path-like identifier registered %d projects", len(projects))
	}
}

// TestConcurrentRegistrationsRecordOneProject checks that the single writer is
// serialised, since the daemon serves several clients at once.
func TestConcurrentRegistrationsRecordOneProject(t *testing.T) {
	layout := testLayout(t)
	env := configured(t, layout, "app", registrationFixture)
	live := serve(t, Options{Layout: layout, Environment: env})

	const callers = 8
	errs := make(chan error, callers)
	for i := 0; i < callers; i++ {
		go func() {
			_, err := live.client(t).RegisterProject(context.Background(), "app")
			errs <- err
		}()
	}
	for i := 0; i < callers; i++ {
		if err := <-errs; err != nil {
			t.Errorf("concurrent registration: %v", err)
		}
	}

	projects, err := live.client(t).Projects(context.Background())
	if err != nil {
		t.Fatalf("Projects: %v", err)
	}
	if len(projects) != 1 {
		t.Errorf("%d concurrent registrations recorded %d projects, want 1", callers, len(projects))
	}

	// The stored snapshot has to be one complete document, not two interleaved
	// ones, which is what atomic replacement is for.
	loaded, err := live.daemon.Store().Projects().Load(context.Background(), domain.ProjectID("app"))
	if err != nil {
		t.Fatalf("loading the registered project: %v", err)
	}
	if loaded.ID != "app" {
		t.Errorf("stored project id = %q, want %q", loaded.ID, "app")
	}
	if _, err := live.daemon.Store().Projects().Load(context.Background(), "absent"); err == nil {
		t.Error("an unregistered project loaded")
	} else if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("error = %v, want a storage not-found", err)
	}
}
