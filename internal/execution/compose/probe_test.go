package compose_test

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/execution"
	"github.com/ma8el/feat/internal/execution/compose"
	"github.com/ma8el/feat/internal/execution/compose/composetest"
)

// probeKey renders the shortened command a probe runs, so a test can arrange an
// answer for exactly one of them.
func probeKey(program string, arguments ...string) string {
	parts := append([]string{"exec", "--no-TTY", "--user", "dev", "--workdir", "/srv/api", "dev", program},
		arguments...)
	return strings.Join(parts, " ")
}

// healthy arranges a container that passes every check.
func healthy() *composetest.Docker {
	docker := composetest.New().
		Answer(probeKey("id", "-u"), "1000\n").
		Answer(probeKey("id", "-un"), "dev\n").
		Answer(probeKey("sh", "-c", ":"), "").
		Answer(probeKey("mktemp", "-u"), "/tmp/tmp.AAA").
		Answer(probeKey("date", "-u", "+%Y"), "2026").
		Answer(probeKey("cat", "/dev/null"), "").
		Answer(probeKey("touch", "--help"), "").
		Answer(probeKey("mv", "--help"), "").
		Answer(probeKey("rm", "--help"), "").
		Answer(probeKey("touch", "/feat/.feat-write-probe"), "").
		Answer(probeKey("rm", "-f", "/feat/.feat-write-probe"), "")

	// No client that speaks the Docker API: the environment reports that there
	// is no such executable, which is what a real Compose says.
	for _, client := range compose.ContainerClients {
		docker = docker.Fail(probeKey(client, "--version"),
			`exec: "`+client+`": executable file not found in $PATH`, 126)
	}
	// And nothing in the image that gives the agent back the privilege its user
	// does not have, stated the same way.
	for _, tool := range compose.EscalationTools {
		docker = docker.Fail(probeKey(tool.Name, tool.Arguments...),
			`exec: "`+tool.Name+`": executable file not found in $PATH`, 126)
	}
	return docker
}

// warnings runs the probes and returns what the container was warned about,
// having first established that it was not refused.
//
// The order is the point: a warning is what a launch says about a container it
// is going ahead with, so a test that only read the warnings would pass if the
// same observation had quietly become a refusal.
func warnings(t *testing.T, docker *composetest.Docker) []string {
	t.Helper()

	environment, _ := arrange(t, docker)
	report, err := environment.Inspect(context.Background(), []string{"/feat"})
	if err != nil {
		t.Fatalf("inspecting the container: %v", err)
	}
	if err := environment.Check(report); err != nil {
		t.Fatalf("the container was refused rather than warned about: %v", err)
	}
	return environment.Warnings(report)
}

// inspect runs the probes against an arranged container.
func inspect(t *testing.T, docker *composetest.Docker) execution.Report {
	t.Helper()

	environment, _ := arrange(t, docker)
	report, err := environment.Inspect(context.Background(), []string{"/feat"})
	if err != nil {
		t.Fatalf("inspecting the container: %v", err)
	}
	return report
}

// check runs the probes and returns the refusal, which must exist.
func check(t *testing.T, docker *composetest.Docker) error {
	t.Helper()

	environment, _ := arrange(t, docker)
	report, err := environment.Inspect(context.Background(), []string{"/feat"})
	if err != nil {
		t.Fatalf("inspecting the container: %v", err)
	}
	err = environment.Check(report)
	if err == nil {
		t.Fatal("the container was accepted")
	}
	return err
}

// TestAHealthyContainerIsAccepted keeps every refusal below from being
// satisfied by a check that refuses everything.
func TestAHealthyContainerIsAccepted(t *testing.T) {
	environment, _ := arrange(t, healthy())

	report, err := environment.Inspect(context.Background(), []string{"/feat"})
	if err != nil {
		t.Fatalf("inspecting the container: %v", err)
	}
	if err := environment.Check(report); err != nil {
		t.Fatalf("a container that meets every requirement was refused: %v", err)
	}
	if report.UID != 1000 || report.User != "dev" {
		t.Errorf("the agent's identity was read as %d/%q, want 1000/%q", report.UID, report.User, "dev")
	}
}

