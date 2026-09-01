# ADR-047 — One record of a review decision

Status: accepted
Recorded: 2026-08-09, after use

The maintainer, reading the review surface during dogfood, observed that the
states a user can put a review into have no consequences beyond changing the
workflow state, and asked whether they were worth having. They are — but the
decision was being recorded twice, and that is what the objection had found.

Evidence:

1. Nothing read the second copy. `domain.Review.Status` reached two call sites,
   `reviewDecision` in the TUI and `printReview` in the CLI, and both only
   rendered it. Every behaviour in the daemon reads `Task.Workflow`: the
   transition table, the effects table that returns a prompted task to `working`,
   the notification conditions, and the one place approval changes anything —
   `approvalOffer`, which offers to stop services a user approved a task with
   still running.
2. The copies could disagree, and one action made them. Leaving a review pending
   was the only decision that moved the review's status without the workflow, so
   approving and then leaving pending produced a task whose panel read `workflow
   approved` above `decision pending`. There was no way out: `approved` has no
   outgoing transition, so requesting changes afterwards failed with a message
   about the agent not having asked for review.
3. The action that produced it was never used or tested. It had no test anywhere
   in the tree, and its key was bound on the task panel but absent from the
   panel's hints — advertised only inside the string it would go on to
   contradict.
4. The decision line was offered where the decision was not available. Read from
   the review aggregate, which knows nothing about the task, it showed "A to
   approve" for a task that was still `working`, and the daemon then refused the
   approval that line invited.

Decisions:

- The task's workflow state is the only record of a review decision. The review
  aggregate keeps what is genuinely its own: the per-repository comparisons, the
  agent's claim, and the check results with the reporter of each.
- Leaving a review pending is not an action. A review nobody has decided is
  already pending, so the action existed only to un-decide, which is what moved
  one copy without the other. FR-REV-004's three options are satisfied without
  it: pending is the state a review rests in, approving is a transition, and
  revision is attaching to the agent.
- `EventReviewChanged` carries no from and no to. It records that what is known
  about the work changed — an agent's report, or a gate's results — and the
  user's decision is recorded as the workflow transition it is.
- The decision the TUI renders is derived from the workflow, so the keys appear
  only where the transition exists.
- The stored schema version does not move. Removing a field normally requires a
  version and a migration, which is the rule `internal/store/fs` documents and
  keeps; this removal is exempt because nothing has to be upgraded. The
  information in `status` and `decided_at` is in the task snapshot beside them,
  so an older document loads with the keys ignored and the next save writes them
  away. A test pins that, because it is the only risk the exemption takes. The
  exemption is an early-alpha one and does not generalise: after v0.1 there is
  state in the wild whose owner did not write it.

Consequence: `ReviewStatus`, its constants, `Review.Status`, `Review.DecidedAt`,
`Review.Decide`, and `api.ReviewLeavePending` are deleted, and `decide` in the
daemon collapses to a workflow transition. FR-REV-004 stands as written.
`changes_requested` is deliberately left alone and is the next question: it tells
the agent nothing — no inbox message is written, unlike a gate's verdict — and its
only reader treats it exactly as it treats `review_requested`,
`ready_for_review`, and `verification_failed`. Either it earns its place by
carrying the user's revision note into the session, or it folds into attaching.
**Answered: it folded into attaching, and `approved` went with it, see ADR-086.
FR-REV-004 no longer stands as written.**
