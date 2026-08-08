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
- `agent.capabilities.docker`, `.network`, and `.git` accept one value each — `denied`, `unrestricted`, and `full`. Recording another would be a promise the binary does not keep. The declaration is still worth making, because slice 8 checks the running container against it. `github_cli` and `gitlab_cli` keep the documented three levels. Amended by ADR-032: the provider-CLI checks arrive with whichever slice can reach the environment that runs them, so slice 7 delivers them for host execution and slice 8 for devcontainer execution.
- `feat doctor` runs `docker compose config --services` and never plain `docker compose config`. Environment files are examined by path and metadata only. Secret values never appear in diagnostics because they are never read, which is a property of the data rather than a filter over the output, and a test uses an unreadable environment file so that a future change cannot pass by accident.
- A path template is checked against its fixed leading directory as well as against what it expands to, and every template that names a per-task resource must contain `{task_id}` or `{task_key}`. Placeholder vocabularies are closed: an unknown placeholder is rejected rather than left to survive into a branch name, a path, or a command argument.
- Project configuration is resolved against the environment of the process that reads it: the daemon's own for registration, the client's for `feat doctor` and `feat project show`. `internal/daemon` gained a `paths.Environment` option so that this is explicit rather than ambient.
- The JSON Schema in `schema/feat-project.schema.json` is hand-written and kept in step with the Go types by a test that compares field names in both directions. Slice 14 finalises it. `docs/examples/project.yaml` is validated by the test suite, so the file a new user copies cannot drift from what Feat accepts.
- `POST /v1/projects/{project_id}/doctor` from [06-technical-architecture.md](06-technical-architecture.md) is deferred to the slice whose TUI reads it, for the reason ADR-027 deferred `daemon.json`: an endpoint with no reader is a compatibility surface with no user. `feat doctor` covers the command surface today.

Consequence: registering a project is the first write the local API carries, so the slice 2 acceptance criterion that the daemon is the only state writer — which ADR-027 recorded as structurally verified — is now checked behaviourally as well, by a test that registers through the socket and finds the snapshot in the daemon's state directory.

Slice 3 cannot verify its own second acceptance criterion, that the company project configuration validates on the target machine, because that needs the reference project and the machine it lives on. The criterion is verified by running `feat doctor` there, and slice 3 is not complete until that has been done.

The user-visible changes are the `<project>` argument on `feat project add`, and `feat doctor` exiting 1 when it finds an error. Package layout gained no new package. [07-configuration-model.md](07-configuration-model.md) and [06-technical-architecture.md](06-technical-architecture.md) were updated in the same change.

### ADR-029 — Git adapter boundary, preparation order, and path safety

Status: accepted  
Recorded: slice 4

Evidence found while implementing the Git and worktree lifecycle:

1. The slice 4 acceptance criterion that a failure halfway through creation leaves a recoverable record needs a writer, and the daemon is the only one (ADR-008). The Git adapter cannot write it, and slice 6 owns the draft API that would normally call both.
2. Generating a branch name needs the placeholder vocabulary that `internal/config` validates. Putting expansion in the adapter would duplicate the vocabulary; putting the adapter behind the configuration types would make the Git CLI adapter depend on the shape of a YAML file.
3. `git.worktree_root` names the directory holding a task's worktrees, and its documented placeholders include `{repository_id}`. A template that uses it expands to one directory per repository; a template that does not expands to one directory for all of them, and the second worktree would then fail on the first one's files.
4. A record of what a task owns can be edited, restored from a backup, or written by an older version. Cleanup reads it to decide what to remove.
5. FR-GIT-001 requires a fetch "when network access is available", which does not say what to do when it is not. Failing task creation because a laptop is offline would make the last fetched state unusable; using it silently would let a user believe their base is current.
6. `TaskRepository` has no field saying whether its worktree exists. Adding one would be a change to a stored format that slice 1 pinned with golden files and a migration policy.
7. Repository names, remotes, and branches come from configuration and reach an argument vector. Nothing is handed to a shell, but Git still reads an argument beginning with `-` as an option, and `--upload-pack=...` in place of a remote name is a command of somebody else's choosing.

Decisions:

- `internal/git` is the adapter and imports only the standard library, `internal/domain`, and `internal/paths`. A `git-stays-an-adapter` `depguard` rule denies it `internal/config`, `internal/store`, and every other package; ADR-025 requires an ADR for a boundary rule, and this is that record.
- `internal/config` gains `Expand`, `Values`, `Uses`, `Slug`, and `StaticPrefix`, so the closed placeholder vocabulary has one owner. The daemon expands templates and hands the adapter final names.
- Task preparation is plan, record, apply, in that order, and the daemon owns it: `service.PrepareTask` plans, writes the plan onto the task, leaves draft, and then creates one repository at a time, recording each before the next begins. Slice 4 adds no endpoint and no command; slice 6 confirms a draft by calling this. The ordering is the criterion: every path and branch that could exist afterwards is written down first, so no resource can exist that the record cannot name, and an interruption at any point is recoverable rather than mysterious.
- A failure part way through is left in place and the task becomes `failed`. Nothing is rolled back, because a worktree that was created may already have been mounted, entered, or written to, and tidying up a failed launch is a destructive act the user did not ask for. `failed` has recovery edges to `preparing` and `working`.
- Whether a worktree exists is observed rather than stored. The planned branch and path are desired state; `GitObservation` is what Feat saw, and a repository with no observation is one nothing has confirmed. No field is added to the stored format, and reconciliation asks Git rather than trusting a stored flag, which is what CLAUDE.md means by never assuming persisted desired state equals observed state.
- A worktree root that does not name the repository gets the repository identifier appended; one that does is used as it expands. Both readings then produce one worktree per repository.
- Every worktree path must be absolute, written cleanly, strictly inside the fixed leading directory of the configured root, outside every repository checkout, and not a shared system directory — checked after symbolic links are resolved as far as the path exists, so a link cannot move a task's directory somewhere Feat would never have accepted. Cleanup applies the same check to a recorded path and refuses the target rather than removing it: the moment a path from a record decides what gets deleted, the record has stopped being a record and become an instruction. The shared-directory list moves from `internal/config` to `internal/paths`, so the package that validates a configured root and the package that removes directories under it use one list.
- A fetch is best effort: a failure becomes a note on the plan, the base resolves from the last fetched state, and the user is told the base may be stale. A missing remote-tracking ref is still an error, and it names `git fetch <remote>` as the remedy. The command is plain `git fetch <remote>`, without `--prune`, `--all`, or `--tags`, each of which changes refs Feat was not asked to change.
- A fetch runs only for the `remote` base policy. Fetching cannot change what a local, current, or explicit policy reads, and a network call whose result nothing depends on is one the user did not ask for.
- Collisions — an existing branch, an occupied path, a path Git has already registered — are reported at plan time and never resolved by choosing another name. A branch Feat renamed is a branch the user did not agree to and will look for under the name they saw.
- Remotes, branches, and refs are rejected when they begin with `-` or contain a control character, rather than relying on every Git subcommand to honour `--`.
- A repository the project configures as `read_only` cannot be promoted to read-write by a task. Taking less access than the default is always allowed; taking more is not.
- Cleanup plans are produced and never executed in slice 4. There is no execution path and no plan token, which slice 12 owns.

Consequence: slice 4 has no user-visible surface. The command surface, the API, and the golden files are unchanged, and `PrepareTask` has no caller in production until slice 6 confirms a draft with it. That is the opposite of the reasoning ADR-027 and ADR-028 used to defer `daemon.json` and the doctor endpoint, and the difference is that those are published compatibility surfaces with no reader, while this is orchestration whose absence would leave a slice 4 acceptance criterion verifiable only against a fake.

Slice 2's structurally verified event-ordering criterion becomes partly behavioural: preparation is the first production code that appends to a task's event log and publishes to the stream.

The four acceptance criteria that are really about Git's behaviour are verified against real repositories in opt-in tests named `TestReal…`, which CI runs on macOS and Linux; the unit tests use a fake runner and pin the argument vectors.

### ADR-030 — tmux identity, command ownership, and attach boundary

Status: accepted
Recorded: slice 5, before implementation

Evidence found while planning the tmux backend:

1. Slice 5 must launch placeholder commands and open a shell in the same execution environment, while the devcontainer execution adapter that builds those commands arrives in slice 8. If tmux learned Compose or host-execution details now, the terminal backend would become a second execution-environment adapter.
2. [06-technical-architecture.md](06-technical-architecture.md) says tmux sits behind `internal/execution`, but that package's documented contract owns environment validation, preparation, command construction, observation, and destruction. tmux owns terminal persistence and attachment instead; making either implement the other would combine two lifecycles that later slices need independently.
3. The CLI is mechanically denied access to `internal/tmux`, while the API explicitly includes an `attach-info` endpoint. Native tmux attachment also has to inherit the client's terminal rather than the daemon's streams.
4. A user's tmux configuration may change `base-index`, `pane-base-index`, automatic names, and hooks. Numeric indexes and display names therefore cannot identify anything Feat intends to recover.
5. An empty tmux server exits by default. Starting a server before it has a task session creates no useful durable state; creating the first project session is the operation that should start it.
6. The daemon socket and tmux socket have different owners and lifetimes. The daemon socket disappears when the daemon stops; the tmux socket must remain while task terminals do.
7. A command that creates a window can succeed before the state snapshot is written. If the window is tagged first and persistence then fails, startup discovery can still associate it with the exact task; deleting it as rollback could destroy work already entered in the pane.

Decisions:

- The Feat tmux server uses the explicit socket `<runtime>/tmux.sock`. Every adapter and attach invocation supplies `tmux -S <socket>`; no operation reaches the user's ordinary tmux server. The socket remains when the daemon stops and moves with `FEAT_RUNTIME_DIR`.
- tmux is its own adapter. It accepts an opaque argument vector and an absolute working directory from its caller. Later host and Compose execution adapters construct those values; tmux does not import configuration, agent, execution, Compose, or storage packages.
- `internal/tmux` imports only the standard library and `internal/domain`. A depguard rule makes that boundary mechanical, as ADR-025 requires for every architectural import rule.
- Feat does not pass `-f`, so tmux loads the user's normal configuration. It then applies only the options required for ownership and persistence. Display names remain conveniences and may change.
- Managed sessions, windows, and panes carry versioned `@feat_*` user options. Discovery requires matching metadata at all three scopes and uses tmux's immutable `$session`, `@window`, and `%pane` identifiers as execution references. Missing, conflicting, or duplicate metadata is reported rather than guessed through a name or index.
- Creation tags the returned pane, window, and session before the daemon records the target. A failure while tagging removes only the exact object just created; a failure after tagging leaves it for reconciliation rather than rolling it back.
- The adapter observes only whether a pane process is alive and, when tmux retains a dead pane, its exit status. It never parses terminal output or infers semantic completion.
- The daemon owns terminal creation and reconciliation because it is the only persistent-state writer. tmux-specific reconciliation runs on daemon startup and updates terminal references and process observations. Slice 12 still owns reconciliation across tmux, Git, Compose, control messages, and review as one recovery workflow.
- `POST /v1/tasks/{task_id}/attach-info` returns the stable socket/session/window/pane target. The CLI invokes the native tmux client itself, with its own terminal streams, and waits for detach. It does not import the adapter. The shell action remains an adapter operation taking a resolved command; slice 6 connects that operation to the TUI/API after the task-launch caller exists.
- The first project session starts the dedicated server. Discovery treats a missing socket as an empty managed server rather than an error; every other tmux failure remains actionable.

Consequence: slice 5 does not pull the devcontainer or Claude adapters forward. Its production surface is native attachment to an already recorded task terminal; its creation and shell orchestration are the tested seams slices 6 to 8 call with final command specifications.

Amended after the slice 5 review, with evidence measured against tmux 3.5a:

8. A pane created together with its command dies before any option can be applied when that command exits immediately. For the first task of a project the session and the whole dedicated server go with it, and the adapter reports "the dedicated tmux server is not running"; for a later task, tagging fails with "no such pane" and the cleanup that follows fails with "can't find window". A missing or misconfigured agent binary is the ordinary way to produce this, and slice 6 supplies the first real command.
9. Discovery aborts for the whole server when any tagged object is inconsistent. One task window whose agent pane was killed while its shell pane survived makes `EnsureTask` fail for every unrelated task and stops startup reconciliation before it reaches any task at all. A future `@feat_schema` value has the same effect on an older daemon.
10. `CommandSpec` validates its program and arguments against the separators discovery parses, but not its working directory. That directory is the one caller-supplied value tmux reports back, as `#{pane_current_path}` inside a tab-separated list format.

