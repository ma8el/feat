package compose_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/execution"
	"github.com/ma8el/feat/internal/execution/compose"
	"github.com/ma8el/feat/internal/execution/compose/composetest"
	"github.com/ma8el/feat/internal/paths"
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

// TestFeatsOwnDirectoriesAreRefused is the rule CLAUDE.md states by hand: no
// daemon or runtime-control socket reaches the agent's container.
//
// Feat mounts none of these directories, and until now nothing refused a project
// that did. The consequence is larger than a Docker socket's. The runtime
// directory holds the tmux control socket, so `tmux -S … new-window` inside the
// container starts a command on the host outside it, and the daemon's own socket
// beside it launches, controls, and cleans up every task on the machine. The
// state directory and the home directory are the same absence for what the
// security model says Feat must not mount by default.
func TestFeatsOwnDirectoriesAreRefused(t *testing.T) {
	environment, spec := arrange(t, composetest.New())
	home := forbiddenPath(t, spec, execution.ForbiddenHome)
	state := forbiddenPath(t, spec, execution.ForbiddenState)
	runtime := forbiddenPath(t, spec, execution.ForbiddenRuntime)

	for name, testCase := range map[string]struct {
		mount    execution.ObservedMount
		contains string
	}{
		"the runtime directory itself": {
			mount:    execution.ObservedMount{Type: "bind", Source: runtime, Destination: "/feat-runtime"},
			contains: "tmux socket",
		},
		// The reachable one: a daemon started without XDG_RUNTIME_DIR puts its
		// sockets under /tmp, and sharing /tmp with a devcontainer is ordinary.
		"a directory holding it": {
			mount:    execution.ObservedMount{Type: "bind", Source: "/tmp", Destination: "/tmp", Writable: true},
			contains: paths.EnvRuntimeOverride,
		},
		"the tmux socket inside it": {
			mount: execution.ObservedMount{
				Type: "bind", Source: filepath.Join(runtime, "tmux.sock"), Destination: "/tmp/tmux.sock",
			},
			contains: "commands on this host outside the container",
		},
		"the state directory": {
			mount:    execution.ObservedMount{Type: "bind", Source: state, Destination: "/state", Writable: true},
			contains: "every other task's",
		},
		"another task's control workspace": {
			mount: execution.ObservedMount{
				Type: "bind", Source: filepath.Join(state, "control", "app", "other"), Destination: "/other",
				Writable: true,
			},
			contains: "every other task's",
		},
		// The same workspace this task owns, at a second target: Compose merges
		// by target, so this is a second mount rather than a replacement, and it
		// would hand the agent the host-only agent/ directory read-write.
		"this task's control workspace at another target": {
			mount: execution.ObservedMount{
				Type: "bind", Source: source(t, spec, "/feat"), Destination: "/feat-too", Writable: true,
			},
			contains: "control workspace",
		},
		"the home directory": {
			mount:    execution.ObservedMount{Type: "bind", Source: home, Destination: "/host", Writable: true},
			contains: "does not allow home directory mounts",
		},
		"a directory holding every user's": {
			mount: execution.ObservedMount{
				Type: "bind", Source: filepath.Dir(home), Destination: "/hosts", Writable: true,
			},
			contains: "does not allow home directory mounts",
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := environment.CheckMounts([]execution.ObservedMount{testCase.mount})
			if err == nil {
				t.Fatalf("a container mounting %s was accepted", testCase.mount.Source)
			}
			if !strings.Contains(err.Error(), testCase.contains) {
				t.Errorf("the message does not explain the refusal\n got: %v\nwant: %q", err, testCase.contains)
			}
			// Whatever the rule, the message has to name the mount as the
			// container reports it: that is what the reader looks for in their
			// own Compose file.
			if !strings.Contains(err.Error(), testCase.mount.Source) {
				t.Errorf("the message does not name the mount %s: %v", testCase.mount.Source, err)
			}
		})
	}
}

// forbiddenPath returns the specification's forbidden path of one kind.
func forbiddenPath(t *testing.T, spec execution.Spec, kind execution.ForbiddenKind) string {
	t.Helper()

	for _, forbidden := range spec.ForbiddenSources {
		if forbidden.Kind == kind {
			return forbidden.Path
		}
	}
	t.Fatalf("the specification forbids no %s path", kind)
	return ""
}

