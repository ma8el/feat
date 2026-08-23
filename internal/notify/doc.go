// Package notify holds the notification policy and its platform adapters.
//
// The policy is domain-driven and the delivery is platform-specific: macOS
// desktop notifications in v0.1, Linux where a standard notifier exists in
// v0.2, plus TUI badges the dashboard renders from task state.
//
// Notifiable conditions:
//
//   - idle, after the configured grace period and only when not attached;
//   - review requested or ready;
//   - verification failed;
//   - session or runtime failure.
//
// Rules this package must enforce:
//
//   - idle notifications never fire immediately, and never while the user is
//     attached to that task;
//   - notification text identifies the task without exposing secrets or task
//     content that should stay local.
//
// The second rule is a property of what this package can reach rather than a
// filter over what it writes: Compose is given a task's key, title, and project
// and has no way to name a brief, an agent's words, a path, a command, or a
// configuration value.
//
// See ADR-035.
package notify
