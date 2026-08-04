// Package control implements the task control-workspace protocol: the
// versioned inbox and outbox through which the host and the agent exchange
// structured messages.
//
// Host layout is task-scoped: task.md, context/, inbox/, outbox/, reports/.
// The container mount path is configurable, defaulting to /feat.
//
// Messages are versioned JSON documents written by atomic rename. Control
// messages never execute themselves. Before a message can change state or
// surface an action, the daemon validates:
//
//   - schema version;
//   - task ID and task ownership;
//   - event ID and sequence, rejecting duplicates;
//   - message type;
//   - maximum size;
//   - allowed relative paths;
//   - the capability the message requires.
//
// A runtime request creates a pending user action and stays inert until host
// validation and explicit approval. A malformed, duplicated, or out-of-task
// message must not execute or duplicate a transition.
//
// Delivered by slice 7.
package control
