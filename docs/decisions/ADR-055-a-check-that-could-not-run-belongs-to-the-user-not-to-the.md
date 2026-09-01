# ADR-055 — A check that could not run belongs to the user, not to the agent

Status: accepted
Recorded: 2026-08-12, from the first real feature run on the reference project

ADR-036 drew the distinction and the code kept it: a check that could not be
started, or that exceeded its bound, is recorded as `unknown` with the reason,
and never as a failure it did not have. One line then threw it away.
`review.Decide` computed `Passed = Failed == 0 && Inconclusive == 0`, and
`finishGate` transitioned on that single boolean, so a check nobody managed to
run and a check that ran and failed produced the same state, the same verdict to
the waiting agent, and the same notification.

The run that found it: a check was configured as `pytest`, the reference project
runs its tests through a wrapper, and the bare program was not on the path inside
the agent's environment. Everything ADR-036 designed worked — the helper blocked,
the failure returned into the agent's loop, and the agent diagnosed it, named the
configuration file, and declined to edit the configuration governing its own
gate, which is the right refusal, because an agent that chooses its own check
command certifies itself. What followed had no exit. The task rested in
`verification_failed`, which says the work failed its checks; the agent could not
fix it; and the person who could was told through a workflow state rather than
asked.

Evidence:

1. The information exists at every layer and is discarded at the one point that
   decides what to say. `Gate.run` records the reason a check could not start,
   `Verdict` counts `Inconclusive` separately from `Failed`, the review record
   stores both, and the review screen has always had a "did not report" column.
   Only the verdict's boolean, and the three expressions of it in `finishGate`,
   could not tell them apart.
2. The two failures belong to different people, and only one of them is about
   the code. A failing test is evidence the agent can act on. A missing program,
   an unreadable directory, or an environment that could not be rebuilt is a
   statement about the project's configuration — which is the user's, and which
   the agent must not edit.
3. `internal/notify` had no condition for it, so there was no way to say it even
   where the daemon knew. Its conditions are pinned tables keyed by state, and
   the state a blocked run should produce is one the tables already map to
   something else.
4. The product already has a state for "the agent asked and Feat has no verdict".
   A project that configures no checks leaves the task in `review_requested` with
   the agent's own report beside it, which is what
   [02-user-workflows.md](02-user-workflows.md) §6 describes: the request stands
   and a person decides. A gate that could not run is the same situation reached
   by a different route.

Decisions:

- `review.Decide` returns a three-valued `Outcome` — `passed`, `failed`,
  `blocked` — and the boolean is removed rather than kept beside it. One record
  of one thing, which is the rule ADR-047 applied to the review decision.
- Failure outranks a check that never ran. A run holding both is `failed`,
  because a check that reported is evidence about the work and the agent can act
  on it, and the checks that did not run are still named in the report and on the
  review screen. The blocked route is for a run that established nothing.
- A blocked run leaves the task in `review_requested`. No workflow state is
  added: the task is exactly where evidence 4 puts it, the recovery edge is the
  one `verify` already uses, and a state added for this would have to be
  explained everywhere `verification_failed` is, to say something the review
  screen and the task's history say better.
- The user is interrupted by a new condition, `verification_blocked` — "its
  checks could not run". It is named by the daemon that ran the gate rather than
  looked up from the task's new state, because `review_requested`'s own condition
  is the one ADR-036 suppresses while a gate is about to run: looking it up would
  announce a blocked gate as a fresh review request, or as nothing at all.
- The notification says no more than that. Naming the check would mean putting a
  configured value into a message that leaves the machine, and `notify.Subject`
  exists to have nowhere to put one. The naming happens where the user then
  looks: the task's history names the check and its repository, and the review
  screen carries the reason each one gave. The check's own output stays out of
  the event stream, as ADR-036 evidence 6 requires.
- The waiting agent is answered with a new verdict status, `blocked`, and the
  generated helper exits **zero** on it. The failing exit status is ADR-036's
  route back into the native loop, and a configuration the agent cannot fix is
  the one thing that must not travel it. The report says why, so the session is
  not left to infer that the failure was not its own, and tells it not to change
  the configuration that decides how its work is verified. A helper generated
  before this change reads the status through its unknown-status branch, prints
  that it did not understand it, and exits zero — so a session already running
  when the daemon is upgraded degrades to the same non-failure.
- The review screen renders a check that did not report as "did not report"
  rather than as `unknown`, in the words the summary line above it already uses,
  and in amber rather than red: it needs the user, and it is not a verdict
  against the work.

Consequence: one workflow state fewer than the alternative, one notification
condition more, one protocol status more, and no stored format change — a
`Check` has carried `unknown` since slice 1. `verification_failed` narrows to
what it says, and a project whose check command is wrong now interrupts the
person who can fix it, on a task whose state does not claim its work was
measured.
