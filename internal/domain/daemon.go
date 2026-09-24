package domain

import "time"

// StateSchemaVersion versions the state directory as a whole, separately from
// the per-document versions. A build reads it once and refuses a directory it
// cannot read, rather than stopping half way through migrating documents.
const StateSchemaVersion = 1

// DaemonRecord is the durable record of the daemons that have owned one state
// directory. It holds nothing machine-bound: process identifiers, socket paths,
// and locks belong to the runtime directory instead (ADR-027, ADR-037).
type DaemonRecord struct {
	// StateSchema is the version of the state directory this record describes.
	StateSchema int
	// StartedAt is when the daemon that owns the record started.
	StartedAt time.Time
	// StoppedAt is when the previous daemon stopped, zero when it never
	// recorded a stop — which is what a crash leaves behind.
	StoppedAt time.Time
	// EndedCleanly reports whether the previous daemon stopped rather than
	// died. Startup writes false and shutdown writes true, so a crash is
	// recorded by the run that follows it rather than by the one that died.
	EndedCleanly bool
	// Version is the build that last wrote the record, so a report can name the
	// Feat that produced the state it is reading.
	Version string
}

// Validate reports whether the record is internally consistent.
func (d *DaemonRecord) Validate() error {
	if d.StateSchema < 1 {
		return &ValidationError{Entity: "daemon record", Field: "state_schema",
			Reason: "must be at least 1, but is " + formatInt(d.StateSchema)}
	}
	if d.StartedAt.IsZero() {
		return &ValidationError{Entity: "daemon record", Field: "started_at", Reason: "must be set"}
	}
	if !d.StoppedAt.IsZero() && d.StoppedAt.Before(d.StartedAt) {
		return &ValidationError{Entity: "daemon record", Field: "stopped_at",
			Reason: "must not precede started_at"}
	}
	if d.EndedCleanly && d.StoppedAt.IsZero() {
		return &InvariantError{Entity: "daemon record",
			Rule:   "a run that ended cleanly recorded when it stopped",
			Reason: "the record claims a clean end and names no stop time"}
	}
	return nil
}

// Newer reports whether the state directory was written by a build this one
// cannot safely write to. An older daemon writing over a newer directory would
// silently lose whatever the newer schema added.
func (d *DaemonRecord) Newer() bool { return d.StateSchema > StateSchemaVersion }
