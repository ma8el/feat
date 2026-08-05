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

### ADR-027 — Daemon ownership, runtime file layout, and local API surface

Status: accepted  
Recorded: slice 2, before implementation

Evidence found while planning the daemon and the local API:

1. [06-technical-architecture.md](06-technical-architecture.md) placed `daemon.json` in the state directory and also stated that socket and PID data use the user runtime directory. That is two homes for one fact, and they are not interchangeable: the state directory survives a reboot and the runtime directory does not, so a process identifier recorded in the state directory can outlive the machine's uptime, and a reused identifier then reports a daemon that is not running.
2. Nothing reads a durable daemon record before startup reconciliation in slice 12. A record written in slice 2 would be a versioned compatibility surface with no reader.
3. A process identifier alone cannot answer whether its owner is still alive, because identifiers are reused and the runtime directory may already exist. An advisory lock that the kernel releases on process death answers it without a heartbeat.
4. Removing a socket file whenever the recorded owner looks absent is unsafe. A live process serving that socket without holding the lock would be cut off from its clients by a second daemon that took the path.
5. The command surface addresses a task by task alone — `feat attach <task>`, `feat review <task>`, `feat cleanup <task>` — while storage addresses a task by project and task together (ADR-026). One of the two boundaries has to resolve the other.
6. A bounded per-subscriber event queue has three options when a subscriber falls behind: block the daemon, discard events silently, or end the subscription. The first makes a slow client a daemon outage; the second makes the event stream a claim rather than a record.
7. Two of the slice 2 acceptance criteria have no production code path inside slice 2: nothing writes persistent state before slice 3, and nothing publishes domain events before slice 6.
8. The exit-code contract in `internal/cli` distinguishes success, failure, usage, unimplemented, and interruption. `feat daemon status` has to report that no daemon is running, which is not a failure of the command and is none of the others.
9. `feat daemon start` needs a foreground process to spawn, and the launchd/systemd units in slice 14 need a command to invoke.

Decisions:

- Daemon liveness lives in the user runtime directory only: `feat.sock`, `daemon.lock`, and `endpoint.json` recording the process identifier, socket path, build version, and start time, owner-only inside an owner-only directory.
- `daemon.json` in the state directory is deferred to slice 12, the first slice that reads a durable daemon record; slice 12 owns writing and reconciling it. Slice 2 writes no durable daemon state, so a record that must not outlive the machine cannot. [06-technical-architecture.md](06-technical-architecture.md) and slice 12's work list were updated in the same change.
- The runtime directory resolves to `$XDG_RUNTIME_DIR/feat`, then `$TMPDIR/feat-<uid>`, then `/tmp/feat-<uid>`. A candidate that is a symlink, is owned by another user, or is writable by others fails with an actionable message instead of moving to the next candidate: two locations would mean two daemons each believing it owns the machine. The resolved socket path is checked against the platform's socket-path length limit, which is 104 bytes on macOS and 108 on Linux and is otherwise reported by `bind` as "invalid argument".
- The escape hatch from that limit is `FEAT_RUNTIME_DIR`, which moves the whole directory. This decision replaces a `FEAT_SOCKET` override recorded before implementation: an override of the socket alone separates the socket from the lock that establishes who owns it, so two daemons pointed at different sockets would each take a different lock and both believe they own the machine. The three files move together or not at all.
- Ownership is an exclusive advisory lock held for the life of the process, combined with a connect probe. A lock held by another process means a daemon is running, and its recorded identifier is reported. A free lock and a socket that refuses connections means the socket is stale: the diagnosis is logged and the path is reclaimed. A free lock and a socket that answers means something is serving without the lock, and the second daemon refuses to start rather than unlink a live socket. The lock is a Unix facility, so `internal/daemon` builds on macOS and Linux only; a Windows port would need its own ownership implementation.
- The local API addresses a task by task identifier and the daemon resolves the owning project. ADR-026's addressing rule is a storage rule and stays one.
- API payloads are a third representation, separate from the domain types and from the stored documents, for the reason ADR-026 separated the first two: renaming a Go field must not silently change a published surface. Golden files pin the response bodies, and the domain and storage error classes map to stable error codes.
- Event publication never blocks the daemon. A subscriber that falls behind its bounded queue receives a terminal event and is disconnected, so a client learns that it lost events instead of silently missing them — the choice ADR-026 already made for an incomplete final event record. Slice 2 does not support stream resume: the stream opens with an event identifying the daemon, and a `Last-Event-ID` request is answered with an explicit resynchronisation instruction rather than a pretended replay.
- `internal/paths` is implemented by slice 2, which ADR-025 left to whichever of slice 2 and 3 needed it first. Slice 3 inherits configuration-directory resolution from it.
- The daemon writes JSON logs to `logs/daemon.log` under the state directory, owner-only. The foreground mode also writes to standard error.
- `feat daemon run` is a real subcommand, hidden from help: it is the foreground daemon that `feat daemon start` spawns and that a later service unit invokes. Hiding it keeps `feat --help` equal to the documented command surface, while the golden file still pins it.
- Exit code 4 reports that no daemon is running, so `feat daemon status` can distinguish an absent daemon from a failed command. Adding it after release would collide with scripts that had already read 1 as both.
- Opening the dashboard starts a daemon; running `feat` without a terminal reports what it observes instead. Printing a summary into a pipe or a CI log should not leave a background process behind, and a non-interactive run is not the dashboard ADR-008 is about.
- A spawned daemon never spawns another, which it knows from `FEAT_DAEMON_SPAWNED` in its environment. A binary started with arguments it does not understand can fall through to the client path, and a client that starts a daemon would then start another: the marker bounds that at one process instead of a growing tree of them.
- `depguard` denies `internal/store` to `internal/api` and `internal/client`, a test in `internal/guard` enumerates the packages that import storage outside `internal/daemon`, and a second guard test rejects a TCP listener anywhere in the repository. ADR-025 requires an ADR for a change to the boundary rules; this is that record.

