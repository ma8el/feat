// Package git is the Git and worktree adapter.
//
// It invokes the Git CLI as an argument vector, never as an interpolated shell
// string.
//
// Responsibilities:
//
//   - validate repositories and remotes;
//   - fetch without mutating the user's ordinary checkout;
//   - resolve a base policy to an immutable commit and record the commit, not
//     the ref name;
//   - generate branch and worktree names and detect collisions;
//   - create read-write and read-only task worktrees;
//   - observe dirty, ahead, behind, and merged state;
//   - compute change summaries against each repository's recorded base;
//   - produce exact cleanup plans and remove worktrees or branches only after
//     confirmation.
//
// Rules this package must enforce:
//
//   - a dirty ordinary checkout is preserved and never blocks an independent
//     task;
//   - unsafe or broad paths are rejected before any destructive operation;
//   - failure halfway through creation leaves a recoverable record and no
//     unidentified worktree.
//
// Delivered by slice 4.
package git
