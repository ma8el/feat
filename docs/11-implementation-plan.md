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

Status: **complete**, 2026-08-06

All five acceptance criteria pass, each with a test named for it. The second — that the company project configuration validates on the target machine — needed the reference project and the machine it lives on, and was verified during slice 8 by running `feat doctor` there: both of that machine's projects validate, and the devcontainer one now describes a container that exists. The design decisions this slice started from, and the evidence that produced them, are recorded in ADR-028 in [10-decisions-and-open-questions.md](10-decisions-and-open-questions.md).

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

Status: **complete**, 2026-08-05

The design decisions this slice started from are recorded in ADR-029 in [10-decisions-and-open-questions.md](10-decisions-and-open-questions.md).

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

### Delivered

All six acceptance criteria pass, each with a test named for it. Four of them are about Git's own behaviour rather than about Feat's, so they are verified against real repositories in opt-in tests named `TestReal…`; CI runs them on macOS and Linux. The unit tests use a fake runner and pin the argument vectors, because a fake can prove that a value lands in one element of a vector and cannot prove that a flag exists.

`internal/git` is the adapter. It imports the standard library, `internal/domain`, and `internal/paths`, and a new `git-stays-an-adapter` `depguard` rule denies it configuration and storage: it takes final names, because the placeholder vocabulary belongs to `internal/config`, and it writes nothing, because the daemon does. `internal/config` gained `Expand`, `Values`, `Uses`, `Slug`, and `StaticPrefix` to keep that vocabulary in one place.

Task preparation is plan, record, apply, and the order is the recoverability criterion rather than an implementation detail. `daemon.PrepareTask` resolves every base to an immutable commit and proposes every branch and path without creating anything, writes that plan onto the task and leaves `draft`, and only then creates one repository at a time, recording an observation of each before the next begins. A test asserts the ordering from inside the creation itself: when Git is asked to make a worktree, the snapshot on disk must already name it. So a failure at any point leaves a record naming a superset of what exists, which a second test checks against the state a restarted daemon would read, and a third checks against real Git by requiring every directory under the task root and every worktree Git has registered to be one the record names.

Nothing is undone when a launch fails half way through. A worktree that exists may already have been entered or written to, and tidying up a failed launch is a destructive act the user did not ask for; the task becomes `failed`, which the workflow can resume from.

Whether a worktree exists is observed rather than stored, so the stored format is unchanged and no migration was needed: the recorded branch and path are desired state, and a `GitObservation` is what Feat saw. A repository with no observation is one nothing has confirmed.

Four properties are checked directly rather than by inspection:

- a dirty ordinary checkout is compared byte for byte — porcelain status, HEAD, the checked-out branch, and the stash list — before and after a task is prepared beside it, because a stray `pull`, `stash`, or `checkout` would show up in exactly one of them;
- remote base resolution is checked where the three candidate commits differ: what the checkout has, what its remote-tracking ref had before the fetch, and what a second clone pushed. The recorded base must be the third, and the user's own branch must not have moved;
- unsafe paths are a table of nine, each a directory Feat would later remove, and every one is rejected before any Git command runs. The same check refuses a recorded path during cleanup planning, because a record that decides what gets deleted has stopped being a record;
- a name that Git would read as an option — `--upload-pack=…` in place of a remote — is refused rather than passed, which the argument-vector rule does not cover on its own.

Slice 4 adds no endpoint, no command, and no TUI: the command surface and its golden file are unchanged, and slice 6 confirms a draft by calling `PrepareTask`. Preparation is, however, the first production code that appends to a task's event log and publishes to the event stream, which makes part of slice 2's structurally verified criterion behavioural.

Package layout gained no new package: `internal/git` was reserved by slice 0. See ADR-029; [06-technical-architecture.md](06-technical-architecture.md) was updated in the same change.

## Slice 5 — tmux execution backend

Status: **complete**, 2026-08-05

The design decisions this slice started from are recorded in ADR-030 in
[10-decisions-and-open-questions.md](10-decisions-and-open-questions.md).

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

### Delivered

All five acceptance criteria pass against tmux 3.7b on macOS, under the race detector. The opt-in `TestReal…` suite creates both an ordinary tmux server and the Feat-owned server, loads a user configuration with non-default window and pane indexes plus automatic rename, changes display names and indexes after creation, adds an untagged user pane, and still rediscovers the same `$session`, `@window`, and `%pane` IDs. CI runs the same real-tool suite on macOS and Linux and now checks that tmux is present rather than silently skipping these criteria.

`internal/tmux` is a terminal-persistence adapter, not a second execution-environment adapter. It accepts a validated program vector and absolute working directory, always supplies the dedicated `<runtime>/tmux.sock`, permits normal user configuration, and applies only Feat ownership/persistence options. A new `tmux-stays-an-adapter` depguard rule limits it to the standard library and `internal/domain`; configuration, storage, Compose, Claude, and host/devcontainer command construction remain outside it.

Sessions, windows, and panes carry versioned `@feat_*` metadata at their own scopes. Display names and indexes are never read as identity. Metadata becomes discoverable only after the persistence options are in place, and a returned setup failure removes only the exact new pane/window/session. Once a target is fully tagged it is retained on a later failure so daemon startup can recover it; an interrupted partial session cannot block retry through a display-name collision because display names are left to tmux.

Panes are created without a command and tagged before the caller's program replaces the holder shell, so `remain-on-exit` and ownership are in place before that program can exit. `TestRealCommandThatExitsImmediatelyStaysObservable` starts `/usr/bin/false` as both the agent and the shell command and requires a live terminal reporting a failed process with exit status 1; against the earlier order the same test fails with the server or the pane already gone. The review evidence and the retention rule this narrows are recorded in the ADR-030 amendment.

The daemon creates or finds task terminals, records stable targets and observed process state, and reconciles the dedicated server before it accepts API requests. Reconciliation repairs stale stored IDs from live metadata, records a reconciliation event, marks a missing recorded pane stopped without inventing a restart, and reports conflicting or orphaned tagged resources rather than guessing. `TestRealDaemonRestartRediscoversTaggedTerminal` corrupts the stored target, constructs a fresh daemon instance, and proves the live target is restored. `internal/tmux/tmuxtest` supplies a fake tmux server so the same orchestration runs without tmux installed: the ordinary path, a refused creation failing the task explainably, a repaired stale target, a missing terminal marked stopped without any restart command being issued, a conflicting project reported rather than adopted, and an orphaned terminal reported without failing recovery. Those branches decide whether a half-finished lifecycle is recoverable, so they run in the default `go test ./...` rather than only under `FEAT_INTEGRATION`.

`POST /v1/tasks/{task_id}/attach-info` resolves the target from live metadata. `feat attach <task>` calls that endpoint and then runs the native tmux client with the caller's streams; it removes only the outer `TMUX`/`TMUX_PANE` identity so attachment also works from an ordinary tmux session. A real control-mode client verifies that native detach returns cleanly. The API response has a golden test, and the CLI validates every returned stable ID before starting tmux.

The on-demand shell seam is implemented and idempotent. It takes its command and primary workspace from the caller, creates at most one tagged shell pane, and leaves the execution-profile decision to slices 6 and 8. The real test reads tmux's observed pane path and proves it is the supplied primary workspace. Slice 6 connects this seam to the user-facing shell action when task launch gains its production caller.

Package layout gained no new package: `internal/tmux` was reserved by slice 0. See ADR-030; [06-technical-architecture.md](06-technical-architecture.md), [10-decisions-and-open-questions.md](10-decisions-and-open-questions.md), and [README.md](../README.md) were updated in the same change.

## Slice 6 — Task preparation and initial TUI

Status: **complete**, 2026-08-06

The design decisions this slice started from are recorded in ADR-031 in
[10-decisions-and-open-questions.md](10-decisions-and-open-questions.md).

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

### Delivered

All four acceptance criteria pass, each with a test named for it, and each is checked at the adapter rather than at the outcome where it can be: that no Git command created a worktree and no tmux command ran is a stronger statement than that no directory exists.

Task preparation is plan, confirm, apply. ADR-029 recorded that slice 6 would confirm a draft by calling `PrepareTask`, which resolves and creates in one step; that turned out to break the second acceptance criterion silently, because a `remote` base policy fetches during planning and a fetch between the screen the user reads and the key they press moves the commit the task starts from. `POST /v1/task-drafts/{id}/plan` now records the proposal on the draft and leaves it a draft, and `POST /v1/task-drafts/{id}/launch` carries a fingerprint of what was displayed and refuses anything else. The fingerprint is computed from the stored task rather than kept beside it, so the stored format is unchanged and no migration was needed. `TestConfirmingLaunchesTheDisplayedSnapshot` moves the remote between the two calls and requires the worktrees to be created at the commits that were shown.

A draft is a task in `draft` state, which the domain already modelled, so `{draft_id}` is a task identifier and drafts survive a daemon restart and appear in the task list as the drafts they are. Cancelling one archives the record: nothing was created for a draft, and the record explains what the user started and decided against. Two endpoints beyond the documented list carry this — the plan and the cancellation — and ADR-031 records why each is its own request.

Editing a draft keeps what was resolved for a repository whose selection did not change, and discards it for one that was added, dropped, or given different access. Resolving fetches, so making a user re-resolve every repository because they fixed a typo in the title would put a network call behind an edit that changed nothing about where the task starts. Editing the brief therefore leaves the plan intact and still changes the fingerprint, which is the case the fingerprint exists for: the brief is what the agent receives, every commit is unchanged, and nothing else would notice.

Slice 6 starts no agent. The Claude adapter arrives with slice 7 and the devcontainer with slice 8, so launch opens the task terminal with the user's `$SHELL` in the primary task worktree and the task rests in `preparing`. `preparing` to `working` is the edge slice 7 takes when Claude actually starts, so no task reports a running agent session that is really a shell. The dashboard and the task detail say so in words rather than by omission, and the two required task-row fields no slice has delivered — verification state and resource usage — render as absent and name the slice that fills them, which is the rule ADR-028 established for `feat doctor`.

`POST /v1/tasks/{task_id}/shell` takes a task identifier and nothing to execute: the daemon builds the command, because a program a caller chose would be a program the daemon runs on its owner's behalf. A test sends a body naming one and requires it to be refused rather than ignored. The same check now covers `attach-info`.

`internal/ui` gained the dashboard, the task detail, and the preparation screen behind a `Backend` interface, so every screen's behaviour is tested against a fake without a socket, a daemon, or tmux. The three things the TUI has to launch — native tmux attach, a task shell, and `$EDITOR` — are `tea.ExecCommand` values built in `internal/cli`, so the TUI names no `os/exec` type and the rule that keeps process execution in adapters stays mechanical. `github.com/charmbracelet/bubbles` was added for the text input and text area, which [06-technical-architecture.md](06-technical-architecture.md) already anticipated.

The dashboard subscribes to the daemon's event stream and re-reads state on every event rather than applying the event itself, because the stream reports what changed and the snapshot is what it changed to; deriving one from the other would give the dashboard a second, divergent copy. A periodic read is the backstop for a stream that ended, which v0.1 answers by re-reading rather than resuming (ADR-027). This makes slice 2's structurally verified event-ordering criterion fully behavioural: a draft created, resolved, launched, and cancelled publishes the sequence a client reads.

