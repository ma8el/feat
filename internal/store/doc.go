// Package store defines the persistence interfaces for projects, tasks,
// events, and review state.
//
// Dependency rule: store defines interfaces over internal/domain and nothing
// else. It must not import the daemon, the API, the UI, or any adapter. The
// interfaces must not leak implementation types, so that the file-backed
// implementation can be replaced (docs/10-decisions-and-open-questions.md
// records no SQLite in v0, but the boundary must permit it later).
//
// The daemon is the only writer of persistent state.
package store