Decisions:

- Panes are created without a command and tagged before the caller's program replaces the holder shell through `respawn-pane`. Ownership and `remain-on-exit` are then already in effect, so a program that exits at once leaves a dead pane carrying its exit status, which discovery reports as a failed process and reconciliation can explain. A failure to start the program removes the exact object just created: the retention rule above protects work entered in a pane, and a holder that never ran the command has none.
- Whole-server discovery failure stands for slice 5 and is decided in slice 12. Quarantining a damaged terminal so that healthy ones stay usable is a reconciliation-wide policy, not a tmux one: worktrees, Compose projects, and control messages raise the same question, and answering it inside one adapter would set a precedent the others would have to follow without the slice that owns recovery having chosen it.
- Working-directory validation is decided with it. Its failure mode is the blast radius above — a tab in a path misaligns the pane fields and breaks discovery for every terminal — so quarantine changes what the fix has to achieve. Until then the gap is recorded rather than closed, because no v0 code path produces such a directory: worktree paths come from validated project configuration.

The Slice 3 target-machine acceptance check remains outstanding in this repository. Slice 5 proceeds by explicit maintainer approval despite that status; the discrepancy is recorded rather than changing Slice 3 to complete without its missing evidence.

### ADR-031 — Task drafts, confirmation, and the first user-facing task lifecycle

Status: accepted
Recorded: slice 6, before implementation

Evidence found while planning task preparation and the initial TUI:

1. The slice 6 acceptance criterion "confirming launches the previously displayed snapshot" is not satisfied by planning at confirmation time. ADR-029 made preparation plan, record, apply in one call, and a `remote` base policy fetches inside the plan. Between the screen the user reads and the key they press, a fetch can move a remote-tracking ref, and the task then starts from a commit nobody was shown. The failure is silent, which is what makes it worth designing against rather than documenting.
2. [06-technical-architecture.md](06-technical-architecture.md) names `POST /v1/task-drafts`, `PUT /v1/task-drafts/{draft_id}`, and `POST /v1/task-drafts/{draft_id}/launch` without saying what a `draft_id` is, and lists no way to abandon a draft. A draft that cannot be cancelled is a record that accumulates, and criterion 1 is about cancelling one.
3. The Claude adapter arrives with slice 7 and devcontainer execution with slice 8, but slice 6 must connect the attach and shell actions, which need a live pane. Nothing in slice 6 can start an agent.
4. `domain.WorkflowWorking` is documented as "a task with a running agent session". A launch that reached it while the pane held a shell would record a claim about the world that is not true, which is the same defect shape ADR-026 pinned the transition table against.
5. FR-TASK-003 requires the draft to show resolved bases, proposed branches, worktree paths, and an editable brief. Resolving a base needs Git, and Git runs in the daemon, so a draft cannot be a client-side object that is posted once at the end.
6. `internal/ui` is denied `os/exec` by the `process-execution-stays-in-adapters` rule, and the three things the dashboard must launch — native tmux attach, a task shell, and `$EDITOR` — all have to take over the terminal Bubble Tea owns.
7. A draft's shape is exactly what `Task` already makes mutable in `draft` and frozen afterwards (ADR-026). A second draft entity would duplicate the repository binding, the base resolution, and the brief, and then have to agree with the first one.

Decisions:

- A draft is a task in `draft` state, and `{draft_id}` is its task identifier. Drafts are persisted, so several drafts and live tasks coexist across a daemon restart, and they appear in the task list as the drafts they are.
- Preparation becomes plan, confirm, apply. `POST /v1/task-drafts/{id}/plan` resolves every base and records the proposal on the draft while leaving it `draft`; `POST /v1/task-drafts/{id}/launch` carries a fingerprint of what was displayed and refuses to launch anything else. This amends ADR-029, which recorded that slice 6 would confirm a draft by calling `PrepareTask`; the plan, record, apply ordering that ADR-029 chose for recoverability is unchanged, and only the point at which the user's confirmation enters it moves.
- The fingerprint covers the task's frozen shape: title, brief, source, and each binding's repository, access, base ref, base commit, branch, worktree path, and container path, in canonical order. It is computed from the stored task rather than stored beside it, for the reason ADR-026 derived the task key from the task identifier: two records of one fact can disagree. No stored format changes, so no migration is needed.
- A mismatch is reported and never resolved. Feat does not silently re-plan, for the same reason ADR-029 does not silently rename a colliding branch: the user would act on a plan they never saw.
- Planning is its own request because it fetches. A network call against the user's repositories should follow a key they pressed, not a field they edited.
- Cancelling a draft archives it. `draft` to `archived` already exists and `Task.notReadyFor` already exempts archived, because a cancelled draft never had a brief, a base, or a session. Nothing is removed from disk; slice 12 owns archival storage.
- Launch opens the task terminal with the user's `$SHELL` in the primary task worktree and leaves the task `preparing`. The pane is real, so attach and shell are genuinely connected and slice 5's creation seam gets its production caller, and `preparing` to `working` remains the edge slice 7 takes when Claude actually starts. A devcontainer-mode project gets the same host shell, labelled as one: the honest report is more useful than refusing the action, and slice 8 replaces it.
- Fields the dashboard cannot fill yet — resource usage from slice 10, verification state from slice 7, change counts beyond the recorded observation — render as absent and name the slice that delivers them. This is the rule ADR-028 established for `feat doctor`: a value that was never measured is never displayed as one.
- The TUI hands native processes to `tea.Exec` as a `tea.ExecCommand`, which `internal/cli` constructs. `internal/ui` names no `os/exec` type, so the boundary rule stays mechanical rather than becoming an exemption.
- `feat implement` gains `--project`. The picker still appears when several projects are registered and no flag is given, and the flag pre-fills rather than making the command headless: confirmation is required before anything is created, so a terminal is required. The command surface changes as it did in ADR-028, and [README.md](README.md) and the golden file move in the same change.

Consequence: slice 6 is the first slice with a user-facing task lifecycle, so the event stream, the store, the Git adapter, and the tmux adapter all gain production callers at once. Slice 2's remaining structurally verified criterion — that state events arrive in order — becomes fully behavioural, because a draft created, planned, launched, and attached publishes the sequence a client reads.

Slice 6 starts no agent. Anything that would report one — a `working` task, a Claude session identifier, a control workspace — is absent rather than approximated, and slices 7 and 8 add them.

The Slice 3 target-machine acceptance check remains outstanding, as it did for slice 5. Slice 6 proceeds under the same explicit maintainer approval, recorded here rather than by marking slice 3 complete without its evidence.

### ADR-032 — Control workspace, agent boundary, and where Claude runs before slice 8

Status: accepted
Recorded: slice 7, before implementation

Evidence found while planning the control workspace and the Claude adapter:

1. The dogfood profile requires Claude inside the configured devcontainer, and slice 8 is what starts one. Slice 7 therefore has nowhere to run an agent except the host, while its first acceptance criterion is that Claude launches in the correct task working directory. ADR-031 met the same shape of problem by giving a devcontainer-mode project a host shell and labelling it, but an autonomous agent holding the user's host credentials is a different object from a shell the user opened themselves: the devcontainer is the boundary [05-security-model.md](05-security-model.md) exists to describe, and running the agent outside it is the one deviation the security model would not recognise as its own.
2. [07-configuration-model.md](07-configuration-model.md) and ADR-028 record that provider-CLI validation "arrives with slice 8", because it must run where Claude will run the command. Slice 7's work list and its sixth acceptance criterion require it. Both cannot be true.
3. [06-technical-architecture.md](06-technical-architecture.md) writes the control workspace as `<task>/control/` without saying which directory `<task>` is. The state directory holds `task.json`, `events.jsonl`, and `prompt.md`, and slice 8 mounts the control workspace into a container the agent writes to.
4. The security model requires a monotonically increasing sequence or a unique event ID, and processed-event tracking. `AgentSession.LastEventSequence` records how far Feat has read, but nothing records which identifiers were applied, and a host that dedupes by removing the agent's files needs write access to the directory the agent owns.
5. Hook delivery over a bind mount is not observable by inotify on macOS Docker Desktop, which is the slice 8 configuration. A watcher that works on the host and silently never fires in the container would move the failure to the slice where it is hardest to see.
6. Claude's own hook contract makes silence load-bearing. A `UserPromptSubmit` hook's standard output is injected into the model's context, and a `Stop` hook exiting 2 blocks the agent. A hook that prints or fails does not merely fail to observe the session; it changes it.
7. The brief can be up to 256 KiB. Passing it as the initial prompt argument puts it in the process's argument vector and therefore in `ps` output for every user on the machine.
8. Nothing in the plan says what happens when the agent process exits. `preparing` has no edge that an exit takes, so a task whose Claude died at startup would rest in `preparing` for ever.

Decisions:

- Claude runs where the project configures it. A `devcontainer` project keeps ADR-031's shell and names slice 8, and `FEAT_HOST_AGENT` in the daemon's own environment is the opt-in that launches Claude natively on the host instead. The opt-in is deliberately not a request field or a client flag: a request that moved the agent outside its configured boundary would be a caller granting itself a capability, which the local-API rules forbid. The daemon logs it at startup, health reports it, and a task launched under it says so. This is what makes the first and sixth acceptance criteria verifiable on the target machine before slice 8 exists.
- Provider-CLI validation runs in whatever environment this build can reach. For host execution that is the host, and the checks stop being skipped; for devcontainer execution they stay skipped and keep naming slice 8. [07-configuration-model.md](07-configuration-model.md) and ADR-028's bullet are corrected in the same change rather than left contradicting the slice.
- The control workspace is `<state>/control/<project-id>/<task-id>/`, a tree of its own. The directory slice 8 mounts into the agent container then holds the protocol and nothing else: no snapshot, no event log, no stored brief. [06-technical-architecture.md](06-technical-architecture.md) gains the resolved path.
- Host-written and agent-written parts of the workspace are separated, so slice 8 can mount them differently: `task.md`, `context/`, and `inbox/` are host-written and agent-read, `outbox/` and `reports/` are agent-written, and `agent/` is host-only and holds the generated settings, the hook scripts, the report helper, and the processed-event record. Dedup therefore never requires the host to write into the directory the agent owns, and the outbox stays an audit trail until slice 12 cleans it up.
- Delivery is polling rather than filesystem notification, for evidence 5. The interval is short and a daemon-side task action polls immediately, so the cost is bounded and the mechanism is the same on both sides of a bind mount.
- A control message that does not parse is retried for a bounded number of polls before it is recorded as malformed, because a write in progress and a malformed document are not the same thing and only one of them is the agent's mistake.
- Generated hooks write the provider's raw payload to the outbox and do nothing else: no standard output, no non-zero exit, no interpretation. Parsing is the adapter's job in Go, where it can be tested. This keeps evidence 6 from becoming a defect that changes the session it was meant to observe.
- The brief is written to `task.md` in the control workspace and the initial prompt names that path. The agent receives the brief the user confirmed, it stays readable for the whole session, and it is not in anyone's process list.
- `internal/agent` and `internal/agent/claude` are adapters under the rule ADR-029 established for Git: they receive final values and never read configuration or persistent state. `internal/control` is a protocol implementation under the same rule. Two `depguard` rules, `agent-stays-an-adapter` and `control-stays-a-protocol`, make that mechanical; ADR-025 requires an ADR for a boundary rule, and this is that record.
- Verification state is agent-reported in slice 7. The review request carries what the agent says it ran, and the dashboard labels it as the agent's claim. The provider-native completion gate that runs configured `checks` waits for the slice that has an environment to run them in, so `verifying` and `verification_failed` stay unreached and the interface names the slice that reaches them. **Corrected by ADR-033**: that slice is 11 rather than 8. Slice 8 supplies the environment, but its work list and acceptance criteria never contained the gate, and a promise no slice schedules is a promise nothing keeps. This narrows what ADR-031 promised for the dashboard's verification column, and narrows it in the direction ADR-028 established: a value that was never measured is never displayed as one.
- An agent process that exits takes the task to `failed` with its exit status, and nothing restarts or resumes it. Slice 12 owns recovery, for the reason recorded in its work list. The provider session identifier is captured from the session-start event before the process can fail, so the resume slice 12 offers can continue the recorded session rather than open an empty one.
- A session-start event whose source is a resume, a clear, or a compaction is an observation of the same session, not a new one. It updates activity and nothing else, so `/clear` cannot re-run a launch transition or reset what the task has already reported.

