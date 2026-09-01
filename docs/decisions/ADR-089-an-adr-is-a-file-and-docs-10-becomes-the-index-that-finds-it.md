# ADR-089 — An ADR is a file, and `docs/10` becomes the index that finds it

Status: accepted
Recorded: 2026-09-01, with the implementation

`CLAUDE.md` orders every agent to read the specification in order, and the tenth
document is this one. It is the log every change appends to, so it grows with the
product rather than with the task at hand — and it is read in full before a line
is written, by every task, whatever that task is about.

Evidence:

1. **The cost is paid per task, and it is most of the reading.** This document is
   7,825 of the repository's 11,628 lines of documentation — 67% of the set, and
   about 525 KiB, which is on the order of 130,000 tokens. Almost none of it
   bears on any one change: a task about the rail's fold marker reads every
   measurement ever taken against Docker Compose, and a task about publication
   reads the palette's chroma ordering. The bill is largest exactly when several
   tasks run at once, which is the working pattern this product exists for.
2. **Concurrent authoring has already produced a collision, and one file hides
   it.** An ADR is written with the implementation and tasks run in parallel, so
   the last heading of one file is a contention point. Branch
   `feat/33abeee0-skill-that-helps-the-user-create` carries
   `### ADR-068 — A skill is installed into the user's own session`, and `main`
   carries `### ADR-068 — A daemon that goes away is offered, once`. The two
   sections do not overlap, so Git merges them cleanly into one document holding
   two ADR-068 headings, and nothing reports it. As files it is a collision on
   the name, which Git refuses rather than resolves.
3. **The split breaks no link.** There is not one anchor-style reference to this
   document anywhere in the repository. Every reference is a bare `ADR-NNN` name:
   197 in the other documents, 451 inside this one, and 1,280 in Go comments
   across 63 distinct ADRs. Nine comments name the document by path, and those
   still resolve, because the path is where the index now is.
4. **The sections are not contiguous.** OQ-001 to OQ-015 sit between ADR-056 and
   ADR-057, under the same `## Accepted decisions` heading, and three of them
   carry substantial bodies. So this document has to be restructured rather than
   truncated: the open questions cannot stay wedged between two index rows.

Not a reason, and recorded so that it is not re-argued: **supersession does not
need the split.** Recording that one decision superseded another works in one
file exactly as it works in eighty-nine, and no decision here is superseded
outright — what this log does instead is mark a bullet in place, which a file
carries as readily as a section does. Where it is recorded is the body and not
the status: a status is one word, and a relation written into the body is one a
reader meets where the reasoning is and one the guards below can hold both ends
of.

Decisions:

- **One ADR is one file**, at `docs/decisions/ADR-NNN-<slug>.md`, zero-padded to
  three digits. Each file leads with its own heading and carries its `Status`
  line and its body verbatim. Nothing is reworded, renumbered, or restatused by
  the split, and the number is what addresses a file: `docs/decisions/ADR-065-*`
  finds one whatever its title turns out to say.
- **The slug is the title, kebab-cased, cut at a word boundary.** Titles here are
  sentences — the longest is 134 characters — and a directory of eighty-nine
  140-character names is one nobody reads. It is cut to at most sixty characters,
  which is a legibility rule rather than an identity one: the number carries
  identity, and the whole title is in the file's own heading and in the index row
  above the link.
- **`docs/10` keeps its name and its place in the reading order, and becomes the
  index.** Number, title, status, one line of what was decided, and the link.
  What an agent reads before writing anything is then the shape of every decision
  and where each one lives, and it opens the ones its change touches. The
  one-line summaries are written for the index and are not extracts: an ADR's own
  words stay in its own file, which is what keeps the two from drifting into two
  accounts of one decision.
- **The open questions get a section of their own**, after the index rather than
  inside it, and the *Decision change process* keeps its place at the end. Both
  are short, both are read often, and neither is under the contention that made
  the decisions worth splitting.
- **The layout is held by a test rather than by care.** `internal/guard` asserts
  that every `ADR-NNN` named anywhere in the repository resolves to exactly one
  file, that the index and the directory name each other exactly, that no two
  files claim one number, that each file's heading number matches its filename,
  that each carries one status written one way and drawn from a closed set, and
  that a decision recording a relation to another is answered by the decision it
  names. The failure this is written against is the silent one: a dropped or
  half-copied section turns nothing red, and evidence 2's duplicate number is
  something a test can see and a merge cannot. It also refuses a number nothing
  has written: naming an ADR that does not exist yet is a dangling reference, and
  a number reserved by a task in flight is reserved somewhere other than here.
- **`CLAUDE.md` moves in two places.** Its reading-order entry says to read the
  index and open what the change touches, and its closing rule says to write a
  new ADR as a file and add its index line. Both are the instruction that was
  already there, with the document's new shape in it.
- **The split is generated, and verified before the monolith's sections go.**
  Eighty-nine sections are not hand-copied, and the generated files are compared
  against the sections they came from — byte for byte, heading markers aside —
  before anything is deleted. Silent truncation is the hazard here, and it is the
  one this ordering exists to close.

Consequence: `docs/decisions/` is new and holds eighty-nine files, `docs/10` goes
from 7,825 lines to an index, and no other document moves — every reference to an
ADR was already a bare name. From here an ADR is authored as a file with an index
line beside it, so two tasks writing one collide on a filename rather than
merging into a duplicate heading. Two things are left alone deliberately: the
collision on `feat/33abeee0` is that branch's to resolve when it lands, and the
next two numbers are claimed by tasks already in flight — so this is the last ADR
written into this document, and every one after it is authored as a file.
