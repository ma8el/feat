# Technical Architecture

## Stack

- Language: Go
- CLI: Cobra
- TUI: Bubble Tea with Bubbles/Lip Gloss where useful
- Local API: HTTP/JSON over a Unix-domain socket
- Event delivery: Server-Sent Events
- Future terminal streaming: WebSocket endpoint, not required in v0
- State: versioned files behind a storage interface
- Configuration: YAML with strict schema validation
- Execution backend: tmux
- Git integration: Git CLI
- Runtime integration: Docker Compose CLI
- Initial agent adapter: Claude Code
- License: Apache 2.0

## Process model

One binary exposes several modes:

```text
feat                         TUI client
feat daemon start|stop|status
feat implement               task creation client
feat project ...             project client
feat task ...                task client: list, attach, review, cleanup
feat runtime ...             runtime client
feat doctor                  diagnostics
```

Every command that acts on an existing task is a subcommand of `feat task`, and
`feat implement` is at the top level because it produces a task rather than
taking one. `feat attach` and `feat review` are hidden top-level aliases holding
the same implementation as the commands they stand for. See ADR-040.

Opening the TUI checks the local Unix socket and starts the daemon in the background if absent. Explicit daemon commands remain available. launchd/systemd installation is later work.

The daemon is the only persistent-state writer and owns reconciliation, orchestration, and event publication. The TUI and CLI are clients.

## Component boundaries

Suggested Go packages:

```text
cmd/feat                     process entrypoint
internal/cli                 Cobra command tree and exit codes
internal/domain              entities, states, transitions, errors
internal/config              YAML loading, merging, validation
internal/paths               configuration/state/runtime directory resolution
internal/store               storage interfaces
internal/store/fs            JSON/JSONL/Markdown implementation
internal/store/storetest     deterministic fixtures, test support only
internal/daemon              orchestration services and lifecycle
internal/api                 HTTP handlers, DTOs, SSE
internal/client              Unix-socket API client
internal/project             registration and diagnostics
internal/wizard              the questions that compose a project configuration
internal/git                 Git and worktree adapter
internal/tmux                tmux execution adapter
internal/agent               agent interfaces and events
internal/agent/claude        Claude Code launch/hooks/control protocol
internal/execution           execution environment interface
internal/execution/host      host-native execution
internal/execution/compose   devcontainer execution through Compose
internal/runtime             application runtime interface
internal/runtime/compose     Docker Compose runtime
internal/control             task inbox/outbox protocol
internal/review              base comparisons and external commands
internal/tracker             the configured ticket command and its output
internal/resources           host/task resource observation
internal/notify              desktop/TUI notification policy
internal/reconcile           startup discovery and repair proposals
internal/ui                  Bubble Tea models and views
internal/version             build identity
internal/guard               repository-wide invariant tests, no runtime code
```

Adapters are compiled into the binary initially. Interfaces must avoid leaking implementation types so a future external plugin protocol remains possible.

`internal/cli`, `internal/paths`, `internal/version`, and `internal/guard` were added to the layout above; see ADR-025. `internal/store/storetest` was added with the store; see ADR-026. `internal/paths` owns directory resolution and nothing else, and storage is denied to `internal/api` and `internal/client`; see ADR-027. Import boundaries between these packages are enforced by `depguard` rules in `.golangci.yml`.

`internal/git` is denied `internal/config` and `internal/store`: it works on domain types and final names, the daemon expands templates, and the daemon records what it creates. See ADR-029. `internal/paths` owns the shared-directory list, so configuration validation and the adapter that creates and removes directories ask one question of one list.

`internal/agent`, `internal/agent/claude`, and `internal/control` sit under the boundary ADR-029 established for Git: the adapters receive final values and read neither configuration nor persistent state, and `agent-stays-an-adapter` and `control-stays-a-protocol` `depguard` rules make that mechanical. The provider adapter returns a launch specification expressed in the terms the agent's own environment uses, and the daemon supplies how the agent sees its worktrees and control directory — host paths under host-native execution, container paths under a devcontainer. That is the seam through which an execution environment is replaced without touching the provider adapter. See ADR-032.

`internal/runtime` and `internal/runtime/compose` sit under the same rule, with a `runtime-stays-an-adapter` `depguard` rule that also denies them `internal/execution`. `internal/paths` owns the runtime root. See ADR-034.

`internal/execution` and `internal/execution/compose` sit under the same rule, with an `execution-stays-an-adapter` `depguard` rule. The daemon resolves configuration into an execution specification, wraps the environment's probe runner as the agent adapter's runner, and turns the adapter's launch specification into a host command the terminal backend runs. The two adapters therefore never import each other, and host-native execution is added behind the same interface without touching either. See ADR-033.

`internal/review` sits under the same rule, with a
`review-stays-a-policy` `depguard` rule. It receives final values — an expanded
argument vector, the task's own worktree paths, a resolved check — and reads
neither configuration nor persistent state. It is denied `internal/git` as well,
because a change summary is Git's own answer and belongs to its adapter. See
ADR-036.

