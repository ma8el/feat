# Domain Model

## Model overview

Feat models a feature lane independently of any particular agent, ticket system, Git provider, or runtime backend.

## Entities

### Project

A locally registered development topology.

Properties:

- stable ID and display name;
- one or more repositories;
- one primary editable repository/workspace;
- agent execution profile;
- optional application runtime profile;
- review commands;
- local ticket and provider configuration;
- resource and notification preferences.

### Repository

A Git repository participating in a project.

Properties:

- stable project-local ID;
- host path;
- container path;
- default branch;
- remote name;
- default task access: read-write, read-only, selectable, or omitted;
- optional repository-level checks and review commands.

The reference project contains:

- `dashboard`: frontend/backend code, normally editable;
- `database`: SQL code, selectable as editable or read-only;
- a separate devcontainer-definition repository, normally stable and mounted read-only.

### Task

The aggregate root for one unit of agent work.

Properties:

- stable UUID and human-facing short ID;
- title and frozen task brief;
- source: prompt, Markdown file, or external ticket;
- project ID;
- immutable creation timestamp;
- workflow state;
- attention state;
- selected repositories and base commits;
- one agent session;
- zero or one runtime environment;
- review and optional publication artifacts.

### TaskRepository

The binding between a task and repository.

Properties:

- repository ID;
- access: read-write or read-only;
- immutable resolved base commit;
- generated branch name for read-write entries;
- worktree path;
- container path;
- current Git observations such as dirty state and ahead/behind counts.

### AgentSession

One native coding-agent session owned by a task. Attention state belongs to the task, not to the session; see ADR-026.

Properties:

- provider adapter ID;
- execution mode: host or devcontainer;
- tmux server/session/window/pane identity;
- provider-native session ID when available;
- process state;
- control-workspace path;
- last observed event sequence;
- creation and last-activity timestamps;
- the execution environment the session runs in, when that is not the host: its
  adapter, its identity, the exact inputs it was started from, the generated
  override, the service and user, and the observed container. The identity is
  recorded because cleanup and reconciliation must resolve what a task owns
  rather than recompute it from configuration that may since have changed; the
  container and its state are observations and are never assumed from the
  record.

### RuntimeEnvironment

The application environment associated with one task.

Properties:

- runtime provider;
- unique runtime identity/Compose project name;
- base and override inputs;
- generated override path;
- service set;
- port assignments;
- network and volume observations;
- lifecycle and health state;
- external/shared resource bindings such as a pre-existing staging database.

The agent execution environment and application runtime are separate even when both use the same Compose project.

### Review

What is known about a task's work: what changed, what the agent claimed about it,
and what the checks found. The user's decision is not here — that is the task's
workflow state, which is the only record of it (ADR-047).

Properties:

- recorded base commit per repository;
- current change summary per repository;
- agent-reported completion summary;
- agent-reported or provider-gated checks;
- when the agent requested review;
- configured diff/editor commands;
- optional publication artifacts.

### ExternalTaskReference

Optional reference to GitHub, Shortcut, GitLab, or another ticket source.

Properties:

- provider;
- external ID and URL;
- snapshot version/time;
- snapshot content;
- change-available indicator.

### ProviderArtifact

Optional issue, PR, or MR connected to a task and repository.

Properties:

- provider;
- repository ID;
- artifact type and external ID;
- URL;
- observed state.

Provider artifacts are roadmap entities and must not be required by the v0 task lifecycle.

### ControlMessage

A structured file exchanged through the task control workspace.

Directions:

- inbox: host-to-agent task/context updates;
- outbox: agent-to-host events and requests.

Examples:

- `review_requested`;
- `runtime_requested`;
- `open_question`;
- `completion_report`.

Control messages never execute themselves. The daemon validates their schema, task ownership, sequence, and requested capability before changing state or presenting an action.

## State dimensions

### Process state

```text
starting | running | idle | stopped | failed
```

This is observable from processes and provider hooks.

### Attention state

```text
none | possibly_waiting | needs_input
```

Feat must remain conservative when the provider cannot distinguish a normal idle turn from a question.

### Workflow state

```text
draft
preparing
working
review_requested
verifying
ready_for_review
verification_failed
changes_requested
approved
archived
failed
```

Workflow and process states are not collapsed. A task can be `review_requested` while its process is `idle`, or `approved` while its runtime is still running.

### Runtime state

```text
absent | creating | stopped | starting | running | degraded | failed | removing
```

Service health is separate from container running state. Without configured health checks, the UI reports `running, health unknown`.

## Invariants

1. One task belongs to exactly one project.
2. One task owns exactly one primary agent session in v0.
3. One task owns at most one application runtime environment.
4. Runtime environments are never shared between tasks in initial versions.
5. A task may bind several repositories.
6. Every selected source repository receives a task-specific worktree in v0; read-only entries are mounted read-only.
7. Every read-write task repository has a task branch.
8. The resolved base commit never changes after task creation.
9. Fetching remotes must not mutate the user's ordinary checkout.
10. tmux identifiers are execution references, not task identity.
11. Docker capability belongs only to the trusted host runtime adapter.
12. Git-provider capability is independent and may be enabled inside the agent environment.
13. A Stop/end-of-turn signal alone cannot produce semantic completion.
14. Destructive cleanup requires exact task-owned targets and confirmation according to resource risk.

## Multi-repository review and publication

A task-level review aggregates repository-level comparisons against their own recorded base commits.

Publication creates at most one PR/MR per changed repository. The task is the parent relationship tying those artifacts together. Cross-repository merge ordering may be represented later but is not part of v0.