// TestAReadOnlyMountThatIsWritableIsRefused is invariant 6 asked of the
// container rather than of the document Feat generated.
//
// read_only: true in the generated override is a request. Compose merges a
// service's volumes by target, so what a path ends up being depends on every
// file in the project: a volumes_from copies another service's bindings, and an
// override applied after Feat's replaces them. The evidence was already being
// decoded from `docker inspect` and read by nobody.
func TestAReadOnlyMountThatIsWritableIsRefused(t *testing.T) {
	environment, _ := arrange(t, composetest.New())

	err := environment.CheckMounts([]execution.ObservedMount{
		{Type: "bind", Source: "/worktrees/store", Destination: "/srv/store", Writable: true},
	})
	if err == nil {
		t.Fatal("a container that can write a read-only worktree was accepted")
	}
	for _, expected := range []string{"read-write", "read-only", "/srv/store"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("the message does not mention %q: %v", expected, err)
		}
	}
}

// TestAReadOnlyVolumeThatIsWritableIsRefused is the same question for a named
// volume, which a project can hold read-only as well.
func TestAReadOnlyVolumeThatIsWritableIsRefused(t *testing.T) {
	_, spec := arrange(t, composetest.New())
	spec.Volumes = []execution.Volume{{Name: "reference", Target: "/reference", ReadOnly: true}}

	environment, err := compose.New(spec, compose.Options{Runner: composetest.New()})
	if err != nil {
		t.Fatalf("building the environment: %v", err)
	}
	err = environment.CheckMounts([]execution.ObservedMount{
		{Type: "volume", Name: "reference", Source: "/var/lib/docker/volumes/reference/_data",
			Destination: "/reference", Writable: true},
	})
	if err == nil {
		t.Fatal("a container that can write a read-only volume was accepted")
	}
	if !strings.Contains(err.Error(), "reference") {
		t.Errorf("the message does not name the volume: %v", err)
	}
}