**Amended during slice 7's end-to-end verification.** As delivered, the dashboard opened a *new* stream for each event instead of holding one open. Because the daemon opens every stream with a `hello` so a client learns the connection is live before anything happens, the answer to a connection was an event and the answer to an event was another connection: a self-sustaining loop running at the speed of the socket, leaking a connection, a goroutine, and a subscriber on each side every time round. It exhausted the machine's file descriptors within a minute — 169,286 streams in six minutes, a 53 MB daemon log — and then surfaced as unrelated-looking failures, including a task launch refused because a healthy repository was reported as not being a Git repository. The dashboard now opens one stream and reads it repeatedly; three tests pin it, the first of which fails against the original behaviour. Two lessons are recorded rather than implied: the fake event stream in the suite ended immediately, so a reconnect produced no event to reconnect for and the loop was invisible to every test; and `internal/git` now refuses to report a command that never ran as a verdict on the user's checkout, because "not a Git repository" sent the investigation to the wrong machine entirely.

`feat implement` gains `--project`, which changes the command surface slice 0 pinned; the golden file, [README.md](README.md), and ADR-031 moved in the same change. The flag pre-fills rather than making the command headless: confirmation is required before anything is created, so a terminal is required, and preparation reports an absent daemon rather than starting one. An imported Markdown brief is read by the client and sent as content, so no caller-supplied filesystem path crosses the socket.

Package layout gained no new package. See ADR-031; [06-technical-architecture.md](06-technical-architecture.md) and [README.md](README.md) were updated in the same change.

## Slice 7 — Control workspace and Claude adapter

Status: **complete**, 2026-08-06

The design decisions this slice started from are recorded in ADR-032 in
[10-decisions-and-open-questions.md](10-decisions-and-open-questions.md),
together with the amendment four defects found by running a real task produced.

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

### Delivered

All six acceptance criteria pass, each with a test named for it, and each split between the layer that can prove the narrow claim and the layer that can prove the broad one: that no tmux command ran is a stronger statement than that no terminal exists, and that a table has no entry is a stronger statement than that one code path did not take it.

Feat launches a native Claude Code session per task, receives its lifecycle through a file protocol, and normalizes it into the domain's four state dimensions. The whole pipeline was run against the real CLI: a task reaching `working` from a session start, `idle` only after the configured grace, `review_requested` from the generated report helper, and agent-attributed check results on the dashboard.

`internal/control` is the protocol and knows no provider. The workspace is split by who writes to each part — `task.md`, `context/`, and `inbox/` host-written; `outbox/` and `reports/` agent-written; `agent/` host-only — so that slice 8 can mount the parts differently and so that recording a message as processed never requires the host to write into the directory the agent owns. Every rule the security model lists is checked before a message can change anything, and each has its own test: schema version, task ownership, event identity, type, size, path, and required capability. A `runtime_requested` message is recognised, recorded, and inert, which is what makes "capability validation" a claim about something that was refused rather than about nothing having been asked.

Delivery is polling. Filesystem notification does not cross a bind mount reliably on every supported platform, and a watcher that worked on the host while silently never firing in a container would hide the failure in the slice 8 configuration that matters most. A document that does not parse is retried before it is called malformed, because a write in progress and a malformed document look identical to a reader and only one of them is the agent's mistake.

`internal/agent` carries the neutral vocabulary and the seam that slice 8 replaces: an adapter is told how the agent will see its own filesystem and answers with a launch in those terms. `internal/agent/claude` holds every Claude-specific decision. Its flags and hook schemas were read out of the installed 2.1.220 binary rather than assumed, the adapter records the version it was checked against, and `feat doctor` warns when the installed one is outside that range — because the failure mode of a changed hook schema is a session that runs perfectly well and never reports again, and silence deserves a diagnosis rather than a mystery.

The generated hooks are almost empty on purpose: each copies its payload into the outbox and exits, because parsing belongs in code that can be tested and because Claude gives a hook's standard output and exit status meaning. A `UserPromptSubmit` hook's standard output is injected into the model's context and a `Stop` hook exiting 2 blocks the agent, so a hook that printed or failed would change the session it exists to observe. Both properties are pinned by a test that runs the generated scripts under a real shell and requires the messages they produce to parse back into the events the daemon acts on.

The claim that a Stop event never means completion is mechanical rather than aspirational. Normalization is a table, pinned by a test in the shape ADR-026 used for the workflow transition table, so the defect has to be introduced by editing the table that documents it. Its most important row is an absence: no end-of-turn path reaches a review state, and the one entry that does exists because an agent authored a message saying so.

Four defects were found by running a task rather than by reasoning about one, and each is now a test:

- a launched agent can be blocked before it emits anything, because Claude asks for workspace trust on every new task worktree and no hook fires while it waits. Feat showed a live process with no attention — a task that looked busy while nothing was happening. A launched agent that has not reported starting now says so, conservatively;
- the brief is outside the working directory, so every launch began by asking permission to read the document Feat wrote for it. The launch now grants tool access to the task's own control workspace and nothing else;
- attention reached `needs_input` from a permission prompt and never left, because nothing cleared it. The end of a turn now does, since a turn cannot end while the agent is blocked on a dialog;
- the flag granting that access takes a list, and it sat immediately before the prompt, so the prompt was read as a second directory. The session started, installed its hooks, reported that it had started, and then waited for a task it had never been given — with every signal Feat observes reporting success. That is the failure this slice is least able to see on its own, and it is why the opt-in real-CLI suite asserts which events a session produced rather than only that it ran.

Running the slice end to end also found a defect that was not this slice's: the dashboard reconnected to the event stream on every event and exhausted the machine's file descriptors. It is described and fixed in slice 6's record above, where the behaviour it broke is described. It is worth noting here because of how it presented — the visible symptoms were a task launch failing with "the daemon could not complete the request" and a shell that could no longer open a pipe, neither of which points at a terminal user interface. A slice is verified by running it, and what running it finds is not always in the slice.

`feat doctor` stops skipping the agent-executable and provider-CLI checks for a host-mode project, because FR-PROJ-004 words them around the environment the agent runs in and for host execution that environment is this machine. A devcontainer project keeps the skipped findings and keeps naming slice 8. [07-configuration-model.md](07-configuration-model.md) and ADR-028 said that validation arrives with slice 8 while this slice's work list required it; the contradiction is resolved in favour of "whichever slice can reach the environment", and both documents were corrected in the same change.

Devcontainer execution is still slice 8's, so a project that configures one gets ADR-031's shell and a message naming that slice. `FEAT_HOST_AGENT` in the daemon's own environment launches Claude on the host instead. It is deliberately not a request field or a client flag: a request that could move an agent outside its configured boundary would be a caller granting itself a capability. The daemon logs it, health reports it, the task detail says it, and the generated instructions tell the agent it is not contained.

Verification is the agent's claim, recorded in the review aggregate that slice 1 built and attributed with the reporter field that was waiting for it, so no stored format changed and no migration was needed. The dashboard marks it as a claim; the gate that runs a project's configured checks needs slice 8's environment and the interface says so.

Package layout gained no new package: `internal/agent`, `internal/agent/claude`, and `internal/control` were reserved by slice 0. Two `depguard` rules, `agent-stays-an-adapter` and `control-stays-a-protocol`, make their boundaries mechanical, and a third exemption list records the one file permitted to spawn a shell — the test whose subject is a shell script — verified to still fail against an injected violation elsewhere. See ADR-032; [06-technical-architecture.md](06-technical-architecture.md), [07-configuration-model.md](07-configuration-model.md), and [README.md](README.md) were updated in the same change.

## Slice 8 — Devcontainer execution

Status: **complete**, 2026-08-06

The design decisions this slice started from are recorded in ADR-033 in
[10-decisions-and-open-questions.md](10-decisions-and-open-questions.md),
including the three amendments it makes to the documented execution interface
and the correction it makes to ADR-032's promise about the completion gate.

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

### Delivered

All six acceptance criteria pass, and every one of them is checked by running a
command inside a real container rather than by reading what Feat generated. That
distinction is the slice's main lesson: the defect that mattered most left
everything Feat generates correct.

Claude now runs where the project puts it. The daemon resolves configuration into
an execution specification, `internal/execution/compose` writes a generated
override and brings the service up, every probe runs inside the container as the
agent's own user, and only then does the task get a terminal whose pane enters
that container. The shell action opens a shell in the same place, so the pane a
user opens beside their agent is the environment their agent is in.

`internal/execution` is the interface and `internal/execution/compose` the
adapter, under the boundary ADR-029 set for Git and an `execution-stays-an-adapter`
`depguard` rule verified against an injected violation. The adapter receives
final values and reads neither configuration nor persistent state, so the two
adapters never learn about each other: the daemon wraps the environment's probe
runner as the agent adapter's runner, and the Claude adapter is unchanged from
slice 7 — it was already written against "how the agent sees its own filesystem",
and this slice fills that structure with the container's paths.

Three properties are checked at the adapter rather than at the outcome:

- the generated override is pinned by a golden file, because every line of it is
  a decision about what the agent can reach;
- `container_name` and published `ports` are reset for the agent service, which
  is what makes acceptance criterion 6 possible at all — both are global, so a
  base file carrying either can be brought up exactly once. Measured against
  Docker Compose 5.1.4, along with merge-by-target and `--project-directory`
  behaviour, before any of it was relied on;
  **Amended: the agent service was not enough. Starting it starts its whole
  `depends_on` closure, so a dependency keeping either put the project back to one
  task per machine; the reset now covers every service the project defines, see
  ADR-033 evidence 15.**
- a launch that a container refuses leaves the task `failed`, having run no tmux
  command, with a message naming what to change. Seven such refusals have a test
  each, and they run in the default `go test ./...` against a fake Docker,
  because whether a half-finished launch is recoverable should not depend on the
  tester's machine.

Five defects were found by running the real thing rather than by reasoning about
it, and each is now a test. Two were Feat reading a correct answer wrongly:
Docker Compose reports an absent executable on standard output, so every "is this
tool installed" probe answered "yes" — a container missing `mktemp` would have
launched an agent that could never report; and a file a program could not open
was read as a missing program. One was a message: a failed `up` reported its
first line, which is "Image … Building", so a precise mount error in the user's
own Compose file reached them as progress.

The most important one is that **a task worktree is not a repository inside the
container**. Its `.git` is a file naming the main checkout's Git directory by
absolute host path, and nothing mounted it, so every Git command failed with "not
a git repository" while the container, the mounts, the user, and every recorded
state were exactly right — `agent.capabilities.git: full`, FR-GIT-006, and half
of criterion 5 all false at once, with nothing Feat observes to say so. Each task
repository's Git directory is now mounted at its host path with the access its
worktree has. Finding it also closed a gap in the rule beside it: the check that
refuses a mount of an ordinary checkout caught a parent directory and missed a
child, so `<checkout>/src` would have exposed the working copy unnoticed.

Verified against the reference project's own devcontainer, with `feat doctor`
green on both its projects — which also settles the slice 3 acceptance criterion
outstanding since 2026-08-05. In a real task container: the two task worktrees at
their configured container paths with the selected access, the read-only one
refusing a write; the stable repository mounted read-only from its ordinary
checkout; uid 1000, not root; no Docker socket and no Docker client; a real
commit made in the task worktree and the working copy unreachable; and three
tasks running side by side in three containers with three worktrees. `gh` is
installed in that image and deliberately logged out, so criterion 5's provider
half is verified as the refusal — a project that requires a CLI its environment
cannot authenticate fails launch with a message naming the remedy, reported from
inside the container. The positive required-CLI path is recorded as not verified
rather than reported as passing.

Two findings belong to the reference project rather than to Feat, and both now
produce an explanation in Feat's terms instead of the container runtime's: a
Compose file that mounts something *inside* a repository mount is satisfied by
the ordinary checkout and not by a task worktree, which holds only what Git
tracks; and a named volume nested inside a repository the task selected read-only
cannot be created.

