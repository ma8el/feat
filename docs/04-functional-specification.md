# Functional Specification

Requirement keywords use MUST, SHOULD, and MAY in their conventional normative sense.

## Project management

### FR-PROJ-001 — Register local projects

Feat MUST register a project from a local YAML configuration without copying repository contents.

### FR-PROJ-002 — Multi-repository projects

A project MUST support several Git repositories with independent host paths, container paths, base branches, remotes, and default access modes.

### FR-PROJ-003 — Primary workspace

A project MUST identify a primary editable repository/workspace used as the default task working directory.

### FR-PROJ-004 — Validation

`feat doctor` MUST validate:

- configuration schema and unknown fields;
- repository paths and Git status;
- required host executables;
- tmux availability;
- Docker Compose availability when configured;
- configured Compose files and service names;
- agent executable and user identity in the selected execution environment;
- optional `gh` and `glab` installation/authentication in the environment where Claude will use them;
- review commands.

It MUST redact secret values and SHOULD show resolved paths and commands.

The dashboard MUST be able to run the same checks and display what they found,
for the selected task's project or for every configured project, and MUST run
them only when the user asks. It MUST report which environment the checks ran
in, because a check is only true of the process that ran it. A skipped check
MUST NOT be displayed or counted as a pass.

### FR-PROJ-005 — Guided configuration

Feat MUST provide a command that writes a project's configuration by asking for
what has to be decided, so that a first project does not require a file to be
authored by hand.

It MUST derive what the host can answer — whether a directory is a Git
repository, its working-tree root, its remote, its default branch, the Compose
files beside it, and the services those files declare — rather than asking for
it, and it MUST show each proposal as a proposal.

It MUST validate the configuration it composed before offering it, display the
whole file, and write nothing until the user confirms. It MUST NOT overwrite an
existing configuration, and it MUST NOT register the project without being
asked. Registration and diagnosis remain the commands they already are.

The dashboard MUST offer the same questions, because it is where a user with no
project configured already is. The questions, their proposals, their validation,
and their order MUST have one implementation, which both the command and the
dashboard drive. The dashboard MUST allow an answer to be stepped back out of,
MUST display the whole composed file before writing it, and MUST leave the
machine unchanged when it is cancelled.

## Task preparation

### FR-TASK-001 — Ad hoc prompt

The user MUST be able to create a task from an interactively entered prompt.

### FR-TASK-002 — Markdown source

The user MUST be able to create a task from a Markdown file.

### FR-TASK-003 — Draft before launch

Task creation MUST have a draft stage containing:

- title and final task brief;
- repository access selection;
- resolved base references/commits;
- proposed branches and worktree paths;
- agent execution profile;
- runtime profile;
- required checks and review commands.

Feat MUST NOT create worktrees, containers, or agent sessions until the user confirms the draft.

### FR-TASK-004 — External tickets

Post-v0 ticket adapters SHOULD provide selectable ticket lists and immutable task snapshots. Comments MUST be excluded by default and MAY be selected.

### FR-TASK-005 — Snapshot changes

If an external ticket changes after task creation, Feat SHOULD notify the user and MUST NOT silently change active agent context.

## Git and worktrees

### FR-GIT-001 — Fetch without pull

Feat MUST fetch configured remotes before resolving task bases when network access is available. It MUST NOT automatically pull or mutate the user's ordinary checkout.

### FR-GIT-002 — Base policy

Base resolution MUST support at least remote-tracking, local branch, current commit, and explicit ref policies. The default SHOULD be the configured remote-tracking default branch.

### FR-GIT-003 — Dirty source checkout

Uncommitted changes in the ordinary checkout MUST NOT automatically block task creation when the selected base commit can be resolved independently.

### FR-GIT-004 — Branch and worktree

Every read-write task repository MUST receive a generated branch and task worktree. Names and paths MUST be configurable with sane defaults.

### FR-GIT-005 — Read-only source

Every selected read-only source repository SHOULD receive a reproducible task worktree and MUST be mounted read-only into a devcontainer. Stable non-task infrastructure checkouts MAY be used read-only when configured explicitly.

### FR-GIT-006 — Full Git in agent

The devcontainer execution mode MUST permit full Git access when configured. Documentation MUST disclose that native Git worktrees share repository metadata and therefore do not isolate Git refs from the agent.

### FR-GIT-007 — Optional commits

Feat MUST support committed and uncommitted agent changes. It MUST NOT require or create commits automatically in v0.

## Agent execution

