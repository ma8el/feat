// Package resources observes host and per-task resource usage.
//
// v0 samples whole-machine CPU, available memory, and disk availability, plus
// Feat-managed process and container CPU and memory aggregated per task.
//
// Rules this package must enforce:
//
//   - sampling is observational. Feat does not schedule, throttle, or reject
//     task creation based on capacity in v0, and metrics never block a task;
//   - collection failure degrades gracefully rather than failing a command;
//   - samples are not persisted continuously.
//
// Delivered by slice 10.
package resources