`feat doctor` stops naming a slice for its devcontainer checks. It probes inside
a container that Feat's ownership labels identify as a live task container of the
project, and reports `skipped` with the condition otherwise — it still starts
nothing, because a command that reports on a machine should not change it. The
one configuration addition is `agent.claude.config_path`, and `config_volume`
became genuinely optional: a project that supplies Claude's configuration through
its own Compose files gets nothing mounted over it.

Package layout gained no new package: `internal/execution` and
`internal/execution/compose` were reserved by slice 0. `internal/paths` gained the
execution root. See ADR-033; [03-domain-model.md](03-domain-model.md),
[05-security-model.md](05-security-model.md),
[06-technical-architecture.md](06-technical-architecture.md), and
[07-configuration-model.md](07-configuration-model.md) were updated in the same
change.

## Slice 9 — Manual application runtime

Status: **complete**, 2026-08-06

The design decisions this slice started from are recorded in ADR-034 in
[10-decisions-and-open-questions.md](10-decisions-and-open-questions.md),
together with the amendment that running the adapter against real Docker
produced.

### Outcome

The user can manage the selected task's application services from Feat without granting Claude Docker access.

### Work

- Implement application Compose runtime adapter.
- Retain exact base/static/generated override/env-file inputs.
- Use unique Compose project identity and ownership labels.
- Implement create/start/stop/status/logs-info/destroy.
- Surface Compose health or unknown health.
- Record external staging database binding without lifecycle ownership.
  **Superseded: the binding is removed, see ADR-048.**
- Add TUI runtime actions.

### Acceptance criteria

- Starting/stopping task A does not affect task B.
- Logs open through normal Compose output.
- External database resources are never included in destroy plans.
  **Amended: what makes this hold is that a destroy addresses the task's own
  Compose project and names nothing, so anything Feat does not own is beyond its
  reach by construction. The declaration that used to restate it is removed, and
  the test now checks the reach rather than the declaration, see ADR-048.**
- Runtime remains running during review unless the user stops it.
- Approval offers stop but does not execute it automatically.

### Delivered

All five acceptance criteria pass, each with a test named for it, and each
checked at the layer that can prove the narrow claim: that a command carries one
task's Compose project name is a stronger statement than that the other task's
containers happen to still be there, and that no Compose command ran at all is a
stronger statement than that a recorded state did not change.

A user can create, start, stop, inspect, log, and destroy one task's application
services from Feat, on the trusted host, without the agent gaining any Docker
access at all. `internal/runtime` is the interface and `internal/runtime/compose`
the adapter, under the boundary ADR-029 set for Git and a
`runtime-stays-an-adapter` `depguard` rule verified against an injected
violation. The rule denies it `internal/execution` as well, and the Compose
plumbing is duplicated rather than shared: sharing it would put the environment
the agent runs in and the application the user tests behind one type, which is
the distinction the domain model, the security model, and CLAUDE.md all keep.

Nothing here happens on its own. No workflow transition, no reconciliation pass,
and no agent reaches an action, which two tests check by counting the commands a
task's transition to `review_requested` and to `approved` produce: none. What an
approved task with running services gets instead is the offer, in words, on both
screens a user reads after approving — which is how the fifth criterion is
satisfied before slice 11 delivers approval itself.

Three properties are decided at the adapter and pinned there:

- the generated override is held to a golden file, because every line of it is a
  decision about what the services run. It resets `container_name`, which is
  global to the Docker daemon, and leaves published ports exactly as configured,
  because a port is how the user reaches the application and v0 allocates none.
  Two tasks wanting one host port is explained in Feat's terms instead;
- destroy passes neither `--volumes` nor `--remove-orphans`, names no external
  resource, and reports the volumes it retained. It is checked on the argument
  vector rather than on the outcome, because a volume that survived because a
  fake never removed it proves nothing;
- the observed containers become a runtime state through a pinned table, in the
  shape ADR-026 used for the workflow transitions: what `degraded` means is a
  product decision and should be readable as itself.

Runtime state is observed on a slow poll over the tasks that hold a runtime
record, and a poll writes and publishes only when something changed. The
dashboard re-reads on every event, so a poll that published each time it looked
would make every task with services a permanent source of reads — the shape
slice 6 has already paid for once, and a test now pins it.

Three defects were found by running the real thing rather than by reasoning
about it, and each is now a test that fails against the behaviour it replaced.

- `docker compose stop` sends SIGTERM and kills a container that does not exit,
  and a service running as PID 1 has no default handler — so the ordinary
  `sleep infinity` service exits 137, and the obvious rule that a non-zero exit
  is a failure reported every stop the user had just asked for as `failed`. It
  is ADR-033's evidence-10 shape exactly: a correct question, an answer read
  wrongly, and every fixture-based test passing. It is now two rows of the state
  table.
- A host-execution project mounted nothing at all. A task's recorded
  `container_path` is filled only for devcontainer execution, because slice 8
  used it for where the *agent's* container mounts a worktree; an application
  runtime has containers whatever the agent does. Its services therefore ran the
  user's ordinary checkout with every record Feat kept still correct — and the
  only thing that said so was the note this slice added for the other half of
  the same problem. The mount target now comes from the project's configuration.
- Asking what is running failed before anything had been created, because every
  Compose command carried a generated override that does not exist until a
  create or a start writes it. The first thing a user does answered with a
  Compose error about a file Feat generates.

The last two were found by driving a real daemon, a real client, and real Docker
through one task's whole lifecycle rather than by testing the adapter, which is
where neither of them lives.

The mount question this slice could not settle by reasoning is settled by
looking. A repository's `container_path` was defined for the *agent's* Compose
files, and the application's are a different set that may mount the repository
elsewhere; Compose replaces a mount only when the target matches, so a mismatch
leaves the base file's own mount in place and the services run the user's
ordinary checkout while everything Feat records stays correct. Feat inspects the
started containers and says which service holds which checkout, and does not
refuse: the application runtime is inside the trusted host, so this is a
correctness problem rather than the boundary breach the same shape would be for
an agent.

`feat runtime` gains `create`, `status`, `logs`, and `destroy` beside the
documented `start` and `stop`, and the local API gains the three endpoints its
own list was missing. `destroy` carries the user's confirmation in the request
and asks for it at the terminal, and volumes are retained whatever the answer.
The command surface golden, [README.md](../README.md),
[06-technical-architecture.md](06-technical-architecture.md), and
[07-configuration-model.md](07-configuration-model.md) moved in the same change.

Verified against real Docker in an opt-in suite that runs the whole lifecycle,
proves two tasks can run the same services at once — which the fixture's fixed
container name makes possible only because the override resets it — keeps a
volume through a destroy, and asserts the `ps --format json` fields the state
table reads, so a Compose that renames one fails here rather than reporting a
running application as absent.

Verified again through the product rather than the package: a daemon started
from the built binary, a project registered through the socket, two tasks
launched, and every command run as a user would run it. Both tasks' services up
at once from one base file carrying a fixed container name; each container
holding its own task's worktree and not the checkout; stopping one leaving the
other running; the ordinary Compose logs opening and closing with an interrupt;
a daemon restart finding the services still up and reporting them without
restarting anything; and a destroy that did nothing when the confirmation was
declined and removed only that task's containers when it was given.

Package layout gained no new package: `internal/runtime` and
`internal/runtime/compose` were reserved by slice 0. `internal/paths` gained the
runtime root. See ADR-034.

A fourth defect was found by using the slice on a real application, and it is
recorded as ADR-034 evidence 12. `runtime.services` was read as the whole of what
exists, when it is only what a create and a start target: Compose starts whatever
those services depend on, so a project managing `api` and `nginx` had four
containers, and a stop that named its two managed services left a database
running, holding a host port, and absent from every status Feat printed. The
services Compose had started alongside also kept the fixed `container_name` their
base file gives them, so a second task could not have started at all — the one
thing a per-task Compose project exists to prevent, reintroduced by the services
nobody had listed.

Stop, status, logs, and destroy now address the task's Compose project and name
no service, the generated override reaches every service the project defines, and
a status says which services the project named and which are there because
another needs them. The aggregation table gained one row with it: a service
Compose started alongside a managed one counts unless it exited cleanly, so a
one-shot migration doing its job is not a degraded application. The opt-in suite
grew a fixture with both kinds of dependency, and two tests that fail against the
behaviour they replaced.

A fifth defect was found on the first create a user asked for on a new task, and
it is recorded as ADR-034 evidence 13. `docker compose create api` builds the
image of `api` and then creates a container for the service `api` depends on
from an image it never built, so a task whose dependency is built from the
project's own Dockerfile failed with `No such image` — every time, on every new
task, while a start of the same services worked. Create is now
`docker compose up --no-start`, which builds the dependency closure and starts
nothing. The opt-in fixture's one-shot dependency is built rather than pulled, so
the whole-lifecycle test fails against the command it replaced.

## Slice 10 — Notifications and resources

Status: **complete**, 2026-08-07

The design decisions this slice started from are recorded in ADR-035 in
[10-decisions-and-open-questions.md](10-decisions-and-open-questions.md),
together with the amendment that running it against a real daemon produced.

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

### Delivered

All four acceptance criteria pass, each with a test named for it, and each
checked where the narrow claim can be made: that no observation command ran at
all is a stronger statement than that a request happened to be fast, and that a
type has no field for a task's brief is a stronger statement than that one
message did not contain it.

Six things were measured on the target machine before any of this was designed,
and each of them decided something. `docker stats --no-stream` takes one to two
seconds however it is asked, so sampling is a background loop with a cache and
the local API serves the cache — a figure a request collected would be a request
a metric could stall. A per-core utilisation percentage turns out not to be
obtainable on macOS from Go without cgo, so Feat reports load average with the
processor count on both platforms rather than a percentage on one and something
differently defined on the other; `docs/04-functional-specification.md` and
`docs/06-technical-architecture.md` were narrowed to say so rather than left
promising something no build keeps. And `docker stats` measures a container's
memory against the container runtime's own virtual machine on macOS, so a task's
container and process memory are reported apart as well as together, and neither
half is presented as a share of the machine above it.

`internal/resources` is the observer and `internal/notify` the policy, both under
the boundary ADR-029 set for Git, with two new `depguard` rules. The resource
rule denies both Compose adapters and that denial is its point: an agent's
container and an application's are the same thing to something that measures
them, and a package able to tell them apart would eventually be asked to treat
them differently. The notification rule denies configuration, the control
protocol, and storage, which is what makes the fourth acceptance criterion a
property of the code rather than a rule to remember — a `Subject` carries a task
key, a title, and a project, and there is nothing to reach a brief, an agent's
words, a path, or a configured value with. A test pins the fields it may hold.

Three properties are decided at the policy and pinned there:

- what is worth interrupting somebody for is two tables, in the shape ADR-026
  used for the workflow transitions. Their most important property is an
  absence: nothing maps an end of turn or an idle process to a notification;
- one change produces one notification. A dying session moves both the process
  and the workflow, and a user told twice about one death reads the second as
  noise;
- startup catch-up records without notifying, so restarting Feat in the morning
  does not announce every turn that ended overnight.

The two grace periods the configuration model has carried since slice 3 finally
mean something, and they mean different things: the provider's decides when an
ended turn becomes idle, and the notification's decides how long a task must have
*been* idle before it is worth saying. Measuring both from the end of the turn
was the other reading and would have let a short notification grace silently
switch the notification off — a configuration that turns off the thing it
configures. `notifications` and `resources` were parsed, resolved, and defaulted
with no reader for seven slices; this is their first, which is why two of their
four fields had to be given semantics here rather than found.

Whether the user is attached is asked of tmux per window, through
`window_active_clients`, which slice 5's discovery did not collect. It is an
observation rather than a memory of an attach: a user who detached, or who
switched to another task's window, stops watching without telling Feat anything,
and a session-level answer would silence every task of a project the moment one
of them was being looked at. Measured against tmux 3.7b, where it follows a
window switch immediately.