Consequence: the two criteria named in evidence 7 are verified structurally rather than behaviourally — the sole-writer rule as an import boundary, and event ordering through the real event bus and the real SSE endpoint driven by a fixture publisher. Pulling a write path or an event source forward would cross a slice boundary that CLAUDE.md scope rule 1 closes, so slice 2's acceptance record states this rather than implying behavioural proof.

The user-visible additions are a hidden `daemon run` subcommand and exit code 4. No milestone or scope boundary changes. These decisions are recorded before implementation; evidence found while implementing that contradicts one of them amends this ADR in the same change, per the decision change process below.

### ADR-028 — Configuration loading, project registration, and the honesty of diagnostics

Status: accepted  
Recorded: slice 3

Evidence found while implementing YAML configuration and `feat doctor`:

1. The repository had no YAML decoder. `gopkg.in/yaml.v3` rejects unknown fields through `KnownFields` and reports a line, but its upstream repository is archived and it reports no column and no surrounding text. The slice 3 acceptance criterion asks for a "useful location/message", and for a nested hand-edited document the surrounding text is most of what makes a location useful.
2. [04-functional-specification.md](04-functional-specification.md) FR-PROJ-004 requires `feat doctor` to validate the agent executable, the container user identity, and `gh`/`glab` authentication "in the environment where Claude will use them". Nothing starts that environment before slice 8, so those checks cannot run in slice 3.
3. The daemon is the only writer of persistent state (ADR-008), enforced by an import boundary. Registering a project therefore cannot be a file the CLI writes. But `feat doctor` must run before a daemon exists, because [02-user-workflows.md](02-user-workflows.md) §1 puts diagnosis before registration.
4. An endpoint that carries a configuration file path would let a caller decide which file the daemon validates and records, and the file that was validated would not be the file the daemon reads again later.
5. `agent.capabilities` in [07-configuration-model.md](07-configuration-model.md) reads as a general vocabulary, but Feat has no mechanism that grants an agent Docker, restricts its network, or limits its Git access. Three of the five capabilities can only ever hold one value.
6. `docker compose config` renders the fully resolved project, including values taken from environment files, which `feat doctor` must never read.
7. `git.worktree_root` is the one configured path Feat later deletes from. Checking only what it expands to accepts `/var/{task_id}/work`, which places Feat's directories in a system location one placeholder deeper down.
8. The state directory is not the only home a configuration value can have. A `~` in project YAML has to expand against somebody's home directory, and the daemon and the client are not necessarily the same user.

Decisions:

