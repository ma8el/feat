package control

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ma8el/feat/internal/domain"
)

// TestReadingAMessageRefusesWhatTheListingCouldNotProve covers the window
// between the directory listing and the read.
//
// checkEntry proves that an entry is a plain, regular file within the size
// limit, and it proves it about the snapshot os.ReadDir returned. The agent
// owns that directory: by the time the file is opened it can be a link to
// somewhere else on the machine, a named pipe with no writer, or a document
// that has grown. The read is therefore tested directly, because the race it
// closes cannot be arranged reliably through the public entry point.
func TestReadingAMessageRefusesWhatTheListingCouldNotProve(t *testing.T) {
	workspace := internalWorkspace(t)
	entry := func(name string) string { return filepath.Join(workspace.OutboxDir(), name) }

	t.Run("a link to a file outside the workspace", func(t *testing.T) {
		outside := filepath.Join(t.TempDir(), "elsewhere.json")
		if err := os.WriteFile(outside, []byte(`{"schema_version":1}`), 0o600); err != nil {
			t.Fatalf("writing the file outside the workspace: %v", err)
		}
		if err := os.Symlink(outside, entry("link.json")); err != nil {
			t.Fatalf("planting the link: %v", err)
		}

		data, err := workspace.readMessage("link.json")
		if err == nil {
			t.Fatalf("a link was read as a message: %q", data)
		}
		var rejection *RejectionError
		if !errors.As(err, &rejection) {
			t.Fatalf("refusal %v is not a rejection; a file the agent got wrong is an ordinary event", err)
		}
		if !strings.Contains(rejection.Reason, "regular file") {
			t.Errorf("reason = %q, want it to name the file kind", rejection.Reason)
		}
	})

	// The worst of the three: one goroutine polls every task, so an open that
	// blocks in the kernel stops control processing daemon-wide and hangs the
	// poller's own shutdown.
	t.Run("a named pipe with no writer", func(t *testing.T) {
		if err := syscall.Mkfifo(entry("pipe.json"), 0o600); err != nil {
			t.Fatalf("creating the named pipe: %v", err)
		}

		done := make(chan error, 1)
		go func() {
			_, err := workspace.readMessage("pipe.json")
			done <- err
		}()

		select {
		case err := <-done:
			if err == nil {
				t.Fatal("a named pipe was read as a message")
			}
			if !strings.Contains(err.Error(), "regular file") {
				t.Errorf("refusal = %q, want it to name the file kind", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("reading a named pipe blocked; every task's control messages would stop with it")
		}
	})

	t.Run("a document larger than the limit", func(t *testing.T) {
		if err := os.WriteFile(entry("grown.json"),
			[]byte(strings.Repeat("x", MaxMessageBytes+1)), 0o600); err != nil {
			t.Fatalf("writing the oversize document: %v", err)
		}

		data, err := workspace.readMessage("grown.json")
		if err == nil {
			t.Fatalf("%d bytes were read, and the limit is %d", len(data), MaxMessageBytes)
		}
		if !strings.Contains(err.Error(), "limit") {
			t.Errorf("refusal = %q, want it to name the limit", err)
		}
	})

	t.Run("an ordinary message", func(t *testing.T) {
		const body = `{"schema_version":1,"id":"a"}`
		if err := os.WriteFile(entry("good.json"), []byte(body), 0o600); err != nil {
			t.Fatalf("writing the message: %v", err)
		}

		data, err := workspace.readMessage("good.json")
		if err != nil {
			t.Fatalf("reading a well-formed message: %v", err)
		}
		if string(data) != body {
			t.Errorf("read %q, want %q", data, body)
		}
	})
}

// internalWorkspace is a created workspace for a test that needs the unexported
// half of the package.
func internalWorkspace(t *testing.T) *Workspace {
	t.Helper()

	workspace, err := Open(t.TempDir(), "app", domain.TaskID("3f2504e0-4f89-41d3-9a0c-0305e82c3301"), Options{})
	if err != nil {
		t.Fatalf("opening a workspace: %v", err)
	}
	if err := workspace.Create(); err != nil {
		t.Fatalf("creating the workspace: %v", err)
	}
	return workspace
}
