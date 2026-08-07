package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/api"
)

// runtimeStatus is a plausible response for one running task.
func runtimeStatus() api.RuntimeStatus {
	return api.RuntimeStatus{
		Task: api.Task{
			Key: "7f3a1c2e",
			Runtime: &api.Runtime{
				Provider: "compose",
				Identity: "feat-app-7f3a1c2e",
				Services: []string{"api"},
				State:    "running",
				Health:   "unknown",
				Ports:    []api.Port{{Service: "api", ContainerPort: 8000, HostPort: 8080}},
				Volumes:  []string{"feat-app-7f3a1c2e_pgdata"},
				External: []api.ExternalResource{
					{ID: "staging_db", Kind: "postgres", Lifecycle: "external", Selector: "7f3a1c2e"},
				},
			},
		},
		Services: []api.RuntimeService{
			{Name: "api", Container: "c0ffee", State: "running", Status: "Up 2 seconds", Health: "unknown"},
		},
	}
}

// TestTheRuntimeSummaryNamesWhatIsRetained keeps a resource nobody removed in
// front of the user.
//
// Volumes survive every destroy in v0, and an external resource is never
// touched at all. Both are printed by name, because a resource a user cannot see
// is one they will not think to clean up (FR-CLEAN-001, FR-CLEAN-004).
func TestTheRuntimeSummaryNamesWhatIsRetained(t *testing.T) {
	var out bytes.Buffer
	printRuntime(&out, runtimeStatus())
	printed := out.String()

	for _, required := range []string{
		"feat-app-7f3a1c2e",          // the Compose project an action reaches
		"api",                        // the service and its observed state
		"8000 -> 8080",               // how to reach the application
		"feat-app-7f3a1c2e_pgdata",   // retained
		"never created or destroyed", // and never Feat's to remove
		"this task's selector  7f3a1c2e",
	} {
		if !strings.Contains(printed, required) {
			t.Errorf("the summary does not mention %q:\n%s", required, printed)
		}
	}
}

// TestANoteIsPrintedWhereTheUserWillSeeIt keeps the silent mount failure visible.
func TestANoteIsPrintedWhereTheUserWillSeeIt(t *testing.T) {
	status := runtimeStatus()
	status.Notes = []string{"api mounts the ordinary checkout /repos/app/api rather than this task's worktree"}

	var out bytes.Buffer
	printRuntime(&out, status)

	if !strings.Contains(out.String(), "ordinary checkout") {
		t.Errorf("the note was not printed:\n%s", out.String())
	}
}

// TestDestroyingNeedsAnAnswer keeps a removal behind something the user typed.
//
// Anything other than an explicit yes is a no, including an empty line and an
// unreadable answer: a command that removes something must never proceed because
// it could not tell what it was told.
func TestDestroyingNeedsAnAnswer(t *testing.T) {
	for answer, want := range map[string]bool{
		"y\n":     true,
		"yes\n":   true,
		"Y\n":     true,
		"n\n":     false,
		"\n":      false,
		"":        false,
		"maybe\n": false,
	} {
		var out bytes.Buffer
		got, err := confirm(strings.NewReader(answer), &out, "Remove them?")
		if err != nil {
			t.Fatalf("confirm(%q): %v", answer, err)
		}
		if got != want {
			t.Errorf("confirm(%q) = %t, want %t", answer, got, want)
		}
		if !strings.Contains(out.String(), "[y/N]") {
			t.Errorf("the question does not show that no is the default: %s", out.String())
		}
	}
}

// TestTheLogsCommandIsCheckedBeforeItIsRun is the client half of the rule the
// daemon follows in the other direction.
//
// The daemon builds the command and this process runs it, exactly as `feat
// attach` runs native tmux. They are the same user, so this is not a security
// boundary — it is what keeps the client something anybody can reason about, and
// it fails loudly rather than executing whatever arrived.
func TestTheLogsCommandIsCheckedBeforeItIsRun(t *testing.T) {
	for name, testCase := range map[string]struct {
		command  api.RuntimeCommand
		contains string
	}{
		"another program entirely": {
			command:  api.RuntimeCommand{Program: "/bin/sh", Arguments: []string{"-c", "echo"}},
			contains: "runs only docker",
		},
		"a program resolved from a path": {
			command:  api.RuntimeCommand{Program: "docker", Arguments: []string{"compose", "logs"}},
			contains: "non-absolute",
		},
		"an argument carrying a newline": {
			command:  api.RuntimeCommand{Program: "/usr/bin/docker", Arguments: []string{"compose\nrm"}},
			contains: "newline",
		},
	} {
		t.Run(name, func(t *testing.T) {
			var out, errOut bytes.Buffer

			err := runLogs(context.Background(), testCase.command, &out, &errOut)
			if err == nil {
				t.Fatal("the command was run, and it should have been refused")
			}
			if !strings.Contains(err.Error(), testCase.contains) {
				t.Errorf("the refusal does not explain itself.\n got: %s\nwant it to contain: %s",
					err, testCase.contains)
			}
		})
	}
}
