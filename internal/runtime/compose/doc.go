// Package compose implements the application runtime over the Docker Compose
// CLI, invoked on the trusted host as an argument vector.
//
// The adapter accepts one or more base Compose files, an optional static
// override, a generated task override, host-side env-file paths, a service
// subset, and a unique project name. It retains the exact inputs in task state
// so a later reconciliation acts on the same identity.
//
// Rules this package must enforce:
//
//   - each task gets a unique Compose project identity and ownership labels, so
//     acting on one task never affects another;
//   - the generated override carries task mounts, generated non-secret
//     variables, and ownership labels only. Never copied secret values, and no
//     unnecessary container_name fields;
//   - external resources are never included in destroy plans;
//   - destroy resolves exact task-owned resources first, and volumes are
//     retained unless explicitly chosen;
//   - stopped containers are reported after recovery, never restarted.
//
// Automatic port allocation and lifecycle phases are roadmap capabilities; the
// structure must leave room for them without implementing them in v0.
//
// Delivered by slice 9.
package compose
