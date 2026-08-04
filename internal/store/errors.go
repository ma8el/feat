package store

import (
	"errors"
	"fmt"
)

// Error classes. Every typed error below unwraps to one of them, so a caller
// can match the class with errors.Is and reach for the details with errors.As.
var (
	// ErrNotFound marks a record that does not exist.
	ErrNotFound = errors.New("not found")
	// ErrCorrupt marks stored data that cannot be interpreted.
	ErrCorrupt = errors.New("corrupt stored state")
	// ErrUnsupportedSchema marks a record written by a version of Feat that
	// this binary does not understand.
	ErrUnsupportedSchema = errors.New("unsupported schema version")
)

// NotFoundError reports a record that does not exist.
type NotFoundError struct {
	// Kind names the record, such as "task".
	Kind string
	// ID identifies the record that was looked up.
	ID string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("%s %s does not exist", e.Kind, e.ID)
}

// Unwrap reports the error class.
func (e *NotFoundError) Unwrap() error { return ErrNotFound }

// CorruptError reports stored data that cannot be interpreted.
//
// It names the file, and the record within it where that is meaningful, because
// the user's recovery options are to repair or to discard the file, and neither
// is possible without knowing which one it is.
type CorruptError struct {
	// Kind names the record, such as "task".
	Kind string
	// ID identifies the record.
	ID string
	// Path is the file that holds it.
	Path string
	// Record is the 1-based line number within an event log, or zero for a
	// whole-file document.
	Record int
	// Err is the underlying decoding failure.
	Err error
}

func (e *CorruptError) Error() string {
	location := e.Path
	if e.Record > 0 {
		location = fmt.Sprintf("%s:%d", e.Path, e.Record)
	}
	return fmt.Sprintf("%s %s is stored incorrectly in %s: %v", e.Kind, e.ID, location, e.Err)
}

// Unwrap reports the underlying decoding failure.
func (e *CorruptError) Unwrap() error { return e.Err }

// Is reports the error class, so that errors.Is matches ErrCorrupt as well as
// the underlying failure.
func (e *CorruptError) Is(target error) bool { return target == ErrCorrupt }

// SchemaError reports a record whose schema version this binary cannot read.
//
// Migrations are one-way (docs/07-configuration-model.md), so a newer record is
// not something a caller can work around: the message has to say that the state
// directory belongs to a newer Feat.
type SchemaError struct {
	// Kind names the record, such as "task".
	Kind string
	// Path is the file that holds it.
	Path string
	// Found is the schema version stored in the file.
	Found int
	// Supported is the newest schema version this binary writes.
	Supported int
}

func (e *SchemaError) Error() string {
	if e.Found > e.Supported {
		return fmt.Sprintf(
			"%s in %s has schema version %d, but this build of feat supports at most %d: it was written by a newer version",
			e.Kind, e.Path, e.Found, e.Supported)
	}
	return fmt.Sprintf(
		"%s in %s has schema version %d, which this build of feat cannot migrate to %d",
		e.Kind, e.Path, e.Found, e.Supported)
}

// Unwrap reports the error class.
func (e *SchemaError) Unwrap() error { return ErrUnsupportedSchema }
