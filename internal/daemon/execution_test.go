package daemon

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/api"
	"github.com/ma8el/feat/internal/config"
	"github.com/ma8el/feat/internal/control"
	"github.com/ma8el/feat/internal/domain"
	"github.com/ma8el/feat/internal/execution"
	"github.com/ma8el/feat/internal/execution/compose"
	"github.com/ma8el/feat/internal/execution/compose/composetest"
	"github.com/ma8el/feat/internal/store"
)

// overridePath is where the fixture project's generated Compose override goes.
func (d *drafting) overridePath(t *testing.T, id domain.TaskID) string {
	t.Helper()

	return filepath.Join(d.layout.ExecutionRoot(), "app", id.String(), "compose.override.yaml")
}

// dockerFunc lets a test swap the fake Docker after the daemon was built, so a
// case can arrange one answer differently without a second daemon.
type dockerFunc func() *composetest.Docker

func (f dockerFunc) Run(ctx context.Context, invocation execution.Invocation) (execution.Output, error) {
	return f().Run(ctx, invocation)
}

func (f dockerFunc) Look(name string) (string, error) { return f().Look(name) }

// The fixture project's devcontainer, as internal/daemon's configuration
// fixtures declare it. They are here rather than repeated in each test so that a
// change to the fixture moves one list.
const (
	fixtureService = "dev"
	fixtureUser    = "developer"
	fixtureWorkdir = "/srv/api"
	fixtureControl = "/feat"
)

// ordinaryPrivileges is what a container granted nothing beyond its mounts
// reports about itself.
//
// It is neither of the two answers a fixture reaches for by reflex. An unread
// host configuration is refused, so Known is true; and a read one whose masked
// and read-only path lists are empty is a grant rather than an absence —
// `security_opt: systempaths=unconfined` reaches a launch as those two lists
// being empty and as nothing else (ADR-067). The lists come from composetest so
// that a report written here and one the fake answers with cannot describe
// different containers.
func ordinaryPrivileges() execution.ObservedPrivileges {
	return execution.ObservedPrivileges{
		Known:         true,
		MaskedPaths:   composetest.MaskedPaths,
		ReadOnlyPaths: composetest.ReadonlyPaths,
	}
}

// containerProbe renders the shortened command a probe runs inside the fixture's
// container, which is how composetest keys an arranged answer.
func containerProbe(program string, arguments ...string) string {
	parts := append([]string{
		"exec", "--no-TTY", "--user", fixtureUser, "--workdir", fixtureWorkdir, fixtureService, program,
	}, arguments...)
	return strings.Join(parts, " ")
}

// workingDocker is a fake Docker whose container passes every check: a recent
// Compose, a service that starts and stays running, a non-root agent, no Docker
// client, the hook toolchain, writable directories, and no unexpected mounts.
//
// A test that wants one of those to be wrong starts here and arranges the one
// answer it cares about, so the failure it asserts is the only difference.
func workingDocker() *composetest.Docker {
	docker := composetest.New().
		Answer(containerProbe("id", "-u"), "1000\n").
		Answer(containerProbe("id", "-un"), fixtureUser+"\n").
		Answer(containerProbe("sh", "-c", ":"), "").
		Answer(containerProbe("mktemp", "-u"), "/tmp/tmp.AAA").
		Answer(containerProbe("date", "-u", "+%Y"), "2026").
		Answer(containerProbe("cat", "/dev/null"), "").
		Answer(containerProbe("touch", "--help"), "").
		Answer(containerProbe("mv", "--help"), "").
		Answer(containerProbe("rm", "--help"), "").
		Answer(containerProbe("claude", "--version"), "2.1.220 (Claude Code)").
		Answer(containerProbe("/bin/bash", "-c", ":"), "")

	// The two control directories the agent reports through, rather than the
	// workspace root: the root is mounted read-only, and what a launch has to
	// prove writable is what Feat asks to be writable.
	for _, directory := range []string{
		fixtureControl + "/outbox", fixtureControl + "/reports", fixtureWorkdir,
	} {
		docker = docker.
			Answer(containerProbe("touch", directory+"/.feat-write-probe"), "").
			Answer(containerProbe("rm", "-f", directory+"/.feat-write-probe"), "")
	}

	// Absent, which is what the security model requires and what the fake must
	// therefore say: an unarranged answer would be a failure of a different
	// shape. Every client that speaks the Docker API, not only the one named
	// after it.
	for _, client := range compose.ContainerClients {
		docker = docker.Fail(containerProbe(client, "--version"),
			`exec: "`+client+`": executable file not found in $PATH`, 126)
	}
	// And nothing that hands the agent back the privilege its user does not
	// have, which is the other thing the launch asks the container about itself.
	for _, tool := range compose.EscalationTools {
		docker = docker.Fail(containerProbe(tool.Name, tool.Arguments...),
			`exec: "`+tool.Name+`": executable file not found in $PATH`, 126)
	}
	return docker
}

