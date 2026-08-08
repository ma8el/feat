// Package reconcile holds the vocabulary of startup discovery and the policy
// that decides what a cleanup is allowed to remove.
//
// It receives final values and reads neither configuration nor persistent
// state, under the rule ADR-029 established for Git and ADR-036 for review: the
// daemon observes the world through the adapters, and this package decides what
// those observations permit. A `reconcile-stays-a-policy` depguard rule makes
// that mechanical.
//
// The three things it owns are the three worth having in one place:
//
//   - what a finding is. Missing, orphaned, inconsistent, and damaged resources
//     are reported and never repaired, adopted, or removed, which is
//     FR-STATE-004 generalised from containers to every resource class;
//   - which classes a cleanup separates, and in which order they may be
//     removed. A class is an independent choice (FR-CLEAN-002) and the order is
//     a requirement rather than a detail: what holds a file is stopped before
//     the file is removed;
//   - whether a selection may proceed. The plan token names what would be
//     removed and never the warnings, so a plan does not expire because the
//     agent wrote a file; the warnings are re-observed at execution and must be
//     covered by what the user confirmed. A stale confirmation is the failure
//     this rule exists to prevent (FR-CLEAN-003, ADR-037).
//
// What it does not own: the observing, which is the adapters'; the removing,
// which is the adapters' too; and the decision to archive a task, which is a
// state change and therefore the daemon's.
//
// Volumes are retained unless a selection names their class, and a target whose
// identity is a broad or unresolvable path is refused rather than removed
// (FR-CLEAN-004, docs/05-security-model.md).
//
// Delivered by slice 12. See ADR-037.
package reconcile
