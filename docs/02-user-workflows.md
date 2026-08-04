# User Workflows

## 1. Register a project

v0.1 uses manually authored YAML.

1. The user creates a local project configuration describing repositories, base branches, container paths, agent execution, Compose files, runtime services, and review commands.
2. The user runs `feat doctor`.
3. Feat validates paths and required executables on the host.
4. For devcontainer execution, Feat validates the configured service, non-root user, Claude executable, required mounts, and optional `gh`/`glab` availability inside the environment.
5. Feat resolves and prints the effective configuration without printing secret values.
6. The user registers the project.

An interactive onboarding wizard is a public-v0 or later feature.

## 2. Implement an ad hoc task

Entry points:

```text
feat implement
feat implement --file task.md
```

Flow:

1. Feat opens a task draft.
2. The user enters a prompt or imports Markdown.
3. Feat asks which project repositories are part of the task and whether each is read-write, read-only, or omitted.
4. Feat fetches configured remotes without mutating the user's normal checkout.
5. Feat resolves the base commit for every selected repository.
6. Feat proposes branch names, worktree paths, execution profile, and runtime profile.
7. Feat displays an editable final task brief.
8. Nothing is created until the user confirms.
9. The daemon creates task state, branches, worktrees, control workspace, tmux target, and the configured agent environment.
10. Claude Code starts with the accepted task brief.
11. The dashboard returns immediately so another task can be prepared.

## 3. Implement a ticket

Ticket ingestion is post-v0 unless pulled forward.

Target sources:

- GitHub Issues;
- Shortcut stories in the current iteration assigned to the user or team;
- later GitLab issues and additional systems.

Flow:

1. Feat lists matching tickets.
2. The user selects one or several tickets.
3. Feat snapshots the title, description, acceptance criteria, selected comments, and relevant metadata.
4. Comments are excluded by default and selectable.
5. The snapshot is placed in the task control workspace and does not mutate while the agent is working.
6. If the ticket later changes, Feat notifies the user; it does not silently alter the active agent context.
7. The rest of the flow matches an ad hoc task.

Natural-language selection such as “implement X, Y, and Z” is a roadmap goal. Public v0 uses explicit selection.

## 4. Supervise parallel work

The global dashboard shows active tasks across projects. Each row includes:

- task ID and short title;
- selected repositories;
- agent/process state;
- attention state;
- runtime state;
- verification state;
- elapsed time;
- resource usage;
- changed-file count.

PR/MR state is not required in the v0 task list.

The user can:

- attach to the native agent terminal;
- create or attach to the optional task shell;
- start or stop application services;
- open normal Compose logs;
- open review commands;
- inspect task configuration and control messages;
- stop or clean up a task.

## 5. Attach and revise

1. The user selects a task and chooses Attach.
2. Feat temporarily yields the terminal to the product-managed tmux task window.
3. The user interacts with the normal Claude Code TUI.
4. Detaching returns to the Feat dashboard.
5. A new user prompt submitted after a review state conservatively changes the task back to `working`.
6. Claude continues in the same session and may request review again.

Feat does not recreate Claude's prompt-and-response interface in v0.

## 6. Agent attention and completion

Observable agent process state and semantic workflow state are separate.

- A normal end of Claude's turn becomes `idle` after a short grace period.
- If the user is attached, Feat suppresses the desktop idle notification.
- `idle` does not claim that the task is complete or that Claude asked a question.
- Claude explicitly emits `review_requested` through its adapter/control workspace.
- If a provider-native completion gate runs configured checks successfully, the task becomes `ready_for_review`.
- Without a completion gate, the UI shows `review_requested` with agent-reported verification.
- A failed gate feeds its output back into the native Claude session and returns the task to `working` or `verification_failed` as appropriate.

## 7. Manual runtime lifecycle in v0

1. Creating a task starts the agent devcontainer when required.
2. Application services remain stopped initially.
3. The user starts them from the dashboard when needed.
4. Feat invokes the configured Docker Compose CLI command on the host.
5. The agent receives no Docker socket or Docker CLI.
6. Feat displays normal Compose service state and opens `docker compose logs` for logs.
7. The runtime remains available during review.
8. Approval offers to stop services; it does not stop or destroy them automatically.

Target versions may start services based on configured phases or an agent-written, user-approved `runtime_requested` control message.

## 8. Review

1. The user opens Review for a task.
2. Feat groups changed repositories and shows each resolved base commit and change summary.
3. The user invokes the configured diff command, defaulting to normal `git diff` against the recorded base.
4. The user invokes the configured editor command, defaulting to `$EDITOR` in the selected repository.
5. For this user's configuration, the editor is Neovim.
6. Feat does not implement an internal diff renderer, inline comments, or reviewed-file tracking in v0.
7. The user either approves, attaches to Claude with revision instructions, or leaves the task pending.

## 9. Publish changes

Publishing is post-v0.

When `gh` or `glab` access is enabled inside the agent environment:

1. Feat verifies CLI installation and authentication before launch or publication.
2. Claude may prepare commits, push branches, create one PR/MR per changed repository, and update provider artifacts when the user prompts it to do so.
3. Feat does not grant Docker access as a consequence.
4. The credential scope determines what remote mutations Claude can perform.
5. Merge remains outside Feat initially.

Projects may instead configure host-side provider execution later, but that is not the required model.

## 10. Recovery

On startup, the daemon reconciles persisted task records with:

- product-managed tmux sessions and windows;
- Git worktrees;
- running or stopped Compose projects;
- task control workspaces;
- pending review state.

Feat does not automatically restart stopped containers. It reports the observed state and offers recovery actions.

## 11. Cleanup

1. The user selects Cleanup.
2. Feat resolves every task-owned resource.
3. It displays dirty repositories, unpushed commits, unmerged branches, running services, and retained volumes.
4. Stopping services and removing containers/networks are separate from removing volumes, worktrees, or branches.
5. Dirty or unmerged worktrees require explicit confirmation.
6. Volumes are retained by default.
7. Task metadata is archived so Feat can explain what happened later.

No age-based automatic deletion exists in initial versions.

## 12. Future remote workflow

The working hypothesis for the commercial remote client is:

- global task overview;
- non-sensitive attention notifications;
- actual tmux/terminal streaming and input;
- runtime approvals and start/stop actions;
- change summaries;
- last-known non-sensitive state when the host is offline;
- local daemon connection through an outbound, end-to-end encrypted relay;
- no persistent source-code or transcript storage by the relay.

The first client should be a responsive web app/PWA. Native apps depend on demonstrated usage.

