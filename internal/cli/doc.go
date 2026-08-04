// Package cli builds the feat command tree and maps command results onto
// process exit codes.
//
// docs/06-technical-architecture.md places command wiring in cmd/feat. It lives
// here instead so the tree can be constructed in tests without spawning a
// process; cmd/feat is reduced to signal handling and the exit call.
//
// Commands in this package are clients. They must not read or write persistent
// state directly: the daemon is the only writer (CLAUDE.md architectural rules).
// Every command that a later slice will implement is registered now with a
// NotImplementedError naming its owning slice, so that `feat --help` describes
// the real v0 command surface without any subcommand pretending to work.
package cli
