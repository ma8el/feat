// Package paths resolves Feat's configuration, state, and runtime directories.
//
// It is not listed in docs/06-technical-architecture.md, which specifies the
// directory layout but gives it no owner. internal/config, internal/store/fs,
// and internal/daemon all need the same resolution, and none of them should
// depend on another to get it, so it lives in its own leaf package.
// See docs/10-decisions-and-open-questions.md, ADR-025 and ADR-027.
//
// It resolves:
//
//   - ~/.config/feat for human-authored YAML configuration;
//   - ~/.local/share/feat for JSON snapshots, JSONL events, Markdown briefs,
//     and the daemon log;
//   - the user runtime directory for the socket, the ownership lock, and the
//     endpoint record, falling back from XDG_RUNTIME_DIR to the per-user
//     temporary directory to /tmp, because macOS has no XDG runtime directory;
//   - XDG environment overrides and a leading "~".
//
// This package is a leaf: it imports only the standard library, and it resolves
// paths without creating, reading, or changing anything. Whether a resolved
// directory may safely be used is a separate question, answered by the
// component that owns the file: internal/daemon validates the ownership of the
// runtime directory before it binds a socket there.
package paths
