package config_test

import (
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/config"
)

// previousShape is the configuration this build stopped reading: one container
// path per repository, and the application's Compose files and services in a
// global runtime section.
const previousShape = `version: 1

project:
  id: app
  name: Example Application
  primary_repository: api

repositories:
  api:
    host_path: ~/repos/app/api
    container_path: /srv/api
    default_branch: main
    remote: origin
    default_access: read_write

git:
  worktree_root: "~/.local/share/feat/worktrees/{project_id}/{task_id}"

agent:
  execution:
    mode: host

runtime:
  provider: compose
  compose_files:
    - ~/repos/app/api/docker-compose.yml
  services:
    - api
`

// TestThePreviousShapeIsRefusedByName covers the rule the rename came with.
//
// There is no version bump and no compatibility period: Feat is used by its
// author and nobody else. The break is still a break the user should not have to
// diagnose, and strict decoding would report each of these as an unknown key —
// which is what it says about a typo, so a user reading it goes looking for a
// spelling mistake in a field they spelled correctly.
func TestThePreviousShapeIsRefusedByName(t *testing.T) {
	dir := write(t, "app.yaml", previousShape)
	opts, _ := testOptions(t, nil)

	_, err := config.Load(dir, "app", opts)
	if err == nil {
		t.Fatal("a configuration in the previous shape loaded")
	}
	invalid := configError(t, err)

	for _, expected := range []struct {
		path        string
		replacement []string
	}{
		{
			path: "repositories.api.container_path",
			replacement: []string{
				"repositories.api.agent.container_path",
				"repositories.api.runtime.container_path",
			},
		},
		{
			path:        "runtime.compose_files",
			replacement: []string{"repositories.<id>.runtime.compose_files"},
		},
		{
			path:        "runtime.services",
			replacement: []string{"repositories.<id>.runtime.services"},
		},
	} {
		problem := problemAt(t, invalid, expected.path)
		for _, name := range expected.replacement {
			if !strings.Contains(problem.Reason, name) {
				t.Errorf("the problem at %s does not name %s: %q", expected.path, name, problem.Reason)
			}
		}
		if strings.Contains(problem.Reason, "unknown field") {
			t.Errorf("the problem at %s is reported as an unknown field: %q", expected.path, problem.Reason)
		}
	}
}

// TestTheReplacementIsReportedInPlace checks that the break is shown where it
// is, which is most of what makes it one round trip rather than three.
func TestTheReplacementIsReportedInPlace(t *testing.T) {
	dir := write(t, "app.yaml", previousShape)
	opts, _ := testOptions(t, nil)

	_, err := config.Load(dir, "app", opts)
	annotated := configError(t, err).Annotated()

	if !strings.Contains(annotated, "container_path: /srv/api") {
		t.Errorf("the rejection does not show the line it is about:\n%s", annotated)
	}
}

// TestTheCurrentShapeIsNotMistakenForThePrevious keeps the check from firing on
// the fields that replaced the ones it names.
//
// Both shapes have a `container_path`, one nested and one not, and both have a
// `runtime`. A check that matched on the leaf name would refuse every valid
// configuration this build writes.
func TestTheCurrentShapeIsNotMistakenForThePrevious(t *testing.T) {
	dir := write(t, "app.yaml", fixture(t, "app.yaml"))
	opts, _ := testOptions(t, nil)

	if _, err := config.Load(dir, "app", opts); err != nil {
		t.Fatalf("the current shape was refused: %v", err)
	}
}
