# User Workflows

## 1. Register a project

A project is one YAML file. It can be written by answering questions or by hand,
and the two produce the same thing: `feat project init` composes the file, and
every command afterwards reads it as though it had been typed.

```text
feat project init
feat project init <project>
feat project init --dry-run
p                              # the same questions, from the dashboard
```

Flow:

1. The user runs `feat project init` in one of the project's checkouts, or
   presses `p` in the dashboard, which asks the same questions as a dialog over
   the task list. Both drive one flow, so the questions and what they accept are
   the same; the dialog adds a cursor on the closed questions, `esc` to step
   back out of an answer, and `tab` to complete one.
2. Feat asks which repositories take part and how each takes part by default,
   where the agent runs, which provider CLI it uses, whether a task runs
   application services, and what verifies the work.
3. Feat answers from the host what the host can answer: whether a path is a Git
   repository, its working-tree root, its remote, its default branch, the
   Compose files beside it, and the services those files declare. Each is shown
   as a proposal, and pressing Enter accepts it. In the dashboard, `tab` puts a
   proposal in the field to be edited rather than retyped, and steps through
   whatever else was found beside it (ADR-077).
4. Feat validates the composed configuration, displays the whole file, and
   writes nothing until the user confirms. An existing configuration is never
   overwritten.
5. Feat runs the diagnosis, which is `feat doctor` for the new project: at the
   command it is offered, and in the dashboard it runs as soon as the file
   exists. What it finds never undoes the project — the file is written, and a
   finding is a thing to fix.
6. Feat offers to register the project when a daemon is running, which is
   `feat project add`.

The same checks are on `D` in the dashboard afterwards, for the selected task's
project or for every configured project when no task is selected, and `r` runs
them again. The report says which environment it was checked from, because a
check is only true of the process that ran it.

Writing the file by hand is the same workflow without the first five steps: copy
`docs/examples/project.yaml`, edit it, and run `feat doctor` before registering.
Either way:

1. `feat doctor` validates paths and required executables on the host.
2. For devcontainer execution, Feat validates the configured service, non-root user, Claude executable, and required mounts inside the environment. `gh`/`glab` are validated on the host instead, where publication runs (ADR-075).
3. Feat resolves and prints the effective configuration without printing secret values.
4. The user registers the project.

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

Ticket ingestion was post-v0 unless pulled forward, and ADR-072 pulled it forward: the tracker is built after publication and before the public preview.

Sources, in the order ADR-071 builds them:

- a configured command printing JSON that conforms to the ticket schema Feat publishes, which is what makes a tracker Feat has never heard of configurable by the user rather than by a release;
- Shortcut and GitLab issues through that command;
- worked example commands afterwards, covering GitHub Issues, GitLab Issues, GitHub Projects, and Shortcut.

Where a ticket lives is the command's business rather than Feat's. Issues attached to a repository, stories in a workspace, and an organisation-level board that spans repositories are all one command away, including the case where tickets are filed in a planning repository that holds no code and is not registered with Feat at all.

Flow:

1. Feat lists matching tickets.
2. The user selects one. A task carries one ticket, because the reference on the task is what a merge request names and what a change is compared against, and neither has an answer for several (ADR-071).
3. Feat snapshots what the ticket schema carries: a reference, a title, a body, a URL, and a state. Anything richer belongs in the brief, which is Markdown and holds whatever the user wants (ADR-071).
4. Whether comments reach the body is the configured command's decision rather than Feat's. Selection by Feat would need a path that fetches comments itself, and none is scheduled.
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
- If the project configures checks, Feat runs them itself — in the environment the
  agent works in, or on the host where the check says so — and the task passes
  through `verifying` to `ready_for_review` or `verification_failed`.
- Without configured checks there is no gate: the UI shows `review_requested`
  with the agent's own reported verification, marked as the claim it is.
- A failed gate reaches the running session as a failed command: the helper the
  agent used to request review exits non-zero with the failing output, so the
  agent reads it and carries on in the same turn. The task rests in
  `verification_failed` until it asks again or the user acts.
