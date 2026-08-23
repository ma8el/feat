// Package guard holds repository-wide invariant tests.
//
// It is not listed in docs/06-technical-architecture.md and contains no runtime
// code: every file except this one is a test. It exists so the rules in
// CLAUDE.md that a linter cannot express are checked by `go test ./...` rather
// than by review attention.
//
// Current guards:
//
//   - no reference-project repository name, path, Compose service, or database
//     identifier is compiled into the binary (CLAUDE.md scope rule 3);
//   - Git, tmux, and Docker Compose are invoked as argument vectors rather than
//     through an interpolated shell (CLAUDE.md architectural rules).
//
// Import boundaries are enforced separately by depguard in .golangci.yml.
package guard
