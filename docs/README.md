# Feat Product Specification

Status: accepted product direction; implementation started, slices 0 and 1 complete, slice 2 in progress  
Working name: **Feat**  
Primary CLI: `feat`  
Initial implementation language: Go  
License: Apache 2.0

Feat is a terminal-native development control plane for running feature work through multiple coding-agent sessions in parallel. It connects tasks, Git worktrees, native agent terminals, optional devcontainers, application runtimes, review, and later PR/MR publication without replacing the underlying tools.

## Read this package in order

1. [01-product-vision.md](01-product-vision.md)
2. [02-user-workflows.md](02-user-workflows.md)
3. [03-domain-model.md](03-domain-model.md)
4. [04-functional-specification.md](04-functional-specification.md)
5. [05-security-model.md](05-security-model.md)
6. [06-technical-architecture.md](06-technical-architecture.md)
7. [07-configuration-model.md](07-configuration-model.md)
8. [08-v0-scope.md](08-v0-scope.md)
9. [09-roadmap.md](09-roadmap.md)
10. [10-decisions-and-open-questions.md](10-decisions-and-open-questions.md)
11. [11-implementation-plan.md](11-implementation-plan.md)

The root [CLAUDE.md](CLAUDE.md) is the implementation contract for Claude Code. It intentionally points back to these documents instead of duplicating the specification.

## Product thesis

> Feat lets a developer turn several tasks into independent, persistent agent workspaces and supervise them through implementation, runtime testing, review, and publication without manually coordinating terminals, branches, paths, containers, and task context.

The long-term destination is ticket-to-PR/MR execution. The initial wedge is narrower: reliable parallel Claude Code sessions with one branch, worktree set, agent session, and feature environment per task.

## Core invariants

- One task owns one agent session and one feature environment.
- A task may span several repositories.
- Every editable task repository receives its own branch and worktree.
- The recorded base commit is immutable for the lifetime of the task.
- tmux is the v0 execution backend, not the product's source of truth.
- Native agent interfaces remain intact; Feat attaches to them instead of recreating them.
- Agent execution and application runtime are separate concepts.
- A coding agent never receives Docker access merely because Feat manages Docker on the host.
- Runtime, provider, and ticket capabilities are explicit project configuration.
- Destructive cleanup is never implicit.

## v0 command shape

```text
feat
feat implement
feat implement --file task.md
feat project add
feat project list
feat task list
feat attach <task>
feat review <task>
feat runtime start <task>
feat runtime stop <task>
feat cleanup <task>
feat doctor
feat daemon start|stop|status
feat daemon run
```

`feat` without arguments opens the dashboard. `feat implement` opens task preparation and does not start an agent until the user confirms the final task brief and repository selection.

`feat daemon run` is the foreground daemon that `feat daemon start` spawns, and the command a later launchd/systemd unit invokes. It is hidden from help because `feat daemon start` is the user-facing entry point; see ADR-027.

## Release boundary

- **v0.1 dogfood:** the multi-repository company project, Claude Code in a non-root devcontainer, manual Compose lifecycle, macOS, local prompt/Markdown tasks.
- **v0.2 public preview:** generalized configuration, host-native execution, macOS and Linux, public documentation and release packaging.
- Ticket ingestion, automated runtime phases, provider publication workflows, stable hostnames, additional agents, and remote control are roadmap work unless explicitly pulled forward.

