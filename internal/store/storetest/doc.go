// Package storetest provides deterministic fixtures for tests that need a
// project, a task, a review, or an event history.
//
// It is test support and is imported only by tests. It lives outside the test
// files of one package because storage, the daemon, the API, and the TUI all
// need the same canned state, and a fixture that each of them redefines is a
// fixture they will each define differently.
//
// Two properties are deliberate:
//
//   - Everything is deterministic. Identifiers and timestamps are constants, so
//     a snapshot written from a fixture is byte-for-byte reproducible and can be
//     compared against a golden file.
//   - Every field is populated somewhere. A round-trip through a persistence
//     layer therefore fails when a field is not persisted, which is what makes
//     the round-trip test a check on the mapping rather than on the fields
//     someone remembered to set.
//
// The fixtures are built through the domain's own constructors and transitions,
// so a fixture that no longer satisfies an invariant fails loudly here rather
// than producing state the domain would never have allowed.
package storetest