// TestARootAgentIsRefused is acceptance criterion 3, checked against the
// process rather than against the configuration.
//
// Configuration already refuses a root user, so this is the second of the two
// places it is checked: a container whose image ignores the configured user, or
// whose user resolves to uid 0, is caught here and nowhere else.
func TestARootAgentIsRefused(t *testing.T) {
	err := check(t, healthy().Answer(probeKey("id", "-u"), "0\n").Answer(probeKey("id", "-un"), "root\n"))

	for _, expected := range []string{"root", "uid 0", "agent.execution.user"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("the message does not mention %q: %v", expected, err)
		}
	}
}

// TestAnUnreadableIdentityIsRefusedRatherThanAssumed keeps a probe that could
// not answer from being read as a non-root answer.
//
// The conservative direction matters: assuming non-root would let exactly the
// configuration this check exists for through.
func TestAnUnreadableIdentityIsRefusedRatherThanAssumed(t *testing.T) {
	err := check(t, healthy().Answer(probeKey("id", "-u"), "who knows\n"))

	if !strings.Contains(err.Error(), "could not be read") {
		t.Errorf("an unreadable identity was not reported as such: %v", err)
	}
}

// TestADockerClientInTheContainerIsRefused is the other half of criterion 4.
//
// A socket is refused by the mount check; a client is refused here. Both are
// needed: a client with no socket today is a capability waiting for a mount
// somebody adds later, and agent.capabilities.docker says denied.
func TestADockerClientInTheContainerIsRefused(t *testing.T) {
	err := check(t, healthy().Answer(probeKey("docker", "--version"), "Docker version 27.0.0"))

	for _, expected := range []string{"Docker API", "denied", "reach the host"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("the message does not mention %q: %v", expected, err)
		}
	}
}

// TestAClientUnderAnotherNameIsRefusedToo widens the same criterion to what a
// container client actually is.
//
// podman and nerdctl speak the Docker API — podman ships a `docker` alias for
// exactly that reason — so an image carrying either has the capability
// agent.capabilities.docker declares denied. Probing one name and reporting "no
// Docker client" would be a claim about something nobody looked at.
func TestAClientUnderAnotherNameIsRefusedToo(t *testing.T) {
	for _, client := range []string{"podman", "nerdctl"} {
		t.Run(client, func(t *testing.T) {
			err := check(t, healthy().Answer(probeKey(client, "--version"), client+" version 5.0.0"))

			for _, expected := range []string{client, "Docker API", "denied"} {
				if !strings.Contains(err.Error(), expected) {
					t.Errorf("the message does not mention %q: %v", expected, err)
				}
			}
		})
	}
}

// TestADockerEndpointInTheEnvironmentIsRefused is the clause of the Docker
// boundary that had no check at all.
//
// docs/05-security-model.md forbids Docker-over-TCP credentials beside the
// socket, and a daemon reached over the network is the same capability as a
// mounted one: no mount to see, and nothing in the container to find by
// probing for executables.
func TestADockerEndpointInTheEnvironmentIsRefused(t *testing.T) {
	err := check(t, healthy().Answer("inspect --type container --format {{json .Config.Env}} c0ffee",
		`["PATH=/usr/bin","DOCKER_HOST=tcp://198.51.100.7:2375","DOCKER_TLS_VERIFY=1"]`))

	for _, expected := range []string{"DOCKER_HOST", "DOCKER_TLS_VERIFY", "denied"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("the message does not mention %q: %v", expected, err)
		}
	}
	// The names are what a refusal needs. A value carries a host, a port, and a
	// path, and it reaches the daemon log and the API through this error.
	if strings.Contains(err.Error(), "198.51.100.7") {
		t.Errorf("the refusal repeats the value of an environment entry: %v", err)
	}
}

