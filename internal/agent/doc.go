// Package agent defines the provider-neutral coding-agent interface and the
// normalized agent event vocabulary.
//
// The contract is in docs/06-technical-architecture.md. Adapter carries
// Validate, Prepare, and ParseEvent; the observation that the conceptual
// Reconcile stands for is the tmux adapter's, because what a session's process
// is doing is a question about its terminal rather than about its provider.
//
// Dependency rule: provider-specific flags, hooks, event schemas, and parsing
// stay inside the provider adapter subpackage. Nothing Claude-specific may
// appear here, and the interface must not leak implementation types, so that an
// external plugin protocol remains possible later.
//
// The seam between an agent and where it runs is Workspace and LaunchSpec: an
// adapter is told how the agent will see its own filesystem and answers with a
// command in those terms. While execution is host-native those paths are the
// host's; once slice 8 runs the agent in a container they are the container's,
// and no adapter code changes. Environment is the matching seam for validation,
// so a probe always runs where the agent will run rather than wherever the
// daemon happens to be.
//
// Rules this package must preserve:
//
//   - a Stop or end-of-turn event means idle, never semantic completion;
//   - semantic completion requires an explicit review or completion event;
//   - structured hooks and control files are the source of truth; terminal-text
//     heuristics may only ever be a supplementary signal;
//   - an event carries state, never transcript: no prompt text, no assistant
//     message, and no terminal output reaches the task history or the event
//     stream through one.
package agent
