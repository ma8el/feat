// Package paths resolves Feat's configuration, state, and runtime directories.
//
// It is not listed in docs/06-technical-architecture.md, which specifies the
// directory layout but gives it no owner. internal/config, internal/store/fs,
// and internal/daemon all need the same resolution, and none of them should
// depend on another to get it, so it lives in its own leaf package.
// See docs/10-decisions-and-open-questions.md, ADR-025.
//
// Responsibilities, once implemented:
//
//   - ~/.config/feat for human-authored YAML configuration;
//   - ~/.local/share/feat for JSON snapshots, JSONL events, and Markdown briefs;
//   - the user runtime directory for the socket and PID file, with the
//     documented fallback;
//   - XDG environment overrides and safe "~" expansion.
//
// This package must remain a leaf: it imports only the standard library, and it
// resolves paths without creating or mutating anything.
//
// Delivered by slice 2 or 3, whichever needs it first. Slice 0 records only the
// boundary.
package paths