### FR-AGENT-001 — Adapter boundary

Agent behavior MUST be implemented through a provider adapter. Claude Code is the only required v0 adapter.

### FR-AGENT-002 — Execution profiles

The architecture MUST support `host` and `devcontainer` execution modes. v0.1 requires devcontainer; public v0 requires both.

### FR-AGENT-003 — Native interface

Feat MUST launch and attach to the native interactive Claude Code terminal instead of reimplementing it.

### FR-AGENT-004 — One session per task

Feat MUST create one primary agent session per task. Two tasks MUST NOT share a worktree or runtime.

### FR-AGENT-005 — Control workspace

Every task MUST receive a dedicated control workspace with schema-validated inbox, outbox, task brief, and report locations.

### FR-AGENT-006 — Structured state

The Claude adapter MUST use provider hooks and explicit control messages for state changes where supported. Terminal-output heuristics MUST NOT be the sole source of review completion.

### FR-AGENT-007 — Idle state

A provider end-of-turn signal SHOULD become `idle` after a configurable short grace period. Notifications SHOULD be suppressed while the user is attached.

### FR-AGENT-008 — Review request

Semantic review completion MUST require an explicit provider event or control message. An ordinary idle/Stop event MUST NOT become `ready_for_review`.

### FR-AGENT-009 — Revision

Submitting a new user prompt in a review state SHOULD conservatively transition the task to `working` or `changes_requested` until the provider emits a new review request.

### FR-AGENT-010 — Provider CLIs

Projects MAY grant Claude access to `gh` and/or `glab` within its execution environment. Feat SHOULD validate CLI authentication. Provider access MUST NOT imply Docker access.

### FR-AGENT-011 — Environment owned by its session

A task's agent execution environment MUST be owned by the agent session that runs in it. It MUST come into existence only as part of launching or resuming that session, and Feat MUST NOT offer a verb that starts or creates one without a session (ADR-057).

### FR-AGENT-012 — Stop and resume

A user MUST be able to stop the environment a task's agent runs in and to bring it back by resuming the session. Stopping MUST keep the task's worktrees, branches, control workspace, volumes, and terminal, MUST NOT change the task's workflow state, and MUST NOT act on the task's application runtime. Both actions MUST be reachable from the command line and from the dashboard.

### FR-AGENT-013 — A session is not alive without its environment

Feat MUST NOT record an agent process as alive while the environment it runs in is observed not to be running. An environment that stopped without the user asking MUST be reported as a session failure, and what reconciliation recommends about such a task MUST be an action the product will accept (ADR-057).

## tmux execution

### FR-TMUX-001 — Dedicated server

Feat MUST use a dedicated tmux server/socket so managed sessions do not collide with ordinary user sessions.

### FR-TMUX-002 — Topology

The default mapping is one tmux session per project and one window per task.

### FR-TMUX-003 — Panes

Every task window MUST have one managed agent pane and MAY have one on-demand shell pane in the same execution environment and primary workspace.

### FR-TMUX-004 — User configuration

Feat SHOULD preserve the user's tmux configuration and keybindings. It MUST identify managed objects through tmux user options/stable metadata rather than indexes or names alone.

### FR-TMUX-005 — Attach/detach

The dashboard MUST support attaching to a task window and returning after detach without losing daemon state.

## Runtime management

### FR-RUN-001 — Runtime independence

Agent execution and application runtime MUST be modeled separately.

### FR-RUN-002 — Compose CLI

The initial runtime adapter MUST invoke the installed Docker Compose CLI rather than mounting Docker access into the agent or talking directly to Docker Engine APIs.

### FR-RUN-003 — Compose inputs

v0 MUST support configured base Compose files plus a generated task override. A static user override MAY also be included.

### FR-RUN-004 — Generated identity

The generated override/runtime invocation MUST provide a unique Compose project name and task worktree mounts. Explicit `container_name` overrides SHOULD be avoided.

**Amended: a mount is not the only way a service's code arrives.** For a managed service whose build context is a configured repository's checkout, or a directory inside it, the generated override MUST point that context at the same place inside the task's worktree; only the context is written, so a relative `dockerfile:` beside it follows. Feat MUST record, per managed service, whether the task's work reaches it by mount, by build context, or not at all, resolved from configuration and the project's own Compose files rather than inspected out of the containers, and MUST report a managed service that the task's work does not reach and one whose image must be built again before a change appears in it. Feat MUST NOT run `docker compose config` to answer any of this, because it renders the values of the project's environment files; reading the Compose documents structurally resolves nothing and is allowed. See ADR-065.