// TestTheGeneratedOverrideMountsWhatTheTaskOwns is acceptance criterion 1 at the
// daemon layer: the task's worktrees appear at their configured container paths
// with the access the task selected.
//
// It is checked on the specification the daemon resolved rather than on a
// running container, because this is the layer that decides it; the real suite
// checks the same thing against Docker.
func TestTheGeneratedOverrideMountsWhatTheTaskOwns(t *testing.T) {
	arranged := arrangeDrafting(t)
	task := arranged.launched(t)

	workspace, err := arranged.service.controlWorkspace(task)
	if err != nil {
		t.Fatalf("resolving the control workspace: %v", err)
	}
	cfg := arranged.config(t)

	spec, err := arranged.service.executionSpec(cfg, task, workspace)
	if err != nil {
		t.Fatalf("resolving the execution specification: %v", err)
	}

	mounts := make(map[string]bool, len(spec.Mounts))
	for _, mount := range spec.Mounts {
		mounts[mount.Target] = mount.ReadOnly
	}

	readOnly, mounted := mounts["/srv/api"]
	if !mounted {
		t.Errorf("the read-write task worktree is not mounted at its container path; mounts: %v", mounts)
	}
	if readOnly {
		t.Error("the read-write task worktree was mounted read-only")
	}
	if readOnly, mounted := mounts["/srv/store"]; !mounted {
		t.Errorf("the read-only task worktree is not mounted at its container path; mounts: %v", mounts)
	} else if !readOnly {
		t.Error("a repository the task selected read-only was mounted read-write, so the agent could edit it")
	}
	if _, mounted := mounts[fixtureControl]; !mounted {
		t.Error("the control workspace is not mounted, so the agent could not report anything")
	}
}

// TestOnlyTheAgentsOwnDirectoriesOfTheControlWorkspaceAreWritable is the mount
// half of the control-workspace boundary.
//
// The workspace was mounted read-write in full, which handed the agent the two
// things ADR-032 keeps out of its reach: agent/, holding the generated hooks
// and the record of which messages have been applied, and inbox/, holding the
// completion gate's verdicts on the agent's own review requests. An agent that
// can write either can rewrite the hooks it runs under, forge the verdict it is
// waiting for, or delete the record that stops a message being applied twice —
// none of which needs a defect anywhere else to work.
func TestOnlyTheAgentsOwnDirectoriesOfTheControlWorkspaceAreWritable(t *testing.T) {
	arranged := arrangeDrafting(t)
	task := arranged.launched(t)

	workspace, err := arranged.service.controlWorkspace(task)
	if err != nil {
		t.Fatalf("resolving the control workspace: %v", err)
	}
	spec, err := arranged.service.executionSpec(arranged.config(t), task, workspace)
	if err != nil {
		t.Fatalf("resolving the execution specification: %v", err)
	}

	writable := make(map[string]string)
	for _, mount := range spec.Mounts {
		if !mount.ReadOnly {
			writable[mount.Target] = mount.Source
		}
	}
	if source, open := writable[fixtureControl]; open {
		t.Errorf("the control workspace is mounted read-write from %s, so the agent owns the record of "+
			"which of its messages have been applied and the hooks it runs under", source)
	}

	for _, name := range control.AgentWritable() {
		target := fixtureControl + "/" + name
		source, open := writable[target]
		if !open {
			t.Errorf("%s is not writable, and the agent has nowhere to report to; mounts: %v", target, writable)
			continue
		}
		if want := filepath.Join(workspace.Root(), name); source != want {
			t.Errorf("%s is mounted from %s, want the task's own %s", target, source, want)
		}
	}

	// And the two host-only parts are reachable, read-only, through the mount
	// of the tree they are in: a hook the agent cannot read is a session Feat
	// never hears from.
	if _, mounted := writable[fixtureControl+"/agent"]; mounted {
		t.Error("the host-only agent directory is mounted read-write")
	}
	if _, mounted := writable[fixtureControl+"/inbox"]; mounted {
		t.Error("the inbox is mounted read-write, so an agent could write its own verification verdict")
	}
}