Verified against the real tools in an opt-in suite: real `vm_stat`, `sysctl`, and
`statfs`; a real `ps` differenced across two samples over a process deliberately
spinning between them; a real labelled container found through `docker ps` and
measured through `docker stats`; a real `osascript` delivery; and a real
control-mode tmux client that watches one task's window while another runs
unwatched, and whose attention follows a window switch.

Verified again through the product rather than the package, with a daemon started
from the built binary, a project registered through the socket, and a task
launched and driven by control messages written the way a hook writes them. The
machine card's figures came back real; the task reported its own pane's process
subtree; a turn ended at 10:35:51 became idle at 10:35:56 after the provider's
five-second grace and produced a desktop notification at 10:35:59 after the
project's three-second one; and a third turn that ended while a real tmux client
was watching that task's window produced the idle transition and no notification,
where the two before it produced both. The daemon ran for three minutes of
two-second sampling and wrote not one warning.

One thing found by hand rather than by reasoning is recorded as ADR-035 evidence
11, and it is about verification rather than about Feat: the first attempt to
show suppression attached a control-mode client without holding its standard
input open, so tmux accepted it and it left at once. The notification was
correctly delivered to an unwatched window, and the check looked as though it had
proved the opposite of what it proved. The real verification held the client open
and read `list-clients` before drawing any conclusion.

`GET /v1/resources` is the one endpoint added, with a golden file, and the
command surface does not change. No stored format changes: samples are not
persisted, and the event vocabulary gains one additive type,
`notification_sent` — recorded because a desktop notification is gone the moment
it is dismissed, and because slice 13 has to measure how many idle notifications
turned out to be false. It is deliberately not itself notifiable, which a test
pins, since recording an event publishes it.

Package layout gained no new package: `internal/resources` and `internal/notify`
were reserved by slice 0. See ADR-035;
[04-functional-specification.md](04-functional-specification.md),
[06-technical-architecture.md](06-technical-architecture.md),
[07-configuration-model.md](07-configuration-model.md), and
[README.md](../README.md) were updated in the same change.

## Slice 11 — Review and external commands

Status: **complete**, 2026-08-07

The design decisions this slice started from are recorded in ADR-036 in
[10-decisions-and-open-questions.md](10-decisions-and-open-questions.md),
together with the two defects that running it end to end produced.

### Outcome

The user can review every changed repository against the correct immutable base using familiar tools.

### Work

- Compute per-repository change summaries.
- Add grouped review view.
- Expand/execute configured diff, editor, and status commands.
- Add approve/pending/revise actions.
- Preserve review state across restart.
- Implement the provider-native completion gate that runs the project's
  configured `checks` in the agent's execution environment and returns a failure
  to the native agent loop, reaching `verifying` and `verification_failed`.
  Deferred from ADR-032, which promised it for slice 8; slice 8 delivers the
  environment the gate needs but never listed the gate itself, and ADR-033
  moves it here rather than leaving a promise no slice schedules.

### Acceptance criteria

- Each repository uses its own recorded base commit.
- Neovim opens in the selected task repository.
- Commands cannot escape configured task paths through unvalidated placeholders.
- Approval does not stop or destroy runtime automatically.
- A failed configured check returns the task to the agent loop and never reaches
  `ready_for_review`; an agent-reported result is still distinguishable from a
  gated one.

### Delivered

All five acceptance criteria pass, each with a test named for it, and each
checked where the narrow claim can be made: that a diff command carries one
repository's own base is a stronger statement than that the numbers beside it
were right, and that approving produced no container command at all is a
stronger statement than that a container happens to still be running.

A task's changes are grouped by repository and compared against the commit that
repository started from. `internal/git` gained `Compare`, because a change
summary is Git's own answer about a worktree, and it reports the two counts
apart: files changed includes untracked ones, and the line counts do not, because
counting a file Git has never been told about would mean writing to the index of
the repository the user is working in. The screen and the printed summary say so
rather than presenting one number derived from two definitions.

`internal/review` decides whether an expanded command may run, under the boundary
ADR-029 set for Git and a `review-stays-a-policy` `depguard` rule. The daemon
expands the templates, because the placeholder vocabulary belongs to
`internal/config`; this package checks what the expansion turned into. Slice
11's third acceptance criterion is therefore one rule against one list, and its
most important case is not an obviously dangerous path: another task's worktree
is absolute, real, and a perfectly safe place for a command to run, and it is
still the wrong one.

The completion gate that ADR-032 promised for slice 8 and ADR-033 moved here is
delivered. It is triggered by the explicit review request and never by an end of
turn, and the daemon runs the checks itself: the outbox is agent-writable by
design, so a result delivered through it is one an agent could have authored, and
a `provider` label on such a result would be Feat claiming enforcement it never
performed. Each check runs where its `execution` field says — the field has had
no reader outside `feat doctor` since slice 3 — and a repository the task holds
read-only is recorded as skipped with that reason rather than dropped.

The failure returns to the running session as a failed command. The generated
helper the agent uses to request review now waits for the daemon's verdict,
written into the task's inbox, and exits non-zero with the failing output on
standard error, so the model reads it and carries on in the same turn. It is
deliberately not a hook: Claude's only blocking hook fires at the end of *every*
turn, so a gate built on it would either run the project's suite whenever the
agent stopped speaking or need shell logic to decide whether a request was
outstanding — and ADR-032 made every generated hook inert precisely because a
hook that blocks changes the session it observes. The wording in
[06-technical-architecture.md](06-technical-architecture.md) and
[02-user-workflows.md](02-user-workflows.md) is corrected rather than left
describing a mechanism nothing implements.

Three properties are decided once and pinned there:

- a check that could not be started, or that exceeded its bound, is
  inconclusive rather than failed, and an inconclusive check does not pass the
  gate. A task reaching `ready_for_review` on the strength of a check nobody
  managed to run would claim a verification that did not happen;
- the verdict the agent reads is a versioned line-oriented document rather than
  JSON, because the only thing that reads it is a shell script, and ADR-032's
  reason for keeping parsing out of generated scripts applies to reading as much
  as to writing;
- what a gate produced and what an agent claimed are both kept, and they do not
  read alike. `api.NewVerification` labelled every set of results as the agent's:
  it started from "agent" and only ever tested whether it was not "agent", so
  the condition was unreachable. It produced the right answer for ten slices
  because every result was a claim; this is the first slice that produces
  evidence, which is what turned dead code into an acceptance criterion.

Two workflow edges were added, each with its reason in the table it lives in:
`verification_failed` to `review_requested`, so an agent that fixed what the gate
caught can ask again, and `verifying` to `review_requested`, so a gate a restart
interrupted returns the task to where the request was rather than leaving it
claiming that checks are running. Nothing new reaches a review state without an
agent having asked, which the pinned "idle is not completion" test still holds.

Approving decides the work and touches nothing else, which is checked by counting
the commands it produces: none. It is also the first thing to exercise the offer
slice 9 rendered for an approval no build could make.

Verified against the real tools: real Git for the comparison, including a binary
file Git reports without a line count and an untracked file that contributes
none; and the generated helper under a real shell, answered the way the daemon
answers it, failing and passing in turn.

Verified again through the product rather than the package, with a daemon started
from the built binary, a project registered through the socket, and a task
launched, worked on, and reviewed as a user would. The agent asked for review
through its own generated helper; the helper waited, and exited 1 with the
failing check's own output and the worktree it ran in; the task was
`verification_failed` with the result attributed to Feat rather than to the agent
that had claimed the same check passed. With the check fixed, the same helper
exited 0 and the task reached `ready_for_review` through `verifying`. A daemon
restarted while the checks were running left the task back at `review_requested`
saying why, and running them again by hand recovered it. `feat review` printed
each repository against its own base, and the diff command it returned produced
the task's own diff when run.

**That run found two defects, and neither was reachable from this repository's
own fakes.**

The first is slice 5's. A tmux client whose locale is not UTF-8 replaces every
non-printable character in the output of `-F` with an underscore, and every
format `internal/tmux` uses is tab-separated — so a daemon started without `LANG`
or `LC_ALL` cannot parse the identifiers of the terminal it has just created, and
discovers nothing at all. Every task launch fails with `tmux returned
"$0_@0_%0"`. An environment with no locale is what a process started by launchd
or systemd gets, which is how slice 14 intends a daemon to run; the suite never
saw it because `go test` inherits the developer's own environment. Every control
invocation now passes `tmux -u`, attachment deliberately does not, and an opt-in
test creates and rediscovers a terminal with every locale variable removed —
which fails against the previous behaviour with exactly the message the product
produced.

The second is this slice's own, and it is the more interesting one: **the daemon
is the only process that writes state and every write is atomic, and neither of
those makes a load-change-save cycle safe against another one.** The completion
gate is the first thing in Feat that writes one task's records from a second
goroutine, and a gate finishing while the review request that started it was
still comparing repositories left a task recorded as `ready_for_review` whose
review held no checks at all — the workflow said the checks had passed and the
record of what passed had been overwritten by a copy loaded a moment earlier.
One task's records are now serialised by a per-task lock, held across a cycle
rather than across an operation, and the gate re-reads everything after taking
it, because a user who approved while the suite ran has decided and a gate must
not undo that. The regression test forces the interleaving inside Git rather than
racing for it, and fails against the behaviour it replaces.

`feat doctor` stops naming a slice for a check that runs in the agent's
environment and looks it up inside a live task container instead, reporting
`skipped` with the condition otherwise — the rule ADR-033 set for every other
question about that environment.

`feat review <task>` opens the review screen in a terminal and prints the same
comparison anywhere else, which is the split ADR-027 made for `feat` itself. The
command surface does not change, so its golden file is untouched, and no
configuration field is added. No stored format changes and the event vocabulary
gains nothing, so no migration was needed.

One thing the same run left open: a task that passes its gate reaches
`ready_for_review` without the user being told, although this slice suppresses
the earlier `review_requested` notification on the promise of exactly that one.
The transition, the event, and the record are all correct, so it is the
notification flow rather than this slice's states, and it is answered in slice 13
where every condition gets the same walk against a real desktop.

Package layout gained no new package: `internal/review` was reserved by slice 0.
See ADR-036; [02-user-workflows.md](02-user-workflows.md),
[06-technical-architecture.md](06-technical-architecture.md), and
[README.md](../README.md) were updated in the same change.

## Slice 12 — Reconciliation and cleanup

Status: **complete**, 2026-08-07

The design decisions this slice started from are recorded in ADR-037 in
[10-decisions-and-open-questions.md](10-decisions-and-open-questions.md).

### Outcome

Feat recovers honestly and removes only resources the user explicitly selected.

### Work

- Reconcile snapshots, tmux, worktrees, Compose, control messages, and review state.
- Decide the quarantine policy deferred from slice 5 (ADR-030): one damaged resource must not make every healthy one unusable. For tmux that means discovery reporting a broken terminal while still returning the rest, and it settles the working-directory validation whose failure mode is the same blast radius.
- Write and reconcile the durable daemon record `daemon.json`, deferred from slice 2 because this is the first slice that reads one (ADR-027).
- Recover a dead agent session, deferred from slice 7 (ADR-032). Slice 7 records the exit status and marks the task `failed`; it never restarts or resumes, for the reason FR-STATE-004 gives for containers. Resuming belongs here because a dead agent pane is the same recovery question as a missing tmux window, a removed worktree, and a stopped Compose project, and answering it inside the agent adapter would set a recovery policy the slice that owns recovery had not chosen. Slice 7 captures the provider session ID from the session-start event before the process can fail, so the resume is available through the provider's own `--continue`/`--resume` rather than through a new session that has lost the task's history. Recovery stays an offered action, never an automatic restart.
- Report missing/orphaned/inconsistent resources.
- Build cleanup-plan API with stable token/IDs.
- Separate containers/networks, volumes, worktrees, and branches.
- Implement dirty/unpushed/unmerged warnings.
- Archive task metadata.

