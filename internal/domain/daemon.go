package domain

import "time"

// StateSchemaVersion is the version of the state directory as a whole.
//
// Individual documents carry their own version and their own migrations. This
// one is about the directory: it is what lets a build refuse a state directory
// written by a newer Feat, rather than reading each document, finding a version
// it does not know, and stopping half way through with some records migrated
// and some not.
const StateSchemaVersion = 1

// DaemonRecord is what one installation's state directory remembers about the
// daemons that have owned it.
//
// It is durable, and therefore deliberately holds nothing that must not outlive
// the machine. A process identifier, a socket path, and a lock all belong to the
// runtime directory, which does not survive a reboot; a process identifier
// recorded here would be reused after one and would then describe a daemon that
// is not running (ADR-027 evidence 1, ADR-037).
//
// What it does hold has three readers, and each of them is a question a
// reconciliation pass has to answer:
//
//   - which schema this directory was written with, so a build older than the
//     directory refuses it instead of overwriting documents it cannot read;
//   - whether the last run ended cleanly, so a report can say a daemon crashed
//     rather than leaving a user to infer it from an interrupted gate;
//   - when it stopped, which is how long Feat was not looking.
type DaemonRecord struct {
	// StateSchema is the version of the state directory this record describes.
	StateSchema int
	// StartedAt is when the daemon that owns the record started.
	StartedAt time.Time
	// StoppedAt is when the previous daemon stopped, zero when it never
	// recorded a stop — which is what a crash leaves behind.
	StoppedAt time.Time
	// EndedCleanly reports whether the previous daemon stopped rather than
	// died. It is written false at startup and true at shutdown, so the absence
	// of a clean stop is recorded by the run that follows it rather than by the
	// one that failed to write anything.
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

// Newer reports whether the recorded state directory is from a build this one
// cannot safely write to.
//
// Refusing is the conservative direction: an older daemon that wrote over a
// newer directory would lose whatever the newer schema added, and unlike almost
// everything else in Feat that loss is silent.
func (d *DaemonRecord) Newer() bool { return d.StateSchema > StateSchemaVersion }