Consequence: slice 7 adds no endpoint and no command. The user-visible additions are `FEAT_HOST_AGENT`, the verification column filling with agent-reported checks, and `feat doctor` running two checks it used to skip. Package layout gains no new package: `internal/agent`, `internal/agent/claude`, and `internal/control` were reserved by slice 0.

The claim that a Stop event never means completion becomes mechanical rather than aspirational: the normalization table is pinned by a test in the shape ADR-026 used for the workflow transition table, so the defect has to be introduced by editing the table that documents it.

Flags and hook schemas were verified against the installed Claude Code 2.1.220 rather than assumed, as [06-technical-architecture.md](06-technical-architecture.md) requires. That version exposes more than the specification anticipated — `StopFailure`, `PermissionRequest`, and `PermissionDenied` alongside the documented events — and the adapter records the range it was verified against so that a later version's changes surface as a diagnostic rather than as a task that silently never reports.

Amended after running a real task end to end against Claude Code 2.1.220, with evidence the unit tests could not produce:

9. A launched session can be blocked before it emits anything at all. Claude asks for workspace trust on a directory it has not seen before, and every task worktree is such a directory, so the first thing a new task's agent does is wait for a person. No hook fires while it waits, because hooks belong to a session that has begun. Feat showed the task as a live process with no attention: a task that looked like it was getting on with its work while nothing was happening, which is the exact failure this slice exists to prevent.
10. The task brief lives in the control workspace, which is outside the agent's working directory, so the session's first act was to ask permission to read the document Feat had written for it. That prompt would arrive on every task launch.
11. Attention reached `needs_input` from a permission prompt and never left it. Nothing in the normalization table cleared it, so a task that had once asked a question would have reported needing the user for the rest of its life.
12. The flag that grants access to the control workspace takes a list of directories, and it was placed immediately before the prompt, so the prompt was read as a second directory. The session started, installed its hooks, reported that it had started, and then sat waiting for a task it had never been given. Every observable signal said the launch had worked.

Decisions:

- A launched agent that has not reported starting within a fixed grace period sets the task's attention to `possibly_waiting`, with an event saying that nothing has been heard and that attaching will show what the terminal is waiting for. It is `possibly_waiting` rather than `needs_input` because the provider reported nothing: Feat knows it has not heard from the agent, and does not know that the agent is blocked. The period is deliberately not configurable — it bounds how long Feat will show a task as launched while having heard nothing, and any value comfortably longer than an agent's start-up serves equally well.
- The launch grants the agent tool access to its own control workspace. It is one directory Feat generated for that task, holding the brief the agent is meant to read and the outbox it reports through, and it widens nothing else. A permission dialog nobody needed teaches a user to click through the ones that matter.
- The end of a turn clears attention. A turn cannot end while the agent is blocked on a dialog, so reaching the end of one is evidence that whatever it was waiting for has been answered; the idle grace that follows then sets the conservative `possibly_waiting`. This is the only attention effect an end-of-turn signal has, and it still touches neither the workflow nor the process state.

- A variadic flag is never the last one before the prompt, and the ordering is pinned by a test rather than left to a careful reader. Evidence 12 is the failure mode this slice is most exposed to and least able to see: everything Feat observes reports success, because everything Feat observes is about the session rather than about what the session was asked to do.

The workspace-trust prompt itself is left to the user. It is Claude's own boundary and answering it on somebody's behalf is not Feat's decision to make, so a task's first launch waits for a person and now says so.

Evidence 9 to 12 were all found by running one real task, and none of them could have been found by a test against Feat's own fixtures: three were provider behaviour Feat had not modelled, and the fourth was Feat passing a correct set of flags in an order the provider read differently. That is what the opt-in real-CLI suite is for, and why it asserts that a session produced the events it should rather than only that it started.

The Slice 3 target-machine acceptance check remains outstanding, as it did for slices 5 and 6. Slice 7 proceeds under the same explicit maintainer approval, recorded here rather than by marking slice 3 complete without its evidence.

### ADR-033 — Devcontainer execution, generated mounts, and what a container may not already hold

Status: accepted
Recorded: slice 8, before implementation

Evidence found while planning devcontainer execution. Items 2 to 5 and 7 were
measured against Docker Compose v5.1.4 and Docker Desktop on the target machine
rather than reasoned about, because every one of them decides what the generated
override may assume:

1. The reference devcontainer's own Compose file mounts the ordinary checkout of
   every working repository read-write, at the paths the agent works in. A
   generated override that mounted task worktrees at *different* container paths
   would leave those in place, and the agent would hold both its task worktree
   and the user's real checkout. That breaks the property slice 4's first
   acceptance criterion exists to protect, and it breaks it silently: everything
   Feat records about the task would be correct.
2. Compose merges a service's `volumes` by target path. A base entry
   `./a:/mnt/x` is *replaced* by an override entry `./b:/mnt/x:ro` — source
   swapped, read-only applied — while unrelated targets survive and new ones are
   added. So a generated override can take over a mount, and the container path
   is the key it takes it over by.
3. `container_name` is global to the Docker daemon and a published port is
   global to the host, so a base file carrying either cannot be brought up three
   times at once whatever project name it is given. `container_name: !reset null`
   and `ports: !reset []` both erase the base value. The `!reset` tag requires
   Compose 2.24 or later.
4. `--project-directory` pinned at the first base file keeps that file's
   relative sources and build contexts resolving while the generated override
   lives somewhere else entirely, which it must: the override belongs in the
   state directory, not in the user's repository.
5. A `0700` host directory owned by the host user is writable by a container
   process running as a different uid on Docker Desktop for macOS, and a file
   the container writes with mode `0600` reads back from the host. The
   file-sharing layer maps ownership both ways. On Linux it does not, and the
   failure mode there is that every hook writes nothing and the task reports
   nothing — the silence ADR-032 was written to prevent.
6. Validation has to run where the agent will run, and for a devcontainer that
   means the container must already be up. ADR-032 put validation before
   anything was created, which is no longer possible without also being useless.
7. `docker compose exec` registers `-T/--no-tty` with a default of `true` in its
   own help output. Read literally, an interactive Claude in a tmux pane would
   get no terminal.
8. `feat doctor` may not start a container (ADR-028), but a container Feat
   started carries Feat's own ownership labels, and labels are discoverable
   without reading any persistent state.
9. A project may already supply the agent's Claude configuration through its own
   Compose file, as the reference project does by mounting the user's `~/.claude`.
   A configuration volume Feat mounted unconditionally would fight it.

Decisions:

- `internal/execution` holds the interface and neutral types; `internal/execution/compose`
  holds every Docker-specific decision. Both receive final values — absolute
  Compose files, project name, service, user, mounts, labels — and read neither
  configuration nor persistent state, under the rule ADR-029 established for Git
  and ADR-032 for the agent. An `execution-stays-an-adapter` `depguard` rule
  makes it mechanical; ADR-025 requires an ADR for a boundary rule, and this is
  that record.
- The interface in [06-technical-architecture.md](06-technical-architecture.md)
  is amended in three places, and the amendments are the reason this bullet
  exists rather than a silent divergence. `Command` returns an argument vector
  rather than an `*exec.Cmd`, because the terminal backend constructs the process
  and an `*exec.Cmd` returned here would be a process nobody runs. `Run` is added
  for probes, because validation asks questions of an environment rather than
  attaching a terminal to it. `Shell` is folded into `Command`, because the
  daemon already builds the shell command and a second entry point would be a
  second place to decide what a task shell is. `Destroy` is not implemented:
  destruction policy — what is retained, what requires confirmation — is slice
  12's, and an untested destructive path with no caller is worse than none.
- The agent's Compose project is `feat-agent-{project_id}-{task_id}`, fixed
  rather than configurable. It is Feat's own resource, the prefix cannot collide
  with a project the user runs by hand from the same files, and a template would
  be a knob whose only use is to break the guarantee it exists to provide.
- The generated override is written to
  `<state>/execution/<project-id>/<task-id>/compose.override.yaml`, beside the
  control root rather than inside a task's snapshot directory. It is host-only
  and never mounted, so nothing the agent can read or write decides what its own
  container mounts; and the snapshot directory holds the documents the storage
  interface owns, which a file an execution adapter writes is not. `internal/paths`
  gains the root, and the daemon builds the per-task path below it after
  validating both identifiers, exactly as `internal/control` does.
- Every task worktree is mounted at the container path its repository
  configures, read-only when the task's access is read-only, and a
  `stable_read_only` repository is mounted read-only from its ordinary checkout.
  For evidence 2 this replaces whatever the base file mounted at that path, which
  is what makes a configured `container_path` that disagrees with the base file a
  configuration error rather than a preference.
- `container_name` and `ports` are reset unconditionally for the agent service,
  and the task detail says so in fixed words. Emitting them conditionally would
  mean rendering the user's resolved Compose configuration to find out whether
  they were there, and that document contains the values of their environment
  files.
- After the service is up, Feat inspects the running container's mounts and
  refuses the launch when one of them is a Docker socket or has a configured
  repository checkout as its source. The check reads the container rather than
  the resolved Compose configuration, so no environment-file value is ever
  rendered, and it is evidence about what exists rather than a claim about what
  was asked for. This is what makes "the agent has no Docker access" and "the
  agent cannot reach your ordinary checkout" statements about the running system.
- Launch order becomes start, validate, prepare, attach: the service is brought
  up, every probe runs inside it, the provider adapter then generates its files,
  and only then does the task get a terminal. This amends ADR-032's "validation
  creates nothing" for devcontainer execution alone, and the thing created before
  validation is the environment the validation is about.
- A failure after the service is up leaves it running and marks the task
  `failed`, naming the Compose project to inspect. Nothing is undone, for
  ADR-029's reason and one more: an entrypoint may already have had effects that
  stopping the container does not undo.
- Control-workspace and worktree write access are probed inside the container as
  the configured user before Claude is launched, and a failure is actionable
  rather than silent. Evidence 5 says this passes on the dogfood platform and
  will not on Linux, and a probe is the difference between a diagnosis and a task
  that runs all day reporting nothing.
- `agent.claude.config_volume` stays optional and gains `agent.claude.config_path`,
  default `/feat-claude`, validated for absoluteness and non-overlap as
  `control_path` is. When no volume is configured Feat mounts nothing and sets no
  `CLAUDE_CONFIG_DIR`, leaving the provider's configuration to the project, which
  is what evidence 9 requires and what the security model already permits as an
  explicit project choice.
- A Claude configuration volume in a `host` project is rejected rather than
  ignored, which it was until now. It is the same rule ADR-028 applied to the
  execution fields, and the belief it prevents is a specific one: a user who
  configured a dedicated volume and is in fact handing the agent their own
  `~/.claude`.
- `feat doctor` still starts nothing. It probes inside a container that Feat's
  ownership labels identify as a live task container of the project, and reports
  `skipped` with the reason otherwise. The reason names the condition rather than
  a slice, because from this slice on the check is deliverable and it is the
  machine's state that decides whether it can run.
- The minimum supported Docker Compose version is 2.24, for the `!reset` tag in
  evidence 3, and `feat doctor` checks it. A version below it fails at launch
  with a YAML error from Compose that says nothing about why Feat wrote that
  document.
- The form of `docker compose exec` that yields an interactive terminal is
  pinned by a test that runs it in a real pane. Evidence 7 is the ADR-032
  evidence-12 shape exactly: a flag whose documented default would break the
  session while every signal Feat observes reports success.
- ADR-032's statement that the provider-native completion gate "waits for slice
  8" is corrected rather than left standing. Slice 8's work list and acceptance
  criteria never contained it, and a promise the plan does not schedule is a
  promise nothing keeps. The gate moves to slice 11, which owns review state,
  with its own work item and acceptance criterion; the strings in `internal/ui`
  that name slice 8 move with it.

Consequence: `verifying` and `verification_failed` remain unreached, and
verification stays the agent's own claim, exactly as ADR-032 narrowed it. What
changes is which slice says so.

Amended after running the adapter against real Docker, with evidence the unit
tests could not produce:

