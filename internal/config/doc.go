// Package config loads, merges, and validates the YAML project configuration
// described in docs/07-configuration-model.md.
//
// Dependency rule: config depends on the standard library, a YAML decoder, and
// internal/domain only. It must not import adapter, daemon, api, or ui
// packages, so that validation stays testable without a host environment.
//
// Rules this package must enforce:
//
//   - unknown YAML fields fail with a useful location and message;
//   - IDs, branch templates, and runtime project-name templates produce safe
//     names, and worktree roots cannot resolve to a broad unsafe path;
//   - container paths are absolute and non-overlapping unless explicitly
//     allowed;
//   - configuration may reference secret file paths but never contains copied
//     secret values, and redaction applies to all resolved output;
//   - capabilities are explicit and independent: Git and provider CLI access
//     never imply Docker access.
//
// Delivered by slice 3.
package config