// TestTheEnvironmentIsReadFromTheContainerRatherThanTheConfiguration pins which
// command answers that question, for the reason the mount check pins its own:
// `docker compose config` would render the project's environment files, whose
// values Feat must never read (ADR-028).
func TestTheEnvironmentIsReadFromTheContainerRatherThanTheConfiguration(t *testing.T) {
	docker := healthy()
	inspect(t, docker)

	if !docker.Ran("inspect --type container --format {{json .Config.Env}} c0ffee") {
		t.Errorf("nothing asked the container what its environment is: %v", docker.Calls())
	}
	for _, call := range docker.Calls() {
		if strings.HasPrefix(call, "config") {
			t.Errorf("the environment was read with %q, which renders the project's environment file values", call)
		}
	}
}

// TestAMissingHookToolIsRefused covers the failure that would otherwise be
// silent.
//
// The generated hooks are shell scripts. An image without mktemp runs the agent
// perfectly well and reports nothing at all, which looks like a task that is
// working rather than one that is broken.
func TestAMissingHookToolIsRefused(t *testing.T) {
	err := check(t, healthy().Fail(probeKey("mktemp", "-u"),
		`exec: "mktemp": executable file not found in $PATH`, 126))

	for _, expected := range []string{"mktemp", "never hear from it"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("the message does not mention %q: %v", expected, err)
		}
	}
}

// TestAToolThatRunsAndFailsIsPresent keeps the presence probe from confusing
// "the image does not have it" with "it disagreed".
//
// busybox's touch prints usage and exits 1 for --help, and an image built on
// busybox is not an image missing touch.
func TestAToolThatRunsAndFailsIsPresent(t *testing.T) {
	report := inspect(t, healthy().Fail(probeKey("touch", "--help"), "BusyBox v1.36 usage: touch", 1))

	if len(report.MissingTools) != 0 {
		t.Errorf("a tool that ran and exited non-zero was reported as missing: %v", report.MissingTools)
	}
}

// TestAnUnwritableControlWorkspaceIsRefused is ADR-033 evidence 5.
//
// A control workspace the agent cannot write to is a session that reports
// nothing, and the cause — a container uid that does not match the host's — is
// invisible from inside Feat unless it is probed for.
func TestAnUnwritableControlWorkspaceIsRefused(t *testing.T) {
	err := check(t, healthy().Fail(probeKey("touch", "/feat/.feat-write-probe"),
		"touch: cannot touch '/feat/.feat-write-probe': Permission denied", 1))

	for _, expected := range []string{"/feat", "Permission denied", "uid"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("the message does not mention %q: %v", expected, err)
		}
	}
}

// TestTheWriteProbeLeavesNothingBehind checks that a probe which succeeded
// removes its own file.
func TestTheWriteProbeLeavesNothingBehind(t *testing.T) {
	docker := healthy()
	inspect(t, docker)

	if !docker.Ran(probeKey("rm", "-f", "/feat/.feat-write-probe")) {
		t.Errorf("the write probe did not remove its file; calls: %v", docker.Calls())
	}
}

// TestEveryProblemIsReportedTogether keeps a container with several mistakes
// from taking several launches to diagnose.
func TestEveryProblemIsReportedTogether(t *testing.T) {
	err := check(t, healthy().
		Answer(probeKey("id", "-u"), "0\n").
		Answer(probeKey("docker", "--version"), "Docker version 27.0.0").
		Fail(probeKey("cat", "/dev/null"), `exec: "cat": executable file not found in $PATH`, 126))

	for _, expected := range []string{"root", "Docker API", "cat"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("the combined message does not mention %q: %v", expected, err)
		}
	}
}

// TestEveryProbeRunsAsTheAgentsOwnUser checks that the answers describe the
// user the agent will be.
//
// A probe that ran as root would answer a question nobody asked: whether root
// can write the control workspace is not whether the agent can.
func TestEveryProbeRunsAsTheAgentsOwnUser(t *testing.T) {
	docker := healthy()
	inspect(t, docker)

	for _, call := range docker.Calls() {
		if !strings.HasPrefix(call, "exec ") {
			continue
		}
		if !strings.Contains(call, "--user dev") {
			t.Errorf("a probe did not run as the agent's user: %q", call)
		}
	}
}