`internal/tracker` sits under the same rule, with a `tracker-stays-an-adapter`
`depguard` rule. It receives a resolved command and returns the tickets it
printed, validated against the shape Feat publishes; the caller resolves the
project's tracker section, and the daemon records what becomes of a ticket. There
is no adapter per service, because a tracker CLI already prints JSON and holds
its own credential: what a native adapter would add is the mapping, and the
mapping belongs to the user's command. See ADR-071.

`internal/resources` and `internal/notify` sit under the
same rule, with `resources-stays-an-adapter` and `notify-stays-a-policy`
`depguard` rules. The observer receives process identifiers, a label selector,
and a path; the notification policy receives a resolved `Policy` and a `Subject`
carrying a task's key, title, and project, and can reach nothing else. See
ADR-035.

`internal/wizard` holds the sequence of questions that composes a project configuration: which question comes next, what it proposes, and whether an answer is acceptable. It reaches nothing — what it needs to know about the machine it asks through a `Host` that `internal/cli` implements over `internal/project` — so both askers can drive it: `feat project init` as a line conversation, and the dashboard as a dialog. See ADR-063.

`internal/config` and `internal/project` have one boundary drawn between them: `internal/config` decides whether a configuration is well formed and safe, and asks the host nothing; `internal/project` asks the host and reports what it found. A configuration therefore stays loadable on a machine where a repository is temporarily missing, which is the machine `feat doctor` is most useful on. See ADR-028.

## Local API

The API is versioned from the beginning but is not promised stable before v1.

Minimum endpoints:

```text
GET    /v1/health
GET    /v1/events                         SSE
GET    /v1/resources                      the most recent resource sample
GET    /v1/projects
POST   /v1/projects                       registers a project by identifier
GET    /v1/projects/{project_id}
POST   /v1/projects/{project_id}/doctor   deferred; see ADR-028
GET    /v1/tasks
POST   /v1/task-drafts
PUT    /v1/task-drafts/{draft_id}
POST   /v1/task-drafts/{draft_id}/plan     resolves bases and proposes branches
POST   /v1/task-drafts/{draft_id}/launch   confirms a displayed plan
DELETE /v1/task-drafts/{draft_id}          cancels a draft
GET    /v1/tasks/{task_id}
POST   /v1/tasks/{task_id}/attach-info
POST   /v1/tasks/{task_id}/shell
POST   /v1/tasks/{task_id}/runtime/create
POST   /v1/tasks/{task_id}/runtime/start
POST   /v1/tasks/{task_id}/runtime/stop
POST   /v1/tasks/{task_id}/runtime/status
POST   /v1/tasks/{task_id}/runtime/destroy
POST   /v1/tasks/{task_id}/runtime/logs-info
POST   /v1/tasks/{task_id}/review/observe
POST   /v1/tasks/{task_id}/review/approve
POST   /v1/tasks/{task_id}/review/changes
POST   /v1/tasks/{task_id}/review/pending
POST   /v1/tasks/{task_id}/review/verify
POST   /v1/tasks/{task_id}/cleanup/plan
POST   /v1/tasks/{task_id}/cleanup/execute
POST   /v1/tasks/{task_id}/resume            continues the recorded agent session
POST   /v1/tasks/{task_id}/stop              stops the environment that session runs in
GET    /v1/reconciliation                    the most recent recovery pass
POST   /v1/reconciliation                    looks again and records what it saw
```

The local socket is user-owned and mode-restricted. Destructive API requests use task/resource IDs and a server-produced cleanup plan token rather than arbitrary filesystem paths. `POST /v1/projects` follows the same rule for the same reason: it carries a project identifier, and the daemon resolves the configuration file from the directory it resolved for itself, so the file that is validated is the file it will read again later.

Task endpoints address a task by its identifier alone, as the command surface does. The daemon resolves the owning project, which storage addresses explicitly; see ADR-026 and ADR-027.

`{task_id}` and `{draft_id}` accept a whole task identifier, the eight-character key derived from it, or any prefix of the identifier. A whole identifier is used as it stands, because it names one task by construction; anything shorter is resolved against every registered project, since a key is unique within a project rather than across the machine. A reference that names more than one task is answered with `400` and both candidates rather than resolved to either, and one that names none is answered with `404`. Both name where a valid value is printed. See ADR-038.

A draft is a task in `draft` state, so `{draft_id}` is a task identifier and a draft appears in `GET /v1/tasks` as the draft it is. Preparation is three requests rather than two: resolving fetches, so it follows a key the user pressed rather than a field they edited, and launching carries the fingerprint of the plan that was displayed so that what is created is what the user read. A draft that changed in between is refused rather than re-resolved. Cancelling archives the record. See ADR-031.

`POST /v1/tasks/{task_id}/shell` carries an identifier and nothing to execute; the daemon builds the command, for the reason destructive requests carry resource identifiers rather than paths.

