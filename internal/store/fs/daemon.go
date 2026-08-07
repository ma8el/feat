package fs

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/ma8el/feat/internal/domain"
)

// daemonDocument is the stored form of the durable daemon record.
//
// It carries no process identifier, socket path, or lock, and it never will:
// those live in the runtime directory, which does not survive a reboot, and a
// durable copy of one would describe a daemon that is not running (ADR-027).
type daemonDocument struct {
	SchemaVersion int       `json:"schema_version"`
	UpdatedAt     time.Time `json:"updated_at"`
	StateSchema   int       `json:"state_schema"`
	StartedAt     time.Time `json:"started_at"`
	StoppedAt     time.Time `json:"stopped_at,omitempty"`
	EndedCleanly  bool      `json:"ended_cleanly"`
	Version       string    `json:"version"`
}

type daemonStore struct{ store *Store }

// Save records the daemon state.
func (d daemonStore) Save(ctx context.Context, record *domain.DaemonRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if record == nil {
		return errors.New("saving the daemon record requires a record")
	}
	if err := record.Validate(); err != nil {
		return err
	}

	defer d.store.lock("daemon")()
	return d.store.writeSnapshot(daemonCodec, filepath.Join(d.store.root, daemonFile), daemonDocument{
		SchemaVersion: daemonSchemaVersion,
		UpdatedAt:     record.StartedAt.UTC(),
		StateSchema:   record.StateSchema,
		StartedAt:     record.StartedAt.UTC(),
		StoppedAt:     record.StoppedAt.UTC(),
		EndedCleanly:  record.EndedCleanly,
		Version:       record.Version,
	})
}

// Load returns the daemon state.
func (d daemonStore) Load(ctx context.Context) (*domain.DaemonRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path := filepath.Join(d.store.root, daemonFile)

	var document daemonDocument
	if err := d.store.readSnapshot(daemonCodec, "daemon record", "daemon", path, &document); err != nil {
		return nil, err
	}

	record := &domain.DaemonRecord{
		StateSchema:  document.StateSchema,
		StartedAt:    document.StartedAt.UTC(),
		StoppedAt:    document.StoppedAt.UTC(),
		EndedCleanly: document.EndedCleanly,
		Version:      document.Version,
	}
	// A record from a newer state schema is returned rather than rejected here.
	// Storage reports what it read; refusing to run against it is a decision,
	// and the daemon is where decisions are made.
	if record.StateSchema <= domain.StateSchemaVersion {
		if err := record.Validate(); err != nil {
			return nil, corrupt("daemon record", "daemon", path, err)
		}
	}
	return record, nil
}
