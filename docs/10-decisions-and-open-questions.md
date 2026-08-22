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
9. `feat daemon start` needs a foreground process to spawn, and the launchd/systemd units in slice 17 need a command to invoke.

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
- The JSON Schema in `schema/feat-project.schema.json` is hand-written and kept in step with the Go types by a test that compares field names in both directions. Slice 17 finalises it. `docs/examples/project.yaml` is validated by the test suite, so the file a new user copies cannot drift from what Feat accepts.
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

Amended a third time, after the alpha review traced acceptance criterion 1
through the generated document:

15. **The reset covered the agent's service and not what starting it starts.**
    `Prepare` runs `docker compose up --detach <service>`, which brings up that
    service's whole `depends_on` closure, and the override named one service. So
    a devcontainer whose `dev` depends on a `db` with a fixed `container_name` and
    a published port ran once per machine: the second task's launch was refused by
    Docker over the first task's `db`, in a message about a service the user did
    not know Feat was starting. It is ADR-034 evidence 12 exactly — "the one thing
    a per-task Compose project exists to prevent, reintroduced by the services
    nobody had listed" — and the runtime adapter had already fixed it while the
    execution adapter had not.

Decisions:

- The generated execution override reaches every service the project's own
  Compose files define. Which they are is read with
  `docker compose config --services` against those files, without the generated
  override so a stale one cannot reintroduce a removed service and so the first
  launch does not fail on a file that does not exist yet. It prints names and
  nothing else, so evidence 5 of ADR-034 still holds: no environment-file value is
  rendered.
- **A service the agent does not run in has both `container_name` and `ports`
  reset**, which is deliberately not what ADR-034 decided for the application
  runtime. ADR-034 keeps a published port because it is how the user reaches the
  application they are testing; a dependency of a devcontainer is not that
  application. Feat surfaces no port from an `feat-agent-*` project, services in
  one Compose project reach each other over its network rather than through a
  published port, and a host port left in place is acceptance criterion 1 failing
  on the second task whatever the container name says. The cost is stated rather
  than hidden: a devcontainer dependency the user reached at a fixed host port is
  no longer published. The alternative is that the second task cannot start.
- Such a service gets those two lines and nothing else — no task worktree, no
  generated variable, and no ownership label. Labels here are not ADR-034's: they
  are how `feat doctor` finds the container the agent runs in without reading
  stored state, and putting them on a database would send that diagnostic looking
  for Claude inside Postgres. Cleanup does not need them, because it resolves what
  a task owns through Compose's own project label.
- The task detail says the reset covers the agent's service and everything Compose
  starts alongside it, in fixed words, as it already said the narrower thing.

Found by review rather than by running, and the reference project could not have
found it: its devcontainer defines one service, with no `depends_on`, no
`container_name`, and no published port, so both resets were already no-ops there
and acceptance criterion 1 held before this change as well as after — verified by
two tasks running side by side on it, each with one container, alongside the
maintainer's own hand-started devcontainer. The defect is reachable for any
devcontainer whose agent service has a name- or port-bearing dependency, which is
the ordinary shape of one that develops against a database.

So the reproduction lives in the opt-in fixture rather than in the dogfood
project: its devcontainer depends on such a service, and against the previous
generator the second of three tasks fails to launch with Docker's own conflict
message. That is the whole of the evidence, and it is worth being exact about
which project it comes from.

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

  **Superseded for ports by ADR-065.** The rule rested on the second half of its
  own reason: that a published port is how the user reaches their application and
  that v0 allocates none of its own. v0 now allocates them, so what a task
  publishes is a port Feat can tell the user about rather than a number only one
  task can hold, and every other publication in the project — a managed service
  the project did not declare reachable, and a dependency it never named — is
  reset like the container name it sits beside. The explanation this rule offered
  instead was an explanation of a task that could not start.
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

**Extended by ADR-065**: it also loses its published ports, which is the second
global value this evidence is about — the database of this very defect kept
5432 and no second task could ever have bound it. A dependency's port is not
replaced by an allocated one, because a service the project never named is not a
service it asked to reach; a project that does reach one manages it and declares
it reachable. The rest of the rule stands: no worktree, no generated variable,
nothing else.

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

Amended a fourth time, after the first create a user asked for on a new task:

13. **`docker compose create` does not build what it is about to create.**
    Given `docker compose create api`, where `api` depends on a service built
    from the project's own Dockerfile, Compose builds the image of `api`, then
    creates a container for the dependency from an image it never built, and
    fails with `No such image: feat-<project>-<task>-prepare:latest`. `up` on the
    same services builds the whole closure. The image name carries the Compose
    project name, which is per task, so no image exists the first time a task is
    created — create failed on every new task and start on none, which made the
    action look broken rather than the command wrong. Measured on Docker 29.5.2
    and Compose 5.1.4, with and without bake, and with `--build`, which does not
    change it.

Decision: create is `docker compose up --no-start` over the managed services.
It builds the dependency closure, creates every container in it, and starts
nothing, which is what FR-RUN-005's create means; against containers that
already exist it does exactly what `create` did. The name of the Compose
subcommand was never the contract — the state the user asked for is — and this
is the same shape as evidence 12: an action Feat targets at the managed services
has to account for everything Compose brings with them. The opt-in fixture's
one-shot dependency is now built rather than pulled, so the defect fails a test
against real Docker rather than waiting for the next new task.

Amended a fifth time, after a user started a task's services for the first time:

14. **The client stopped waiting before the work could finish.** A
    `runtime/start` failed with `Post "http://feat/v1/tasks/…/runtime/start":
    context deadline exceeded` and succeeded when the user asked again.
    `internal/client` bounded every request at ten seconds, on the reasoning that
    the daemon is local and a local request that takes longer is stuck rather
    than slow. That reasoning holds for every endpoint that answers out of what
    the daemon already knows and for none of the ones where it drives Docker: a
    first start pulls the images the project names and runs the builds it
    defines, and the second start of the same task answers in about a second —
    which is why the ceiling is invisible until the first run of a project
    nobody has built on that machine, and why trying again looks like a fix.

    It is the launch defect of slice 13's work list, at a second endpoint and
    with worse consequences. The client's deadline cancels the request, the
    daemon's handler context is the request's, and `exec.CommandContext` kills
    the process when its context ends — so the ten seconds did not only produce
    a misleading error, it killed a `docker compose up` part way through
    creating a project. Nothing was written about it anywhere: the failure
    classifies as an invalid request, the daemon logs only what it answers with
    a 500, and the connection the answer would have gone to had already gone.

Decision: one manual runtime action has one budget, `api.RuntimeTimeout`, which
lives in the API package because it is a term of the endpoint's contract rather
than either end's private business. The daemon bounds the whole action with it
instead of relying on the ten minutes `runtime.HostRunner` allows each Docker
command, so the ceiling is a single number rather than an unknown multiple of
one. The client waits for that number plus a minute, the same margin the daemon
already allows a completion gate over its own timeout, so what ends a slow
request is the daemon's diagnosis rather than the client's silence.

Both ways of running out — Feat's own budget, and a caller that went away — are
reported as what they are, name the action and what it did not finish, and point
at `feat runtime status`, because what they leave behind is a Compose project
that may be half created. The record already names it before anything is created
(ADR-029, ADR-033), the observer corrects the state within one poll, and neither
path undoes anything: tidying up after a start that was interrupted is the
destructive act nobody asked for. A caller that went away is logged as well,
since by definition there is nobody left to answer.

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
  support stays slice 17's, and this build says so rather than failing silently
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
   started by launchd or systemd gets, which is how slice 17 intends a daemon to
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

### ADR-040 — Where a command lives

Status: accepted  
Recorded: slice 13, before implementation

Evidence found by reading the command surface rather than by running it, which is
why it is recorded before the change rather than after a failure:

1. The surface is two designs at once. `project`, `task`, `runtime`, and `daemon`
   are nouns with verbs beneath them; `implement`, `attach`, `review`, `cleanup`,
   and `doctor` are verbs at the top level. ADR-001 already decided for the first
   shape — "use scoped commands such as `feat project add`" — and five commands
   arrived in the second one, each added by the slice that needed it.
2. The seam runs through the `task` noun. Every command that takes a `<task>` is
   an operation on a task: `attach`, `review`, `cleanup`, and all six `runtime`
   actions. `feat task --help` lists one of them, `list`, and it is the least
   interesting one. A user who has learned `feat task list` has no route from
   there to `feat attach` except the documentation.
3. ADR-038 is the field evidence that these are one family rather than five
   separate commands. A single defect landed on `attach`, `review`, every
   `runtime` action, and `cleanup` together, through one helper, because naming a
   task is what they have in common. A defect whose extent is exactly that set is
   a set the surface should name.
4. The present shape can be justified, and the justification does not survive.
   Top level for what opens a screen or hands over the terminal, a noun for what
   prints and exits, fits today's commands. It is already leaky — `review` and
   `cleanup` both print without a terminal (ADR-036, ADR-037) — it sorts commands
   by a property a user cannot see from outside, and adding `feat task show` or
   `feat task stop` would leave the top-level verbs reading as whatever was there
   first.
5. The window is this slice. The surface is pinned by a golden file and described
   in three documents, slice 13 is already rewriting every `<task>` argument for
   ADR-038, and slice 17 publishes v0.2. After that, moving a command breaks a
   shell history that is not Feat's to break.
6. Found while implementing this: cobra's own "Did you mean this?" had never
   fired, for any command. It is built on the path cobra takes for a command
   with no `Args` of its own, and every command in this tree has one, so
   `feat tsk` was answered with "unknown command" and nothing else. A moved name
   made it visible; a typo had always asked the same question.

Decisions:

- A command that takes a task lives under `feat task`: `task attach`,
  `task review`, and `task cleanup`, beside `task list`. That is the rule, and it
  is the one ADR-001 stated.
- `feat implement` stays where it is and is not renamed to `feat task add`. It
  does not take a task, it produces one, so the rule above does not reach it. The
  name is also the activity — it fetches, resolves an immutable base commit per
  repository, proposes branches and worktrees, and launches an agent session —
  while `add` describes appending a row to a list. `feat task`'s own help names
  it as where a task comes from, so the noun a user explores still answers that
  question.
- `feat attach` and `feat review` keep their top-level names as aliases, because
  brevity is earned by how often something is typed and these are typed all day.
  Both are hidden from help so that `feat --help` stays equal to the documented
  surface, which is ADR-027's rule for `feat daemon run`, and both appear in the
  golden file, which walks hidden commands.
- `feat cleanup` gets no alias. It is rare and it is irreversible, and making the
  longer path the only path is the argument ADR-037 made when it refused a
  blanket `--yes`. What it gets instead is a rejection that leads somewhere: the
  old name is answered with the noun that now holds it. There is no compatibility
  shim behind that, because nothing has been released and a shim would put the
  name back on the surface this ADR took it off.
- The suggestion is built where a command's positional arguments are checked,
  which is the one place every rejection passes through, so an unknown word gets
  it whether it is a name that moved or a name that was mistyped. Restoring it
  only for `cleanup` would have left evidence 6 in place for everything else.
- One implementation with two names, never two implementations. The alias is
  built by the same constructor and runs the same `RunE`; cobra sets a parent in
  `AddCommand`, so one value cannot hold both positions, and two bodies would
  drift. An alias says in its own help which command it is.
- `feat runtime` stays a top-level noun rather than becoming
  `feat task runtime`. A feature environment is a co-equal thing a task owns,
  with its own identity and lifecycle (ADR-003, ADR-034), rather than an
  attribute of the task, and three levels before an argument is worse than the
  inconsistency. This is recorded as an exception rather than dressed up as a
  rule, because it is one.
- `feat project`, `feat daemon`, `feat doctor`, and `feat version` do not move. A
  diagnostic named `doctor` at the top level is what the tools this one is
  installed beside already do.
- The asymmetry between `feat runtime destroy --yes` and a `feat task cleanup`
  with no such flag is deliberate and stays. What changes is that each says why in
  its help, because a user who meets the second after the first reads it as an
  omission rather than as a decision.

Consequence: the golden file, [README.md](README.md),
[06-technical-architecture.md](06-technical-architecture.md), and the README
moved in the same change, which is the rule ADR-028 and ADR-031 followed for a
command-surface change. Nothing crosses the socket differently: no endpoint, no
domain type, and no storage path changes, and ADR-038's rule for naming a task is
untouched. Three reconciliation findings that told a user to run `feat cleanup`
now name the command that exists.

Two things this deliberately leaves alone. Machine-readable output is a separate
gap — every command can be read by a person and parsed by nothing — and belongs
with slice 17's JSON Schema. And an unknown subcommand of a group, `feat task lst`
or `feat daemon bogus`, still prints the group's help and exits zero, which
predates this change and is the same on every group: a script cannot tell that
one from a command that ran.

### ADR-041 — What the dashboard is shaped like

Status: accepted  
Recorded: slice 13, before implementation

Evidence found by reading the dashboard against the terminal it is read in, after
the maintainer reported it as confusing while preparing the three-task dogfood
runs:

1. A task row does not fit. `taskColumns` is eleven columns totalling 136 cells,
   joined by two-space separators and preceded by a two-cell cursor marker, so a
   row is 158 columns wide (`internal/ui/task.go:43`, `internal/ui/style.go:54`).
   A standard terminal is 80 and a wide one is 120 to 160. Every row wraps onto
   the next, which is why a list of three tasks reads as nine lines of unaligned
   text rather than as three tasks.
2. The requirement is what makes it that wide. FR-UI-002 names nine fields a row
   MUST show, and each of them earned its place. So the row cannot be narrowed by
   dropping columns without changing the specification, and the only remaining
   move is to stop putting all nine on one line per task.
3. Every screen replaces every other. `screen` has six values and each renders
   the whole terminal (`internal/ui/app.go:59`). Opening review, runtime, or
   cleanup discards the task list, so a user watching three tasks can look at one
   of them. Three concurrent tasks is the case v0.1 acceptance criterion 1 is
   about, and criterion 14 — that the user no longer manually coordinates
   sessions, paths, and branches — is a claim about whether this screen carries
   the coordination. A screen that shows one task at a time hands part of it back.
4. Nothing has a fixed position. The dashboard stacks a heading, an attention
   summary, a machine card, a recovery band, the table, an archived note, and
   twelve key hints in one column, and the recovery band appears only when
   reconciliation found something (`internal/ui/dashboard.go:11`). So the row a
   user's eye learned moves down the screen on the day something breaks, which is
   the day the position mattered.
5. The TUI is width-unaware. `m.width` is recorded on `tea.WindowSizeMsg` and
   read by task preparation alone (`internal/ui/app.go:352`); every other view
   renders at fixed widths. Whatever fixes evidence 1 has to introduce
   width-awareness, and a layout is the thing that would consume it, so the two
   are one job rather than two.
6. FR-UI-001 already asks for "project drill-down" and the dashboard is a flat
   global list. Grouping by project is the requirement rather than an addition to
   it.
7. The main region cannot hold the live agent session. Attachment is the native
   tmux client inheriting this process's terminal (`internal/cli/attach.go:51`,
   released by `internal/ui/app.go:522`), which ADR-030 decided deliberately. To
   render that session inside a Bubble Tea viewport, Feat would run tmux under a
   pty and become a terminal emulator — escape-sequence parsing, resize
   propagation, key and mouse forwarding, scrollback — which is larger than the
   rest of this change together and puts a second emulator between the user and a
   multiplexer.

Found while reviewing the first draft of this ADR, which had decided to keep the
row and move it:

8. Moving the table does not make it fit. A rail of about 30 columns leaves 128
   of a 160-column terminal and 88 of a 120-column one, against a row of 158.
   Scoping the table to one project saves the width the project implied and
   nothing like the sixty columns needed. So relocating the nine fields was not a
   decision, and the row has to lose fields or lose the single line.
9. FR-UI-002 restates requirements that already exist. Four of its nine fields
   are named by another requirement: repositories by FR-UI-003's "repository/base
   mapping", runtime state by its "runtime services", verification state by its
   "completion/check summary", and resource usage by FR-UI-005's "per-task
   environment totals". The five that no other requirement claims — identity,
   agent state, attention state, elapsed time, changed-file count — are exactly
   the fields that answer which task to go to next. The duplication is historical:
   the row was the only place a field could live, because the dashboard had no
   persistent region beside it. A layout that always shows one removes the reason.
10. An overlay costs less than a modal screen and preserves more.
    `github.com/charmbracelet/x/ansi` is already in the module graph as an
    indirect dependency and cuts styled text by cell, so compositing a dialog over
    rendered content is a small helper rather than a dependency change; lipgloss
    v1.1.0's `Place` fills a whitespace box and cannot layer. A full-screen modal
    discards the task list for the duration, which is evidence 3 again, and an
    overlay does not.

Found by the maintainer using the first build of this layout, and fixed in it:

11. Two of the tabs ended the tab cycle. Review and runtime answer their own keys
    and return for everything else, so the key that was meant to leave them never
    reached the frame: `tab` moved overview, detail, review, and then stopped. The
    same shape had a second exit — both views refuse a draft without changing the
    screen, so cycling onto one would have stranded the user too.
12. The rail was unreachable from half the dashboard. The plain arrows belong to
    whichever view has the keyboard, and review spends them on its repository
    cursor, so from the review or runtime tab there was no way to change task at
    all. The rail is the thing this layout is built around and two of its four
    tabs could not reach it.
13. The events tab could not answer the question it existed for. It showed the
    state changes this client had seen, so it was empty on open and could never
    describe what happened while the user was away — which is the only reason to
    look. The daemon's log holds the record.

Decisions:

- The dashboard is three regions that persist: a left rail, a main region, and a
  footer. Only the main region's content changes. This replaces the model where
  each screen owns the terminal.
- The left rail is a task selector grouped by project, and it is not the task
  row. It carries the five fields evidence 9 found unclaimed — the
  eight-character key, the title, attention, agent state, elapsed time, and the
  changed-file count — over two lines per task, which is what buys the width back.
- The rail shows attention and agent state as two things and never as one. A
  glyph carries attention and a word carries the process state. Feat keeps
  process, attention, workflow, and runtime states separate in the domain, and a
  composite status badge would put them back together in the one place a user
  actually reads. Colour cannot carry the distinction either, because a terminal
  without it would lose the distinction silently rather than visibly.
- FR-UI-002 becomes a requirement about what the task list carries for triage and
  stops restating FR-UI-003 and FR-UI-005. No field loses its requirement; four
  of them stop having two. This is the specification following the code's shape,
  which is what ADR-028, ADR-031, and ADR-040 each did.
- The main region is tabbed over views of the selected task: overview, detail,
  review, and runtime. A tab is a view you leave and come back to, and its state
  survives the leaving. ADR-042 replaced this set with terminal, task, and
  runtime; what stands here is the shape of a tab, not the list.
- There is no events tab. It was built and removed after the first use: it could
  only show what had happened since the dashboard opened, so the one thing a
  user would want it for — what happened while they were away — is what it could
  not answer, and the daemon's log holds the record either way. Removed rather
  than kept cheaply, because a tab that looks like a history and is not is worse
  than no tab.
- The layout's own keys are answered before the tab's. Moving between tabs and
  moving between tasks belong to the frame, and a view with its own keyboard
  returns for every key it does not recognise, so it would otherwise swallow
  them. No tab declines to open: a tab that refuses is a tab the cycle cannot
  pass, so one with nothing to show for the selected task opens and says so. That
  replaced an earlier fallback to the detail tab, which worked only because the
  refusing tabs happened to come after it in the order.
- Selecting a task has a pair of keys of its own, distinct from the plain
  arrows, and they work from every tab. The arrows belong to whichever view has
  the keyboard — review moves a repository with them — so a rail reachable only
  by them is a rail unreachable from half the dashboard. Changing the task
  re-opens the tab for it, because a view holding one task's services under
  another task's name is worse than a view that reloads.
- Task preparation, cleanup, every confirmation, and the key map are overlays
  over the live dashboard rather than screens that replace it. An overlay is for
  something that is not about the selected task, or that must be answered before
  work continues; it ends, by completion or by cancellation. Preparation is the
  clearest case, because it has no selected task — it produces one.
- The size test decides an ambiguous case. Something that needs the whole main
  region is not a dialog, whatever it is called. That is why review and runtime
  are tabs and their decisions — approve, request changes, destroy — are overlays.
