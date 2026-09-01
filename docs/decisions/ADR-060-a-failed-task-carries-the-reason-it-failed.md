# ADR-060 — A failed task carries the reason it failed

Status: accepted
Recorded: 2026-08-14, from dogfooding the launch refusals

The maintainer, watching a launch fail on purpose to test ADR-059: it is not
clear in the TUI why the task launch failed.

Evidence:

1. The reason existed and was reachable from nowhere. `transition` writes it as
   the `Detail` of the workflow event, so it is in `events.jsonl` on disk, and
   the dashboard's own launch path put it in `m.err` — a banner cleared by the
   next action. A user looking at the task a minute later saw `workflow failed`
   and stopped.
2. Nothing else could have shown it. `api.Task` had no field for it, there is no
   per-task events endpoint, and the dashboard discards the events it streams
   (`case eventMsg: return m, tea.Batch(m.load(), m.awaitEvent())`) — including
   the `Detail` the stream carries. So the one copy was the file.
3. It is every failed task, not every failed launch. A Git apply that fails, a
   resume that cannot start, a session the provider reported as failed: all five
   paths into `failed` go through one call, and all five lost their reason the
   same way.
4. The event log's stated job is that "Feat can explain what happened later"
   ([06-technical-architecture.md](06-technical-architecture.md)). Nothing in the
   product reads it.

Decisions:

- The task carries the reason it failed and the moment it did. The workflow state
  and its explanation are one fact, and a state a user cannot act on is one that
  only describes itself.
- It is recorded by the transition rather than beside it. `FailWith` is the only
  way into `failed` that records anything, so a caller cannot write the state and
  the reason apart, and a blank reason is refused: whatever failed knows what it
  was.
- Leaving `failed` discards it. A recovered task that still carried its old
  reason would be read as failing now, and a stale explanation beside a live
  state is worse than none.
- The reason is kept verbatim. It is the same sentence the caller was given;
  rewording it here would produce a second account of one event.
- It is stored as an optional field at the same schema version, which is what the
  storage codec's own rule allows: a build that adds a field without changing the
  meaning of the existing ones stays readable by the build before it, and a
  snapshot written earlier decodes to no failure — which is the truth about a
  record that never held one. No migration, and none owed.
- The panel prints it under the workflow and wraps it rather than truncating it.
  A reason names a service, a mount, or a path, and every one of those is at the
  end of the sentence.

Consequence: one domain field and one method, one stored field, one DTO field,
and one block on the task panel. The `failed` state itself, the transition table,
and every notification are unchanged. The fixtures grow a second task — a task
carries a reason only while it is failed, so the fixture that has reached review
cannot also cover it, and the coverage tests that keep the round trip honest now
take the union over both. What this does not do is make a task's history
readable: the event log is still a file nothing in the product opens, and a
per-task events endpoint stays a separate piece of work.
