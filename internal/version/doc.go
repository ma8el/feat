// Package version reports build information for the feat binary.
//
// It is not listed in docs/06-technical-architecture.md. It exists because the
// version command, the health screen, and later `feat doctor` all need the same
// build identity, and none of them should depend on each other for it.
//
// Identity has two sources. The Makefile links it in; where it did not — above
// all in a binary a tester installed with `go install ...@latest`, which never
// sees the Makefile at all — the build information the toolchain embedded
// answers instead, field by field.
//
// This package must remain a leaf: it imports only the standard library.
package version
