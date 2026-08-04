// Package daemon owns the background process lifecycle, orchestration
// services, reconciliation, and event publication.
//
// The daemon is the only writer of persistent state. The TUI and CLI are
// clients that reach it over the Unix-domain socket.
//
// Rules this package must enforce:
//
//   - the socket is user-owned and mode-restricted, and no TCP listener is
//     opened;
//   - a stale socket or PID file is diagnosed and safely recovered rather than
//     silently overwritten;
//   - reconciliation is explicit: persisted desired state is never assumed to
//     equal observed state;
//   - stopped application containers are reported, not restarted;
//   - a failure halfway through a lifecycle leaves a recoverable, explainable
//     record.
//
// Delivered by slice 2.
package daemon
