// Package review computes per-repository change summaries and expands the
// user's configured external diff, editor, and status commands.
//
// A task-level review aggregates repository-level comparisons, each against
// that repository's own immutable recorded base commit.
//
// Rules this package must enforce:
//
//   - command templates receive structured variables such as repository path,
//     base commit, task ID, and branch, and are expanded into argument vectors
//     or an explicitly configured shell command with clear trust semantics;
//   - expanded commands cannot escape configured task paths through
//     unvalidated placeholders;
//   - approval never stops or destroys a runtime automatically; it may offer
//     to;
//   - review state survives a daemon restart.
//
// The TUI does not render source diffs in v0.
//
// Delivered by slice 11.
package review