// TestEachTaskGetsItsOwnComposeProject is what makes acceptance criterion 6
// possible.
//
// Two tasks sharing a Compose project name would be two tasks sharing one
// container, and the second launch would silently join the first task's
// environment.
func TestEachTaskGetsItsOwnComposeProject(t *testing.T) {
	arranged := arrangeDrafting(t)

	first := arranged.launched(t)
	second := arranged.launched(t)

	if first.Session == nil || first.Session.Execution == nil {
		t.Fatal("the first task recorded no execution environment")
	}
	if second.Session == nil || second.Session.Execution == nil {
		t.Fatal("the second task recorded no execution environment")
	}
	if first.Session.Execution.Identity == second.Session.Execution.Identity {
		t.Fatalf("both tasks own the Compose project %s, so they would share one container",
			first.Session.Execution.Identity)
	}
	for _, task := range []string{first.ID.String(), second.ID.String()} {
		identity := first.Session.Execution.Identity
		if task == second.ID.String() {
			identity = second.Session.Execution.Identity
		}
		if !strings.Contains(identity, task) {
			t.Errorf("the Compose project %q does not name its task %s", identity, task)
		}
	}
}

// TestTheExecutionEnvironmentIsRecordedBeforeItExists is ADR-029's ordering
// applied to containers.
//
// Cleanup and reconciliation resolve what a task owns from its record, so a
// container that exists while nothing names it is a container nothing can find.
// The check is made from inside the launch: at the moment Docker is asked to
// bring a service up, a durable record must already name the Compose project.
//
// That record is the task's event log rather than its snapshot, and the
// difference is not an implementation detail. A session is what carries the
// environment on the snapshot, and a session needs the tmux target of a terminal
// that runs inside the container — so the snapshot cannot name the container
// until after it exists. The event log has no such requirement (ADR-033).
func TestTheExecutionEnvironmentIsRecordedBeforeItExists(t *testing.T) {
	arranged := arrangeDrafting(t)

	draft := arranged.draft(t, "Add a rate limit")
	arranged.selectRepositories(t, draft.ID)
	displayed := arranged.resolve(t, draft.ID)

	var namedWhenStarted []string
	var overrideExisted bool
	arranged.docker.Before("up --detach dev", func() {
		log, err := arranged.service.store.Events().Replay(context.Background(),
			store.TaskRef{Project: "app", Task: draft.ID})
		if err != nil {
			t.Errorf("replaying the task's events: %v", err)
			return
		}
		for _, event := range log.Events {
			if event.Type == domain.EventExecutionChanged {
				namedWhenStarted = append(namedWhenStarted, event.To)
			}
		}
		// And the document that decides what the container mounts is on disk
		// before the container reads it.
		if _, err := os.Stat(arranged.overridePath(t, draft.ID)); err == nil {
			overrideExisted = true
		}
	})

	task, err := arranged.service.LaunchDraft(context.Background(), draft.ID, api.Confirmation{Fingerprint: displayed.Fingerprint})
	if err != nil {
		t.Fatalf("LaunchDraft: %v", err)
	}
	if task.Session == nil || task.Session.Execution == nil {
		t.Fatal("the launched task records no execution environment")
	}

	identity := task.Session.Execution.Identity
	if !slices.Contains(namedWhenStarted, identity) {
		t.Errorf("when the container was started the log named %v, want it to name %q: "+
			"a container that exists before anything names it cannot be found again",
			namedWhenStarted, identity)
	}
	if !overrideExisted {
		t.Error("the generated override did not exist when the service was started")
	}
}