- Use `github.com/goccy/go-yaml`, decoding with `yaml.Strict()`, which rejects both unknown fields and duplicate keys. It reports line, column, and an annotated excerpt, so the acceptance criterion is met by the decoder rather than by reconstruction. Semantic problems are located the same way, through `yaml.PathString(...).AnnotateSource(...)`, so every configuration problem can be shown in place.
- Loading is three stages — parse, resolve, validate — and validation reports every problem rather than the first, because a configuration file is edited by hand.
- `internal/config` never asks the host a question. Whether a path exists, holds a Git repository, or names a real Compose service is diagnostics, and lives in `internal/project`. That line is what keeps a configuration loadable on a machine where a repository is temporarily missing, which is the machine `feat doctor` is most useful on.
- A check this build cannot run is reported as `skipped`, and names the slice that delivers it. It is never reported as passing. The checks in evidence 2 are the first users of this: a diagnostic that claims a check it did not run is worse than no diagnostic.
- `feat doctor` runs in the client process, changes nothing, and needs neither a daemon nor a registration. It reports registration only when a daemon happens to be running, and starting one to answer a diagnostic question would make a command that changes nothing change something.
- `POST /v1/projects` carries a project identifier and nothing else. The daemon resolves the file from its own configuration directory, so no caller-supplied filesystem path crosses the socket. `feat project add <project>` therefore takes an identifier; the command surface changes from the argument-less `feat project add` recorded in slice 0, and the golden file was updated in the same change.
- `feat project add` requires a running daemon and reports exit code 4 when there is none, rather than starting one. Registration is an explicit mutation, and its behaviour should not depend on whether a terminal is attached; ADR-027 made the same distinction for a non-interactive `feat`.
- Registration is idempotent. Re-registering re-reads the configuration, updates the record, and keeps the original registration time, because a user who edits their YAML runs the command again. Tasks already running are unaffected: their configuration was resolved into a launch snapshot when they launched.
- `agent.capabilities.docker`, `.network`, and `.git` accept one value each — `denied`, `unrestricted`, and `full`. Recording another would be a promise the binary does not keep. The declaration is still worth making, because slice 8 checks the running container against it. `github_cli` and `gitlab_cli` keep the documented three levels.
- `feat doctor` runs `docker compose config --services` and never plain `docker compose config`. Environment files are examined by path and metadata only. Secret values never appear in diagnostics because they are never read, which is a property of the data rather than a filter over the output, and a test uses an unreadable environment file so that a future change cannot pass by accident.
- A path template is checked against its fixed leading directory as well as against what it expands to, and every template that names a per-task resource must contain `{task_id}` or `{task_key}`. Placeholder vocabularies are closed: an unknown placeholder is rejected rather than left to survive into a branch name, a path, or a command argument.
- Project configuration is resolved against the environment of the process that reads it: the daemon's own for registration, the client's for `feat doctor` and `feat project show`. `internal/daemon` gained a `paths.Environment` option so that this is explicit rather than ambient.
- The JSON Schema in `schema/feat-project.schema.json` is hand-written and kept in step with the Go types by a test that compares field names in both directions. Slice 14 finalises it. `docs/examples/project.yaml` is validated by the test suite, so the file a new user copies cannot drift from what Feat accepts.
- `POST /v1/projects/{project_id}/doctor` from [06-technical-architecture.md](06-technical-architecture.md) is deferred to the slice whose TUI reads it, for the reason ADR-027 deferred `daemon.json`: an endpoint with no reader is a compatibility surface with no user. `feat doctor` covers the command surface today.

Consequence: registering a project is the first write the local API carries, so the slice 2 acceptance criterion that the daemon is the only state writer — which ADR-027 recorded as structurally verified — is now checked behaviourally as well, by a test that registers through the socket and finds the snapshot in the daemon's state directory.

Slice 3 cannot verify its own second acceptance criterion, that the company project configuration validates on the target machine, because that needs the reference project and the machine it lives on. The criterion is verified by running `feat doctor` there, and slice 3 is not complete until that has been done.

The user-visible changes are the `<project>` argument on `feat project add`, and `feat doctor` exiting 1 when it finds an error. Package layout gained no new package. [07-configuration-model.md](07-configuration-model.md) and [06-technical-architecture.md](06-technical-architecture.md) were updated in the same change.

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