10. Docker Compose reports "no such executable" on **standard output** rather
    than standard error. Reading only standard error is the obvious
    implementation, passes every test written against a fixture, and reports
    every absent tool as present. A container with no `mktemp` would therefore
    have launched an agent Feat could never hear from, and a `required` provider
    CLI that was not installed would have passed validation — the two failures
    the probes exist to prevent. Both streams are now read, and both directions
    are pinned by a test.
11. A tool that ran and could not open a file also says "no such file or
    directory". The first version of the same check read that as an absent
    executable, so `cat` failing on a missing file was reported as `cat` not
    being installed. The check now requires the container runtime's own quoted
    refusal to start the program, which is what distinguishes the two.

Evidence 10 and 11 are the same shape as ADR-032's evidence 12: Feat asking a
correct question and reading the answer wrongly, with every signal it observes
reporting success. Neither was reachable from a fixture, because both are
statements about what the real tool prints and where.

Amended again after launching a task in the reference project's own
devcontainer:

12. **A task worktree is not a repository inside the container.** Its `.git` is a
    file holding the absolute host path of the main checkout's
    `.git/worktrees/<name>`, and nothing mounted that directory. Every Git
    command in the container therefore failed with "not a git repository", while
    the container, its mounts, its user, and every state Feat recorded were
    exactly right. `agent.capabilities.git: full`, FR-GIT-006, and acceptance
    criterion 5 were all false at once, and nothing Feat observes would have said
    so.
13. A base file's mount that lives *inside* a repository mount is satisfied by
    the ordinary checkout and not by a task worktree. The reference devcontainer
    masks each repository's ignored `.env` by mounting `/dev/null` over it, and a
    worktree holds only what Git tracks, so the file is not there and the
    container runtime refuses to create it. The same shape breaks a named volume
    nested inside a repository the task selected read-only.
14. `docker compose up` narrates every build step and every resource it creates,
    so the first line of a failed `up` is "Image … Building" and the reason is the
    last line. Feat reported the first, which turned a precise mount error in the
    user's own Compose file into a progress message.

Decisions:

- Each task repository's Git directory is mounted at the same absolute path it
  has on the host, with the access its worktree has. That is what makes the link
  a worktree records resolve, whatever Git version wrote it and whether it wrote
  a relative path or an absolute one. What it exposes is repository metadata,
  which [05-security-model.md](05-security-model.md) already accepts by name and
  declines to call isolation; what it does not expose is the working copy, since
  the checkout's own directory then holds nothing else in the container.
- The rule that refuses a mount of an ordinary checkout is widened to any
  directory containing one or contained by one, with that Git directory as its
  single exception. Before this it caught a parent and missed a child, so
  mounting `<checkout>/src` would have exposed part of the working copy
  unnoticed.
- A failure that the container runtime reports against a path Feat mounted is
  explained in Feat's own terms — which repository, which access decision, and
  what to change — rather than left as an accurate sentence about a path. The
  two cases evidence 13 produces are both recognised.
- A failed `up` reports its last line rather than its first.

Evidence 12 is the most important thing this slice found, and it is the reason
the acceptance criteria are verified by running commands inside the container
rather than by reading what Feat generated. Everything Feat generated was
correct.

Acceptance criterion 5 asks that full Git and the required provider CLI work
inside the agent environment. The reference devcontainer has `gh` installed and
deliberately logged out, and the project configures `github_cli: optional`, so
the criterion is verified as full Git against a real task worktree plus the
refusal path — a project that requires a CLI its environment cannot authenticate
fails launch with an actionable message. The positive required-CLI path is not
verified, and is recorded here as not verified rather than reported as passing.

The Slice 3 target-machine acceptance check remains outstanding, as it did for
slices 5, 6, and 7. Slice 8 proceeds under the same explicit maintainer
approval.

### ADR-034 — Application runtime identity, generated mounts, and what a manual lifecycle owns

Status: accepted
Recorded: slice 9, before implementation

Evidence found while planning the manual application runtime:

1. [06-technical-architecture.md](06-technical-architecture.md) lists
   `runtime/start`, `runtime/stop`, and `runtime/logs-info`, while FR-RUN-005
   requires create, start, stop, status, logs, and destroy. Three of the six
   actions have no endpoint. `status` in particular cannot be a read of the
   stored snapshot: a runtime's state is an observation, and the snapshot holds
   the last one somebody took.
2. A repository's `container_path` is documented as the path *the agent's*
   Compose files already mount it at (ADR-033, [07-configuration-model.md](07-configuration-model.md)).
   The application's Compose files are a different set and may mount the same
   repository somewhere else. Compose merges a service's `volumes` by target, so
   a path that disagrees adds a second mount rather than replacing one, and the
   services run the user's ordinary checkout while every record Feat keeps about
   the task stays correct. It is ADR-033 evidence 1 in the zone where the
   security model does not forbid the mount, because the application runtime is
   inside the trusted host and the agent is not in it.
3. `container_name` and a published port are both global, which for the agent
   service made resetting both necessary (ADR-033 evidence 3). For the
   application runtime they are not the same question. A container name is
   Feat's own problem; a published port is how the user reaches the application
   they are testing, and v0.1 excludes port allocation
   ([08-v0-scope.md](08-v0-scope.md)).
4. Nothing observes a runtime unless something asks it to. The dashboard
   re-reads task state on every event and every two seconds, so observing inside
   a read would run one `docker compose ps` per task per refresh — and an
   observation is a write, so a write inside a read would publish an event,
   which would cause the next read. Slice 6 has already paid for that shape once.
5. `docker compose config` renders the resolved project including the values of
   the project's environment files, which Feat must never read (ADR-028). The
   ports, networks, and volumes a runtime owns have to come from somewhere else.
6. Slice 9's work list contains destroy, while slice 12 owns cleanup plans, plan
   tokens, the separation of destructive classes, and the confirmation rules for
   dirty or unmerged work.
7. `domain.RuntimeEnvironment` records the exact inputs a runtime was created
   from, and a project file can be edited between one action and the next.
8. `internal/execution/compose` already drives the Docker Compose CLI, and
   CLAUDE.md keeps the application runtime separate from agent execution even
   where both use Compose.

Decisions:

- `internal/runtime` holds the interface and neutral types;
  `internal/runtime/compose` holds every Docker decision. Both receive final
  values and read neither configuration nor persistent state, under the rule
  ADR-029 established for Git and ADR-033 for execution. A
  `runtime-stays-an-adapter` `depguard` rule denies it configuration, storage,
  the daemon, the transport, and the execution adapter; ADR-025 requires an ADR
  for a boundary rule, and this is that record.
- The Compose CLI plumbing — the runner, the `ps` decoding, the version check —
  is duplicated rather than shared with `internal/execution/compose`. A shared
  package was considered and rejected: it is not in the documented package
  layout, and it would put the agent's environment and the application runtime
  behind one type, which is the distinction the domain model, the security
  model, and CLAUDE.md all keep. Roughly a hundred and fifty lines is the price
  of a boundary that three documents state.
- All six actions get an endpoint, and the endpoint list in
  [06-technical-architecture.md](06-technical-architecture.md) gains `create`,
  `status`, and `destroy`. `status` is a POST because it observes and records
  what it observed. `destroy` carries `{"confirm": true}` and is refused without
  it, so a stray request cannot remove anything: it is the shape ADR-031 used
  for the launch fingerprint, where the request carries what the user agreed to.
- The generated override is written to
  `<state>/runtime/<project-id>/<task-id>/compose.override.yaml`, beside the
  execution root that ADR-033 placed under the same rule. It is host-only and is
  never mounted anywhere.
- The runtime's identity is `runtime.project_name_template` expanded for the
  task, which validation already requires to carry `{task_id}` or `{task_key}`.
  Unlike the agent's Compose project it is configured rather than generated,
  because the user brings these services up by hand today and the name is theirs.
- Task worktrees are mounted at each repository's configured `container_path`,
  into every service the project lists under `runtime.services`. After a start,
  Feat inspects the started containers and records a note when one of them
  turned out to mount an ordinary checkout. It reports rather than refuses:
  evidence 2 is a correctness problem here and not a boundary breach, and
  refusing would stop a project whose own Compose file mounts a checkout Feat
  has no task worktree for. The note names the service and the repository, so
  the silent version of the failure does not exist.
- `container_name` is reset for every service in the task's Compose project — the
  managed ones and, since evidence 12, everything Compose starts alongside them —
  and published `ports` are left exactly as configured. A port two tasks both want is explained in Feat's
  terms — that this is the other task's runtime and that v0 allocates no ports —
  rather than passed through as a bind error. [07-configuration-model.md](07-configuration-model.md)
  gains the runtime half of the rule it currently states for the agent alone.
- Runtime state is observed by a slow poll over the tasks that hold a runtime
  record, and a poll writes and publishes only when the observed state or health
  changed. Ports come from `ps`; networks and volumes come from
  `docker network ls` and `docker volume ls` filtered on Compose's own project
  label, so evidence 5 never has to be worked around.
- Destroy is `docker compose down` without `--volumes` and without
  `--remove-orphans`. It removes the containers and networks of the task's own
  Compose project, retains every volume and says which ones it retained, never
  names an external resource, and never looks at a worktree or a branch. The
  wider question — which classes a user may choose, and what a dirty worktree
  requires — stays slice 12's, and this is deliberately the narrow half.
- The recorded inputs win while a runtime exists, as they do for the agent's
  environment. A runtime whose state is `absent` — never created, or destroyed
  since — is re-resolved from current configuration through a domain method that
  refuses in any other state. So a user who fixes their Compose file after
  destroying a runtime gets the fixed one, and a user who edits it while services
  are running does not silently point an action at a different project.
- The selector value Feat generates for an external resource is the task key: a
  short, unique, non-secret identifier the application can use to pick its share
  of a shared development database. Feat names it and never creates, migrates, or
  drops anything behind it. OQ-011 stays open.
- No workflow transition starts, stops, or destroys a runtime. Approval offers
  to stop and does not act, which is the one acceptance criterion of this slice
  that reaches into a slice that has not happened yet: slice 11 delivers the
  approval action, and the offer is rendered for a task that has reached
  `approved` however it got there.

Consequence: the user-visible additions are four `feat runtime` subcommands and
three endpoints. No stored format changes — `domain.RuntimeEnvironment` and its
document have carried every field this slice fills since slice 1 — so no
migration is needed, and `verifying` and `ready_for_review` stay exactly as
narrow as ADR-033 left them.

Amended after running the adapter against real Docker, with evidence the unit
tests could not produce:

9. **Stopping a service makes it exit 137.** `docker compose stop` sends
   SIGTERM and kills the container when it does not exit, and a process running
   as PID 1 has no default signal handlers — so the ordinary
   `command: sleep infinity` service that every devcontainer and most
   application images use exits by signal. The obvious rule, that a non-zero
   exit is a failure, therefore reported every stop the user had just asked for
   as `failed`. It is the same shape as ADR-033 evidence 10 and 11: Feat asking
   a correct question and reading the answer wrongly, with every fixture-based
   test passing.

Decision: an exit produced by SIGINT, SIGKILL, or SIGTERM — 130, 137, and 143 by
the shell's convention — is `stopped`, and any other non-zero exit is `failed`.
The distinction is the difference between a state that means something and one
that cries wolf on the ordinary path, and a state people learn to ignore is
worse than no state at all. Both readings are rows in the pinned aggregation
table, so the defect has to be introduced by editing the table that documents it.

Amended again after driving a real daemon, a real client, and real Docker
through one task's whole lifecycle. Two more defects, and neither was reachable
from the adapter's own tests:

10. **A host-execution project mounted nothing.** A task's recorded
    `container_path` is filled only for devcontainer execution, because that
    field says where the *agent's* container mounts the worktree and a
    host-native agent has no container (slice 8). An application runtime has
    containers whatever the agent does, so reading the mount from the binding
    produced a generated override with no `volumes:` at all: the services ran
    the user's ordinary checkout, every record Feat kept was correct, and the
    only thing that said so was the note added for the other half of this
    problem. The mount target now comes from the project's configuration, which
    is where a project declares where its containers hold a repository; the
    binding stays as honest as slice 8 made it.
11. **Asking what is running failed before anything had been created.** Every
    Compose command carries the generated override, and that document does not
    exist until a create or a start writes it — so the first thing a user does,
    `feat runtime status`, answered with a Compose error about a file Feat
    generates. The file is now passed only when it is there. Every path that
    creates something writes it first, so nothing else changes.

