# Roadmap

Roadmap items are ordered by product value and dependency, not promised dates.

## Phase 0 — v0.1 dogfood

Goal: prove the product on the real multi-repository company project.

Deliver the exact scope in [08-v0-scope.md](08-v0-scope.md), centered on:

- Claude in a non-root devcontainer;
- multi-repository worktrees;
- tmux supervision;
- manual task-scoped Compose lifecycle;
- attention notifications;
- external diff/editor workflow;
- recovery and safe cleanup.

Exit signal: three concurrent tasks can be implemented and reviewed with less coordination and idle time.

## Phase 1 — v0.2 public preview

Goal: make the local core usable outside the reference project.

- host-native execution;
- macOS and Linux support;
- generalized config and diagnostics;
- documented Claude adapter installation;
- release binaries, Homebrew, and `go install`;
- Apache 2.0 project setup;
- no telemetry;
- optional Shortcut only if it does not delay reliability.

## Phase 2 — automated runtime lifecycle

Highest post-v0 priority.

Goal: given a task and repository selection, create an independently addressable application environment without hand-edited Compose overrides.

Capabilities:

- generated task override;
- automatic port allocation from configured ranges (**delivered in v0.1**: a host
  port per reachable service per task, held while the runtime exists and released
  when it is destroyed, because the reference project could not run two tasks
  without it — see ADR-065);
- task labels and deterministic Compose identity;
- Compose health-based readiness;
- configurable lifecycle phases;
- user-approved agent `runtime_requested` messages;
- runtime retained through review;
- stop offered after approval;
- generic provision/migrate/seed/destroy hooks;
- managed, shared, and external resource semantics;
- shared PostgreSQL with database-per-task as a generic optional mode;
- stale-environment reporting.

Do not introduce opinionated framework templates until several real projects reveal recurring needs.

## Phase 3 — GitLab and GitHub workflows

Goal: close the task-to-PR/MR loop while preserving native provider tools.

Order:

1. GitLab for company dogfooding.
2. GitHub as the primary public integration.

Capabilities:

- validate `glab`/`gh` inside the agent environment;
- allow Claude to create commits, push, and publish when prompted;
- discover one MR/PR per changed repository;
- task-level linking of related provider artifacts;
- issue/ticket linking;
- observe merge/close state and offer cleanup;
- no automatic merging.

Host-side provider execution may be added as another configured mode, but container-side native CLI access is a first-class path.

## Phase 4 — stable local hostnames

Goal: replace mentally expensive port maps with stable per-task URLs.

v0.1 built the half this phase routes to: every task's reachable service has an
allocated host port and an address Feat tells its own services (ADR-065). What is
left is the half that makes a URL stable across tasks, which is the proxy below.

Capabilities:

- local reverse proxy;
- hostnames derived from project/task/service;
- automatic route registration and removal;
- TLS only if local developer experience justifies it;
- dashboard links;
- preserve direct ports for debugging.

## Phase 5 — additional agent adapters

Goal: validate the provider-neutral core.

Priority:

1. OpenAI Codex
2. Other terminal coding agents based on demand
3. Arbitrary command adapter for advanced users

Each adapter defines launch, hooks/events, native attach behavior, completion semantics, configuration, and capability validation without changing the task domain.

## Phase 6 — ticket ingestion

Goal: eliminate copying task context between planning systems and agents.

Priority:

- GitHub Issues: issue number/URL and repository lists first, Projects iterations later;
- Shortcut: current iteration assigned to the user or team;
- GitLab Issues later if demanded.

Behavior:

- immutable task snapshot;
- comments excluded by default and selectable;
- change notification without automatic context mutation;
- issue/ticket reference carried into publication.

## Phase 7 — remote web/PWA control

Goal: monetize secure access to the local multi-agent control plane from another device.

Open-source side:

- local/LAN responsive web client.

Commercial side:

- hosted outbound relay;
- device pairing;
- end-to-end encrypted state and terminal transport;
- push notifications;
- cross-network access;
- no persistent source or transcript storage;
- last-known non-sensitive state stored on the client;
- individuals first.

Working MVP capabilities:

- task overview;
- attention notifications;
- terminal streaming and input;
- runtime request approval and manual actions;
- change summary.

Full phone-based code review and native mobile apps require usage evidence.

## Phase 8 — natural-language orchestration

Goal: support requests such as “list this iteration and implement X, Y, and Z.”

Open decision:

- master native agent with constrained orchestration tools; or
- host-integrated model translating language into a previewed action plan.

Any design must display the exact proposed tasks, repositories, bases, capabilities, and runtime actions before execution.

## Phase 9 — additional runtime backends

Possible backends:

- hardened container runtime;
- local microVM/sandbox;
- remote Docker host;
- Kubernetes/dev environments;
- managed runners.

This phase also addresses stronger hostile-code isolation and task-local Git metadata.

## Phase 10 — team and governance

Only after individual usage proves the core workflow:

- shared task queues;
- runner ownership and scheduling;
- roles and permissions;
- approvals and audit trails;
- centrally managed templates and policies;
- SSO;
- managed preview environments.

## Roadmap exclusions

The roadmap does not include becoming a coding agent, editor, ticket system, production deployment platform, generic container orchestrator, or autonomous merge bot.

