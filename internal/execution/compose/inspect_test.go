package compose_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/execution"
	"github.com/ma8el/feat/internal/execution/compose/composetest"
)

// TestADockerSocketIsRefused is acceptance criterion 4 at the layer that can
// state it narrowly.
//
// A container holding a Docker socket controls the host's Docker daemon and
// therefore the host, whatever the project's declared capabilities say. The
// check reads the running container rather than the configuration, so it is
// evidence about what exists rather than a claim about what was asked for.
func TestADockerSocketIsRefused(t *testing.T) {
	for name, mount := range map[string]execution.ObservedMount{
		"the usual path": {
			Type: "bind", Source: "/var/run/docker.sock", Destination: "/var/run/docker.sock", Writable: true,
		},
		"mounted somewhere else in the container": {
			Type: "bind", Source: "/var/run/docker.sock", Destination: "/tmp/d.sock", Writable: true,
		},
		"the host's own path is unusual": {
			Type: "bind", Source: "/opt/colima/docker.sock", Destination: "/var/run/docker.sock",
		},
		"a rootless socket": {
			Type: "bind", Source: "/run/user/1000/docker.sock", Destination: "/var/run/docker.sock",
		},
		"a podman socket": {
			Type: "bind", Source: "/run/podman/podman.sock", Destination: "/run/podman/podman.sock",
		},
		// A socket reached through the directory holding it. Nothing in the
		// Compose file says "docker.sock", which is what makes this the shape a
		// project arrives at by accident.
		"the directory the socket sits in": {
			Type: "bind", Source: "/var/run", Destination: "/var/run", Writable: true,
		},
		"a parent of that directory": {
			Type: "bind", Source: "/", Destination: "/host", Writable: true,
		},
		// A rootless daemon's socket is under a uid nobody can enumerate, so the
		// name is what identifies it.
		"a rootless podman socket by name": {
			Type: "bind", Source: "/run/user/1000/podman/podman.sock", Destination: "/srv/socket",
		},
		"a socket the container's own client would find": {
			Type: "bind", Source: "/opt/colima/default/sock", Destination: "/srv/.docker/run/docker.sock",
		},
	} {
		t.Run(name, func(t *testing.T) {
			environment, _ := arrange(t, composetest.New())

			err := environment.CheckMounts([]execution.ObservedMount{mount})
			if err == nil {
				t.Fatal("a container with a Docker socket was accepted")
			}
			if !strings.Contains(err.Error(), "never grants an agent Docker access") {
				t.Errorf("the message does not say what the rule is: %v", err)
			}
		})
	}
}

// TestAnOrdinaryCheckoutMountIsRefused is ADR-033 evidence 1 at the layer that
// can prove it.
//
// This is the failure the reference project would have hit: the devcontainer's
// own Compose file mounts the user's checkouts, and a container_path that
// disagrees with it adds the task worktree beside them rather than replacing
// them. Nothing about the task would look wrong afterwards.
func TestAnOrdinaryCheckoutMountIsRefused(t *testing.T) {
	for name, mount := range map[string]execution.ObservedMount{
		"the checkout itself": {
			Type: "bind", Source: "/repos/app/api", Destination: "/srv/ordinary", Writable: true,
		},
		"through Docker Desktop's file sharing prefix": {
			Type: "bind", Source: "/host_mnt/repos/app/api", Destination: "/srv/ordinary", Writable: true,
		},
		"a parent directory holding the checkout": {
			Type: "bind", Source: "/repos/app", Destination: "/srv/all", Writable: true,
		},
		"read-only is still the checkout": {
			Type: "bind", Source: "/repos/app/api", Destination: "/reference/api",
		},
	} {
		t.Run(name, func(t *testing.T) {
			environment, _ := arrange(t, composetest.New())

			err := environment.CheckMounts([]execution.ObservedMount{mount})
			if err == nil {
				t.Fatal("a container mounting the user's ordinary checkout was accepted")
			}
			for _, expected := range []string{"leave alone", "container_path"} {
				if !strings.Contains(err.Error(), expected) {
					t.Errorf("the message does not mention %q: %v", expected, err)
				}
			}
		})
	}
}

// TestTheTasksOwnMountsAreAccepted keeps the two refusals above from being
// satisfied by refusing everything.
//
// A check that rejects the correct configuration is not a stricter check; it is
// a broken one, and it would be invisible in a suite that only ever asserts
// refusals.
func TestTheTasksOwnMountsAreAccepted(t *testing.T) {
	environment, _ := arrange(t, composetest.New())

	err := environment.CheckMounts([]execution.ObservedMount{
		{Type: "bind", Source: "/worktrees/api", Destination: "/srv/api", Writable: true},
		{Type: "bind", Source: "/worktrees/store", Destination: "/srv/store"},
		{Type: "bind", Source: "/state/control/app/task", Destination: "/feat", Writable: true},
		{Type: "volume", Name: "feat-claude", Source: "/var/lib/docker/volumes/feat-claude/_data",
			Destination: "/feat-claude", Writable: true},
		// A repository whose name merely starts the same way is a different
		// repository, and refusing it would be a false positive nobody could
		// work around.
		{Type: "bind", Source: "/repos/app/api-docs", Destination: "/srv/docs"},
		// The rule is about container runtimes rather than about sockets: a
		// project's own service socket is an ordinary thing to share, and a
		// directory that merely sits beside one is not a runtime's.
		{Type: "bind", Source: "/var/run/myapp.sock", Destination: "/var/run/myapp.sock", Writable: true},
		{Type: "bind", Source: "/var/lib/postgresql", Destination: "/var/lib/postgresql", Writable: true},
	})
	if err != nil {
		t.Fatalf("the task's own mounts were refused: %v", err)
	}
}

// TestEveryProblemMountIsReportedAtOnce keeps a container with two mistakes
// from taking two launches to diagnose.
func TestEveryProblemMountIsReportedAtOnce(t *testing.T) {
	environment, _ := arrange(t, composetest.New())

	err := environment.CheckMounts([]execution.ObservedMount{
		{Type: "bind", Source: "/var/run/docker.sock", Destination: "/var/run/docker.sock"},
		{Type: "bind", Source: "/repos/app/api", Destination: "/srv/ordinary"},
	})
	if err == nil {
		t.Fatal("a container with both problems was accepted")
	}
	for _, expected := range []string{"docker.sock", "/repos/app/api"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("the combined message does not mention %q: %v", expected, err)
		}
	}
}

// TestMountsAreReadFromTheContainerRatherThanTheConfiguration pins which
// command answers the question.
//
// `docker compose config` renders the project including the values of the
// project's environment files, which Feat must never read (ADR-028). A test
// that only checked the outcome would not notice the day somebody switched to
// the more convenient command.
func TestMountsAreReadFromTheContainerRatherThanTheConfiguration(t *testing.T) {
	docker := composetest.New().
		Answer("inspect --type container --format {{json .Mounts}} c0ffee",
			`[{"Type":"bind","Source":"/worktrees/api","Destination":"/srv/api","RW":true}]`)
	environment, _ := arrange(t, docker)

	mounts, err := environment.Mounts(context.Background(), "c0ffee")
	if err != nil {
		t.Fatalf("inspecting the container: %v", err)
	}
	if len(mounts) != 1 || mounts[0].Destination != "/srv/api" {
		t.Fatalf("the mounts were not read: %v", mounts)
	}

	for _, call := range docker.Calls() {
		if strings.HasPrefix(call, "config") {
			t.Errorf("the mounts were read with %q, which renders the project's environment file values", call)
		}
	}
}
