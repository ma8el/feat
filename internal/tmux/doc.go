// Package tmux is the tmux execution adapter.
//
// tmux is a required execution backend in v0. Feat drives a dedicated named
// server, so a managed session cannot collide with one the user started.
//
// The default topology is one Feat-owned server, one session per project, one
// window per task, one tagged pane for the native agent, and an optional tagged
// task shell.
//
// Rules this package must enforce:
//
//   - tmux loads the user's normal configuration and keybindings where they are
//     compatible, and Feat then applies its own metadata;
//   - sessions, windows, and panes carry stable project and task IDs in tmux
//     user options, and a numeric index or display name is never identity;
//   - commands are argument vectors, not interpolated shell strings;
//   - a pane's process may be inspected, but semantic completion is never
//     inferred from its terminal text;
//   - a daemon restart rediscovers the tagged sessions and windows that
//     outlived it rather than creating new ones.
//
// The package also captures pane content, so the dashboard can draw a task's
// terminal without the user attaching to it.
//
// Execution-environment adapters supply this adapter with a final command
// vector and working directory. tmux owns terminal persistence and attachment;
// it does not implement or import the execution-environment interface
// (ADR-030).
package tmux
