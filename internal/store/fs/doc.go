// Package fs implements the store interfaces over the file layout in
// docs/06-technical-architecture.md: versioned JSON snapshots, JSONL event
// history, and Markdown task briefs.
//
// The package name shadows the standard library's io/fs; import that as iofs
// where both are needed.
//
// Rules this package must enforce:
//
//   - every schema carries a version;
//   - snapshots are written to a temporary file, fsynced where appropriate, and
//     atomically renamed, so a crash never leaves a partially replaced snapshot
//     as current;
//   - writes are serialized;
//   - events append in order, and recovery ignores only an incomplete final
//     JSONL record;
//   - derived resource samples are not persisted continuously.
//
// Dependency rule: storage code contains no daemon or TUI dependencies.
//
// Delivered by slice 1.
package fs