- An overlay closes on a keybind without ceremony, including cleanup's. This is
  not ADR-037 eroding: a cleanup plan is inert until it is confirmed, so
  discarding an unexecuted one costs nothing, and what ADR-037 made deliberate was
  triggering an execution rather than opening a screen. An overlay whose execution
  has started is not dismissible.
- The overview tab keeps the wide table, provisionally. It is the only place
  resource usage or check counts can be compared across tasks, which is FR-UI-005's
  case, and it is also the part of this design with the least evidence behind it.
  It is kept for the three-task runs and removed if those runs never use it —
  recorded here so that a later reader knows it was a question rather than a
  preference. **Superseded: removed, see ADR-043.**
- Attachment stays a handover. Pressing attach yields the terminal to tmux and
  returns to the tab the user left, which is what ADR-030 decided and what
  evidence 7 says the alternative costs. Feat does not embed a terminal emulator
  in v0.
- The footer carries the selected task's worktree path and the machine's
  resources, which moves the machine card out of the vertical stack, and the key
  hints for the focused region rather than all twelve at once. **Amended: the
  machine's figures moved on to the foot of the rail, see ADR-044.**
- There is a minimum width. Below it the three regions collapse to the single
  column that exists today, because a rail and a main region inside 80 columns
  gives neither one enough to be read.
- The recovery band keeps a fixed position rather than appearing between other
  things, so that evidence 4 does not survive the change that was made for it.
- `internal/tmux` gains the split direction deferred on 2026-08-06: a shell pane
  beside the agent rather than below it. It is the same question — what a user
  sees when they look at a task — and it was parked for the moment every screen
  existed, which is now.

Consequence: [04-functional-specification.md](04-functional-specification.md)
FR-UI-001 and FR-UI-002 move with the code, which is the rule ADR-028, ADR-031,
and ADR-040 followed. `github.com/charmbracelet/x/ansi` becomes a direct
dependency, which it already was in effect. Nothing crosses the socket
differently: no endpoint, no domain type, and no storage path changes. The
dashboard's keys are not
renumbered, because a user who learned them during dogfood is the only user there
is. This is presentation work, which the maintainer batches for after every screen
exists; it is taken now rather than in slice 17 because the three-task runs are
read through this screen, and a screen that shows one task at a time cannot
produce evidence about three.

### ADR-042 — Showing the agent's terminal without becoming one

Status: accepted  
Recorded: slice 13, before implementation

ADR-041 built the dashboard around views Feat writes itself, on the reasoning
that the alternative was a terminal emulator inside Bubble Tea. The maintainer
used it and said it was not what they wanted: the main region should hold the
agent's session, and possibly a shell beside it. The reasoning was right about
the emulator and wrong about the alternatives.

Evidence:

1. The main region is thin without a terminal in it. Detail and review are
   conceptually different and share most of their content — workflow,
   repositories, checks — and neither fills the region at 120 columns. The
   region was built for something substantial and given something that is not.
2. Moving the agent's pane next to the rail costs an accepted decision. Measured
   against tmux 3.5a on a scratch socket: `join-pane` preserves the pane id and
   every `@feat_*` option, so identity survives, but the pane's window changes
   and the source window is destroyed when its last pane leaves. ADR-030
   requires matching metadata at session, window, and pane scope, and slice 12's
   reconciliation reads it, so a joined pane is a task Feat can no longer
   discover. Pane surgery is therefore not the cheap path it looks like.
3. Two tools solving this problem move no panes and emulate no terminals.
   claude-squad renders a preview with `capture-pane -p -e -J` and attaches for
   real with `attach-session` over a pty. agent-manager holds a persistent
   `tmux -C` control-mode connection, redraws on the `%output` notification tmux
   pushes when a pane paints, and sends input with `send-keys` and with
   `load-buffer`/`paste-buffer -p` — the latter because `send-keys` truncates
   around a kilobyte and bracketed paste stops an application swallowing the
   trailing Enter. Their own note on control mode is the argument for it: a
   focused preview then needs no per-tick process forks and no polling.
4. Rendering tmux's output is not emulating a terminal. tmux owns the pty,
   interprets the program's escape sequences, and maintains the screen grid.
   `capture-pane -e` returns that finished grid as text with colour attributes.
   What is left for Feat is placing a rectangle and cutting it by cell without
   splitting a sequence, which is what `internal/ui/overlay.go` already does for
   dialogs.
5. The TUI cannot hold the connection. The `ui-is-a-client` depguard rule denies
   `internal/ui` any import of `internal/tmux`, as `cli-is-a-client` does for the
   CLI. That rule is the executable form of "the daemon is the only writer", and
   sending keys to an agent is a write.
6. Preparing a pane for display is not free and not idempotent, which evidence 3's
   preference for control mode understated. Measured against tmux 3.7b with a
   zoomed two-pane window already sized 179x52, reading `stty size` inside the
   pane: a `resize-window` to the size the window already has sets the zoomed
   pane's pty to 90 columns — its share of the split — and then back to 179. tmux
   reports `pane_width` as 179 throughout, so the window looks motionless from
   outside while a full-screen program repaints itself at half the region's width
   and repaints again. The same measurement puts a settled frame's five `tmux`
   invocations at 30.5 ms and the two it actually needs at 16.3 ms, which at a
   60 ms focused poll is half the interval spent forking processes.
7. A resize is a request to repaint, and a pane whose program has ended cannot
   answer it. tmux reflows the screen instead. Measured against tmux 3.5a, a
   pane retained by `remain-on-exit` holding a full-width prompt, sized from 20
   columns to 14 and back:

   ```
   20 columns              14 columns
   │ > type here      │    │ > type here
   ╰──────────────────╯         │
                           ╰─────────────
                           ─────╯
   ```

   The maintainer reported it as the terminal tab drawing an agent's prompt over
   two rows, for some tasks and not others: the tasks whose agent had stopped.
   Feat keeps those panes on purpose — a stopped agent's pane is the account of
   what the session did (ADR-030) — and the same measurement back at 20 columns
   returns the box whole, because tmux rejoins exactly the rows it split. So the
   damage is one-directional and so is the repair.