// TestALaunchRefusedByTheContainerIsExplainable covers the failures a
// devcontainer can produce, and what each one leaves behind.
//
// Every case must do three things: refuse the launch, say why in terms that name
// the fix, and leave the task `failed` rather than in a state that claims an
// agent is running. Nothing is undone, because a container that started may have
// had effects that stopping it does not reverse (ADR-029, ADR-033).
func TestALaunchRefusedByTheContainerIsExplainable(t *testing.T) {
	for name, testCase := range map[string]struct {
		arrange  func(*composetest.Docker)
		contains []string
	}{
		"the agent would be root": {
			arrange: func(d *composetest.Docker) {
				d.Answer(containerProbe("id", "-u"), "0\n").Answer(containerProbe("id", "-un"), "root\n")
			},
			contains: []string{"root", "agent.execution.user"},
		},
		"the container has a Docker client": {
			arrange: func(d *composetest.Docker) {
				d.Answer(containerProbe("docker", "--version"), "Docker version 27.0.0")
			},
			contains: []string{"Docker API", "reach the host"},
		},
		"the container mounts the Docker socket": {
			arrange: func(d *composetest.Docker) {
				d.Answer("inspect --type container --format {{json .Mounts}} c0ffee",
					`[{"Type":"bind","Source":"/var/run/docker.sock","Destination":"/var/run/docker.sock","RW":true}]`)
			},
			contains: []string{"docker.sock", "never grants an agent Docker access"},
		},
		"the control workspace cannot be written to": {
			arrange: func(d *composetest.Docker) {
				d.Fail(containerProbe("touch", fixtureControl+"/outbox/.feat-write-probe"),
					"touch: cannot touch '/feat/outbox/.feat-write-probe': Permission denied", 1)
			},
			contains: []string{fixtureControl + "/outbox", "Permission denied"},
		},
		"the hook toolchain is incomplete": {
			arrange: func(d *composetest.Docker) {
				d.Fail(containerProbe("mktemp", "-u"),
					`exec: "mktemp": executable file not found in $PATH`, 126)
			},
			contains: []string{"mktemp", "never hear from it"},
		},
		"the service does not stay running": {
			arrange: func(d *composetest.Docker) {
				d.Answer("ps --all --format json dev",
					`{"ID":"c0ffee","Service":"dev","State":"exited","Status":"Exited (0) 1 second ago"}`)
			},
			contains: []string{"did not stay running", "sleep infinity"},
		},
		"the service cannot be started at all": {
			arrange: func(d *composetest.Docker) {
				d.Fail("up --detach dev", "no such service: dev", 1)
			},
			contains: []string{"dev", "no such service"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			arranged := arrangeDrafting(t)
			testCase.arrange(arranged.docker)

			draft := arranged.draft(t, "Add a rate limit")
			arranged.selectRepositories(t, draft.ID)
			displayed := arranged.resolve(t, draft.ID)

			_, err := arranged.service.LaunchDraft(context.Background(), draft.ID, api.Confirmation{Fingerprint: displayed.Fingerprint})
			if err == nil {
				t.Fatal("the launch succeeded")
			}
			for _, expected := range testCase.contains {
				if !strings.Contains(err.Error(), expected) {
					t.Errorf("the message does not mention %q: %v", expected, err)
				}
			}

			// Checked at the adapter rather than at the outcome: that no tmux
			// command ran is a stronger statement than that no terminal exists.
			if calls := arranged.tmux.Calls(); len(calls) != 0 {
				t.Errorf("the refused launch still ran %d tmux commands: %v", len(calls), calls)
			}
			stored := arranged.reload(t, draft.ID)
			if stored.Session != nil {
				t.Error("the refused launch recorded an agent session")
			}
			if stored.Workflow != domain.WorkflowFailed {
				t.Errorf("workflow = %q, want failed: a launch that could not start must be explainable",
					stored.Workflow)
			}
		})
	}
}

// TestAContainerThatHandsTheAgentRootIsLaunchedAndSaidSo is G7-05 at the layer
// that decides what happens about it.
//
// The launch goes ahead, because a project may have installed `sudo` on purpose
// and a refusal would be Feat overruling a choice that is the project's to make.
// What it must not do is go ahead silently: the requirement "the container user
// is non-root" is satisfied at the instant it is measured and not afterwards, so
// the launch says which container that is true of, against the task, where the
// person running it will find it.
func TestAContainerThatHandsTheAgentRootIsLaunchedAndSaidSo(t *testing.T) {
	arranged := arrangeDrafting(t)
	arranged.docker.Answer(containerProbe("sudo", "-n", "true"), "")

	task := arranged.launched(t)

	if task.Session == nil {
		t.Fatal("a container that grants passwordless root was refused rather than reported")
	}
	log, err := arranged.service.store.Events().Replay(context.Background(),
		store.TaskRef{Project: "app", Task: task.ID})
	if err != nil {
		t.Fatalf("replaying the task's events: %v", err)
	}

	var said string
	for _, event := range log.Events {
		if strings.Contains(event.Detail, "sudo") {
			said = event.Detail
		}
	}
	if said == "" {
		t.Fatalf("nothing in the task's %d events says the agent can become root there", len(log.Events))
	}
	for _, expected := range []string{"sudo", "service " + fixtureService, "without a password"} {
		if !strings.Contains(said, expected) {
			t.Errorf("the recorded warning does not mention %q: %v", expected, said)
		}
	}
}

// TestAContainerThatGrantsNothingSaysNothing keeps the report above from being
// noise on every launch.
//
// A warning that appears whatever the container is says nothing about any
// container, and the fixture's image is the ordinary case.
func TestAContainerThatGrantsNothingSaysNothing(t *testing.T) {
	arranged := arrangeDrafting(t)

	task := arranged.launched(t)

	log, err := arranged.service.store.Events().Replay(context.Background(),
		store.TaskRef{Project: "app", Task: task.ID})
	if err != nil {
		t.Fatalf("replaying the task's events: %v", err)
	}
	for _, event := range log.Events {
		if strings.Contains(event.Detail, "without a password") {
			t.Errorf("a container that grants nothing was reported as granting root: %v", event.Detail)
		}
	}
}