Both are the ADR-033 evidence-1 shape rather than the evidence-10 one: not a
wrong answer read wrongly, but a correct implementation of something that had
quietly stopped being the question. Each is now a test that fails against the
behaviour it replaced.

Amended a third time, after the slice was used on a real application:

12. **A stop left a task's database running.** The user's project manages `api`
    and `nginx`; `api` depends on a migration that depends on PostgreSQL, so
    `docker compose up api nginx` started four containers. Every action then
    named the managed services, so the stop stopped two of them. The database
    stayed up, kept its published port — which is global to the host, so no
    second task could ever have bound it — and appeared in no status Feat
    printed, because `ps` named the same two services. Nothing short of a
    destroy would have stopped it. The two containers Compose had started also
    kept the fixed `container_name` their base file gives them, because the
    generated override named only the managed services: the one thing a per-task
    Compose project exists to prevent, reintroduced by the services nobody had
    listed.

Decision: `runtime.services` is what a create and a start *target*, and it is not
what exists. Everything Compose starts to satisfy those services lands in the
task's own Compose project, and everything in that project is there because Feat
acted — so stop, status, logs, and destroy address the project and name no
service, and the generated override reaches every service the project defines.
A service the project did not name gets exactly two things: its `container_name`
reset, and Feat's ownership labels. It is not given the task's worktrees or the
generated variables, because the project did not ask Feat to manage it.

The aggregation table gains one row with it: a service Compose started alongside
a managed one counts towards the runtime's state unless it exited cleanly. A
one-shot migration that has done its job is the ordinary path of every project
that uses `service_completed_successfully`, and a runtime that reported `degraded`
every time one succeeded would be the state-that-cries-wolf of evidence 9 again.
One that is up, restarting, or failed does count, because then the application
really is partly there or broken.

Which services Compose will start is read with `docker compose config --services`
against the project's own files, without the generated override so that a stale
one cannot reintroduce a service the project has since removed. It prints service
names and nothing else, so evidence 5 still holds: no value from an environment
file is read.

Two smaller things were fixed with it: a published port was listed once per
protocol family, so the same port appeared twice on a screen whose purpose is
telling a user where to reach their application; and a status showing a
container the project never named now says where it came from, because a service
appearing without explanation is a service a user has to go and investigate.

The Slice 3 target-machine acceptance check was settled during slice 8, so slice
9 is the first slice since slice 4 that starts with none outstanding.

### ADR-035 — Resource observation, notification policy, and what a machine can honestly report

Status: accepted
Recorded: slice 10, before implementation

Evidence found while planning notifications and resources. Items 1 to 6 were
measured on the target machine — Docker 29.5.2, Compose 5.1.4, tmux 3.7b, macOS
26.5 — rather than reasoned about, because each of them decides something the
design cannot take back later:

1. `docker stats --no-stream` takes **1.1 to 2.0 seconds**, whether it is asked
   about one container or all of them, while `resources.sample_interval`
   defaults to two seconds. A sample therefore cannot be taken inside a request,
   and the configured interval is a floor rather than a promise.
2. `docker ps --filter label=… --format '{{.ID}}\t{{.Label "dev.feat.task"}}…'`
   answers in **25 ms** and extracts one label directly. Docker's own `Labels`
   field is a comma-joined string, so a label a user's Compose file sets with a
   comma in it would otherwise split into values that were never there.
3. `docker stats` reported a container's memory against **7.653 GiB** on a
   machine with **16 GiB**: on macOS that limit is the container runtime's own
   virtual machine. A per-task container total is therefore not a share of host
   memory, and presenting it as one would be a claim nothing measured.
4. tmux exposes `#{window_active_clients}`: 1 for the window an attached client
   is viewing, 0 for every other, following a window switch immediately. It is
   the per-task answer to "is the user watching this", which `#{session_attached}`
   is not — a user attached to a project's session is looking at one of its task
   windows and not at the others.
5. `osascript` with an `on run argv` handler delivers a notification in 141 ms,
   exits 0, and reads its text out of `argv` rather than out of the script. It
   also exits 0 when macOS drops the notification for want of permission, which
   it does silently.
6. `ps -A -o pid=,ppid=,rss=,time=` returns five hundred processes in 18 ms. Its
   `%cpu` column means different things on the two supported platforms — a
   decaying recent average on macOS, a lifetime average on Linux — while the
   cumulative `time` column means the same thing on both.
7. A per-core processor utilisation figure is not obtainable on macOS from Go
   without cgo. The Mach call that carries it, `host_processor_info`, is not
   reachable from pure Go, and the usual third-party library returns "not
   implemented" in its no-cgo build.
8. `agent.claude.idle_grace_period` and `notifications.idle_grace_period` both
   exist in [07-configuration-model.md](07-configuration-model.md), both default
   to five seconds, and only the first has a reader. The document says what
   neither of them means.
9. `resources.sample_interval` is per-project configuration for a measurement
   that is machine-wide. Three registered projects can ask for three intervals
   for one machine.
10. The daemon applies the control messages that arrived while it was stopped
    before it serves anything. Every state change that catch-up produces looks,
    to anything downstream, exactly like one that happened just now.

Decisions:

- `internal/resources` is an adapter under the rule ADR-029 established for Git:
  it receives final values — process identifiers, a label selector, a filesystem
  path — and reads neither configuration nor persistent state. A
  `resources-stays-an-adapter` `depguard` rule denies it both Compose adapters,
  and that denial is the point of it: an agent's container and an application's
  are the same thing to a resource observer, and a package that could tell them
  apart would eventually be asked to treat them differently. ADR-025 requires an
  ADR for a boundary rule, and this is that record.
- `internal/notify` holds the policy and its platform adapters under the same
  rule, with a `notify-stays-a-policy` rule that denies it configuration, the
  control protocol, and storage. The daemon resolves a project's settings into a
  `Policy` and hands it a `Subject` carrying a task's key, title, and project.
- Sampling is a background loop with a cache, and the local API serves the cache.
  Evidence 1 makes this the difference between a working dashboard and one that
  blocks for two seconds per refresh, and it gives the first two acceptance
  criteria their shape: no request path can be slowed or failed by a metric, and
  a sample that failed leaves the previous one with its own time and its notes.
- The interval is the shortest any registered project asks for, floored at one
  second and at however long the last sample took. Evidence 9 is an oddity of the
  configuration model rather than a design, and the most eager project winning is
  the reading under which no project's setting weakens another's.
- The machine sample is **load average with the core count**, available memory,
  and available disk on the filesystem holding the state directory. Evidence 7
  means a utilisation percentage would exist on one supported platform and not
  the other, and one measure on both is worth more than two that look alike and
  are not. This narrows the words "whole-machine CPU" in
  [06-technical-architecture.md](06-technical-architecture.md) and FR-UI-005,
  which is updated in the same change. Load is reported beside the core count
  because the number means nothing without it.
- Per-task usage is containers and processes, summed and also reported apart.
  Containers are found by Feat's own ownership labels in one `docker ps` and
  measured in one `docker stats`, so the cost is two Docker calls per sample
  whatever the number of tasks — which is the number the dashboard exists to make
  large. Processes come from one `ps`, summed over the subtree of each task's
  tmux panes, with processor use differenced between samples for evidence 6.
  Evidence 3 is stated in the interface and on the screen rather than smoothed
  over.
- A Feat-owned container with no `dev.feat.kind` label is the agent's. The
  runtime adapter labels its own `kind: runtime` and the execution adapter labels
  nothing, and the asymmetry is left alone rather than corrected: a container an
  older build created would carry no new label either, so the inference has to
  exist regardless, and adding one would change a document slice 8 pinned for no
  gain.
- `GET /v1/resources` is added to the endpoint list, as slice 9 added three. A
  sample is not persisted (`docs/06-technical-architecture.md`, storage rules),
  so it is not part of what a task record says about itself; it has its own time
  and its own failure mode, which a task carrying the figures would have to
  borrow. Every figure nothing measured is published as null rather than zero.
- `notifications.idle_grace_period` is measured **from the idle transition**: how
  long a task must have *been* idle before Feat interrupts somebody about it. The
  other reading — both graces measured from the end of the turn — was considered
  and rejected, because a notification grace shorter than the provider's would
  then expire before the task was idle and no notification would ever be
  delivered. A configuration that silently turns off the thing it configures is
  worse than one that needs explaining. Evidence 8 is resolved in
  [07-configuration-model.md](07-configuration-model.md) in the same change.
- Notification suppression asks tmux, per window, using evidence 4. It is an
  observation rather than a memory of somebody having run `feat attach`: a user
  who detached, or who switched to another task's window, stops watching without
  telling Feat anything. A tmux that cannot answer counts as nobody watching, so
  the notification is delivered — of the two mistakes, an unnecessary
  notification is noise and a missing one is the failure the slice exists to
  prevent.
- What is worth interrupting somebody for is two pinned tables, in the shape
  ADR-026 used for the workflow transitions and ADR-032 for agent events. Their
  most important property is again an absence: nothing maps an end of turn or an
  idle process to a notification, because idle is not a state a task arrives in
  but one it stays in, and how long it has stayed is what decides. That is armed
  by a timer instead, which makes "idle notifications do not fire immediately" a
  property of the mechanism rather than of a value somebody chose.
- One change produces one notification. A session that dies moves both the
  process and the workflow, and the workflow wins; a process failure that left
  the workflow where it was is reported, because nothing else reports it.
- Startup catch-up records without notifying (evidence 10). Restarting Feat in
  the morning must not announce every turn that ended overnight.
- Notification text is composed from a task's key, title, and project plus a
  fixed phrase per condition. There is deliberately no way to reach a brief, an
  agent's summary, a path, a command, or a configured value, so the fourth
  acceptance criterion is a property of what the code can see rather than a
  filter over what it writes — the shape slice 3 used for secrets in diagnostics.
  A test pins the fields a `Subject` may hold.
- Delivery is `osascript` alone on macOS, with the text passed as arguments and
  read out of `argv` (evidence 5). Feat reports that it **delivered** a
  notification and never that one was seen: macOS decides per application whether
  to show one and drops an unauthorised one without saying so, and evidence 12
  shows the application it decides about may not be one the user can find. Linux
  support stays slice 14's, and this build says so rather than failing silently
  there.
- A delivered notification is recorded as a task event, `notification_sent`. A
  desktop notification is gone the moment it is dismissed, so without this there
  would be no record that Feat asked for somebody's attention — and slice 13 has
  to measure how many idle notifications turned out to be false. The event type
  is deliberately not itself notifiable, which a test pins, because recording an
  event publishes it.

Consequence: the user-visible additions are one endpoint, a machine resource card
and a filled resource column on the dashboard, attention badges, and macOS
desktop notifications. The command surface does not change, so its golden file
and the README's command list are untouched. No stored format changes — samples
are not persisted and the event vocabulary gains one additive type — so no
migration is needed.

`notifications` and `resources` have been parsed, resolved, and defaulted since
slice 3 without a reader. This slice is their first, which is why the semantics
of two of their four fields had to be settled here rather than found in the
configuration model.

Amended after running the slice against a real daemon, a real tmux client, and
real Docker rather than against its own fakes:

11. A control-mode tmux client attached without a held-open standard input is
    accepted by tmux and then leaves at once, so `window_active_clients` reports
    zero and the notification is delivered. That is correct behaviour and it is
    worth recording, because the first attempt to verify suppression by hand
    proved nothing: the test looked as though it had shown suppression when it
    had shown an unattached window. Verification held the client's input open and
    checked `list-clients` before drawing any conclusion.
12. A notification `osascript` posts is attributed to `com.apple.ScriptEditor2`
    and travels the legacy notification path, which does not require the
    per-application registration the modern one does. On macOS 26 that bundle has
    **no entry** in Notification Center's preferences at all, so the advice to
    allow notifications for Script Editor named a switch that was not there,
    while delivery worked the whole time. `log show --predicate 'process ==
    "usernoted"'` reports both the delivery and the presentation decision, and is
    the only diagnostic that distinguishes "macOS dropped it" from "it was shown
    and missed". The README and
    [06-technical-architecture.md](06-technical-architecture.md) are corrected in
    the same change.
13. `go test` caches a passing result and replays its output verbatim, including
    `--- PASS` under `-v`. An opt-in test whose entire purpose is a side effect
    outside the process therefore reports success while posting nothing, which is
    evidence 11's failure mode in a second form: a check that appears to have run.
    `-count=1` is part of the instruction for running these, not an optimisation.
