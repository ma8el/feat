// Package github opens pull requests on GitHub through the gh CLI.
//
// It is the second forge adapter, which is the order the roadmap records:
// GitLab is what the reference project's application repositories use, and
// GitHub is the primary public integration — and the one Feat's own repository
// is on, so it is what publishes the work on Feat itself (docs/09-roadmap.md
// Phase 3, ADR-070).
//
// Everything GitHub-specific lives here — the executable, its flags, and the
// shape of what it prints — so that internal/forge stays a description of what
// publishing is and the daemon never learns that one forge calls the thing a
// pull request and the other a merge request.
//
// The CLI is run already authenticated, on the trusted host, with the user's own
// environment. This package passes no token, reads no configuration, and writes
// nothing down.
package github
