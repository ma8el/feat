package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	iofs "io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/ma8el/feat/internal/client"
	"github.com/ma8el/feat/internal/paths"
)

// endpointSchemaVersion is the version of the endpoint record. It follows the
// state schema policy in docs/07-configuration-model.md even though the record
// is ephemeral: a build that finds a record it does not understand should be
// able to say so rather than misread it.
const endpointSchemaVersion = 1

// File modes for the runtime directory. The socket, the lock, and the record
// name a user's own daemon, and only that user may reach them.
const (
	runtimeDirPerm  os.FileMode = 0o700
	runtimeFilePerm os.FileMode = 0o600
)

// Endpoint is the record a running daemon publishes about itself. It lives in
// the runtime directory rather than the state directory, because a process
// identifier that outlives the system's uptime can be reused by an unrelated
// process (ADR-027).
type Endpoint struct {
	// SchemaVersion is the version of this record.
	SchemaVersion int `json:"schema_version"`
	// PID is the daemon's process identifier.
	PID int `json:"pid"`
	// Socket is the path the daemon listens on.
	Socket string `json:"socket"`
	// Version is the build version of the running daemon, so that a client from
	// a newer build can report the difference instead of behaving oddly.
	Version string `json:"version"`
	// Commit is the build commit.
	Commit string `json:"commit"`
	// StartedAt is when the daemon acquired ownership.
	StartedAt time.Time `json:"started_at"`
}

// ReadEndpoint returns the record the running daemon published. A missing record
// produces ErrNotRunning, because from a caller's point of view there is nothing
// to talk to.
func ReadEndpoint(layout paths.Layout) (Endpoint, error) {
	path := layout.EndpointFile()

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, iofs.ErrNotExist) {
			return Endpoint{}, ErrNotRunning
		}
		return Endpoint{}, fmt.Errorf("reading the daemon endpoint %s: %w", path, err)
	}

	var endpoint Endpoint
	if err := json.Unmarshal(data, &endpoint); err != nil {
		return Endpoint{}, fmt.Errorf("the daemon endpoint %s is not readable JSON: %w", path, err)
	}
	if endpoint.SchemaVersion != endpointSchemaVersion {
		return Endpoint{}, fmt.Errorf(
			"the daemon endpoint %s has schema version %d, and this build understands %d",
			path, endpoint.SchemaVersion, endpointSchemaVersion)
	}
	return endpoint, nil
}

// askEndpoint asks a running daemon to describe itself. It is the better of the
// two ways to learn what ReadEndpoint reads: macOS's temporary-directory cleaner
// collects the record out from under a daemon that has been up for three days,
// and a daemon cannot go missing while it is answering (ADR-101).
//
// api.Daemon already carries every field the record holds.
func askEndpoint(ctx context.Context, layout paths.Layout) (Endpoint, error) {
	if !Answering(layout.Socket) {
		return Endpoint{}, ErrNotRunning
	}

	daemon := client.New(layout.Socket)
	defer daemon.Close()

	health, err := daemon.Health(ctx)
	if err != nil {
		return Endpoint{}, fmt.Errorf("asking the daemon on %s to identify itself: %w", layout.Socket, err)
	}
	if health.Daemon.PID <= 0 {
		// Refused by name rather than signalled: kill(2) reads a non-positive
		// identifier as a process group or as every process this user owns.
		return Endpoint{}, fmt.Errorf("the daemon on %s reported process identifier %d, which is not a process",
			layout.Socket, health.Daemon.PID)
	}

	return Endpoint{
		SchemaVersion: endpointSchemaVersion,
		PID:           health.Daemon.PID,
		Socket:        health.Daemon.Socket,
		Version:       health.Daemon.Version,
		Commit:        health.Daemon.Commit,
		StartedAt:     health.Daemon.StartedAt,
	}, nil
}

// writeEndpoint publishes the record by atomic replacement, so a client never
// reads a half-written one.
func writeEndpoint(path string, endpoint Endpoint) error {
	data, err := json.MarshalIndent(endpoint, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding the daemon endpoint: %w", err)
	}
	return replaceFile(path, append(data, '\n'), runtimeFilePerm)
}

// replaceFile writes data to a temporary file in the same directory and renames
// it over the target. The state store has a more careful version, with directory
// syncs, because a lost snapshot loses a user's work; this record is rebuilt on
// every start, so the atomic rename is enough.
func replaceFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)

	temp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating a temporary file in %s: %w", dir, err)
	}
	name := temp.Name()

	if err := temp.Chmod(perm); err != nil {
		return discard(temp, fmt.Errorf("setting permissions on %s: %w", name, err))
	}
	if _, err := temp.Write(data); err != nil {
		return discard(temp, fmt.Errorf("writing %s: %w", name, err))
	}
	if err := temp.Sync(); err != nil {
		return discard(temp, fmt.Errorf("flushing %s: %w", name, err))
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("closing %s: %w", name, err)
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	return nil
}

// discard removes a temporary file after a failed write and returns the original
// failure, which is the one worth reporting.
func discard(file *os.File, cause error) error {
	_ = file.Close()
	_ = os.Remove(file.Name())
	return cause
}