14. What a broken observation command can break is platform-shaped. macOS reads
    load and memory through `sysctl` and `vm_stat`, which are processes, while
    Linux reads both out of `/proc` and the disk through `statfs`, which are not.
    A daemon test that failed every command therefore lost two machine figures on
    one platform and none on the other, and its assertion that they were absent
    passed on macOS for a reason that was never the acceptance criterion. It is
    the tasks, whose sources run commands on both platforms, that carry the
    property at the daemon; the machine's half belongs to `internal/resources`,
    where an injected machine reader makes it platform-neutral. This is the third
    form of the same failure — a check that looked like proof — and the first one
    a machine caught rather than a person.
15. `syscall.Statfs_t.Bsize` is signed on Linux and unsigned on macOS, so the
    conversion `gosec` refuses under G115 exists on one platform only. A negative
    block size cannot come from a working kernel, and it is reported as an
    unmeasured filesystem rather than converted, which is what every other
    figure this build cannot trust does.
16. `ps -o time=` reports cumulative processor time in **whole seconds on Linux**
    and in centiseconds on macOS — `00:00:00` against `149:45.95`. Evidence 6
    established that the column means the same thing on both platforms, which is
    true, and missed that it does not carry the same resolution. The integration
    test spun for 200ms and asserted a positive figure: a tenth of what Linux can
    represent, so Linux answered zero and was right to.

    The consequence outlives the test. Per-process use is a difference of that
    counter over the sampling interval, so on Linux the difference is a whole
    number of seconds and the reported percentage is quantised to `1s/interval`
    — steps of 50 points at the default two-second interval, where macOS resolves
    to about one. A task steadily using a quarter of a core reports 0% and 50% by
    turns rather than 25%.

    This is the shape ADR-035 rejected once already, when it chose load average
    over a processor percentage because two figures that look alike and are not
    are worse than one figure on both platforms. It is recorded rather than fixed
    here: the honest fix is `/proc/<pid>/stat`, whose `utime` and `stime` are
    clock ticks at the same 10ms USER_HZ macOS reports, and adding a
    platform-specific process reader is more than a red build justifies. Until
    then a Linux per-task processor figure is coarse rather than wrong, and OQ-012
    carries it.

The end-to-end run is what settled the timing. A turn ended at 10:35:51, the task
became idle at 10:35:56 after the provider's five-second grace, and the
notification was delivered at 10:35:59 after the project's three-second
notification grace — the two periods measured from the two moments this ADR says
they are. A third turn ended with a real client watching that task's window
produced the idle transition and no notification, while the two before it
produced both.

### ADR-036 — Review comparisons, external commands, and where a completion gate can honestly interrupt an agent

Status: accepted
Recorded: slice 11, before implementation

Evidence found while planning review and the completion gate. Items 1 to 3 are
properties of code this repository already has, and each of them decides
something this slice cannot take back later:

1. `api.NewVerification` labels every verification `agent`. Its loop reads
   `check.Reporter == provider && verification.Source != agent`, and `Source` is
   initialised to `agent` and written nowhere else, so the condition is
   unreachable and the comment above it describes a rule the code inverts. It
   produces the right answer today because every check is an agent's claim.
   Slice 11 is the first slice that produces a provider-gated one, which is what
   turns dead code into acceptance criterion 5.
2. The workflow table has no way out of a failed or an interrupted gate.
   `verifying` reaches only `ready_for_review`, `verification_failed`, and
   `failed`; `verification_failed` reaches only `working`, `changes_requested`,
   and `failed`. So an agent that is handed a failing check, fixes it, and asks
   for review again produces no transition at all, and a daemon that restarts
   while checks are running leaves a task in `verifying` for ever.
3. `agent.HostRunner` bounds every command at twenty seconds. That is right for
   a probe asking whether `glab` is authenticated and wrong for a test suite, so
   a configured `execution: host` check cannot use it.
4. [06-technical-architecture.md](06-technical-architecture.md) describes the
   gate as "provider-native completion hooks". Claude's `Stop` hook is the hook
   that can block, and it fires at the end of **every** turn — so a gate built
   on it either runs the project's suite whenever the agent stops speaking, or
   needs shell logic to work out whether a review request is outstanding.
   ADR-032 made every generated hook inert for a related reason: a hook that
   prints changes the model's context and a hook that exits 2 stops the session
   it exists to observe.
5. The agent's outbox is agent-writable by design, so any result delivered
   through it is a result the agent could have authored. A "gated" label on such
   a result would be Feat claiming enforcement it did not perform, which is the
   distinction `CheckReporter` exists to keep.
6. `domain.Check.Detail` is documented as never carrying a secret, "because
   review state reaches the dashboard and the event stream". A failing test
   prints whatever the project's own program prints, and that is the one thing a
   person reviewing a failed check needs to see.
7. `git.ChangedFiles` counts untracked files as well as tracked ones, and
   `git diff --numstat` cannot report line counts for a file Git has never been
   told about without writing to the index — which every observation in
   `internal/git` deliberately avoids through `--no-optional-locks`.

Decisions:

- The gate is triggered by the explicit review request and never by an end of
  turn. Evidence 4 rules out the `Stop` hook, and the trigger this leaves is the
  one signal that already means what the gate is about: an agent that says its
  work is ready.
- The **daemon** runs the checks, for evidence 5. A result recorded as
  `provider` is therefore evidence Feat collected by running a configured
  command itself, and the agent cannot author one. This is what makes the second
  half of acceptance criterion 5 a property of where the code runs rather than a
  label.
- The failure returns to the native agent loop through the **exit status of the
  helper the agent invoked**. The agent asks for review by running the generated
  `feat-report review_requested`; that helper now waits for the daemon's verdict
  and, when a check failed, prints the failing checks and a bounded excerpt of
  their output to standard error and exits non-zero. The model reads a failed
  tool call and carries on in the same turn, which is as native as the loop
  gets, and it needs no provider-specific blocking semantics — so the mechanism
  survives a second provider adapter. [06-technical-architecture.md](06-technical-architecture.md)
  and [02-user-workflows.md](02-user-workflows.md) are corrected in the same
  change rather than left describing a hook.
- The helper is the only generated script that waits, and it is not a hook.
  ADR-032's rule stands untouched: the six hook scripts still write a file and
  exit 0, and nothing that observes a session can block one.
- The verdict is written to the task's **inbox**, which gains its first writer.
  It is a versioned line-oriented document — `feat-verification 1 <status>`,
  then the report — rather than JSON, because the only thing that reads it is a
  shell script, and ADR-032's reason for keeping parsing out of generated
  scripts applies to reading exactly as it does to writing.
- Two workflow edges are added for evidence 2, and each carries its reason in
  the table: `verification_failed → review_requested`, so an agent that fixed
  what the gate caught can ask again; and `verifying → review_requested`, so a
  gate interrupted by a daemon restart returns the task to where it was rather
  than stranding it. The pinned transition test moves with them, and neither
  edge reaches a review state without an agent having asked: `TestIdleIsNotCompletion`
  still holds.
- Each check runs where its `execution` field says: `agent` inside the task's
  execution environment, `host` on the trusted host in the task worktree. The
  field has had no reader outside `feat doctor` since slice 3, and a
  configuration value nothing acts on is a promise the binary does not keep.
  `internal/review` owns the host runner, with the gate's bound rather than the
  probe's (evidence 3).
- Only repositories a task bound **read-write** are checked. A read-only binding
  holds code the task cannot have changed, and running its suite would spend
  minutes to learn nothing; the checks are recorded as `skipped` naming that
  reason, because a check that did not run is never absent.
- The bounds are fixed constants, not configuration: thirty minutes for one
  check, sixty for a whole gate run, sixty seconds for the helper to be
  acknowledged and sixty-one minutes for it to be answered. A check that exceeds
  one is recorded as timed out with the bound named, and never as a failure it
  did not have. This is the argument ADR-032 made for the startup grace: the
  value bounds how long Feat will wait while knowing nothing, and any value
  comfortably beyond a working case serves equally well.
- A check's output is stored as a bounded excerpt in `Check.Detail`, shown on
  the review screen, and never put into an event payload. Evidence 6 is resolved
  by narrowing where the field may travel rather than by adding a field, which
  would change a stored format that has carried every field this slice needs
  since slice 1. The excerpt is the output of the user's own program shown to
  its owner, which is the same class of thing as the Compose logs Feat opens
  wholesale; what the security model forbids is copying secrets into generated
  documents and event payloads, and neither happens here.
- `internal/review` receives final values and expands nothing, under the rule
  ADR-029 established for Git: the daemon expands review and check templates
  with `config.Expand`, because the placeholder vocabulary belongs to
  `internal/config`. A `review-stays-a-policy` `depguard` rule makes it
  mechanical; ADR-025 requires an ADR for a boundary rule, and this is that
  record.
- An expanded command is refused unless its working directory is one of *this*
  task's recorded worktree paths, and that path passes the same safety check
  `internal/paths` applies to a directory Feat would later remove. Acceptance
  criterion 3 is therefore a property checked in one package against one list,
  rather than a rule spread over the three commands that happen to exist today.
- The external commands are run by the client, as `feat attach` runs tmux and
  `feat runtime logs` runs Compose: they take over the caller's terminal, and
  the daemon has neither the terminal nor the user's `$EDITOR`. The client
  checks what it was handed before running it — the same user, but a client that
  ran whatever it received would be one nobody could reason about.
- `POST /v1/tasks/{task_id}/review/{action}` carries `observe`, `approve`,
  `changes`, `pending`, and `verify`, and the endpoint list in
  [06-technical-architecture.md](06-technical-architecture.md) gains the four it
  is missing. It is the shape slice 9 used for the runtime: one action per thing
  a user asks for, and `observe` is a POST because it observes and records what
  it observed.
- `verify` exists as a user action because it is what makes an interrupted gate
  recoverable by hand, which is this product's idiom everywhere else: recovery
  is offered and never automatic. Slice 12 still owns the reconciliation pass
  that finds such a task at startup.
- Approval decides the work and touches nothing else. No review action starts,
  stops, or destroys a runtime, which is checked by counting the commands an
  approval produces rather than by asserting that a recorded state did not
  change — and it is what finally exercises the offer slice 9 rendered for an
  approval that had not been implemented yet.
- Insertions and deletions cover tracked changes only, for evidence 7, and the
  screen says so beside the file count rather than presenting one number derived
  from two definitions. It is the same choice ADR-035 made about load average:
  one figure that means what it says beats two that look alike.

Consequence: the user-visible additions are the review screen, `feat review`,
four endpoints, and a gate that can move a task through `verifying` to
`ready_for_review` or `verification_failed` — the two states no build has reached
until now. The command surface does not change, so its golden file and the
README's command list are untouched, and no configuration field is added. No
stored format changes and the event vocabulary gains nothing, so no migration is
needed.

`verifying` and `verification_failed` stop being unreachable, which also gives
the two notification conditions slice 10 wrote for them their first delivery.

Amended after running the slice end to end against a real daemon, real tmux, and
the generated helper under a real shell. Two defects, and neither was reachable
from this repository's own fakes:

8. **tmux mangles its own output when the client has no locale.** A tmux client
   whose locale is not UTF-8 replaces every non-printable character in the
   output of `-F` with an underscore — a tab, a newline, and a unit separator
   alike — and every format `internal/tmux` uses is tab-separated. So a daemon
   started without `LANG` or `LC_ALL` cannot parse the identifiers of the
   terminal it has just created: every task launch fails with `tmux returned
   "$0_@0_%0", want stable session, window, and pane ids`, and discovery finds
   nothing at all, which would make every task look like a task whose terminal
   had gone. Measured against tmux 3.7b, where the substitution follows the
   *client's* locale and not the server's.

   An environment with no locale is not an exotic case: it is what a process
   started by launchd or systemd gets, which is how slice 14 intends a daemon to
   run, and it is what any sanitised environment looks like. The suite never saw
   it because `go test` inherits the developer's own environment.

   Every control invocation now passes `tmux -u`, which is the documented flag
   for exactly this and changes nothing about the environment a pane inherits.
   Interactive attachment deliberately does not pass it: there the client is the
   user's own terminal, and what it can render is theirs to declare. It is
   slice 5's defect, found by running slice 11 — the shape slice 7 recorded when
   a dashboard's stream loop surfaced as a Git error.

