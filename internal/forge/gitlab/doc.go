// Package gitlab opens merge requests on GitLab through the glab CLI.
//
// It is the first forge adapter, which is the order the roadmap records: GitLab
// is what the reference project uses, and GitHub follows (docs/09-roadmap.md
// Phase 3, ADR-070).
//
// Everything GitLab-specific lives here — the executable, its flags, and the
// shape of what it prints — so internal/forge stays a description of what
// publishing is and the daemon never learns what one forge calls a merge
// request.
//
// The CLI is run already authenticated, on the trusted host, with the user's own
// environment. This package passes no token, reads no configuration, and writes
// nothing down.
package gitlab