// TestAnAgentIsNeverStartedInARefusedContainer is the narrow form of the rule
// above.
//
// The refusals exist to keep an agent out of a container that breaks the
// security model, so the thing worth checking is not only that the launch failed
// but that nothing ran the agent anyway.
func TestAnAgentIsNeverStartedInARefusedContainer(t *testing.T) {
	arranged := arrangeDrafting(t)
	arranged.docker.Answer(containerProbe("id", "-u"), "0\n")

	draft := arranged.draft(t, "Add a rate limit")
	arranged.selectRepositories(t, draft.ID)
	displayed := arranged.resolve(t, draft.ID)

	if _, err := arranged.service.LaunchDraft(context.Background(), draft.ID, api.Confirmation{Fingerprint: displayed.Fingerprint}); err == nil {
		t.Fatal("a root container was accepted")
	}
	for _, call := range arranged.docker.Calls() {
		if strings.Contains(call, "claude") && !strings.Contains(call, "--version") {
			t.Errorf("an agent was started in a refused container: %q", call)
		}
	}
}

// TestTheGeneratedOverrideIsNotWhereTheAgentCanReachIt checks where the document
// deciding the container's mounts lives.
//
// It is under the state directory, outside the control workspace, and outside
// every task worktree. A file the agent could write that decided what its own
// container mounts would undo every other mount rule.
func TestTheGeneratedOverrideIsNotWhereTheAgentCanReachIt(t *testing.T) {
	arranged := arrangeDrafting(t)
	task := arranged.launched(t)

	override := arranged.overridePath(t, task.ID)
	if _, err := os.Stat(override); err != nil {
		t.Fatalf("the generated override was not written: %v", err)
	}

	workspace, err := arranged.service.controlWorkspace(task)
	if err != nil {
		t.Fatalf("resolving the control workspace: %v", err)
	}
	if strings.HasPrefix(override, workspace.Root()) {
		t.Errorf("the generated override %s is inside the control workspace %s, which the agent writes to",
			override, workspace.Root())
	}
	for _, binding := range task.Repositories {
		if binding.WorktreePath != "" && strings.HasPrefix(override, binding.WorktreePath) {
			t.Errorf("the generated override %s is inside the task worktree %s", override, binding.WorktreePath)
		}
	}
}

// TestAStableRepositoryIsMountedReadOnlyFromItsCheckout is acceptance criterion
// 2 in its general form: the stable devcontainer code is read-only.
//
// A stable_read_only repository is not a task repository. It has no branch and
// no worktree, and the agent reads it from the ordinary checkout — so this is the
// one mount whose source is a directory the user works in themselves, and the
// only thing keeping the agent out of it is that Feat mounts it read-only.
func TestAStableRepositoryIsMountedReadOnlyFromItsCheckout(t *testing.T) {
	arranged := arrangeDrafting(t)
	task := arranged.launched(t)

	cfg := arranged.config(t)
	stable := filepath.Join(arranged.env.Home, "repos", "app", "tooling")
	cfg.Repositories["tooling"] = config.Repository{
		HostPath:      stable,
		Agent:         config.RepositoryAgent{ContainerPath: "/srv/tooling"},
		DefaultAccess: string(domain.DefaultAccessStableReadOnly),
	}

	workspace, err := arranged.service.controlWorkspace(task)
	if err != nil {
		t.Fatalf("resolving the control workspace: %v", err)
	}
	mounts, err := taskMounts(cfg, task, workspace)
	if err != nil {
		t.Fatalf("resolving the task's mounts: %v", err)
	}

	var found bool
	for _, mount := range mounts {
		if mount.Target != "/srv/tooling" {
			continue
		}
		found = true
		if mount.Source != stable {
			t.Errorf("the stable repository is mounted from %s, want its ordinary checkout %s",
				mount.Source, stable)
		}
		if !mount.ReadOnly {
			t.Error("the stable repository is mounted read-write, so the agent could edit the user's own checkout")
		}
	}
	if !found {
		t.Errorf("the stable repository is not mounted at all: %v", mounts)
	}
}