9. **A finishing gate lost its results to the request that started it.** The
   daemon is the only process that writes state (ADR-008) and every write is
   atomic (FR-STATE-002), and neither of those makes a load-change-save cycle
   safe against another one. A gate finishing while the review request that
   started it was still comparing repositories left a task recorded as
   `ready_for_review` whose review held no checks at all: the workflow said the
   checks had passed and the record of what passed had been overwritten by a
   copy loaded a moment earlier. Every fixture-based test passed, because the
   background gate is the first thing in Feat that writes one task's records
   from two goroutines.

   Decision: a per-task lock, held across a cycle rather than across an
   operation — the gate takes it to record what it found and releases it while
   the checks run, which take minutes. Every path that loads, changes, and saves
   one task takes it: the review actions, the gate's two halves, control
   delivery, the idle and startup timers, and the runtime actions. The gate's
   second half re-reads everything after taking it, because a task can move
   while its checks run: a user who approved in the meantime has decided, and a
   gate must not undo that.

   Slice 12 owns reconciliation and will meet the same question for the paths
   that run before a task can have a gate. This is the narrow half.

10. **The notification this decision suppresses one for did not arrive.** A task
    that passes its gate reaches `ready_for_review` and the user is not told,
    although the argument above for dropping the `review_requested` notification
    is that the later one is the one that means something. So a gated task
    currently arrives more quietly than an ungated one, which inverts what the
    suppression was for. The transition, the event, and the review record are all
    correct, and `notify.notifiableWorkflow` maps the state, so nothing here is a
    rule that was decided wrongly: what is unproven is the flow between a
    transition and a desktop, and this ADR's closing sentence about giving slice
    10's two conditions "their first delivery" should be read as untested rather
    than as observed.

    Not diagnosed and deliberately not fixed inside slice 11, because the same
    walk is owed to every condition: slices 10 and 11 both tested notifications
    over a fake notifier, which proves the daemon asked and not that anybody was
    told. Slice 13 owns it, against a real desktop, along with the layers that
    can legitimately drop one — the suppress-while-attached policy in
    particular, since a user who has just told an agent to request review is by
    definition attached to it.

11. **A gate outlived the daemon that started it.** `startGate` detached its run
    from the request context, which is right — a test suite outlives the message
    that asked for it — and detached it from the daemon's lifetime too, which is
    not. Nothing cancelled a running gate at shutdown and nothing waited for one,
    so `Serve` could return while a goroutine was still writing a task's records,
    and a check that had not finished was left running with no daemon to report
    to. Found by CI on Linux, where three tests that emit a review request failed
    in cleanup with `TempDir RemoveAll: directory not empty`: the gate was
    writing the control workspace the testing package was removing. macOS lost
    the same race quietly.

    Decision: gates are owned like the pending transitions beside them in
    `Serve`'s shutdown, which already carry the rule — nothing may fire into a
    daemon that can no longer write what it decided. Stopping cancels each run
    and then waits for it; cancelling is what ends the check itself, and the wait
    is bookkeeping rather than the suite, so it costs milliseconds.

    What a stopped gate must not do is record. A cancelled run produces
    inconclusive results, `Decide` reads inconclusive as not passing, and
    recording that would fail a task because Feat was restarted and answer the
    waiting agent with a verdict its checks never produced. So a cancelled run
    leaves the task in `verifying` and writes nothing, which is exactly the state
    evidence 2's recovery was built for.

### ADR-037 — Quarantine, recovery, and what cleanup is allowed to resolve

Status: accepted
Recorded: slice 12, before implementation

Evidence found while planning reconciliation and cleanup. Items 1 to 5 are
properties of code this repository already has:

1. `tmux.Discover` fails for the whole server when any tagged object is
   inconsistent. `parseSessions`, `parseWindows`, `parsePanes`, and `assemble`
   each return `nil, err`, so one task window whose agent pane was killed while
   its shell pane survived makes `EnsureTask` fail for every unrelated task and
   stops startup reconciliation before it reaches any task at all. ADR-030
   recorded this and deferred the answer here, because worktrees, Compose
   projects, and control messages raise the same question.
2. `CommandSpec.Validate` checks its program and arguments against NUL and
   newline, and checks only that the working directory is absolute. Discovery
   reads tab-separated formats and the directory is the one caller-supplied
   value tmux reports back, as `#{pane_current_path}`. A tab in a path therefore
   misaligns every pane field and breaks discovery for every terminal — the
   blast radius of evidence 1, reached through a different door. `safeArgument`
   does not reject a tab either.
3. `control.Workspace.Pending` already returns what it could read and what it
   could not, as `([]Message, []error, error)`, and reports a whole-directory
   failure separately. It is the shape evidence 1 is missing, and it is already
   in this repository rather than a design to invent.
4. `internal/git/cleanup.go` produces an exact inventory with per-target
   warnings and has had no production caller since slice 4. Nothing in it
   removes anything, and `CleanupPlanFor` already refuses a recorded path that
   is not inside the root Feat owns.
5. Slice 7 records `AgentSession.ProviderSessionID` from the session-start event
   before the process can fail, and `applyAgentEvent` suppresses the workflow
   change of a *continued* session start so that a user typing `/clear` does not
   move their task. The workflow table allows `failed` to reach `preparing`.
6. Measured against the installed Claude Code 2.1.220, in a real terminal inside
   a tmux 3.7b pane: `claude --resume <unknown-id>` prints
   `No conversation found with session ID: …` and exits 1. It does not open the
   interactive picker its `--help` describes for a bare `--resume`. So a resume
   that cannot find its session fails where somebody sees it, rather than
   producing the ADR-032 evidence-4 shape — a session that starts perfectly and
   has been given nothing.
7. `docker compose down --volumes` is all or nothing. It removes the volumes
   declared in the project's own Compose files, which is not the same set as the
   volumes a plan enumerated and named.
8. ADR-027 deferred `daemon.json` to "the first slice that reads a durable
   daemon record", and named this one. But no slice 12 acceptance criterion
   needs it: task identity survives a restart through task snapshots and tmux
   metadata, neither of which is a daemon record. Writing it here on the
   strength of the deferral alone would produce exactly what ADR-027 evidence 2
   refused — a versioned compatibility surface with no reader.
9. `docs/04-functional-specification.md` FR-CLEAN-002 names four destructive
   classes: containers/networks, volumes, worktrees, and branches. A task also
   owns a tmux window and a control workspace, and FR-CLEAN-001 requires the
   inventory to be exact.

Decisions:

- **Quarantine is one rule, stated for every resource class rather than for
  tmux.** An enumeration returns what it could read together with what it could
  not, and fails as a whole only when the enumeration itself failed. Evidence 3
  is the precedent and is named as one rather than left as a coincidence:
  `Pending` has behaved this way since slice 7. `tmux.Discover` returns a
  discovery carrying terminals and damaged entries. A damaged pane quarantines
  the terminal it belongs to, because half a terminal is not one; a damaged
  session quarantines its windows for the same reason. Everything else stays
  usable, which is acceptance criterion 5.
- Working-directory validation is settled with it, as ADR-030 said it would be:
  `CommandSpec.Directory` is checked with the same rule as the arguments, and
  that rule gains the tab. Quarantine bounds the damage a bad directory does;
  validation stops Feat creating one. Both are wanted, and neither replaces the
  other.
- `internal/reconcile` is a policy package under the boundary ADR-029 set for
  Git and ADR-036 for review, with a `reconcile-stays-a-policy` depguard rule.
  It owns the vocabulary of a finding, the cleanup classes, the plan token, and
  the consent rule. It reads no configuration and no persistent state, and it
  drives no adapter: the daemon observes, and this package decides what the
  observations permit.
- **Reported, never adopted.** Missing, orphaned, inconsistent, and damaged
  resources appear in a report and change nothing. Nothing in reconciliation
  starts a container, restarts a process, removes a directory, or adopts an
  orphan, which is FR-STATE-004 generalised from containers to every class.
- **The cleanup token names what, and the fresh observation decides whether.**
  The token covers the task, the resource identities, and the schema — not the
  warnings. A token computed over observations would expire whenever the agent
  wrote a file, so a user would learn that their plan was stale rather than that
  their worktree was dirty. Execute therefore re-resolves the plan, refuses a
  token that no longer names the same resources, and refuses a selection whose
  currently observed warnings the confirmation does not cover. A stale
  confirmation is the failure this rule exists to prevent; a refused one is an
  inconvenience.
- **Seven classes, each an independent choice.** The four FR-CLEAN-002 names,
  with the agent's containers and the application's kept apart because they are
  separate concepts everywhere else in this product, plus the task's tmux window
  and its control workspace, for evidence 9. A dead window and an audit trail
  that no cleanup can ever remove are resources a task owns and Feat cannot
  resolve, which is the thing FR-CLEAN-001 forbids. That they are an extension
  of the specification's list rather than in it is recorded here rather than
  discovered later; [04-functional-specification.md](04-functional-specification.md)
  moves in the same change.
- **Volumes are removed by name, never by `--volumes`.** They are enumerated
  from the container runtime's own project label and removed one at a time, for
  evidence 7: a plan that says exactly what will go and a command that removes
  exactly that is what "resolve the exact task-owned resources" means. It also
  makes the external-resource rule structural — a resource the project declares
  external carries no task label and cannot appear in the enumeration, so no
  code path has to remember to exclude it.
- `execution.Destroy` arrives as the method ADR-033 deferred, and removes
  containers and networks only. Volume removal is a separate method on both
  Compose adapters rather than a flag, so that "volumes are retained by default"
  is a shape of the interface rather than an argument somebody can pass wrongly.
- **Archiving is refused while the plan still names resources the user did not
  select.** An archived task is one Feat stops tracking, and stranding a running
  container behind it would manufacture precisely the orphan this slice exists
  to report. Archiving itself needs no new stored document: the task reaches
  `archived`, its snapshot keeps the branches, bases, and session it recorded,
  and the append-only event log carries what each class removed. So "Feat can
  explain what happened later" is satisfied by the two durable records slice 1
  already built, and no stored format changes.
- **Recovery of a dead agent session is an offered action and resumes the
  recorded session.** It re-plans the launch and passes the provider session
  identifier through a neutral `Resume` field, which the Claude adapter turns
  into `--resume <id>` with no initial prompt: a resumed session already holds
  its history, and inventing a prompt would be Feat putting words in the user's
  mouth. Evidence 6 is what makes this safe to offer — the failure is visible.
- Evidence 5's suppression narrows rather than disappears: a continued session
  start does not move a task **that is already working**. The rule was written
  so that `/clear` could not move a task's workflow, and that reason does not
  reach a task in `preparing`. Without the narrowing a resume would go
  `failed → preparing` and stop there, leaving a task that looks broken while
  its agent is running — so the resume takes the ordinary launch path,
  `preparing` to `working`, with slice 7's own silent-launch safety net behind
  it.
- A resume brings up a devcontainer, which is not FR-STATE-004's forbidden
  automatic restart: it is what a user asked for. The rule is kept structural by
  making the resume unreachable from reconciliation, and the report says a
  container will be started before the user commits to it.
- **`daemon.json` is written because it has three readers, not because ADR-027
  named this slice.** It records the state directory's own schema version, so a
  build older than the directory refuses it instead of overwriting documents it
  does not understand; whether the previous run ended cleanly, so a report can
  say a daemon crashed rather than leaving the user to infer it from an
  interrupted gate; and when it stopped, which is how long Feat was not looking.
  It carries no process identifier, socket, or lock — those stay in the runtime
  directory, which is the whole of ADR-027 evidence 1, and a durable record that
  acquired one would reintroduce the reused-identifier bug that decision
  prevents. Had these readers not existed the record would have been deferred
  again rather than written unread.
- The local API gains `GET`/`POST /v1/reconciliation`, the two cleanup endpoints
  [06-technical-architecture.md](06-technical-architecture.md) already lists,
  and `POST /v1/tasks/{task_id}/resume`. The endpoint list moves in the same
  change. `POST` on the reconciliation path re-runs the pass and records what it
  observed, which is the shape slice 9 used for runtime status and slice 11 for
  review observation.
- `feat cleanup <task>` has no blanket `--yes`. FR-CLEAN-002 requires separate
  choices and FR-CLEAN-003 explicit confirmation, and one flag that answers
  every question is the thing both rules exist to refuse. In a terminal it asks
  once per class in increasing order of risk and again for each warning;
  anywhere else it prints the inventory and removes nothing, which is the split
  ADR-027 made for `feat` and ADR-036 for `feat review`.

