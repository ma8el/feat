package daemon

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ma8el/feat/internal/paths"
)

// TestAskEndpointDescribesADaemonWhoseRecordIsGone covers the second oracle.
//
// The record is a file the system can remove — macOS does, on the fourth day of
// a daemon's uptime — and a daemon that is answering can still say everything
// the record said (ADR-101).
func TestAskEndpointDescribesADaemonWhoseRecordIsGone(t *testing.T) {
	live := serve(t, Options{})

	if err := os.Remove(live.layout.EndpointFile()); err != nil {
		t.Fatalf("removing the endpoint record: %v", err)
	}
	if _, err := ReadEndpoint(live.layout); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("ReadEndpoint after removal = %v, want ErrNotRunning", err)
	}

	asked, err := askEndpoint(t.Context(), live.layout)
	if err != nil {
		t.Fatalf("askEndpoint: %v", err)
	}

	// Every field the record carries, from the daemon instead of from the file.
	if asked.PID != live.endpoint.PID {
		t.Errorf("pid = %d, want %d", asked.PID, live.endpoint.PID)
	}
	if asked.Socket != live.endpoint.Socket {
		t.Errorf("socket = %q, want %q", asked.Socket, live.endpoint.Socket)
	}
	if asked.Version != live.endpoint.Version || asked.Commit != live.endpoint.Commit {
		t.Errorf("build = %q/%q, want %q/%q",
			asked.Version, asked.Commit, live.endpoint.Version, live.endpoint.Commit)
	}
	if !asked.StartedAt.Equal(live.endpoint.StartedAt) {
		t.Errorf("started_at = %s, want %s", asked.StartedAt, live.endpoint.StartedAt)
	}
	if asked.SchemaVersion != endpointSchemaVersion {
		t.Errorf("schema version = %d, want %d", asked.SchemaVersion, endpointSchemaVersion)
	}
}

// TestAskEndpointReportsNotRunningWhenNothingAnswers keeps the fallback from
// inventing a daemon where there is none.
func TestAskEndpointReportsNotRunningWhenNothingAnswers(t *testing.T) {
	layout := testLayout(t)

	if _, err := askEndpoint(t.Context(), layout); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("askEndpoint with no daemon = %v, want ErrNotRunning", err)
	}
}

// TestStopReportsTheRecordsOwnFailureWhenNothingAnswers covers what the fallback
// must not do to the errors that were already correct.
//
// A record that cannot be read and a socket that does not answer is still a
// question about ownership, and `feat daemon restart` declines to spawn a second
// daemon over it (internal/cli/daemon.go). That depends on the failure the caller
// sees being the record's own, not the socket's.
func TestStopReportsTheRecordsOwnFailureWhenNothingAnswers(t *testing.T) {
	for _, test := range []struct {
		name    string
		written string
		want    string
	}{
		{
			name:    "no record at all",
			written: "",
			want:    ErrNotRunning.Error(),
		},
		{
			name:    "a record that is not JSON",
			written: "{not json",
			want:    "is not readable JSON",
		},
		{
			name:    "a record from a schema this build does not understand",
			written: `{"schema_version":99,"pid":1}`,
			want:    "schema version 99",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			layout := testLayout(t)
			if test.written != "" {
				if err := os.WriteFile(layout.EndpointFile(), []byte(test.written), runtimeFilePerm); err != nil {
					t.Fatalf("writing a record: %v", err)
				}
			}

			_, err := Stop(t.Context(), layout, time.Second)
			if err == nil {
				t.Fatal("Stop succeeded with no daemon running")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Errorf("Stop = %q, want it to report the record's own failure %q", err, test.want)
			}
		})
	}
}

// TestTheEndpointRecordComesBackAfterItIsRemoved is the regression for the
// record rotting out from under a running daemon.
//
// macOS reaps files under the runtime directory that have gone three days
// untouched, and the record was written once and never again. This asserts the
// mechanism in terms a test can observe in milliseconds rather than days: the
// record returns on its own (ADR-101).
func TestTheEndpointRecordComesBackAfterItIsRemoved(t *testing.T) {
	layout := testLayout(t)
	started := time.Date(2026, time.September, 14, 12, 0, 0, 0, time.UTC)

	ownership, err := Acquire(layout, testBuild, started, nil)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer func() { _ = ownership.Release() }()

	ownership.keepRecord(time.Millisecond)

	if err := os.Remove(layout.EndpointFile()); err != nil {
		t.Fatalf("removing the endpoint record: %v", err)
	}

	published, err := awaitRecord(t, layout)
	if err != nil {
		t.Fatalf("the endpoint record did not come back: %v", err)
	}

	// What comes back is the record, not an approximation of it: a daemon that
	// republished a different identifier would be worse than one that published
	// none.
	want := ownership.Endpoint()
	if published.PID != want.PID || published.Socket != want.Socket ||
		published.Version != want.Version || published.Commit != want.Commit ||
		published.SchemaVersion != want.SchemaVersion ||
		!published.StartedAt.Equal(want.StartedAt) {
		t.Errorf("republished record = %+v, want %+v", published, want)
	}
}

// TestReleaseStopsTheKeeperBeforeRemovingTheRecord covers the ordering that
// makes the keeper safe.
//
// A record written back after ownership was given up would name a process that
// is no longer running, on a system free to reuse its identifier — which is the
// failure ADR-027's evidence 1 exists to prevent, and a worse one than the record
// going missing.
func TestReleaseStopsTheKeeperBeforeRemovingTheRecord(t *testing.T) {
	layout := testLayout(t)

	ownership, err := Acquire(layout, testBuild, time.Now(), nil)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	ownership.keepRecord(time.Millisecond)

	// The keeper is running and writing before the release, so this is a release
	// that has to interrupt one rather than one that never started.
	if _, err := awaitRecord(t, layout); err != nil {
		t.Fatalf("the record was not being kept: %v", err)
	}

	if err := ownership.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}

	// Many keeper intervals, so a keeper that outlived Release would have had
	// every chance to write.
	deadline := time.Now().Add(100 * time.Millisecond)
	for time.Now().Before(deadline) {
		if _, err := os.Lstat(layout.EndpointFile()); err == nil {
			t.Fatal("the endpoint record was written back after ownership was released")
		}
		time.Sleep(time.Millisecond)
	}
}

// awaitRecord waits for a readable endpoint record to exist.
func awaitRecord(t *testing.T, layout paths.Layout) (Endpoint, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	var last error
	for {
		endpoint, err := ReadEndpoint(layout)
		if err == nil {
			return endpoint, nil
		}
		last = err

		select {
		case <-ctx.Done():
			return Endpoint{}, last
		case <-time.After(time.Millisecond):
		}
	}
}
