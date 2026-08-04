package domain

import (
	"crypto/rand"
	"encoding/hex"
	"regexp"
)

// safeIDPattern is the documented safe pattern for the identifiers a user
// chooses in project configuration (docs/07-configuration-model.md validation
// rules).
//
// These identifiers reach file paths, generated branch names, tmux object
// metadata, and Compose project names, so the pattern is the intersection of
// what those accept rather than merely what a filesystem tolerates. It excludes
// "." and "/" so that no identifier can ever escape a directory it names.
var safeIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// taskIDPattern matches a version 4 UUID in canonical lowercase form.
var taskIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// ProjectID identifies a locally registered project. It is chosen by the user.
type ProjectID string

// Validate reports whether the project identifier is safe to use in paths,
// branch names, and Compose project names.
func (id ProjectID) Validate() error {
	return validateSafeID("project", string(id), "id", string(id))
}

// String returns the identifier as a plain string.
func (id ProjectID) String() string { return string(id) }

// RepositoryID identifies a repository within one project. It is chosen by the
// user and is unique per project rather than globally.
type RepositoryID string

// Validate reports whether the repository identifier is safe to use in paths,
// branch names, and Compose project names.
func (id RepositoryID) Validate() error {
	return validateSafeID("repository", string(id), "id", string(id))
}

// String returns the identifier as a plain string.
func (id RepositoryID) String() string { return string(id) }

// TaskID is the stable identity of a task: a version 4 UUID in canonical
// lowercase form. Feat generates it, so it is never a user-supplied string.
type TaskID string

// TaskKey is the human-facing short identifier of a task. It appears in branch
// names, worktree paths, and the dashboard.
type TaskKey string

// String returns the key as a plain string.
func (k TaskKey) String() string { return string(k) }

// NewTaskID returns a new random task identifier.
//
// It panics only if the operating system's randomness source fails, which the
// standard library already treats as unrecoverable.
func NewTaskID() TaskID {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("domain: reading random bytes for a task id: " + err.Error())
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant

	encoded := hex.EncodeToString(b[:])
	return TaskID(encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" +
		encoded[16:20] + "-" + encoded[20:32])
}

// Validate reports whether the task identifier has the generated form.
func (id TaskID) Validate() error {
	if !taskIDPattern.MatchString(string(id)) {
		return &ValidationError{
			Entity: "task",
			ID:     string(id),
			Field:  "id",
			Reason: "must be a version 4 UUID in canonical lowercase form",
		}
	}
	return nil
}

// Key returns the human-facing short identifier of the task.
//
// The key is derived from the identifier rather than stored beside it, so the
// two can never disagree. Callers that need keys to be unique within a project
// resolve a collision by generating another task identifier.
func (id TaskID) Key() TaskKey { return TaskKey(id[:8]) }

// String returns the identifier as a plain string.
func (id TaskID) String() string { return string(id) }

// validateSafeID checks one user-chosen identifier against the safe pattern.
func validateSafeID(entity, id, field, value string) error {
	if value == "" {
		return &ValidationError{Entity: entity, ID: id, Field: field, Reason: "must not be empty"}
	}
	if !safeIDPattern.MatchString(value) {
		return &ValidationError{
			Entity: entity,
			ID:     id,
			Field:  field,
			Reason: "must start with a lowercase letter or digit and contain only lowercase letters, digits, \"-\", and \"_\" (at most 64 characters)",
		}
	}
	return nil
}
