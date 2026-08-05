# Implementation Plan

The plan uses ordered vertical slices. Every slice must leave the repository buildable and tested. Do not implement roadmap features while completing v0.1.

A slice carries a status line once work on it has started; a slice without one has not been started. A slice is complete only when every one of its acceptance criteria has been verified, not when its work items look done.

## Slice 0 — Repository bootstrap

Status: **complete**, 2026-08-04

### Outcome

A buildable Apache-2.0 Go project with CLI skeleton and documented development commands.

### Work

- Initialize Go module using the current supported Go version.
- Add Apache 2.0 license.
- Add Cobra root command and placeholders for `daemon`, `project`, `task`, `implement`, `runtime`, `doctor`, `attach`, `review`, and `cleanup`.
- Add Bubble Tea dependency but only a minimal health screen.
- Establish `internal` package boundaries from the architecture document.
- Add formatting, lint, unit-test, and build commands.
- Add CI for macOS/Linux builds and tests if the repository host is ready.

### Acceptance criteria

- `go test ./...` passes.
- `go build ./cmd/feat` produces one binary.
- `feat --help` shows the intended top-level command model.
- No project-specific path or service name is compiled into the binary.

### Delivered

All four acceptance criteria pass on macOS with Go 1.26. `make check` runs the tidy, format, lint, test, and build sequence.

Every command in the v0 surface is registered and returns a typed error naming the slice that delivers it, so no subcommand can report success it has not earned. The surface itself is pinned by a golden file.

Three rules from `CLAUDE.md` are enforced mechanically rather than by review attention, and each was verified to fail against an injected violation:

- import boundaries, as `depguard` rules in `.golangci.yml`; a change to those rules is an architectural change;
- the prohibition on hard-coded reference-project identifiers, as an AST test over every Go string literal, with exemptions recorded in a reviewable denylist rather than in `//nolint` comments;
- the argument-vector rule, as an AST test over every `exec.Command` call.

Package layout gained `internal/cli`, `internal/paths`, `internal/version`, and `internal/guard`. See ADR-025 in [10-decisions-and-open-questions.md](10-decisions-and-open-questions.md); the package list in [06-technical-architecture.md](06-technical-architecture.md) was updated in the same change.

CI builds, vets, tests, and lints on macOS and Linux, and pins the linter version through `.golangci-version`. Verified green on the slice 0 pull request.

## Slice 1 — Domain and file storage

Status: **complete**, 2026-08-04

### Outcome

Versioned project/task aggregates can be created, transitioned, persisted, and recovered without external tools.

### Work

- Implement domain IDs, entities, states, invariants, and transition errors.
- Implement storage interfaces.
- Implement versioned JSON snapshots, Markdown prompt storage, and JSONL events.
- Implement atomic file replacement and serialized writes.
- Tolerate incomplete final JSONL record.
- Add deterministic fixtures and migration placeholder.

### Acceptance criteria

- Invalid state transitions fail with typed errors.
- A task with multiple repository bindings round-trips exactly.
- Crash simulation never leaves a partially replaced snapshot as current.
- Event replay ignores only an incomplete final line.
- Storage code contains no daemon/TUI dependencies.

### Delivered

All five acceptance criteria pass, each with a test named for it.

`internal/domain` holds the entities, the four state dimensions, the invariants, and three error classes (`ErrInvalid`, `ErrInvalidTransition`, `ErrInvariant`), each reachable through `errors.Is` with the details through `errors.As`. It imports only the standard library.

The workflow transition table is pinned against the documented lifecycle by a test that also proves the shape a "Stop means complete" defect would take: no state reaches `ready_for_review` or `approved` without passing through an explicit review request. Transitions additionally check the preconditions of the target state, so the error says what is missing rather than that something is.

`internal/store` defines the interfaces and the storage error classes. `internal/store/fs` implements them over the layout in [06-technical-architecture.md](06-technical-architecture.md), with per-record write serialization, atomic replacement, and validation of every aggregate on the way in and on the way out.

Three properties are checked directly rather than by inspection:

- crash simulation interrupts an atomic replacement at each of its four points and asserts which snapshot is current afterwards, and a second test runs readers against a writer to catch a replacement that wrote in place;
- an event log is read back under nine different shapes of damage, of which exactly one — an unterminated final record — is ignored rather than reported, and appending afterwards discards the partial record instead of joining it to the next one;
- the round-trip test is backed by a reflection check that the fixtures leave no persisted field at its zero value, so a field the mapping forgets cannot round-trip by being absent from both sides.

