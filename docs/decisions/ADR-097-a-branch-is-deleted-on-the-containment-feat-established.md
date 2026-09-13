# ADR-097 — A branch is deleted on the containment Feat established, not the one `git branch -d` asks about

Status: accepted
Recorded: 2026-09-13, with the implementation

Evidence, from this machine's own event log and from the code paths it names:

1. Task `0e65edb2`, 2026-08-23: eight consecutive failed cleanups between 12:06
   and 12:09, each the same failure, and then at 12:10 "nothing was left to
   remove for the branches" — which is what a `git branch -D` run by hand in
   another window looks like from here. The user resolved it outside the product
   because nothing inside it could.
2. The command that failed was `-d`, not `-D`:
   `` `git branch -d -- feat/0e65edb2-test` failed with exit code 1: error: the
   branch 'feat/0e65edb2-test' is not fully merged``.
3. Two different questions were being treated as one answer. Feat's plan asks
   whether the branch is contained by the ref it branched from —
   `IsAncestor(refs/heads/<branch>, <base_ref>)` in `internal/git/cleanup.go`,
   against the recorded `base_ref: refs/remotes/origin/main` — and the answer was
   yes. `git branch -d` asks whether the branch is contained by the checkout's
   HEAD, or by the branch's own upstream, which a task branch never has; the
   local `main` was behind `origin/main`, so the answer was no. Both were right.
4. The force flag was derived from the warnings, so a plan with no warnings had
   no path to `-D`. Feat concluded merged, `branchTarget` attached no warning,
   `Target.Risky()` was false, `RemoveRequest.Force` was false, and
   `DeleteBranch` sent `-d`. There was nothing at risk for the user to confirm,
   so no selection they could make changed the command.
5. And the task could not be archived by any selection either. `Plan.Check`
   refuses an archive while a class with a present target is left out
   (FR-CLEAN-001, ADR-059), so selecting the branch failed and not selecting it
   refused the archive. There was no third choice: the deadlock was permanent
   rather than a bad morning.
6. This is the ordinary state rather than an edge case. Under the `base_policy:
   remote` the reference project uses, every checkout sits behind its remote
   between one pull and the next, and Feat fetches on its own during planning
   without ever moving HEAD (FR-GIT-001). The two answers therefore disagree for
   any task whose branch was not merged locally.
7. The error was in Git's terms and contradicted the plan the user had read a
   second earlier, which is why eight attempts looked worth making.

Decisions:

- The warning keeps its question. Whether the branch is contained by the ref the
  task branched from is the better question and the one the warning text already
  names, and nothing about the user's decision changes. A branch the base ref
  does not contain warns exactly as before and is deleted only from a
  confirmation given against that warning (FR-CLEAN-003).
- The flag stops being derived from the warning. Git will not answer "is `-d`
  safe" against the base ref, so Feat decides the flag from what it established:
  a branch Feat has determined is contained by its recorded base is deleted with
  `-D`. A branch that is not contained is forced only by the confirmation, and a
  branch that is neither is deleted with `-d`, which is the conservative
  remainder rather than a fourth case.
- What replaces Git's refusal is Feat's own, and this is the part not to skip.
  `DeleteBranch` leaned on Git as a second gate behind the confirmation rule —
  "the second refusal is the one that still holds if the first is ever wrong" —
  and a decision that removes a safety owes a sentence saying what took its
  place. What took its place is the containment check against the recorded base
  ref, which is the narrower question of the two: it is asked about the ref the
  branch was actually made from rather than about whatever HEAD happens to be,
  and it is asked of Git, by `merge-base --is-ancestor`, at the moment of
  removal. Where it cannot be answered — no base ref recorded, a base ref that is
  no longer there, a comparison that failed — the answer is no, which warns and
  asks. So the gate is narrower and still a gate; what it is not is a second
  opinion from a different question.
- The containment answer travels on `reconcile.Target`, beside the warnings and
  outside the plan token. It describes the same resource rather than a different
  one, so a branch that became contained between the plan being displayed and
  the cleanup being executed is the same target with a fresher answer, not a plan
  that has gone stale — the reason ADR-037 keeps the warnings out of the token,
  applied to the field that sits next to them. Execute re-resolves the plan
  before removing anything, so what the adapter is given is what was true at the
  moment of removal. The daemon does not re-ask Git separately: a second question
  asked after the plan was checked would be an answer nothing had validated
  against the plan the user was shown.
- The removal records that it forced and on what evidence. `DeleteBranch` reports
  the flag and the reason, the cleanup carries it as a note on the removal, and
  the event log prints it beside the branch. A removal that overrode a tool's own
  refusal should leave an account of why it was allowed to, and the log is where
  a question asked weeks later is answered.
- A deletion that fails anyway explains itself in the plan's terms first. The
  error names the flag Feat chose and why it chose it, and wraps Git's message
  rather than leading with it. Evidence 1 is what a failure that only quotes Git
  costs: the message contradicted the plan, so the same attempt looked worth
  repeating eight times.

Three things this deliberately does not do:

- It does not let a task be archived over a resource Feat could not remove. The
  evidence behind ADR-059 is a task archived over containers nothing could then
  find, and the rule that came out of it is the one that kept this task from
  disappearing with a branch behind it. The deadlock was the removal's to fix.
- It does not force unconditionally. A branch with commits the base ref does not
  contain still warns, and is still deleted only from a confirmation the user
  gave against a warning they were shown.
- It does not touch the worktree path checks. `CheckWorktreePath` and the
  broad-path rule run again immediately before anything is deleted, and nothing
  here reaches them.

Consequence: `BranchTarget.Merged` is renamed to `BranchTarget.Contained`,
because the defect was two questions sharing a word and the rename is what keeps
them apart in the code as well as in the log; the warning text a user reads is
unchanged. `RemoveRequest` gains `Contained` and the `BaseRef` that names the
evidence, `DeleteBranch` returns what it did rather than a bare bool, and
`api.CleanupRemoval` gains the note the event log prints.

The tests are in three places, because the defect needed all three to be missed.
The flag decision is a unit test against a fake whose `branch -d` refuses a
branch the checkout's HEAD does not contain, which is what the fake did not model
before; the deadlock is a daemon test that cleans up and archives in one pass
against a fake that says contained and not-fully-merged at the same time; and the
disagreement itself is an opt-in test against real Git, because it exists only in
a repository whose remote is ahead of HEAD and no fake can establish that `git
branch -d` really asks about HEAD. That test asserts Git's refusal before
asserting Feat's deletion, so it fails rather than passes vacuously if Git ever
changes the question.
