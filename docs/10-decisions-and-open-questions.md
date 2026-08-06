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
- Verification state is agent-reported in slice 7. The review request carries what the agent says it ran, and the dashboard labels it as the agent's claim. The provider-native completion gate that runs configured `checks` waits for slice 8, which has an environment to run them in, so `verifying` and `verification_failed` stay unreached and the interface names the slice that reaches them. This narrows what ADR-031 promised for the dashboard's verification column, and narrows it in the direction ADR-028 established: a value that was never measured is never displayed as one.
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