// granted renders one `docker inspect` answer for .HostConfig from the fields a
// test cares about, over the defaults an ordinary container has.
//
// It is written as fields rather than as a JSON string so that a fixture states
// what the container was granted and nothing else, and so that two tests
// arranging different grants cannot disagree about the rest of the record.
func granted(fields map[string]any) string {
	config := map[string]any{
		"Privileged": false, "CapAdd": nil, "CapDrop": nil, "PidMode": "",
		"IpcMode": "private", "NetworkMode": "feat-agent-app-default", "Devices": []any{},
	}
	maps.Copy(config, fields)
	encoded, err := json.Marshal(config)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

// TestAContainerGrantedMoreThanItsMountsIsRefused is G4-04.
//
// The launch inspection asked the container two questions — its mounts and its
// environment — and nothing about what it was granted. So a check that refuses
// a home-directory mount accepted a privileged container, which is strictly
// more than that mount would have given away: CAP_SYS_ADMIN remounts the
// read-only control workspace read-write and mounts the host's filesystem from
// a block device, and the read-only half of what Feat grants held only because
// nobody had added the line.
func TestAContainerGrantedMoreThanItsMountsIsRefused(t *testing.T) {
	for name, testCase := range map[string]struct {
		granted  map[string]any
		contains string
	}{
		"privileged": {
			granted:  map[string]any{"Privileged": true},
			contains: "runs privileged",
		},
		"an added capability": {
			granted:  map[string]any{"CapAdd": []string{"SYS_ADMIN"}},
			contains: "SYS_ADMIN",
		},
		// The same capability written the way Linux names it. A deny-list with
		// a spelling as its bypass is not a deny-list.
		"the same capability spelled in full": {
			granted:  map[string]any{"CapAdd": []string{"CAP_SYS_ADMIN"}},
			contains: "SYS_ADMIN",
		},
		"every capability at once": {
			granted:  map[string]any{"CapAdd": []string{"ALL"}},
			contains: "ALL",
		},
		"reading any file on the host": {
			granted:  map[string]any{"CapAdd": []string{"DAC_READ_SEARCH"}},
			contains: "open_by_handle_at",
		},
		"the host's PID namespace": {
			granted:  map[string]any{"PidMode": "host"},
			contains: "pid: host",
		},
		"the host's IPC namespace": {
			granted:  map[string]any{"IpcMode": "host"},
			contains: "ipc: host",
		},
		// The one that reaches a Docker daemon with nothing mounted and no
		// DOCKER_HOST for the environment check to find.
		"the host's network namespace": {
			granted:  map[string]any{"NetworkMode": "host"},
			contains: "127.0.0.1:2375",
		},
		"a host device": {
			granted: map[string]any{"Devices": []map[string]string{
				{"PathOnHost": "/dev/sda1", "PathInContainer": "/dev/sda1"},
			}},
			contains: "/dev/sda1",
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := check(t, healthy().Inspect("c0ffee", "HostConfig", granted(testCase.granted)))

			if !strings.Contains(err.Error(), testCase.contains) {
				t.Errorf("the message does not explain the refusal\n got: %v\nwant: %q", err, testCase.contains)
			}
			// Every refusal names the service, because the line that produced
			// it is in the Compose files that define one.
			if !strings.Contains(err.Error(), "service dev") {
				t.Errorf("the message does not name the service to edit: %v", err)
			}
		})
	}
}

// TestTheLaunchAsksWhatTheContainerWasGranted pins the question rather than the
// outcome.
//
// A rule can only be enforced about a field somebody asked for, and the
// blindness G4-04 records was exactly a missing question: `{{json .Mounts}}`
// and `{{json .Config.Env}}` and nothing else. A test that only checked the
// refusals would pass again the day the third question was dropped and the
// evidence went back to being unread.
func TestTheLaunchAsksWhatTheContainerWasGranted(t *testing.T) {
	docker := healthy()
	report := inspect(t, docker)

	if !docker.Ran("inspect --type container --format {{json .HostConfig}} c0ffee") {
		t.Errorf("nothing asked the container what it was granted: %v", docker.Calls())
	}
	if !report.Privileges.Known {
		t.Error("the report does not say the container's grants were read")
	}
	for _, call := range docker.Calls() {
		if strings.HasPrefix(call, "config") {
			t.Errorf("the grants were read with %q, which renders the project's environment file values", call)
		}
	}
}

