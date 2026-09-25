// Package git is the Git and worktree adapter.
//
// It works on domain types and final names: the daemon expands templates,
// because the placeholder vocabulary belongs to configuration, and a
// `git-stays-an-adapter` depguard rule keeps it that way.
//
// Task preparation is two steps with the record between them:
//
//   - Plan resolves every base policy to an immutable commit, proposes every
//     branch and worktree path, and reports every collision. It creates
//     nothing. The only change it makes is to the remote-tracking refs a fetch
//     updates, which are never the user's working tree, index, or checked-out
//     branch.
//   - Apply creates the worktrees and branches, one repository at a time,
//     journalling each before the next begins.
//
// The caller records the plan between the two, so every path and branch that
// could exist afterwards is written down first. Nothing is undone when a launch
// fails half way through, because a worktree that exists may already have been
// written to.
//
// Two reference points are used deliberately and are not interchangeable. What
// a task did is measured against its recorded base commit, which never moves.
// Where the world went is measured against the base ref as it stands now.
//
// Removing is here, in remove.go; deciding what may be removed is not.
// internal/reconcile holds that policy, and a request arriving here is
// re-checked against the rules below immediately before it is carried out,
// because a plan resolved minutes ago may have been edited since.
//
// Rules this package enforces:
//
//   - a dirty ordinary checkout is preserved and never blocks an independent
//     task;
//   - a worktree path must be absolute, clean, strictly inside the root Feat
//     owns after symbolic links are resolved, outside every repository
//     checkout, and never a shared system directory;
//   - a configured name that Git would read as an option is refused rather than
//     passed;
//   - a collision is reported, never resolved by renaming.
//
// See ADR-029.
package git