// TestAStableRepositoryIsNotAlsoForbidden keeps the two rules about ordinary
// checkouts from contradicting each other.
//
// Mounting a checkout is normally refused, because the agent would be able to
// edit the working copy the task exists to leave alone. A stable_read_only
// repository the task left stable is the declared exception: the project said
// the agent reads it from the checkout, and Feat mounts it read-only itself. If
// it were on the never-mount list, every launch of such a project would refuse
// its own mount.
func TestAStableRepositoryIsNotAlsoForbidden(t *testing.T) {
	arranged := arrangeDrafting(t)
	task := arranged.launched(t)
	cfg := arranged.config(t)

	stable := filepath.Join(arranged.env.Home, "repos", "app", "tooling")
	cfg.Repositories["tooling"] = config.Repository{
		HostPath:      stable,
		Agent:         config.RepositoryAgent{ContainerPath: "/srv/tooling"},
		DefaultAccess: string(domain.DefaultAccessStableReadOnly),
	}

	for _, forbidden := range checkouts(cfg, task) {
		if forbidden == stable {
			t.Error("a stable read-only repository is on the forbidden list, so launching would refuse its own mount")
		}
	}
	// It is not unprotected either: Feat mounts it at one target and nowhere
	// else, which is the weaker rule the stable kind carries.
	if !slices.Contains(stableCheckouts(cfg, task), stable) {
		t.Errorf("the stable checkout %s is not protected at all: %v", stable, stableCheckouts(cfg, task))
	}
	// The editable ones are still forbidden outright, or the rule would protect
	// nothing.
	editable := filepath.Join(arranged.env.Home, "repos", "app", "api")
	if !slices.Contains(checkouts(cfg, task), editable) {
		t.Errorf("the editable repository's checkout %s is not protected: %v", editable, checkouts(cfg, task))
	}
}

// TestAPromotedStableRepositoryIsAnOrdinaryCheckout is the half of the rule
// above that was missing.
//
// default_access: stable_read_only says how a repository participates by
// default, and DefaultAccess.Permits lets a task promote it to read-write. A
// promoted repository has a branch, a worktree, and a mount of that worktree
// like any other — so its ordinary checkout is the working copy the task exists
// to leave alone, and a base file mounting it beside the worktree is the silent
// failure ADR-033 evidence 1 records.
func TestAPromotedStableRepositoryIsAnOrdinaryCheckout(t *testing.T) {
	arranged := arrangeDrafting(t)
	task := arranged.launched(t)
	cfg := arranged.config(t)

	// The project keeps api stable, and this task promoted it: it is one of the
	// task's repositories, with a worktree of its own.
	repository, ok := cfg.Repository("api")
	if !ok {
		t.Fatal("the fixture has no api repository")
	}
	repository.DefaultAccess = string(domain.DefaultAccessStableReadOnly)
	cfg.Repositories["api"] = repository
	if _, promoted := task.Repository("api"); !promoted {
		t.Fatal("the launched task does not select the api repository")
	}

	if !slices.Contains(checkouts(cfg, task), repository.HostPath) {
		t.Errorf("the promoted repository's checkout %s is not forbidden: %v",
			repository.HostPath, checkouts(cfg, task))
	}
	if slices.Contains(stableCheckouts(cfg, task), repository.HostPath) {
		t.Errorf("the promoted repository's checkout %s is treated as one Feat mounts itself, "+
			"which it no longer is", repository.HostPath)
	}

	workspace, err := arranged.service.controlWorkspace(task)
	if err != nil {
		t.Fatalf("resolving the control workspace: %v", err)
	}
	spec, err := arranged.service.executionSpec(cfg, task, workspace)
	if err != nil {
		t.Fatalf("resolving the execution specification: %v", err)
	}
	// Feat does not mount the checkout of a repository it gave a worktree to.
	for _, mount := range spec.Mounts {
		if mount.Source == repository.HostPath {
			t.Errorf("the promoted repository is mounted from its checkout %s at %s", mount.Source, mount.Target)
		}
	}

	environment, err := arranged.service.environments(spec)
	if err != nil {
		t.Fatalf("building the environment: %v", err)
	}
	err = environment.Check(execution.Report{
		UID: 1000, UIDKnown: true, User: "developer",
		Privileges: ordinaryPrivileges(),
		Mounts: []execution.ObservedMount{
			{Type: "bind", Source: repository.HostPath, Destination: "/opt/api", Writable: true},
		},
	})
	if err == nil {
		t.Fatal("a container mounting the promoted repository's own checkout was accepted")
	}
	if !strings.Contains(err.Error(), "leave alone") {
		t.Errorf("the message does not say what the rule is: %v", err)
	}
}

