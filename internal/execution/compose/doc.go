// Package compose runs the coding agent in a project's Compose service.
//
// The adapter starts the configured dev service, mounts the task's worktrees at
// the project-defined container paths, mounts the control workspace, and runs
// the agent as the configured user (ADR-033). Every Docker command is an
// argument vector run on the trusted host.
//
// Rules this package must enforce (docs/05-security-model.md):
//
//   - no Docker socket and no host Docker CLI reach the agent container;
//   - neither the daemon socket nor the runtime-control socket reaches it;
//   - the generated override carries no secret value;
//   - a repository the task selected read-only is mounted read-only;
//   - the agent runs as a non-root user.
//
// The last two are checked against the running container rather than against
// what the project declared. Inspect asks the container who it runs as, what it
// has mounted, and what it was granted beyond that. The launch probe asks
// whether the image carries a client that speaks a container runtime's API,
// which is the capability agent.capabilities.docker denies (ADR-080).
//
// A devcontainer separates the agent from the host's files and processes. It is
// not a boundary against a deliberate kernel or container-runtime exploit, and
// no message here may suggest that it is.
package compose
