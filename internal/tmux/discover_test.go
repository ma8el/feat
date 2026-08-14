package tmux

import (
	"context"
	"testing"

	"github.com/ma8el/feat/internal/domain"
)

// deadPane seeds a tagged task terminal whose agent pane tmux reports as dead,
// with whatever it has published about how it ended.
func deadPane(t *testing.T, status, signal string) Terminal {
	t.Helper()

	runner := newFakeTmux()
	runner.sessions["$1"] = &fakeSession{id: "$1", options: map[string]string{
		optionManaged: "1", optionVersion: metadataVersion, optionProject: testProject.String(),
	}}
	runner.windows["@1"] = &fakeWindow{session: "$1", id: "@1", options: map[string]string{
		optionManaged: "1", optionVersion: metadataVersion,
		optionProject: testProject.String(), optionTask: testTask.String(),
	}}
	runner.panes["%1"] = &fakePane{
		session: "$1", window: "@1", id: "%1", directory: "/work/primary",
		dead: true, status: status, signal: signal,
		options: map[string]string{
			optionManaged: "1", optionVersion: metadataVersion,
			optionProject: testProject.String(), optionTask: testTask.String(),
			optionRole: roleAgent,
		},
	}

	backend, err := New("/run/user/501/feat/tmux.sock", runner)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	terminal, found, err := backend.Find(context.Background(), testProject, testTask)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if !found {
		t.Fatal("the seeded task terminal was not found")
	}
	return terminal
}

// TestAPaneIsDeadOnlyWhenTmuxCanSayHowItEnded is the failure Linux CI found, at
// the level that would have caught it on any machine.
//
// On tmux 3.4 `pane_dead` is the pane's closed file descriptor and nothing more,
// while `pane_dead_status` waits for `PANE_STATUSREADY` — the flag tmux sets
// once it has reaped the child. Between the two, tmux reports a dead pane with
// no outcome, and a reader that took the first and asked for the second in the
// same breath called a failed agent stopped. tmux 3.7 closed the gap by making
// `pane_dead` require the same flag, which is why a machine with a current tmux
// never sees it and why the integration test failed only on Linux.
func TestAPaneIsDeadOnlyWhenTmuxCanSayHowItEnded(t *testing.T) {
	terminal := deadPane(t, "", "")

	if terminal.Agent.Dead {
		t.Error("a pane whose outcome tmux has not published yet is reported as dead")
	}
	if got := terminal.ProcessState(); got != domain.ProcessRunning {
		t.Errorf("ProcessState = %q, want running: nothing can be said yet about how it ended, "+
			"and stopped is the claim that it ended cleanly", got)
	}
}

// TestAKilledPaneIsFailedRatherThanStopped is the other half of the same field.
//
// tmux publishes a process it saw exit as `pane_dead_status` and one that was
// killed as `pane_dead_signal`, so a killed pane has no exit status at all.
// Reading that absence as a clean exit reports an agent the OOM killer took as
// one that finished — which is the ordinary way a container's agent dies.
func TestAKilledPaneIsFailedRatherThanStopped(t *testing.T) {
	terminal := deadPane(t, "", "KILL")

	if !terminal.Agent.Dead {
		t.Fatal("a killed pane is not reported as dead")
	}
	if terminal.Agent.ExitStatus != nil {
		t.Errorf("the pane reports exit status %d, and tmux publishes none for a killed process",
			*terminal.Agent.ExitStatus)
	}
	if got := terminal.ProcessState(); got != domain.ProcessFailed {
		t.Errorf("ProcessState = %q, want failed", got)
	}
}

// TestAPaneThatExitedKeepsItsStatus is the case that already worked, kept beside
// the other two so the three shapes tmux reports are read in one place.
func TestAPaneThatExitedKeepsItsStatus(t *testing.T) {
	terminal := deadPane(t, "1", "")

	if !terminal.Agent.Dead {
		t.Fatal("a pane that exited is not reported as dead")
	}
	if terminal.Agent.ExitStatus == nil || *terminal.Agent.ExitStatus != 1 {
		t.Fatalf("exit status = %v, want 1", terminal.Agent.ExitStatus)
	}
	if got := terminal.ProcessState(); got != domain.ProcessFailed {
		t.Errorf("ProcessState = %q, want failed", got)
	}
}
