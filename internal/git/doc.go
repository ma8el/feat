// Package git is the Git and worktree adapter.
//
// It invokes the Git CLI as an argument vector, never as an interpolated shell
// string, and it works on domain types and final names: templates are expanded
// by the daemon, because the placeholder vocabulary belongs to configuration,
// and an adapter that had to read a YAML file to create a directory would be
// coupled to a format it has no opinion about.
//
// Task preparation is two steps with the record between them:
//
//   - Plan resolves every base policy to an immutable commit, proposes every
//     branch and worktree path, and reports every collision. It creates nothing.
//     The only change it makes is to the remote-tracking refs a fetch updates,
//     which is never the user's working tree, index, or checked-out branch.
//   - Apply creates the worktrees and branches, one repository at a time,
//     journalling each before the next begins.
//
// The caller records the plan between the two, which is what makes an
// interruption survivable: every path and branch that could exist afterwards is
// already written down, so at no point does a resource exist that the record
// cannot name. Nothing is undone when a launch fails half way through — a
// worktree that exists may already have been written to, and removing it to
// tidy up is a destructive act the user did not ask for.
//
// Responsibilities:
//
//   - validate repositories and remotes;
//   - fetch without mutating the user's ordinary checkout;
//   - resolve a base policy to an immutable commit and record the commit, not
//     the ref name;
//   - detect branch, path, and worktree collisions before anything is created;
//   - create read-write and read-only task worktrees;
//   - observe dirty, ahead, behind, and merged state;
//   - compute change summaries against each repository's recorded base;
//   - produce exact cleanup plans. Removing anything is slice 12's, and this
//     package has no code that does it.
//
// Two reference points are used deliberately: what a task did is measured
// against its recorded base commit, which never moves, and where the world went
// is measured against the base ref as it is now.
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
// Delivered by slice 4. See ADR-029 in
// docs/10-decisions-and-open-questions.md.
package git