// TestAnUnreadableGrantIsRefusedRatherThanAssumed keeps an answer nobody could
// read from being treated as an empty one.
//
// It is the direction TestAnUnreadableIdentityIsRefusedRatherThanAssumed takes,
// for the same reason: assuming the reassuring answer lets exactly the
// container this check exists for through.
func TestAnUnreadableGrantIsRefusedRatherThanAssumed(t *testing.T) {
	err := check(t, healthy().Inspect("c0ffee", "HostConfig", "null"))

	if !strings.Contains(err.Error(), "could not be read") {
		t.Errorf("an unreadable host configuration was not reported as such: %v", err)
	}
}

// TestADebuggersCapabilityIsAccepted holds the one deliberate hole in the
// capability rule to a test, so that closing it is a decision rather than an
// accident.
//
// SYS_PTRACE is what a debugger needs and what devcontainer templates add for
// exactly that. Inside the container's own PID namespace it traces the agent's
// own processes, and it reaches the host's only through a shared PID namespace,
// which is refused on its own.
func TestADebuggersCapabilityIsAccepted(t *testing.T) {
	environment, _ := arrange(t, healthy().
		Inspect("c0ffee", "HostConfig", granted(map[string]any{"CapAdd": []string{"SYS_PTRACE"}})))

	report, err := environment.Inspect(context.Background(), []string{"/feat"})
	if err != nil {
		t.Fatalf("inspecting the container: %v", err)
	}
	if err := environment.Check(report); err != nil {
		t.Errorf("a container with a debugger's capability was refused: %v", err)
	}
}

// TestAContainerThatReturnsRootWithoutAPasswordIsReported is G7-05.
//
// The root refusal reads `id -u` as the configured user, which answers what the
// agent starts as. An image that also installs `sudo` and writes NOPASSWD makes
// that answer true of the probe and not of the session: the check passes, and
// the agent is uid 0 one command later with the runtime's default capabilities,
// which include CAP_DAC_OVERRIDE. Nothing asked, so nothing said.
func TestAContainerThatReturnsRootWithoutAPasswordIsReported(t *testing.T) {
	said := warnings(t, healthy().Answer(probeKey("sudo", "-n", "true"), ""))

	if len(said) != 1 {
		t.Fatalf("the launch said %d things about a container that hands the agent root, want 1: %v",
			len(said), said)
	}
	for _, expected := range []string{
		// What was found, and where the line that produced it lives.
		"sudo", "service dev",
		// Who it is true of, so it cannot be read as being about some other user.
		"dev", "uid 1000",
		// Why it matters and what it does not reach, because a warning that
		// overstates its case is one people learn to skip.
		"without a password", "write through every writable mount", "not enough to remount",
	} {
		if !strings.Contains(said[0], expected) {
			t.Errorf("the warning does not mention %q: %v", expected, said[0])
		}
	}
}

// TestAnEscalationToolUnderAnotherNameIsReportedToo widens the same finding to
// what the question actually is.
//
// `sudo` is one binary that hands privilege back and the dogfood image is one
// image. A check written for the name rather than for the capability is
// answered by installing the other one, which is the shape ContainerClients was
// widened for after the same reasoning about `podman`.
func TestAnEscalationToolUnderAnotherNameIsReportedToo(t *testing.T) {
	for _, tool := range compose.EscalationTools {
		t.Run(tool.Name, func(t *testing.T) {
			said := warnings(t, healthy().Answer(probeKey(tool.Name, tool.Arguments...), ""))

			if len(said) != 1 || !strings.Contains(said[0], tool.Name) {
				t.Errorf("a container whose %s returns root without a password was described as %v",
					tool.Name, said)
			}
		})
	}
}

