// Package compose runs the coding agent inside a configured Compose service as
// a non-root user.
//
// The adapter starts the configured dev service, mounts task worktrees at the
// project-defined container paths, mounts the task control workspace, and
// executes the agent as the configured user.
//
// Security rules this package must enforce (docs/05-security-model.md):
//
//   - never mount or expose a Docker socket to the agent container, and never
//     provide a host Docker CLI;
//   - never add the daemon or runtime-control socket to the agent container;
//   - never copy secret values into generated Compose overrides;
//   - honour read-only mounts for read-only repository selections;
//   - verify the agent process is non-root when policy requires it.
//
// Full Git access and configured gh/glab access are separate capabilities and
// are permitted; they must never be conflated with Docker access.
//
// Compose is invoked as an argument vector on the trusted host. A normal
// devcontainer is not claimed to resist deliberate kernel or container-runtime
// exploitation.
package compose