Consequence: the user-visible additions are `feat cleanup`, a TUI cleanup
screen, a resume action on a failed task, a recovery report, and five endpoints.
The command surface gains no flag, so its golden file is untouched. One stored
document is added and none changes, so no migration is needed; the event
vocabulary gains the additive types a removal and a recovery finding are
recorded as.

These decisions are recorded before implementation; evidence found while
implementing that contradicts one of them amends this ADR in the same change,
per the decision change process below.

Amended after running the slice against the real binary. Three defects, and none
of them was reachable from this repository's own fixtures:

10. **A daemon that shut down cleanly could never start again.** The claim
    carried the previous run's stop time forward into the new run's record, so
    the record described a run whose stop preceded its own start — which
    `DaemonRecord.Validate` refuses, correctly. Only a daemon that had *crashed*
    could start, because a crashed run leaves no stop time to carry.

    The suite missed it for a reason worth recording: every fixture in
    `internal/daemon` freezes the clock, so the carried stop and the fresh start
    were the same instant and the invariant held. The first regression test
    written for it passed against the injected defect. A daemon that is stopped
    and started has a clock that moved, and the test now moves one.

    Decision: a record describes one run — its start, and its stop once it has
    one. What the previous run left is read once when the state directory is
    claimed and held in memory for that run's reconciliation, because claiming
    replaces the record on disk with this run's own. The invariant stands
    unchanged; it was right and the writer was wrong.

11. **A live task's own directory was reported as an orphan a user should
    delete.** With a worktree root of `…/worktrees/{project_id}/{task_id}`, the
    directories the orphan scan listed were task directories rather than
    worktrees, and comparing only for equality found nothing claiming them. The
    report said "a directory under the project's worktree root that no task
    records" and advised removing it if it looked stale — about the directory
    holding both of a running task's worktrees.

    It is the worst shape a false positive can take here, because the product's
    whole discipline is that Feat reports and the user acts: a report that
    recommends the wrong deletion turns that discipline against the user. The
    same scan also missed the orphan it existed to find, since an abandoned task
    directory sits at the same depth.

    Decision: the scan descends only where a task's own paths lead. A directory
    that holds something a task records is walked into rather than reported; one
    that holds nothing is reported. The walk is bounded by the template's depth
    rather than by a number somebody chose, and it works for a root whose
    worktrees are direct children as well as for one whose are not.

12. **The plan token did not cover which repository a target belongs to.** One
    branch template gives every repository of a task the same branch name, so a
    two-repository task's inventory printed the same branch twice — which is what
    made it visible. A token over the name alone cannot tell a plan naming one
    repository's branch from one naming another's, while the removal is pointed
    at a repository by exactly the field the token omitted.

    Decision: the repository is part of what a target is, and the token covers
    it. Nothing else changes; the plan already carried the field, and the
    removals already used it.

13. **The one task that most needed recovery was the one that could not have
    it.** Resuming transitioned unconditionally to `preparing`, and the ordinary
    state of a task whose agent died is not `failed` — it is `working` with a
    failed process. A process that dies while no daemon is watching leaves the
    workflow where it was, and reconciliation reports the dead process rather
    than moving it, because reporting instead of repairing is the whole rule. So
    `working` is the state a resume mostly meets, and `working` has no edge to
    `preparing`, correctly: the work did not go back to being prepared.

    Found by resuming a real task whose devcontainer had been killed a day
    earlier; it failed in fifteen milliseconds with a generic message, before
    touching Docker. The fixtures all reached the resume from `failed`, because a
    test that arranges a dead agent naturally arranges the whole of it.

    Decision: only a `failed` task is moved to `preparing`, and only a task this
    call moved is moved back when the launch fails. A task that was already
    working stays working — its agent is dead either way, and a failed resume is
    not new information about the work. `ensureTerminal`'s confirmation gate
    admits a restart for the same reason, while still refusing a draft and an
    archived task.

14. **The recovery band could never be brought up to date.** Reading the last
    pass and running a new one are deliberately different requests, because a
    pass asks the container runtime about every task and the dashboard refreshes
    every two seconds. But nothing in the dashboard ever ran one: the periodic
    refresh and the refresh key both re-read, so the band went on describing the
    pass that ran when the daemon started. A user who resumed a task or cleaned
    one up was still being told about resources they had just dealt with, and
    the only way to clear it was to restart the daemon.

    Reported by the maintainer from the dashboard itself, which is the only
    place it is visible: every test asserted the band's *content* against a
    report handed to the model, so none of them could notice that no report ever
    arrived twice.

    Decision: the split stands — the periodic refresh still only reads. What
    changes is that the two actions which resolve a finding, a resume and a
    finished cleanup, run a pass, and so does the explicit refresh key, because a
    user who pressed it is asking for what is true now and the cost is theirs to
    spend. The band also says when it looked, since everything it names can be
    acted on from the screen it is on: one with no time on it reads as current
    however old it is.

### ADR-038 — Naming a task

Status: accepted  
Recorded: slice 13

Evidence found while running the product by hand, across slices 9 and 11:

1. Every `<task>` argument took the whole 36-character identifier, and no list
   printed one. `feat task list` prints the eight-character key, so does the
   dashboard, and so does every desktop notification slice 10 added. The
   identifier appeared in exactly one place, the dashboard's task detail. The
   identifier a user could see was therefore the one no command accepted, and a
   user reading `feat attach <task>` in the documentation had nowhere to get the
   argument from. Found by running `feat runtime status` with the key the task
   list had just printed.
2. The rejection made it worse rather than better. It explained the format of an
   identifier — "must be a version 4 UUID in canonical lowercase form" — to
   somebody who had no way of seeing one. A message that describes a format is
   only useful to a user who can produce a value in it.
3. It is one defect on the whole command surface rather than one command's:
   `attach`, `review`, every `runtime` action, and `cleanup` all had it, and it
   had been there since slices 5 and 6. Thirteen endpoints read a task
   identifier out of a path, through one helper.
4. A key is unique within a project and not across the machine (ADR-026 resolves
   a collision by generating another task identifier, per project). Two projects
   can therefore hold tasks whose keys share a prefix, and one of the commands
   that takes a task is `feat cleanup`.
5. Storage addresses a task by project and task together, and the daemon already
   resolves the owning project for a caller that holds only an identifier
   (ADR-027). Resolving a key is the same kind of question asked one step
   earlier.

Decisions:

- A task is named by a `domain.TaskRef`: its short key, its whole identifier, or
  any prefix of that identifier. Case is folded, because an identifier copied out
  of somewhere that upper-cased it is still that identifier.
- A reference is a prefix of an identifier and nothing else — lowercase
  hexadecimal with a `-` where the canonical layout has one. It is deliberately
  not a search over titles or branches: a title that became a way of addressing a
  task would make what a command acts on depend on text a user typed for another
  purpose.
- Ambiguity is reported with every candidate, named as the key and project the
  lists print, and never resolved to one of them. This is the rule ADR-029 set
  for a colliding branch name, applied where `feat cleanup` can reach.
- Every task is a candidate, archived ones included, so that a cancelled draft
  can still be cleaned up. An archived task can therefore make a reference
  ambiguous; preferring the live one would be the guess this rule refuses.
- Resolution lives in the daemon, beside the project resolution ADR-027 put
  there, and the matching rule itself is a pure function in `internal/domain`.
  The API resolves at its single task-identifier helper, so no endpoint can be
  added that misses it. A whole identifier is used as it stands rather than
  resolved, which is the path the dashboard takes on every request it makes.
- Both refusals name where a valid value is printed. ADR-027 mapped a domain
  validation error to `400` and a missing record to `404`; ambiguity is a `400`
  through the existing `domain.ErrInvalid` class, so no new error code enters the
  published surface.
- The validation this replaces was justified by keeping a malformed value out of
  a path join. Resolution is stronger: what a handler receives is an identifier
  the daemon read out of storage, so no value from a request reaches a path at
  all.

Consequence: `docs/06-technical-architecture.md`, `docs/README.md`, and the
README were updated in the same change, and the help of every command that takes
a task now says what the argument is. The command surface is unchanged, so its
golden file is unchanged. This amends ADR-027's decision that "the local API
addresses a task by task identifier"; the addressing boundary is where it was,
and only what counts as naming a task moves.

### ADR-039 — Proving a notification arrived

Status: accepted  
Recorded: slice 13

Evidence found while walking every notifiable condition against a real desktop:

1. A task that passed its completion gate reached `ready_for_review` and the user
   was not told. Observed by hand while exercising slice 11. The state, the
   event, and the review record were all correct, so what was missing was the
   interruption rather than the transition.
2. Slices 10 and 11 each added notifications with unit tests over a fake
   notifier, and a fake notifier proves the daemon asked rather than that anybody
   was told. No test crossed the boundary the defect was on.
3. `notifyTask` had five paths that did not deliver. Four returned silently and
   one logged at debug, so "why was I not told" was a question the daemon's own
   log could not answer.
4. A notification that does not arrive leaves nothing behind. The state change it
   was about is recorded correctly either way, and macOS drops an unauthorised
   notification without saying so, so a policy Feat applied on purpose and a
   notification the desktop swallowed are indistinguishable after the fact.
5. `notify.Absent.Notify` documents that "a caller checks Available first and
   never reaches this", and `notifyTask` did not. On a build that delivers
   nothing, every notifiable change reached a notifier that refused it and was
   logged as a failed delivery rather than as a platform that has none.
6. The condition for failed application services had no test that reached it, and
   could not have had one: the Compose fixture hard-coded `ExitCode: 0`, and a
   non-zero exit is what separates a failed runtime from a stopped one. The walk
   found it — six conditions delivered and this one did not.

Decisions:

- Every path that does not deliver names the policy that stopped it, at info,
  with the task, its key, the project, and the condition. Info rather than debug
  because the reader is a user working out why they were not told something, not
  somebody debugging Feat.
- It is a log line and not a task event. A suppressed notification is not
  something that happened to the task, and recording an event publishes it, which
  is a step towards notifying somebody about not having notified them.
- The reasons are phrased as the user's own setting or situation —
  `notifications.desktop`, `notifications.suppress_while_attached`, being
  attached, a daemon still catching up — rather than as internal state.
- Whether this platform can deliver is asked once at startup and kept, so a
  platform that delivers none drops a notification saying that, rather than
  handing it to a notifier that refuses it.
- Every condition is walked against a real desktop by an opt-in test that drives
  the actual state change — a hook, a control message, a gate over the project's
  own checks, a runtime observation — and asserts both that Feat handed one over
  and that it recorded doing so. `notify.Conditions()` exists so a condition
  added later has to be walked too: a list extended by hand is a list that
  eventually is not.
- The walk asserts delivery and never sight, which is all Feat can know. What it
  prints is what to compare against the desktop, and the two logs that tell the
  two failures apart.
- ADR-036's suppression of `review_requested` while a gate is about to run
  stands. The concern that a gated task might arrive more quietly than an ungated
  one was the reason for the walk, and the walk shows both arriving.

Consequence: [06-technical-architecture.md](06-technical-architecture.md) and the
README were updated in the same change. No notification policy changed: the
conditions, the tables, the grace periods, and the suppression rules are exactly
what slices 10 and 11 decided. What changed is that not being told now has an
answer, and that every condition has been shown to reach a desktop once.

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

### OQ-012 — Per-process processor resolution on Linux

`ps` reports cumulative processor time in whole seconds on Linux and in
centiseconds on macOS, so a per-task processor figure differenced over a
two-second interval resolves to about one point on macOS and to fifty on Linux
(ADR-035 evidence 16). Reading `/proc/<pid>/stat` on Linux would close the gap at
the cost of a platform-specific process reader beside the platform-specific
machine readers that already exist.

Whether that is worth doing depends on evidence this version cannot supply: how
often a task's processor figure is the one a user acts on, rather than the memory
figure or the attention badge beside it. Decide it against dogfood use, not
before. Until then the dashboard shows what was measured and the figure is coarse
rather than wrong, which is the rule ADR-028 set.

## Decision change process

During implementation:

1. Record new evidence.
2. Identify affected requirements and milestones.
3. Add or amend an ADR.
4. Update linked specification files in the same change.
5. Do not silently let implementation behavior become the specification.
