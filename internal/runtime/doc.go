// Package runtime defines the application runtime interface for the software
// under development.
//
// The package name shadows the standard library's runtime; import that as
// stdruntime where both are needed.
//
// The application runtime is separate from agent execution even when both use
// Docker Compose. One task owns at most one runtime environment, and runtime
// environments are never shared between tasks in v0. internal/execution covers
// where the agent runs; this package covers what the user tests.
//
// Rules this package must preserve:
//
//   - v0 lifecycle is manual and explicit: create, start, stop, status, logs,
//     destroy. Nothing here is called by a workflow transition, a recovery pass,
//     or an agent. Automated phases are roadmap work;
//   - resources are managed or external; external resources such as a
//     pre-existing staging database are referenced but never provisioned or
//     destroyed by Feat;
//   - container running state and service health are distinct. Without
//     configured health checks the state is "running, health unknown";
//   - a runtime request arriving from an agent is inert until host validation
//     and user approval.
//
// A runtime receives final values and reads neither configuration nor
// persistent state: the daemon expands the project name template and records
// what an adapter reports, as it does for Git (ADR-029), the agent (ADR-032),
// and agent execution (ADR-033). The runtime-stays-an-adapter depguard rule
// makes that mechanical (ADR-034).
//
// Delivered by slice 9.
package runtime
