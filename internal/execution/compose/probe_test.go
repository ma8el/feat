package compose_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/execution"
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

	// No Docker client in the container: the environment reports that there is
	// no such executable, which is what a real Compose says.
	return docker.Fail(probeKey("docker", "--version"),
		`exec: "docker": executable file not found in $PATH`, 126)
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

	for _, expected := range []string{"Docker client", "denied", "reach the host"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("the message does not mention %q: %v", expected, err)
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

	for _, expected := range []string{"root", "Docker client", "cat"} {
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
