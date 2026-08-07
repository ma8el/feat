package review

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/domain"
)

// look finds a standard executable, or skips the test on a machine without one.
func look(t *testing.T, program string) string {
	t.Helper()

	path, err := exec.LookPath(program)
	if err != nil {
		t.Skipf("this machine has no %s: %v", program, err)
	}
	return path
}

// TestAHostCheckThatFailsIsAnAnswer checks the distinction every runner in this
// repository draws: a command that ran and exited non-zero has answered, and
// only a command that could not be started at all is an error.
func TestAHostCheckThatFailsIsAnAnswer(t *testing.T) {
	output, err := HostRunner{}.Run(context.Background(), Check{
		ID: "test", Program: look(t, "false"), Directory: t.TempDir(), OnHost: true,
	})
	if err != nil {
		t.Fatalf("a check that exited non-zero was reported as an error: %v", err)
	}
	if output.ExitCode == 0 {
		t.Error("a check that exited non-zero was recorded as having succeeded")
	}

	results := Gate{Host: HostRunner{}}.Run(context.Background(), []Check{
		{ID: "test", RepositoryID: "api", Program: look(t, "false"), Directory: t.TempDir(), OnHost: true},
	})
	if results[0].Status != domain.CheckFailed {
		t.Errorf("the recorded result is %s, want failed", results[0].Status)
	}
}

// TestAHostCheckRunsInTheTaskWorktree checks that a check is run where the task
// is, rather than wherever the daemon happens to have been started.
func TestAHostCheckRunsInTheTaskWorktree(t *testing.T) {
	worktree := t.TempDir()

	output, err := HostRunner{}.Run(context.Background(), Check{
		ID: "where", Program: look(t, "pwd"), Directory: worktree, OnHost: true,
	})
	if err != nil {
		t.Fatalf("running a check: %v", err)
	}
	// The temporary directory may be reached through a symbolic link, so the
	// tail is what identifies it.
	if !strings.HasSuffix(strings.TrimSpace(output.Stdout), strings.TrimPrefix(worktree, "/private")) {
		t.Errorf("the check ran in %q, want the task worktree %q", strings.TrimSpace(output.Stdout), worktree)
	}
}

// TestAnAbsentHostCheckProgramIsNotAFailedCheck checks that an uninstalled tool
// sends the user to their configuration rather than to their code.
func TestAnAbsentHostCheckProgramIsNotAFailedCheck(t *testing.T) {
	results := Gate{Host: HostRunner{}}.Run(context.Background(), []Check{
		{ID: "test", RepositoryID: "api", Program: "feat-no-such-program", Directory: t.TempDir(), OnHost: true},
	})

	if results[0].Status == domain.CheckFailed {
		t.Fatal("an absent program was reported as a failing check")
	}
	if results[0].Status != domain.CheckUnknown {
		t.Fatalf("the result is %s, want unknown", results[0].Status)
	}
	if !strings.Contains(results[0].Detail, "could not be started") {
		t.Errorf("the detail is %q, and it should say the check never ran", results[0].Detail)
	}
}

// TestAHostCheckIsCheckedBeforeItRuns checks that a program that would be read
// as an option is refused, which is the rule ADR-029 set for Git arguments.
func TestAHostCheckIsCheckedBeforeItRuns(t *testing.T) {
	if _, err := (HostRunner{}).Run(context.Background(), Check{
		ID: "test", Program: "--upload-pack=curl", Directory: t.TempDir(), OnHost: true,
	}); err == nil {
		t.Fatal("a program beginning with a dash was run")
	}
}