The migration mechanism carries no migrations, because there is one version of each document. It is exercised with a synthetic schema change instead, since the first real migration should not be the first one ever run. Golden files pin the stored format, so changing it fails in `go test` where the fix is a schema version and a migration.

Storage independence is enforced by the `storage-stays-independent` and `domain-stays-provider-neutral` `depguard` rules that slice 0 installed.

Package layout gained `internal/store/storetest`. See ADR-026 in [10-decisions-and-open-questions.md](10-decisions-and-open-questions.md); [03-domain-model.md](03-domain-model.md) and [06-technical-architecture.md](06-technical-architecture.md) were updated in the same change.

## Slice 2 — Daemon and local API

Status: **complete**, 2026-08-04

The design decisions this slice started from are recorded in ADR-027 in [10-decisions-and-open-questions.md](10-decisions-and-open-questions.md), including which two acceptance criteria this slice can only verify structurally, and why.

### Outcome

The binary can start a background daemon and clients can query health/state over a protected Unix socket.

### Work

- Implement daemon lifecycle, PID/socket ownership, stale socket recovery, and logging.
- Implement HTTP/JSON server and client over Unix socket.
- Add `/v1/health`, project/task list/get, and `/v1/events` SSE.
- Make TUI auto-start the daemon.
- Ensure daemon is the only state writer.

### Acceptance criteria

- Two client processes can query the daemon concurrently.
- Socket permissions restrict access to the current user.
- A stale socket is diagnosed and safely recovered.
- State events arrive through SSE in order.
- No TCP listener is opened.

### Delivered

All five acceptance criteria pass, each with a test named for it. Two of them are verified structurally rather than behaviourally, as ADR-027 records in advance: slice 2 has no writer of persistent state and no publisher of domain events, because the endpoints that create something to change arrive with slices 3 and 6.

- Two clients querying concurrently is checked twice: in process, with overlapping reads from two clients over one socket, and as two operating-system processes in an opt-in test that builds the binary and runs `feat daemon start`, two concurrent clients, and `feat daemon stop`. CI runs the opt-in test on both platforms.
- Socket permissions are asserted on the socket, the lock, the endpoint record, and the runtime directory, because a private socket inside a world-writable directory is not private.
- Stale-socket recovery is checked from both sides: a socket left behind by a daemon that is gone is reclaimed and the reclaim is logged, and a socket that still answers while the lock is free makes the second daemon refuse rather than disconnect the first one's clients.
- Event order is checked through the real bus, the real SSE endpoint, a real socket, and the real client parser, with two subscribers receiving the same 40 events in the same order. Only the publisher is a fixture.
- The absence of a TCP listener is an AST test over every Go file in the repository, tests included, rather than an observation of one code path.

`internal/paths`, which ADR-025 left to whichever of slice 2 and 3 needed it first, resolves the configuration, state, and runtime directories from an explicitly passed environment. It creates nothing, which is checked, so a command that only prints a path leaves nothing behind. It rejects a socket path longer than the platform allows, which is otherwise reported by `bind` as "invalid argument", and refuses to expand another user's home directory.

`internal/daemon` owns the process. Ownership is an advisory lock the kernel releases on death, plus a connect probe, so the three questions a PID file cannot answer are answered separately: whether a daemon is alive, whether a socket is stale, and whether something else is serving the path. It also validates the runtime directory before binding, because the fallback location is shared with other users.

`internal/api` serves the wire types, and they are a third representation, separate from the domain and from the stored documents; golden files pin every response body. `internal/client` is transport only. The stream reports its own health: it opens with a hello, answers a resume attempt with an explicit resync rather than a pretended replay, and ends a subscriber that fell behind with a `stream_lost` item, so lost events are reported instead of silently missing.

`internal/cli` gains `daemon start`, `stop`, `status`, and a hidden `run`, plus exit code 4 for "no daemon is running". Two rules became mechanical, and each was verified to fail against an injected violation: no TCP listener anywhere in the repository, and only the daemon imports storage.

Four findings changed the design during implementation:

- an override of the socket path alone would separate the socket from the lock that proves who owns it, so `FEAT_RUNTIME_DIR` moves the whole directory instead; ADR-027 was amended in the same change;
- graceful shutdown had to end event streams first and then close whatever had not drained, because net/http leaves an unused connection alone for several seconds and every shutdown would otherwise wait for it;
- `feat` without a terminal reports the daemon's absence rather than starting one, so printing a summary in a pipe or in CI does not leave a background process behind;
- a spawned daemon refuses to spawn another. The test suite found this by invoking the foreground command's own handler, which turned one process into a growing tree of them.

