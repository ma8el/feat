// Package ui holds the Bubble Tea models and views.
//
// The UI is a client. It renders state obtained from the daemon and issues
// requests through internal/client; it must never read or write persistent
// state, invoke Git, tmux, or Docker Compose, or reach into adapter packages
// directly (CLAUDE.md architectural rules).
//
// docs/08-v0-scope.md excludes a built-in source diff renderer and a full
// transcript view from v0. Review opens the user's configured external
// commands instead.
package ui
