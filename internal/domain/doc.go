// Package domain holds the entities, state dimensions, transitions, and typed
// errors described in docs/03-domain-model.md.
//
// Dependency rule: domain types must stay independent of Claude, tmux, Docker,
// GitHub, GitLab, and Bubble Tea. This package imports only the standard
// library. Claude Code is the first adapter, not the domain model.
//
// Modelling rules this package must preserve:
//
//   - process, attention, workflow, and runtime states are separate dimensions
//     and are never collapsed into one enum;
//   - a Stop or end-of-turn signal means idle, never semantic completion;
//   - the resolved base commit is immutable for the lifetime of a task;
//   - one task owns one agent session and at most one runtime environment;
//   - tmux identifiers are execution references, not task identity.
//
// Delivered by slice 1.
package domain