Package layout gained no new package. `internal/paths`, `internal/daemon`, `internal/api`, and `internal/client` were all reserved by earlier slices. See ADR-027; [06-technical-architecture.md](06-technical-architecture.md) and [README.md](README.md) were updated in the same change.

## Slice 3 — YAML project configuration and doctor

Status: **awaiting verification on the target machine**, 2026-08-05

Four of the five acceptance criteria pass, each with a test named for it. The second — that the company project configuration validates on the target machine — needs the reference project and the machine it lives on, so it is verified by running `feat doctor` there rather than in this repository. The design decisions this slice started from, and the evidence that produced them, are recorded in ADR-028 in [10-decisions-and-open-questions.md](10-decisions-and-open-questions.md).

### Outcome

The real project can be represented in YAML and validated without launching work.

### Work

- Implement strict YAML structs and validation.
- Expand `~` and resolve paths safely.
- Validate multi-repository topology, primary workspace, base policies, branch/worktree templates, commands, Compose inputs, and capabilities.
- Implement `feat project add/list/show` using manual YAML.
- Implement `feat doctor` host checks.
- Add resolved-configuration output with redaction.
- Draft JSON Schema for editor support.

### Acceptance criteria

- Unknown YAML fields fail with a useful location/message.
- The company project config validates on the target machine.
- Missing tools/files/services produce actionable diagnostics.
- Secret file contents never appear in diagnostics.
- Repository/container-path mappings are printed accurately.

### Delivered

`internal/config` loads configuration in three stages, because they fail for different reasons and are fixed in different ways: `Parse` decodes strictly, `Resolve` expands `~` and fills defaults into the configuration itself, and `Validate` reports every rule the result breaks rather than the first. A configuration file is edited by hand, so finding four mistakes one round trip at a time is four times the work of seeing them together.

Unknown fields and duplicate keys are rejected with a line, a column, and the surrounding text, which the decoder already knows; the same mechanism locates semantic problems, so `agent.execution.user must not be root` is printed against the line that says `user: root`. The decoder is `goccy/go-yaml`, chosen for that and for being maintained; see ADR-028.

The package asks the host no questions. Whether a path exists, holds a Git repository, or names a real Compose service is diagnostics, and lives in `internal/project`. That line is what keeps a configuration loadable on a machine where a repository is temporarily missing, which is the machine `feat doctor` is most useful on.

Four validation rules protect resources rather than shape:

- every template that names a per-task resource must contain `{task_id}` or `{task_key}`, or two tasks share a branch, a worktree, or a Compose project;
- placeholder vocabularies are closed, so a name Feat does not expand cannot survive into a branch name, a path, or a command argument, and an argument-vector's program may not be templated at all;
- `git.worktree_root` is checked against its fixed leading directory as well as its expansion, because Feat creates and removes worktrees under that directory and `/var/{task_id}/work` is a system location one placeholder deeper down;
- configuration that a mode would silently ignore is rejected rather than ignored, so a user who configured a service and a non-root user is never left believing their agent is in a container when the mode says it is not.

`feat doctor` runs in the client process and changes nothing: no daemon, no registration, and no state, which a test asserts. Findings carry a severity and an action, and every problem must name one. A check this build cannot run is reported as `skipped` and names the slice that delivers it — the checks FR-PROJ-004 asks for inside the agent's execution environment are the first users of that, because nothing starts that environment before slice 8. A diagnostic that claims a check it did not run is worse than no diagnostic.

Secret file contents never appear because they are never read. Environment files are examined by path and metadata, and the only Compose command used is `config --services`; plain `docker compose config` renders the resolved project including values taken from those files, and a test fails if it is ever invoked. The acceptance test uses an environment file with mode `000`, so an implementation that opened it could not pass by accident.

Registering a project is the first write the local API carries. `POST /v1/projects` takes an identifier and nothing else, and the daemon resolves the file from its own configuration directory, so no caller-supplied path crosses the socket. That makes slice 2's structurally verified sole-writer criterion behavioural: a test registers through the socket and finds the snapshot in the daemon's state directory. Registration is idempotent, keeps the original registration time, and leaves nothing behind when the configuration is rejected.

`feat project add <project>` changes the command surface slice 0 pinned, and `feat doctor` exits 1 when it finds an error. Both are recorded in ADR-028 and in the golden file. `schema/feat-project.schema.json` is hand-written and held to the Go types by a drift test that checks both directions, verified against an injected violation each way; `docs/examples/project.yaml` is validated by the test suite, so the file a new user copies cannot drift from what Feat accepts.

