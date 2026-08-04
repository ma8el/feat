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
feat task ...                task client
feat runtime ...             runtime client
feat doctor                  diagnostics
```

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
internal/resources           host/task resource observation
internal/notify              desktop/TUI notification policy
internal/reconcile           startup discovery and repair proposals
internal/ui                  Bubble Tea models and views
internal/version             build identity
internal/guard               repository-wide invariant tests, no runtime code
```

Adapters are compiled into the binary initially. Interfaces must avoid leaking implementation types so a future external plugin protocol remains possible.

`internal/cli`, `internal/paths`, `internal/version`, and `internal/guard` were added during slice 0; see ADR-025. `internal/store/storetest` was added during slice 1; see ADR-026. Import boundaries between these packages are enforced by `depguard` rules in `.golangci.yml`.

## Local API

The API is versioned from the beginning but is not promised stable before v1.

Minimum endpoints:

```text
GET    /v1/health
GET    /v1/events                         SSE
GET    /v1/projects
POST   /v1/projects
GET    /v1/projects/{project_id}
POST   /v1/projects/{project_id}/doctor
GET    /v1/tasks
POST   /v1/task-drafts
PUT    /v1/task-drafts/{draft_id}
POST   /v1/task-drafts/{draft_id}/launch
GET    /v1/tasks/{task_id}
POST   /v1/tasks/{task_id}/attach-info
POST   /v1/tasks/{task_id}/shell
POST   /v1/tasks/{task_id}/runtime/start
POST   /v1/tasks/{task_id}/runtime/stop
POST   /v1/tasks/{task_id}/runtime/logs-info
POST   /v1/tasks/{task_id}/review/approve
POST   /v1/tasks/{task_id}/cleanup/plan
POST   /v1/tasks/{task_id}/cleanup/execute
```

The local socket is user-owned and mode-restricted. Destructive API requests use task/resource IDs and a server-produced cleanup plan token rather than arbitrary filesystem paths.

SSE events carry domain state changes, not raw terminal streams or secrets.

## File-backed storage

Configuration directory:

```text
~/.config/feat/projects/<project-id>.yaml
```

State directory:

```text
~/.local/share/feat/
  daemon.json
  projects/<project-id>/project.json
  projects/<project-id>/tasks/<task-id>/task.json
  projects/<project-id>/tasks/<task-id>/prompt.md
  projects/<project-id>/tasks/<task-id>/events.jsonl
  projects/<project-id>/tasks/<task-id>/review.json
```

Runtime socket/PID data uses the operating system's user runtime directory with a documented fallback.

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
- generate branch/worktree names;
- create read-write and read-only worktrees;
- observe dirty/ahead/behind/merged state;
- compute change summaries against recorded bases;
- produce exact cleanup plans;
- remove worktrees/branches only after confirmation.

The adapter invokes Git as an argument vector, not through interpolated shell strings.

The v0 worktree path default should be deterministic under Feat's data/workspace directory and configurable. Worktree metadata sharing is documented in the security model.

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
- reconcile existing managed sessions on daemon startup.

The tmux adapter is behind an internal execution interface even though no alternative backend is required before v1.

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
- may enforce configured checks using provider-native completion hooks inside the devcontainer;
- supports direct `gh`/`glab` usage when configured and authenticated.

Implementation must verify exact supported Claude CLI flags and hook schemas against the installed/supported Claude Code version. Provider-specific flags must remain inside the adapter.

## Execution environments

Conceptual interface:

```go
type ExecutionEnvironment interface {
    Validate(ctx context.Context) error
    Prepare(ctx context.Context, task Task) error
    Command(ctx context.Context, spec CommandSpec) (*exec.Cmd, error)
    Shell(ctx context.Context, task Task) (*exec.Cmd, error)
    Observe(ctx context.Context, task Task) (ExecutionState, error)
    Destroy(ctx context.Context, task Task) error
}
```

### Host execution

Runs Claude directly in the primary task worktree. It provides convenience and no container security boundary.

### Devcontainer execution

The host Compose adapter starts the configured service and executes Claude as the configured non-root user. It mounts task worktrees at project-defined container paths plus the control workspace. It never mounts a Docker socket.

## Runtime adapter

The runtime Compose adapter accepts:

- one or more base Compose files;
- optional static override;
- generated task override;
- host-side env-file paths;
- service subsets;
- unique project name.

v0 commands are explicit create/start/stop/status/logs/destroy actions. The adapter uses argument arrays and retains the exact Compose inputs in task state for reconciliation.

The generated override controls task mounts and non-secret generated variables. Automated port allocation and lifecycle phases are roadmap capabilities; the architecture must leave room for them.

External resources such as pre-existing staging databases are configuration bindings, not resources Feat owns or destroys.

## Control workspace protocol

Host layout:

```text
<task>/control/
  task.md
  context/
  inbox/
  outbox/
  reports/
```

The container mount path is configurable, with `/feat` as a reasonable default.

Messages are versioned JSON documents written by atomic rename. The daemon validates:

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

- whole-machine CPU and available memory;
- whole-machine disk availability where relevant;
- Feat-managed process/container CPU and memory aggregated per task.

Sampling remains observational. Feat does not schedule or reject tasks based on capacity in v0.

## Notifications

Notification policy is domain-driven and platform-adapted:

- idle after grace period and only if not attached;
- review requested/ready;
- verification failed;
- session/runtime failure.

v0.1 implements macOS desktop notification plus TUI badges. Public v0 adds Linux support where a standard notifier is available.

## Review commands

Feat records each repository's base commit and exposes external command templates. Commands receive structured variables such as repository path, base commit, task ID, and branch. They are executed only after template expansion into an argument vector or an explicitly configured shell command with clear trust semantics.

The TUI does not render source diffs in v0.

## Recovery

On startup:

1. load project/task snapshots;
2. validate schema versions;
3. discover tagged tmux objects;
4. query Git worktrees and branches;
5. query configured Compose projects;
6. scan unprocessed control messages;
7. compare desired and observed state;
8. update observations and publish recovery events;
9. offer actions for inconsistent resources.

Stopped application containers are reported, not restarted.

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

