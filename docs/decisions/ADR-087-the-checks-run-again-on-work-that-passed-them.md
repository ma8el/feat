# ADR-087 — The checks run again on work that passed them

Status: accepted
Recorded: 2026-08-30, with the implementation

The maintainer read a task that was ready for review, changed something in the
worktree themselves, and pressed `V` to run the project's checks against it. Feat
refused: "task X is ready_for_review, and the checks run for a task whose agent
has asked for review" — about a task whose agent had asked for review, and whose
checks Feat had run because of it minutes earlier.

Evidence:

1. `verifyNow` (`internal/daemon/review.go`) accepts `review_requested` and
   `verification_failed` and nothing else. Both are the states a gate can leave a
   task in *except* the one where it passed, so the rule is not "the agent asked"
   — it is "the checks have not succeeded yet".
2. The edge this needs is the one that already exists twice.
   `verification_failed → review_requested` is in the table for the agent that
   fixed what the gate caught, and `verifying → review_requested` for a run a
   restart interrupted; both return a task to the request it came from so the
   gate can decide again. `ready_for_review → review_requested` is the third
   outcome of the same gate taking the same road back.
3. It adds no path *into* a review state. `TestIdleIsNotCompletion` asserts that
   no state outside `review_requested`, `verifying`, and `ready_for_review`
   reaches `ready_for_review`, and this edge leaves `ready_for_review` rather
   than entering it. The test passes unchanged, which is the evidence rather than
   the argument.
4. A user-run gate that fails has somewhere to land: `verifying →
   verification_failed`, which exists, means exactly that, and is a state `V`
   already runs from — so a user who breaks the work while reading it can fix it
   and re-run without the agent.
5. The background half of a gate bails when there is nothing to run, and it bails
   *after* the task has been returned to `review_requested`. A run started with
   no checks configured therefore strands the task there: no gate is left to move
   it, and the only ways out are the agent asking again or the user attaching and
   typing. Reachable today from `verification_failed` when a project's checks
   were removed since the gate ran, which is the situation of somebody who has
   just been editing configuration.

Decisions:

- **`ready_for_review` gains one edge, to `review_requested`, and nothing else
  changes in the lifecycle.** No new state, no second verb, no new endpoint, and
  no field: because the run goes through `verifying` as every other gate does,
  that state goes on meaning "the checks are running" and every screen, event,
  and recovery path already built for it works untouched.
- **The gate is the only thing that runs checks.** The alternative considered and
  rejected was a check run separate from the gate, which would record results and
  move nothing — needed if `V` were to work from `working` as well. It costs a
  running marker on the review record, a second recovery path for a run a restart
  interrupted, an API field, and a display that no longer keys off the workflow.
  Running the checks from `working` is not what was asked for, and evidence 4 is
  why the cheaper shape is also the better one: a bare run has nowhere to put a
  failure, and this one does.
- **A run with nothing to run is refused before anything moves**, naming the
  project rather than the state. Evidence 5 is a trap this change would widen and
  is a latent one today, so `verifyNow` asks `taskChecks` first and leaves the
  workflow alone. Plan, record, then apply, applied to a transition rather than
  to a resource.
- **A run that a restart interrupts is recovered as it already was.** The task is
  in `verifying`, `reconcileReviews` finds it, returns it to `review_requested`,
  and reports it with the action that runs it again. Nothing about that path
  knows or cares which key started the run.
- The desktop notification a landing sends is not suppressed for a user-started
  run, because it is not suppressed for one started from `verification_failed`
  either and this changes nothing about that. If it becomes noise it is one
  condition in `finishGate`, and it is the same condition for both.

One accepted cost: a check running against a worktree the agent is also writing
to can fail for reasons that are nobody's fault. `gates.claim` prevents two runs
at once and cannot prevent that. The results are recorded as what the run found,
which is what they are.

Consequence: one row of `workflowTransitions` and its reason, one state added to
one `switch`, and one guard before a transition.
[03-domain-model.md](03-domain-model.md) and
[04-functional-specification.md](04-functional-specification.md) FR-REV-004 move
with it, and [02-user-workflows.md](02-user-workflows.md) §8 gains the sentence.
No API change, no stored-format change, no schema version, and no client change:
`V` is already on the panel and already says "run checks".