// TestAStableCheckoutIsRefusedAnywhereButItsOwnMount is the milder half of the
// same defect.
//
// Feat mounts a stable checkout read-only at the container_path its repository
// configures. A base file that mounts the same checkout at a different target
// adds the user's own working copy beside it, usually writable, and Compose
// merges by target so nothing replaces it. "Stable read-only infrastructure
// checkout" is a category the product declares, and this is what makes it one
// the product enforces.
func TestAStableCheckoutIsRefusedAnywhereButItsOwnMount(t *testing.T) {
	arranged := arrangeDrafting(t)
	task := arranged.launched(t)

	cfg := arranged.config(t)
	stable := filepath.Join(arranged.env.Home, "repos", "app", "tooling")
	cfg.Repositories["tooling"] = config.Repository{
		HostPath:      stable,
		Agent:         config.RepositoryAgent{ContainerPath: "/srv/tooling"},
		DefaultAccess: string(domain.DefaultAccessStableReadOnly),
	}

	workspace, err := arranged.service.controlWorkspace(task)
	if err != nil {
		t.Fatalf("resolving the control workspace: %v", err)
	}
	spec, err := arranged.service.executionSpec(cfg, task, workspace)
	if err != nil {
		t.Fatalf("resolving the execution specification: %v", err)
	}
	environment, err := arranged.service.environments(spec)
	if err != nil {
		t.Fatalf("building the environment: %v", err)
	}

	// Feat's own mount, read-only at the configured container path.
	if err := environment.Check(execution.Report{
		UID: 1000, UIDKnown: true, User: "developer",
		Privileges: ordinaryPrivileges(),
		Mounts: []execution.ObservedMount{
			{Type: "bind", Source: stable, Destination: "/srv/tooling"},
		},
	}); err != nil {
		t.Errorf("Feat's own mount of the stable checkout was refused: %v", err)
	}

	// The same checkout anywhere else is the user's working copy.
	err = environment.Check(execution.Report{
		UID: 1000, UIDKnown: true, User: "developer",
		Privileges: ordinaryPrivileges(),
		Mounts: []execution.ObservedMount{
			{Type: "bind", Source: stable, Destination: "/opt/tooling", Writable: true},
		},
	})
	if err == nil {
		t.Fatal("a second mount of the stable checkout was accepted")
	}
	for _, expected := range []string{"/srv/tooling", "container_path"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("the message does not mention %q: %v", expected, err)
		}
	}
}

// TestTheGitDirectoryIsMountedSoTheWorktreeIsARepository is acceptance
// criterion 5's first half, and the failure it prevents is total.
//
// A task worktree is not a repository on its own: its .git is a file naming the
// main checkout's Git directory by absolute host path. Without that directory
// mounted, every Git command in the container reports "not a git repository" —
// so the agent could not commit, diff, or read a log, while the container, the
// mounts, and every state Feat records looked exactly right.
func TestTheGitDirectoryIsMountedSoTheWorktreeIsARepository(t *testing.T) {
	arranged := arrangeDrafting(t)
	task := arranged.launched(t)

	workspace, err := arranged.service.controlWorkspace(task)
	if err != nil {
		t.Fatalf("resolving the control workspace: %v", err)
	}
	cfg := arranged.config(t)
	spec, err := arranged.service.executionSpec(cfg, task, workspace)
	if err != nil {
		t.Fatalf("resolving the execution specification: %v", err)
	}

	for _, binding := range task.Repositories {
		repository, ok := cfg.Repository(binding.RepositoryID.String())
		if !ok {
			continue
		}
		metadata := filepath.Join(repository.HostPath, ".git")

		var mounted bool
		for _, mount := range spec.Mounts {
			if mount.Source != metadata {
				continue
			}
			mounted = true
			// At the same path, because the link the worktree records is the
			// host's absolute one and it has to resolve unchanged.
			if mount.Target != metadata {
				t.Errorf("repository %s mounts its Git directory at %s, want the host path %s: "+
					"the worktree's recorded link would not resolve",
					binding.RepositoryID, mount.Target, metadata)
			}
			// And with the worktree's own access: a task that may not write the
			// code may not rewrite the history either.
			wantReadOnly := binding.Access == domain.TaskAccessReadOnly
			if mount.ReadOnly != wantReadOnly {
				t.Errorf("repository %s mounts its Git directory read-only=%t, want %t to match its %s access",
					binding.RepositoryID, mount.ReadOnly, wantReadOnly, binding.Access)
			}
		}
		if !mounted {
			t.Errorf("repository %s does not mount %s, so its worktree is not a repository in the container",
				binding.RepositoryID, metadata)
		}
	}
}

