// Package github opens pull requests on GitHub through the gh CLI.
//
// It is the second forge adapter, in the order the roadmap records. GitLab is
// what the reference project's application repositories use; GitHub is the
// primary public integration, and the forge Feat's own repository is on
// (docs/09-roadmap.md Phase 3, ADR-070).
//
// Everything GitHub-specific lives here — the executable, its flags, and the
// shape of what it prints — so internal/forge stays a description of what
// publishing is and the daemon never learns that one forge says pull request
// where the other says merge request.
//
// The CLI is run already authenticated, on the trusted host, with the user's own
// environment. This package passes no token, reads no configuration, and writes
// nothing down.
package github
