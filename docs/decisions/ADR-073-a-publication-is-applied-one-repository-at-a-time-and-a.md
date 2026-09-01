# ADR-073 — A publication is applied one repository at a time, and a partial one is recorded rather than undone

Status: accepted
Recorded: 2026-08-24, from working through what ADR-070 left unspecified

ADR-071 puts the forge on the repository, and publication is one merge request
per changed repository, so a task can legitimately publish to two forges in one
action. ADR-070 describes what one publication is and says nothing about what
happens when the third of five fails. That is not an edge case: a push crosses a
network to somebody else's server, and the resources it creates are not on this
machine and cannot be un-created reliably.

Evidence:

1. The shape is already decided elsewhere for the same hazard. ADR-029 made task
   preparation plan, record, apply, in that order, so that "every path and branch
   that could exist afterwards is written down first, so no resource can exist
   that the record cannot name, and an interruption at any point is recoverable
   rather than mysterious." Publication has that hazard in a stronger form,
   because a worktree Feat forgot is on the user's disk and a merge request Feat
   forgot is not.
2. Idempotence has a precedent in this codebase and it is an identifier, not a
   timestamp. A control message ID "is what makes replaying an outbox idempotent:
   an identifier Feat already applied is recognised and skipped"
   (`internal/control/message.go:98`).
3. The failures are not correlated in the direction that would justify stopping.
   A protected branch or a missing forge project is true of one repository; a
   broken credential or an unreachable proxy is true of all of them and produces
   the same error however many are attempted.

Decision: publication plans every repository first — forge, remote, base branch,
and the commit each draft describes — records that plan on the task, and then
applies one repository at a time, recording each result before the next begins.

Rollback is rejected. Deleting a merge request that was just opened and
force-deleting a branch that was just pushed is destructive, can fail on its own
account, and cannot recall a notification that has already gone out. CLAUDE.md
requires resolving exact task-owned resources before cleanup and retaining by
default; an automatic rollback across a network is the opposite of that. A
partial publication is therefore a recorded state rather than one to be undone,
and what the user sees is which repositories published and which did not.

A failure on one repository does not abort the others, on evidence 3. Where the
cause is common, the user reads one cause reported several times, which costs
nothing; where the cause is local to a repository, the others land and the user
has one thing to fix rather than an unknown number still unattempted. Stopping
early would turn a single round trip into as many as there are repositories.

Re-publishing skips a repository that already has a recorded merge request, by
evidence 2, and skips it as already published rather than as stale. Keeping those
two reasons distinct is what lets ADR-070's staleness refusal keep one meaning: a
refusal says the agent's draft describes a commit that is no longer current, and
never says merely that this ran before.

What this does not decide is what Feat does about a merge request that was opened
and then closed by somebody on the forge. Observing merge and close state is
Phase 3 work that comes after publication, and a record that is accurate when it
is written is the precondition for observing it later, not a substitute.

Consequence: the task's publication record is per repository rather than per
task, and holds the plan before it holds any result, so that an interrupted
publication names what it had not yet attempted. `internal/daemon` owns the
sequencing, as it owns preparation's; `internal/forge` sees one repository at a
time and knows nothing about the others.