`resume` and `stop` are the two verbs of an agent environment's lifecycle, and there is deliberately no third: a launch creates one, a resume brings one back, and cleanup removes it, so no endpoint produces a container that no session owns. Both, and the draft launch, are bounded by `api.AgentTimeout` rather than by an ordinary request's budget — the daemon stops waiting for Docker at it and the client waits for it plus a margin, because a client that gave up first would cancel a request the daemon is still serving and leave behind a container the answer never named. See ADR-057.

The runtime endpoints are one per manual action, because each is a separate
thing a user asks for and the path is what names it: an action Feat does not
perform is an endpoint that does not exist, rather than a request the daemon has
to interpret. `create`, `status`, and `destroy` are recorded in ADR-034. `status` is a POST because it observes and records what it observed.
`destroy` is the only one carrying a body, and what it carries is the user's
confirmation; every other action takes none, for the reason the shell endpoint
does. `logs-info` returns a command rather than output — Feat does not aggregate
or persist logs (FR-RUN-006) — and the client checks the program it was handed
before running it.

The review endpoints follow the runtime's rule: one per action a user asks for,
because the path is what names it. `observe` compares every repository against
its own recorded base and records what it found, which is why it is a POST;
`verify` runs the project's configured checks, which is also how a gate
interrupted by a restart is run again. None of them carries a body — the
decision is in the path, and the external commands the response returns are the
project's own, expanded by the daemon and run by the client (ADR-036).

`GET /v1/resources` returns the most recent sample rather than taking one. A
sample is not persisted and is not part of a task's record, so it has its own
document, its own collection time, and its own failure mode; and asking the
container runtime what it is using costs one to two seconds, so a request that
collected would be a request a metric could stall. Every figure nothing measured
is published as null rather than as zero. Nothing published on the event stream
reports a sample: a figure that moves every two seconds would make every client
re-read every two seconds (ADR-035).

SSE events carry domain state changes, not raw terminal streams or secrets. Subscribers have bounded queues: publication never blocks the daemon, and a subscriber that falls behind receives a terminal event and is disconnected rather than silently losing events. Stream resume is not supported in v0.1, so a reconnecting client re-reads current state.

## File-backed storage

Configuration directory:

```text
~/.config/feat/projects/<project-id>.yaml
```

State directory:

```text
~/.local/share/feat/
  logs/daemon.log
  logs/daemon.log.<generation>
  projects/<project-id>/project.json
  projects/<project-id>/tasks/<task-id>/task.json
  projects/<project-id>/tasks/<task-id>/prompt.md
  projects/<project-id>/tasks/<task-id>/events.jsonl
  projects/<project-id>/tasks/<task-id>/review.json
  execution/<project-id>/<task-id>/compose.override.yaml
  runtime/<project-id>/<task-id>/compose.override.yaml
  control/<project-id>/<task-id>/
```

The daemon log is bounded. Nothing prunes it otherwise and the daemon appends
for as long as it runs, so its size would be decided by uptime rather than by
activity. It is rotated once it reaches a fixed size, a fixed number of numbered
generations is kept beside it, and a log already past the bound when it is opened
is cut down to its most recent records rather than carried over whole. Rotation
copies and truncates rather than renaming, because `feat daemon start` opens the
log itself and hands that descriptor to the process it spawns as standard output
and standard error: a rename would leave the spawned daemon's own output going to
the rotated file. See ADR-069.

The generated execution override sits beside the control workspace rather than
inside it, and outside the per-task snapshot directory. It decides what the
agent's own container mounts, so it must be somewhere the agent never sees; and
the snapshot directory holds the documents the storage interface owns, which a
file written by an execution adapter is not.

The generated application-runtime override sits beside it under its own root and
never inside it. The two adapters are separate concepts even where both drive
Compose: one decides what the agent's container reaches, the other what the
application under development runs. Both are host-only and neither is mounted
anywhere (ADR-034).

The task control workspace is under the state directory but outside the
per-task snapshot directory, because it is the one tree an agent writes to and
it is mounted into the agent's execution environment. Its layout is in the
control workspace protocol below.

A durable daemon record, `daemon.json`, sits at the state root. It records the
state directory's own schema version, whether the previous run ended cleanly, and
when it stopped. It carries no process identifier, socket path, or lock: those
live in the runtime directory, which does not survive a reboot, and a durable
copy of one would describe a daemon that is not running. Its three readers are
the reason it exists rather than the deferral that scheduled it — a build older
than the directory refuses it instead of overwriting documents it cannot read, a
recovery report can say a daemon crashed rather than leaving a user to infer it,
and the stop time is how long Feat was not looking. See ADR-027 and ADR-037.

Runtime ownership data uses the operating system's user runtime directory:

```text
$XDG_RUNTIME_DIR/feat/     or $TMPDIR/feat-<uid>, or /tmp/feat-<uid>
  feat.sock
  daemon.lock
  endpoint.json
```

