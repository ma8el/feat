// Package config loads, resolves, and validates the YAML project configuration
// described in docs/07-configuration-model.md.
//
// Dependency rule: config depends on the standard library, a YAML decoder,
// internal/domain, and internal/paths only. It must not import adapter, daemon,
// api, or ui packages, so that validation stays testable without a host
// environment.
//
// Loading is three stages, kept separate because they fail for different
// reasons and are fixed in different ways:
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
// accepts, and the rules live in one place for a file that was typed and a file
// that was answered.
//
// This package checks shape and safety; it never asks the host a question.
// Whether a path exists, holds a Git repository, or names a real Compose
// service is diagnostics, and that lives in internal/project. The line between
// them is what keeps a configuration loadable on a machine where a repository
// is temporarily missing, which is the machine `feat doctor` is most useful on.
//
// Rules this package enforces:
//
//   - unknown YAML fields fail with a useful location and message;
//   - IDs, branch templates, and runtime project-name templates produce safe
//     names, and worktree roots cannot resolve to a broad unsafe path;
//   - container paths are absolute and non-overlapping;
//   - configuration may reference secret file paths but never contains copied
//     secret values: this package records the path of an environment file and
//     never opens it, so resolved output has nothing to redact;
//   - capabilities are explicit and independent: Git access never implies
//     Docker access, and the capabilities Feat cannot actually vary accept only
//     the value Feat delivers.
//
// See ADR-028 in docs/10-decisions-and-open-questions.md.
package config