// TestTheTasksOwnMountsAreAccepted keeps the two refusals above from being
// satisfied by refusing everything.
//
// A check that rejects the correct configuration is not a stricter check; it is
// a broken one, and it would be invisible in a suite that only ever asserts
// refusals.
func TestTheTasksOwnMountsAreAccepted(t *testing.T) {
	environment, spec := arrange(t, composetest.New())
	home := forbiddenPath(t, spec, execution.ForbiddenHome)

	err := environment.CheckMounts([]execution.ObservedMount{
		{Type: "bind", Source: "/worktrees/api", Destination: "/srv/api", Writable: true},
		{Type: "bind", Source: "/worktrees/store", Destination: "/srv/store"},
		// The control workspace, which lives inside the state directory the rule
		// above protects: Feat's own mount is what the check must not refuse.
		{Type: "bind", Source: source(t, spec, "/feat"), Destination: "/feat", Writable: true},
		{Type: "volume", Name: "feat-claude", Source: "/var/lib/docker/volumes/feat-claude/_data",
			Destination: "/feat-claude", Writable: true},
		// A mount inside the home directory is not the home directory. The
		// security model's own list of mount categories ends with "explicitly
		// configured environment/credential mounts", and this is what one looks
		// like; refusing it would refuse a documented configuration.
		{Type: "bind", Source: filepath.Join(home, ".ssh"), Destination: "/creds", Writable: false},
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

// TestABindBackedVolumeIsRefusedWhereItsBindWouldBe is G7-01: the rules are
// about what a mount reaches rather than about what the runtime calls it.
//
// `driver_opts: {type: none, device: /var/run, o: bind}` on a local volume
// makes an ordinary bind wearing a volume's name. Measured on 2026-08-19,
// Docker reports it as {Type: "volume", Source:
// "/var/lib/docker/volumes/<name>/_data"} — the volume's own mountpoint, and
// never the device — so a rule that compared the reported source compared a
// path under the runtime's own directory against the forbidden list and found
// nothing. Every rule below refuses the plain spelling of the same mount, which
// made this one YAML indirection around all of them, and the Docker socket the
// one at the end of it.
func TestABindBackedVolumeIsRefusedWhereItsBindWouldBe(t *testing.T) {
	environment, spec := arrange(t, composetest.New())

	for name, testCase := range map[string]struct {
		device   string
		contains string
	}{
		"the directory holding the Docker socket": {"/var/run", "never grants an agent Docker access"},
		"the Docker socket itself":                {"/var/run/docker.sock", "never grants an agent Docker access"},
		"Feat's runtime directory":                {forbiddenPath(t, spec, execution.ForbiddenRuntime), "tmux socket"},
		"Feat's state directory":                  {forbiddenPath(t, spec, execution.ForbiddenState), "every other task's"},
		"the home directory":                      {forbiddenPath(t, spec, execution.ForbiddenHome), "does not allow home directory mounts"},
		"an ordinary checkout":                    {"/repos/app/api", "leave alone"},
	} {
		t.Run(name, func(t *testing.T) {
			// The shape the finding measured, verbatim: the source is the
			// volume's mountpoint and the device is what it is a window onto.
			err := environment.CheckMounts([]execution.ObservedMount{{
				Type: "volume", Name: "hostrun", Source: "/var/lib/docker/volumes/hostrun/_data",
				Device: testCase.device, Destination: "/mnt/probe", Writable: true,
			}})
			if err == nil {
				t.Fatalf("a volume backed by %s was accepted, and the same path as a bind is refused",
					testCase.device)
			}
			if !strings.Contains(err.Error(), testCase.contains) {
				t.Errorf("the message does not explain the refusal\n got: %v\nwant: %q", err, testCase.contains)
			}
			// Both halves, because neither on its own is actionable: the name
			// is the line in their Compose file and the device is why it is a
			// problem.
			for _, expected := range []string{"hostrun", testCase.device} {
				if !strings.Contains(err.Error(), expected) {
					t.Errorf("the message does not name %q, so the reader cannot find it: %v", expected, err)
				}
			}
		})
	}
}

// TestAnOrdinaryNamedVolumeIsStillAccepted keeps the rule above from being
// satisfied by refusing every volume.
//
// A named volume the runtime backs itself is a category docs/05 blesses, and
// Feat mounts one of its own. A device that is not a path on this host — an NFS
// export, a CIFS share — reaches no host path either, and refusing it would
// name a host path that does not exist.
func TestAnOrdinaryNamedVolumeIsStillAccepted(t *testing.T) {
	environment, _ := arrange(t, composetest.New())

	for name, mount := range map[string]execution.ObservedMount{
		"a volume the runtime backs itself": {
			Type: "volume", Name: "node-modules", Source: "/var/lib/docker/volumes/node-modules/_data",
			Destination: "/srv/api/node_modules", Writable: true,
		},
		"Feat's own provider configuration volume": {
			Type: "volume", Name: "feat-claude", Source: "/var/lib/docker/volumes/feat-claude/_data",
			Destination: "/feat-claude", Writable: true,
		},
		"an anonymous volume": {
			Type: "volume", Name: "0f2c8b1e", Source: "/var/lib/docker/volumes/0f2c8b1e/_data",
			Destination: "/srv/cache", Writable: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := environment.CheckMounts([]execution.ObservedMount{mount}); err != nil {
				t.Errorf("an ordinary named volume was refused: %v", err)
			}
		})
	}
}

// TestTheDeviceOfAMountedVolumeIsRead pins the question that makes the rule
// above answerable.
//
// The device is not in `docker inspect`'s mount record at any format string:
// the volume has to be asked about itself. A check that read only the container
// would have nothing to compare, which is the whole of G7-01.
func TestTheDeviceOfAMountedVolumeIsRead(t *testing.T) {
	docker := composetest.New().
		Inspect("c0ffee", "Mounts", `[{"Type":"volume","Name":"hostrun",`+
			`"Source":"/var/lib/docker/volumes/hostrun/_data","Destination":"/var/run","RW":true}]`).
		Volume("hostrun", map[string]string{"type": "none", "device": "/var/run", "o": "bind"})
	environment, _ := arrange(t, docker)

	mounts, err := environment.Mounts(context.Background(), "c0ffee")
	if err != nil {
		t.Fatalf("inspecting the container: %v", err)
	}
	if len(mounts) != 1 {
		t.Fatalf("the mounts were not read: %v", mounts)
	}
	if mounts[0].Device != "/var/run" {
		t.Errorf("the volume's device was read as %q, want /var/run: nothing else in the record says "+
			"what the volume is a window onto", mounts[0].Device)
	}
	if !docker.Ran("volume inspect --format {{json .Options}} hostrun") {
		t.Errorf("nothing asked the volume what it is backed by: %v", docker.Calls())
	}
}

// TestAVolumeThatCannotBeReadStopsTheLaunch keeps an unanswerable question from
// being read as a reassuring answer.
//
// The conservative direction is the same one an unreadable identity takes: a
// volume Feat could not resolve is a volume that might be a bind onto anything.
func TestAVolumeThatCannotBeReadStopsTheLaunch(t *testing.T) {
	docker := composetest.New().
		Inspect("c0ffee", "Mounts", `[{"Type":"volume","Name":"hostrun",`+
			`"Source":"/var/lib/docker/volumes/hostrun/_data","Destination":"/var/run","RW":true}]`).
		Fail("volume inspect --format {{json .Options}} hostrun",
			"Error response from daemon: get hostrun: no such volume", 1)
	environment, _ := arrange(t, docker)

	_, err := environment.Mounts(context.Background(), "c0ffee")
	if err == nil {
		t.Fatal("a volume whose definition could not be read was passed on as an ordinary one")
	}
	if !strings.Contains(err.Error(), "hostrun") {
		t.Errorf("the message does not name the volume: %v", err)
	}
}

// TestANetworkVolumeIsNotTreatedAsAHostPath keeps the device rule from reading
// a remote address as a path on this machine.
//
// `device` belongs to the driver: for nfs it is ":/export" and for cifs it is
// "//server/share". Neither reaches this host, and a refusal naming "/server"
// would be one nobody could act on.
func TestANetworkVolumeIsNotTreatedAsAHostPath(t *testing.T) {
	for name, options := range map[string]map[string]string{
		"an NFS export": {"type": "nfs", "o": "addr=198.51.100.9,rw", "device": ":/export/data"},
		"a CIFS share":  {"type": "cifs", "o": "username=dev", "device": "//198.51.100.9/share"},
	} {
		t.Run(name, func(t *testing.T) {
			docker := composetest.New().
				Inspect("c0ffee", "Mounts", `[{"Type":"volume","Name":"remote",`+
					`"Source":"/var/lib/docker/volumes/remote/_data","Destination":"/srv/data","RW":true}]`).
				Volume("remote", options)
			environment, _ := arrange(t, docker)

			mounts, err := environment.Mounts(context.Background(), "c0ffee")
			if err != nil {
				t.Fatalf("inspecting the container: %v", err)
			}
			if mounts[0].Device != "" {
				t.Errorf("a remote device was read as the host path %q", mounts[0].Device)
			}
			if err := environment.CheckMounts(mounts); err != nil {
				t.Errorf("a volume on a remote filesystem was refused: %v", err)
			}
		})
	}
}

// TestARootlessRuntimeDirectoryIsRefused is G4-05: the case
// runtimeSocketNames' own comment says the known paths cannot enumerate.
//
// A rootless daemon puts its socket under /run/user/<uid>, where the uid is the
// user's own, so no fixed path names it. `- /run/user/1000/docker.sock:/x` is
// caught by the name rule and `- /run/user/1000:/run/user/1000` was caught by
// nothing: the containment test compares against the seven known paths, and
// this is not one of them.
func TestARootlessRuntimeDirectoryIsRefused(t *testing.T) {
	environment, _ := arrange(t, composetest.New())

	for name, mount := range map[string]execution.ObservedMount{
		"the runtime directory itself": {
			Type: "bind", Source: "/run/user/1000", Destination: "/run/user/1000", Writable: true,
		},
		"the directory holding every user's": {
			Type: "bind", Source: "/run/user", Destination: "/run/user", Writable: true,
		},
		"a runtime's own directory inside it": {
			Type: "bind", Source: "/run/user/1000/podman", Destination: "/run/podman-host", Writable: true,
		},
		// The destination end: what the container's own client finds is the
		// same capability as where it came from.
		"an unremarkable directory landing there": {
			Type: "bind", Source: "/opt/sockets", Destination: "/run/user/1000", Writable: true,
		},
		"a volume backed by it": {
			Type: "volume", Name: "hostrun", Source: "/var/lib/docker/volumes/hostrun/_data",
			Device: "/run/user/1000", Destination: "/mnt/probe", Writable: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := environment.CheckMounts([]execution.ObservedMount{mount})
			if err == nil {
				t.Fatal("a mount of the directory a rootless daemon puts its socket in was accepted")
			}
			if !strings.Contains(err.Error(), "never grants an agent Docker access") &&
				!strings.Contains(err.Error(), "controls this host's containers") {
				t.Errorf("the message does not say what the rule is: %v", err)
			}
		})
	}
}

// TestAMountLandingOnAKnownSocketPathIsRefused is the destination half of
// G4-05.
//
// Containment was tested on the source alone. Feat cannot see what a host
// directory holds, so a directory mounted *at* /var/run puts whatever socket is
// inside it exactly where the container's own client looks — and the source
// path gives nothing to compare, because a socket at an unenumerable host path
// is what the seven known paths cannot cover.
func TestAMountLandingOnAKnownSocketPathIsRefused(t *testing.T) {
	environment, _ := arrange(t, composetest.New())

	err := environment.CheckMounts([]execution.ObservedMount{
		{Type: "bind", Source: "/opt/colima/default", Destination: "/var/run", Writable: true},
	})
	if err == nil {
		t.Fatal("a host directory mounted at /var/run was accepted")
	}
	if !strings.Contains(err.Error(), "/var/run/docker.sock") {
		t.Errorf("the message does not name the socket path the mount lands on: %v", err)
	}
}

// TestARunOfTheMillDirectoryUnderTheRuntimeDirectoryIsAccepted keeps the
// rootless rule from refusing what it has no reason to.
//
// The per-user runtime directory holds a session bus and a keyring as well as a
// daemon socket. A rule that refused every path under it would refuse them with
// no way out, which is the false positive a user learns to ignore checks over.
func TestARunOfTheMillDirectoryUnderTheRuntimeDirectoryIsAccepted(t *testing.T) {
	environment, _ := arrange(t, composetest.New())

	if err := environment.CheckMounts([]execution.ObservedMount{
		{Type: "bind", Source: "/run/user/1000/bus", Destination: "/run/user/1000/bus"},
		{Type: "bind", Source: "/run/user/1000/keyring", Destination: "/run/keyring"},
	}); err != nil {
		t.Errorf("an ordinary path under the per-user runtime directory was refused: %v", err)
	}
}

// TestAMountWithNothingOfThisHostBehindItIsAccepted keeps the two rules that
// read a destination from firing on a mount that reaches no host path.
//
// A tmpfs at /run is how a devcontainer that runs systemd is written, and a
// volume the runtime backs itself holds nothing of this machine's. Neither can
// put the host's daemon socket where a client would find it, whatever it is
// mounted over, and refusing them would refuse an ordinary image for a rule
// about paths it does not have.
func TestAMountWithNothingOfThisHostBehindItIsAccepted(t *testing.T) {
	environment, _ := arrange(t, composetest.New())

	if err := environment.CheckMounts([]execution.ObservedMount{
		{Type: "tmpfs", Destination: "/run", Writable: true},
		{Type: "volume", Name: "state", Source: "/var/lib/docker/volumes/state/_data",
			Destination: "/run/user/1000", Writable: true},
	}); err != nil {
		t.Errorf("a mount with no host path behind it was refused: %v", err)
	}
}

// TestSourcesNamesWhatAVolumeIsBackedBy is G7-11: the one diagnostic that could
// have surfaced G7-01 printed the reassuring half of it.
//
// Rendering a volume by name is right for a volume the runtime backs itself —
// /var/lib/docker/volumes/feat-claude/_data is a useless thing to show somebody
// about a volume they named. For a bind-backed one it discards the only field
// that distinguishes it, and `hostrun -> /var/run (volume, rw)` reads as a
// category the security model blesses.
func TestSourcesNamesWhatAVolumeIsBackedBy(t *testing.T) {
	rendered := compose.Sources([]execution.ObservedMount{
		{Type: "volume", Name: "hostrun", Source: "/var/lib/docker/volumes/hostrun/_data",
			Device: "/var/run", Destination: "/var/run", Writable: true},
		{Type: "volume", Name: "feat-claude", Source: "/var/lib/docker/volumes/feat-claude/_data",
			Destination: "/feat-claude", Writable: true},
	})

	want := []string{
		"feat-claude -> /feat-claude (volume, rw)",
		"hostrun (/var/run) -> /var/run (volume, rw)",
	}
	if len(rendered) != len(want) {
		t.Fatalf("the mounts rendered as %v, want %v", rendered, want)
	}
	for i, expected := range want {
		if rendered[i] != expected {
			t.Errorf("the mount rendered as %q, want %q", rendered[i], expected)
		}
	}
}