Package layout gained no new package: `internal/config` and `internal/project` were reserved by slice 0. See ADR-028; [06-technical-architecture.md](06-technical-architecture.md), [07-configuration-model.md](07-configuration-model.md), and [README.md](../README.md) were updated in the same change.

## Slice 4 — Git and worktree lifecycle

### Outcome

A confirmed task draft creates correct coordinated branches/worktrees across selected repositories.

### Work

- Implement Git command runner using argument vectors.
- Implement fetch without pull.
- Resolve base policies to commits.
- Generate branch/worktree names and detect collisions.
- Create read-write and read-only task worktrees.
- Record immutable base commits.
- Observe dirty/ahead/behind/merged state.
- Generate cleanup plans without executing them yet.

### Acceptance criteria

- Dirty changes in the ordinary checkout are preserved and do not block an independent task.
- Remote base resolution uses the fetched remote-tracking commit.
- A two-repository task receives the correct branch/worktree mapping.
- Read-only and read-write selections are recorded correctly.
- Failure halfway through creation leaves a recoverable record and no unidentified worktree.
- Tests cover unsafe/broad path rejection.

## Slice 5 — tmux execution backend

### Outcome

Feat can create, discover, attach to, and reconcile persistent task terminals.

### Work

- Start/use a dedicated tmux server/socket.
- Permit normal user configuration, then apply minimal Feat metadata.
- Create project sessions and task windows.
- Tag objects with stable project/task IDs.
- Launch placeholder commands in agent pane.
- Add on-demand shell pane.
- Implement attach-info and reconciliation.

### Acceptance criteria

- Managed sessions do not collide with ordinary tmux sessions.
- Custom user window indexes/names do not break identity.
- Detaching returns cleanly to the caller.
- Daemon restart rediscovers existing tagged sessions/windows.
- Shell pane opens in the configured primary workspace.

## Slice 6 — Task preparation and initial TUI

### Outcome

The user can prepare, confirm, launch, list, and inspect local/Markdown tasks from the dashboard.

### Work

- Implement task-draft API and lifecycle.
- Add prompt and Markdown import.
- Add repository access selection.
- Show resolved bases, proposed branches/worktrees, execution/runtime profile, and editable brief.
- Require explicit confirmation before launch.
- Implement global task list and task detail.
- Connect attach/shell actions.

### Acceptance criteria

- Cancelling a draft creates no worktrees, tmux windows, or containers.
- Confirming launches the previously displayed snapshot.
- Task list contains required v0 fields except integrations not yet implemented.
- Several task drafts and live tasks can coexist.

## Slice 7 — Control workspace and Claude adapter

### Outcome

Native Claude Code runs per task and Feat receives structured lifecycle events.

### Work

- Implement task control-workspace creation and mounts/specification.
- Implement versioned inbox/outbox event schemas, atomic writes, validation, deduplication, and size limits.
- Implement Claude validation/launch preparation.
- Generate provider-specific settings/instructions outside repositories.
- Integrate supported Claude hooks for session start, prompt submit, Stop/idle, completion/failure, and notification where available.
- Normalize events into domain state.
- Add idle grace period.
- Ensure Stop alone never means review completion.
- Validate optional/required `gh`/`glab` inside execution environment.

### Acceptance criteria

- Claude launches in the correct task working directory with the final brief.
- A normal end of turn becomes idle only after the grace period.
- A submitted revision prompt changes review state conservatively.
- Duplicate/malformed/out-of-task control messages do not execute or duplicate transitions.
- Review request is explicit and distinguishable from idle.
- Missing required GitLab authentication prevents launch with an actionable message.

## Slice 8 — Devcontainer execution

### Outcome

Claude runs inside the real non-root devcontainer with correct multi-repository mounts and no Docker access.

### Work

- Implement Compose-backed execution environment separate from application runtime.
- Generate task mount override from project container paths.
- Mount control workspace.
- Integrate dedicated Claude configuration volume.
- Start the configured dev service as the configured user.
- Launch Claude and shell panes inside the service.
- Validate no configured Docker socket/CLI capability for the company profile.
- Observe execution-container state.

### Acceptance criteria

- `dashboard` and `database` task worktrees appear at expected paths/access modes.
- stable devcontainer code is read-only.
- Claude process UID is non-root.
- Docker socket is absent and host Docker cannot be controlled from the agent.
- Full Git and required `glab` access work.
- Three task devcontainers and Claude sessions can run concurrently.

