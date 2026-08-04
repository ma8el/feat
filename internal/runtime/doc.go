// Package runtime defines the application runtime interface for the software
// under development.
//
// The package name shadows the standard library's runtime; import that as
// stdruntime where both are needed.
//
// The application runtime is separate from agent execution even when both use
// Docker Compose. One task owns at most one runtime environment, and runtime
// environments are never shared between tasks in v0.
//
// Rules this package must preserve:
//
//   - v0 lifecycle is manual and explicit: create, start, stop, status, logs,
//     destroy. Automated phases are roadmap work;
//   - resources are managed or external; external resources such as a
//     pre-existing staging database are referenced but never provisioned or
//     destroyed by Feat;
//   - container running state and service health are distinct. Without
//     configured health checks the state is "running, health unknown";
//   - a runtime request arriving from an agent is inert until host validation
//     and user approval.
//
// Delivered by slice 9.
package runtime
