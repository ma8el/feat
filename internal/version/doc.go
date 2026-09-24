// Package version reports build information for the feat binary.
//
// It is not listed in docs/06-technical-architecture.md. It exists because the
// version command, the health screen, and later `feat doctor` all need the same
// build identity, and none of them should depend on each other for it.
//
// Identity has two sources. The Makefile links it in, and where it did not the
// toolchain's embedded build information answers instead, field by field. A
// binary installed with `go install ...@latest` never sees the Makefile.
//
// This package must remain a leaf: it imports only the standard library.
package version
