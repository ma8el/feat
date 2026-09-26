// Package cli builds the feat command tree and maps command results onto
// process exit codes.
//
// docs/06-technical-architecture.md places command wiring in cmd/feat. It lives
// here instead so a test can build the tree without spawning a process, which
// leaves cmd/feat with signal handling and the exit call.
//
// Commands in this package are clients. They must not read or write persistent
// state directly: the daemon is the only writer (CLAUDE.md architectural rules).
// A command that does not do its work yet is registered all the same and returns
// a NotImplementedError naming what is missing, so `feat --help` describes the
// real v0 command surface without a subcommand pretending to work.
package cli