The directory resolves in that order and is owner-only. A candidate that is a symlink, is owned by another user, or is writable by others fails with an actionable message rather than moving to the next candidate, because two locations would mean two daemons each believing it owns the machine. `endpoint.json` records the running daemon's process identifier, socket path, build version, and start time, and `daemon.lock` carries the advisory lock that makes that record's liveness verifiable. `FEAT_RUNTIME_DIR` moves all three together; the resolved socket path is checked against the platform's socket-path length limit, which is 104 bytes on macOS and 108 on Linux.

The daemon serialises one task's records with a per-task lock. Atomic writes
make each write whole and do not make a load-change-save cycle safe against
another one: the completion gate runs in the background for as long as a test
suite takes, and everything that changes a task while it runs — a control
message, an idle timer, a review action, a runtime action — takes that lock and
re-reads what it locked (ADR-036).

Storage rules:

- every schema has a version;
- snapshots are written to a temporary file, fsynced where appropriate, and atomically renamed;
- the document a migration replaces is retained as `<file>.v<version>.bak`;
- the daemon serializes writes;
- events append in order and carry a log-assigned sequence number;
- recovery ignores only an incomplete final JSONL record, and the next append discards it;
- derived resource samples are not persisted continuously;
- a storage repository interface allows a later SQLite backend.

The task brief is stored as Markdown in `prompt.md` rather than inside the snapshot, and is written before it, so an interrupted save never leaves a snapshot referring to a brief that was never written.

## Project configuration merge

Two levels are supported architecturally:

1. local project topology under `~/.config/feat`;
2. optional repository `.feat.yaml` conventions.

v0.1 may implement local configuration only. When both exist later, local topology supplies machine paths and credentials references while repository configuration supplies shareable branch/check/runtime conventions. Secrets are not stored in either generated state or examples.

## Git adapter

Responsibilities:

- validate repository and remote;
- fetch without mutating the ordinary checkout;
- resolve base ref to immutable commit;
- detect branch, path, and worktree collisions;
- create read-write and read-only worktrees;
- observe dirty/ahead/behind/merged state;
- compute change summaries against recorded bases;
- produce exact cleanup plans;
- remove worktrees/branches only after confirmation, re-checking the path against
  the directory Feat owns immediately before deleting anything;
- remove the generated directories a task was given, once they hold nothing, and
  never the directory its project keeps.

The adapter invokes Git as an argument vector, not through interpolated shell strings.

The v0 worktree path default should be deterministic under Feat's data/workspace directory and configurable. Worktree metadata sharing is documented in the security model.

Branch and worktree names are generated by the daemon rather than by the adapter, because the placeholder vocabulary belongs to `internal/config`, which validates it. The adapter receives final names and paths, so a future Git backend inherits no dependency on the configuration format. See ADR-029.

Task preparation is three steps, and the order is a requirement rather than an implementation detail:

1. plan — resolve every base to an immutable commit, propose every branch and worktree path, and report every collision, creating nothing;
2. record — the daemon writes the plan onto the task, which stays a `draft` until the user confirms it;
3. apply — on confirmation, leave `draft` and create the worktrees and branches one repository at a time, recording an observation of each before the next begins.

Confirmation carries a fingerprint of the recorded plan, so a draft that changed after it was displayed is refused rather than applied; see ADR-031.

Every resource that can exist afterwards is written down before it is created, so an interruption leaves a record naming a superset of what exists. A failure part way through leaves what was created in place and the task `failed`; Feat does not undo a partial launch.

Whether a worktree exists is observed, not stored: the recorded branch and path are desired state, and a `GitObservation` is what Feat saw. Reconciliation asks Git rather than trusting a stored flag.

A task worktree path must be absolute, clean, strictly inside the fixed leading directory of the configured worktree root, outside every repository checkout, and never a shared system directory. The check resolves symbolic links as far as the path exists, and cleanup applies the same check to a recorded path, refusing a target rather than removing it.

A worktree root generates three kinds of directory, and each has a different lifetime. Its fixed leading prefix is the directory a user allowed Feat to write under, shared by every project on the machine. Between that and a worktree, a root such as `…/worktrees/{project_id}/{task_id}` gives each project a directory of its own — `config.ProjectPrefix` resolves it — which Feat creates for the project's first task and every later task is created inside; a project with no task right now still has one. The rest is the task's, created by the adapter before Git runs.

Creating those creates them, so removing the task removes them. A removal walks up from the worktree, removing each directory that is now empty, and stops at the project's own directory, at the fixed prefix under it, and at the first directory that still holds anything. A symbolic link is stepped over rather than followed. Each directory that goes is reported with the worktree, and one that cannot be removed is left rather than forced: the worktree is already gone, and reconciliation names what is left. See ADR-037.

## tmux adapter

Feat uses a dedicated named tmux server/socket. The default topology is:

```text
server: Feat-owned
session: project
window: task
pane 0: native agent
pane 1: optional task shell
```

Requirements:

- permit tmux to load the user's normal configuration when compatible;
- tag sessions/windows/panes using tmux user options such as task/project IDs;
- never rely on numeric indexes as stable identifiers;
- attach/detach through normal tmux behavior;
- launch shell panes in the same execution profile and primary workspace;
- inspect process existence without interpreting semantic completion from terminal text;
- reconcile existing managed sessions on daemon startup;
- quarantine a tagged object it cannot read, returning the rest. Discovery
  reports what it could read together with what it could not, and fails as a
  whole only when the enumeration itself failed, so one damaged terminal never
  makes the healthy ones unreachable (ADR-037).

