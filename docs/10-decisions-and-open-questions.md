# Decisions and Open Questions

This document is the architecture decision log before implementation. Accepted decisions should not be reopened during v0 implementation without new evidence.

## Accepted decisions

### ADR-001 — Working name and CLI

Status: accepted

- Working product name: Feat
- Binary: `feat`
- `feat` opens the dashboard.
- Use scoped commands such as `feat project add`; avoid ambiguous `feat add`.
- Revisit brand ownership before public release without blocking dogfood.

### ADR-002 — Product type

Status: accepted

Feat is an orchestration layer connecting native tools. It does not replace Claude Code, tmux, Git, Docker Compose, or Neovim.

### ADR-003 — Task invariant

Status: accepted

One task owns one agent session, one selected set of worktrees, and one feature runtime. Tasks do not share feature environments initially.

### ADR-004 — Multi-repository from the beginning

Status: accepted

The reference project requires `dashboard`, `database`, and devcontainer-definition repositories. The domain model supports multiple repositories in v0.1.

### ADR-005 — tmux backend

Status: accepted

tmux is required in initial versions and runs through a dedicated Feat server/socket. It is an execution backend, not the source of task truth. Preserve user configuration/keybindings where possible.

### ADR-006 — TUI shape

Status: accepted

Use a structured dashboard and task detail with native terminal attach. Do not recreate the Claude UI or implement a source diff renderer in v0.

### ADR-007 — Technology

Status: accepted

- Go
- Cobra
- Bubble Tea
- one binary with daemon/TUI/CLI modes
- Apache 2.0

### ADR-008 — Daemon from the beginning

Status: accepted

The TUI auto-starts a local daemon. Explicit daemon commands exist. The daemon is the sole state writer.

### ADR-009 — Local API

Status: accepted

HTTP/JSON over a Unix-domain socket, SSE for state events, no TCP listener in v0. Future terminal streaming may use WebSockets.

### ADR-010 — Configuration and state formats

Status: accepted

- YAML for human configuration
- JSON for snapshots
- JSON Lines for event history
- Markdown for task briefs/reports
- file-backed storage behind an interface; no SQLite in v0

### ADR-011 — Agent providers

Status: accepted

Claude Code only in v0. Provider-neutral interfaces from the beginning. Codex later.

### ADR-012 — Agent execution profiles

Status: accepted

Devcontainer execution is required for dogfood but optional product-wide. Host-native execution joins public v0.

### ADR-013 — Runtime integration

Status: accepted

Invoke Docker Compose CLI on the host. v0 uses base files plus generated override and manual application lifecycle.

### ADR-014 — Docker security

Status: accepted

The company agent is non-root and receives no Docker socket/CLI. A normal devcontainer is not claimed to resist deliberate kernel/runtime exploitation.

### ADR-015 — Network

Status: accepted

General internet access is allowed. Feat does not claim network DLP.

### ADR-016 — Git access

Status: accepted

Claude has full Git access. Worktree shared-metadata implications are documented. Commits are optional and never automatic by Feat in v0.

### ADR-017 — GitHub/GitLab access

Status: accepted

Claude may have authenticated `gh`/`glab` access inside its execution environment. Feat validates configured capabilities; Docker access remains denied. GitLab is required for the company project, while GitHub is the primary public integration.

### ADR-018 — Task input

Status: accepted

v0 supports prompts and Markdown files with an editable preparation step. Shortcut is post-v0 unless time remains after core reliability.

### ADR-019 — Review

Status: accepted

Group changes by repository, compare to immutable recorded bases, and launch configurable Git/editor commands. Neovim is the reference editor.

### ADR-020 — Resource policy

Status: accepted

Show whole-machine availability and per-task totals. Do not enforce concurrency limits in v0.

### ADR-021 — Cleanup

Status: accepted

Conservative, explicit cleanup; volumes retained by default; no age-based automatic deletion.

### ADR-022 — Distribution and telemetry

Status: accepted

Public v0: release binaries, Homebrew, `go install`, macOS/Linux, and no telemetry.

### ADR-023 — Open-source boundary

Status: accepted

Local core and local/LAN web client open source. Hosted relay, push, and later team control may be commercial.

### ADR-024 — Plugin strategy

Status: accepted

Adapters are compiled-in Go implementations initially. Maintain interface/package boundaries that leave a future external plugin protocol possible.

### ADR-025 — Package layout additions and mechanical rule enforcement

Status: accepted  
Recorded: slice 0

Evidence found while bootstrapping the repository:

1. The suggested package list in [06-technical-architecture.md](06-technical-architecture.md) specifies the configuration, state, and runtime directory layout but gives it no owner. `internal/config`, `internal/store/fs`, and `internal/daemon` all need the same resolution, and making any one of them the owner creates a dependency the other two should not have.
2. Placing the Cobra tree in `cmd/feat` puts the whole command surface in `package main`, where it cannot be constructed by a test. The slice 0 acceptance criterion "`feat --help` shows the intended top-level command model" is only checkable if it can.
3. Several architectural and security rules in `CLAUDE.md` are import rules or source-content rules. Leaving them to review attention makes them likely to erode across fourteen slices.

Decisions:

