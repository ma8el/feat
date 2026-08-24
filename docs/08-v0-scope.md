# v0 Scope

## Release strategy

`v0` consists of two milestones:

- `v0.1`: personally usable dogfood on the reference project.
- `v0.2`: generalized public preview.

The first milestone is allowed to use manually authored project YAML and project-specific configuration. It must not hard-code the reference project's paths, repository names, or services into the domain model.

## v0.1 dogfood

### Supported environment

- macOS host
- Go single binary
- Git repositories on host
- dedicated Feat tmux server
- Claude Code in an existing Compose-defined devcontainer
- non-root agent user
- no Docker socket/CLI available to Claude
- general internet access
- full Git access
- optional/required `glab` or `gh` inside devcontainer
- Docker Compose CLI on host

### Project model

- One registered project is sufficient for dogfood.
- Several repositories are required.
- `dashboard` is the primary editable repository.
- `database` can be omitted, read-only, or read-write per task.
- the devcontainer-definition repository is normally stable read-only.
- one task may span all selected repositories.

### Task input

Included:

- interactive prompt;
- Markdown file;
- editable prelaunch task brief;
- explicit repository access selection.

Excluded:

- Shortcut ingestion;
- GitHub Issues ingestion;
- natural-language multi-task selection.

### Git lifecycle

Included:

- fetch configured remotes;
- resolve and record base commits;
- configurable branch naming;
- task worktree per selected repository;
- dirty ordinary checkout does not block creation;
- full Git available to Claude;
- committed or uncommitted final changes;
- safe worktree/branch cleanup planning.

Excluded:

- automatic rebase;
- automatic conflict resolution;
- automatic commit;
- automatic push/PR/MR workflow owned by Feat.

Claude may use authenticated `glab`/`gh` directly when the user prompts it; Feat only validates the configured capability in v0.1.

### Agent lifecycle

Included:

- Claude Code adapter;
- generated task instructions and settings outside repositories;
- native interactive Claude TUI;
- one session per task;
- hooks/control events for starting, working, idle, failure, prompt submission, and explicit review request where supported;
- short idle grace period;
- attach/detach;
- optional on-demand shell pane inside the devcontainer;
- dedicated control workspace.

Excluded:

- Codex or other agent adapters;
- multiple collaborating agents in one task;
- master agent;
- recreated chat UI.

### Runtime lifecycle

Included:

- configured base Compose files plus generated task override;
- unique Compose project identity;
- correct task worktree/control mounts;
- start agent devcontainer;
- explicit application create/start/stop/status/logs/destroy actions;
- a generated per-task discriminator an application can name a share of an
  external staging database by (ADR-048);
- Compose health when natively available;
- no automatic restart after recovery.

Excluded:

- automatic application start;
- agent-controlled Docker;
- automatic runtime phases;
- port-range allocation unless required to make the reference project run. **The
  condition was met and this exclusion is spent.** The reference project's own
  application publishes its entry point at a fixed host port, and a host port is
  global to the machine: the second task's runtime could not start at all, and
  its frontend reached the first task's API through an address baked to that
  number. Testing one task's application while other agents work is the reason a
  per-task runtime exists, so allocation is what makes the milestone's own
  runtime work rather than a widening of it (ADR-065 evidence 8);
- stable local hostnames;
- database provisioning/reset/destruction;
- additional runtime backends.

### TUI

Included:

- global dashboard structure, even if only one project is registered;
- task rows with agent, attention, runtime, verification, elapsed-time, resource, repository, and change state;
- task detail;
- attach, shell, runtime, review, and cleanup actions;
- whole-machine resources;
- per-task aggregate resources;
- TUI attention badges;
- macOS notifications.

Excluded:

- built-in source diff renderer;
- full transcript display;
- PR state in task rows;
- inline code comments;
- enforced concurrency limit.

### Review

Included:

- change summary grouped by repository;
- each repository compared to its recorded base commit;
- configurable diff shortcut;
- configurable `$EDITOR`/Neovim shortcut;
- configurable status shortcut;
- approve, leave pending, or attach for revision.

### Persistence and recovery

Included:

- YAML project configuration;
- JSON snapshots;
- JSONL event history;
- Markdown task brief;
- atomic file writes;
- daemon auto-start from TUI;
- reconciliation of tmux, worktrees, Compose projects, task state, control events, and pending review;
- no automatic container restart.

### Cleanup

Included:

- exact resource inventory;
- separate stop/remove/volume/worktree/branch choices;
- volumes retained by default;
- dirty/unmerged warnings;
- explicit confirmations;
- archived metadata;
- no age-based deletion.

## v0.1 acceptance criteria

1. Three independent tasks can run concurrently in the reference project.
2. Every task has correct branches and worktrees across selected repositories.
3. Every task mounts the correct repository versions at the devcontainer-defined paths.
4. Claude runs as the configured non-root user.
5. Claude has no Docker socket or usable host Docker CLI.
6. Git and configured GitLab CLI authentication work inside the agent environment.
7. Application runtime commands affect only the selected task Compose identity.
8. The user can review one task while other agents continue.
9. Idle notifications arrive after the grace period without firing while attached.
10. A daemon restart preserves task identity and reconciles live resources.
11. Stopped containers are shown but not restarted.
12. Diff/editor shortcuts compare against the correct base per repository.
13. Cleanup does not remove dirty/unmerged work or retained volumes without explicit confirmation.
14. The user no longer manually coordinates terminal sessions, worktree paths, branches, and Compose project identities.

## v0.2 public preview additions

- Linux support
- host-native agent execution
- generalized configuration examples
- clearer project registration/manual onboarding
- JSON Schema and shell completion
- public diagnostics and troubleshooting
- GitHub release binaries
- Homebrew tap
- `go install`
- Linux desktop notification where supported
- Apache 2.0 license and contribution documentation
- no telemetry

Shortcut integration enters v0.2 only if core reliability is already complete and time remains. It must not delay public preview.

## Explicit v0 non-goals

- Ticket-to-PR automation
- Automated runtime lifecycle phases
- Stable per-task hostnames
- Codex adapter
- Mobile/web control
- Team features
- Plugin protocol
- Built-in diff/editor
- Automatic merging
- Strong microVM isolation
- Runtime or database templates beyond generic Compose primitives

## Definition of done for public v0

Public v0 is ready when a new macOS or Linux user can configure a normal Git project, run two host-native or devcontainer Claude tasks in parallel, attach and review safely, recover after restart, and understand every created resource through diagnostics and documentation.

