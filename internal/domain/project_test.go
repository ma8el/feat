package domain

import (
	"errors"
	"strings"
	"testing"
)

// TestProjectValidationRules checks the rules the domain owns for a project's
// topology. Whether a host path really holds a Git repository is a host question
// for project diagnostics; everything here is answerable from the model alone.
func TestProjectValidationRules(t *testing.T) {
	tests := map[string]struct {
		breakIt func(*Project)
		names   string
	}{
		"a project without a name": {
			breakIt: func(p *Project) { p.Name = "" },
			names:   "name",
		},
		"a project without repositories": {
			breakIt: func(p *Project) { p.Repositories = nil },
			names:   "repositories",
		},
		"a repository listed twice": {
			breakIt: func(p *Project) { p.Repositories = append(p.Repositories, p.Repositories[0]) },
			names:   "repositories",
		},
		"a primary repository that is not in the project": {
			breakIt: func(p *Project) { p.PrimaryRepository = "missing" },
			names:   "primary_repository",
		},
		"a primary repository a task cannot edit": {
			breakIt: func(p *Project) { p.Repositories[0].DefaultAccess = DefaultAccessStableReadOnly },
			names:   "primary_repository",
		},
		"a relative host path": {
			breakIt: func(p *Project) { p.Repositories[0].HostPath = "repositories/core" },
			names:   "host_path",
		},
		"a relative container path": {
			breakIt: func(p *Project) { p.Repositories[0].ContainerPath = "src/core" },
			names:   "container_path",
		},
		"a repository without a default branch": {
			breakIt: func(p *Project) { p.Repositories[0].DefaultBranch = "" },
			names:   "default_branch",
		},
		"a repository without a remote": {
			breakIt: func(p *Project) { p.Repositories[0].Remote = "" },
			names:   "remote",
		},
		"an undocumented access mode": {
			breakIt: func(p *Project) { p.Repositories[0].DefaultAccess = "writable" },
			names:   "default_access",
		},
		"a repository identifier that traverses a directory": {
			breakIt: func(p *Project) { p.Repositories[0].ID = "../escape" },
			names:   "id",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			project := testProjectValue(t)
			test.breakIt(project)

			err := project.Validate()
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("%s validated: %v", name, err)
			}
			if !strings.Contains(err.Error(), test.names) {
				t.Errorf("error does not name %q: %v", test.names, err)
			}
			if !strings.Contains(err.Error(), "example") {
				t.Errorf("error does not name the project or repository it is about: %v", err)
			}
		})
	}
}

// TestProjectWithoutAContainerPathIsValid checks that host-native projects, for
// which no mount path exists, are not forced to invent one.
func TestProjectWithoutAContainerPathIsValid(t *testing.T) {
	project := testProjectValue(t)
	for i := range project.Repositories {
		project.Repositories[i].ContainerPath = ""
	}
	if err := project.Validate(); err != nil {
		t.Errorf("a host-native project is rejected: %v", err)
	}
}

// TestProjectRepositoryLookup checks the accessor task preparation uses to
// resolve a binding against the project topology.
func TestProjectRepositoryLookup(t *testing.T) {
	project := testProjectValue(t)

	repository, ok := project.Repository(testSecondary)
	if !ok {
		t.Fatalf("%s is not in the project", testSecondary)
	}
	if repository.DefaultAccess != DefaultAccessSelectable {
		t.Errorf("%s defaults to %s", testSecondary, repository.DefaultAccess)
	}
	if _, ok := project.Repository("missing"); ok {
		t.Error("an unknown repository was found")
	}
}

func testProjectValue(t *testing.T) *Project {
	t.Helper()

	project, err := NewProject(testProject, "Example", testPrimary, []Repository{
		{
			ID:            testPrimary,
			Name:          "Core",
			HostPath:      "/srv/repositories/core",
			ContainerPath: "/src/core",
			DefaultBranch: "main",
			Remote:        "origin",
			DefaultAccess: DefaultAccessReadWrite,
		},
		{
			ID:            testSecondary,
			Name:          "Schema",
			HostPath:      "/srv/repositories/schema",
			ContainerPath: "/src/schema",
			DefaultBranch: "main",
			Remote:        "origin",
			DefaultAccess: DefaultAccessSelectable,
		},
	}, origin)
	if err != nil {
		t.Fatalf("creating a project: %v", err)
	}
	return project
}