Every control invocation passes `tmux -u`. A tmux client whose locale is not
UTF-8 replaces every non-printable character in the output of `-F` with an
underscore, and every format Feat asks for is tab-separated — so without it a
daemon started by a service manager, which has no locale, cannot parse the
identifiers of a terminal it just created and discovers nothing. Interactive
attachment does not pass it: there the client is the user's own terminal
(ADR-036).

The tmux adapter and the execution-environment adapters are separate boundaries.
Execution adapters construct an argument vector and working directory for a
host or devcontainer command; tmux keeps that command attached to a persistent
terminal. Neither implements the other. See ADR-030.

## Agent adapter contract

Conceptual interface:

```go
type AgentAdapter interface {
    Validate(ctx context.Context, env ExecutionEnvironment) error
    Prepare(ctx context.Context, task Task, control ControlWorkspace) (LaunchSpec, error)
    ParseEvent(ctx context.Context, raw ControlEvent) (AgentEvent, error)
    Reconcile(ctx context.Context, session AgentSession) (ObservedAgentState, error)
}
```

The Claude adapter:

- generates task-specific settings/instructions outside the repositories;
- allows checked-in `CLAUDE.md` and project settings to continue applying;
- launches the native interactive CLI;
- installs supported provider hooks for session, prompt, stop/idle, task completion, and failure events;
- writes normalized events to the control outbox;
- does not treat a normal Stop event as task completion;
- tells the agent whether a completion gate will answer its review requests, and
  how it learns the verdict;
- supports direct `gh`/`glab` usage when configured and authenticated.

Implementation must verify exact supported Claude CLI flags and hook schemas against the installed/supported Claude Code version. Provider-specific flags must remain inside the adapter.

## Execution environments

Conceptual interface:

```go
type ExecutionEnvironment interface {
    Validate(ctx context.Context) error
    Prepare(ctx context.Context) error
    Command(ctx context.Context, command Command) (Invocation, error)
    Run(ctx context.Context, command Command) (Output, error)
    Observe(ctx context.Context) (State, error)
}
```

Devcontainer execution amended the conceptual interface in three places, and
ADR-033 records why. `Command` returns an argument vector rather than an `*exec.Cmd`, because
the terminal backend constructs the process and an `*exec.Cmd` returned here
would be a process nobody runs. `Run` is added, because validation asks an
environment questions rather than attaching a terminal to it. `Shell` is folded
into `Command`, because the daemon already decides what a task shell is.
`Destroy` removes containers and networks only.
Volume enumeration and removal are separate methods rather than a flag, so that
"volumes are retained by default" is the shape of the interface rather than an
argument that can be passed wrongly (ADR-037).

An environment is constructed from final values — absolute Compose files, a
project name, a service, a user, mounts, and labels — and reads neither
configuration nor persistent state, as the Git and agent adapters do.

### Host execution

Runs Claude directly in the primary task worktree. It provides convenience and no container security boundary.

### Devcontainer execution

The host Compose adapter starts the configured service and executes Claude as the configured non-root user. It mounts task worktrees at project-defined container paths plus the control workspace. It never mounts a Docker socket.

The agent's Compose project is `feat-agent-{project_id}-{task_id}`, which is
generated rather than configured: it is Feat's own resource, and its prefix
cannot collide with a project the user brings up by hand from the same files.
`--project-directory` stays pinned at the first configured Compose file, so that
file's relative sources and build contexts keep resolving while the generated
override lives under the state directory.

Because the name is derived from the two identifiers rather than stored, it is
also resolvable for a task whose record names no environment. A launch that fails
after its container exists is exactly that task — the session the identity is
recorded on is created after the container — so its containers, networks, and
volumes are addressed by the derived name instead: `docker compose --project-name
<name>` reads Compose's own labels and needs no Compose file, which is what lets
it answer for a project whose file has since changed. Reconciliation reports what
it finds as an orphan of the record, and cleanup removes it. See ADR-059.

Compose merges a service's `volumes` by target path, so the generated override
takes over whatever the base files mounted at a configured `agent.container_path`
rather than adding a second mount beside it. A path that disagrees with the base
file is therefore a configuration error and not a preference: the agent would
otherwise hold its task worktree *and* the user's ordinary checkout.

The application runtime asks the same question of a different container, and
answers it from a different field. `repositories.<id>.runtime.container_path` is
where that repository's own services expect their source, which is a fact about
that application's Compose files rather than a choice, and it applies whether or
not the agent is containerised. The runtime's own composition is generated:
each repository's Compose files are joined by a Feat-written `include` document
with one project directory per repository, so nothing relative crosses a
repository boundary. See ADR-065.

