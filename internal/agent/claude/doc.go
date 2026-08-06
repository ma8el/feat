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
//   - validate the agent executable and configured gh/glab availability and
//     authentication inside the execution environment where Claude will run.
//
// Enforcing configured checks through a provider-native completion gate is not
// here. Those checks run in the agent's environment, and nothing starts one
// before slice 8; until then a review request carries what the agent says it
// ran, attributed to the agent (ADR-032).
//
// Rules this package must enforce:
//
//   - every Claude-specific flag, hook schema, and parser stays inside this
//     package;
//   - a normal Stop event becomes idle only after the configured grace period,
//     and never becomes review completion;
//   - exact CLI flags and hook schemas are verified against the supported
//     Claude Code version rather than assumed. See version.go for the version
//     this build was checked against.
//
// The generated hooks are the one part of this package that runs outside Go,
// and they are deliberately almost empty: a hook copies its payload into the
// control outbox and exits, because parsing belongs in code that can be tested
// and because Claude gives a hook's standard output and exit status meaning. A
// hook that printed would put Feat's words into the user's conversation, and a
// hook that failed would block the agent. See hookScript in hooks.go.
package claude
