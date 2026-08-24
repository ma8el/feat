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
- source: prompt, Markdown file, or external ticket, with the ticket reference
  itself where the brief was composed from one;
- project ID;
- immutable creation timestamp;
- workflow state;
- why it failed and when, while the workflow state is `failed` and never
  otherwise: the state carries its own explanation, because a user who can read
  one and not the other is told that something went wrong and not what
  (ADR-060);
- attention state;
- selected repositories and base commits;
- one agent session;
- zero or one runtime environment;
- review, and the optional publication record below.

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
- the composition: one entry per repository that brings Compose files, each with the directory its own relative paths resolve against;
- override and environment-file inputs;
- generated include and override paths;
- service set;
- per-service code provenance: for each managed service, the repositories that
  asked Feat to manage it, those whose task worktree it mounts, and those whose
  task worktree its image is built from. A service with neither is one running
  the user's ordinary checkout, which is a state rather than a note because
  nothing else about it is visible: the containers start, the application serves,
  and every other record stays correct;
- allocated host ports: for each service a repository declares reachable, the
  port inside the container, the host port Feat reserved, the protocol, and the
  host address the project publishes on. They are an input rather than an
  observation, and they are held: while the runtime exists no other task may be
  given one of them, and a runtime that becomes absent releases them all;
- port assignments observed on the started containers, which is what the
  allocations turned into and is empty until something is running;
- network and volume observations;
- lifecycle and health state.

Provenance is resolved from configuration and the project's own Compose files
when the runtime is resolved, not inspected out of the containers afterwards, so
it is answered before anything is created. It is not one of the frozen inputs: it
follows the mounts and build contexts of the generated override, which are
resolved from current configuration every time that document is written.

The allocations are on the other side of that line, with the identity and the
file list: a published port is bound by a running container, so re-resolving one
from edited configuration would move a task's address out from under the
containers holding it — and it is what other tasks are kept away from, which
only works while it is the recorded value.

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
- configured diff/editor commands.

What a task published is not here. It is the publication record on the task,
because it holds a plan before it holds a result and a re-publication reads it
back to skip what already published (ADR-073); a second copy beside the review
would be a second answer to what exists on a forge.

### ExternalTaskReference

Optional reference to GitHub, Shortcut, GitLab, or another ticket source. It is
provider-neutral because a tracker is a configured command rather than an
adapter per service, and what Feat holds is the shape it publishes as
`schema/feat-tickets.schema.json` (ADR-071).

Properties:

- provider, which is what the published shape's optional source fills and is
  absent for a project drawing on one tracker;
- external ID and URL;
- snapshot content: the title, body, and state the tracker's command printed,
  and nothing richer;
- snapshot time, which is what versions a snapshot: the published shape carries
  no revision of the tracker's own, and a change is found by running the command
  again and comparing;
- change-available indicator.

### Publication

What publishing one task's work would do, and what came of it. It is optional
and belongs to the task.

Properties:

- one entry per repository, in the order they are applied, each holding the
  plan — the forge, the remote, the base branch, and the commit the agent's
  draft describes — before it holds any result;
- per entry, whether that repository is still planned, published, or failed;
  the merge request that was opened, or the reason it was not; and when it was
  attempted;
- when the plan was recorded, and when the record last changed.

The plan is recorded before anything is attempted and every result before the
next repository begins, so an interrupted publication names what it had not yet
attempted rather than leaving it to be discovered on a forge (ADR-073). Nothing
here is rolled back: a partial publication is a recorded state, because a merge
request that was just opened cannot be un-created reliably and a notification
that has gone out cannot be recalled. Re-publishing keeps every merge request
already opened and skips that repository as already published, which is distinct
from refusing one as stale.

### ProviderArtifact

Optional issue, PR, or MR connected to a task and repository.

Properties:

- provider;
- repository ID;
- artifact type and external ID;
- URL;
- observed state.

Provider artifacts are roadmap entities and must not be required by the v0 task
lifecycle. Discovering the merge request of a changed repository is not among
what they are for any more: that existed because the agent published and Feat
had to find the result, and a host that opens the request records it in the
publication above instead (ADR-072). What is left for them is observing the
merge and close state of a request Feat already knows about.

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