A mount is not the whole answer there. A service whose image copies its source in
has no mount to take over, so the generated override points its `build.context`
at the task's worktree instead — at the same place inside it, and without
touching a relative `dockerfile:` beside it, which follows the new context. The
contexts are read from the project's own Compose files structurally, never
through `docker compose config`, which would render the values of the
environment files Feat must not read. The task records, per managed service,
which repositories' worktrees it mounts and which it builds from, so a service
that will run neither is reported when the runtime is created rather than found
later by a user whose change had no effect.

Each task repository's Git directory is mounted at the same absolute path it has
on the host, with the access its worktree has. A task worktree is not a
repository on its own: its `.git` is a file naming the main checkout's
`.git/worktrees/<name>`, so without that directory every Git command inside the
container fails and full Git access is false while everything else looks correct.
The working copy stays unreachable, because the checkout's directory holds
nothing else in the container.

What that mount exposes is not only repository metadata. For a read-write task it
is writable, and `hooks` and `config` in the common Git directory are shared with
the user's own checkout, so an agent that writes either has arranged host code
execution as the user — through `git status` in the cheapest case, and through
the `fetch` and `worktree add` this adapter itself runs when the next task is
created. The security model accepts repository-metadata *mutation* for a native
host worktree, where there is no boundary to cross; it does not make this case
acceptable by extension. [05-security-model.md](05-security-model.md) § Git
boundary states the devcontainer case in its own terms and ADR-050 records why
the mount stays writable and what was rejected instead.

Two things are established by observing the started container rather than by
reading the resolved Compose configuration, which would render the values of the
project's environment files: that no mount is a Docker socket, and that no mount
exposes a configured repository's working copy — the checkout itself, anything
containing it, or anything inside it other than that Git directory. Both refuse
the launch.
Write access to the control workspace and to the task worktrees is probed inside
the container as the configured user for the same reason — a uid that cannot
write across a bind mount produces a session that reports nothing at all.

Minimum supported Docker Compose version is 2.24, for the `!reset` tag that
removes a base file's `container_name` and published `ports`, and the `!override`
tag the application runtime publishes an allocated port with. Both values are
global to the daemon or the host and would otherwise make two concurrent task
containers impossible.

The reset reaches every service the project's own Compose files define, not the
agent's alone. Starting the agent's service starts its whole `depends_on`
closure, and everything Compose starts is in the task's Compose project, so a
dependency that kept a fixed name or a published port is the same collision one
service over — arriving as a Compose error about a service the user did not know
Feat was starting. Which services those are is read with
`docker compose config --services` against the project's own files, without the
generated override so a stale one cannot reintroduce a service the project has
since removed; it prints service names and nothing else, so no environment-file
value is rendered. A service the agent does not run in gets the two resets and
nothing else: no task worktree, no generated variable, and no ownership label,
because Feat's labels are how the container the agent *does* run in is found
(ADR-033).

## Runtime adapter

The runtime Compose adapter accepts:

- one or more base Compose files;
- optional static override;
- generated task override;
- host-side env-file paths;
- service subsets;
- unique project name.

v0 commands are explicit create/start/stop/status/logs/destroy actions. The adapter uses argument arrays and retains the exact Compose inputs in task state for reconciliation.

The service subset is what a create and a start target. Compose starts whatever
those services depend on, and everything it starts is in the task's own Compose
project, so stop, status, logs, and destroy name no service and address the
project — what starting starts, stopping stops. The generated override reaches
every service the project defines for the same reason (ADR-034 evidence 12).

Create is `docker compose up --no-start` rather than `docker compose create`,
which builds the image of the service it is given and none of the images the
services it brings along need (ADR-034 evidence 13).

Every action is a user's explicit request. No workflow transition, no
reconciliation pass, and no agent reaches one: services start when a user asks,
approval offers to stop them and never does, and a `runtime_requested` control
message is inert until a person approves it.

The generated override controls task mounts, published ports, and non-secret
generated variables. A repository declares which of its managed services a user
reaches from the host; the daemon allocates a host port per publication from the
project's configured range, holds it on the task's record while its runtime
exists, releases it when the runtime becomes absent, and writes it into the
override in place of what the project published — every other service of the
task's Compose project publishes nothing. The allocation is the daemon's, under
one lock across every task, because a free port can only be chosen against what
every other task holds. Automated lifecycle phases remain roadmap capabilities;
the architecture must leave room for them.

The runtime and the agent's execution environment never import each other. The
Compose plumbing is deliberately duplicated between `internal/runtime/compose`
and `internal/execution/compose` rather than shared, because sharing it would
put the environment the agent runs in and the application the user tests behind
one type — the distinction the domain model, the security model, and CLAUDE.md
all keep. A `runtime-stays-an-adapter` `depguard` rule makes it mechanical
(ADR-034).

Runtime state is observed rather than assumed, on a slow poll over the tasks
that hold a runtime record, and a poll writes and publishes only when what it
saw differs from what was recorded — and an observation is applied only when the
record it was taken against has not changed since, because Docker answers slowly
enough for a create to finish in between and a stale answer would release the
host ports that create had just allocated (ADR-065 evidence 16). The ports come
from `docker compose ps`; the
networks and volumes come from `docker network ls` and `docker volume ls`
filtered on Compose's own project label, because `docker compose config` would
render the values of the project's environment files.