// TestAToolThatWouldAskForAPasswordIsNotAGrant keeps the probe from confusing an
// image that has `sudo` with an image that gives the agent anything.
//
// A sudoers file that grants the agent nothing is the non-root requirement
// holding, demonstrated rather than assumed. Warning about it would be a warning
// on every image built from a distribution base, which teaches a user to skip
// the one that means something.
func TestAToolThatWouldAskForAPasswordIsNotAGrant(t *testing.T) {
	docker := healthy().Fail(probeKey("sudo", "-n", "true"),
		"sudo: a password is required", 1)

	report := inspect(t, docker)
	if len(report.Escalation) != 0 {
		t.Errorf("a tool that refused without a password was reported as a grant: %v", report.Escalation)
	}
	if said := warnings(t, docker); len(said) != 0 {
		t.Errorf("a container that grants nothing was warned about: %v", said)
	}
}

// TestTheLaunchAsksWhetherTheAgentCanBecomeRoot pins the question rather than
// the outcome, for the reason TestTheLaunchAsksWhatTheContainerWasGranted pins
// its own: a rule can only be enforced about something somebody asked for, and
// G7-05 is a missing question rather than a wrong answer. A test that checked
// only the warning would pass again on the day the probe was dropped and every
// container went back to answering `dev`.
func TestTheLaunchAsksWhetherTheAgentCanBecomeRoot(t *testing.T) {
	docker := healthy()
	inspect(t, docker)

	for _, tool := range compose.EscalationTools {
		if !docker.Ran(probeKey(tool.Name, tool.Arguments...)) {
			t.Errorf("nothing asked the container whether %s returns root there: %v", tool.Name, docker.Calls())
		}
	}
}

// TestEveryEscalationProbeRefusesToPrompt keeps a probe from being the thing
// that hangs a launch.
//
// Each of these tools asks for a password on a terminal when it needs one, and
// the probe runs without a terminal to ask on. The non-interactive flag is what
// turns "wait for somebody" into "answer no", so it is checked over the list
// rather than in the one test that happens to arrange a tool.
func TestEveryEscalationProbeRefusesToPrompt(t *testing.T) {
	for _, tool := range compose.EscalationTools {
		if !slices.Contains(tool.Arguments, "-n") {
			t.Errorf("the %s probe runs %v, which may wait for a password nobody can type",
				tool.Name, tool.Arguments)
		}
	}
}

// TestAHealthyContainerIsWarnedAboutNothing keeps the warnings above from being
// satisfied by a launch that warns about everything.
func TestAHealthyContainerIsWarnedAboutNothing(t *testing.T) {
	if said := warnings(t, healthy()); len(said) != 0 {
		t.Errorf("a container that meets every requirement was warned about: %v", said)
	}
}

// TestABindBackedVolumeIsRefusedAtLaunch is G7-01 through the path a launch
// actually takes.
//
// Every other test of this states the mount; this one starts from what `docker
// inspect` reports about a container and lets the adapter ask the second
// question for itself. The Compose file behind it is six lines a reviewer reads
// as a named volume:
//
//	volumes: {hostrun: {driver: local, driver_opts: {type: none, device: /var/run, o: bind}}}
//	services: {dev: {volumes: [hostrun:/mnt/probe]}}
//
// The container path is deliberately not /var/run. Landing on a known socket
// path is refused by a rule of its own, and a fixture that leant on it would
// pass with the device unread — which is the defect.
func TestABindBackedVolumeIsRefusedAtLaunch(t *testing.T) {
	err := check(t, healthy().
		Inspect("c0ffee", "Mounts", `[{"Type":"volume","Name":"hostrun",`+
			`"Source":"/var/lib/docker/volumes/hostrun/_data","Destination":"/mnt/probe","RW":true}]`).
		Volume("hostrun", map[string]string{"type": "none", "device": "/var/run", "o": "bind"}))

	for _, expected := range []string{"hostrun", "/var/run/docker.sock", "never grants an agent Docker access"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("the message does not mention %q: %v", expected, err)
		}
	}
}