- A check that could not run at all is not a failed gate. Nothing was established
  about the work, so the task rests in `review_requested` as it would in a
  project with no gate, the helper exits zero rather than sending the session
  after a configuration it must not edit, and the user is told that the checks
  could not run. The review screen names each check that did not report and the
  reason it gave, and running them again is an action the user takes once the
  configuration or the environment is fixed (ADR-055).

## 7. Manual runtime lifecycle in v0

1. Creating a task starts the agent devcontainer when required.
2. Application services remain stopped initially.
3. The user starts them from the dashboard when needed.
4. Feat invokes the configured Docker Compose CLI command on the host.
5. The agent receives no Docker socket or Docker CLI.
6. Feat displays normal Compose service state and opens `docker compose logs` for logs. The logs hold the terminal until the user interrupts them, which returns to the dashboard rather than leaving Feat.
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

Publishing was scheduled before the public preview (ADR-072) and is built. Every credentialed call is made by the daemon on the trusted host, and the agent environment receives no provider token (ADR-070). It is `feat task publish <task>`, and `P` on a task's panel in the dashboard.

1. The agent writes a publication draft — a title and a body per repository — into the control workspace when it requests review, which is while it still knows what it did. The draft asks for nothing and needs no capability.
2. The user reads it on the publication screen, which draws the title and the whole description that would be sent, and may rewrite it through the configured editor command. Nothing is sent before every line of it has been displayed — in Feat, or in the editor it was opened in. What was displayed is what is sent (ADR-076).
3. A draft describing a commit that is no longer current is refused rather than published, as a stale launch plan is refused (ADR-031).
4. On approval Feat pushes each changed repository's task branch and opens one PR/MR per repository, composing the final request from the agent's prose and what Feat already knows: the remote, the base branch, the task's own branch, and — added to the draft the user reads rather than to the request, so it can be deleted — the ticket the task came from.
5. The push runs with hooks and the external pager and diff commands disabled, and the approval step names any `pre-push` hook it is skipping.
6. Repositories are published one at a time, each result recorded before the next begins. A failure on one does not abort the others, nothing is rolled back, and re-publishing skips a repository that already has a recorded request (ADR-073).
7. Merge remains outside Feat.

A project may instead enable `gh` or `glab` inside the agent environment and let Claude publish when prompted. Feat verifies CLI installation and authentication before launch, grants no Docker access as a consequence, and the credential's scope determines what remote mutations are possible. That path stays configurable and is not what the flow above is built on.

## 10. Recovery

On startup, the daemon reconciles persisted task records with:

- product-managed tmux sessions and windows;
- Git worktrees;
- running or stopped Compose projects;
- task control workspaces;
- pending review state.

Feat does not automatically restart stopped containers, and nothing else either:
a pass repairs, restarts, recreates, and adopts nothing. It reports what it
observed and offers recovery actions the user takes.

A resource Feat cannot read is quarantined rather than allowed to end the pass,
so one damaged terminal, worktree, or Compose project never makes the healthy
ones unusable. Missing, orphaned, inconsistent, and damaged resources are each
reported with what the user can do about them.

A dead agent session can be resumed, which continues the recorded provider
session rather than opening an empty one. It is offered and never automatic.

## 11. Cleanup

1. The user selects Cleanup.
2. Feat resolves every task-owned resource.
3. It displays dirty repositories, unpushed commits, unmerged branches, running services, and retained volumes.
4. Stopping services and removing containers/networks are separate from removing volumes, worktrees, or branches. The task's terminal and its control workspace are two further separate choices.
5. Dirty or unmerged worktrees require explicit confirmation. The confirmation names the warning the user was shown, and is re-checked against what is true at the moment of removal.
6. Volumes are retained by default.
7. Task metadata is archived so Feat can explain what happened later. Nothing is deleted from the state directory: the snapshot keeps what the task was, and the event log keeps what became of what it owned. Archiving is refused while the task still owns resources the cleanup leaves behind.

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

