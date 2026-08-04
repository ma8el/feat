// Package notify holds the notification policy and its platform adapters.
//
// The policy is domain-driven and the delivery is platform-specific: macOS
// desktop notifications in v0.1, Linux where a standard notifier exists in
// v0.2, plus TUI badges.
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
// Delivered by slice 10.
package notify