## Slice 9 — Manual application runtime

### Outcome

The user can manage the selected task's application services from Feat without granting Claude Docker access.

### Work

- Implement application Compose runtime adapter.
- Retain exact base/static/generated override/env-file inputs.
- Use unique Compose project identity and ownership labels.
- Implement create/start/stop/status/logs-info/destroy.
- Surface Compose health or unknown health.
- Record external staging database binding without lifecycle ownership.
- Add TUI runtime actions.

### Acceptance criteria

- Starting/stopping task A does not affect task B.
- Logs open through normal Compose output.
- External database resources are never included in destroy plans.
- Runtime remains running during review unless the user stops it.
- Approval offers stop but does not execute it automatically.

## Slice 10 — Notifications and resources

### Outcome

The user can decide when to start more work and is notified when attention may be useful.

### Work

- Implement whole-machine CPU/memory/disk observation.
- Aggregate managed process/container usage per task.
- Add dashboard resource card and per-task totals.
- Implement TUI badges and macOS desktop notifications.
- Suppress idle notification while attached and apply grace period.

### Acceptance criteria

- Metrics remain observational and never block task creation.
- Resource collection failure degrades gracefully.
- Idle notifications do not fire immediately or while attached.
- Review/failure notifications identify the task without exposing secrets.

## Slice 11 — Review and external commands

### Outcome

The user can review every changed repository against the correct immutable base using familiar tools.

### Work

- Compute per-repository change summaries.
- Add grouped review view.
- Expand/execute configured diff, editor, and status commands.
- Add approve/pending/revise actions.
- Preserve review state across restart.

### Acceptance criteria

- Each repository uses its own recorded base commit.
- Neovim opens in the selected task repository.
- Commands cannot escape configured task paths through unvalidated placeholders.
- Approval does not stop or destroy runtime automatically.

## Slice 12 — Reconciliation and cleanup

### Outcome

Feat recovers honestly and removes only resources the user explicitly selected.

### Work

- Reconcile snapshots, tmux, worktrees, Compose, control messages, and review state.
- Write and reconcile the durable daemon record `daemon.json`, deferred from slice 2 because this is the first slice that reads one (ADR-027).
- Report missing/orphaned/inconsistent resources.
- Build cleanup-plan API with stable token/IDs.
- Separate containers/networks, volumes, worktrees, and branches.
- Implement dirty/unpushed/unmerged warnings.
- Archive task metadata.

### Acceptance criteria

- Daemon/computer restart loses no task identity.
- Stopped containers are not restarted.
- Orphan resources are reported before adoption/removal.
- Volumes remain unless explicitly chosen.
- Broad path or non-task resource deletion is rejected.
- Dirty/unmerged resources require explicit confirmation.

## Slice 13 — Dogfood hardening

### Outcome

v0.1 meets every acceptance criterion in [08-v0-scope.md](08-v0-scope.md).

### Work

- Run repeated three-task workflows on the reference project.
- Capture setup/recovery/cleanup failures as regression tests.
- Improve error messages and `doctor` output.
- Document known security limitations.
- Measure manual coordination removed and false idle notifications.
- Remove hard-coded assumptions discovered during dogfood.

### Acceptance criteria

- Full v0.1 acceptance checklist passes.
- No unresolved data-loss or cross-task runtime defect remains.
- A clean installation can reproduce the dogfood setup from documentation.

## Slice 14 — Public v0.2

### Outcome

A new macOS/Linux user can use Feat outside the reference project.

### Work

- Implement host-native execution.
- Add Linux notification support.
- Generalize examples and troubleshooting.
- Finalize JSON Schema and shell completion.
- Add release binaries, Homebrew formula/tap, and `go install` instructions.
- Add contribution/security policy.
- Verify no telemetry.
- Add onboarding wizard only if manual configuration is the dominant public blocker.
- Add Shortcut only if all core reliability work is complete.

### Acceptance criteria

- Public-v0 definition of done passes on macOS and Linux.
- Host-native and devcontainer modes use the same task domain.
- Installation and first-task documentation is reproducible.

## Implementation discipline

- Keep changes within the current slice unless a prerequisite is proven missing.
- Prefer fake adapters and deterministic integration harnesses before requiring real Docker/tmux in every unit test.
- Add opt-in end-to-end tests for real Git, tmux, Docker Compose, and Claude.
- Never expose Docker to the agent to simplify implementation.
- Never replace structured agent events with terminal scraping as the semantic source of truth.
- Update the decision log when evidence changes an accepted design.