### FR-RUN-005 — Manual lifecycle

v0 MUST provide create/start/stop/status/logs/destroy actions. Application services MUST start only by explicit user action in v0.

### FR-RUN-006 — Logs

Feat MUST allow the user to open normal `docker compose logs` output. It need not aggregate or persist logs.

### FR-RUN-007 — Health

Feat SHOULD use native Compose health state where available and otherwise report `running, health unknown`.

### FR-RUN-008 — External resources

The runtime model MUST allow external/shared resources such as pre-existing staging PostgreSQL databases, by not interfering with them. Feat MUST set a per-task discriminator (`FEAT_TASK_KEY`) on every managed service, so that an application can name its own share of one, and MUST NOT read the environment files that configure a connection. v0 does not provision, migrate, seed, reclaim, or model such a resource. **Amended: the configuration block that declared one is removed, because Feat could not see, verify, or reach what it named, see ADR-048.**

### FR-RUN-009 — Agent runtime request

Post-v0, Claude MAY write a `runtime_requested` control message. The daemon MUST validate it and require user approval before executing any host Docker operation.

### FR-RUN-010 — Automated phases

Post-v0 project rules MAY start or stop configured services on lifecycle transitions. Task-level overrides remain an open design question.

## Dashboard and notifications

### FR-UI-001 — Global dashboard

The dashboard SHOULD show active tasks across projects with project drill-down. Tasks across every registered project MUST be reachable without leaving the dashboard, grouped by the project that owns them.

The dashboard MUST keep the task list, the selected task's view, and the machine's resources on screen together. A view that replaces all three MUST be limited to a transaction the user opened and can cancel; see ADR-041.

A project's tasks MAY be folded away so that the list is about the projects a user is working in. A folded project MUST still report how many tasks it holds and whether any of them needs the user, and the list MUST always say which task is selected — on the entry, or on the fold holding it; see ADR-051 and ADR-052. Folding MUST be reversible by the same control that folded, on a position the list's own movement keys can reach. Any control the list draws MUST do something: a marker that offers a fold is a control.

### FR-UI-002 — Task list entry

Each entry in the task list MUST show task ID/title, agent state, attention state, elapsed time, and changed-file count, and MUST NOT require horizontal scrolling or line wrapping at the supported terminal width.

Agent state and attention state MUST remain separately legible. A single composite status indicator does not satisfy this, and neither does an encoding that colour alone carries.

Repositories, runtime state, and verification state are required of the selected task by FR-UI-003, and resource usage by FR-UI-005. A task list MAY show them and MUST NOT do so at the cost of the paragraph above. PR state is not required.

### FR-UI-003 — Task detail

Task detail MUST expose the task brief, repository/base mapping, tmux target, runtime services, completion/check summary, and actions. It need not reproduce the last Claude response.

A failed task MUST show why it failed, beside the state that says it did. The
reason is recorded on the task when it enters `failed` and is shown as it was
reported: an error banner that has already gone and an event log on disk are
between them the whole of what a user had before, and neither is where anybody
looks. See ADR-060.

### FR-UI-004 — Notifications

v0.1 MUST support TUI attention badges and macOS desktop notifications for significant idle/failure/review transitions. Linux desktop notifications are required for public v0 where supported.

### FR-UI-005 — Resource metrics

The dashboard MUST show whole-machine available resources and per-task environment totals. Per-container metrics MAY appear in a secondary view. Feat MUST NOT enforce a concurrency limit in v0.

Whole-machine availability is sampled as load average with the processor count, available memory, and disk availability, and is shown as the share of each in use. A per-core utilisation percentage is not obtainable on macOS without cgo, so the processor share is derived from the load average against the processor count: Feat reports one measure on both supported platforms rather than two that look alike and are not, and that share may exceed 100% because it is demand rather than occupancy. See ADR-035 and ADR-044. Metrics MUST remain observational: a figure nothing measured is shown as absent rather than as zero, a share nothing could be measured against is not drawn, and a collection failure MUST NOT fail a request or block task creation.

## Review

### FR-REV-001 — Repository grouping

Review MUST group changes by repository and compare each repository against its recorded base commit.

### FR-REV-002 — External commands

v0 MUST provide shortcuts for configurable diff and editor commands, each in the selected task repository. It need not render diffs internally. An optional Git status command MAY be configured; it is expanded, validated, and reported with the review rather than launched from the task panel (ADR-045).

