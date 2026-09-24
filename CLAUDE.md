# CLAUDE.md — Implementing Feat

You are implementing **Feat**, a terminal-native multi-agent development control plane. The product specification in `docs/` is authoritative.

## Read before changing code

Read in this order:

1. `README.md`
2. `docs/01-product-vision.md`
3. `docs/03-domain-model.md`
4. `docs/04-functional-specification.md`
5. `docs/05-security-model.md`
6. `docs/06-technical-architecture.md`
7. `docs/07-configuration-model.md`
8. `docs/08-v0-scope.md`
9. `docs/10-decisions-and-open-questions.md` — the decision index. Read it, then open the decisions your change touches; each one is a file under `docs/decisions/`.

Read `docs/02-user-workflows.md` for user-facing behavior. Read `docs/09-roadmap.md` when a change touches something the roadmap defers, so the extension boundary survives. Don't implement roadmap features during v0 unless a recorded decision schedules one. ADR-072 is the only one that does: it moves publication out of Phase 3 and the ticket adapter out of Phase 6, both ahead of the public preview, publication first because the dogfood cannot finish a task without it.

## Current product contract

- Working name and binary: `Feat` / `feat`
- Go, Cobra, Bubble Tea
- One binary with daemon, TUI, and CLI modes
- HTTP/JSON over a Unix-domain socket; SSE state events
- YAML configuration; JSON snapshots; JSONL events; Markdown briefs
- File-backed state behind an interface; no SQLite in v0
- tmux required and product-managed through a dedicated socket
- Claude Code only in v0, behind an agent interface
- Multi-repository tasks from the beginning
- One task owns one agent session and one feature environment
- Devcontainer execution for dogfood; host-native execution beside it from v0.1
- Docker Compose CLI on the trusted host
- Agent receives no Docker socket/host Docker CLI
- Provider work is host-side by default; the agent drafts the words and the user approves them before anything is sent
- `gh`/`glab` run on the trusted host; the agent environment has no provider-CLI declaration and Feat checks none there
- Manual application runtime lifecycle in v0
- External diff/editor commands; no built-in source diff viewer
- Conservative explicit cleanup
- macOS/Linux target
- Apache 2.0; no telemetry

## Scope rules

1. Implement what the change asks for and no more. A prerequisite you have proven is missing counts as part of it. A nearby improvement does not.
2. Do not add automated runtime phases, stable hostnames, Codex, remote control, plugin RPC, team features, or an internal diff viewer during v0. ADR-072 took ticket ingestion off this list. Only a recorded decision changes it.
3. Do not hard-code the reference project's repository names, paths, Compose services, or database behavior.
4. If an accepted decision looks infeasible, stop and record concrete evidence before changing it.
5. An open question is not permission to settle a permanent design early.

## Architectural rules

- Keep domain types independent of Claude, tmux, Docker, GitHub, GitLab, and Bubble Tea.
- Keep provider-specific flags, hooks, event schemas, and parsing inside the provider adapter.
- Keep host and devcontainer execution behind one execution interface.
- Keep application runtime separate from agent execution even if both use Compose.
- Keep storage behind repositories/interfaces; the daemon is the only writer.
- Use argument vectors rather than interpolated shell commands for Git, tmux, Docker Compose, the provider CLIs, and every user-supplied command — tracker, review, and checks.
- Plan, record, then apply, so no resource can exist that the record cannot name and an interrupted lifecycle is recoverable. This matters most for resources on someone else's server.
- Use stable IDs and tagged metadata; never use tmux window indexes or display names as identity.
- Record immutable Git base commits when launching a task.
- Make reconciliation explicit; never assume persisted desired state equals observed state.

## Security rules

- Never mount or expose a Docker socket to an agent container.
- Never add a daemon/runtime-control socket to the agent container.
- Never copy secret values into generated YAML, JSON, logs, or Compose overrides.
- Validate every control-workspace message, path, capability, size, task ID, and event ID.
- A runtime request is inert until host validation and user approval.
- Make credentialed provider calls on the trusted host; the agent environment receives no provider token.
- The user reads agent-authored text before it reaches a durable destination, and what was displayed is what gets sent. Inbound works the same way: the user approves the composed brief, not the ticket behind it.
- Resolve exact task-owned resources before cleanup.
- Retain volumes by default and require explicit confirmation for dirty/unmerged work.
- Do not claim standard containers provide hostile-kernel isolation or network DLP.

## Agent-state rules

- A Claude Stop/end-of-turn event means `idle`, not complete.
- Semantic completion requires an explicit review/completion event.
- Use Claude hooks/control files before considering terminal-output heuristics.
- Keep process, attention, workflow, and runtime states separate.
- Provider-native check failures should return to the native agent loop when configured.

## Quality bar

For every change:

- write focused unit tests for domain/config/storage logic;
- use fake adapters for orchestration tests;
- add opt-in integration tests for real Git/tmux/Compose behavior;
- make failure halfway through a lifecycle recoverable and explainable;
- name the failing project, task, or resource in the error, and leave the reasoning to the event log;
- preserve unrelated user checkouts, tmux sessions, containers, volumes, and configuration;
- run formatting, lint, tests, and build before declaring the work complete.

## Writing

These rules cover comments, error messages, READMEs, and commit messages.

**Sentences.** One idea per sentence, around 25 words. Subject, verb, object, where the subject is a concrete thing — the function, the container, Docker — not an abstraction. Cut any sentence whose job is to restate the one before it. No aphorisms: if a line sounds quotable, rewrite it. Don't quote this file or the specs back into the code.

Bad: *An unanswerable question refuses. Feat cannot say the tree is unheld while Docker will not say what it holds.*

Good: *If Docker won't report container state, this refuses rather than guessing.*

**Comments.** Say why, not what: a constraint, a non-obvious reason, a pointer to the decision that settled it. Three lines is the budget for a comment on a declaration. A block that explains a whole package or a whole file carries the long version; the package one lives in `doc.go`. Don't narrate the implementation or record what the code used to do — Git holds that.

Rationale lives in the ADRs under `docs/decisions/`. A comment cites the decision by number and stops: `// ADR-059: the containers must be gone before the tree is removed.` Don't re-derive the argument in the code. If a comment holds a fact its ADR doesn't, move the fact into the ADR.

Match the surrounding code's naming and idiom. Do not match its comment register. Most of this codebase predates these rules; new comments follow them anyway.

**Errors.** One sentence: what failed, which resource, what to do about it. Under 100 characters. Reasoning and context go in the event log, not the message.

**Commit messages.** Subject in the imperative, under 72 characters, plain. "Ask the wizard for forge and tracker, and reorder its questions", not "Reach the forge and the tracker, and ask the application before the agent". A body only when the change needs a reason the diff doesn't show, and then three or four sentences. Cite the ADR instead of restating it. No bullet list of what changed, no test plan.

**Review notes.** `CLAUDE-NOTE:` marks something that helps review a change and stops being useful afterwards. Prefix every line, four lines maximum, and use it inline only when the note is about a specific line. Never commit one.

## Definition of complete

Work is complete when the promised behavior is verified, not when the code that would produce it exists. A feature without recovery, validation, and safe failure behavior is not finished.

Prefer fake adapters and deterministic harnesses over requiring real Docker, tmux, or Git in every unit test, and add opt-in end-to-end tests for the real tools. Never expose Docker to the agent to simplify an implementation. Never replace structured agent events with terminal scraping as the semantic source of truth. When evidence changes an accepted design, write the ADR as a file under `docs/decisions/` and add its row to the index in `docs/10-decisions-and-open-questions.md`, in the same change.