### Acceptance criteria

- Daemon/computer restart loses no task identity.
- Stopped containers are not restarted.
- A failed agent session is resumed only by explicit user action, and resuming continues the recorded provider session rather than starting an empty one.
- Orphan resources are reported before adoption/removal.
- One damaged or unreadable resource does not make unrelated healthy ones unusable.
- Volumes remain unless explicitly chosen.
- Broad path or non-task resource deletion is rejected.
- Dirty/unmerged resources require explicit confirmation.

### Delivered

All eight acceptance criteria pass, each with a test named for it, and each
checked where the narrow claim can be made: that a reconciliation pass ran no
tmux command at all is a stronger statement than that a task happens to still be
failed, and that a selection never named the volume class is a stronger statement
than that a volume survived because a fake did not remove it.

**Quarantine is one rule rather than tmux's.** ADR-030 deferred it here because
worktrees, Compose projects, and control messages raise the same question, and
answering it inside one adapter would set a policy for all of them. The rule: an
enumeration returns what it could read together with what it could not, and fails
as a whole only when the enumeration itself failed. `control.Workspace.Pending`
has behaved this way since slice 7 and is named as the precedent rather than left
a coincidence. `tmux.Discover` now returns terminals, managed sessions, and
damaged entries; a damaged pane quarantines its terminal because half a terminal
is not one, a damaged session quarantines its windows, and everything else stays
usable. Seven shapes of damage have a row each in a table, and every one of them
previously ended discovery for the whole server — `EnsureTask` failing for every
unrelated task, with startup reconciliation stopping before it reached any of
them.

Two properties of that rule are worth stating because they are what bound the
damage rather than merely report it. A project whose every window is quarantined
still has its session, so `projectSession` reads the discovered sessions rather
than deriving one from healthy terminals: the other reading would answer "none"
and give the project a second session, making the ambiguity permanent. And two
sessions claiming one project quarantine that project alone — it is refused a
third rather than given one, and every other project stays usable.

Working-directory validation is settled with it, as ADR-030 said it would be.
`CommandSpec.Directory` is now checked with the same rule as the arguments, and
that rule gains the tab: the directory is the one caller-supplied value tmux
reports back, inside a tab-separated list format, so a tab in a path misaligns
every pane field and breaks discovery for every terminal on the server. Quarantine
bounds what that costs; validation stops Feat creating one. Both are wanted.

**One recovery pass.** `internal/reconcile` is a policy package under the
boundary ADR-029 set for Git and ADR-036 for review, with a
`reconcile-stays-a-policy` depguard rule: it owns the vocabulary of a finding,
the cleanup classes, the plan token, and the consent rule, and it drives no
adapter and reads no state. The daemon asks tmux, Git, both Compose adapters, the
control workspaces, and the review records the same question in one pass, and a
step that fails records a problem and lets the remaining steps run — the
quarantine rule applied to the pass itself. Nothing is repaired, restarted,
recreated, or adopted, which is FR-STATE-004 generalised from containers to every
class. A pass that could not ask a question reports that rather than reporting
the answer as "nothing": an unreadable tmux server told a user their terminals
were gone.

**The cleanup token names what, and the fresh observation decides whether.** The
token covers the task, the resource identities, and a schema — deliberately not
the warnings. A token over observations would change whenever the agent wrote a
file, so a user would learn their plan was stale when what had happened is that
their worktree became dirty, and the second is the thing they need to hear.
Execute re-resolves the plan, refuses a token that no longer names the same
resources, and refuses a confirmation that does not cover the warnings observed
at the moment of removal. Both halves have a test, and the one that matters most
proves the token does **not** change when a clean worktree becomes dirty.

Seven classes, each an independent choice: the four FR-CLEAN-002 names, with the
agent's containers and the application's kept apart because they are separate
concepts everywhere else in this product, plus the task's tmux window and its
control workspace. That the last two are an extension of the specification's list
rather than in it is recorded in ADR-037 and in
[04-functional-specification.md](04-functional-specification.md) rather than left
to be discovered. The removal order is a requirement rather than a detail —
whatever holds a file is stopped before the file is removed — and is pinned by a
test.

Three properties are decided once and pinned there:

- volumes are removed **by name**, never through `docker compose down --volumes`,
  which is all or nothing and would remove a volume the plan never named. The
  names come from the container runtime's own project label, so a resource the
  project declares external carries no such label and cannot appear — the
  external-resource rule becomes a property of the enumeration rather than a
  filter somebody has to remember;
- the volume class carries a standing warning in the policy itself, so a volume
  is removable only when that exact warning was confirmed. "Volumes are retained
  by default" is therefore a property of `internal/reconcile` rather than of the
  daemon having remembered to attach a string. The first version depended on the
  daemon, and a test written for the acceptance criterion is what found it;
- archiving is refused while the plan still names resources the selection leaves
  behind. An archived task is one Feat stops tracking, and stranding a running
  container behind it would manufacture exactly the orphan this slice exists to
  report.

Archiving needs no new stored document. The task reaches `archived`, its snapshot
keeps the branches, bases, and session it recorded, and the append-only event log
gains one additive type carrying what each class removed — including a removal
that stopped half way, which is the case a user most needs an account of. So both
halves of "explain what happened later" are answerable from the two durable
records slice 1 already built, and no stored format changes.

**`daemon.json` is written because it has three readers**, not because ADR-027
named this slice. No acceptance criterion here needs a durable daemon record:
task identity survives a restart through task snapshots and tmux metadata,
neither of which is one. Writing it on the strength of the deferral alone would
have produced exactly what ADR-027 evidence 2 refused — a versioned compatibility
surface with no reader. It records the state directory's schema version, so a
build older than the directory refuses it rather than silently discarding what a
newer schema added; whether the last run ended cleanly, written by the run that
ends rather than the one that starts, so a crash needs nothing to be written in
order to be visible; and when it stopped. It carries no process identifier,
socket, or lock, which is the whole of ADR-027 evidence 1.

**Recovering a dead agent session** is offered and never automatic, which is
checked by counting the commands every automatic path produces: none. It
continues the provider session slice 7 captured from the session-start event
before the process could fail, through `--resume <id>` with no initial prompt — a
resumed session already holds the conversation, and one invented here would be
Feat putting words in the user's mouth. The identifier is validated before it
reaches an argument vector, because it arrives in a message an agent could have
authored.

Three things running the package suite made necessary, none of which reasoning
had produced:

- **`EnsureTask` returns an existing terminal untouched**, so the first resume
  changed the task's state and started nothing. That is right for a launch —
  repeating one must not restart an agent that is already working — and wrong for
  the one case where the caller means to replace the process. `tmux.Restart`
  respawns the agent pane and keeps the pane whatever happens, because unlike a
  pane created moments ago this one holds the output of the session that died,
  which is often the only account of why. A test that asserted two launches is
  what found it;
- ADR-032's suppression had to narrow. A session start that resumed must not move
  a task that is **already working**, or a user typing `/clear` would move their
  task's workflow; the wider rule also caught a task in `preparing`, which is
  where a resume puts it and which is waiting for exactly that event. Unnarrowed,
  a resumed task sat in `preparing` with a running agent, looking broken;
- `observeRuntime` ran a load-change-save cycle outside the per-task lock. It is
  ADR-036 evidence 9's shape in the one place that reaches a task's records on a
  timer, and every reconciliation write now takes the lock and re-reads under it.

Verified against the real tools: real Git for the removals, where the point is
the part a fake cannot decide — that `worktree remove` refuses a dirty worktree
without `--force` and takes it with one, and that `branch -d` refuses an unmerged
branch while `-D` does not. Both safeties sit under the confirmation rule, and
the first version of that test passed against a branch Git was willing to delete
because it pointed at its own base, which is a test that checks nothing. And
against the installed Claude Code 2.1.220 in a real terminal inside a tmux pane:
`claude --resume <unknown-id>` exits 1 with a message rather than opening the
interactive picker its help describes, which is what makes the resume safe to
offer — the failure is visible rather than a session that starts perfectly and
has been given nothing.

**Verified again through the product rather than the package**, with a daemon
started from the built binary, a project registered through the socket, and a
two-repository task launched, inventoried, cleaned up, and archived as a user
would. The inventory named the terminal, both worktrees, both branches, and the
control workspace; a dirty worktree and an unpushed, unmerged branch produced
their own warnings; declining a warning dropped its whole class and removed
nothing; confirming removed both worktrees and both branches — one per
repository, despite identical names — and left the ordinary checkouts byte for
byte as they were. A clean stop recorded itself, a `kill -9` did not, and the
next run reported each correctly. The archive removed the control workspace,
kept the task's branches and base commits in the snapshot, and left an event per
class in the log.

Verified once more against the reference machine's own two projects — one host,
one devcontainer — with six real tasks, five live tmux windows, real worktrees,
a running agent container, exited ones, exited application containers, and a
task-owned volume. The recovery pass reported every one of them correctly and
reported no false orphan, on exactly the `{project_id}/{task_id}` worktree layout
that produced one. A cleanup plan for a devcontainer task named its agent Compose
project; a plan for the application task named its own `…_postgres_data` volume
and not the unrelated `jobharbor-devcontainer_frontend-node-modules` beside it,
which is the external-resource rule holding against real Docker rather than
against a label a fake supplied. Removing that task's containers left the volume;
choosing the volume class without confirming its warning was refused and the
volume untouched; confirming removed it by name. A dead containerised agent was
resumed: the container came back up and the pane's own process is
`claude … --resume a4732887-…` with the recorded identifier and no initial
prompt, showing the conversation it had before it died.

**That run found five defects, and none was reachable from this repository's
own fixtures.** They are recorded as ADR-037 evidence 10 to 14. The last was
found by the maintainer using the dashboard rather than by any test.

The first is the most serious: **a daemon that shut down cleanly could never
start again.** The claim carried the previous run's stop time into the new run's
record, producing a record whose stop preceded its own start — which the domain
refuses, correctly. Only a daemon that had *crashed* could start, because a
crashed run leaves no stop time to carry. The suite missed it because every
fixture in `internal/daemon` freezes the clock, so the carried stop and the fresh
start were the same instant; the first regression test written for it passed
against the injected defect, and it is now a test with a clock that moves.

The second is the one a user would have been hurt by: **a live task's own
directory was reported as an orphan, with advice to delete it.** The worktree
root `…/worktrees/{project_id}/{task_id}` puts the interesting directories two
levels below what the scan listed, so it named the directory holding both of a
running task's worktrees while missing the abandoned task directory beside it —
both halves of the mistake at once. A report that recommends the wrong deletion
turns this product's whole discipline against the user, since Feat reports and
the user acts. The scan now descends only where a task's own paths lead.

The third was visible in the inventory itself: a two-repository task printed the
same branch name twice, because one template names every repository's branch
alike. **The plan token covered the name and not the repository**, while the
removal is pointed at a repository by exactly that field.

The fourth came from the reference machine, and is the one worth remembering:
**the task that most needed recovery was the one that could not have it.**
Resuming moved a task to `preparing` unconditionally, and the ordinary state of a
task whose agent died is `working` with a failed process — because a process that
dies unobserved leaves the workflow alone, and reconciliation reports it rather
than moving it. `working` has no edge to `preparing`, correctly, so every such
task was refused in fifteen milliseconds with a generic message. Every fixture
reached the resume from `failed`, since a test that arranges a dead agent
arranges the whole of it; a real one had only lost its container overnight.

