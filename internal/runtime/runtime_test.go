package runtime_test

import (
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/runtime"
)

const (
	task    = domain.TaskID("11111111-1111-4111-8111-111111111111")
	project = domain.ProjectID("app")
)

// valid returns a specification that passes, so that each case below changes
// exactly one thing and the failure names that thing.
func valid() runtime.Spec {
	return runtime.Spec{
		Project:  project,
		Task:     task,
		Identity: "feat-app-11111111",
		Includes: []runtime.Include{{
			Repository: "api",
			Directory:  "/repos/app/api",
			Files:      []string{"/repos/app/api/docker-compose.yml"},
		}},
		IncludePath:     "/state/runtime/app/" + string(task) + "/compose.include.yaml",
		StaticOverrides: []string{"/repos/app/api/docker-compose.dev.yml"},
		Directory:       "/state/runtime/app/" + string(task),
		OverridePath:    "/state/runtime/app/" + string(task) + "/compose.override.yaml",
		EnvFiles:        []string{"/repos/app/api/.env"},
		Services:        []string{"api", "worker"},
		Mounts: []runtime.Mount{
			{Services: []string{"api"}, Source: "/worktrees/api", Target: "/srv/api",
				Description: "the api task worktree"},
		},
		Variables:        map[string]string{"FEAT_TASK_KEY": "11111111"},
		ForbiddenSources: []string{"/repos/app/api"},
	}
}

// TestASpecificationIsCheckedBeforeItCanCreateAnything covers the values that
// end up in a command that creates containers and mounts the user's filesystem.
//
// Each case is a way a specification can be wrong that nothing downstream would
// catch: the services would simply come up with the wrong identity or the wrong
// code, and every record Feat kept about them would be correct.
func TestASpecificationIsCheckedBeforeItCanCreateAnything(t *testing.T) {
	for name, testCase := range map[string]struct {
		change   func(*runtime.Spec)
		contains string
	}{
		"no identity": {
			change:   func(s *runtime.Spec) { s.Identity = "" },
			contains: "affect one task's services and no other's",
		},
		"no repositories": {
			change:   func(s *runtime.Spec) { s.Includes = nil },
			contains: "composed of no repositories",
		},
		"a repository bringing no files": {
			change:   func(s *runtime.Spec) { s.Includes[0].Files = nil },
			contains: "brings no Compose files",
		},
		"a file brought by two repositories": {
			change: func(s *runtime.Spec) {
				s.Includes = append(s.Includes, runtime.Include{
					Repository: "web",
					Directory:  "/repos/app/web",
					Files:      s.Includes[0].Files,
				})
			},
			contains: "defines its services twice",
		},
		"a relative project directory": {
			change:   func(s *runtime.Spec) { s.Includes[0].Directory = "app/api" },
			contains: "must be absolute",
		},
		"no services": {
			change:   func(s *runtime.Spec) { s.Services = nil },
			contains: "names no services to manage",
		},
		"relative compose file": {
			change:   func(s *runtime.Spec) { s.Includes[0].Files = []string{"docker-compose.yml"} },
			contains: "must be absolute",
		},
		"relative static override": {
			change:   func(s *runtime.Spec) { s.StaticOverrides = []string{"override.yml"} },
			contains: "must be absolute",
		},
		"relative environment file": {
			change:   func(s *runtime.Spec) { s.EnvFiles = []string{".env"} },
			contains: "must be absolute",
		},
		"uncleaned override path": {
			change:   func(s *runtime.Spec) { s.OverridePath = "/state/../state/override.yaml" },
			contains: "written cleanly",
		},
		"a service named twice": {
			change:   func(s *runtime.Spec) { s.Services = []string{"api", "api"} },
			contains: "twice",
		},
		"a service Compose would read as a flag": {
			change:   func(s *runtime.Spec) { s.Services = []string{"--rm"} },
			contains: "must not begin with",
		},
		"relative mount source": {
			change:   func(s *runtime.Spec) { s.Mounts = []runtime.Mount{{Source: "api", Target: "/srv/api"}} },
			contains: "must be absolute",
		},
		"two mounts at one target in one service": {
			change: func(s *runtime.Spec) {
				s.Mounts = append(s.Mounts, runtime.Mount{
					Services: []string{"api"}, Source: "/elsewhere", Target: "/srv/api"})
			},
			contains: "would hide the other",
		},
		"a mount belonging to no service": {
			change:   func(s *runtime.Spec) { s.Mounts[0].Services = nil },
			contains: "applies to no service",
		},
		"a mount naming a service the task does not manage": {
			change:   func(s *runtime.Spec) { s.Mounts[0].Services = []string{"unmanaged"} },
			contains: "does not manage",
		},
		"a variable name carrying an equals sign": {
			change:   func(s *runtime.Spec) { s.Variables = map[string]string{"A=B": "c"} },
			contains: "must not contain",
		},
		"an unknown task": {
			change:   func(s *runtime.Spec) { s.Task = "not-a-uuid" },
			contains: "task",
		},
	} {
		t.Run(name, func(t *testing.T) {
			spec := valid()
			testCase.change(&spec)

			err := spec.Validate()
			if err == nil {
				t.Fatalf("the specification was accepted, and it should not have been")
			}
			if !strings.Contains(err.Error(), testCase.contains) {
				t.Fatalf("the message does not explain what is wrong.\n got: %s\nwant it to contain: %s",
					err, testCase.contains)
			}
		})
	}
}

func TestAValidSpecificationIsAccepted(t *testing.T) {
	if err := valid().Validate(); err != nil {
		t.Fatalf("a valid specification was refused: %v", err)
	}
}

// TestGeneratedVariablesAreOrdered pins the order the generated override is
// written in. A map's iteration order does not repeat, so without this the
// generated document would differ between two runs that changed nothing.
func TestGeneratedVariablesAreOrdered(t *testing.T) {
	spec := valid()
	spec.Variables = map[string]string{"C": "3", "A": "1", "B": "2"}

	entries := spec.Entries()
	var names []string
	for _, entry := range entries {
		names = append(names, entry[0])
	}
	if got, want := strings.Join(names, ","), "A,B,C"; got != want {
		t.Fatalf("generated variables are not in a fixed order: got %s, want %s", got, want)
	}
}

// TestAServiceIsRunningOnlyWhenTheRuntimeSaysSo keeps the reading of a
// container runtime's own word in one place.
func TestAServiceIsRunningOnlyWhenTheRuntimeSaysSo(t *testing.T) {
	for state, want := range map[string]bool{
		"running": true,
		"Running": true,
		"exited":  false,
		"created": false,
		"":        false,
	} {
		if got := (runtime.ServiceState{State: state}).Running(); got != want {
			t.Fatalf("a service in state %q reported running=%t, want %t", state, got, want)
		}
	}
}