### FR-REV-003 — Editor

The editor command MUST default to `$EDITOR` and execute in the selected task repository. The reference configuration uses Neovim.

### FR-REV-004 — Review decision

The user MUST be able to leave pending, approve, or attach to the agent with revision instructions.

## Persistence and recovery

### FR-STATE-001 — File storage

v0 MUST persist user-edited configuration as YAML, task/project snapshots as versioned JSON, task briefs as Markdown, and event history as JSON Lines.

### FR-STATE-002 — Atomicity

The daemon MUST be the sole state writer. Snapshot writes MUST use atomic replacement. Event readers MUST tolerate an incomplete final JSON Lines record after a crash.

### FR-STATE-003 — Reconciliation

Startup MUST reconcile persisted tasks with tmux, Git worktrees, Compose projects, control workspaces, and review state.

### FR-STATE-004 — No automatic restart

Feat MUST NOT automatically restart stopped containers after recovery.

## Cleanup

### FR-CLEAN-001 — Resolve targets

Feat MUST enumerate the exact task-owned resources before cleanup.

The enumeration is not limited to what the task's record names. A launch that
fails after its container exists records no environment, and its containers,
networks, and volumes are enumerated by the Compose project name the task
derives — which names this task's resources by construction, so the inventory
stays exact. See ADR-059.

The enumeration is also what every surface offering the choice shows.
`feat task cleanup` and the dashboard's cleanup screen present the same targets,
each with what it is and whether it is still there, and a warning that is true of
some of a class's resources is shown against those resources rather than against
the class. A surface too small for the inventory scrolls rather than dropping the
end of it: a choice made against a summary is a choice made against something
other than the plan that will be executed.

### FR-CLEAN-002 — Separate destructive classes

Stopping/removing containers, removing volumes, removing worktrees, and deleting branches MUST be separate choices.

Slice 12 separates the agent's containers from the application's, because they
are distinct concepts everywhere else in the product, and adds two classes this
list does not reach: the task's tmux window and its control workspace. Both are
resources a task owns, and FR-CLEAN-001 requires the inventory to be exact. The
seven classes are removed in a fixed order — terminal, agent containers,
application containers, volumes, worktrees, branches, control workspace — so that
whatever holds a file is stopped before the file is removed. See ADR-037.

Because the classes are independent choices, the order alone does not establish
that. Removing the control workspace therefore asks first whether any container
of the task's agent Compose project is still there, and refuses rather than
removing a directory that is an active bind-mount source. See ADR-059.

### FR-CLEAN-003 — Dirty/unmerged protection

Dirty worktrees, unpushed commits, and unmerged branches MUST produce explicit warnings and confirmation.

The warning and the confirmation are separate obligations, and a surface satisfies
them where each belongs. The warning is shown against the resource it is true of,
for as long as that resource is on the screen. The confirmation is of the removal:
it names what would go, lists every warning of everything selected, and defaults
to no. A surface that can display a selection before acting on it MUST NOT ask
per selection — a question raised while the user is still choosing interrupts a
decision that has not been made, and consent given that early is consent to
something the eventual removal may not match. A surface with no selection to
display, such as a sequence of prompts on a terminal, asks as it goes.

The confirmation MUST be put against a freshly resolved plan. A task being
cleaned up may still have an agent working in its resources, so the warnings a
surface displayed when it opened are not necessarily the warnings that are true
when the user answers — and removal is refused for a warning that was not
confirmed. A surface that resolves only on opening therefore reports that refusal
after the fact rather than the warning before it. See ADR-061.

### FR-CLEAN-004 — Volume retention

Volumes MUST be retained by default in initial versions.

### FR-CLEAN-005 — No age deletion

Feat MUST NOT automatically delete resources based only on age.

## Provider publication and remote control

### FR-PUB-001 — Provider adapters

Post-v0 provider support SHOULD cover GitLab first for company dogfooding and GitHub as the primary public integration.

### FR-PUB-002 — PR/MR mapping

Publication SHOULD create at most one PR/MR per changed repository and preserve a parent task relationship.

### FR-PUB-003 — Merge

Feat MUST NOT merge automatically in initial provider integrations.

### FR-REMOTE-001 — Privacy

A future hosted relay MUST NOT persist source code or terminal transcripts and SHOULD use end-to-end encryption between daemon and paired client.

### FR-REMOTE-002 — Offline state

A future client MAY retain last-known non-sensitive state. It SHOULD NOT queue terminal input while the host is offline initially.