- Add `internal/paths` as a standard-library-only leaf package owning configuration, state, and runtime directory resolution, including XDG overrides, safe `~` expansion, and the documented runtime-directory fallback. It resolves paths and does not create or mutate anything. Implemented by whichever of slice 2 or 3 needs it first.
- Add `internal/cli` for the command tree and process exit codes. `cmd/feat` keeps only signal handling and the exit call.
- Add `internal/version` for build identity, shared by `feat version`, the health screen, and `feat doctor`.
- Add `internal/guard` for repository-wide invariant tests. It contains no runtime code.
- Enforce import boundaries with `depguard` rules in `.golangci.yml` rather than by convention. A change to those rules is an architectural change and requires an ADR.
- Enforce the "no reference-project identifiers" scope rule and the "argument vectors, not interpolated shell commands" rule with AST tests in `internal/guard`. Exemptions are recorded in a reviewable denylist file, not in `//nolint` comments.
- Pin the command surface with a golden file so the published command model cannot drift silently.

Consequence: the acceptance criteria of slice 0 and CLAUDE.md scope rule 3 are checked by `go test ./...` and `golangci-lint run` on every change, on both target platforms.

This decision affects package layout only. It does not change any product behaviour, milestone, or scope boundary.

### ADR-026 — Domain and storage modelling

Status: accepted  
Recorded: slice 1

Evidence found while implementing the domain and the file-backed store:

1. [03-domain-model.md](03-domain-model.md) listed an attention state on both `Task` and `AgentSession`. They answer the same question, and the dashboard, the notification policy, and review all ask it of the task. Two copies would be two sources of truth for one answer.
2. Invariant 8 says the resolved base commit never changes "after task creation", while FR-TASK-003 requires a draft to show resolved bases before anything is created. Both hold only if a draft is not yet a created task.
3. Workflow state is decided by Feat, while process, attention, and runtime states are observed. Applying a transition table to an observation would reject what the world actually reported and turn stored state into a claim rather than a record.
4. The stored file format is a compatibility surface with a documented migration policy. If the documents were the domain structs, renaming a Go field would silently change the format of every existing state directory.
5. Events need a total order per task, and a caller supplying its own sequence numbers would have to know what every other caller had already written.

Decisions:

- Attention state is recorded on the task only. The session keeps process state, which is genuinely per-session. [03-domain-model.md](03-domain-model.md) was updated in the same change.
- A task's shape — title, brief, repository selection, and resolved bases — is mutable while it is a draft and frozen when it leaves draft. That single rule carries FR-TASK-003, the frozen brief, and invariant 8.
- Workflow transitions are checked against a transition table plus the preconditions of the target state; observed dimensions validate values only. Both failures are typed errors that name the task and what is missing.
- Stored documents are a separate versioned representation inside `internal/store/fs`, not the domain types. Golden files pin the format, so changing it requires a new schema version and a migration. The document a migration replaces is retained as `<file>.v<version>.bak`.
- The event log assigns sequence numbers on append, starting at 1, and reports an incomplete final record rather than hiding it.
- The human-facing short task identifier is derived from the task UUID rather than stored beside it, so the two cannot disagree. A key collision within a project is resolved by generating another task identifier.

Consequence: no product behaviour, milestone, or scope boundary changes. The rules above are checked by unit tests in `internal/domain` and `internal/store/fs`, and the format is checked by golden files.

## Open questions

### OQ-001 — Natural-language orchestrator

Should mature natural-language commands be interpreted by a constrained master native agent or by a host-integrated model? Do not decide during v0.

### OQ-002 — Remote interaction surface

Should the remote client primarily stream the real tmux terminal or present a simplified prompt/response surface? Terminal streaming is the working hypothesis and needs user validation.

### OQ-003 — Automatic runtime rules

What lifecycle rule language is sufficient for task start, review request, approval, and cleanup? Are task-level overrides necessary?

### OQ-004 — Cleanup retention

Which generated volumes can safely become ephemeral after real-project evidence? Initial answer: none automatically.

### OQ-005 — Strong isolation

Which microVM/hardened runtime provides useful hostile-code isolation without excessive resource cost or Git friction?

### OQ-006 — Task-local Git metadata

Can Feat provide full useful Git behavior without exposing shared worktree metadata, and is the complexity justified?

### OQ-007 — Claude configuration isolation

When should the shared dedicated Claude profile become per-task while preserving one interactive login?

### OQ-008 — Stable hostname implementation

Which local proxy and name-resolution approach works consistently across macOS and Linux with minimal privilege?

### OQ-009 — Plugin protocol

What external adapter protocol is justified after internal interfaces have stabilized? Do not define it speculatively.

### OQ-010 — Mobile product scope

Which remote actions users actually perform on a phone remains a product discovery question. Do not build native mobile apps before PWA usage evidence.

### OQ-011 — External/shared database automation

The dogfood project uses pre-existing staging databases. Assignment, migration, seed, and cleanup conventions need project evidence before generalization.

## Decision change process

During implementation:

1. Record new evidence.
2. Identify affected requirements and milestones.
3. Add or amend an ADR.
4. Update linked specification files in the same change.
5. Do not silently let implementation behavior become the specification.

