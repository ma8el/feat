# ADR-026 — Domain and storage modelling

Status: accepted  
Recorded: 2026-08-04

Evidence found while implementing the domain and the file-backed store:

1. [03-domain-model.md](03-domain-model.md) listed an attention state on both `Task` and `AgentSession`. They answer the same question, and the dashboard, the notification policy, and review all ask it of the task. Two copies would be two sources of truth for one answer.
2. Invariant 8 says the resolved base commit never changes "after task creation", while FR-TASK-003 requires a draft to show resolved bases before anything is created. Both hold only if a draft is not yet a created task.
3. Workflow state is decided by Feat, while process, attention, and runtime states are observed. Applying a transition table to an observation would reject what the world actually reported and turn stored state into a claim rather than a record.
4. The stored file format is a compatibility surface with a documented migration policy. If the documents were the domain structs, renaming a Go field would silently change the format of every existing state directory.
5. Events need a total order per task, and a caller supplying its own sequence numbers would have to know what every other caller had already written.

Decisions:

- Attention state is recorded on the task only. The session keeps process state, which is genuinely per-session. [03-domain-model.md](03-domain-model.md) was updated in the same change.
- A task's shape — title, brief, repository selection, and resolved bases — is mutable while it is a draft and frozen when it leaves draft. That single rule carries FR-TASK-003, the frozen brief, and invariant 8.
- Workflow transitions are checked against a transition table plus the preconditions of the target state; observed dimensions validate values only. Both failures are typed errors that name the task and what is missing.
- Stored documents are a separate versioned representation inside `internal/store/fs`, not the domain types. Golden files pin the format, so changing it requires a new schema version and a migration. The document a migration replaces is retained as `<file>.v<version>.bak`.
- The event log assigns sequence numbers on append, starting at 1, and reports an incomplete final record rather than hiding it.
- The human-facing short task identifier is derived from the task UUID rather than stored beside it, so the two cannot disagree. A key collision within a project is resolved by generating another task identifier.

Consequence: no product behaviour, milestone, or scope boundary changes. The rules above are checked by unit tests in `internal/domain` and `internal/store/fs`, and the format is checked by golden files.
