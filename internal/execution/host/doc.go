// Package host runs the native coding agent directly in the task's primary
// worktree.
//
// This mode offers convenience and no container security boundary. Diagnostics
// and documentation must say so plainly rather than implying isolation.
//
// Host-native and devcontainer execution share one task domain; selecting a
// mode must not change task semantics.
//
// Delivered by slice 14 (public v0.2).
package host
