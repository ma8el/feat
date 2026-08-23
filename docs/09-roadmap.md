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

The v0.1 dogfood milestone is complete. What is left before a new macOS or Linux
user can run Feat outside the reference project:

- **Host-native execution.** The task domain, the execution interface, and the
  provider adapter already separate the agent from the environment it runs in;
  what is missing is the second implementation behind that interface.
- **Linux notifications.** `internal/notify` reports its own absence on Linux
  rather than pretending to deliver. The attention badges in the dashboard work
  on every platform and are unaffected.
- **Generalized configuration and diagnostics**, including a documented
  installation of the Claude adapter itself, so that `feat doctor` on a fresh
  machine says what is missing rather than what is broken.
- **Generalized examples and troubleshooting**, written against
  [07-configuration-model.md](07-configuration-model.md)'s account of what a
  project's own Compose files must provide. That section is the specification
  half, established by making the reference project's two applications run per
  task. What a public reader still needs is a worked pair of Compose files and
  troubleshooting entries for the two failures that are silent: a service
  serving the ordinary checkout, and a second task that will not start.
- **A finalized JSON Schema and shell completion.** The schema in
  `schema/feat-project.schema.json` is kept in step with the Go types by a test
  that compares field names in both directions; publishing it is what makes it a
  compatibility surface. The generated completion command is registered and
  hidden until it is supported.
- **Machine-readable output for the reading commands.** Every command prints a
  table a person reads and nothing else can parse, so a user scripting around
  Feat has the socket or screen-scraping. `task list`, `task review`,
  `runtime status`, and `project show` are the ones with something to say, and
  the schema they would publish is the one this milestone finalizes.
- **Release packaging:** binaries, a Homebrew formula and tap, and `go install`
  instructions.
- **A contribution and security policy**, including the known security
  limitations: what a standard container does and does not protect against, that
  Feat claims no hostile-kernel isolation and no network data-loss prevention,
  and that full Git and provider CLI access are capabilities a project grants
  deliberately ([05-security-model.md](05-security-model.md)). The reader of
  "what this does not protect against" is somebody deciding whether to run Feat
  on their own work, which is a person this milestone introduces and the dogfood
  milestone does not have.
- **The path from a clean installation to a first running task**, documented and
  checked on a machine that has never run Feat. Reproducing a setup from
  documentation is a public-v0 property: [08-v0-scope.md](08-v0-scope.md) puts it
  in the definition of done for public v0 rather than in the v0.1 acceptance
  criteria, and it is best written against what the dogfood runs turned out to
  need.
- **A second pass over the onboarding wizard**, against what public users hit.
  `feat project init` (ADR-062) exists because dogfooding showed manual
  configuration to be the hardest step; what is left is whatever a machine that
  has never run Feat turns out to need, which the first-task documentation above
  is written against. Two findings from running it against the reference project
  are held for this milestone:
  - the managed-services proposal offers every service a repository's files
    declare, including a database that runs none of its code, so a user
    accepting the proposal manages more than the project meant;
  - the agent's environment is answered before the application, so the agent's
    Compose question cannot exclude the files the application will claim. It now
    proposes nothing rather than guessing, and asking the application first would
    let it propose what is left — which reorders the whole conversation.
- **Verified absence of telemetry.**
- **Shortcut**, only if all core reliability work is complete.

Done when the public-v0 definition of done passes on macOS and Linux, host-native
and devcontainer modes use the same task domain, the installation and first-task
documentation is reproducible on a machine that has never run Feat, and the known
security limitations are stated where somebody deciding to run Feat on their own
work will read them.

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