8. Releasing the pinned size as the attach target is handed out does not survive
   the attach. The maintainer reported it after the release was already in
   place: attaching to a task showed the agent in part of the terminal with
   tmux's fill characters over the rest. Read off the running dedicated server
   afterwards, every task window was `window-size manual` at 171x49 — the
   dashboard's main region — while the terminal attaching was larger.

   The gap is between the release and the client. Starting a `tmux` client takes
   tens of milliseconds, the terminal tab polls every 250 ms and every 60 ms
   while the pane has the keyboard, and a frame drawn in that gap asks tmux who
   is attached, is told nobody, and pins the window again. The client then
   arrives to exactly the state the release existed to prevent.

   It stays there because nothing looks again. Bubble Tea's `tea.Exec` blocks
   the event loop for as long as the terminal is handed over (`tea.go`: "NB:
   this blocks"), so the dashboard that would have noticed the client is not
   polling at all — which is why this reads as permanent rather than as a flash.

   Measured against tmux 3.7b, a window pinned at 171x49 with a control-mode
   client attached and sized to 200x60 with `refresh-client -C`:

   ```
   left alone          -u window-size
   171x49              200x60
   ```

   So the option coming off is what hands the window back, and it takes effect
   the moment it does.

Decisions:

- The main region shows the selected task's agent pane. What is drawn is the
  output of `capture-pane -p -e` against the pane ADR-030 already identifies, and
  a shell pane may be shown beside it where one exists.
- Feat does not implement a terminal. It keeps no screen grid, interprets no
  cursor movement, implements no scroll regions, and stores no scrollback. Every
  escape sequence it handles it passes through; the only thing it reads is cell
  width, in order to clip. A change that requires Feat to understand what a
  sequence means is a change this decision refuses.
- This is display and never a source of truth. ADR-030's rule that the tmux
  adapter "never parses terminal output or infers semantic completion" is
  unchanged and is extended rather than weakened: capturing a pane in order to
  draw it is allowed, and deriving any task, agent, attention, or workflow state
  from those bytes is not. Agent state continues to come from provider hooks
  (ADR-032, ADR-036). The distinction is stated because the two operations look
  alike from outside and only one of them is permitted.
- The daemon owns the control-mode connection and proxies. It resolves task to
  pane, validates the target, holds one `tmux -C` client, and publishes frames
  for the focused pane only. The TUI never names a pane and never runs tmux. This
  keeps evidence 5's rule intact, gives the validation one home, and is the shape
  a remote client would need if OQ-002 is ever answered yes.
- Frames are published for what is focused, not for every task. A dashboard
  watching three agents does not need three streams, and the cost of this
  decision is the traffic it avoids.
- Keys travel the same way, and focus is explicit. A key gives the pane the
  keyboard, a key takes it back, and while the pane has it the rail stays on
  screen and the dashboard's own keys do not fire. Text goes through
  `load-buffer` and `paste-buffer -p` for the reason evidence 3 gives.
- Attach stays exactly as ADR-030 defined it. Control-mode rendering is a view of
  a terminal, not a terminal: it has no scrollback and no mouse, and the native
  `attach-session` remains how a user gets the real thing. Both tools in evidence
  3 keep both for the same reason.
- Rendering a frame changes nothing that is already as it should be. The size and
  the zoom are read first and set only where they differ, and the pane is measured
  again only when one of them moved. Evidence 6 is the reason this is a rule
  rather than an optimisation: a preparation step that looks idempotent from
  tmux's side is visible to the program in the pane, and a poll that repeats it is
  a poll that disturbs what it is trying to show. It holds for the control-mode
  transport too, which changes when a frame is asked for and not what asking costs.
- A window holding a pane that has stopped is never made smaller. Evidence 7 is
  the reason: sizing it reflows a screen nothing will repaint, and the reflow is
  then permanent. Growing one stays allowed and is the repair. The question is
  asked about the window rather than about the pane being drawn, because a resize
  reflows every pane in the window — including the stopped agent behind a live
  shell — and it is answered inside the measurement a frame already takes.
- A window a client is on belongs to that client, and Feat's sizing comes off it.
  Evidence 8 is the reason. The release at hand-over stays, because it makes the
  common attach correct with no frame in between, but it is no longer the whole
  of it: a rendering that finds a client on a window Feat pinned releases the pin
  rather than only declining to resize. That covers the client the daemon never
  saw — a user attaching with `tmux` itself, or from a second terminal while this
  dashboard polls — and it is the only step of a frame that acts on a watched
  window at all. It costs nothing per frame: the option's value rides along in
  the measurement a frame already takes, and an unpinned window is left alone.
- A terminal handed to a client is left alone until that client arrives or is
  judged not to be coming. The daemon remembers which task's window it last gave
  an attach target for, and a rendering treats that task as watched for five
  seconds. Evidence 8's gap is the reason, and the record is held against the
  render rather than only consulted by it: a frame that has already asked tmux
  who is attached would otherwise pin the window immediately after the release,
  which is the same defect a few milliseconds later. Nothing about this is
  persistent — a daemon that restarted has no attach in flight — and a client
  that never arrives costs one wrongly-sized frame after the grace expires.
- What the region cannot fit, the renderer clips, and it clips to the foot. A
  window is larger than the region whenever Feat is not the one sizing it: the
  window a native client owns, and now the window of a stopped pane. A terminal's
  newest output is at its bottom and the prompt a user reads is its last row, so
  the rows to drop are the ones above — ending at the last row the panes wrote,
  because a window with blank rows under its content has a foot that is not the
  content's.
- Detail and review become one task view rather than two tabs. Evidence 1 is the
  reason, and a main region holding a live session leaves room for one panel
  rather than four.

Consequence: ADR-041's tab set changes and its layout does not — the rail, the
footer, the overlays, the minimum width, and the compositor all stand, and this
is why that work was committed before this decision rather than after it. The
overview table's fate stops being an open question in ADR-041's terms, because
the main region now has an occupant; whether a cross-task comparison is still
wanted is decided with the three-task runs as before. A new endpoint and a new
stream cross the socket, carrying rendered bytes rather than state, which is the
first traffic of that kind and the reason it is scoped to one pane.

### ADR-043 — Removing the overview table

Status: accepted  
Recorded: slice 13, after use

ADR-041 kept the overview tab's wide task table provisionally, to be removed if
the three-task runs never used it. The maintainer used the dashboard and asked
for it to go before those runs, which answers the question earlier than planned
and in the same direction.

Evidence:

1. Nothing needs it. The rail answers which task to go to next — identity,
   attention, agent state, elapsed time, changed files, grouped by project — and
   the task panel answers everything the table's remaining columns did, for the
   task the rail sent you to. The table was the same facts a second time, laid
   out for a comparison nobody made.
2. It never fitted. Eleven columns are 158 cells against a supported width of 80
   to 160, which is the defect ADR-041 was built to fix; `fitColumns` dropped
   columns from the right until they fitted and told the user which were missing.
   A view that reports what it cannot show is honest, not useful.
3. Two things were living on it that are not overviews, and both were already
   invisible in the three-region layout, because the page it drew them on was
   reachable only in the narrow fallback. The recovery band — reconciliation's
   findings and the action for each — had no other surface anywhere, in the TUI
   or the CLI. The resource sample's notes, which say why a figure is absent, had
   none either.

Decisions:

- The tab set is terminal, task, and runtime. Every tab is about the selected
  task; the overview was the one that was not.
- Reconciliation findings that name a listed task appear on that task's panel,
  above the fields they contradict. A workflow of working beside a worktree that
  is not on disk is the contradiction the pass exists to surface, and the panel
  is where both are read and where the keys that resolve it are.
- Everything the pass found is also in one overlay, on `!`, and the rail carries
  its count and that key. The footer was tried first and is the wrong shape: a
  finding is three lines — what, where, and what to do — and a machine with
  several of them has a list rather than a line, so the footer either truncated
  the action or became the thing nobody reads. The rail's job here is the one it
  already does above the task list with the attention summary: say that something
  needs a person, and let them choose when to look. The overlay is where an
  orphan whose task record is gone, an enumeration that failed outright, and a
  previous daemon that died rather than stopped can each have their three lines.
- The pass carries the time it ran, and looking again is a key on the overlay.
  Nothing repeats a pass on a timer, so what is shown is always what was true at
  startup; a user who has just resumed a task is reading history unless the view
  says when it was taken and offers to retake it.
- The resource sample's notes join the machine figures in the footer, for the
  reason FR-UI-005 gives: a figure nothing measured is shown as absent, and an
  absent figure with no reason beside it is the same silence in another form.
  **Amended: the figures moved to the rail and the notes stayed in the footer,
  see ADR-044.**
- The narrow fallback draws the rail's own entries rather than the wide table.
  It is the only way to choose a task below the layout's minimum, and the table
  fitted no terminal small enough to reach it.

Consequence: the recovery band, the machine card, the eleven-column task row, and
the column-fitting they needed are all deleted. Nothing in FR-UI-002 is lost —
ADR-041 already rewrote it to the triage set the rail carries — and FR-UI-005's
"secondary view" for per-container metrics remains a MAY that nothing implements.
A rail entry now names a task's workflow when it has no session, because the rail
is the only list left and a draft that reported an absent agent state read like a
task whose agent had stopped.

### ADR-044 — The machine's resources at the foot of the rail

Status: accepted  
Recorded: slice 13, after use

ADR-041 put the machine's figures in the footer beside the selected task's
worktree path, to get them out of the vertical stack that pushed the task list
down the screen. The maintainer used that dashboard and asked for them in the
rail instead, below the tasks and above the warning count, and then for the first
build of that: no heading, the processor row named for what a reader looks for,
the percentage on the bar rather than a figure after it, and Feat's orange.

Evidence:

1. The figures were three numbers and no proportion. "48 GiB free of 460 GiB"
   asks the reader to divide before it answers anything, and the question the
   block exists for — is there room to start another task — is a proportion.
   The same free figure is roomy on one machine and nearly nothing on the next.
2. The rail's foot already held the other machine-wide block. Reconciliation's
   count is not about the selected task either, and the two are read the same
   way: a glance at a corner, not a lookup. What is left in the footer — the
   worktree path — is about the selected task, so the footer became one thing
   rather than two unrelated ones.
3. Moving it means reshaping it. The rail is thirty-two cells and the footer's
   form is ninety, so this is not a relocation. A bar is what buys the width
   back, because the total stops being a number and becomes the bar's length,
   and FR-UI-005 asks for available memory and disk availability rather than for
   the totals.

Decisions:

- The machine's resources are at the foot of the rail, below the tasks and above
  the warning count, pinned to the bottom rather than following the task list
  down. This is evidence 4 of ADR-041 again: something read by position is in the
  same position every time.
- A metric is a fixed label, a bar of the share in use, and the percentage after
  it. Fixed columns, so that three bars start and end in the same place and can
  be compared by eye, and the number is right-aligned in a column of its own so
  that a machine crossing from 99% to 100% does not shift it sideways twice a
  second. A number wider than that column takes the cells from the bar rather
  than from the rail: a line is thirty-two cells whatever the machine is doing,
  and a machine wanting twelve times its processors has a bar with nothing left
  to say.
- The number is in the label's grey, not the bar's colour. It says exactly what
  the bar says, and a second colour would make one measurement look like two. It
  was first put on the bar itself, to spend no width on it, and that was wrong
  in use: it split the blocks either side of it into two runs, which read as two
  bars rather than as one with a gap.
- There is no heading over the three. The labels say what they are, and the
  rail's one heading belongs to the tasks.
- The rail's three parts are ruled apart rather than spaced apart. They are about
  three different subjects — the tasks, the machine, and what reconciliation
  found — and blank space between them read as one list that had stopped. The
  rule is the grey of the divider beside it, so the rail is ruled by the frame
  rather than decorated, and the lower rule is drawn only when there is something
  below it.
- The processor row is labelled `cpu` and reads as a percentage. What is measured
  is unchanged — the daemon samples the run-queue average with the core count, as
  ADR-035 decided, and the API still carries both — but the rail divides one by
  the other and shows the result as a share. This is taken knowingly against
  ADR-035's caution about figures that look alike and are not: what is lost is
  the reader's cue that this is demand rather than occupancy. Two things are kept
  against that. Only one measure is derived, identically on both platforms, so
  the cross-platform half of ADR-035 stands. And the share is not clamped in the
  number: a machine wanting more processors than it has reads 245%, which nothing
  that was truly a utilisation percentage could, and the number is marked while
  the bar stops at full.
- A bar is never empty or full by rounding, in the bar or in the number. Two
  percent draws one cell and reads `<1%`; ninety-nine leaves one cell empty and
  reads `>99%`. The bar is read before the number and both roundings would state
  something the sample did not find.
- A share that cannot be computed draws no bar and says it was not measured. A
  capacity of zero is not an empty disk; it is a filesystem nothing measured, and
  a bar at zero would be the most readable false claim on the screen — the rule
  ADR-028 and ADR-031 set, applied to a shape rather than to a number.
- The bars are Feat's orange, which is also the attention colour. A bar is a
  measure rather than a summons and the shape is what tells them apart: an
  attention badge is a glyph beside a task and a bar is a block that fills a
  column. Bold stays with the attention styles, so a bar never shouts and the
  number on an overloaded machine still can.
- The sample's notes and a failed read stay in the footer. They are sentences,
  and thirty-two cells would truncate them into the silence ADR-043 kept them out
  of; the rail says which figure is absent and the footer says why, on screen at
  once.
- There is one rendering of the machine and the narrow fallback uses it too. The
  one-line form is deleted rather than kept for the fallback, which would have
  been two renderings of the same sample to maintain and one of them visible only
  below the layout's minimum.

Consequence: `machineLine` and the three field renderers it composed are deleted,
and the absolute figures they carried — free bytes and total bytes, the load
average itself — are no longer on the dashboard; the share and the percentage
replace them, and `GET /v1/resources` still answers with all of them.
[04-functional-specification.md](04-functional-specification.md) FR-UI-005 moves
with the code, as it did for ADR-035, ADR-041, and ADR-043.

### ADR-045 — The status command loses its key

Status: accepted  
Recorded: slice 13, after use

The task panel bound `s` to the project's configured status command, the third of
the external commands FR-REV-002 asks for. The maintainer pressed it and reported
that nothing happened.

Evidence:

1. It ran. `tea.Exec` leaves the alternate screen, runs the command in the
   selected repository's worktree, and re-enters the alternate screen when it
   exits. The default status command — `git status --short --branch` — prints one
   or two lines and exits at once, so its output was written to the screen the
   TUI had just left and was gone within a frame. Diff and editor survive the
   same path only because a pager and an editor wait for the user; nothing about
   the status command does.
2. Making it visible costs more than it returns. The alternatives are a pause
   after every short-lived external command, which puts a key press between the
   user and a tool that finished, or rendering the output in the panel, which is
   the internal diff surface ADR-006 refused.
3. The panel already says it. Per repository it carries the recorded base, the
   head, the branch, the worktree path, the changed-file count, and whether the
   tree is dirty, merged, or ahead of its base. What `git status` adds over that
   is the names of the uncommitted and untracked files, which `d` shows for
   tracked work and the shell shows for all of it.
4. The key was borrowed. `s` opens the task's shell everywhere else in the
   dashboard, and the panel's own comment recorded that as a compromise made to
   keep the three external commands together.

Decisions:

- `s` opens the task's shell on the task panel, as it does on every other view.
  The panel's external commands are diff and editor.
- The `review.status` configuration stays. It is still expanded, still validated
  by `review.New` against the task's own worktrees, and still printed by
  `feat review` in a terminal that cannot show the screen, where an expanded
  command line is something a user can read and run themselves.
- No external command Feat launches is wrapped in a pause. A command that wants
  to be read is one that waits, and that is the tool's business rather than
  Feat's.

Consequence: one key case and one hint are deleted from the task panel and
FR-REV-002 moves with the code. Nothing in the daemon, the API, the
configuration, or `internal/review` changes: `review.KindStatus` is still one of
the three kinds, and a project that configures a status command still gets it
expanded and reported.

### ADR-046 — Shifted keys move the frame, plain keys move the view

Status: accepted  
Recorded: slice 13, after use

The maintainer reported that dashboard navigation was inconsistent: the arrow
keys sometimes moved the task rail and sometimes moved a cursor inside the main
region, with no way to tell which without pressing them.

Evidence:

1. It was true, and the split was per tab. The dashboard's fall-through key
   handler bound the plain arrows to the rail's cursor, but a view with its own
   keyboard is answered before that handler is reached — so the arrows moved the
   rail on the terminal tab and a repository on the task panel, and did nothing
   on runtime. One key, three meanings, chosen by a tab the user was not thinking
   about.
2. The frame already had shifted keys and they were incomplete. `shift+↑`/`↓`
   selected a task from any view and `tab`/`shift+tab` changed view, so the rule
   existed; the plain arrows duplicating half of it is what made it unlearnable.
3. A terminal has no modifier bit for a shifted letter. `shift+j` arrives as
   `J`, so the Vim-shaped binding for this is the uppercase letter itself. `J`,
   `K`, `H`, and `L` were unbound; the capitals in use were `A`, `C`, `P`, `V`,
   and `R`.
4. Shifted arrows are not reliably delivered. That is why `ctrl+p`/`ctrl+n`
   already existed beside them, and it is why the letters are the primary
   binding rather than a convenience.
5. The same swallowing defect had a second half, reported straight after the
   first: `?` opened nothing from the task panel or the runtime view. Those
   handlers answered their own keys and returned for everything else, so the
   dashboard's keys — `?`, `!`, `n`, `z`, `x`, `v`, and on runtime `a` and `s`
   too — were reachable only from the terminal tab. The footer on both views
   advertised `? keys` throughout, because the frame's hints are drawn whatever
   has the keyboard.

Decisions:

- Shifted keys move the frame. `J`/`K`, `shift+↓`/`shift+↑`, and `ctrl+n`/
  `ctrl+p` select a task; `L`/`H`, `shift+→`/`shift+←`, and `tab`/`shift+tab`
  change view. They are answered before any view sees them, so they work from
  everywhere.
- Plain keys move within whatever the main region draws, and never reach the
  frame. `j`/`k`/`h`/`l` and the plain arrows are equivalent. A view with
  nothing to move through — the terminal tab, whose unfocused pane has no cursor
  — moves nothing, rather than reaching past itself to the rail.
- `h`, `j`, `k`, and `l` are reserved for that movement even where a view has no
  use for them yet. The runtime view's logs action moves from `l` to `o`.
- The narrow fallback keeps the plain arrows on the task list. Below the
  layout's minimum there is no rail: the list is what the single column draws,
  so it is the main region, and the rule is unchanged rather than excepted.
- A focused pane still takes every key except `ctrl+q`. That is not an exception
  to this rule but the layer above it: while the keyboard belongs to the agent,
  the dashboard has no keys at all.
- Movement is answered before a view and actions are answered after it. A view
  that swallowed movement would trap the user inside itself, so the frame takes
  those keys first; a view that is overruled on its own actions would lose `r`,
  which means compare again on the task panel, refresh on runtime, and look
  again on the dashboard. So a view keeps every key it claims, and everything it
  does not claim falls through to the dashboard's meaning.
- A dialog is not a view and does not fall through. Preparation, cleanup, and a
  pending confirmation answer every key themselves, because an overlay is
  something to be answered before work continues.

Consequence: the change is contained to `internal/ui`. No daemon, API,
configuration, or documented CLI surface moves — `feat runtime logs` is
unaffected by the dashboard's key for the same action. The cost is that plain
arrows no longer select a task on the terminal tab, which is the dashboard's
opening view; it is paid deliberately, because that binding is the one that made
the two meanings look interchangeable.

### ADR-047 — One record of a review decision

Status: accepted  
Recorded: slice 13, after use

The maintainer, reading the review surface during dogfood, observed that the
states a user can put a review into have no consequences beyond changing the
workflow state, and asked whether they were worth having. They are — but the
decision was being recorded twice, and that is what the objection had found.

Evidence:

1. Nothing read the second copy. `domain.Review.Status` reached two call sites,
   `reviewDecision` in the TUI and `printReview` in the CLI, and both only
   rendered it. Every behaviour in the daemon reads `Task.Workflow`: the
   transition table, the effects table that returns a prompted task to `working`,
   the notification conditions, and the one place approval changes anything —
   `approvalOffer`, which offers to stop services a user approved a task with
   still running.
2. The copies could disagree, and one action made them. Leaving a review pending
   was the only decision that moved the review's status without the workflow, so
   approving and then leaving pending produced a task whose panel read `workflow
   approved` above `decision pending`. There was no way out: `approved` has no
   outgoing transition, so requesting changes afterwards failed with a message
   about the agent not having asked for review.
3. The action that produced it was never used or tested. It had no test anywhere
   in the tree, and its key was bound on the task panel but absent from the
   panel's hints — advertised only inside the string it would go on to
   contradict.
4. The decision line was offered where the decision was not available. Read from
   the review aggregate, which knows nothing about the task, it showed "A to
   approve" for a task that was still `working`, and the daemon then refused the
   approval that line invited.

Decisions:

- The task's workflow state is the only record of a review decision. The review
  aggregate keeps what is genuinely its own: the per-repository comparisons, the
  agent's claim, and the check results with the reporter of each.
- Leaving a review pending is not an action. A review nobody has decided is
  already pending, so the action existed only to un-decide, which is what moved
  one copy without the other. FR-REV-004's three options are satisfied without
  it: pending is the state a review rests in, approving is a transition, and
  revision is attaching to the agent.
- `EventReviewChanged` carries no from and no to. It records that what is known
  about the work changed — an agent's report, or a gate's results — and the
  user's decision is recorded as the workflow transition it is.
- The decision the TUI renders is derived from the workflow, so the keys appear
  only where the transition exists.
- The stored schema version does not move. Removing a field normally requires a
  version and a migration, which is the rule `internal/store/fs` documents and
  keeps; this removal is exempt because nothing has to be upgraded. The
  information in `status` and `decided_at` is in the task snapshot beside them,
  so an older document loads with the keys ignored and the next save writes them
  away. A test pins that, because it is the only risk the exemption takes. The
  exemption is an early-alpha one and does not generalise: after v0.1 there is
  state in the wild whose owner did not write it.

Consequence: `ReviewStatus`, its constants, `Review.Status`, `Review.DecidedAt`,
`Review.Decide`, and `api.ReviewLeavePending` are deleted, and `decide` in the
daemon collapses to a workflow transition. FR-REV-004 stands as written.
`changes_requested` is deliberately left alone and is the next question: it tells
the agent nothing — no inbox message is written, unlike a gate's verdict — and its
only reader treats it exactly as it treats `review_requested`,
`ready_for_review`, and `verification_failed`. Either it earns its place by
carrying the user's revision note into the session, or it folds into attaching.

### ADR-048 — Removing the external resource declaration

Status: accepted  
Recorded: slice 13, after use

`runtime.external_resources` is the one part of the runtime model drawn from the
reference project's shape rather than from the product's. The maintainer, asking
what it is for, gave the answer that settles it: which database an application
connects to is the user's deliberate choice, expressed in a connection string
inside an environment file, and Feat has no way of knowing what is at the other
end of it.

Evidence:

1. Feat cannot see the resource and is forbidden from trying. The connection
   string lives in an environment file, which `checkEnvFiles` stats and never
   opens because [05-security-model.md](05-security-model.md) requires it, and
   which the runtime adapter passes to Compose by path for the same reason. The
   declaration therefore names something Feat has committed never to look at, and
   no code path can ever reconcile the two.
2. The check that reports on it cannot fail. `feat doctor` stats every configured
   Compose file, asks `docker compose config --services` whether each managed
   service is really defined, and stats each environment file and reports its
   permissions. For an external resource it emits an unconditional pass carrying
   the words "referenced, never created or destroyed by Feat" — the only runtime
   check in `internal/project/checks.go` that makes a claim about a user's
   resource without contacting it.
3. Its whole runtime effect duplicates a variable that is already set.
   `FEAT_TASK_KEY` is generated for every managed service unconditionally, and an
   external resource's `selector_variable` is given the task key — the identical
   value. The entire behaviour of the feature is that the variable may be called
   something else. A static override cannot do the same, because it is one file
   for every task and cannot hold a per-task value, so the aliasing is real; it is
   also all there is.
4. The safety it appears to provide comes from somewhere else. Destroy runs
   `down` on the task's own Compose project and volume removal enumerates by
   Compose label, so a resource outside that project is excluded by construction
   — the rule `removeVolumes` records, and the reason no cleanup path reads the
   recorded list. `validateExternal` refuses an external resource whose
   identifier is also a managed service, which guards a project that runs the
   resource in Compose while declaring that Feat must not touch it; for a
   resource that is not in Compose at all, the branch cannot fire.
5. The record has two readers and both are display. `ExternalResources` is
   validated, persisted, published, and rendered by `feat runtime status` and the
   runtime panel. `domain.ExternalResource` says its lifecycle is recorded so
   that a cleanup plan can prove it excluded the resource; no cleanup plan reads
   it.
6. The lifecycle it records has one inhabitant. `LifecycleManaged` appears in
   non-test code only in its own declaration and in `Valid()`. Nothing is ever
   recorded as managed, so the enum draws a distinction that only ever has one
   side.

Decisions:

- `runtime.external_resources` is removed from the configuration model, the JSON
  schema, the domain, the store, the API, and both display surfaces.
- `FEAT_TASK_KEY` is the mechanism, and is documented as such in
  [07-configuration-model.md](07-configuration-model.md) rather than left
  implicit. Feat sets it on every managed service; a project that shares one
  resource between tasks uses it to name its share; Feat neither knows nor asks
  what is behind the name. Removing a documented feature and replacing it with an
  undocumented convention would be worse than either.
- FR-RUN-008 is amended rather than dropped. Allowing an external resource means
  not interfering with one: Feat supplies a per-task discriminator, reads no
  environment file, and models nothing about the resource. Provisioning,
  migration, seeding, and reclamation stay out of v0.
- Nothing reports on a resource Feat cannot reach. Naming an unreclaimed share in
  a cleanup plan was considered and refused. It would assert the existence of
  something Feat has never contacted, on the user's word alone, at the moment a
  user is deciding whether it is safe to proceed — the same unverifiable claim as
  evidence 2, moved somewhere it would carry more weight. A share on a server Feat
  cannot see is the user's to reclaim, and saying so in a plan does not make it
  Feat's.

Consequence: about 240 lines across twelve non-test files and seven test files
are deleted, together with the `externalResource` schema definition, the example
in [07-configuration-model.md](07-configuration-model.md), and the block in
`docs/examples/project.yaml`. No stored snapshot needs migrating: the field is
written `omitempty` and task documents are decoded without
`DisallowUnknownFields`, so the key in an existing snapshot is ignored. A project
that shared a staging database by `selector_variable` reads `FEAT_TASK_KEY`
instead, in its own configuration or in a container entrypoint that re-exports
it. The generated Compose override loses nothing: the variable it carried is
still there under the name Feat generates.

### ADR-049 — Who owns the interrupt while another program has the terminal

Status: accepted  
Recorded: slice 13, after use

The maintainer opened the Compose logs from the runtime tab and found no way out
of them that was not also a way out of Feat.

Evidence, measured against Bubble Tea 1.3.10 on a real pseudo-terminal rather
than reasoned about, because two signal handlers disagreeing is exactly the kind
of thing reasoning gets right in the wrong order:

1. `docker compose logs --follow` ends when the user interrupts it. There is no
   other exit: it follows until something stops it, which is what FR-RUN-006
   asks for.
2. The terminal driver sends the interrupt to every process in the foreground
   process group, not to the program the user is looking at. The dashboard is in
   that group, so it receives the key that was meant for the logs.
3. Bubble Tea already has a policy for this. `ReleaseTerminal`, which it calls
   before running a command, sets its `ignoreSignals` flag, and its own handler
   drops signals while that flag is set — the program that holds the terminal is
   the program the interrupt is for. `RestoreTerminal` clears it again.
4. `tea.WithContext` is not covered by that flag. A cancelled external context
   ends the event loop wherever it is, and `feat` handed the program the
   process-wide interrupt context that `main` derives with
   `signal.NotifyContext`. So Feat had a second signal handler that did not know
   when the dashboard was not in charge: on a pseudo-terminal, the same program
   with that context died on the interrupt that left its child, and without it
   the child died, the program repainted, and the user carried on. That is the
   defect exactly.
5. Compose exits 130 when it is interrupted, and the dashboard turned any
   non-zero exit from a command it ran into an error banner. Leaving the logs
   therefore also reported a failure — evidence 9's state that cries wolf, in
   the other adapter.
6. Ctrl-C typed at the dashboard itself is not a signal at all. Bubble Tea holds
   the terminal in raw mode, where the key arrives as input and the model
   already quits on it. The interrupt context was doing nothing for the ordinary
   path it appeared to serve.

Decisions:

- The dashboard's lifetime is its own. `ui.Run` detaches from the process-wide
  interrupt context and passes no external context to the program; Bubble Tea's
  own handling ends it, which is the one place that knows whether the dashboard
  or a program it lent the terminal to currently owns it. `tea.ErrInterrupted`
  joins `tea.ErrProgramKilled` as an ordinary shutdown rather than a failure.
- An exit produced by an interrupt — 130, or the signal itself for a program
  that installs no handler — is how a user leaves a program the dashboard ran,
  and is not reported. Any other non-zero exit still is: a diff tool that could
  not open is something the user needs to know about. It is ADR-034 evidence 9's
  rule, applied at the client to the same distinction.
- The health screen keeps its context. It renders and exits, lends the terminal
  to nothing, and has no child whose interrupt could be mistaken for its own.
- What this costs is stated rather than hidden: while a child holds the
  terminal, a `SIGTERM` aimed at Feat waits for that child. The previous
  behaviour was not better — it tore the dashboard down while another program
  still had the terminal — and a user who needs the child gone can interrupt it,
  which is the same key this decision exists to make work.

Consequence: three small changes — the program's context, the exit status a
command's failure is read from, and a test for each that fails against the
behaviour it replaced. No stored format, endpoint, or key binding moves. The
gap this leaves is discoverability: nothing on screen says that the interrupt is
the way back, which belongs with the deferred dashboard polish rather than here.

### ADR-050 — The writable Git directory is host execution, and is disclosed rather than closed

Status: accepted  
Recorded: alpha review, after slice 13

The alpha review asked what the devcontainer boundary is worth and found one
mount that goes straight through it. ADR-033 gives a task's container the main
checkout's Git directory at its host path, with the access the worktree has,
because a linked worktree is not a repository without it. For a read-write task
that mount is writable, and `.git/hooks` and `.git/config` are common-directory
files shared with the user's own checkout. Writing either is host code execution
as the user, outside the container.

[05-security-model.md](05-security-model.md) already accepts Git metadata
mutation, but it accepts it for full Git inside a *native host worktree*, where
there is no boundary to cross. Nothing in the documents said that the same mount
in devcontainer mode converts container write access into host execution, and
[06-technical-architecture.md](06-technical-architecture.md) cited that
acceptance as though it covered this case.

Evidence:

1. The mount is the user's own Git directory, not a copy. `gitMetadataMount`
   returns `filepath.Join(repository.HostPath, ".git")` as both source and
   target, with `ReadOnly` set only when the task holds the repository
   read-only. `hooks/` and `config` are common-directory files: one checkout and
   every linked worktree share them.
2. Feat's own checks pass it by design. `CheckMounts` tests each mount for a
   container-runtime socket, then for a forbidden source, then for writability
   against a read-only declaration. The Git directory is exempt from the
   forbidden-source rule deliberately — without the exemption the mount ADR-033
   requires would refuse every launch — and it is writable exactly as declared,
   so the writability rule is satisfied rather than violated.
3. No commit is needed, and in the cheapest case no Git command is. A hook is
   the slow path: `post-checkout` waits for a checkout, `post-merge` for a pull,
   `pre-commit` for a commit. `.git/config` is the fast one. `core.fsmonitor`
   names a program that Git runs on every index refresh, so `git status` alone
   fires it; `core.pager` fires on `git log`; `diff.external` on `git diff`;
   `core.sshCommand` on any fetch. An `[alias]` entry is the weakest of these
   and should not be the example anyone reaches for: Git will not let an alias
   shadow a built-in, so it waits for the user to type a name they had no reason
   to type.
4. Feat detonates it without the user touching Git. The host Git adapter runs
   `fetch` when it plans a task (`git.go:72`), `worktree add` when it creates one
   (`git.go:233`), and `worktree prune`, `worktree remove`, and `branch -d` when
   it cleans one up. `fetch` updates refs, which runs the `reference-transaction`
   hook; `worktree add` runs `post-checkout` as well. So an agent that writes a
   hook during one task gets host execution from the next `feat task create` in
   that project, through Feat's own ordinary loop.
5. Git offers no protection to rely on. `safe.directory` is an ownership check
   and the user owns the repository. Git has always treated write access to
   `.git/config` as equivalent to code execution — it is why `clone` does not
   copy the remote's configuration — so the exposure is the documented
   consequence of the mount, not a defect in Git.
6. The exposure and the feature are one mount. FR-GIT-006 requires full Git in
   the agent environment and FR-GIT-007 requires that the agent be able to
   commit. Mounting the metadata read-only satisfies neither: `git commit` writes
   objects, refs, and the index through that directory.

Decisions:

- The mount stays writable for a read-write task, and the exposure is written
  down in the terms above rather than left to be inferred. § Git boundary states
  the devcontainer case explicitly; the architecture document stops citing an
  acceptance that did not cover it.
- The claim Feat makes about the devcontainer is narrowed to what is true: it is
  a boundary everywhere except the one place Feat itself opens. The existing
  hedge — that the checks are not a defence against a kernel or
  container-runtime exploit — is not the relevant one here. This path needs no
  exploit and no misconfiguration; it is the supported configuration working as
  designed.
- Mounting `.git` read-only is rejected, with its cost stated so the option
  stays visible: it would take FR-GIT-006 and FR-GIT-007 with it and leave an
  agent that can read history and write nothing, which is not the product.
- Restricting which metadata paths are writable is rejected as a boundary that
  cannot be drawn where it would need to be. `hooks/` and `config` could be
  masked, but `commit` needs `objects/`, `refs/`, and `index`, and a writable
  `objects/` plus a writable `refs/` is enough to move any branch the user has;
  `config.worktree`, `info/attributes`, and the hooks path itself are further
  doors of the same kind. A partial mask would read as a fix while leaving the
  class open, which is worse than the honest mount.
- Host-mediated Git is neither adopted nor rejected here, and the premise it is
  usually argued from is corrected: Feat has no host mediation to extend.
  `docs/06` § Agent capabilities supports *direct* `gh`/`glab` use in the
  container, authenticated by a mounted or injected credential. Proxying Git
  through the host would be new machinery, and it inherits the problem rather
  than solving it — a host-side Git that accepts commits from the container must
  still run hooks and read configuration somewhere, and deciding where is the
  whole design.
- A separate container-side Git metadata directory remains the one option that
  keeps both the feature and the boundary, and it stays open as OQ-006 rather
  than being decided here. It is the maintainer's call whether that work is
  worth its complexity, and it is deferred until the dogfood test is finished:
  an exposure this branch could only reason about is one that running the
  product against real repositories will price properly, and committing to a
  metadata backend before then would be choosing a permanent design early.
  This ADR records what is true today so that the choice is made against a
  stated exposure rather than an implied one.

Consequence: no code changes. Three documents move — this record, § Git boundary
in [05-security-model.md](05-security-model.md), and the Devcontainer execution
paragraph in [06-technical-architecture.md](06-technical-architecture.md) — and
the product's behaviour is unchanged, which is the point: a user who accepted the
devcontainer on the strength of the old wording accepted something narrower than
they were told. What this leaves open is a real hole with a name, reachable by a
prompt-injected agent as easily as a deliberate one, and closing it is OQ-006's
to schedule. The user-facing text is not edited here; the README block that
enumerates what Feat refuses hedges only against kernel exploits and the network,
and belongs to `fix/security-claims`.

### ADR-051 — What the dashboard looks like

Status: accepted  
Recorded: alpha review, after slice 13

ADR-041 decided what the dashboard is *shaped* like — a rail, a main region, and
a footer, all on screen at once — and left what it looks like to whatever each
screen was written with. The maintainer read the result in use and reported four
things, and each of them turned out to be a rule the dashboard did not have
rather than a preference about taste.

Evidence:

1. The two colours a user reads most are not from the same family. The selection
   colour was a pale sky blue (`#7cc4ff` on dark) and the attention colour a
   saturated orange (`#ffb454`), which differ in saturation as much as in hue:
   one is washed out and the other shouts. A rail holding both reads as two
   programs sharing a window, and the difference the eye actually registers is
   loudness rather than meaning.
2. Every project header offered a control that did not exist. The rail drew
   `▾ project` above each group from the day it was written, which is the
   universal marker for a disclosure triangle, and nothing was bound to it. A
   control a user tries and finds inert is worse than no control: what it teaches
   is that the rest of the screen may also be decoration.
3. Neither region's header was separated from its content. The rail's heading and
   the tab bar were the first line of their own block, so an eye scanning down met
   `tasks` and the first task, or the tab bar and the first line of a rendered
   pane, as two entries of one list. The tab bar had the same defect twice over:
   the selected tab differed from the others in shade alone, which is a
   comparison rather than a thing seen.
4. The footer was not separated either. It is the one part of the frame that
   holds still while the regions change, and nothing said so.
5. The regions were divided by a column of `│`. Two blocks sharing one drawn edge
   read as one block with a line down it, which is the opposite of what the layout
   is for: the rail and the main region are about different things and answer
   different questions.
6. Sizing was left to lipgloss, which re-flows what it is given. A line wider than
   the region was wrapped rather than cut, so the task panel's long sentences put
   their tails against the region's left edge, under an indented label they did
   not belong to — and the panel's own scroll arithmetic, which counts lines
   before they are drawn, could not see it.

Decisions:

- The palette is six named colours in one file, chosen as a set: an accent for
  what the user has chosen, an amber for what may want them, a red for what has
  failed, two neutrals for text and its labels, and one quiet colour for every
  line the layout is drawn with. The accent and the amber are at the same weight,
  so the eye reads the difference as meaning. Every colour stays adaptive, because
  a terminal's background belongs to the user.
- Each region is a card: a rounded box with a header row, a rule between that
  header and its content, and content that is cut to the box rather than re-flowed
  inside it. The two are set apart by a blank column rather than joined by a drawn
  one. The footer is ruled off from both.
- The rail's heading and the tab bar become those headers, and each carries a
  summary on its right: how many tasks are waiting, and which task every tab is a
  view of. The selected tab takes the accent as a background, which is what the
  focused task entry already did.
- The region holding the keyboard says so by taking the accent for its border.
  An overlay's border is always the accent, because an overlay always has the
  keyboard.
- A project folds, on the space bar, which is what its marker has been promising.
  A folded project keeps saying how many tasks it holds and whether any of them
  wants the user: a fold that could hide the one task that stopped would make the
  rail unsafe to fold at all. The cursor never stays inside a fold, because the
  main region draws whatever the cursor is on and the keys act on it.
- The box is drawn by Feat rather than by lipgloss, a line at a time, cutting by
  cell through the escape-aware primitives the overlay already uses and ending
  each line's styling before the border it is about to write. The content of the
  main region is a rendered tmux pane: a capture carries the colour tmux emitted
  and not the clearing tmux does as it draws, so a line re-flowed mid-escape-
  sequence sets a colour and keeps it, across the border and the region beside it.
- The one body that is re-flowed is the task panel, deliberately and before it is
  measured. It is the only prose the dashboard draws, its sentences say what to do
  about what they report, and wrapping before the split is what keeps the scroll
  honest: the lines counted are the lines drawn.
- A task list too long for the rail is cut above the rail's foot and says so,
  naming the key that makes room. The region used to be clipped by the layout,
  which cut from the bottom and so took the machine's figures — the one part of
  the rail that is read by position — instead of the tasks.

Consequence: the frame is four cells narrower and two lines shorter for its
content than it was, which the main region pays: 79 cells at a 120-column
terminal and 55 at the narrowest supported width, where it was 87 and 63. That is
the price of the borders and the gutters, and it buys the separation the four
reports above were all about. No stored format, endpoint, or state moves; the
task-preparation dialog is also sized to the dialog it is drawn in rather than to
the terminal, which is where its ellipsis-per-line came from. `space` is the one
new binding.

Amended by ADR-052: the rule that the cursor never stays inside a fold is what
made folding a one-way door, and a fold is now a cursor position of its own.

### ADR-052 — A folded project is a cursor position

Status: accepted  
Recorded: alpha review, after ADR-051 shipped

ADR-051 gave the rail's fold marker the control it had always been promising, and
paired it with a rule: the cursor never stays inside a fold, because the main
region draws whatever the cursor is on and the keys act on it. Both halves of the
rail obeyed it — folding a project moved the cursor to the next task still
listed, and `J`/`K` stepped over folded projects rather than through them.

Evidence: the maintainer folded a project in use and could not open it again.
`space` acts on the project the cursor is in, and the two halves of the rule
together mean no cursor position exists inside a folded project: nothing moves
onto one, so nothing can press `space` on one. The only case that worked was
folding every project, where the cursor had nowhere to go and stayed by accident
— which is also the case the header was already written to draw, naming the task
the fold was holding. Folding was a one-way door for as long as one project
remained open, and the control the ADR added to stop the rail claiming a control
it did not have now claimed a reversal it did not have.

Decisions:

- A folded project is one cursor stop: `J`/`K` move onto the fold, and past it in
  one step whatever it holds. One stop for the project rather than one per hidden
  task is what keeps folding worth pressing — the point is to move past a project
  quickly — and is what makes the fold reachable at all.
- `space` folds and opens the project the cursor is in, and does not move the
  cursor either way. The task under the cursor stays selected across a fold, so
  folding no longer takes a user's selection away as the price of reading less
  about other projects.
- What the rail must always say is which task is selected, not that the selected
  task has an entry. A fold holding the cursor names the task's key on its header,
  beside the count and the attention glyph it already carried, which is the
  rendering ADR-051 wrote for the all-folded case and is now the general one.
- The footer names the key for what it would do where the cursor is — `space
  fold` or `space open` — because one control in two directions is otherwise
  legible only from the marker beside the cursor.

Consequence: the main region can draw a task whose rail entry is hidden, which is
what ADR-051's rule was protecting against. It is bounded by the header naming
that task, by the main region's own header naming it in words, and by the user
having pressed a key to get there. No stored format, endpoint, or state moves,
and no new binding: `space` is the same key with a reachable inverse.

### ADR-053 — The palette is ordered by chroma, and measured

Status: accepted  
Recorded: alpha review, after ADR-052

ADR-051 chose the six colours as a set and said what the set was for: the
selection colour and the attention colour at one weight, so that the eye reads
the difference between them as meaning rather than as loudness. It did not say
what weight meant, and the values it shipped were taken from an existing terminal
theme. The maintainer read the result in use and reported that the orange was
right and the blue was not.

Evidence, all of it measured in OKLCH and in simulated colour-vision deficiency
(Machado 2009 at full severity) rather than argued from taste:

1. The pair was not at one weight after all. On dark the accent carried chroma
   0.132 against the attention colour's 0.106, so the blue led; on light it was
   0.178 against 0.108, which is half again as much. The rule the ADR set was
   satisfied on neither theme.
2. The accent shared a hue with every neutral in the palette — 264° against the
   text's 268.5°, the muted colour's 269°, the rule's 272.7°. The effect is
   smaller than it sounds, because those neutrals are nearly grey, but it is
   exactly where the focused card's border changes: rule to accent was a change of
   lightness and chroma with no change of hue.
3. The palette's closest pair was not the one the ADR was about. Attention against
   failure measured 0.086 in OKLab distance under deuteranopia — orange and red
   are neighbours, and at the old weights lightness was nearly all that separated
   them. The pair the ADR did protect measured 0.254.
4. A cooler accent was proposed, argued for on hue separation, and withdrawn on
   measurement: teal at 200° loses two fifths of the accent/attention separation
   under the two common deficiencies, and at the light theme's lightness sRGB
   holds no more than 0.086 chroma there — less than the orange's 0.108 — so the
   equal-weight rule would have been unsatisfiable on one theme by construction.
   Violet is worse: 0.094 against the orange under tritanopia.

Decisions:

- The set is ordered by chroma: failure above attention, attention level with the
  accent. Hue and lightness distinguish colours within a rank; chroma is the rank.
- The accent stays blue and is re-weighted to the orange rather than re-hued away
  from it. Nearly opposite in hue and lower in lightness is the pairing that keeps
  working when a channel is missing.
- The dark values are `#53a0ff`, `#f5a623`, and `#ff6287`. The red moved because
  the orange did: at the orange's new chroma the old red no longer outranked it.
- The light values are `#1f4e88`, `#8a5a00`, and `#a8202a`. The light accent
  copies the dark relationship — same chroma as the orange, about a tenth darker —
  rather than matching it on every axis, which measured worse: a pair matched on
  lightness and chroma has only hue left to lose.
- The light theme's orange is not the dark one adjusted. `#f5a623` reads at 2.03:1
  on white, and `#8a5a00` is already the most chroma the hue holds at a lightness
  that is legible there. The product's colour is a dark-theme value, and the light
  theme has an ochre standing in for it.

Consequence: every pair in the palette separates further than it did, including
the one nobody had looked at — attention against failure goes from 0.086 to 0.135
under deuteranopia, and accent against attention from 0.254 to 0.334. Nothing
outside `internal/ui/palette.go` changes; no format, endpoint, or state moves. The
figures above are reproducible from the hexes in that file, and a future change to
them should be measured the same way rather than compared by eye.

### ADR-054 — Text Feat did not write is made measurable before it is measured

Status: accepted  
Recorded: alpha review, after ADR-053

ADR-051 made the dashboard draw its own frame a line at a time, cutting by cell
through escape-aware primitives, precisely so that content could not run into the
border. Every measurement it added asks one question of a string — how wide is it
— and answers it with the display width of the characters in it.

Evidence: the maintainer opened the task tab on a task whose gate had captured
`go test` output, and the frame came apart. The panel's lines crossed the border,
the rail was overwritten, the footer was drawn halfway up the screen, and the
machine block appeared three times with different figures. The cause is one byte.
`go test` separates its columns with tabs; a tab has a display width of zero and
is drawn by the terminal as a jump to the next multiple of eight. A captured line
measured forty-eight cells and was drawn as sixty-two. Everything below it was
then painted over rows the renderer believed were somewhere else. The same defect
was in two further places, neither reported: a task title carrying a line break
would have added a row to a rail that counts its own rows, and a wrapped error
carrying a command's output was written into a footer whose height the regions
above it are sized against.

Decisions:

- Text from outside — a brief, a captured check detail, an error another program
  produced, a title a user pasted — is converted before it is measured. Tabs are
  expanded to the terminal's own stops, so the columns they were holding apart
  survive as spacing; the other C0 controls are dropped, because they move the
  cursor and nothing that moves the cursor may reach a screen laid out by counting
  cells.
- Escape sequences are not touched. The styling is Feat's own, and the conversion
  is by byte in a range no escape sequence uses.
- Values drawn on one line have their line breaks removed as well as their
  controls. A count of rows is the rail's and the footer's arithmetic, and a
  string that can add one is a string that can break it.
- The rendered tmux pane is not converted, and must not be. It is drawn by the
  splice ADR-042 describes, which is by cell and passes every escape through; its
  content is a grid tmux has already laid out, so the controls that break a layout
  are not in it.
- Storage keeps what the command actually emitted. The width constraint belongs to
  the screen, not to the record.

Consequence: `internal/ui` gains one conversion and three call sites — the task
panel before it is wrapped, the rail's title, and both footers. No stored format,
endpoint, or state moves. The general rule this leaves behind is worth more than
the fix: anything the dashboard did not compose is untrusted input to a layout,
and a region that measures what it draws has to be given something measurable.

### ADR-055 — A check that could not run belongs to the user, not to the agent

Status: accepted  
Recorded: slice 13, from the first real feature run on the reference project

ADR-036 drew the distinction and the code kept it: a check that could not be
started, or that exceeded its bound, is recorded as `unknown` with the reason,
and never as a failure it did not have. One line then threw it away.
`review.Decide` computed `Passed = Failed == 0 && Inconclusive == 0`, and
`finishGate` transitioned on that single boolean, so a check nobody managed to
run and a check that ran and failed produced the same state, the same verdict to
the waiting agent, and the same notification.

The run that found it: a check was configured as `pytest`, the reference project
runs its tests through a wrapper, and the bare program was not on the path inside
the agent's environment. Everything ADR-036 designed worked — the helper blocked,
the failure returned into the agent's loop, and the agent diagnosed it, named the
configuration file, and declined to edit the configuration governing its own
gate, which is the right refusal, because an agent that chooses its own check
command certifies itself. What followed had no exit. The task rested in
`verification_failed`, which says the work failed its checks; the agent could not
fix it; and the person who could was told through a workflow state rather than
asked.

Evidence:

1. The information exists at every layer and is discarded at the one point that
   decides what to say. `Gate.run` records the reason a check could not start,
   `Verdict` counts `Inconclusive` separately from `Failed`, the review record
   stores both, and the review screen has always had a "did not report" column.
   Only the verdict's boolean, and the three expressions of it in `finishGate`,
   could not tell them apart.
2. The two failures belong to different people, and only one of them is about
   the code. A failing test is evidence the agent can act on. A missing program,
   an unreadable directory, or an environment that could not be rebuilt is a
   statement about the project's configuration — which is the user's, and which
   the agent must not edit.
3. `internal/notify` had no condition for it, so there was no way to say it even
   where the daemon knew. Its conditions are pinned tables keyed by state, and
   the state a blocked run should produce is one the tables already map to
   something else.
4. The product already has a state for "the agent asked and Feat has no verdict".
   A project that configures no checks leaves the task in `review_requested` with
   the agent's own report beside it, which is what
   [02-user-workflows.md](02-user-workflows.md) §6 describes: the request stands
   and a person decides. A gate that could not run is the same situation reached
   by a different route.

Decisions:

- `review.Decide` returns a three-valued `Outcome` — `passed`, `failed`,
  `blocked` — and the boolean is removed rather than kept beside it. One record
  of one thing, which is the rule ADR-047 applied to the review decision.
- Failure outranks a check that never ran. A run holding both is `failed`,
  because a check that reported is evidence about the work and the agent can act
  on it, and the checks that did not run are still named in the report and on the
  review screen. The blocked route is for a run that established nothing.
- A blocked run leaves the task in `review_requested`. No workflow state is
  added: the task is exactly where evidence 4 puts it, the recovery edge is the
  one `verify` already uses, and a state added for this would have to be
  explained everywhere `verification_failed` is, to say something the review
  screen and the task's history say better.
- The user is interrupted by a new condition, `verification_blocked` — "its
  checks could not run". It is named by the daemon that ran the gate rather than
  looked up from the task's new state, because `review_requested`'s own condition
  is the one ADR-036 suppresses while a gate is about to run: looking it up would
  announce a blocked gate as a fresh review request, or as nothing at all.
- The notification says no more than that. Naming the check would mean putting a
  configured value into a message that leaves the machine, and `notify.Subject`
  exists to have nowhere to put one. The naming happens where the user then
  looks: the task's history names the check and its repository, and the review
  screen carries the reason each one gave. The check's own output stays out of
  the event stream, as ADR-036 evidence 6 requires.
- The waiting agent is answered with a new verdict status, `blocked`, and the
  generated helper exits **zero** on it. The failing exit status is ADR-036's
  route back into the native loop, and a configuration the agent cannot fix is
  the one thing that must not travel it. The report says why, so the session is
  not left to infer that the failure was not its own, and tells it not to change
  the configuration that decides how its work is verified. A helper generated
  before this change reads the status through its unknown-status branch, prints
  that it did not understand it, and exits zero — so a session already running
  when the daemon is upgraded degrades to the same non-failure.
- The review screen renders a check that did not report as "did not report"
  rather than as `unknown`, in the words the summary line above it already uses,
  and in amber rather than red: it needs the user, and it is not a verdict
  against the work.

Consequence: one workflow state fewer than the alternative, one notification
condition more, one protocol status more, and no stored format change — a
`Check` has carried `unknown` since slice 1. `verification_failed` narrows to
what it says, and a project whose check command is wrong now interrupts the
person who can fix it, on a task whose state does not claim its work was
measured.

### ADR-056 — One stash stack is shared by every worktree, and Feat says so rather than working around it

Status: accepted  
Recorded: slice 13, from an assessment of what concurrent tasks on one repository share

A linked worktree has its own working tree, index, and HEAD. Everything else
belongs to the repository it was created from, and Git's per-worktree ref
namespace is exactly `HEAD`, `refs/bisect/*`, `refs/worktree/*`, and
`refs/rewritten/*`. `refs/stash` is not in it. Every task worktree Feat creates
for a repository, and the user's own checkout, therefore push onto and pop from
one stack, and `git stash pop` takes the newest entry in the repository
regardless of who made it.

Evidence:

1. It is deterministic, not a race. Task A stashes, task B stashes, task A pops:
   A's working tree now holds B's changes and B's entry is gone. Both commands
   exit zero and print what they normally print. Under concurrency it is worse
   and just as quiet — three worktrees running forty stash/pop cycles each
   restored another worktree's content 91 times out of 120, reported zero
   errors, and left six orphaned entries behind.
2. The user's own checkout is on the same stack. An agent can pop the
   maintainer's uncommitted work into a task worktree, and one `git stash clear`
   removes every entry the user had. That is the preservation rule in
   [05-security-model.md](05-security-model.md) failing in the one place nothing
   was watching.
3. Containers do not bound it. A task worktree is not a repository on its own,
   so `internal/daemon/execution.go` mounts the main checkout's whole Git
   directory, read-write, at its host path in every task container. The shared
   stack is reachable from inside an environment that is otherwise isolated.
4. Git protects the neighbouring cases and not this one. Checking out or
   deleting a branch another worktree holds is refused with a message naming the
   worktree; the stash has no such guard, and `git worktree remove` leaves a
   task's entries behind in the user's repository.
5. Redirecting the ref does not work. `git symbolic-ref refs/stash
   refs/worktree/stash` looks like the fix and is not one: the ref dereferences
   per worktree while the reflog stays shared, which produces `warning: log for
   ref refs/stash unexpectedly ended` and `error: 'refs/stash@{0}' is not a
   stash reference` — an unpoppable stack, which is worse than a shared one.
   Git 2.52 has no configuration for the stash ref; `stash.index`,
   `stash.showPatch`, `stash.showStat`, and `stash.showIncludeUntracked` are all
   of it.
6. Nobody upstream has fixed it and nobody documents it. `git-worktree(1)`
   recommends a worktree as the alternative to stashing without mentioning what
   the two share. The same defect is open against another agent CLI
   (github/copilot-cli#1725, February 2026), and the published analyses of
   worktrees as an agent boundary reach this list independently: hooks, config,
   the stash, refs, and the object store.
7. Two of the settings that reach the stack are not commands the agent chose.
   `rebase.autoStash` and `merge.autoStash` live in the repository's shared
   configuration, which is the user's, and on a conflicting re-apply the
   autostash entry is left on `refs/stash`. An instruction the session follows
   perfectly does not cover them.

Decisions:

- The session is told. The generated Claude instructions state that the
  repositories are worktrees sharing one Git directory, that the stash is one
  stack per repository, that `pop` takes an entry that may not be its own, and
  that work in progress belongs on the task branch. It is the one thing in that
  document that is not about the protocol, because it is not advice about the
  work: it is a fact about an environment Feat built and the session cannot see.
- The settings that decide on the session's behalf are turned off.
  `git.WorktreeEnvironment` renders `rebase.autoStash=false` and
  `merge.autoStash=false` as `GIT_CONFIG_COUNT` entries, and the daemon adds
  them to every agent launch in both execution modes. A rebase with a dirty tree
  then stops with `error: cannot rebase: You have unstaged changes`, which names
  what to do next, instead of stashing where anyone can pop it.
- They travel as environment, never as a configuration write. The file that
  would have to hold them is the user's `.git/config`, shared by the worktree
  and the checkout, and a value written there to protect a task would outlive
  the task and change the user's own commands.
- The environment reaches the process in both modes. Devcontainer execution
  already passed the adapter's variables through `docker compose exec --env`;
  host execution dropped them, so `tmux.CommandSpec` carries variables and
  `respawn-pane` renders them as `-e` before the working directory and program.
  A guard that held in one mode would be a guard the user could not rely on.
- Stashing is not forbidden. Nothing refuses `git stash`, and no tool policy is
  added to the generated settings: a session that stashes has decided something,
  and this is for the settings that decide without it. What that leaves is
  recorded below rather than described as solved.
- The structural fix is named and not taken. A per-task clone — hardlinked, or
  sharing objects through `alternates` — gives each task its own refs, config,
  hooks, and stash, and would let the read-write Git directory mount in evidence
  3 become task-scoped. It is not a patch: branch visibility in the user's
  repository, the review comparison, and the `git worktree`-based cleanup plans
  of ADR-029 all assume linked worktrees. It stays available and unchosen.

Consequence: the loud half of the hazard is closed and the quiet half is
narrowed to something a session had to choose. An agent can still stash, a
`git stash clear` still reaches the user's entries, and a removed worktree still
leaves its entries in the repository. Revisit on the first dogfood incident, or
when concurrent tasks on one repository stop being occasional — whichever comes
first — and decide then between a refusing hook and the per-task clone, which
OQ-014 holds open.

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

**Open, and half built.** Feat now allocates a host port per reachable service
per task and tells every managed service the address (ADR-065), which is what
such a proxy would have to route to. It is not a substitute for the feature: an
allocated port changes with the task, and the point of a stable hostname is a URL
that does not. What still blocks it is what was recorded against it before — a
proxy is a machine-wide resource with a lifetime no task owns, which is the
`shared` lifecycle ADR-034 called roadmap work, and a label-driven one wants the
Docker socket this product's headline denies the agent.

### OQ-009 — Plugin protocol

What external adapter protocol is justified after internal interfaces have stabilized? Do not define it speculatively.

### OQ-010 — Mobile product scope

Which remote actions users actually perform on a phone remains a product discovery question. Do not build native mobile apps before PWA usage evidence.

### OQ-011 — External/shared database automation

The dogfood project uses pre-existing staging databases. Assignment, migration, seed, and cleanup conventions need project evidence before generalization. **Resolved: the evidence arrived and says not to generalize. The declaration Feat could not verify is removed and the per-task discriminator stays, see ADR-048.**

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

### OQ-013 — What the explicit review request earns

The agent protocol is a generated system-prompt section, a generated helper
script, an outbox, an inbox, message validation, a poller, and a helper that
blocks on a verdict with its own timeouts and recovery. ADR-032 and ADR-036 built
it on the rule that an end of turn means idle and never done, and that semantic
completion needs an explicit act by the agent.

The maintainer's objection, raised while dogfooding slice 13: a user mostly wants
to know that an agent went idle, and to review a task that changed something.
Both are observable without the agent's cooperation — Feat already records a
`GitObservation` per repository — and the explicit message may be paying for
itself only in the projects that configure a completion gate.

Two things are already clear and neither settles it. Idle plus changed files is
not a substitute for the message: an agent writes files within minutes and stays
dirty for the rest of the task, so the condition would be true at every turn
boundary and would say nothing the idle notification does not. And the gate does
need an explicit trigger, because a project's test suite cannot run at every
pause — which makes the gate, rather than the notification, what the protocol is
actually for.

That points at a narrower question than "keep or remove": whether the protocol
should be generated at all for a project that configures no checks. There, the
whole apparatus produces a workflow state, one notification, and the agent's own
summary in the review record — and the agent's summary is the only part nothing
else supplies.

The evidence to decide on is the measurement slice 13 already owes: how often the
agent requested review unprompted, how often the user had to ask for it, how
often an idle notification was already the moment they would have reviewed, and
whether a gate ever caught something that would otherwise have been reviewed
broken. Three real tasks answer all four. Do not decide before them, and do not
decide the narrow question by removing the general one.

### OQ-014 — Whether a linked worktree is the right per-task boundary

ADR-029 gives a task a linked worktree per repository, and ADR-056 records what
that shares with the user's checkout and with every other task on the same
repository: one stash stack, one configuration, one set of hooks, one object
store, and one branch namespace. Git protects the branch namespace itself, and
ADR-056 closes the autostash route onto the stash; the rest is unguarded, and a
read-write mount of the user's Git directory carries all of it into every task
container.

The alternative is a per-task clone — hardlinked, or sharing objects through
`alternates` — which isolates refs, configuration, hooks, and the stash for
roughly a worktree's cost, and which would let that mount become task-scoped.
Against it: branch visibility in the user's repository, the review comparison,
and the cleanup plans of ADR-029 all assume a linked worktree, and `--shared`
adds a pruning hazard the worktree does not have.

The evidence to decide on is dogfood, and it is narrow: how often two tasks run
against one repository at the same time, whether an agent ever reaches shared
state a worktree does not isolate, and what a task branch being invisible in the
user's own checkout would cost the review workflow in practice. Do not decide
before that; a boundary changed on reasoning alone would move ADR-029, ADR-032,
and the cleanup contract at once.

### ADR-057 — The agent's environment is owned by its session, and the pair of verbs is resume and stop

Status: accepted  
Recorded: slice 13, from dogfooding the devcontainer lifecycle

A task's agent environment had two ways in and one way out of the product:
`feat implement` created it, cleanup removed it, and nothing else named it. The
maintainer's question, asked while looking at a task whose container had died:
why does a devcontainer have no management surface, when the application runtime
next to it has six verbs?

Evidence:

1. The command surface says it plainly. `feat runtime` has `create`, `start`,
   `stop`, `destroy`, `status`, and `logs` for the *application's* environment.
   The agent's had none, and `Resume` — the one verb that manages it — existed on
   the daemon and the client with the dashboard as its only caller. So
   reconciliation's own finding said "resume the task to start it again" to a
   user who, outside the dashboard, had no way to do that.
2. The record and the guard disagreed with each other, and one pass produced
   both halves. `reconcileEnvironments` wrote what it saw onto the execution
   record and left the session's process state alone; `resumable` decided from
   that process state, and its liveness check was the *existence* of a tagged
   tmux object. Feat sets `remain-on-exit` on every pane it creates (ADR-030), so
   a pane whose command is over is still a pane tmux reports — and the pane's own
   process is on the host side of a container, so it outlives one. Measured on a
   jobharbor-dev task whose devcontainer exited 137: the finding said to resume,
   the resume said "it is running in a terminal that is still there. Attach to it
   instead", and the only way out was `kill-window` on Feat's own tmux socket.
3. Stopping a container meant destroying the task. `feat task cleanup` was the
   only thing that would stop an agent's containers, and it removes the worktree,
   the branch, and the control workspace with them. A user freeing memory
   overnight had no lever that was not also a way to lose the work.
4. One container per project was evaluated and does not work. The generated
   override replaces the base file's mounts *by container path* with this task's
   worktrees, at the access this task selected (ADR-033 evidence 1 and 2) — so
   which worktree is at `/srv/api` is the task's identity expressed as container
   configuration, and one container cannot answer it three ways. The one shape
   that is mechanically possible, mounting the worktree root and giving each
   agent a subdirectory, gives up cross-task isolation, per-repository read-only
   access, and the per-task control workspace that ADR-032 splits — and it
   returns the user's real checkout to the agent, which is what evidence 1 exists
   to prevent.
5. A `start` verb would manufacture the resource class Feat handles worst. A
   launch that fails after its container exists already leaves a container the
   record does not name, and cleanup plans nothing for it. A verb whose purpose
   is to create a container with no session behind it would make that state
   reachable deliberately.

Decisions:

- The environment's lifetime is its session's, and this is a rule rather than an
  omission: it comes into being with a launch, comes back with a resume, sleeps
  with a stop, and is removed by cleanup. There is no `start` and no `create` for
  it. [04-functional-specification.md](04-functional-specification.md) states it
  where a reader looking for the missing verb will find it.
- One invariant ties the two halves of a session together: an agent process
  cannot be alive while the environment it runs in is not running. Every
  observation applies it, so a session whose container is gone stops claiming a
  running agent — which is what makes the recovery reconciliation already
  recommends available to the user it recommends it to.
- The state is `failed` rather than `stopped`, and it raises `session_failed`. A
  stop the user asked for records its own process state before any observation
  runs, so an alive process against a container that is not running is a death
  nobody asked for. No desired-state field is added: the act that had the
  intention is what writes it, which keeps every later reading a pure observation
  (CLAUDE.md's rule that persisted desired state is never assumed to equal
  observed state).
- Resume decides liveness from what a user could actually attach to: the window
  exists, its agent pane is not dead, and — for a containerised session — the
  environment is running. Each is asked rather than believed, and a failure to
  look is not evidence, so an unreadable tmux or Docker refuses the resume by
  name rather than guessing.
- `feat task resume` and `feat task stop` join the noun ADR-040 established.
  Neither takes a top-level alias: they are new, so there is no shell history to
  protect.
- Stop keeps the worktrees, branches, control workspace, volumes, and tmux
  window, does not move the workflow, does not touch the application runtime, and
  clears attention — a task with no agent is not waiting for anybody. It is
  `docker compose stop` rather than `down`, and it asks for no confirmation
  except in the dashboard, where a key press is cheaper to hit by accident than a
  typed command and only when the agent is mid-turn.
- A launch, a resume, and a stop are bounded by `api.AgentTimeout`, declared on
  the endpoint's contract the way `api.RuntimeTimeout` is, with the client
  waiting for it plus a margin. Three minutes, matching the daemon's own patience
  for a container to come up. This closes the launch half of the cancelled-launch
  finding: a client that gave up at ten seconds cancelled a launch the daemon was
  still serving and left a container behind that nothing could name.

Consequence: two commands, one endpoint, one interface method, and one invariant
in the two places that observe an environment. `feat project build`, an execution
poller, and any per-project container stay out — the first two are worth doing
and are not this, and the third is refused above. The orphaned container a failed
launch leaves is still open: the budget stops the launch being cancelled, and
finding a container for a task with no session is a separate piece of work.

### ADR-058 — Attention is set by the end of a turn, not by a message the agent wrote mid-turn

Status: accepted  
Recorded: slice 13, from the event logs of the dogfood runs

The maintainer's observation, from dogfooding: `possibly_waiting` shows up while
the agent is still running. The stored event logs say it did, on every task that
ever asked for review, and the cause is a row in the normalization table rather
than a race.

`possibly_waiting` is defined as the conservative reading of an end of turn:
Feat cannot tell a finished turn from a question, so it says the session may be
waiting rather than that it is. The review-request row set it too, on the theory
that an agent asking for review is an agent waiting for an answer. It is not.
The agent writes that message in the middle of a turn it then carries on with —
finishing what it was saying, waiting on the completion gate, and going back
into its own loop when the gate fails (ADR-036) — and nothing revisits the state
until the turn ends.

Evidence, all of it from `~/.local/share/feat/projects/*/tasks/*/events.jsonl`:

1. Task `e3065d30` requested review at 20:03:42 and set `possibly_waiting`. The
   gate ran, failed at 20:04:37, and the agent went back to work, committed, and
   reported again at 20:10:45. Feat said the session might be waiting for a
   person for seven minutes of demonstrably active work. Task `cde786f3` is the
   same shape across two gate runs, 19:59:25 to 20:06:54.
2. The state is inverted rather than merely stale. It is cleared by
   `KindTurnEnded` — the moment the agent actually stops — and re-set five
   seconds later by the idle grace. During the stretch where the agent is
   working it reads "possibly waiting"; at the instant it stops, it reads
   "none".
3. It is not a display-only wrong. `attentionSummary` counts both attention
   states into the "N tasks may need you" line the dashboard puts above
   everything else, so a working agent was in the count a user reads to decide
   whether anything needs them.
4. Nothing in the specification asked for it. FR-AGENT-008 is about semantic
   completion reaching a workflow state, which the same row does and keeps;
   03-domain-model.md defines the attention states without giving a review
   request one; and the two attention rules recorded in slice 7 above are about
   a launched agent that has said nothing and about an ended turn. The row was
   an implementation choice that was never written down, which is why nothing
   caught it disagreeing with the state's own definition.
5. `becomeIdle` wrote the state onto the task and recorded no event for it. Every
   `possibly_waiting -> needs_input` line in the logs has no matching arrival:
   the task's history explains the process going idle and never mentions the
   attention state the dashboard was counting.
6. `reportSilentStart` recorded the opposite error. It skipped the state change
   when the task already needed the user, and recorded the event anyway with a
   hard-coded `from: none`. Task `e7c9bfc9` at 19:22:19 carries
   `none -> needs_input` for a transition that did not happen: the task had been
   `needs_input` since a failure ninety seconds earlier.

Decisions:

- The review-request row sets no attention. The workflow state is what says a
  task is in review and what the notification is composed from; attention is the
  session dimension, and it stays the answer to "has the provider reported
  something that means a person is needed". A review request answers the first
  question and says nothing about the second.
- Attention after a review request is therefore decided where it is decided
  everywhere else: the turn ends, the idle grace expires, and the conservative
  state is set then — when it is true. An agent blocked on the gate helper is not
  idle and does not reach it, which is correct: it is waiting for Feat, not for a
  person.
- `becomeIdle` records the attention change it makes, as its own event with its
  own reason. A state that reaches the count above the dashboard must be
  explainable from the task's history.
- `reportSilentStart` records an event only when the state moved, and records the
  transition that happened. A task that already says it needs the user learns
  nothing from silence, so the report is a log line and not a second claim.

Consequence: one table row, two event-recording sites, and no change to any
stored format, endpoint, or state vocabulary. `possibly_waiting` now has exactly
one meaning in the product — the session went quiet and Feat will not guess why —
which is the meaning 03-domain-model.md always gave it. The narrower defect in
the same area is untouched and still recorded in `review/findings`: an idle timer
that has already fired cannot be cancelled, so a prompt arriving in that window
can still be followed by `becomeIdle` marking a working session idle. It needs a
generation check rather than a decision.

### ADR-059 — A task's containers are addressable by name, and a mounted directory is released before it is removed

Status: accepted  
Recorded: slice 13, from dogfooding the mount-and-socket rules

Both halves of this are one sentence: the container outlives the request that
created it. A launch that fails after its container exists leaves resources the
product could not see, and a cleanup that removes the tree those resources mount
fails part way through. ADR-057 closed the third half — the client no longer
cancels a launch the daemon is still serving — and left these two, saying so.

Evidence, from a jobharbor-dev task (`d7f54fa5`) whose devcontainer Compose file
had been given a mount of the home directory on purpose:

1. Nothing in the product could remove what the cancelled launch had created.
   The container was on the machine, exited, with its network beside it; the task
   record had `session: null`, because the session is created after the container
   and a launch that fails never reaches it; and both passes that could have
   found it resolved from that record. `resolveEnvironmentCleanup` and
   `reconcileEnvironments` each began by returning early for a task with no
   recorded environment, so cleanup planned nothing and reported success, and no
   reconciliation pass mentioned the containers at all.
2. It was archived over. Cleanup archives a task once the classes a user chose
   are gone, and refuses to archive one that still owns resources — but the
   refusal reads the plan, and the plan was empty. So the record that named the
   task was retired while its containers stayed, and an archived task is one
   reconciliation stops looking at.
3. Widening the launch refusals makes it more frequent rather than less. Every
   rule added in `fix/mount-and-socket-rules` inspects the started container, so
   every one of them fires after the container is up (ADR-033's amendment to
   ADR-032). The refusals are right; what they leave behind was not resolvable.
4. Removing the control workspace failed while a container still mounted it. The
   first `cleanup/execute` failed with `unlinkat …/control/jobharbor-dev/
   d7f54fa5-…/outbox: permission denied`; the second, after the container had
   died, succeeded. On macOS the file-sharing layer holds a directory that is an
   active bind-mount source, so this is an ordering rule rather than a
   permissions bug. It became reachable when the control workspace stopped being
   one mount and became three (ADR-032's read-only split).
5. The class order alone does not establish the ordering. Cleanup removes the
   agent containers before the control workspace, but only when a user chose
   both — the classes are independent choices (FR-CLEAN-002) — and the case above
   is a task whose plan named containers nowhere at all.
6. The name was there the whole time. The agent's Compose project is
   `feat-agent-{project_id}-{task_id}`, generated rather than configured
   precisely so it cannot collide (ADR-033), and Compose labels every container,
   network, and volume with the project it belongs to. `docker compose
   --project-name <name> ps --all` and `down` were measured against real Docker
   with no `--file` at all: they find an exited container and its network, they
   remove both, and they exit zero over a project that no longer has anything.

Decisions:

- A task's agent Compose project is addressable by its name alone, and the name
  is derived from the two identifiers rather than read from a record. That makes
  it total: a task that never recorded an environment has the name it would have
  used, and a project whose own Compose file has changed since — which is what
  made the launch slow enough to be interrupted — still answers, because nothing
  in the query reads that file.
- This is a derivation and not a scan. What it finds belongs to the task by
  construction, which is the exactness FR-CLEAN-001 asks for reached without the
  record that ordinarily supplies it. Feat looks at nothing else on the machine
  and adopts nothing: a container carrying another project's name is not a thing
  this can name.
- Reconciliation reports it as an orphan of the record — the finding names the
  task the containers were created for, and the action is the cleanup that
  resolves them. Nothing is restarted or removed there, as in every other pass
  (FR-STATE-004).
- Cleanup plans them as the agent-container class it already has, with the
  volumes carrying the same label as the volume class they already are. No new
  class and no new state: what changes is that the two passes have an answer for
  a task whose record has none. Archiving is refused over them by the rule that
  already existed, now that the plan can see them.
- The control workspace is removed only after establishing that no container of
  the task's agent Compose project is still there. Only that project is asked
  about, because it is the only one the workspace is mounted into — the
  application runtime's override mounts worktrees and never the control tree.
- What is established is about Feat's own containers, and the wording says so. A
  container somebody else pointed at that directory is not one Feat knows to ask
  about, and claiming the directory is unheld would be the overclaim the honesty
  rule exists for.
- An unanswerable question refuses. A Docker that will not say what it holds
  leaves a user with a workspace they can still remove once it is fixed; removing
  it on an unanswered question leaves a half-removed tree, which is the outcome
  this rule exists to prevent.

Consequence: one type in the Compose execution adapter — a project addressed by
name, which observes and removes and can never create — and the two early
returns replaced. The recorded path is untouched: a task with a session still
resolves its environment from the specification its launch recorded, because a
task's environment is the one it was launched with (ADR-033, ADR-057).
[06-technical-architecture.md](06-technical-architecture.md) states both rules
where a reader meets them. What stays open is a task archived by an earlier build
over resources nobody could see: cleanup refuses an archived task and
reconciliation skips one, so those are removable by `docker compose --project-name
feat-agent-{project}-{task} down` and by nothing in the product. It is a state no
build from here on can create, and closing it for the machines that already have
it would mean tracking a task Feat has stopped tracking.

Amended after the round-2 review traced this ADR's own edges. The evidence below
is read from the code rather than measured against Docker, which is stated
because evidence 1–6 were the other kind:

7. **The two queries this ADR exists for were the only Compose invocations in
   either adapter that named no directory.** Neither `--file` nor
   `--project-directory` nor a working directory, so `exec.Cmd.Dir` was empty
   and Compose discovered its files by walking up from whatever directory the
   daemon inherited — `feat daemon start` sets none. A user who starts the
   daemon from an application repository, which by construction holds the
   Compose files, gives every one of these queries a `compose.yaml` to find.
   Evidence 6 measured the no-file case in a directory where nothing was
   discovered; nothing measured the case where something is. What is certain
   without measuring it is that what `ps` and `down` act on had stopped being a
   question the product controls.
8. **The release rule refused on the state evidence 4 measured the removal
   succeeding in.** `Remaining` carried Docker's free-text status and no state,
   though `ps --format json` reports one and the parser already decoded it and
   threw it away. ADR-057's `feat task stop` keeps a task's containers on
   purpose, and the classes of a cleanup are independent choices (FR-CLEAN-002),
   so the ordinary morning after — a task stopped overnight, its control
   workspace cleaned up alone — was refused with "mounted into container …
   (Exited (137) 3 hours ago)". Exactly the container that had died, which is
   the case that worked.
9. **"An unanswerable question refuses" was implemented one call below the two
   places the question stopped being asked.** `agentProject` returned nil — the
   same nil that means there is nothing to ask about — when the Docker CLI was
   not on `PATH`, and when today's `agent.execution.mode` no longer said
   devcontainer. The first is a machine that cannot answer. The second is a line
   in a file the user edits, deciding what Feat asks about a container a launch
   had already created. Both left the workspace removed without anything
   established, and both left the plan naming nothing with `Archivable` true,
   which is evidence 2 reached by another route; the second also left the volume
   removal reporting a user's confirmed selection as not removed, with no error
   and no reason.
10. **One report said both things.** A task with no session was offered "clean
    it up and prepare the task again; its agent never ran, so nothing it did is
    lost", and the finding beside it named the containers its launch had left.
    The reassurance is true of a task interrupted before its container existed
    and false of the task this ADR exists for, and the record does not
    distinguish them — the answer the same pass had already fetched does.

Decisions:

- The by-name queries run from a directory Feat names, carried both as
  `--project-directory` and as the invocation's own working directory. Any
  directory Feat owns would do; what it may not be is the one the daemon
  inherited or one a project controls. The type refuses to be constructed
  without an absolute one, because an omission is the value that reinstates the
  dependency and omissions are what nobody notices.
- The release rule is about containers that have not stopped. `exited` and
  `dead` release what they mounted; every other state, and a state Compose did
  not report at all, counts as holding it — so a state nothing established comes
  out the way an unanswerable question does. `created` is in the careful half on
  that ground rather than on evidence, and is the one a measurement could move.
- "The question does not apply" may be read only from the task's own record: a
  draft, or a session recording host mode, which the domain already enforces
  carries no execution environment. Configuration is not consulted at all. A
  task's environment is the one it was launched with (ADR-033, ADR-057), and a
  session-less task is the one case where the record does not say — so it is
  asked about rather than assumed away, which is what the derived name was built
  for.
- Everything else that stops the question being asked is an error rather than an
  absence, and each caller says so in its own terms: the release rule refuses,
  the cleanup plan carries a problem and is rendered un-archivable, the volume
  removal fails rather than declining a confirmed selection in silence, and
  reconciliation records a problem.
- Reconciliation asks what a session-less task left once per pass, and every
  pass that reports on it reads that one answer. Two queries a few milliseconds
  apart are two moments, and a report is one document.

Consequence: [04-functional-specification.md](04-functional-specification.md)
FR-CLEAN-002 and [06-technical-architecture.md](06-technical-architecture.md)
state the rule as "still running" rather than "still there", and say that a
question that could not be asked refuses as an answer of "still there" would.
Two things are knowingly left. Whether Compose would have loaded a discovered
file for `ps` and `down` given a project name and no `--file` is unmeasured; the
dependency is gone either way, and the regression test is on the invocation
rather than on the answer. And `Plan.Check` refuses an archive over the targets
a plan names and not over the problems it records, so the refusal is complete on
both shipped surfaces — each reads `Archivable` — and incomplete for a client
that calls the daemon directly. That is a property of every problem source
rather than of this one, and is recorded here as open rather than closed.

### ADR-060 — A failed task carries the reason it failed

Status: accepted  
Recorded: slice 13, from dogfooding the launch refusals

The maintainer, watching a launch fail on purpose to test ADR-059: it is not
clear in the TUI why the task launch failed.

Evidence:

1. The reason existed and was reachable from nowhere. `transition` writes it as
   the `Detail` of the workflow event, so it is in `events.jsonl` on disk, and
   the dashboard's own launch path put it in `m.err` — a banner cleared by the
   next action. A user looking at the task a minute later saw `workflow failed`
   and stopped.
2. Nothing else could have shown it. `api.Task` had no field for it, there is no
   per-task events endpoint, and the dashboard discards the events it streams
   (`case eventMsg: return m, tea.Batch(m.load(), m.awaitEvent())`) — including
   the `Detail` the stream carries. So the one copy was the file.
3. It is every failed task, not every failed launch. A Git apply that fails, a
   resume that cannot start, a session the provider reported as failed: all five
   paths into `failed` go through one call, and all five lost their reason the
   same way.
4. The event log's stated job is that "Feat can explain what happened later"
   ([06-technical-architecture.md](06-technical-architecture.md)). Nothing in the
   product reads it.

Decisions:

- The task carries the reason it failed and the moment it did. The workflow state
  and its explanation are one fact, and a state a user cannot act on is one that
  only describes itself.
- It is recorded by the transition rather than beside it. `FailWith` is the only
  way into `failed` that records anything, so a caller cannot write the state and
  the reason apart, and a blank reason is refused: whatever failed knows what it
  was.
- Leaving `failed` discards it. A recovered task that still carried its old
  reason would be read as failing now, and a stale explanation beside a live
  state is worse than none.
- The reason is kept verbatim. It is the same sentence the caller was given;
  rewording it here would produce a second account of one event.
- It is stored as an optional field at the same schema version, which is what the
  storage codec's own rule allows: a build that adds a field without changing the
  meaning of the existing ones stays readable by the build before it, and a
  snapshot written earlier decodes to no failure — which is the truth about a
  record that never held one. No migration, and none owed.
- The panel prints it under the workflow and wraps it rather than truncating it.
  A reason names a service, a mount, or a path, and every one of those is at the
  end of the sentence.

Consequence: one domain field and one method, one stored field, one DTO field,
and one block on the task panel. The `failed` state itself, the transition table,
and every notification are unchanged. The fixtures grow a second task — a task
carries a reason only while it is failed, so the fixture that has reached review
cannot also cover it, and the coverage tests that keep the round trip honest now
take the union over both. What this does not do is make a task's history
readable: the event log is still a file nothing in the product opens, and a
per-task events endpoint stays a separate piece of work.

### ADR-061 — The confirmation belongs to the removal, not to each tick that led to it

Status: accepted  
Recorded: slice 13, from using the cleanup screen

The maintainer, on the dashboard's cleanup dialog: it is clunky — why the extra
confirmation, and why the extra archive button.

Evidence:

1. The screen asked twice. Ticking a class with warnings raised a `y/N` that took
   the keyboard immediately, and `enter` raised another. A task with three risky
   classes was four questions and three ticks, and the first three arrived while
   the user was still reading the list they were choosing from.
2. The first question bought nothing the daemon can see. `selection()` sends
   `ConfirmedWarnings: class.Warnings` whatever was accepted — the `accepted` map
   only gated `removable()` — so ADR-037's defence against a stale confirmation
   rests entirely on the daemon comparing those strings with what is true at
   removal. One question and two produce the identical request.
3. The screen already says what the first question said. Since the inventory
   change earlier in this slice, each warning is drawn beside the target it is
   true of. The modal was reading a line back to the user that was on the screen
   behind it.
4. The archive row was not a button but a checkbox rendered only once every class
   was selected. Because it is drawn under the inventory, and the inventory is
   sized by what the tail takes, ticking the last class shortened the list by two
   lines and moved what the user was looking at. `A` was in the key map
   throughout, and for most of the interaction did nothing.
5. `r` looked dead, reported after the above, and then: in which case is a
   re-resolve even required. It always asked the daemon and always replaced the
   inventory, and on a task nothing had touched it redrew a list identical to the
   one on the screen — while setting `m.status = ""`, so asking the question
   blanked the one line that could have reported the answer. The plan has carried
   a `ResolvedAt` since the endpoint existed, "when the inventory was taken", and
   no surface displayed it.
6. The case a re-resolve is required for is narrow and real, and it is not the
   one the key implied. Cleanup is reachable on any non-draft task, including one
   whose agent is mid-turn — so the likeliest change under an open cleanup screen
   is a worktree going dirty while it is being read. The daemon refuses that at
   `checkWarnings`, comparing what was confirmed against what it observes at
   removal, so the user meets it as a rejection naming a warning they were never
   shown. The other case, a resource gained or lost, needs a second actor and is
   rare. Both are answered by looking at the moment the answer matters, and
   neither is answered by a key a user has to know to press.

Decisions:

- One confirmation, at the removal. It names the classes, lists every distinct
  warning of everything selected, and defaults to no. That is FR-CLEAN-003 met
  where the consent is actually given: a user reads the whole cost of the whole
  decision, against the request that is about to be sent.
- Selecting asks nothing. A tick is a decision being assembled. The warnings stay
  visible beside their resources throughout, and the class title carries a marker
  so a class does not read as free when the window has scrolled past its
  warnings.
- The confirmation's question is its first line and the warnings follow it. A
  region too small for both keeps the line that says what answering does; the
  warnings are drawn twice over and the question is drawn nowhere else. The
  inventory yields its lines to the question rather than the other way round, and
  says how many it yielded.
- `feat task cleanup` keeps its per-class questions, because there they are the
  selection. A terminal prompt has nothing to tick, so the question is how the
  choice is expressed rather than an interruption of it. What changes is that the
  specification now says which shape belongs to which kind of surface, instead of
  the TUI inheriting the CLI's sequence because it was written second.
- The archive choice is a row of the screen and not a key of its own. Everything
  on this screen with a checkbox is reached the same way — down to it, space to
  tick it — and the archive choice was the one checkbox the cursor could not land
  on, ticked instead by a key that did nothing for most of the interaction. A
  second way of doing the one thing the screen does is a way that has to be
  learnt separately and remembered separately.
- It is drawn wherever the plan could ever be archived, greyed and saying what it
  is waiting for when it may not be taken yet. The rule it is waiting for is
  unchanged and is ADR-037's: archiving a task that still owns a running
  container manufactures the orphan reconciliation exists to report. Drawing it
  throughout is what keeps the inventory above it from moving as classes are
  ticked, and it is what stops a cursor stop from blinking in and out of
  existence underneath the cursor. Pressing space on it while it waits answers
  where the press happened rather than only in the status line.
- A screen with a question outstanding advertises that question's keys and no
  others. The key map and the scroll note both offered keys the confirmation had
  taken, which is a promise a user has to try in order to disbelieve.
- Enter resolves before it asks, and there is no re-resolve key. Freshness is
  worth something at exactly one moment — the moment consent is given — so that is
  when Feat looks, rather than leaving a user to know they should ask. The
  confirmation is then built from a plan taken a moment ago, which is what makes
  "the warnings of everything selected" a statement about now.
- The inventory says the moment it was taken. The screen is an observation and
  not a live view, which is the same thing the recovery overlay says by naming
  when it last checked, and it is what stops the timestamp on a dialog left open
  for ten minutes from being read as current.
- What the resolve found decides whether the question is asked at all, on the two
  axes a plan moves along. They are separate because the token only sees one: it
  covers the identity of every target and deliberately not the warnings, so that
  an agent writing a file is not reported as a stale plan.
  - A cost that moved under the same resources asks anyway. The warning is listed
    in the confirmation, above the answer, which is where it is read — and this is
    the case the whole arrangement exists for.
  - A resource gained or lost does not. The confirmation names classes rather than
    targets, so a class that quietly grew a third worktree would be confirmed by a
    user who had read two. The inventory is replaced and the question waits for
    another enter, which is FR-CLEAN-001's rule about choosing against a summary
    applied to the moment it would be broken.
  - A selection whose resources have all gone says so instead of either, because
    "read it and press enter again" is poor advice for a screen with nothing left
    to press it for.
- The selection survives a plan that moved, minus any class the plan no longer
  names. A tick is a choice about a resource and a resource that has gone takes
  its choice with it; the rest stand, because discarding them would charge the
  user for a change they did not make.
- A cleanup that finished closes the dialog, and what it did becomes a line of
  the footer. The overlay is a transaction the user opened (ADR-041) and the
  transaction is over: what stayed open afterwards was a screen about a decision
  already taken, showing an inventory of what was left rather than what had been
  asked about. For an archived task it was worse than redundant — the screen's
  next resolve is one the daemon refuses, because an archived task is one Feat has
  stopped tracking, so the dialog sat over an error it had caused by remaining.
- The line names the classes and counts what was already gone. The classes because
  they are what the user chose; the count because a resource that was already gone
  is not a failure — the user asked for it to be absent and it is — but a cleanup
  that removed nothing because everything had gone is a different morning from one
  that removed six things. The itemised list is in the event log, which is where
  "Feat can explain what happened later" already lives.
- A cleanup that failed halfway keeps the dialog, and re-reads the plan. There the
  screen is the only account of what happened, the classes are removed in a fixed
  order so some of them went, and the inventory from before the attempt describes
  a machine that no longer exists. Reading it again is what makes a partial
  cleanup recoverable by looking at it, which is what ADR-029 said it would be.
- The keyboard is held while the resolve is in flight. A tick landing in between
  would put a class into the question that the plan under it was never checked
  for, which is the defect this whole decision is about, arriving through the
  door built to prevent it.
- The key that executes says what it acts on: `enter cleanup selected`. It takes
  the whole selection and not the row the cursor is on, and a key map that said
  `remove` beside a cursor resting on one class read as an offer to remove that
  class. Sixty-three cells against the sixty-eight a dialog has at the layout's
  minimum width, so the distinction costs nothing to draw. It says "cleanup"
  rather than "remove" because that is what the screen and the command are both
  called, and the removal of particular classes is what the confirmation names.

Consequence: the `accepted` map and the `confirming` state leave `cleanupModel`,
which removes the only place the screen held consent separately from selection,
and the cursor runs one row past the classes. The wire format, the daemon's
validation, and ADR-037's stale-confirmation refusal are untouched — the request
is byte-for-byte what it was. A removal of two risky classes goes from seven key
presses to four, and the screen's key map from six keys to three: `A` and `r` are
both unbound here, space is the only way to tick anything, and enter is the only
way to ask for anything. The screen's per-resource result rendering goes with the
dialog that held it — and what does not come back is a partial result after a
failure: the daemon returns one alongside its error, and the HTTP layer's
`send[T]` discards the body of a non-2xx, so the UI has never had it. The error
names the class that failed and the classes are removed in a fixed order, so what
went is derivable; carrying the result itself would mean changing the shape of
every error response, which is a wider change than this one.
[04-functional-specification.md](04-functional-specification.md) states the
surface rule under FR-CLEAN-003. What this does not settle is whether the CLI's
per-warning question is worth its own press on top of its per-class one; it is
the same sequence it has always been, and no evidence from using it says
otherwise yet.

### ADR-062 — A project is configured by answering questions, and the answers are checked before there is a file

Status: accepted  
Recorded: slice 13, from the cost of adding a project by hand

The maintainer, adding a second project: adding a project to Feat is quite
involved as it stands — the YAML file has to be created by hand, copied from the
template.

Evidence:

1. The first thing a new user does is the thing with no help in it. Every other
   command explains itself and validates its input; the file every one of them
   reads is written in an editor, against a 176-line example, with `feat doctor`
   as the only feedback loop.
2. Most of what the file asks for is already true of the machine. The
   working-tree root, the remote, the default branch, the Compose files beside a
   checkout, and the services those files declare are all facts a tool can read,
   and every one of them was being retyped — which is also how a configuration
   acquires a value that was never true, such as `main` in a repository whose
   branch is `trunk`.
3. The parts that are decisions are few: which repositories take part, how each
   takes part by default, where the agent runs, which provider CLI it expects,
   whether a task runs application services, and what verifies the work. Six
   questions, against a file with sixty fields in it.
4. [02-user-workflows.md](02-user-workflows.md) had already put a wizard in
   public v0 and [11-implementation-plan.md](11-implementation-plan.md) had made
   it conditional on manual configuration being "the dominant public blocker".
   Dogfooding answered that question ahead of schedule: it is the step that is
   hardest and the only one Feat does not help with.

Decisions:

- The wizard is a conversation on the command line, not a screen. `feat project
  init` runs before there is a daemon, before there is a project, and possibly
  before Feat has ever run on the machine, and the dashboard is a client of a
  daemon. A line-oriented conversation is also what a user can read back in
  their scrollback, which is what somebody debugging their own configuration
  does next.
- The answers are collected into `config.Draft`, and the file is rendered from
  it once. Nothing is written down as it is answered, so an interrupted run
  leaves nothing behind, and the text the user is shown is rendered from the
  same value the file is written from.
- The rendering is parsed, resolved, and validated before it is displayed. What
  is offered is therefore a configuration Feat accepts, not a proposal that might
  be; a rule the questions failed to cover fails while the answers still exist,
  naming its field, rather than after the file is on disk.
- What Feat can find out, it finds out; what it assumes, it says. A path is
  inspected rather than trusted, and a repository with no remote is reported as
  having none and gets the local base policy — which is the one value the wizard
  decides on the user's behalf, decided from what it found rather than from a
  preference.
- The file states decisions and omits defaults. A default written into a
  generated file is a value that stops following Feat when Feat's own changes,
  and `feat project show` already prints the resolved configuration. The
  capability block is the deliberate exception: what the agent may reach is
  written down, with the sentence saying why it cannot vary, because that is the
  paragraph somebody deciding to run Feat on their own work will look for.
- Nothing is written until the whole file has been displayed and confirmed, and
  an existing configuration is never overwritten — the create is exclusive, so
  even a file that appeared during the conversation is left alone. There is no
  `--force`: a project's configuration is the one thing on the machine Feat asks
  the user to author, and losing it to a mistyped command is not a trade this
  command makes.
- Diagnosing and registering stay the commands they already are. The wizard
  offers each at the end and runs neither on its own: `feat doctor` is what
  checks the project against the host, `feat project add` is what the daemon
  records, and the wizard calls exactly those rather than growing versions of
  its own.
- Writing a file by hand remains supported and unchanged. Outside a terminal the
  command refuses and names the example to copy, rather than asking questions
  into a pipe.

Consequence: one command, one draft type in `internal/config` whose only
capability is to render itself and be validated, and two host discoveries in
`internal/project` — what a checkout says about itself, and which services a
Compose file declares. The configuration schema does not change, no field is
added, and the file the wizard writes is a file the previous build would have
read. What this does not do is keep an edited file in step with anything: the
wizard writes a project once, and the file is the user's from then on.

### ADR-063 — One flow, two askers: the wizard's questions are a package, and the dashboard asks them itself

Status: accepted  
Recorded: slice 13, from asking whether the wizard is reachable from the TUI

The maintainer, after `feat project init` landed: is it possible to execute the
project wizard in the TUI? It was not. The dashboard's only answer to an
unconfigured machine was a sentence telling the user to quit and go and type.
Asked next whether it should be a dialog rather than a released terminal: it
should.

Evidence:

1. The dashboard is where a new user is. `feat` with no subcommand opens it, and
   it opens on a machine with no project as readily as on one with twelve — at
   which point the one thing that would help was somewhere else. Preparing a
   task, the key a new user presses first, could only fail there.
2. Releasing the terminal to the line conversation worked and cost two
   workarounds, which is what a wrong seam costs. Bubble Tea holds the interrupt
   while it has released the terminal (ADR-049) and the conversation ran in the
   same process, so Ctrl-C could not reach it and a Ctrl-D exit had to be taught;
   and the dashboard repaints the moment the command returns, so a "press Enter"
   pause had to be added to stop it eating the outcome.
3. Task preparation is already a multi-step form drawn as a dialog, with a step
   back out of an answer and the rail still visible behind it. Next to that, a
   released terminal reads as the odd one out: no going back, no cursor between
   fields, none of the dashboard's own shape.
4. What the two askers would share is most of what the wizard is. The draft
   renders and validates itself in `internal/config`, and the host discoveries
   live in `internal/project`; what was in the command was the sequence — each
   question's proposal, its validation, and what it decides about the next one.

Decisions:

- The questions are a package, `internal/wizard`, and both askers drive it.
  `Step` says what to ask, `Answer` applies one answer, `Back` undoes one, and
  `Review` renders and validates. Neither asker decides what comes next, what is
  proposed, or whether an answer is acceptable.
- The flow reaches nothing. What it needs to know about the machine — whether a
  path is a checkout, what Git says about it, which Compose files are beside it
  and what they declare — it asks through `Host`, which `internal/cli`
  implements over `internal/project`. That is what lets the dashboard drive the
  same questions while remaining a client that reaches no adapter (ADR-031).
- `feat project init` keeps its conversation and owns its presentation: the
  headings, the indentation, the brackets around a proposal, and the offers to
  diagnose and register at the end. ADR-062's reasons stand — it runs before
  there is a daemon, and its scrollback is what somebody debugging their own
  configuration reads back.
- The dashboard asks the same questions on `p`, as a dialog over the rail. It
  adds what a screen can add and a conversation cannot: a cursor on the closed
  questions, `esc` to step back out of an answer, and the file scrolled in a pane
  before it is written. It does not add a question, a proposal, or a rule.
- A proposal is a placeholder, never the field's contents. Enter takes it and
  typing replaces it, which is what the brackets mean at a shell — prefilling
  the field meant typing appended to it, and an identifier proposed from the
  working directory became that directory's name with the answer stuck on the
  end.
- Stepping back restores a snapshot rather than replaying the answers. An answer
  changes more than the field it names — an access mode decides whether a
  repository is asked for a mount point, a mode decides whether the devcontainer
  is asked about at all — so the flow keeps the state each answer replaced.
- The dashboard asks its backend to write the file and to register the project.
  The exclusive create that refuses to replace an existing configuration lives
  once, in `internal/wizard`, and the daemon is reached over the socket as it is
  for everything else.
- Diagnosis stays a command. The dialog says `feat doctor` checks the project
  against the host and does not run it: a report of findings is a screen of its
  own, and the dashboard has never had one.

Consequence: `internal/wizard` holds the flow and its tests; `internal/cli` holds
the conversation, the host, and the file; `internal/ui` holds a dialog that draws
questions it does not author. A question added to the flow appears in both
askers, which is the property the split exists for. What this does not do is
diagnose from the dashboard, or offer the wizard where there is no daemon — the
dashboard is a client, and `feat project init` is the answer on a machine that
has never run Feat.

### ADR-064 — Diagnosis is read on the dashboard, and it says which process it is true of

Status: accepted  
Recorded: slice 13, from the hole ADR-063 left

The dashboard could configure a project and could not tell the user whether it
worked. The wizard's last screen named `feat doctor`, which is a command — so
the first-run path closed for writing a configuration and not for having one
that works, and that is where a project actually fails: a Compose service that
is not there, an agent that is not installed, a remote that does not resolve.

Evidence:

1. The questions cannot ask the host anything. Every proposal the wizard derives
   is about what a checkout *is*, not about whether the project *works*, and the
   difference is the whole of what `feat doctor` reports.
2. The findings are already data. `project.Diagnose` returns a report of
   `{check, severity, summary, action}`; the command's printer is one renderer
   of it, and a screen is another.
3. Diagnosis is worth having a second time. The first run tells a new user
   whether their configuration is right; every run after that is somebody whose
   task has stopped working, who is already looking at the dashboard.

Decisions:

- The checks run in the process the user is in front of, and reach the dashboard
  as data. `feat doctor` works before a daemon exists (ADR-028), so a daemon
  endpoint would be a second implementation of the same checks — and the answer
  would be about the daemon's environment rather than the one the user asked
  about. The dashboard's backend runs them and converts the report to `api`
  types, so the screen that draws them reaches no adapter (ADR-031).
- The screen says where the checks ran. A tool on this terminal's PATH is not
  necessarily on the daemon's, and the daemon is what launches agents: "checked
  from this terminal" is what the report is honestly about, and it is drawn with
  the findings rather than left to be assumed.
- Nothing runs a diagnosis on its own. The checks shell out to Git, Compose, and
  the container runtime; a dashboard that ran them on a timer would be one
  nobody could leave open. `D` runs one, `r` runs it again, and that is all.
- The subject is a project, not a task. Whether a Compose service exists is true
  of every task in a project or of none of them, so the screen checks the
  selected task's project — and every configured project when no task is
  selected, which is what `feat doctor` does.
- The wizard runs the checks itself once the file exists, rather than offering
  them. The user has just asked for the project to exist and is waiting either
  way, and the checks change nothing. What they find never fails the setup: the
  file is written, and a finding is a thing to fix rather than a reason to undo
  a project.
- A report opens at the first finding that is not a pass, with the heading it
  belongs to. Most of a report is passes and the pane holds a dozen lines; what
  is above the window is counted rather than hidden, so nothing is lost by
  starting where the problem is.
- A skipped check is drawn as a skipped check. It is not a pass, it says why it
  did not run, and it is counted separately wherever findings are counted
  (ADR-033).

Consequence: `api.Diagnosis` describes a report, `internal/cli` runs it, and
`internal/ui` draws it in two places — the dashboard's own screen and the
wizard's last step. The command is unchanged. What this does not do is diagnose
from the daemon, which is the only way to answer about the daemon's own
environment; that stays an open question for the machine where the two
environments differ.

### ADR-065 — A runtime is composed of its repositories, and a service that is not running the task's code says so

Status: accepted
Recorded: slice 13, before implementation

Evidence found while making the reference project's whole application — an API
and a frontend in separate repositories — run for one task. Most of it is
measured against Docker 29.5.2 and Compose 5.1.4 rather than reasoned:

1. **A project configured for host execution mounted nothing, again.**
   `jobharbor.yaml` had no `container_path` on any repository, because nothing
   asks for one outside devcontainer execution. `runtimeMounts` skips a
   repository without one (`internal/daemon/runtime.go:540`), so the runtime
   generated no `volumes:` at all and every service ran the user's ordinary
   checkout. It is ADR-034 evidence 10 surviving its own fix: the daemon was
   corrected to read the mount target from configuration, and nothing was done
   about a configuration flow that never collects it.
2. **Two repositories' Compose files cannot be merged with `-f`.** Compose
   resolves relative paths in every file against the project directory, which
   Feat sets to the first configured file's directory
   (`internal/daemon/runtime.go:479`). Both of the reference project's files use
   `build: .`, so listing them together builds the frontend from the API
   repository. Measured, not inferred.
3. **`include` is the mechanism that does work.** Paths inside an included file
   resolve against that file's own directory, and the long form takes a
   `project_directory` per entry. Feat's generated override merges over the
   result unchanged: `!reset null` clears an included service's
   `container_name`, ownership labels and worktree mounts land on it, and
   `docker compose config --services` enumerates it, so ADR-034 evidence 12's
   dependency walk still holds. It needs Compose 2.20; ADR-034 already requires
   2.24.
4. **The generated override never touches `build.context`.** A service that
   bakes its code with `COPY` runs the ordinary checkout whatever
   `container_path` says, and ADR-034's ordinary-checkout note cannot fire
   because the note inspects mounts and there is no mount. The reference
   project's frontend is a multi-stage build ending in nginx: mounting a
   worktree into it is meaningless, and only the build context decides what
   runs. A fix that addresses mounts alone leaves that service broken and
   silent.
5. **One field is doing two unrelated jobs.** The agent's execution adapter
   (`internal/daemon/execution.go:207,233`) and the application runtime
   (`internal/daemon/runtime.go:549`) both read
   `repositories.<id>.container_path`. They are different questions with
   different owners: where the agent's devcontainer mounts a repository is the
   user's free choice, and where an application's own services expect their
   source is a fact about that application's Compose files. `jobharbor-dev.yaml`
   needs `/workspace/jobharbor-api` for the first and `/app` for the second, and
   cannot say both.
6. **The configuration flow neither collects the field nor shows it.**
   `feat project init` jumps from the execution mode straight to the provider
   CLI when the answer is not `devcontainer` (`internal/wizard/wizard.go:639`),
   so a host-execution project is never asked. `feat doctor` drops the CONTAINER
   PATH column for the same projects (`internal/cli/table.go:87`). Both
   assertions covering that column use a devcontainer fixture
   (`internal/cli/project_test.go:310,349`), so neither branch is pinned. The
   value the daemon depends on is one the product neither asks for nor prints.
7. **Every way of getting this wrong is silent.** A missing container path
   produces no mount; a mismatched one produces two mounts, because Compose
   merges a service's volumes by target and a target that does not collide is
   simply added; a baked build context produces neither. In all three the
   containers start, the application serves, and every record Feat keeps stays
   correct. The user sees a healthy runtime that is not running their task.
8. **Fixed published ports prevent the thing the runtime is for.** The reference
   project publishes 8000, 5173 and 5432 at fixed numbers, so a second task's
   runtime cannot start, and its frontend reaches the API through a URL baked to
   one of those numbers. Testing one task's application while other agents work
   is the reason a per-task runtime exists, and ADR-034's decision to leave
   published ports exactly as configured is what stops it.
9. **Hot reload across a bind mount is sound.** With the source mounted rather
   than baked, and the virtualenv moved outside the mount, an edit on the host
   reached both `uvicorn --reload` and the Vite dev server in two seconds, over
   VirtioFS, with no polling. Reload is therefore a mechanism the product may
   rely on rather than a hope — which matters because an agent confined to a
   devcontainer has no Docker and cannot restart anything it changes.

Decisions:

- **A runtime is composed of its repositories.** The global
  `runtime.compose_files` list is replaced by a per-repository runtime
  contribution: the Compose files that repository brings, resolved relative to
  it, the container path its own services expect, and the services it asks Feat
  to manage. Feat generates the `include` document that joins them, with a
  `project_directory` per entry. The user stops hand-writing the file that
  composes their application, and evidence 2 stops being reachable — nothing
  relative ever crosses a repository boundary.
- **`container_path` splits in two.** `repositories.<id>.agent.container_path`
  is where the agent's devcontainer mounts the worktree;
  `repositories.<id>.runtime.container_path` is where that repository's own
  services expect their source. They are separate because evidence 5 shows they
  are separate questions, and under a compliance regime that requires
  devcontainer execution both always exist.
- **A build context is redirected like a mount.** For a managed service whose
  build context is a configured repository, the generated override sets
  `build.context` to that repository's task worktree. Measured: the override can
  do it, and a relative `dockerfile:` follows the new context. Where the code
  comes from is one question, and a mount and a build context are two answers to
  it; fixing one and not the other is evidence 4 preserved.
- **A managed service that is not running the task's code is a state, not a
  note.** It is resolved at create, from configuration, and shown on the task.
  The half that needs no Docker at all — a repository selected by a task, with a
  runtime configured, and no runtime container path — is refused at
  configuration load, because it cannot produce anything but evidence 1.
  ADR-034's post-start inspection stays as the check that catches what
  configuration cannot.
- **Feat allocates published ports, and tells a service where its siblings
  are.** **Corrected by this ADR's amendment of 2026-08-22, below: the address
  is host-scoped and is not how one service calls another, and the variables are
  now named `FEAT_HOST_URL_<SERVICE>` and `FEAT_HOST_PORT_<SERVICE>`.** Read the
  heading of this bullet as "and tells a service the host address of each
  reachable one"; every statement of the sibling reading elsewhere in the product
  was derived from the sentence as it was first written here.
  [08-v0-scope.md](08-v0-scope.md) excludes port-range allocation
  "unless required to make the reference project run", and evidence 8 is that
  condition being met, so this is the exclusion's own escape rather than a scope
  change. A repository declares which of its services are reachable; Feat
  allocates a host port per reachable service per task, records it, writes it
  into the generated override in place of the configured publication, and
  releases it on destroy. The resulting address reaches every managed service of
  the task as a generated non-secret variable — `FEAT_URL_<SERVICE>` and
  `FEAT_PORT_<SERVICE>`, normalised to upper case with non-alphanumerics
  replaced, and refused when two service names normalise alike. This
  **supersedes** ADR-034's rule that published ports are left exactly as
  configured. That rule's stated reason was that a port is how the user reaches
  their application and that v0 allocates none of its own; the second half has
  now changed, and the first is better served by an address Feat can tell the
  user than by a number that only one task can hold.
- **Configuration is derived and confirmed rather than transcribed.** Feat reads
  a project's own Compose files structurally: service keys, `volumes` targets,
  `build.context`, and published `ports`. It never resolves interpolation — an
  entry containing `${...}` is a value Feat could not derive, and the user is
  asked — and it never reads `environment:` values, `build.args`, or an
  `env_file`. This does not touch ADR-034 evidence 5, whose subject was
  `docker compose config` rendering environment-file values into its output;
  reading the document resolves nothing. The rule is not who reads but where a
  value comes to rest: a derived value becomes configuration only when the user
  accepts it into their own YAML, and nothing Feat inferred is persisted in
  Feat's own state. `internal/project/discover.go` already draws this line for
  service names and is extended rather than replaced.
- **The wizard asks in every execution mode, and `doctor` prints in every
  execution mode.** Evidence 6 is one decision applied to two commands. A
  project with a runtime is asked for its runtime container paths whether or not
  its agent is containerised, and the mapping table shows them whatever the mode,
  because it is the mapping that decides whether the user's services run their
  task.
- **The configuration interface breaks without a compatibility period.** Feat is
  used by its author and nobody else, so a version bump would buy ceremony. The
  old shape fails the strict unknown-field rejection that
  [07-configuration-model.md](07-configuration-model.md) already requires, and
  the message names the replacement rather than reporting an unknown key: a
  break the user has agreed to is still a break they should not have to diagnose.
- **What this does not decide.** It does not make an application
  hot-reloadable — that stays in the user's own Compose files, and Feat's
  contribution is saying which services will not reload. It does not deliver
  stable per-task hostnames: OQ-008 stays open, and the evidence recorded against
  it is that a proxy is a machine-wide resource with a lifetime no task owns,
  which is the `shared` lifecycle ADR-034 called roadmap work, and that a
  label-driven proxy wants the Docker socket that this product's headline denies
  the agent. Allocated ports are the first half of that feature rather than a
  detour from it, since a proxy must route to something. Nothing here addresses
  what several parallel application stacks cost a laptop.

Amended during slice 14, which implemented the first of the three.

Evidence 12, found by running `feat project init` against the reference project
after the rest of the slice was green: **the command meant to prevent evidence 1
produced it.** Compose-file discovery looked only for the four names Compose
itself defaults to, and both of the reference project's `docker-compose.dev.yml`
overlays — which carry the bind mounts a worktree replaces, the reset of a
published port, and, in the frontend, the only service anybody runs — were
invisible to it. What the wizard proposed was the base files alone: no runtime
container path for either repository, the frontend's static production build in
place of its dev server, and a database offered as reachable. The result loads,
starts, and runs the user's ordinary checkout in every service. Two further
defects compounded it: a file loop proposed the next candidate *and* claimed an
empty answer would finish, so pressing Enter added files rather than ending the
list; and the agent's Compose question proposed files found beside any
configured repository, which after this slice are overwhelmingly the
application's. Discovery now finds overlays, a loop proposes only its first
candidate, and the agent's question proposes nothing — it is asked before the
application section exists, so it has nothing to exclude with.

Two decisions the composition needed and this ADR had not made:

- **The Compose project directory is Feat's own generated directory.** It used
  to be the first configured file's, so that file's relative paths resolved as
  they do by hand — and with an include document, every entry carries the
  directory its own repository's paths resolve against, so a project directory
  belonging to one of the repositories could only be the directory a second
  repository's paths were wrongly resolved against. It is therefore the
  directory holding the generated documents, whose own paths are all absolute.
  One consequence is user-visible and is documented rather than left to be
  discovered: Compose's implicit `.env` lookup beside a repository no longer
  applies, so an environment file a project needs is named in
  `runtime.env_files`, and a relative path inside a `static_overrides` file
  resolves against Feat's directory rather than a repository's.
- **A worktree is mounted into the services that named the repository, not into
  every managed service.** The ADR says a repository's container path is where
  *its own services* expect their source; mounting every worktree into every
  service would additionally make two repositories that expect their source at
  the same path a collision, which is an ordinary arrangement between two
  applications rather than a mistake. A service may appear in more than one
  repository's `services`, which is how a service that runs an application and a
  shared library it depends on receives both, at their own container paths. The
  managed list Compose is asked for is the union.

Consequence: the configuration gains a per-repository runtime section and loses
a global one, the agent's and the runtime's container paths become separate
fields, and `domain.RuntimeEnvironment` gains the port allocations it must
release. `feat doctor`, `feat project init`, the JSON schema and the documented
example all move with it, because [07-configuration-model.md](07-configuration-model.md)
holds the last two to the implementation by test. The work exceeds slice 13's
outcome and is ordered as three slices, each of which ends with a product that
runs: the configuration shape, its validation, and the composition that consumes
it, because a slice that reshaped the configuration and left nothing reading it
would not build, let alone start a runtime; then build contexts and the
provenance state; then allocation and reachability. The first also rewrites the
project configurations on this machine, since `feat.yaml` is among those that
stop loading and it is how Feat's own tasks are run. It pushes the current slice
14 out by three.
The reference project's whole application then runs for one task, hot-reloading,
several times over, and the failure this ADR exists for stops being silent.

Amended during slice 15, which implemented the second of the three: the build
contexts and the provenance state.

Evidence 13, found while writing the reader against the service this slice
exists for: **the structural reader could not read the reference project's
frontend at all.** Its production service writes a plain `context: .` beside a
`build.args` entry carrying a "${...}" — a value Feat never reads and has no
business reading — and the reader judged the interpolation on the whole `build`
mapping, so the plainest build context in the project came back undecided.
Measured against the repository itself: the slice 14 reader answers
`builds-from-source=false` for `jobharbor-frontend` and lists its build context
as unread, while the fixed one resolves the context to the checkout. That service
is the multi-stage build ending in nginx of evidence 4, where the build context
is the only thing that decides what runs: a reader that could not see it could
not have redirected it, and `feat project init` would have proposed nothing about
it either. Interpolation is now judged on the context alone.

Evidence 14, found dogfooding this slice against the reference project: **a
managed service the project's files no longer define is reported as Feat's own
generated document being invalid.** Switching one repository's `compose_files` to
the file defining its production service while its `services` still named the
development one produced `service "web" has neither an image nor a build context
specified: invalid compose project` on every create, and the same message from
every poll afterwards. Both halves of it point away from the mistake: the
service is one the user named in their own configuration, and the document
without an image is the override Feat writes an entry into for every managed
service. Compose is right and unhelpful. The adapter now compares the managed
services against what `config --services` says the project defines — a list it
already had — and refuses before writing the override, naming both sides.
`feat doctor` reports the same mismatch per repository; this is that question
asked at the moment it is the reason nothing started.

Evidence 15, from the same run: **a mount does not make a baked service
current, and Feat said it did.** The frontend was configured with the production
file and a runtime container path left in place, so the service both mounted the
task's worktree at `/web` and built its image from it. The report that a change
needs a rebuild was suppressed, on the reasoning that a mounted worktree is
current the moment it is written — which is true of the API, whose server reloads
from `/app`, and false of an nginx image serving what its build produced and
never reading `/web` at all. Whether the mount is read is a fact about the image
that Feat cannot see. The report is therefore made whenever a service builds the
task's code, and says what the mount is still worth: current wherever the image
reads it. Suppressing it whenever a mount existed was true of exactly one of the
two services the reference project runs.

Three decisions this slice needed:

- **A build context inside a repository is that repository's.** The ADR said the
  context *is* a configured repository's checkout; a monorepo writes
  `build: ./web`, and the task's worktree holds the same subdirectory. The
  redirect therefore points at the same place inside the worktree, and a context
  above or beside the checkout is left alone. A bind source is still matched
  against the repository root exactly: a mount of a subdirectory is a partial
  mount that a whole worktree mounted at one container path would not replace,
  so it is not a candidate for a container path. Two questions, two rules.
- **The configuration-load refusal is asked of the runtime, not of each
  repository.** The plan wrote it as a task-eligible repository with no runtime
  container path. Implemented that way it refuses a project that is correct: the
  frontend of evidence 4 mounts nothing anywhere and wants no mount — pointing
  its build context at the worktree is what makes it run the task's code — and
  requiring a container path of its repository would demand a path its services
  do not use. Worse than pointless: a worktree mounted over an image's own
  `/app` hides the `node_modules` and the built output that were baked there, so
  the requirement would break the service it was meant to protect. What is
  refused is therefore evidence 1 exactly — a configured runtime, repositories a
  task selects, and no runtime container path anywhere, which can mount no task
  worktree at all — and the message names the repository and its services. Which
  particular service is not running the task's code is the provenance state's
  answer, resolved where the project's own Compose files can be read.
- **`create` builds again; `start` does not.** Redirecting a build context is
  half an answer while `up` reuses the image it made the first time: the service
  would run the copy of the worktree it was first built from, for the life of the
  task, and the agent that changed the code has no Docker to rebuild with
  (evidence 9). `create` therefore passes `--build`, which is the action that
  makes a service's image and the one a user asks for when they want it made
  again; `start` stays as it was, because a start is what a user asks for when
  they want their application up now, and Docker's cache makes the rebuild cheap
  when nothing changed. The report a baked service carries names that command.

Consequence beyond the plan's list: `runtime.Spec` gains the redirected build
contexts, `domain.RuntimeEnvironment` gains the per-service provenance, and the
three places that re-applied a task's recorded inputs to a freshly resolved
specification became one — which also drops a mount or a build naming a service
the task does not manage, so a project file that gains a service can no longer
refuse the stop or the destroy of a runtime created before it.

Amended during slice 16, which implemented the third of the three: the allocation
and the reachability.

Evidence 16, found by running three tasks of the reference project at once:
**a poll that started before a create finished gave the task's ports away.** The
runtime poller lists the tasks outside any lock and asks Docker about each in
turn, so a create that finishes while it is asking leaves it holding an answer
about the world as it was — nothing existed, therefore the runtime is absent.
Applied, that released the host ports the create had just allocated, while the
containers created with them were bound to those ports, and the next task was
given them. Measured as the second start of a task publishing nothing at all: the
generated override was rewritten with `ports: !reset []` on every service, and
the task the ports belonged to said its own reachable services were unreadable.
The state alone would have survived it, because the next poll corrects a state;
a released port is corrected by nothing. An observation is now applied only if
the record it was taken against has not changed since.

Decisions this slice needed:

- **Every published port is replaced, not only a managed service's.** The plan
  said to reset the publications of managed services the project did not declare
  reachable. Implemented that way it leaves a dependency's fixed port bound —
  which is ADR-034 evidence 12 exactly, and it is enough on its own to stop the
  second task. A published port is global to the machine as a container name is
  global to the Docker daemon, so it is treated the same way: reset everywhere,
  and replaced only where Feat allocated something. What that costs is a
  dependency a user reached at a fixed port by hand; what it buys is that
  concurrency is a property of Feat rather than of the user's diligence, and the
  remedy is one line of configuration — manage the service and declare it
  reachable.
- **The addresses reach the Compose process as well as the containers.** A
  service finds its siblings under the project's own names rather than Feat's.
  The reference project's frontend is a Vite dev server, which exposes only
  `VITE_`-prefixed variables to the browser, so `FEAT_URL_nginx` in the
  container's environment is a value nothing can use; what the project needs is
  to write `VITE_API_BASE_URL: ${FEAT_URL_NGINX}` in its own Compose file, and
  Compose interpolates that from the environment of the process running it. The
  generated variables are therefore passed to the Compose command as well as
  written into the override. They are generated task metadata either way, and
  nothing read from an environment file is in them.
- **The container port is read, and the host port is not.** A publication's
  target is a fact about the project's own Compose files, so Feat reads it
  structurally, in every syntax Compose accepts; the host port beside it is the
  thing an allocation replaces, so it is not read at all. What cannot be read —
  an interpolated entry, which resolving would mean reading the values Feat is
  forbidden to read, or a port range, which is several publications where an
  allocation is one — publishes nothing, and the task says which services those
  are. Refusing the action instead was considered and rejected: a project can
  reach that state by editing one file, and a runtime nobody can stop or destroy
  is a worse answer than a service that says it is unreachable.
- **The range has a default.** `runtime.port_range` defaults to `21000-21999`:
  above the privileged ports, below the ephemeral ones the kernel hands out, and
  a thousand wide. A required field would have broken every configuration written
  for slice 14, which collected the reachable declaration before anything
  allocated from it, and where a machine's own ports lie is exactly the kind of
  value a default should carry and `feat project show` should print.

Consequence: `domain.RuntimeEnvironment` gains the allocations it holds and
releases, beside the publications it observes — an intention and an observation
that can disagree, which is why they are two fields. `runtime.Invocation` gains
an environment, `feat runtime status` and the dashboard show the allocated
address rather than the observed publication, and the reference project's own
frontend names `${FEAT_URL_NGINX}` where it used to name a fixed port.

Three tasks of the reference project then ran their whole applications at once,
each frontend reaching its own task's API and no other's.

Amended after the round-2 review, settled 2026-08-22, correcting what the
generated address means and renaming the variables that carry it.

Evidence 17, `G4-08`: **the address this ADR said tells a service where its
siblings are does not reach a sibling.** The value written into every managed
service's `environment:` is `http://localhost:<allocated port>`, and a published
port belongs to the *host's* network namespace. Read inside a container that
address is the container's own loopback, so a service following the documented
pattern and calling `$FEAT_URL_api` connects to itself and gets a connection
refused; with the loopback `bind_address` Feat now publishes on by default, the
host's port is not reachable from a container at all. The value is right for the
one consumer that runs on the host — a browser opening a frontend, a shell
running `curl`, or a build baking an API address into a bundle a browser then
loads — which is the reference project's own case, and is why three tasks ran
their whole applications at once without this being caught. For a genuine
service-to-service call there is nothing to allocate: the Compose service name
and the container port are already in the project's own files and do not differ
per task.

Two decisions:

- **The generated address is host-scoped, and the documentation that said
  otherwise was derived from this ADR.** The corrected statement lives in
  [07-configuration-model.md](07-configuration-model.md) § Runtime ownership,
  which is where a user reads it. The bullet above is marked rather than
  rewritten, because it is the sentence four other statements were copied from
  and a reader who lands on it has to be turned around there.
- **The variables are renamed `FEAT_URL_` → `FEAT_HOST_URL_` and `FEAT_PORT_` →
  `FEAT_HOST_PORT_`.** The mistake is made while typing a variable into a
  service's `environment:`, and the name is the only thing present at that
  moment — the paragraph explaining it is not. A prefix that says `HOST` refuses
  the sibling reading where the reading happens; a document only corrects a
  reader who is already looking somewhere else. It breaks the configuration
  surface with no compatibility period, on the same calculus as `G5-01`'s format
  break and this ADR's own: one user, pre-alpha, and a migration for a
  population of one is waste. A project's own Compose file that interpolates
  `${FEAT_URL_NGINX}` — including the reference project's frontend, named in the
  slice 16 amendment above — names the new variable instead, and Compose
  interpolates an unset one to empty, so the break is visible on the next start
  rather than silent.

Consequence: `internal/domain/runtime.go` constructs both prefixes and is the
only place that does, so the rename is one edit and a set of assertions that
follow it; the three override goldens carry the new names and their environment
blocks re-sort. Nothing persisted and nothing on the wire carries a variable
name — `api.PortAllocation` carries the address itself — so an existing task's
generated override is simply rewritten with the new names the next time its
services are created or started. A container already running keeps the old names
in its environment until it is recreated, which is the ordinary consequence of
editing an environment and is what `feat runtime create` does.

### ADR-066 — What a container grants and no rule refuses is said out loud

Status: accepted
Recorded: round-2 review, batch 8, from `G7-05` and the half of `G7-04` the
maintainer's acceptance does not cover

The launch inspection had two answers about a container — accepted, or refused
with a reason — and a security profile has a third kind of fact in it: something
a project is entitled to configure and the next person is not entitled to be
surprised by. Both findings below are that third kind, and both had been written
down as if they were one of the first two.

Evidence:

1. **The non-root answer is about an instant.** `Inspect` runs `id -u` as the
   configured user and `Check` refuses uid 0, which is what the requirement in
   [05-security-model.md](05-security-model.md) § Dogfood security profile says.
   `.devcontainer/Dockerfile` writes `dev ALL=(ALL) NOPASSWD:ALL`, so on the
   machine this alpha is being built on the check reads `dev`, passes, and the
   agent is uid 0 one command later. Nothing anywhere probed for a way back.
2. **What that reaches, and what it does not.** Measured against Docker 29.5.2
   rather than reasoned: `CAP_DAC_OVERRIDE` is in the default capability set, so
   root inside the container writes host-owned files through every writable bind
   mount and the ordinary uid-mismatch failure `probe.go` diagnoses stops
   applying — which is most of what the non-root requirement was written for.
   `CAP_SYS_ADMIN` is not in the default set, so the read-only control workspace
   holds. The residual guarantee of the round-1 control-workspace fix is intact,
   by the kernel's rule rather than by any rule of Feat's, and Feat refuses an
   added `CAP_SYS_ADMIN` separately (ADR-033's grant check).
3. **Real `sudo` is what decides the answer, so it is asked of real sudo.**
   `sudo -n true` exits zero under a `NOPASSWD` rule and non-zero under one that
   asks for a password; `-n` is what turns a prompt nobody can answer into a
   refusal. Both arms run against a real container in the opt-in suite, on one
   container and two sudoers files, because the rule is the only difference
   between them.
4. **A writable mount of the host's Claude configuration is not an exposure.**
   § Claude authentication described mounting `~/.claude` as something that "may
   expose global settings, approvals, and plaintext session data", which is
   accurate for a read-only mount and wrong for a writable one. `settings.json`
   holds `hooks` and `.claude.json` holds `mcpServers`: both are commands, and
   they are run on the *host*, as the user, by their own Claude Code the next
   time they open it. The same directory holds the approvals record that would
   otherwise be asked first. A user can therefore reason their way into a
   writable mount believing they accepted a disclosure risk and have accepted
   deferred host execution.

Decisions:

- **Probed, reported, not refused.** Whether an agent may become root inside its
  own container is the project's decision — a session that installs a package is
  the ordinary reason — and a refusal would be answered by whichever edit made
  the check stop looking. What Feat can do honestly is say what it found, at
  every launch, so the grant is deliberate rather than inherited from a template.
- **A list of tools rather than a check for `sudo`.** `EscalationTools` is the
  shape `ContainerClients` already has, and for the same reason: `sudo` is not
  the only binary that hands back privilege and this image is not the only
  image. Adding one is a line.
- **Presence is not a grant.** Only a tool that ran and exited zero counts. An
  image carrying `sudo` whose sudoers file grants the agent nothing is the
  requirement holding, demonstrated rather than assumed, and warning about it
  would be a warning on every image built from a distribution base — which
  teaches a user to skip the one that means something.
- **`Warnings` sits beside `Check` on the execution interface.** The report is
  evidence, `Check` is judgement, and this is disclosure; folding it into either
  would make it a refusal nobody wanted or a fact nobody reads. It is the first
  thing the launch inspection says about a container it is going ahead with.
- **It reaches the daemon log and the task's own event log.** The log belongs to
  whoever started the daemon; the question — did I mean to give the agent that? —
  belongs to whoever is running the task, so it is recorded against the task
  where the answer is durable and per task.
- **`feat doctor` asks it too, where it already reads the uid.** `feat doctor`
  is where a user asks this question deliberately, and it reported
  `agent.execution.user` as a green line meaning "uid 1000" — which is the shape
  `F6-08` records one check away, a security property stated as verified where
  the claim replaces the reader's own review. It is a separate implementation
  because ADR-064 requires one: doctor runs in the process the user is in front
  of and reaches no daemon. It shares `EscalationTools`, so the two surfaces
  cannot drift about what is asked, and the green line now says what it
  established rather than restating the uid.
- **§ Claude authentication distinguishes the two mounts, and the dogfood image
  states its own grant.** The documentation says what a writable mount is rather
  than how large a disclosure it is, and `.devcontainer/Dockerfile` says why its
  `NOPASSWD` line is there and what it costs. `G7-04` — the maintainer's own
  read-write `~/.claude` mount — stays as an accepted risk of 2026-08-19, and is
  a decision about one machine rather than about what the product tells anybody
  else. The alternative it should be revisited against, `agent.claude.config_volume`,
  already exists and is already the documented recommendation.

What this does not claim: a sudoers rule narrow enough to exclude `true` is not
found this way, and Feat never reads a sudoers file. Setuid binaries, a writable
Docker group, and a container escape are all outside it. The warning says what
was established, which is why it names the tool that answered rather than
describing the container's security posture.

Consequence: `execution.Report` carries `Escalation`, `execution.Environment`
gains `Warnings`, `planContainerAgent` logs and records what it returns, and
`checkContainerUser` asks the same question of a live container.
[05-security-model.md](05-security-model.md) gains § What "non-root" is a
statement about, under the profile whose requirement it qualifies, and rewrites
§ Claude authentication around the read-only/read-write distinction.

### ADR-067 — The confinement the other rules stand on is checked, and a policy Feat cannot read is disclosed

Status: accepted
Recorded: round-2 review, batch 2's outstanding half, from the last field of
`G4-04`'s own claim

Batch 2 closed six of the seven fields `G4-04` named. `security_opt` was decoded
nowhere, so `seccomp=unconfined`, `apparmor=unconfined`, and
`systempaths=unconfined` passed a check that refuses a home-directory mount.

The distinction that decides this ADR is what those entries are. Every other rule
in `inspect.go` compares a name a project wrote — a path, a capability, a
namespace, an environment entry — and each of them is worth what its enforcement
is worth. `cap_add: [SYS_ADMIN]` is refused because `CAP_SYS_ADMIN` is `mount(2)`;
the default syscall filter is what stops a process obtaining the same capability
without asking Docker for it. `security_opt` is where that enforcement is
switched off, which is why it belongs with the rules rather than beside them —
and ADR-066's residual guarantee, the read-only control workspace holding by the
kernel's rule, is the thing it stands under.

Evidence, measured on Docker 29.5.2 and Compose v5.1.4 on 2026-08-22 rather than
reasoned about, because each of the first three decides the shape of the rule and
not only its wording:

1. **`systempaths=unconfined` is reported nowhere.** A container given it —
   through `docker run` and through Compose alike — reports
   `.HostConfig.SecurityOpt` *without* the entry, and reports
   `.HostConfig.MaskedPaths` and `.HostConfig.ReadonlyPaths` as `[]`. An
   ordinary container reports twelve masked paths and five read-only ones. The
   daemon spends the option at create time rather than recording it. So the fix
   as the finding states it — decode `SecurityOpt` — would have refused two of
   the three and stayed blind to the third, which is the one the finding calls
   the least ambiguous: unmasked `/proc/kcore` is this host's physical memory and
   a writable `/proc/sysrq-trigger` reboots it, neither needing a capability any
   rule reads or a mount any rule sees.
2. **A privileged container reports both lists as `null` and carries a
   `label=disable` nobody wrote.** Docker adds the label option itself. So the
   unmasked-paths rule must not fire for a privileged container — whose refusal
   already names `privileged: true`, the line that produced it — and `label=…`
   is not reliably a line a project can be sent to look for.
3. **Both separators reach the daemon.** Compose passes `seccomp:unconfined`
   through exactly as it passes `seccomp=unconfined`. A rule split on `=` alone
   would be a deny-list with a spelling as its bypass, which is what
   `capabilityName` exists to prevent one field over.
4. **A custom profile arrives whole.** `seccomp=./profile.json` is reported as
   `seccomp={"defaultAction":"SCMP_ACT_ALLOW"}`: the client sends the contents,
   not the path. The value of a `security_opt` entry is therefore sometimes a
   policy hundreds of lines long, and never something a message prints.
5. **A permissive profile is `unconfined` under another name.** That four-line
   profile allows every syscall. Whatever the rule refuses by name a project can
   express by file, and telling it from a stricter-than-default profile means
   interpreting a syscall list.
6. **The set of options is closed by the daemon and still open to Feat.** An
   unknown entry is refused at create time (`invalid --security-opt`), so what
   reaches a container is what Docker knows — and that grows: `writable-cgroups`
   is accepted by 29.5.2 and appears in no list this repository could have
   written before today.

Decisions:

- **Refuse what switches a layer off; report what replaces one.** The three
  `unconfined` forms are refused, because "unconfined" is Docker's own word for
  no policy and there is nothing to evaluate. A profile is reported, because
  evaluating it is the one thing these checks do not do.
- **`systempaths` is found by its effect and named by its cause.** The rule reads
  `MaskedPaths` and `ReadonlyPaths`, both empty and not privileged; the message
  states what was observed before it names `security_opt: systempaths=unconfined`
  as the line that produces it. A reader on a runtime that reports the lists
  differently is then told something true rather than sent to a line that is not
  there.
- **`label=…` is reported rather than refused, and the asymmetry with
  `apparmor=unconfined` is deliberate.** `docker-default` is loaded for every
  container and costs a project nothing to keep, so removing that line is a
  remedy anyone can act on. `label=disable` is not the same: on a host with
  SELinux enforcing, Feat generates its bind mounts with no `:z` and offers no
  configuration key for one, so that entry may be what makes Feat's *own* mounts
  work there. A refusal whose remedy the product does not offer is a refusal
  nobody can act on, and evidence 2 adds that Docker writes the entry itself.
- **`no-new-privileges` is silent.** It only tightens. A warning about a
  container doing better than the default is one people learn to skip, which is
  the reasoning ADR-066 applies to an image that merely carries `sudo`.
- **Anything else is reported.** Evidence 6 says the list Feat holds will be
  incomplete before Feat notices. An entry nobody recognised is exactly the case
  where silence would be the wrong answer, and it costs one line.
- **The evidence type carries both halves and judges neither.**
  `ObservedPrivileges` gains the options split into name and value, and the two
  path lists verbatim; `CheckPrivileges` decides. It is the split the rest of
  the report has, and it is what lets the same reading serve a refusal and a
  disclosure.
- **A value is never printed unless it is a word.** `SecurityOption.Describe`
  renders `label=disable` and renders a profile as `<a policy Feat did not
  evaluate>`. A warning whose subject is that Feat did not read a policy cannot
  be a warning that prints it, and `Endpoints` already returns names and never
  values for the same reason.
- **The fake describes a container rather than the queries the product makes.**
  `composetest` carries Docker's own masked and read-only lists verbatim and
  owns the `.HostConfig` defaults both fixtures build on, so the healthy
  container and a container granted one thing cannot disagree about the fields
  neither of them names. That is `G6-17` applied to the field this fix added.

What this does not claim: this is not an evaluation of anybody's security policy,
and a permissive custom profile passes with a warning — evidence 5 is the honest
limit of a rule that compares names. Feat does not read the host's AppArmor or
SELinux policy and does not know whether the host enforces either;
`apparmor=unconfined` is refused on macOS too, where it does nothing, because a
Compose file is read on every machine it is opened on. Nothing here is a defence
against a kernel or runtime exploit — § Container limitation still governs — and
the unmasked-paths consequences are consequences for a root process in the
container, which ADR-066 reports separately and does not refuse.

Consequence: `execution.ObservedPrivileges` carries `SecurityOptions`,
`MaskedPaths`, and `ReadOnlyPaths`; `execution.SecurityOption` is added with
`Describe`; the Compose adapter gains `ConfinementLayers`, `SystemPathsOption`,
and `UnevaluatedOptions`, and `Warnings` gains its second entry.
[05-security-model.md](05-security-model.md) § Dogfood security profile requires
the runtime's confinement to be left on, and § What "non-root" is a statement
about says what stands behind the kernel's rule it already cites.

## Decision change process

During implementation:

1. Record new evidence.
2. Identify affected requirements and milestones.
3. Add or amend an ADR.
4. Update linked specification files in the same change.
5. Do not silently let implementation behavior become the specification.
