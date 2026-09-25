// Package runtime defines the application runtime interface for the software
// under development.
//
// The package name shadows the standard library's runtime; import that as
// stdruntime where both are needed.
//
// The application runtime is separate from agent execution even when both use
// Docker Compose. internal/execution covers where the agent runs; this package
// covers the application's own services. One task owns at most one runtime
// environment, and v0 never shares one between tasks.
//
// Rules this package must preserve:
//
//   - the lifecycle is manual: create, start, stop, status, logs, and destroy
//     happen because a user asked for them. No workflow transition, recovery
//     pass, or agent calls any of them, and automated phases are roadmap work;
//   - a resource is managed or external. An external resource, such as a
//     staging database that existed first, is referenced and never provisioned
//     or destroyed;
//   - a container's running state is not its health. Without configured health
//     checks the answer is "running, health unknown";
//   - a runtime request from an agent is inert until the host validates it and
//     the user approves it.
//
// A runtime receives final values and reads neither configuration nor
// persistent state: the daemon expands the project name template and records
// what an adapter reports, as it does for Git (ADR-029), the agent (ADR-032),
// and agent execution (ADR-033). The runtime-stays-an-adapter depguard rule
// makes that mechanical (ADR-034).
package runtime
