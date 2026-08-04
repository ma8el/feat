// Package agent defines the provider-neutral coding-agent interface and the
// normalized agent event vocabulary.
//
// The conceptual contract is in docs/06-technical-architecture.md:
//
//	Validate(ctx, env) error
//	Prepare(ctx, task, control) (LaunchSpec, error)
//	ParseEvent(ctx, raw) (AgentEvent, error)
//	Reconcile(ctx, session) (ObservedAgentState, error)
//
// Dependency rule: provider-specific flags, hooks, event schemas, and parsing
// stay inside the provider adapter subpackage. Nothing Claude-specific may
// appear here, and the interface must not leak implementation types, so that an
// external plugin protocol remains possible later.
//
// Rules this package must preserve:
//
//   - a Stop or end-of-turn event means idle, never semantic completion;
//   - semantic completion requires an explicit review or completion event;
//   - structured hooks and control files are the source of truth; terminal-text
//     heuristics may only ever be a supplementary signal.
//
// Delivered by slice 7.
package agent
