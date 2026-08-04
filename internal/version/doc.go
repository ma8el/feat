// Package version reports build information for the feat binary.
//
// It is not listed in docs/06-technical-architecture.md. It exists because the
// version command, the health screen, and later `feat doctor` all need the same
// build identity, and none of them should depend on each other for it.
//
// This package must remain a leaf: it imports only the standard library.
package version
