// Package resources observes host and per-task resource usage.
//
// v0 samples whole-machine load, available memory, and disk availability, plus
// Feat-managed process and container CPU and memory aggregated per task.
//
// Rules this package must enforce:
//
//   - sampling is observational. Feat does not schedule, throttle, or reject
//     task creation based on capacity in v0, and metrics never block a task;
//   - collection failure degrades gracefully rather than failing a command;
//   - samples are not persisted continuously;
//   - a figure that was not measured is absent rather than zero.
//
// It is an adapter under the rule ADR-029 established for Git: it receives
// final values — process identifiers, a label selector, a filesystem path — and
// reads neither configuration nor persistent state. It knows nothing about the
// agent's execution environment or the application runtime; both are containers
// to it, which is why a `resources-stays-an-adapter` rule denies it either
// package.
//
// See ADR-035.
package resources
