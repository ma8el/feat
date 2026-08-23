// Package review decides whether an expanded review command may run, and runs
// a project's configured checks as a completion gate.
//
// It receives final values and reads neither configuration nor persistent
// state, under the rule ADR-029 established for Git: the daemon expands the
// templates, because the placeholder vocabulary belongs to internal/config,
// which validates it. A `review-stays-a-policy` depguard rule makes that
// mechanical.
//
// The two things it owns are the two that are worth having in one place:
//
//   - an expanded command may run only in one of its own task's recorded
//     worktrees, and only when nothing in it was left unexpanded. That rule is
//     checked against one list rather than spread across the three commands
//     that happen to exist today;
//   - a gate's results are attributed to the provider because the gate ran the
//     command itself, and a check that could not be started or that exceeded
//     its bound is inconclusive rather than failed. A task never reaches
//     ready_for_review on the strength of a check nobody managed to run.
//
// What it does not own: the change summaries, which internal/git computes
// because they are Git's own answers about a worktree; the decision to approve,
// which is a state change and therefore the daemon's; and the running of the
// external commands, which the client does because they take over the caller's
// terminal.
//
// The TUI does not render source diffs in v0.
//
// See ADR-036.
package review
