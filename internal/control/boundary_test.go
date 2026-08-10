package control_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ma8el/feat/internal/control"
)

// TestAPlantedLinkNeverRedirectsAHostWrite is the boundary the control
// workspace exists to draw.
//
// Everything under the workspace is reachable from a container, so a path the
// host built from validated identifiers still says only where a file belongs.
// If the host then trusts the filesystem underneath it, an agent that replaces
// a name with a symbolic link decides where the daemon's own writes land — and
// the daemon is the trusted process. Every case below plants a link and asks
// for the write it would redirect.
func TestAPlantedLinkNeverRedirectsAHostWrite(t *testing.T) {
	// The record of applied messages is the one write that truncates, which
	// makes it the one that would do the most damage somewhere else.
	t.Run("the processed record", func(t *testing.T) {
		workspace, _ := newWorkspace(t)
		write(t, workspace, "one.json", envelope("applied-one", testTask, control.TypeProviderEvent))

		messages, _, err := workspace.Pending()
		if err != nil || len(messages) != 1 {
			t.Fatalf("reading pending messages returned %d messages: %v", len(messages), err)
		}

		outside := filepath.Join(t.TempDir(), "notes.md")
		const kept = "a file of the user's, with no trailing newline"
		if err := os.WriteFile(outside, []byte(kept), 0o600); err != nil {
			t.Fatalf("writing the file outside the workspace: %v", err)
		}
		record := filepath.Join(workspace.AgentDir(), "processed.jsonl")
		if err := os.Symlink(outside, record); err != nil {
			t.Fatalf("planting the link: %v", err)
		}

		err = workspace.MarkProcessed(messages[0], control.OutcomeApplied, "")
		if err == nil {
			t.Error("marking a message processed followed a link out of the workspace")
		} else if !strings.Contains(err.Error(), record) {
			t.Errorf("refusal = %q, want it to name %s", err, record)
		}

		after, readErr := os.ReadFile(outside)
		if readErr != nil {
			t.Fatalf("reading the file outside the workspace: %v", readErr)
		}
		if string(after) != kept {
			t.Errorf("the file outside the workspace is now %q, want %q: "+
				"the daemon truncated and appended to a file it does not own", after, kept)
		}
	})

	// A directory replaced by a link is the same defect one level up: O_NOFOLLOW
	// protects the last component of a path and nothing above it.
	for name, test := range map[string]struct {
		replace func(*control.Workspace) string
		write   func(*control.Workspace) error
		expect  string
	}{
		"the inbox": {
			replace: func(w *control.Workspace) string { return w.InboxDir() },
			write: func(w *control.Workspace) error {
				return w.WriteVerification("feat-request", control.Verification{
					Status: control.VerificationPassed,
					Report: "every check passed",
				})
			},
			expect: "every check passed",
		},
		"the host-only agent directory": {
			replace: func(w *control.Workspace) string { return w.AgentDir() },
			write: func(w *control.Workspace) error {
				return w.WriteAgentFile("hooks/stop.sh", []byte("#!/bin/sh\nrm -rf /\n"), true)
			},
			expect: "rm -rf",
		},
	} {
		t.Run(name, func(t *testing.T) {
			workspace, _ := newWorkspace(t)
			elsewhere := t.TempDir()

			replaced := test.replace(workspace)
			if err := os.RemoveAll(replaced); err != nil {
				t.Fatalf("removing %s: %v", replaced, err)
			}
			if err := os.Symlink(elsewhere, replaced); err != nil {
				t.Fatalf("planting the link: %v", err)
			}

			if err := test.write(workspace); err == nil {
				t.Errorf("writing into %s followed a link out of the workspace", replaced)
			} else if !strings.Contains(err.Error(), replaced) {
				t.Errorf("refusal = %q, want it to name %s", err, replaced)
			}

			entries, err := os.ReadDir(elsewhere)
			if err != nil {
				t.Fatalf("reading the link target: %v", err)
			}
			for _, entry := range entries {
				t.Errorf("the link target holds %q, and the write should have created nothing there", entry.Name())
			}
		})
	}
}

// TestAnOutboxThatIsNotADirectoryIsReported checks the read side of the same
// rule: a workspace whose outbox has been replaced is a problem to report, not
// a directory to follow.
func TestAnOutboxThatIsNotADirectoryIsReported(t *testing.T) {
	workspace, _ := newWorkspace(t)
	elsewhere := t.TempDir()
	if err := os.WriteFile(filepath.Join(elsewhere, "planted.json"),
		[]byte(envelope("planted", testTask, control.TypeProviderEvent)), 0o600); err != nil {
		t.Fatalf("writing the planted message: %v", err)
	}
	if err := os.RemoveAll(workspace.OutboxDir()); err != nil {
		t.Fatalf("removing the outbox: %v", err)
	}
	if err := os.Symlink(elsewhere, workspace.OutboxDir()); err != nil {
		t.Fatalf("planting the link: %v", err)
	}

	messages, _, err := workspace.Pending()
	if len(messages) != 0 {
		t.Errorf("%d messages were read through a link standing in for the outbox", len(messages))
	}
	if err == nil {
		t.Fatal("an outbox that is a symbolic link was read as a directory")
	}
	if !strings.Contains(err.Error(), testTask) {
		t.Errorf("failure = %q, want it to name the task %s", err, testTask)
	}
}
