package compose_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/execution/compose"
	"github.com/ma8el/feat/internal/execution/compose/composetest"
)

// identity is the Compose project name of the task above, derived the way the
// daemon derives it.
const identity = "feat-agent-app-" + string(task)

// byName returns the project addressed by name over a fake Docker, with the two
// enumerations arranged to answer nothing.
//
// Both are arranged even when a test only cares about one of them, because the
// fake refuses an unarranged command: a test that left the networks unanswered
// would be testing an error rather than an absence.
func byName(t *testing.T, docker *composetest.Docker) *compose.Project {
	t.Helper()

	docker.Answer("ps --all --format json", "").Answer(networksOf(identity), "").Answer("down", "")
	project, err := compose.ByName(identity, compose.Options{Runner: docker})
	if err != nil {
		t.Fatalf("addressing Compose project %s: %v", identity, err)
	}
	return project
}

// networksOf is the shortened form of the network enumeration of one project.
func networksOf(name string) string {
	return "network ls --filter label=com.docker.compose.project=" + name + " --format {{.Name}}"
}

// TestAProjectIsAskedByNameAndNeverByFile is what makes this usable at all for
// the task it exists for.
//
// A launch that fails after its container exists usually fails because the
// project's own Compose file changed, and the container has to be recreated
// under it. Reading that file to find what the launch left would be reading the
// thing that moved, so every invocation here carries the project name and no
// --file at all.
func TestAProjectIsAskedByNameAndNeverByFile(t *testing.T) {
	docker := composetest.New()
	project := byName(t, docker)

	if _, err := project.Remains(context.Background()); err != nil {
		t.Fatalf("Remains: %v", err)
	}
	if err := project.Destroy(context.Background()); err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	for _, command := range []string{"ps --all --format json", networksOf(identity), "down"} {
		vector, ran := docker.Vector(command)
		if !ran {
			t.Fatalf("%q was not run: %v", command, docker.Calls())
		}
		if slices.Contains(vector, "--file") {
			t.Errorf("%q read a Compose file: %v", command, vector)
		}
		if !strings.Contains(strings.Join(vector, " "), identity) {
			t.Errorf("%q does not name the project %s: %v", command, identity, vector)
		}
	}
}

// TestRemainsNamesWhatIsLeftOfAProject covers the two halves of what a launch
// leaves, and the fact that either can outlive the other: an exited container
// keeps its network, and a container removed by hand leaves the network alone.
func TestRemainsNamesWhatIsLeftOfAProject(t *testing.T) {
	for name, testCase := range map[string]struct {
		containers string
		networks   string
		want       []string
		absent     []string
	}{
		"a container that exited and its network": {
			containers: `{"ID":"c0ffee","Name":"feat-agent-app-dev-1","Service":"dev",` +
				`"State":"exited","Status":"Exited (137) 3 hours ago"}`,
			networks: identity + "_default\n",
			want: []string{
				"container feat-agent-app-dev-1 (Exited (137) 3 hours ago)",
				"network " + identity + "_default",
			},
		},
		"a network whose container is already gone": {
			networks: identity + "_default\n",
			want:     []string{"network " + identity + "_default"},
			absent:   []string{"container"},
		},
		"nothing at all": {},
	} {
		t.Run(name, func(t *testing.T) {
			docker := composetest.New()
			project := byName(t, docker)
			docker.Answer("ps --all --format json", testCase.containers).
				Answer(networksOf(identity), testCase.networks)

			remains, err := project.Remains(context.Background())
			if err != nil {
				t.Fatalf("Remains: %v", err)
			}
			if remains.Empty() != (len(testCase.want) == 0) {
				t.Fatalf("Empty() = %v with %d entries", remains.Empty(), len(remains))
			}

			described := remains.Describe()
			for _, expected := range testCase.want {
				if !strings.Contains(described, expected) {
					t.Errorf("%q does not name %q", described, expected)
				}
			}
			for _, unexpected := range testCase.absent {
				if strings.Contains(described, unexpected) {
					t.Errorf("%q names %q, which is not there", described, unexpected)
				}
			}
		})
	}
}

// TestOnlyContainersHoldAMount is what the control workspace ordering rule reads.
//
// A network holds no bind mount, so a project reduced to one must not keep a
// cleanup waiting for something that will never let go.
func TestOnlyContainersHoldAMount(t *testing.T) {
	docker := composetest.New()
	project := byName(t, docker)
	docker.Answer(networksOf(identity), identity+"_default\n")

	remains, err := project.Remains(context.Background())
	if err != nil {
		t.Fatalf("Remains: %v", err)
	}
	if remains.Empty() {
		t.Fatal("the project reported nothing, so there is no network to test with")
	}
	if !remains.Containers().Empty() {
		t.Errorf("a network was counted as a container: %s", remains.Containers().Describe())
	}
}

// TestDestroyingByNameKeepsTheVolumeRules is the same set of rules
// Environment.Destroy documents, checked on the argument vector: a task's
// volumes are retained by default and an orphan is a container Feat did not put
// there, and neither rule may be lost by removing the files from the command.
func TestDestroyingByNameKeepsTheVolumeRules(t *testing.T) {
	docker := composetest.New()
	project := byName(t, docker)

	if err := project.Destroy(context.Background()); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	vector, _ := docker.Vector("down")
	for _, forbidden := range []string{"--volumes", "-v", "--remove-orphans"} {
		if slices.Contains(vector, forbidden) {
			t.Errorf("the removal passed %s: %v", forbidden, vector)
		}
	}
}

// TestAProjectNameDockerMustNotBeGivenIsRefused is removeVolumes' rule applied
// to the other identifier this package passes on.
func TestAProjectNameDockerMustNotBeGivenIsRefused(t *testing.T) {
	for _, name := range []string{"", "   ", "--project-directory"} {
		if _, err := compose.ByName(name, compose.Options{Runner: composetest.New()}); err == nil {
			t.Errorf("%q was accepted as a Compose project name", name)
		}
	}
}

// TestAFailedEnumerationIsNotAnEmptyProject keeps the two apart at the boundary
// where it matters: a cleanup that read a Docker failure as "nothing is there"
// would report success over a container it never saw.
func TestAFailedEnumerationIsNotAnEmptyProject(t *testing.T) {
	docker := composetest.New()
	project := byName(t, docker)
	docker.Fail("ps --all --format json", "Cannot connect to the Docker daemon", 1)

	remains, err := project.Remains(context.Background())
	if err == nil {
		t.Fatalf("a Docker that could not answer reported %d resources", len(remains))
	}
	if !strings.Contains(err.Error(), identity) {
		t.Errorf("the failure does not name the project: %v", err)
	}
}
