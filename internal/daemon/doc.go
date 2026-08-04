// Package daemon owns the background process lifecycle, orchestration
// services, reconciliation, and event publication.
//
// The daemon is the only reader and writer of persistent state. The TUI and CLI
// are clients that reach it over the Unix-domain socket, and internal/api is the
// transport it serves them through.
//
// Rules this package must enforce:
//
//   - the socket is user-owned and mode-restricted, and no TCP listener is
//     opened;
//   - a stale socket or endpoint record is diagnosed and safely recovered rather
//     than silently overwritten;
//   - reconciliation is explicit: persisted desired state is never assumed to
//     equal observed state;
//   - stopped application containers are reported, not restarted;
//   - a failure halfway through a lifecycle leaves a recoverable, explainable
//     record.
//
// Ownership of the runtime directory is an advisory file lock plus a connect
// probe, described on Acquire, and it requires a Unix platform: see
// platform_unix.go and ADR-027. Feat targets macOS and Linux.
package daemon