An external resource such as a pre-existing staging database is not modelled at
all. Feat generates the non-secret `FEAT_TASK_KEY` a task can use to name its
share of one, and knows nothing else about it: the connection string lives in an
environment file Feat passes to Compose by path and never opens, so there is
nothing for Feat to own, verify, or destroy (ADR-048).

Destroy removes the containers and networks of the task's own Compose project.
It passes neither `--volumes` nor `--remove-orphans`: volumes are retained by
default (FR-CLEAN-004) and an orphan is a container Feat did not put there. What
else a user may choose to remove, and what a dirty worktree requires, belongs to
cleanup (ADR-037).

## Control workspace protocol

Host layout, under the state directory but outside the per-task snapshot
directory, so that the tree mounted into an agent container holds the protocol
and nothing else — no snapshot, no event log, no stored brief (ADR-032):

```text
~/.local/share/feat/control/<project-id>/<task-id>/
  task.md            host-written, agent-read: the confirmed brief
  context/           host-written, agent-read
  inbox/             host-written, agent-read
  outbox/            agent-written, host-read
  reports/           agent-written, host-read
  agent/             host-only, mounted read-only where it is mounted at all
```

The container mount path is configurable, with `/feat` as a reasonable default.

`agent/` holds what the provider adapter generates — settings, hook scripts, the
report helper — and the record of which events have been processed. Keeping it
host-only means deduplication never requires the host to write into the
directory the agent owns, which in turn leaves the outbox intact as an audit
trail until cleanup.

Messages are versioned JSON documents written by atomic rename. The daemon
polls for them rather than watching the filesystem: notification does not cross
a bind mount reliably on every supported platform, and a watcher that works on
the host and silently never fires in a container would hide the failure in the
configuration that matters most. A document that does not parse is retried for a
bounded number of polls before it is recorded as malformed, because a write in
progress and a malformed document are different things.

The daemon validates:

- schema version;
- task ID;
- event ID/sequence;
- message type;
- maximum size;
- allowed relative paths;
- capability required by the message;
- whether it has already been processed.

Runtime requests create pending user actions. They never execute directly.

## Resource monitoring

v0 samples:

- whole-machine load average and processor count;
- whole-machine available memory;
- disk availability on the filesystem holding the state directory;
- Feat-managed process/container CPU and memory aggregated per task.

Load rather than a processor-utilisation percentage, because a per-core figure
is not obtainable on macOS without cgo, and one measure reported the same way on
both supported platforms is worth more than two that look alike and are not. The
core count is reported beside it, because a load of four is idle on sixteen
processors and saturated on two. See ADR-035.

A task's containers are found through Feat's own ownership labels — two container
commands per sample whatever the number of tasks — and its processes through the
subtrees of its tmux panes, with processor use differenced between two samples
because that is the one definition that means the same thing on both platforms.
Container and process memory are reported apart as well as together: a container's
memory is what the container runtime reported, and on macOS that is memory inside
its own virtual machine rather than a share of the machine's.

Sampling runs on its own schedule, keeps one sample, and blocks nothing. Feat
does not schedule or reject tasks based on capacity in v0, and a collection
failure degrades to notes on a sample rather than to a failed request. A figure
nothing measured is absent rather than zero.

`internal/resources` is an adapter under the rule ADR-029 established for Git,
and a `resources-stays-an-adapter` `depguard` rule denies it both Compose
adapters: an agent's container and an application's are the same thing to a
resource observer.

## Notifications

Notification policy is domain-driven and platform-adapted:

- idle after grace period and only if not attached;
- review requested/ready;
- verification failed;
- verification blocked, which is a check that could not be run at all;
- session/runtime failure.

The conditions are pinned tables in `internal/notify`, in the shape ADR-026 used
for the workflow transitions. Their most important property is an absence:
nothing maps an end of turn or an idle process to a notification, because idle is
not a state a task arrives in but one it stays in. That is armed by a grace timer
instead, so "idle notifications do not fire immediately" is a property of the
mechanism rather than of a configured value. A blocked gate is named by the
daemon for the same reason: it leaves the task in `review_requested`, and what
has to be said is about the run rather than about where the task landed
(ADR-055).

Whether the user is attached is asked of tmux, per window, through
`window_active_clients`. It is an observation rather than a memory of somebody
having attached: a user who detached, or who switched to another task's window,
stops watching without telling Feat anything.

One change produces at most one notification, and the text is composed from a
task's key, title, and project plus a fixed phrase. Nothing in the composer can
reach a brief, an agent's summary, a path, or a configured value. A delivered
notification is recorded as a `notification_sent` task event, because a desktop
notification is gone the moment it is dismissed.

