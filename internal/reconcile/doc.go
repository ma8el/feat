// Package reconcile performs startup discovery and produces repair proposals.
//
// Reconciliation is explicit: persisted desired state is never assumed to equal
// observed state. On startup Feat loads snapshots, validates schema versions,
// discovers tagged tmux objects, queries Git worktrees and branches, queries
// configured Compose projects, scans unprocessed control messages, compares
// desired against observed, updates observations, publishes recovery events,
// and offers actions for inconsistent resources.
//
// Rules this package must enforce:
//
//   - a daemon or computer restart loses no task identity;
//   - missing, orphaned, and inconsistent resources are reported before any
//     adoption or removal;
//   - stopped application containers are reported, never restarted;
//   - cleanup plans resolve exact task-owned resources, carry a stable
//     server-produced token, and separate containers and networks, volumes,
//     worktrees, and branches into independent choices;
//   - volumes are retained by default, dirty or unmerged work requires explicit
//     confirmation, and broad-path or non-task resource deletion is rejected;
//   - unrelated user checkouts, tmux sessions, containers, volumes, and
//     configuration are preserved.
//
// Delivered by slice 12.
package reconcile
