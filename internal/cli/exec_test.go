package cli

import (
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// helperEnv names the ending the helper process below should produce.
const helperEnv = "FEAT_TEST_EXEC_ENDING"

// TestExecHelperProcess is not a test. It is the program the test below runs,
// re-executed from this binary so that an ending can be arranged without
// spawning a shell, which internal/guard forbids and which would be a
// convenience here rather than the subject.
func TestExecHelperProcess(t *testing.T) {
	switch os.Getenv(helperEnv) {
	case "":
		t.Skip("not a test: the helper process of the exec test below")

	case "interrupted":
		// What a program that handles the interrupt itself does. Compose exits
		// 130 when a user leaves `docker compose logs --follow`.
		os.Exit(130)

	case "signalled":
		// And what one that installs no handler does: the signal ends it.
		_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
		time.Sleep(5 * time.Second)
		// Only reached if something swallowed the signal, and this status says
		// so rather than letting the case pass without testing anything.
		os.Exit(7)

	case "failed":
		os.Exit(1)
	}
}

// TestLeavingAProgramTheDashboardLentTheTerminalToIsNotAFailure covers the way
// out of the Compose logs.
//
// `docker compose logs --follow` ends when the user interrupts it, which is the
// only way it ends: it has no other exit. Reporting the status that produced —
// 130, or the signal itself for a program that installs no handler — would put a
// failure banner on the dashboard for a key the user meant to press. A status
// that is not an interrupt is still reported, because a diff tool that could not
// open is something the user needs to know about (ADR-049).
func TestLeavingAProgramTheDashboardLentTheTerminalToIsNotAFailure(t *testing.T) {
	for name, testCase := range map[string]struct {
		ending  string
		wantErr string
	}{
		"interrupted, and it handled the interrupt itself": {ending: "interrupted"},
		"interrupted, and it had no handler of its own":    {ending: "signalled"},
		"it failed on its own":                             {ending: "failed", wantErr: "exited with status 1"},
		"it finished":                                      {ending: "finished"},
	} {
		t.Run(name, func(t *testing.T) {
			// #nosec G204 -- this test binary, re-executed as its own helper.
			process := exec.Command(os.Args[0], "-test.run=^TestExecHelperProcess$")
			process.Env = append(os.Environ(), helperEnv+"="+testCase.ending)
			process.Stdout, process.Stderr = io.Discard, io.Discard

			err := execCommand{process}.Run()
			switch {
			case testCase.wantErr == "" && err != nil:
				t.Fatalf("leaving the program was reported as a failure: %v", err)
			case testCase.wantErr != "" && err == nil:
				t.Fatal("a program that failed on its own was reported as an ordinary exit")
			case testCase.wantErr != "" && !strings.Contains(err.Error(), testCase.wantErr):
				t.Errorf("the failure does not say what happened.\n got: %s\nwant it to contain: %s",
					err, testCase.wantErr)
			}
		})
	}
}