Every notification is headed by Feat's mark, `❯`, before the task's key and its
project. That is the logo reduced to what the medium can carry: the image beside
a desktop notification is the icon of the application that posted it, and the
one Feat posts through is not Feat, so the heading is the only part of a
notification that can say whose it is. One glyph and not several, because the
heading is a single line it shares with the two things that identify the task.

A notification Feat decides not to deliver names the policy that stopped it in
the daemon's log: the daemon still catching up after a restart, a platform that
delivers none, `notifications.desktop`, `notifications.suppress_while_attached`
with somebody attached, and a condition this build composes no text for. It is a
log line rather than a task event, because a suppressed notification is not
something that happened to the task and an event would publish. Nothing else can
distinguish a policy Feat applied on purpose from a notification the desktop
swallowed, since neither leaves anything to inspect and the state change is
correct either way. See ADR-039.

v0.1 implements macOS desktop notification plus TUI badges, and reports that it
delivered a notification rather than that one was seen: macOS decides per
application whether to show one and drops an unauthorised one without saying so.
What it decides about is Script Editor, which `osascript` posts as, and which is
not necessarily listed among the applications a user can configure. `usernoted`
in the unified log is where a delivery can be confirmed. Public v0 adds Linux
support where a standard notifier is available.

## Review commands

Feat records each repository's base commit and exposes external command
templates. Commands receive structured variables such as repository path, base
commit, task ID, and branch. The daemon expands them, because the placeholder
vocabulary belongs to `internal/config`, which validates it; `internal/review`
decides whether the result may run, and the client runs it with its own terminal.

An expanded command may run only in one of its own task's recorded worktrees,
and only when nothing in it was left unexpanded. That is one rule checked against
one list rather than three commands checked separately, and the case it exists
for is not an obviously dangerous path: another task's worktree is absolute,
real, and safe, and it is still the wrong directory.

The change summaries come from `internal/git`, because they are Git's own
answers about a worktree. Insertions and deletions cover tracked changes only:
reporting a line count for a file Git has never been told about would mean
writing to the index, which no observation in that package does. The untracked
files are counted and named as such rather than folded into one number.

The TUI does not render source diffs in v0.

## Completion gate

A project's configured `checks` are run by the daemon when the agent explicitly
requests review, and never at the end of a turn. Each check runs where its
`execution` field says: inside the task's execution environment, or on the
trusted host in the task worktree. Only repositories the task holds read-write
are checked, and the rest are recorded as skipped with that reason.

The daemon runs them rather than the agent, which is what makes a `provider`
result evidence rather than a claim: the outbox is agent-writable by design, so
anything delivered through it is something an agent could have authored.

The failure returns to the native agent loop through the exit status of the
helper the agent invoked. The generated helper waits for the daemon's verdict,
written into the task's inbox, and exits non-zero with the failing output on
standard error — so the model reads a failed tool call and carries on in the same
turn. It is not a hook: Claude's only blocking hook fires at the end of every
turn, and a gate built on it would either run the project's suite whenever the
agent stopped speaking or need shell logic to work out whether a request was
outstanding (ADR-036).

A check that could not be started, or that exceeded its bound, is inconclusive
rather than failed, and an inconclusive check does not pass the gate: a task
reaching `ready_for_review` on the strength of a check nobody managed to run
would claim a verification that did not happen. The bounds are fixed constants
rather than configuration.

Nor does it fail the gate. A run in which nothing reported failure and something
never reported at all is blocked: the task stays in `review_requested`, where a
review request Feat has no verdict for always rests, the user is interrupted with
`verification_blocked`, and the helper exits zero so the session is not sent back
into its loop over a check configuration it must not edit. A run holding both a
failure and a check that never ran is a failed one, because a check that reported
is evidence about the work — the ones that did not are still named in what the
agent is told, in the task's history, and on the review screen (ADR-055).

A gate does not outlive the process that started it, so a task found in
`verifying` at startup returns to `review_requested` with an event saying the
checks were interrupted. Running them again is an action the user takes.

## Recovery

On startup:

1. load project/task snapshots;
2. validate schema versions;
3. discover tagged tmux objects;
4. query Git worktrees and branches;
5. query configured Compose projects, and the derived one of every task that
   records none;
6. scan unprocessed control messages;
7. compare desired and observed state;
8. update observations and publish recovery events;
9. offer actions for inconsistent resources.

Stopped application containers are reported, not restarted.

Cleanup removes classes in a fixed order, so that what holds a file is gone
before the file is. The control workspace is last, and its removal establishes
rather than assumes the order: a task's agent containers are asked about by name
first, and a workspace still mounted into a running one is refused rather than
removed half way. Stopped containers do not refuse it, and a question that could
not be asked does. See ADR-059.

## Future remote architecture

The daemon's domain event stream is the boundary for a future remote connector. The relay must not simply expose the local API socket.

Working model:

- daemon opens outbound authenticated connection;
- paired client and daemon negotiate end-to-end encryption;
- relay forwards encrypted state/terminal frames;
- terminal streaming uses a dedicated bidirectional protocol;
- non-sensitive push signals contain no task text;
- source and transcripts are not persisted by the service;
- local/LAN web access remains open source.
