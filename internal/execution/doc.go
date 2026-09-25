// Package execution defines the interface behind which host-native and
// devcontainer agent execution are interchangeable.
//
// The contract is in docs/06-technical-architecture.md. ADR-033 amends it in
// three places. Command returns an argument vector rather than an *exec.Cmd,
// because internal/tmux constructs the process. Run is added, because validation
// asks an environment questions rather than attaching a terminal to it. Shell is
// folded into Command, because the daemon already decides what a task shell is.
// Destroy belongs to cleanup, which owns what is retained and what must be
// confirmed.
//
// Agent execution and application runtime are separate concepts even when both
// use Docker Compose. This package covers only where the agent runs;
// internal/runtime covers the application under development.
//
// Only devcontainer execution implements the interface. Host-native execution is
// the absence of a container, so internal/daemon dispatches a project whose
// agent.execution.mode is host straight to the agent launch instead of building
// an environment first (ADR-090).
//
// That mode offers convenience and no container security boundary, and
// diagnostics and documentation must say so plainly rather than implying
// isolation. Host-native and devcontainer execution share one task domain;
// selecting a mode must not change task semantics (ADR-070).
//
// An environment receives final values and reads neither configuration nor
// persistent state: the daemon expands templates and records what an adapter
// reports, as it does for Git (ADR-029) and the agent (ADR-032). The
// execution-stays-an-adapter depguard rule makes that mechanical.
//
// Commands are argument vectors, never interpolated shell strings.
package execution
