// Package claude implements the agent interface for Claude Code, the only
// provider adapter in v0.
//
// Responsibilities:
//
//   - generate task-specific settings and instructions outside the
//     repositories, while letting checked-in CLAUDE.md and project settings
//     continue to apply;
//   - launch the native interactive CLI in the correct task working directory
//     with the final brief;
//   - install supported hooks for session start, prompt submission, Stop/idle,
//     completion, failure, and notification;
//   - normalize hook output into agent events written to the control outbox;
//   - optionally enforce configured checks through provider-native completion
//     hooks, returning failures to the native agent loop when configured;
//   - validate configured gh/glab availability and authentication inside the
//     execution environment where Claude will run.
//
// Rules this package must enforce:
//
//   - every Claude-specific flag, hook schema, and parser stays inside this
//     package;
//   - a normal Stop event becomes idle only after the configured grace period,
//     and never becomes review completion;
//   - exact CLI flags and hook schemas are verified against the supported
//     Claude Code version rather than assumed.
//
// Delivered by slice 7.
package claude
