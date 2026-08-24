# Feat Product Specification

Status: accepted product direction; alpha, v0.1 dogfood scope complete  
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

The root [CLAUDE.md](../CLAUDE.md) is the implementation contract for Claude Code. It intentionally points back to these documents instead of duplicating the specification.

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
feat implement --project <project>
feat project init [<project>]
feat project add <project>
feat project list
feat project show <project>
feat task list
feat task attach <task>
feat task review <task>
feat task publish <task>
feat task resume <task>
feat task stop <task>
feat task cleanup <task>
feat runtime create <task>
feat runtime start <task>
feat runtime stop <task>
feat runtime status <task>
feat runtime logs <task>
feat runtime destroy <task> [--yes]
feat doctor
feat daemon start|stop|status
feat daemon run
```

`feat` without arguments opens the dashboard. `feat implement` opens task preparation and creates nothing until the user confirms the final task brief and repository selection. Confirming creates exactly what was displayed: a draft that changed since the plan was shown is refused rather than launched, and `--project` preselects the project without removing the confirmation. See ADR-031.

`feat project add` takes the project's identifier, which is also its configuration file's name; the daemon reads the file from the configuration directory rather than from a path a caller supplies. See ADR-028.

`feat project init` writes that file by asking about the project rather than
requiring it to be authored by hand. It derives from the host what the host can
answer, validates the configuration it composed before displaying it, writes
nothing until the user confirms, and never overwrites an existing configuration.
Diagnosing and registering stay the commands they already are; the wizard offers
each and runs neither on its own. See ADR-062.

The questions themselves are `internal/wizard`, which owns the sequence, the
proposals, and the validation, and reaches the machine through a Host the
command implements. The dashboard drives the same flow on `p`, as a dialog with
a cursor and a step back out of an answer, so that neither asker can drift from
the other. See ADR-063.

Every command that acts on an existing task lives under `feat task`, because
naming a task is what they have in common. `feat implement` stays at the top
level: it takes no task, it produces one. `feat attach` and `feat review` also
answer to those shorter names, which are hidden from help and run the same
implementation; `feat task cleanup` deliberately has no short name. See ADR-040.

Every `<task>` above is a task's short key, its whole identifier, or any prefix
of that identifier. The key is what every list prints, the identifier is what the
dashboard's task detail shows, and a prefix matching two tasks is reported rather
than resolved to either. See ADR-038.

`feat task resume` and `feat task stop` are the whole lifecycle of the
environment a task's agent runs in. A launch creates it, a stop puts it to sleep
keeping the worktrees, branches, control workspace, volumes, and terminal, a
resume brings the same containers back and continues the recorded session, and
cleanup removes it. There is deliberately no verb that starts one without a
session: every route to a running agent environment goes through the session that
owns it, so no container exists that no task accounts for. See ADR-057.

`feat runtime` carries the six manual actions FR-RUN-005 names. Each is an
explicit user request: no workflow transition and no agent reaches one, and
approval offers to stop a task's services rather than stopping them. Destroying
asks for confirmation, retains every volume, and never touches a resource the
project declares external. See ADR-034.

`feat task publish <task>` opens one merge request per changed repository, from
this machine and with the authentication the user already has here. The agent
holds no provider credential and publishes nothing: it drafts a title and a
description per repository, and what the user reads and edits in their own editor
is what is sent. It shows what publishing would do, opens that draft, asks once,
and then pushes and opens one repository at a time, recording every result before
the next. Nothing is undone: a failure on the third of five leaves the first two
open, still attempts the last two, and publishing again skips whatever already
has a merge request. It needs a terminal, because reading the draft is the whole
of the control. See ADR-070 and ADR-073.

`feat task cleanup <task>` prints the exact inventory of what a task owns and removes
only what is selected. Each class is a separate choice, dirty or unmerged work
needs a second confirmation naming what would be lost, and volumes are retained
unless chosen. There is deliberately no flag that answers every question; outside
a terminal the inventory is printed and nothing is removed. See ADR-037.

`feat daemon run` is the foreground daemon that `feat daemon start` spawns, and the command a later launchd/systemd unit invokes. It is hidden from help because `feat daemon start` is the user-facing entry point; see ADR-027.

## Release boundary

- **v0.1 dogfood:** the multi-repository reference project, Claude Code in a non-root devcontainer, manual Compose lifecycle, macOS, local prompt/Markdown tasks.
- **v0.2 public preview:** generalized configuration, host-native execution, macOS and Linux, public documentation and release packaging.
- Ticket ingestion, automated runtime phases, stable hostnames, additional agents, and remote control are roadmap work unless explicitly pulled forward. Publication was pulled forward, ahead of the public preview, because a dogfood task cannot finish without it; the tracker follows it. See ADR-072.

