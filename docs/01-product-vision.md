# Product Vision

## Vision

Software development with coding agents should be organized around independent feature execution, not around manually managed chat terminals.

Feat is a local developer control plane that connects a task to everything required to implement and review it:

- imported or ad hoc task context;
- selected repositories;
- branches and worktrees;
- one native coding-agent session;
- an optional isolated agent environment;
- an optional application runtime;
- checks, changes, review, and publication state.

The mature workflow is:

```text
List the work scheduled for the current iteration
→ select several tasks
→ launch one isolated feature lane per task
→ supervise native coding agents in parallel
→ respond only when attention is required
→ test and review each completed feature independently
→ publish one PR/MR per changed repository
```

## Primary job to be done

> When I have several implementation tasks, let me execute them concurrently from task selection through isolated testing and review without copy-pasting context between systems or waiting idly for one agent.

## Initial target user

1. The product author using a real multi-repository, Compose-based project.
2. Individual terminal-centric developers using Claude Code.
3. Individual developers whose agents run in devcontainers.
4. Later, small engineering teams and developers using additional coding agents.

Feat does not require devcontainers globally. Agent execution is a selectable profile:

- `host`: run the native agent directly in the task worktree;
- `devcontainer`: run it as a non-root user inside a configured Compose service;
- later: sandbox, microVM, remote runner, or other execution providers.

## Initial problem

Git worktrees isolate checked-out source, but do not by themselves coordinate:

- multiple agent terminals;
- task context;
- branches across several repositories;
- application Compose identities;
- mounts, networks, volumes, ports, and database selection;
- agent attention state;
- review while other tasks continue;
- safe cleanup and recovery.

Developers compensate with shell scripts, terminal tabs, handwritten port maps, copied ticket text, manual Compose overrides, and memory. The result is serial testing, avoidable idle time, and uncertainty about which terminal, code, runtime, and logs belong together.

## Product position

Feat is an orchestration layer, not a new coding agent, editor, terminal multiplexer, container runtime, ticket system, or Git forge.

It composes existing tools:

- Claude Code initially; Codex and other agents later;
- Git and Git worktrees;
- tmux;
- Docker Compose;
- the user's editor and diff tools;
- GitHub, GitLab, Shortcut, and other external systems later.

The product owns the relationships and lifecycle among those tools.

## Differentiation

The ultimate differentiator is not merely showing several terminals. It is a unified task-to-PR/MR lifecycle in which task context, agent interaction, repository changes, runtime identity, review, and publication remain linked.

The v0 wedge is deliberately smaller:

- persistent multi-session supervision;
- coordinated multi-repository worktrees;
- optional devcontainer execution;
- manual but task-scoped runtime control;
- attention notification;
- task-scoped review commands and recovery.

## Product principles

### Native tools remain native

Feat attaches to the normal Claude Code TUI, invokes normal Git and Docker Compose commands, opens the user's editor, and preserves tmux keybindings. It does not recreate mature interfaces without a compelling reason.

### Structured state beats terminal scraping

Agent adapters use hooks and explicit control messages whenever the provider supports them. Terminal text heuristics may be a fallback signal but never the sole source of semantic task completion.

### Explicit capabilities

Docker access, host execution, devcontainer execution, and runtime actions are separate capabilities. Enabling one does not silently enable another.

A capability is something Feat can withhold. Network access, Git access, and a provider CLI in the agent's environment are not: Feat restricts none of them, so it declares nothing about them and [05-security-model.md](05-security-model.md) states the exposure directly rather than a setting implying Feat controls it.

### Conservative destruction

Feat may automate creation aggressively. It removes containers, volumes, worktrees, or branches only after resolving the exact target and obtaining the required confirmation.

### Local first

The local daemon, CLI, TUI, adapters, and review workflow are open source. Local operation does not require a hosted account or telemetry.

### Provider-neutral core

Claude Code is the first adapter, not the domain model. Tasks, sessions, runtimes, and reviews must not embed Claude-specific assumptions outside the Claude adapter.

## Open-source and commercial boundary

### Open source

- Local daemon, CLI, and TUI
- Project and task lifecycle
- tmux, Git, worktree, and Compose integration
- Agent adapters
- Local review workflow
- Local ticket and Git-provider integrations
- Local/LAN web client when one exists

### Potential commercial service

- Hosted encrypted relay
- Remote web/PWA access across networks
- Push notifications
- Cross-machine orchestration
- Shared team queues and runners
- Central templates, policy, audit, approvals, and SSO
- Managed preview environments

## Non-goals

Feat will not:

- build or replace a coding agent;
- replace Neovim or another editor;
- become a source-code host;
- become another ticket system;
- provide a generic cloud IDE;
- manage production deployments;
- become a generic container orchestrator;
- assign work among human developers;
- promise fully autonomous software development without review;
- merge PRs/MRs automatically in initial versions;
- defend a normal devcontainer against deliberate kernel or container-runtime exploits.

## Success definition

After one month, Feat is successful if the developer can use a native agentic development workflow without manually copying context between tools and without spending large amounts of time idle while one agent works.

The dogfood release must demonstrate that the developer can:

- run three independent tasks concurrently;
- review one while the other agents continue;
- identify attention needs quickly;
- recover after daemon or computer restart;
- avoid manually coordinating worktree paths, branch names, tmux targets, and Compose identities;
- keep agent containers non-root and without Docker access;
- clean up without losing dirty or unmerged work.