The fifth is the only one no test could have caught in the shape the tests were
written: **the recovery band could never be brought up to date.** Reading the
last pass and running a new one are deliberately separate, because a pass asks
the container runtime about every task while the dashboard refreshes every two
seconds — but nothing in the dashboard ran one, so the band described the daemon's
startup pass for ever. A user who resumed a task or cleaned one up went on being
told about resources they had already dealt with. Every existing test asserted
the band's content against a report handed to the model, so none could notice
that no second report ever arrived. The split stands; what changed is that a
resume, a finished cleanup, and the explicit refresh key each run a pass, and the
band now says when it looked.

`feat cleanup <task>` is implemented and has no blanket `--yes`. FR-CLEAN-002
requires separate choices and FR-CLEAN-003 explicit confirmation, and one flag
answering every question is the thing both rules exist to refuse: in a terminal
it asks once per class in removal order and again for each warning, and anywhere
else it prints the inventory and removes nothing. The TUI gains a cleanup screen
with the same structure, a recovery band that appears only when a pass found
something, and a resume key. The command surface gains no flag, so its golden
file is untouched; five endpoints are added and recorded in
[06-technical-architecture.md](06-technical-architecture.md).

This is the first slice after which no command in the documented surface reports
an unimplemented slice, which a test now pins — the counterpart of the one
requiring a placeholder to name its slice.

Package layout gained no new package: `internal/reconcile` was reserved by slice
0. See ADR-037; [02-user-workflows.md](02-user-workflows.md),
[04-functional-specification.md](04-functional-specification.md),
[05-security-model.md](05-security-model.md), and
[06-technical-architecture.md](06-technical-architecture.md) were updated in the
same change.

## Slice 13 — Dogfood hardening

### Outcome

v0.1 meets every acceptance criterion in [08-v0-scope.md](08-v0-scope.md).

### Work

- Run repeated three-task workflows on the reference project.
- Capture setup/recovery/cleanup failures as regression tests.
- Improve error messages and `doctor` output.
- Let a command name a task the way the product does. Every `<task>` argument
  takes the full identifier, while every list — the dashboard, `feat task list`,
  and the notifications slice 10 adds — shows the eight-character key derived
  from it. So the identifier a user can see is the one no command accepts, and
  the only place the full one appears is the dashboard's task detail: a user
  reading `feat attach <task>` in the documentation has nowhere to get the
  argument from. Found during slice 9 by running `feat runtime status` with the
  key the task list had just printed. The rejection is also unhelpful — it
  explains the format rather than naming where to find a valid value.

  Deferred to this slice rather than fixed in slice 9 because it belongs to the
  whole command surface: `attach`, `review`, `runtime`, and `cleanup` all have
  it, it has been there since slices 5 and 6, and resolving a key is an
  addressing rule the API owns rather than a runtime concern (ADR-026 derives
  the key from the identifier; ADR-027 makes the daemon resolve a task's owning
  project). An ambiguous prefix must be reported rather than guessed, which is
  the same rule ADR-029 applied to a colliding branch name.
- Check the whole notification flow end to end, against a real desktop rather
  than a fake notifier. A task that passes its completion gate reaches
  `ready_for_review` and the user is not told: observed while exercising slice 11
  by hand. The state, the event, and the review record are all correct, so what
  is missing is the interruption rather than the transition.

  It matters more than one absent notification, because slice 11 deliberately
  drops the `review_requested` notification when a gate is about to run, on the
  argument that the second interruption is the one that means something
  (ADR-036). If the second one does not arrive, a gated task arrives more
  quietly than an ungated one, and the suppression made things worse rather than
  better.

  Not diagnosed, because it is a flow rather than a rule: the state-to-condition
  table is right (`notify.notifiableWorkflow` maps `ready_for_review`), so the
  question is which of the layers between a transition and a desktop drops it.
  The ones to walk are `finishGate`'s notify call, the suppress-while-attached
  policy — a user who just told the agent to request review is by definition
  attached to it — the startup catch-up gate in `notifyTask`, and the project's
  own `notifications.desktop`. macOS also drops an unauthorised notification
  without saying so, which is why this needs a real desktop to answer.

  Deferred here rather than fixed in slice 11 because every condition deserves
  the same walk. Slices 10 and 11 each added notifications with unit tests over a
  fake notifier, and a fake notifier proves the daemon asked, not that anybody
  was told.
- Route a check that could not run away from the agent and to the user. A check
  whose program does not start is recorded as `unknown` with the reason — the
  distinction `review.Gate` already draws between a check that reported failure
  and one that never ran — and then `review.Decide` collapses the two:
  `Passed = Failed == 0 && Inconclusive == 0`. The task lands in
  `verification_failed`, which says the work failed its checks. Nothing ran.

  Found in the first real feature run on the reference project. A check was
  configured as `pytest`, the project runs its tests through a wrapper, and the
  bare program was not on the path inside the agent's environment. The gate
  behaved exactly as ADR-036 designed: the helper blocked, the failure returned
  into the agent's loop, and the agent diagnosed it, named the configuration
  file, and declined to edit the configuration governing its own gate — which is
  the right refusal, because an agent that chooses its own check command
  certifies itself.

  What has no exit is what follows. The agent cannot fix it, the task rests in a
  state that misdescribes it, and the person who can fix it was told through a
  workflow state rather than asked. A check that cannot start is a configuration
  failure and belongs with the user, naming the check, the repository, and the
  project. The information exists at every layer and is discarded at the one
  point that decides what to say.

  The verdict becomes three-valued, a run that established nothing leaves the
  task in `review_requested` rather than `verification_failed`, and a new
  `verification_blocked` notification is what reaches the person who can fix it.
  The helper exits zero on that verdict, so a configuration the agent must not
  edit is not handed back to its loop. No workflow state is added. See ADR-055.

- Stop the daemon's own bookkeeping from reaching the commands it runs for a
  task. `FEAT_DAEMON_SPAWNED` marks a process that `Spawn` started, so that a
  binary spawned with arguments it does not understand cannot re-run the client
  path and spawn again. It is set on the daemon and never cleared, and every
  child a serving daemon starts inherits its environment — a configured check, a
  tmux pane, the agent's session — so a `feat` invocation anywhere inside a task
  refuses to start a daemon, having been told it was one.

  Found by running Feat's own integration check through Feat's completion gate:
  `feat daemon start` failed on a variable no test had set. The marker is
  cleared once the daemon holds runtime ownership, which is the point past which
  the case it guards cannot happen, and the binary lifecycle test builds the
  environment a user's shell would have rather than inheriting the runner's.

