// Package control implements the task control-workspace protocol: the
// versioned inbox and outbox through which the host and the agent exchange
// structured messages.
//
// The workspace lives under the state directory but outside the per-task
// snapshot directory, because it is the one tree an agent writes to and it is
// mounted into the agent's execution environment. Its layout is split by who
// writes to each part: task.md, context/, and inbox/ are host-written and
// agent-read; outbox/ and reports/ are agent-written; agent/ is host-only and
// holds what the provider adapter generated together with the record of which
// messages have been applied. Keeping that record host-only means marking a
// message processed never requires writing into the directory the agent owns
// (ADR-032). The container mount path is configurable, defaulting to /feat.
//
// Messages are versioned JSON documents written by atomic rename. Control
// messages never execute themselves. Before a message can change state or
// surface an action, it is validated for:
//
//   - schema version;
//   - task ID and task ownership;
//   - event ID, so that one already applied is recognised rather than repeated;
//   - message type;
//   - maximum size;
//   - allowed relative paths;
//   - the capability the message requires.
//
// A runtime request creates a pending user action and stays inert until host
// validation and explicit approval. A malformed, duplicated, or out-of-task
// message must not execute or duplicate a transition.
//
// The vocabulary here is provider-neutral. A provider-native event arrives as
// TypeProviderEvent carrying the provider's own document, which only that
// provider's adapter reads, so this package never learns what any one agent's
// events mean.
//
// Delivery is polling rather than filesystem notification: notification does
// not cross a bind mount reliably on every supported platform, and a watcher
// that worked on the host while silently never firing in a container would hide
// the failure in the configuration that matters most.
package control
