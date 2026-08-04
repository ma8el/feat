// Package project implements project registration and host diagnostics, and
// backs `feat project` and `feat doctor`.
//
// Responsibilities:
//
//   - register a project from validated YAML and assign its stable ID;
//   - check host prerequisites such as Git, tmux, and the Docker Compose CLI;
//   - check that configured repositories, Compose files, and services resolve;
//   - report repository and container path mappings accurately;
//   - report provider CLI capability as disabled, optional, or required, and
//     validate it inside the execution environment where the agent will run it.
//
// Diagnostics must be actionable and must never print secret file contents.
//
// Delivered by slice 3.
package project
