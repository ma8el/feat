package fs

import (
	"encoding/json"
	"errors"
	"fmt"
	iofs "io/fs"
	"os"
	"time"

	"github.com/ma8el/feat/internal/store"
)

// Schema versions of the stored documents. Every document records the version
// it was written with, so a later build can tell what it is reading before it
// reads it (docs/07-configuration-model.md).
const (
	projectSchemaVersion = 1
	taskSchemaVersion    = 1
	reviewSchemaVersion  = 1
	eventSchemaVersion   = 1
	daemonSchemaVersion  = 1
)

// migration upgrades a stored document from one schema version to the next.
//
// A migration works on the generic decoded document rather than on a struct,
// so that the code implementing an upgrade does not have to keep a copy of
// every historical Go type around to describe the version it starts from.
type migration struct {
	// from is the version this migration reads.
	from int
	// apply rewrites the document in place, leaving it at version from+1.
	apply func(document map[string]any) error
}

// codec reads and writes one kind of versioned document.
//
// v0.1 has one version of each document, so every migration list below is
// empty. The mechanism exists anyway: a migration that is designed after the
// first state directory exists in the wild is a migration designed too late.
type codec struct {
	// kind names the document in error messages, such as "task".
	kind string
	// version is the newest version this build writes.
	version int
	// migrations upgrade older documents, ordered from oldest to newest.
	migrations []migration
}

var (
	projectCodec = codec{kind: "project", version: projectSchemaVersion}
	taskCodec    = codec{kind: "task", version: taskSchemaVersion}
	reviewCodec  = codec{kind: "review", version: reviewSchemaVersion}
	eventCodec   = codec{kind: "event", version: eventSchemaVersion}
	daemonCodec  = codec{kind: "daemon record", version: daemonSchemaVersion}
)

// marshal renders a document, with a trailing newline so that the files stay
// readable in an editor and reviewable in a diff.
func (c codec) marshal(document any) ([]byte, error) {
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding %s: %w", c.kind, err)
	}
	return append(encoded, '\n'), nil
}

// unmarshal decodes a document, upgrading it first when it was written by an
// older build.
//
// Unknown fields are tolerated: a build that adds a field without changing the
// meaning of the existing ones stays readable by the build before it. Removing
// or reinterpreting a field is what requires a new version and a migration.
func (c codec) unmarshal(id, path string, raw []byte, target any) error {
	version, err := c.versionOf(id, path, raw)
	if err != nil {
		return err
	}

	switch {
	case version > c.version:
		return &store.SchemaError{Kind: c.kind, Path: path, Found: version, Supported: c.version}
	case version < c.version:
		raw, err = c.migrate(id, path, raw, version)
		if err != nil {
			return err
		}
	}

	if err := json.Unmarshal(raw, target); err != nil {
		return &store.CorruptError{Kind: c.kind, ID: id, Path: path, Err: err}
	}
	return nil
}

// versionOf reads the schema version of a stored document.
func (c codec) versionOf(id, path string, raw []byte) (int, error) {
	var header struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return 0, &store.CorruptError{Kind: c.kind, ID: id, Path: path, Err: err}
	}
	if header.SchemaVersion < 1 {
		return 0, &store.CorruptError{
			Kind: c.kind,
			ID:   id,
			Path: path,
			Err:  errors.New("no schema_version, so the document cannot be interpreted safely"),
		}
	}
	return header.SchemaVersion, nil
}

// migrate upgrades a document to the current version.
func (c codec) migrate(id, path string, raw []byte, version int) ([]byte, error) {
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, &store.CorruptError{Kind: c.kind, ID: id, Path: path, Err: err}
	}

	for version < c.version {
		step, ok := c.migrationFrom(version)
		if !ok {
			return nil, &store.SchemaError{Kind: c.kind, Path: path, Found: version, Supported: c.version}
		}
		if err := step.apply(document); err != nil {
			return nil, &store.CorruptError{
				Kind: c.kind,
				ID:   id,
				Path: path,
				Err:  fmt.Errorf("migrating from schema version %d: %w", version, err),
			}
		}
		version++
		document["schema_version"] = version
	}

	upgraded, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("encoding the migrated %s: %w", c.kind, err)
	}
	return upgraded, nil
}

func (c codec) migrationFrom(version int) (migration, bool) {
	for _, candidate := range c.migrations {
		if candidate.from == version {
			return candidate, true
		}
	}
	return migration{}, false
}

// writeSnapshot replaces a document, retaining the previous one when it was
// written by an older build.
//
// Migrations are one-way, so the retained copy is the only way back to the
// state a downgrade could read. It is written before the replacement, because a
// backup written afterwards is a backup of the wrong thing.
func (s *Store) writeSnapshot(c codec, path string, document any) error {
	encoded, err := c.marshal(document)
	if err != nil {
		return err
	}
	if err := s.retainPrevious(c, path); err != nil {
		return err
	}
	return s.replaceFile(path, encoded)
}

// retainPrevious copies a document aside when this build is about to replace it
// with a newer schema version.
func (s *Store) retainPrevious(c codec, path string) error {
	raw, err := readFile(path)
	if errors.Is(err, iofs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	var header struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		// An unreadable document is not something to preserve silently; the
		// replacement itself is what repairs the state directory.
		return nil
	}
	if header.SchemaVersion < 1 || header.SchemaVersion >= c.version {
		return nil
	}
	return s.replaceFile(fmt.Sprintf("%s.v%d.bak", path, header.SchemaVersion), raw)
}

// readSnapshot loads and decodes one document.
func (s *Store) readSnapshot(c codec, kind, id, path string, target any) error {
	raw, err := readFile(path)
	if errors.Is(err, iofs.ErrNotExist) {
		return &store.NotFoundError{Kind: kind, ID: id}
	}
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	return c.unmarshal(id, path, raw, target)
}

// corrupt wraps a failure to turn a stored document into a valid domain object.
// Storage validates what it reads: a snapshot that no longer satisfies the
// domain's invariants is corrupt state, not a task to operate on.
func corrupt(kind, id, path string, err error) error {
	return &store.CorruptError{Kind: kind, ID: id, Path: path, Err: err}
}

// optionalTime renders a timestamp that may be unset.
func optionalTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	utc := value.UTC()
	return &utc
}

// timeValue reads a timestamp that may be unset.
func timeValue(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return value.UTC()
}

// listDir returns the entries of a directory, treating a missing directory as
// empty. A project with no tasks and a project whose task directory was never
// created are the same thing to a caller.
func listDir(path string) ([]os.DirEntry, error) {
	entries, err := os.ReadDir(path)
	if errors.Is(err, iofs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return entries, nil
}