- Diagnose a check command before a task depends on it. `feat doctor` reports a
  check configured to run in the agent's environment as skipped, because there is
  no container to look inside (ADR-033's rule for a check this build cannot run).
  That is honest and it is also why the misconfiguration above survived
  registration, `feat doctor`, task preparation, and an entire implementation
  before anything noticed. Where a task of the project is running, its
  environment is exactly the place the program can be resolved.

- Find why an attention state does not clear. Observed in the same run: the task
  reached `needs_input` correctly during planning, the user answered, the agent
  carried on implementing, and the dashboard went on reporting `needs_input`.

  Undiagnosed. `UserPromptSubmit` is installed and its effect sets attention to
  none, so the candidates are the hook not firing, the message not being applied,
  or `Notification` re-arming it afterwards — that entry sets `needs_input` and
  only a turn ending or a prompt clears it, so a single notification during a
  long implementation with neither would pin it. An attention state that never
  clears is one nobody reads, which is the reason `KindTurnEnded` clears it
  already.

- Put every command that takes a task under the noun a user can explore. The
  surface is two designs at once: `project`, `task`, `runtime`, and `daemon` are
  nouns with verbs beneath them, while `implement`, `attach`, `review`,
  `cleanup`, and `doctor` are verbs at the top level. The seam runs through
  `task`, because everything that takes a `<task>` is an operation on one and
  `feat task --help` lists only `list`. A user who has learned `feat task list`
  reaches `feat attach` through the documentation or not at all.

  ADR-038 is the evidence that these are one family: one defect landed on
  `attach`, `review`, every `runtime` action, and `cleanup` at once, through one
  helper, because naming a task is what they share. So `attach`, `review`, and
  `cleanup` move under `feat task`; `feat implement` stays, because it produces a
  task rather than taking one; `feat runtime` stays a noun of its own, because a
  feature environment is a co-equal thing a task owns. `attach` and `review` keep
  hidden top-level aliases and `cleanup` deliberately does not. See ADR-040.

  Done in this slice because slice 13 is already rewriting every `<task>`
  argument for ADR-038, and because slice 14 publishes v0.2: after that, moving a
  command breaks a shell history that is not Feat's to break. The golden file,
  [README.md](README.md), [06-technical-architecture.md](06-technical-architecture.md),
  and the README move in the same change, as they did in ADR-028 and ADR-031.
- Give the dashboard a shape that survives three tasks. A task row is 158
  columns wide against a terminal that is 80 to 160, so every row wraps and three
  tasks read as nine lines of unaligned text. Each of the six screens replaces the
  whole terminal, so a user watching three tasks can look at one. Reported by the
  maintainer as confusing while preparing the runs below.

  It becomes a left rail of tasks grouped by project, a tabbed main region over
  views of the selected task, and a footer holding the worktree path and the
  machine's resources — which moved to the foot of the rail after use, as bars
  against the total, because the question they answer is a proportion (ADR-044).
  The rail carries the five fields no other requirement
  claims; the four FR-UI-002 shared with FR-UI-003 and FR-UI-005 stop being
  required twice, and the specification moves with the code. Preparation,
  cleanup, confirmations, and the key map become overlays over the live dashboard
  rather than screens that replace it. Attachment stays a handover to native
  tmux, because embedding the session means Feat implements a terminal emulator
  (ADR-030). See ADR-041.

  Done in this slice, ahead of the three-task runs rather than after them,
  because those runs are read through this screen: a dashboard that shows one
  task at a time cannot produce evidence about three, and criterion 14 is a claim
  about whether this screen carries the coordination. The deferred tmux split
  direction lands with it.
- Put the agent's session in the main region. The layout above was built around
  views Feat writes itself, and using it showed that the region wants the
  terminal: detail and review overlap, and neither fills it. Moving the pane
  there is not the way — measured, `join-pane` destroys the task's own window and
  breaks ADR-030's discovery — and neither is an emulator.

  What works is what claude-squad and agent-manager both do: tmux renders, and
  Feat draws the result. The daemon holds one `tmux -C` control-mode connection,
  redraws the focused pane on tmux's `%output`, and sends keys back; the TUI
  never runs tmux, because `ui-is-a-client` denies it the adapter. Feat keeps no
  screen grid and reads nothing out of the bytes but their width. Detail and
  review merge into one panel. See ADR-042.
- Record a review decision once. The maintainer, reading the review surface,
  observed that the states a user can put a review into have no consequences
  beyond changing the workflow state, and asked whether they were worth having.
  They are; the defect the question found is that the decision was recorded
  twice, as `Review.Status` and as the task's workflow.

  Nothing read the second copy — both its call sites only rendered it — and one
  action moved it without the other: leaving a review pending set the review to
  pending and left the workflow at `approved`, so the panel read "workflow
  approved" above "decision pending" with no way out, because `approved` has no
  outgoing transition. That action had no test and its key was bound but never
  advertised. The workflow becomes the only record, leaving pending stops being
  an action, and the decision the panel renders is derived from the workflow, so
  the keys appear only where the transition exists. See ADR-047.

  Done in this slice because it is a dogfood finding about the surface slice 13
  is already rewriting, and because the stored fields go without a schema
  migration — an exemption that is only available while the state directory
  belongs to the people writing it, which slice 14 ends.
- Measure manual coordination removed and false idle notifications. The measure
  is also the evidence OQ-013 needs.

  It stays in this slice while the two documentation items move to slice 14,
  because it is evidence rather than prose: it is a reading of the
  `notification_sent` events the state directory already holds, it cannot go
  stale as the code around it changes, and two things wait on it — v0.1
  acceptance criterion 14, which is the claim the product rests on, and OQ-013.
- Let the interrupt that leaves a program the dashboard ran stay that program's.
  Opening the Compose logs from the runtime tab left no way out that was not also
  a way out of Feat: `logs --follow` ends only when it is interrupted, the
  terminal driver delivers that interrupt to every process in the foreground
  group, and the dashboard held the process-wide interrupt context, which ends a
  Bubble Tea program wherever it is. Bubble Tea already ignores signals while it
  has released the terminal — Feat's second handler was the one that did not know
  the dashboard was not in charge. Reported by the maintainer during dogfood and
  reproduced on a pseudo-terminal.

  The dashboard's lifetime becomes its own, and an exit produced by an interrupt
  stops being reported as a failure — leaving the logs was raising an error
  banner for a key the user meant to press. See ADR-049.
- Reconcile the agent's execution environment against the session that owns it,
  so that the recovery reconciliation recommends is one resume will accept. A
  devcontainer that dies leaves its task with no route back. `reconcileTerminals`
  marks a process stopped only when the *terminal* is missing, and a tmux window
  outlives the container it ran a command in; `reconcileEnvironments` observes
  the container and records what it saw on the execution record without touching
  the session's process state. So the record goes on claiming a running agent
  indefinitely, and the same pass produces both halves of a contradiction: the
  finding says "the agent container exists and is Exited (137); Feat did not
  restart it — resume the task to start it again", and `resumable` then refuses
  that resume with "the agent session of task X is running in a terminal that is
  still there, so there is nothing to resume. Attach to it instead". The only way
  out today is to kill the task's tmux window by hand on Feat's own socket.

  Found by the maintainer while dogfooding the control-workspace fix: a
  jobharbor-dev task's devcontainer exited 137 and nothing in the product would
  bring it back. It is worth stating in the same change that a devcontainer has
  no lifecycle of its own — it comes up as part of a launch or a resume and
  nowhere else — because the first thing a user reaches for is a verb that starts
  it, and `runtime` is the application's, not the agent's.

  This is not the automatic restart FR-STATE-004 forbids. Reconciliation should
  still report rather than repair; what has to change is the record it leaves,
  because a session whose container is gone is not running, and saying so is what
  makes the recovery it already recommends available to the user it recommends
  it to.

  Assessed and widened to the surface, because the dead end above is the visible
  end of one story rather than a defect of its own. The maintainer asked why a
  devcontainer has no management at all, when the application runtime beside it
  has six verbs — and the answer was three things at once. The record was wrong:
  observing the container wrote to the execution record and left the session
  claiming a running agent. The guard was wrong: `resumable` read liveness as the
  *existence* of a tmux object, and Feat's own `remain-on-exit` keeps a dead pane
  reported. And the surface was missing: `Resume` had no command at all, so the
  action reconciliation recommends was reachable only from the dashboard, and
  `feat task cleanup` was the only way to stop a container — which is to say the
  only way to free a machine overnight was to destroy the task.

  One container per project was evaluated as the alternative and refused: the
  generated override replaces mounts *by container path* with this task's
  worktrees at this task's access, so which worktree is at a given path is the
  task's identity written as container configuration (ADR-033). A `start` verb was
  refused too, because a container with no session behind it is the class a
  failed launch already leaves and cleanup already cannot resolve. What lands is
  one invariant — an agent process cannot be alive while its environment is not
  running — and one pair of verbs, `feat task resume` and `feat task stop`. See
  ADR-057.
- Make a launch that fails after its container exists recoverable, in the three
  places it currently is not. They are one story — the container outlives the
  request that created it — and each half was found by the maintainer while
  dogfooding the mount-and-socket rules, on a jobharbor-dev task
  (`d7f54fa5`) whose devcontainer Compose file had been given a mount of the
  home directory on purpose.

  The launch never reached the check it was meant to trigger. `POST
  /v1/task-drafts/{id}/launch` failed after 10.018 seconds with `running
  /usr/local/bin/docker: context canceled`, because `internal/client`'s
  `requestTimeout` is ten seconds and the client cancels the request the daemon
  is still serving. The daemon's own patience is three minutes
  (`defaultReadyTimeout`), which is the honest budget: a launch that has to
  create a container is not a request that answers in ten seconds, and this one
  did not because the edited Compose file changed the service's configuration
  hash and the container had to be recreated with a new host share. The five
  launches before it took between 0.46 and 3.13 seconds, so the ceiling is
  invisible until the day a project's own file changes. A launch needs a
  timeout of its own, or a shape that does not hold a request open while a
  container is built.

  The same ten seconds reached the runtime endpoint next, on the first
  `runtime/start` of a task whose images nobody had pulled yet, and that half is
  fixed: an action's budget is a term of the endpoint's contract
  (`api.RuntimeTimeout`), the daemon bounds the action with it, and the client
  waits for it plus a margin (ADR-034 evidence 14). What that leaves for this
  entry is a launch giving itself the same treatment — the daemon's own budget
  there is `defaultReadyTimeout` and nothing declares it to a client — and the
  two paragraphs below, which are about what an interrupted launch leaves behind
  rather than about how long anybody waits.

  The timeout half is now done, with the lifecycle work above and for the same
  reason: a resume runs the whole launch path, so shipping `feat task resume`
  onto a ten-second budget would have been shipping this defect on a new command.
  `api.AgentTimeout` is three minutes, matching the daemon's own patience for a
  container; the daemon bounds a launch, a resume, and a stop with it, and the
  client waits for it plus the margin. What is left of this entry is the two
  paragraphs below (ADR-057).

  What the cancelled launch left behind, nothing can now remove. The container
  it had already created is still on the machine, exited, with its network
  beside it; the archived task record has `session: null`, because a failed
  launch clears the session it had recorded before creating anything; and
  cleanup resolves a task's resources from that record, so it plans nothing and
  reports success. The Compose project name is derivable from the project and
  task identifiers, and the containers carry `dev.feat.*` labels, so the
  resources are discoverable — reconciliation finds this class for tasks that
  have a session and has no home for the ones that do not (F2-15). Widening the
  launch refusals makes this more frequent rather than less: every rule added in
  `fix/mount-and-socket-rules` fires after the container is up.

  And cleanup removes the control workspace without establishing that no
  container still mounts it. The first `cleanup/execute` failed with `unlinkat
  …/control/jobharbor-dev/d7f54fa5-…/outbox: permission denied` while the
  container was still running; the second, after it had died, succeeded. On
  macOS the file-sharing layer holds a directory that is an active bind-mount
  source, so this is an ordering rule rather than a permissions bug: destroy the
  containers, establish that they are gone, and only then remove the tree they
  mounted. It became reachable when the control workspace stopped being one
  mount and became three (ADR-032's read-only split).

  Both are now done, and they went together because the second cannot be
  established without the first: a guard that asks the record which containers
  hold the workspace finds nothing for exactly the task that has one. What lands
  is one route rather than two — the agent's Compose project addressed by its
  derived name, which observes and removes and can never create — so
  reconciliation reports what a failed launch left, cleanup plans it, archiving
  is refused over it by the rule that already existed, and the control workspace
  is removed only once Docker has said the containers are gone. Measured against
  real Docker rather than reasoned about: `--project-name` with no `--file` finds
  an exited container and its network and removes both. See ADR-059.

  What stays open is a task an earlier build archived over resources nobody could
  see. Cleanup refuses an archived task and reconciliation skips one, so those are
  removable by hand and by nothing in the product; it is a state no build from
  here on can create.
- Say why a task failed where a user is looking. Reported by the maintainer while
  exercising the refusals above: the dashboard shows `workflow failed` and
  nothing more. The reason was recorded all along as the detail of the workflow
  transition, and it was reachable from nowhere — the event log is a file on
  disk, the launch's error banner is cleared by the next key, `api.Task` had no
  field for it, and the dashboard drops the detail its own event stream carries.

  It is every failed task rather than every failed launch: all five paths into
  the state go through one call and lost the reason the same way. The task
  carries it now, recorded by the transition so the state and its explanation
  cannot be written apart, discarded when the task leaves `failed` so a recovered
  task stops explaining what it recovered from, and printed under the workflow on
  the panel. Stored as an optional field at the same schema version, which is
  what the codec's compatibility rule allows and what keeps this free of a
  migration. See ADR-060.

  What it does not do is make a task's history readable. The event log is still a
  file nothing in the product opens, and reconciliation still has no surface
  outside the dashboard — `feat --help` has no reconcile command and
  `feat daemon status` prints no findings, so the pass that exists to make a
  half-created task recoverable is reachable from one screen and from nowhere a
  script or a terminal-first user would look. Both are recorded here rather than
  fixed: they are a new endpoint and a new command, and slice 13 is closing
  defects rather than adding surface.
- Read a dead pane the way the tmux Feat runs on defines it. Linux CI failed
  where macOS passes: `process state = "stopped", want failed` with no exit
  status, from the regression test that exercises an agent binary which exits at
  once.

  It is a version difference with a product defect behind it. On tmux 3.4, which
  is what `apt-get install tmux` gives an Ubuntu runner, `pane_dead` is the
  pane's closed file descriptor alone, while `pane_dead_status` waits for the
  flag tmux sets once it has reaped the child; tmux 3.7 made `pane_dead` require
  the same flag. So on the older one a pane reports itself dead before tmux can
  say how it ended, and Feat read the absent status as a clean exit — which is
  also what it did for an agent killed by a signal, where the status is absent
  for good and `pane_dead_signal` carries the answer. An agent the OOM killer
  takes is the ordinary way that happens inside a container.

  A pane is therefore dead when tmux can say how it ended, by a status or by a
  signal, which is tmux 3.7's own definition derived rather than depended on. The
  race was not reproduced on this machine — five runs of the test against tmux
  3.4 on Ubuntu, and a direct probe of the three format variables, always found
  the child already reaped — so what is recorded here is the mechanism from
  tmux's own source and a failure signature that matches it exactly. If CI fails
  again it will fail differently, and that difference is the next piece of
  evidence.

  The change also caught a near miss of its own: the parser padded pane lines to
  a field count written beside the format rather than derived from it, and both
  test fakes happened to emit the wider line, so a format that gained a field
  would have panicked in front of a user and not in a test. The counts come from
  the formats now.
- Give the dashboard the design its shape was waiting for. ADR-041 decided where
  the regions are and left what they look like to whatever each screen was
  written with, and the maintainer read the result in use: the selection colour
  and the attention colour are not from one family, every project header offers a
  fold that does nothing, neither region's header is separated from its content,
  and the footer is not separated from either.

  Each is a rule the dashboard did not have. The palette becomes six named
  colours chosen as a set, with the accent and the amber at the same weight so
  that the difference between them reads as meaning rather than as loudness. Each
  region becomes a card — a rounded box, a header of its own, a rule under it —
  with a blank column between the two rather than a drawn one, and the footer
  ruled off from both. The rail's heading and the tab bar become those headers and
  each gains a summary on its right. `space` folds a project, which is what its
  marker has been promising since the rail was written, and a folded project
  keeps saying how many tasks it holds and whether any of them wants the user. The
  box is drawn a line at a time rather than by lipgloss, because the main region
  holds a rendered tmux pane and a re-flow through a layout engine cuts escape
  sequences in half. See ADR-051.
- Make the dashboard's cleanup screen an inventory rather than a list of names.
  Reported as "cleanup is only executable from the CLI", which is not literally
  true — the screen has been on `C` since slice 12 — and is what using it leads
  a person to conclude, for two reasons.

  It drew each target's identity and nothing else. The plan carries a sentence
  per target and `feat task cleanup` has printed it since the command existed, so
  the dashboard said `@3`, a volume name beginning with a Compose project, and a
  path, where the command said what each of them was. The plan's `workflow` is
  documented as being there "so a screen can say what is being cleaned up" and no
  screen said it. And a class's warnings are the distinct set of its targets', so
  a class of three worktrees with one dirty one said only that a worktree had
  uncommitted work, without saying which — the exactness FR-CLEAN-001 asks for,
  lost in the summarising.

  It also could not be read. The overlay clamps what does not fit and leaves a
  note counting the lines it dropped, and nothing moved the window: the six
  classes of a task with three repositories are about forty lines with the detail
  restored, against twenty in the dialog on a terminal at the layout's minimum.
  A user could move the cursor onto a class the screen had never drawn and select
  it. So the class list scrolls, following the cursor as it moves and on the page
  keys the task panel already uses, and it says what is above and below it in the
  words `taskBody` uses for the same thing.

  The screen's own hint line was ninety cells inside a dialog that is sixty-eight
  on that terminal, so its last two hints were truncated away. It is the frame's
  key style now, short enough to survive, and the page keys are named in the
  scroll note rather than in it — the one place they are worth naming is when
  there is something to scroll to.
- Ask the cleanup screen's question once, where the consent is given. Reported as
  the dialog being clunky: the extra confirmation and the extra archive button.

  It asked twice. Ticking a class with warnings raised a `y/N` that took the
  keyboard there and then, and `enter` raised another — three risky classes was
  four questions interleaved with the three ticks. The first of those bought
  nothing: the request carries the plan's own warning strings whatever was
  accepted, so ADR-037's defence against a stale confirmation is the daemon's
  comparison and one question sends what two sent. And since the inventory change
  above, the modal was reading back a line already on the screen behind it.

  So one confirmation, at the removal, naming the classes and listing every
  distinct warning of everything selected — the question first, because a region
  too small for both must keep the line that says what answering does. The
  warnings stay beside their resources and the class title carries a marker, so a
  class does not read as free once the window has scrolled past them. The command
  keeps its sequence: a terminal prompt has nothing to tick, so there the
  question is the selection rather than an interruption of it.

  The archive choice had a key of its own, `A`, which made it the one checkbox
  the cursor could not land on and a key that did nothing for most of the
  interaction. It is a row now, reached and ticked the way every other choice on
  the screen is. It was also rendered only once every class was selected, and it
  sits under the inventory that is sized by what the tail takes — so ticking the
  last class moved the list it was ticked in, and blinked a cursor stop into
  existence. It is drawn throughout, greyed and saying what it waits for. The
  rule it waits for is unchanged.

  And `r` looked dead, which asking what it was for showed to be the right
  reaction: it always asked the daemon and always replaced the inventory, but on a
  task nothing had touched it redrew an identical list, and it cleared the status
  line while doing so. The case it existed for is real and narrow — a task being
  cleaned up often still has an agent working in its worktrees, so a worktree that
  was clean when it was ticked can be dirty when enter is pressed, and the daemon
  refuses that as a warning the user was never shown. But that case is answered by
  looking when the answer matters, not by a key somebody has to know to press.

  So enter resolves before it asks, and `r` is gone. A cost that moved asks
  anyway, with the new warning in the question. A resource gained or lost does
  not: the confirmation names classes, so a class that grew a third worktree would
  be confirmed by a user who read two — the inventory is replaced and the question
  waits for another enter. The plan has carried the moment it was resolved since
  the endpoint existed and nothing displayed it; the screen says it now, because a
  dialog left open for ten minutes is an observation and not a live view.

  And a finished cleanup closes the dialog. It is a transaction the user opened
  and the transaction is over; what stayed open was a screen about a decision
  already taken, over an inventory of what was left rather than what had been
  asked about — and for an archived task, over one the daemon refuses to resolve
  again. What it did becomes a line of the footer, naming the classes chosen and
  counting what was already gone. A cleanup that failed halfway keeps the dialog
  and re-reads the plan, because there the screen is the only account of what
  happened and the inventory on it describes a machine that no longer exists. See
  ADR-061.
- Let a project be configured by answering questions. Adding a project means
  authoring its YAML by hand, copied from a 176-line example, with `feat doctor`
  as the only feedback loop — and it is the first thing anybody does. Found by
  adding a second project to a machine that already ran Feat.

  Most of the file is not a decision. The working-tree root, the remote, the
  default branch, the Compose files beside a checkout, and the services they
  declare are facts a tool can read, and retyping them is how a configuration
  acquires a value that was never true. What is left is six questions.

  `feat project init` asks those, derives the rest, renders one draft, and
  parses, resolves, and validates it before displaying it: what the user
  confirms is a configuration Feat accepts. Nothing is written until they
  confirm, an existing file is never overwritten, and diagnosing and registering
  stay the commands they already are — the wizard offers each at the end and
  calls exactly those. Writing the file by hand is unchanged.

  Pulled forward from slice 14, which made it conditional on manual
  configuration being the dominant public blocker: dogfooding answered that, and
  the first-run path is not one the public milestone should meet for the first
  time. See ADR-062.
- Ask the same questions from the dashboard. `feat` with no subcommand opens on
  a machine with no project as readily as on one with twelve, and the only thing
  the dashboard had to say to the first was a command to quit and go and type.
  Preparing a task — the key a new user presses first — could only fail there.

  The questions move to `internal/wizard`, which owns the sequence, the
  proposals, the validation, and what an answer decides about the next question;
  it reaches the machine through a Host the command implements. `feat project
  init` drives it as the conversation it already was, and `p` in the dashboard
  drives it as a dialog with a cursor, a step back out of an answer, and the
  composed file scrolled before it is written. Neither owns a question. See
  ADR-063.
- Show what `feat doctor` found on the dashboard. Configuring a project from the
  dashboard could not say whether the project worked, and that is where a first
  project fails: a Compose service that is not there, an agent that is not
  installed, a remote that does not resolve. The checks run in this process and
  arrive as data, `D` opens them for the selected task's project, `r` runs them
  again, and the wizard runs them itself once the file exists. See ADR-064.
- Remove hard-coded assumptions discovered during dogfood.

### Acceptance criteria

- A project can be configured without authoring YAML by hand: what the host can
  answer is derived rather than asked, the composed configuration is validated
  before it is displayed, nothing is written until it is confirmed, and an
  existing configuration is never overwritten.
- The same questions are reachable from the dashboard without leaving it, from
  one implementation both askers drive; an answer can be stepped back out of, the
  whole file is shown before it is written, and cancelling leaves the machine
  unchanged.
- What `feat doctor` checks can be read on the dashboard, for a project or for
  the machine, on demand and never on a timer; the report says which environment
  it is true of, and a skipped check is neither shown nor counted as a pass.
- Full v0.1 acceptance checklist passes.
- No unresolved data-loss or cross-task runtime defect remains.
- Every notifiable condition has been shown to reach a real desktop once, from
  the state change that produces it, and each policy that drops one drops it for
  a reason a user would recognise.
- A task can be named by what a user can see, and a name that matches two tasks
  is reported rather than resolved to either.
- A task whose agent container died can be recovered from the product, without
  reaching for tmux: what reconciliation reports about it and what resume will
  accept agree.
- The environment a task's agent runs in can be stopped and brought back from
  both the command line and the dashboard, without removing anything the work
  lives in and without a verb that starts a container no session owns.
- A launch that fails after its container exists leaves nothing the product
  cannot see: the client does not cancel a launch the daemon is still serving,
  the container and network are removable by name from the task that created
  them, and a cleanup that has to remove a control workspace establishes first
  that nothing still mounts it.
- Every command that takes a task can be found from `feat task --help`, no alias
  carries a second implementation, and the golden file, the specification, and
  `feat --help` describe the same surface.
- Three concurrent tasks are legible at once on an ordinary terminal, no line
  wraps at the supported width, and every field FR-UI-002 requires is reachable
  without leaving the task that was selected.
- What a task owns can be read as well as removed from the dashboard: the
  screen's inventory says everything `feat task cleanup` prints about each
  target, a warning true of one resource of several is drawn beside that one,
  and a plan taller than the terminal is scrolled to rather than clipped.
- Removing from the dashboard is one question, asked when the removal is
  requested rather than as it is assembled, carrying every warning of everything
  selected; it stays legible on a terminal at the layout's minimum width, and no
  key the screen offers is one the outstanding question has taken.
- Every choice on the cleanup screen, the archive included, is reached with the
  cursor and taken with the same key.
- The cleanup screen says the moment its inventory was taken, and the removal is
  confirmed against a plan resolved when it was asked for: a warning that appeared
  while the screen was open is in the question rather than in the daemon's
  refusal, and a resource that appeared is read before it can be confirmed.
- A cleanup that finished leaves the dashboard showing what it did; one that
  failed halfway leaves the screen that explains it, over an inventory read after
  the attempt rather than before it.

## Slice 14 — Public v0.2

### Outcome

A new macOS/Linux user can use Feat outside the reference project.

### Work

- Implement host-native execution.
- Add Linux notification support.
- Generalize examples and troubleshooting.
- Finalize JSON Schema and shell completion.
- Give the reading commands machine-readable output. Every command prints a
  table a person reads and nothing else can parse, so a user scripting around
  Feat has the socket or screen-scraping. `task list`, `task review`,
  `runtime status`, and `project show` are the ones with something to say. It
  belongs here rather than with ADR-040 because the schema this publishes is the
  one this slice finalizes.
- Add release binaries, Homebrew formula/tap, and `go install` instructions.
- Add contribution/security policy, including the known security limitations:
  what a standard container does and does not protect against, that Feat claims
  no hostile-kernel isolation and no network data-loss prevention, and that full
  Git and provider CLI access are capabilities a project grants deliberately
  (docs/05-security-model.md).

  Moved here from slice 13, where the v0.1 acceptance criteria never asked for
  it. The reader of "what this does not protect against" is somebody deciding
  whether to run Feat on their own work, which is a person this milestone
  introduces and the dogfood milestone does not have.
- Document the path from a clean installation to a first running task, and check
  it on a machine that has never run Feat.

  Also moved from slice 13. Reproducing a setup from documentation is a
  public-v0 property — [08-v0-scope.md](08-v0-scope.md) puts it in the definition
  of done for public v0 and not in the v0.1 acceptance criteria — and it is best
  written against what the dogfood runs turn out to need rather than before them.
- Verify no telemetry.
- Revisit the onboarding wizard against what public users hit. Slice 13
  delivered `feat project init` (ADR-062) once dogfooding showed manual
  configuration to be the hardest step; what is left here is whatever a machine
  that has never run Feat turns out to need, which the first-task documentation
  above is written against.
- Add Shortcut only if all core reliability work is complete.

### Acceptance criteria

- Public-v0 definition of done passes on macOS and Linux.
- Host-native and devcontainer modes use the same task domain.
- Installation and first-task documentation is reproducible on a machine that has
  never run Feat.
- The known security limitations are stated where somebody deciding to run Feat
  on their own work will read them.

## Implementation discipline

- Keep changes within the current slice unless a prerequisite is proven missing.
- Prefer fake adapters and deterministic integration harnesses before requiring real Docker/tmux in every unit test.
- Add opt-in end-to-end tests for real Git, tmux, Docker Compose, and Claude.
- Never expose Docker to the agent to simplify implementation.
- Never replace structured agent events with terminal scraping as the semantic source of truth.
- Update the decision log when evidence changes an accepted design.
