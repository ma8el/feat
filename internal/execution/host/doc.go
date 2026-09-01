// Package host documents host-native execution, in which the coding agent runs
// directly in the task's primary worktree.
//
// It holds no environment implementation, and that is what the mode turned out
// to be rather than work outstanding: host execution is the absence of a
// container, so the daemon dispatches a project configured with `mode: host`
// straight to the agent launch instead of building an environment behind
// internal/execution's interface first (ADR-090).
//
// This mode offers convenience and no container security boundary. Diagnostics
// and documentation must say so plainly rather than implying isolation.
//
// Host-native and devcontainer execution share one task domain; selecting a
// mode must not change task semantics.
package host
