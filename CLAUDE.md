# CLAUDE.md — Implementing Feat

You are implementing **Feat**, a terminal-native multi-agent development control plane. The product specification in this directory is authoritative.

## Read before changing code

Read in this order:

1. `README.md`
2. `01-product-vision.md`
3. `03-domain-model.md`
4. `04-functional-specification.md`
5. `05-security-model.md`
6. `06-technical-architecture.md`
7. `07-configuration-model.md`
8. `08-v0-scope.md`
9. `10-decisions-and-open-questions.md`
10. the current slice in `11-implementation-plan.md`

Use `02-user-workflows.md` when implementing user-facing behavior and `09-roadmap.md` only to preserve extension boundaries. Do not implement roadmap features during v0 slices.

## Current product contract

- Working name and binary: `Feat` / `feat`
- Go, Cobra, Bubble Tea
- One binary with daemon, TUI, and CLI modes
- HTTP/JSON over a Unix-domain socket; SSE state events
- YAML configuration; JSON snapshots; JSONL events; Markdown briefs
- File-backed state behind an interface; no SQLite in v0
- tmux required and product-managed through a dedicated socket
- Claude Code only in v0, behind an agent interface
- Multi-repository tasks from the beginning
- One task owns one agent session and one feature environment
- Devcontainer execution for dogfood; host-native by public v0
- Docker Compose CLI on the trusted host
- Agent receives no Docker socket/host Docker CLI
- Agent may have full Git and configured `gh`/`glab` access
- Manual application runtime lifecycle in v0
- External diff/editor commands; no built-in source diff viewer
- Conservative explicit cleanup
- macOS/Linux target
- Apache 2.0; no telemetry

## Scope rules

1. Implement the current ordered slice from `11-implementation-plan.md`.
2. Do not add ticket ingestion, automated runtime phases, stable hostnames, Codex, remote control, plugin RPC, team features, or an internal diff viewer during v0.1.
3. Do not hard-code the reference project's repository names, paths, Compose services, or database behavior.
4. If an accepted decision appears infeasible, stop and record concrete evidence before changing it.
5. Open questions are not permission to choose a permanent design prematurely.

## Architectural rules

- Keep domain types independent of Claude, tmux, Docker, GitHub, GitLab, and Bubble Tea.
- Keep provider-specific flags, hooks, event schemas, and parsing inside the provider adapter.
- Keep host and devcontainer execution behind one execution interface.
- Keep application runtime separate from agent execution even if both use Compose.
- Keep storage behind repositories/interfaces; the daemon is the only writer.
- Use argument vectors rather than interpolated shell commands for Git, tmux, and Docker Compose.
- Use stable IDs and tagged metadata; never use tmux window indexes or display names as identity.
- Record immutable Git base commits when launching a task.
- Make reconciliation explicit; never assume persisted desired state equals observed state.

## Security rules

- Never mount or expose a Docker socket to an agent container.
- Never add a daemon/runtime-control socket to the agent container.
- Never copy secret values into generated YAML, JSON, logs, or Compose overrides.
- Validate every control-workspace message, path, capability, size, task ID, and event ID.
- A runtime request is inert until host validation and user approval.
- Full Git and provider CLI access are allowed only when configured; do not conflate them with Docker access.
- Resolve exact task-owned resources before cleanup.
- Retain volumes by default and require explicit confirmation for dirty/unmerged work.
- Do not claim standard containers provide hostile-kernel isolation or network DLP.

## Agent-state rules

- A Claude Stop/end-of-turn event means `idle`, not complete.
- Semantic completion requires an explicit review/completion event.
- Use Claude hooks/control files before considering terminal-output heuristics.
- Keep process, attention, workflow, and runtime states separate.
- Provider-native check failures should return to the native agent loop when configured.

## Quality bar

For every slice:

- write focused unit tests for domain/config/storage logic;
- use fake adapters for orchestration tests;
- add opt-in integration tests for real Git/tmux/Compose behavior;
- make failure halfway through a lifecycle recoverable and explainable;
- include actionable errors with project/task/resource context;
- preserve unrelated user checkouts, tmux sessions, containers, volumes, and configuration;
- run formatting, lint, tests, and build before declaring the slice complete.

## Definition of complete

A slice is complete only when its acceptance criteria in `11-implementation-plan.md` pass. Feature presence without recovery, validation, and safe failure behavior is incomplete.

