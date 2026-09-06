// Package config loads, resolves, and validates the YAML project configuration
// described in docs/07-configuration-model.md.
//
// Loading is three stages, and each one holds a property that is easy to remove
// by accident:
//
//   - Parse decodes the document strictly. An unknown field and a repeated key
//     are errors rather than values silently ignored, and the decoder reports
//     the line, the column, and the surrounding text.
//   - Resolve expands a leading "~", makes paths absolute, and fills defaults
//     into the configuration itself, so that `feat project show` prints the
//     values Feat will act on rather than the text of the file.
//   - Validate reports every rule the result breaks rather than the first,
//     because a configuration file is edited by hand.
//
// Configuration is also composed here rather than only read: Draft is the
// answers `feat project init` collects, and it renders a file, parses it,
// resolves it, and validates it through the same three stages. A caller can
// therefore obtain a generated configuration only by obtaining one Feat
// accepts.
//
// This package checks shape and safety; it never asks the host a question.
// Whether a path exists, holds a Git repository, or names a real Compose
// service is diagnostics, and that lives in internal/project. The line between
// them is what keeps a configuration loadable on a machine where a repository
// is temporarily missing, which is the machine `feat doctor` is most useful on.
//
// Rules this package enforces:
//
//   - IDs, branch templates, and runtime project-name templates produce safe
//     names, and worktree roots cannot resolve to a broad unsafe path;
//   - container paths are absolute and non-overlapping;
//   - configuration may reference secret file paths but never contains copied
//     secret values: this package records the path of an environment file and
//     never opens it, so resolved output has nothing to redact;
//   - the one declared capability accepts only the value Feat delivers, and it
//     is declared because a launch and a diagnostic check the agent's container
//     against it (ADR-080).
//
// See ADR-028.
package config