// TestTheWorkingCopyIsStillOutOfReach keeps the mount above from undoing the
// rule it sits beside.
//
// The Git directory carries history, which the security model exposes by name.
// The working copy is a different thing, and nothing may mount it — not the
// checkout, not a directory holding it, and not a directory inside it.
func TestTheWorkingCopyIsStillOutOfReach(t *testing.T) {
	arranged := arrangeDrafting(t)
	task := arranged.launched(t)

	cfg := arranged.config(t)
	repository, ok := cfg.Repository("api")
	if !ok {
		t.Fatal("the fixture has no api repository")
	}
	if task.Session == nil || task.Session.Execution == nil {
		t.Fatal("the launched task records no execution environment")
	}

	workspace, err := arranged.service.controlWorkspace(task)
	if err != nil {
		t.Fatalf("resolving the control workspace: %v", err)
	}
	spec, err := arranged.service.executionSpec(cfg, task, workspace)
	if err != nil {
		t.Fatalf("resolving the execution specification: %v", err)
	}
	environment, err := arranged.service.environments(spec)
	if err != nil {
		t.Fatalf("building the environment: %v", err)
	}

	// What Feat generates is accepted, including the Git directory.
	permitted := []execution.ObservedMount{
		{Type: "bind", Source: filepath.Join(repository.HostPath, ".git"),
			Destination: filepath.Join(repository.HostPath, ".git"), Writable: true},
	}
	if err := environment.Check(execution.Report{
		UID: 1000, UIDKnown: true, User: "developer",
		Privileges: ordinaryPrivileges(), Mounts: permitted,
	}); err != nil {
		t.Errorf("the Git directory mount was refused: %v", err)
	}

	// The working copy is not, however it is reached.
	for name, source := range map[string]string{
		"the checkout itself":     repository.HostPath,
		"a directory holding it":  filepath.Dir(repository.HostPath),
		"a directory inside it":   filepath.Join(repository.HostPath, "src"),
		"its Git directory's own": filepath.Join(repository.HostPath, ".gitignore"),
	} {
		err := environment.Check(execution.Report{
			UID: 1000, UIDKnown: true, User: "developer",
			Privileges: ordinaryPrivileges(),
			Mounts: []execution.ObservedMount{
				{Type: "bind", Source: source, Destination: "/mounted", Writable: true},
			},
		})
		if err == nil {
			t.Errorf("%s (%s) was accepted as a mount", name, source)
		}
	}
}

// TestFeatsOwnDirectoriesAreOutOfReachToo is the rule CLAUDE.md states by hand:
// no daemon or runtime-control socket reaches the agent's container.
//
// The daemon is the only place that knows where those sockets are, so this is
// the layer that has to name them. Feat mounts none of these directories, and a
// project whose own Compose files mount one would otherwise hand the agent the
// tmux server that runs every task's session, the API that launches and cleans
// up every task, and the state directory holding every other task's control
// workspace.
func TestFeatsOwnDirectoriesAreOutOfReachToo(t *testing.T) {
	arranged := arrangeDrafting(t)
	task := arranged.launched(t)

	workspace, err := arranged.service.controlWorkspace(task)
	if err != nil {
		t.Fatalf("resolving the control workspace: %v", err)
	}
	spec, err := arranged.service.executionSpec(arranged.config(t), task, workspace)
	if err != nil {
		t.Fatalf("resolving the execution specification: %v", err)
	}
	environment, err := arranged.service.environments(spec)
	if err != nil {
		t.Fatalf("building the environment: %v", err)
	}

	// The daemon's half: the resolved specification names them at all.
	for kind, want := range map[execution.ForbiddenKind]string{
		execution.ForbiddenRuntime: arranged.layout.Runtime,
		execution.ForbiddenState:   arranged.layout.State,
		execution.ForbiddenHome:    arranged.env.Home,
	} {
		var named bool
		for _, forbidden := range spec.ForbiddenSources {
			if forbidden.Kind == kind && forbidden.Path == want {
				named = true
			}
		}
		if !named {
			t.Errorf("the specification does not forbid the %s directory %s: %v", kind, want, spec.ForbiddenSources)
		}
	}

	// The adapter's half: a container that turns out to mount one is refused,
	// whether it names the directory or a directory holding it.
	for name, source := range map[string]string{
		"the runtime directory":     arranged.layout.Runtime,
		"the daemon's socket":       arranged.layout.Socket,
		"the tmux socket":           arranged.layout.TmuxSocket(),
		"a directory holding them":  filepath.Dir(arranged.layout.Runtime),
		"the state directory":       arranged.layout.State,
		"every task's control root": arranged.layout.ControlRoot(),
		"the home directory":        arranged.env.Home,
	} {
		err := environment.Check(execution.Report{
			UID: 1000, UIDKnown: true, User: "developer",
			Privileges: ordinaryPrivileges(),
			Mounts: []execution.ObservedMount{
				{Type: "bind", Source: source, Destination: "/mounted", Writable: true},
			},
		})
		if err == nil {
			t.Errorf("%s (%s) was accepted as a mount", name, source)
		}
	}

	// And this task's own control workspace, which lives inside the state
	// directory, is still accepted: a rule that refused it would refuse every
	// launch of every project.
	own := make([]execution.ObservedMount, 0, len(spec.Mounts))
	for _, mount := range spec.Mounts {
		own = append(own, execution.ObservedMount{
			Type: "bind", Source: mount.Source, Destination: mount.Target, Writable: !mount.ReadOnly,
		})
	}
	if err := environment.Check(execution.Report{
		UID: 1000, UIDKnown: true, User: "developer",
		Privileges: ordinaryPrivileges(), Mounts: own,
	}); err != nil {
		t.Errorf("the task's own mounts were refused: %v", err)
	}
}
