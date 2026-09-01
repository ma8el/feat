# ADR-030 — tmux identity, command ownership, and attach boundary

Status: accepted
Recorded: 2026-08-05, before implementation

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
