// Package tmux is the tmux execution adapter.
//
// tmux is a required execution backend in v0, not the product's source of
// truth. Feat drives a dedicated named tmux server so managed sessions cannot
// collide with the user's ordinary sessions.
//
// Default topology: one Feat-owned server, one session per project, one window
// per task, one tagged native-agent pane, and an optional tagged task shell.
// Their user-visible indexes are presentation, not identity.
//
// Rules this package must enforce:
//
//   - tmux loads the user's normal configuration and keybindings where
//     compatible; Feat then applies minimal metadata;
//   - sessions, windows, and panes are tagged with stable project and task IDs
//     using tmux user options;
//   - numeric indexes and display names are never used as identity;
//   - commands are argument vectors, not interpolated shell strings;
//   - process existence may be inspected, but semantic completion is never
//     inferred from terminal text;
//   - daemon startup rediscovers existing tagged sessions and windows.
//
// Execution-environment adapters supply this adapter with a final command
// vector and working directory. tmux owns terminal persistence and attachment;
// it does not implement or import the execution-environment interface
// (ADR-030).
//
// Delivered by slice 5.
package tmux
