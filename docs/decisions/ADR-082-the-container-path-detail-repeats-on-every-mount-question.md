# ADR-082 — The container-path detail repeats on every mount question, because this is the field whose wrong answer says nothing

Status: accepted  
Recorded: 2026-08-29, settled with the maintainer, from walking the configuration interface end to end

`Question.Detail` is documented as set "on the first question of a group rather
than on every question of one", and the two mount groups are the first place
that convention is departed from. The convention is not replaced. It is right
where it was written for — a section's opening question, the first repeat of a
file loop — because there the second question of a group differs from the first
only in which file or which repository it names. It is wrong for these two,
and this ADR exists so that the next block of prose has to argue its own case
rather than cite this one.

Evidence:

1. **The rule that decides the field is stated in no question.** Compose merges
   a service's `volumes` on the target, and Feat's generated override is merged
   last, so an entry whose target matches one in the base file replaces it and
   an entry whose target matches nothing is added beside it. Everything about
   both container paths follows from that sentence, and it is in
   [07-configuration-model.md](07-configuration-model.md), in ADR-065, and in
   the header of every generated override — which is to say everywhere except
   where the value is decided.
2. **The explanation was inverted with respect to the risk.**
   `agent.container_path`, whose failure is *caught* — a launch refuses a
   container that mounts a configured repository checkout — carried a detail
   block. `runtime.container_path`, whose failure is *silent* by this product's
   own account (ADR-065 evidence 7), carried none at all. The field with no
   safety net had the least explanation.
3. **The group is where the explanation is needed and is where the convention
   removes it.** For the reference project the agent's group is three
   repositories. The first is asked with a paragraph; the second and third are
   asked `Mount point for <id>` and nothing else. A user answering the first has
   just read it, and a user answering the third has scrolled past it — while the
   answers are not even the same kind of value: where the files mount the
   repository nowhere the path is Feat's own to choose, and where they do mount
   it the path is a fact about somebody else's Compose files.
4. **A proposal read out of the user's own file arrived looking like one Feat
   invented.** Both mount questions report their readings only in the negative:
   an entry that could not be read is named, and a path transcribed out of a
   named Compose file is proposed with nothing beside it. Accepting a
   transcription is always right; accepting `/srv/<id>` where those files did
   say something Feat could not read is the mismatch case. The two questions
   were identical to look at.

Decisions:

- **The detail repeats on every question of both mount groups.** Not the first
  of each. Both say it in the same words, because it is one rule: Feat
  generates an override at the answered path, so an answer that is not the
  mount point the project's own Compose files already use leaves the worktree
  and the repository mounted simultaneously. The merge mechanics behind that —
  that Compose matches on the target and the source beside it is irrelevant —
  are deliberately not spelled out in either: what a user acts on is "the exact
  same mount point", and the mechanics are in
  [07-configuration-model.md](07-configuration-model.md) for the reader who
  wants them.
- **The repeat may be shorter than the first, and may not drop the warning.**
  The first question of each group opens by saying what the field is, breaks,
  and then warns; the repeats are the warning alone, in four or five lines. The
  sentence naming the other container path as a separate question was in the
  agent's block and was dropped in review: the warning is what this group
  repeats, and the two fields are told apart by their prompts and by the
  section each is asked in.
- **`stageRuntimeMount` gains a detail at all, and it is the one that says the
  failure is silent.** A mismatch here is not an error: two live mounts, the
  task's worktree at the answered path and the ordinary checkout still at the
  path the file gives, the services going on reading the checkout, and no error
  anywhere. What the started containers turn out to mount is still reported
  afterwards (ADR-034), which is a report on a runtime that already ran rather
  than help with the answer being typed.
- **A proposal read out of a Compose file names the file, as a `Notes` entry on
  the branch that added none.** It follows the `Notes` contract exactly — what
  the previous answer established, and what a question found out about its own
  proposals — and it is the smallest change here and the most valuable: it is
  the difference between a user confirming something and a user guessing. The
  negative branches are unchanged.
- **The convention stands, and this is an exception on stated grounds rather
  than a precedent.** What earns a repeat is both of: a wrong answer that
  produces no error the user will see, and a group whose later questions the
  convention reduces to a prompt line. Neither is true of the section openings
  or the file loops the convention was written for. A detail that repeats
  because this one does is not covered by this ADR.

Two answers were rejected and are recorded rather than dropped:

- **Keeping the convention.** It costs nothing and records nothing, and leaves
  the second and third repository answered with the rule out of view. It is the
  answer that would have been right if the failure here announced itself.
- **Carrying the operative half in each prompt line.** `Mount point for <id>,
  where its own Compose files already mount it` stays inside the convention and
  needs no exception. It can say what a correct answer is; it cannot say what a
  wrong one produces, which is the half that matters, because nothing else in
  the product says it either. A prompt line long enough to carry both is a
  detail block that has lost its formatting.

Consequence: `internal/wizard` gains prose, a helper that says which repository
is the first asked for a runtime mount, and one that names the files a proposal
was read from. The flow asks no new question, proposes nothing new, and
validates nothing new: ADR-063's limit on what it may hold applies to the words
as much as to the fields. Nothing about the configuration format changes, so
the three gaps this ADR does not decide — the merge rule in a detail, the
runtime detail existing at all, and the provenance note — needed no decision of
their own and are implemented with it. Tests assert that both groups carry a
detail on every question, and that a proposal read from a file names it.

ADR-065 evidence 6 is marked in place in the same change. It recorded that
`feat project init` never collected the runtime container path and that
`feat doctor` never printed it; both were fixed by that ADR's own decisions, and
a reader taking the evidence for a description of today's flow goes looking for
a hole that is not there.
