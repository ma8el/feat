# ADR-044 — The machine's resources at the foot of the rail

Status: accepted  
Recorded: 2026-08-09, after use

ADR-041 put the machine's figures in the footer beside the selected task's
worktree path, to get them out of the vertical stack that pushed the task list
down the screen. The maintainer used that dashboard and asked for them in the
rail instead, below the tasks and above the warning count, and then for the first
build of that: no heading, the processor row named for what a reader looks for,
the percentage on the bar rather than a figure after it, and Feat's orange.

Evidence:

1. The figures were three numbers and no proportion. "48 GiB free of 460 GiB"
   asks the reader to divide before it answers anything, and the question the
   block exists for — is there room to start another task — is a proportion.
   The same free figure is roomy on one machine and nearly nothing on the next.
2. The rail's foot already held the other machine-wide block. Reconciliation's
   count is not about the selected task either, and the two are read the same
   way: a glance at a corner, not a lookup. What is left in the footer — the
   worktree path — is about the selected task, so the footer became one thing
   rather than two unrelated ones.
3. Moving it means reshaping it. The rail is thirty-two cells and the footer's
   form is ninety, so this is not a relocation. A bar is what buys the width
   back, because the total stops being a number and becomes the bar's length,
   and FR-UI-005 asks for available memory and disk availability rather than for
   the totals.

Decisions:

- The machine's resources are at the foot of the rail, below the tasks and above
  the warning count, pinned to the bottom rather than following the task list
  down. This is evidence 4 of ADR-041 again: something read by position is in the
  same position every time.
- A metric is a fixed label, a bar of the share in use, and the percentage after
  it. Fixed columns, so that three bars start and end in the same place and can
  be compared by eye, and the number is right-aligned in a column of its own so
  that a machine crossing from 99% to 100% does not shift it sideways twice a
  second. A number wider than that column takes the cells from the bar rather
  than from the rail: a line is thirty-two cells whatever the machine is doing,
  and a machine wanting twelve times its processors has a bar with nothing left
  to say.
- The number is in the label's grey, not the bar's colour. It says exactly what
  the bar says, and a second colour would make one measurement look like two. It
  was first put on the bar itself, to spend no width on it, and that was wrong
  in use: it split the blocks either side of it into two runs, which read as two
  bars rather than as one with a gap.
- There is no heading over the three. The labels say what they are, and the
  rail's one heading belongs to the tasks.
- The rail's three parts are ruled apart rather than spaced apart. They are about
  three different subjects — the tasks, the machine, and what reconciliation
  found — and blank space between them read as one list that had stopped. The
  rule is the grey of the divider beside it, so the rail is ruled by the frame
  rather than decorated, and the lower rule is drawn only when there is something
  below it.
- The processor row is labelled `cpu` and reads as a percentage. What is measured
  is unchanged — the daemon samples the run-queue average with the core count, as
  ADR-035 decided, and the API still carries both — but the rail divides one by
  the other and shows the result as a share. This is taken knowingly against
  ADR-035's caution about figures that look alike and are not: what is lost is
  the reader's cue that this is demand rather than occupancy. Two things are kept
  against that. Only one measure is derived, identically on both platforms, so
  the cross-platform half of ADR-035 stands. And the share is not clamped in the
  number: a machine wanting more processors than it has reads 245%, which nothing
  that was truly a utilisation percentage could, and the number is marked while
  the bar stops at full.
- A bar is never empty or full by rounding, in the bar or in the number. Two
  percent draws one cell and reads `<1%`; ninety-nine leaves one cell empty and
  reads `>99%`. The bar is read before the number and both roundings would state
  something the sample did not find.
- A share that cannot be computed draws no bar and says it was not measured. A
  capacity of zero is not an empty disk; it is a filesystem nothing measured, and
  a bar at zero would be the most readable false claim on the screen — the rule
  ADR-028 and ADR-031 set, applied to a shape rather than to a number.
- The bars are Feat's orange, which is also the attention colour. A bar is a
  measure rather than a summons and the shape is what tells them apart: an
  attention badge is a glyph beside a task and a bar is a block that fills a
  column. Bold stays with the attention styles, so a bar never shouts and the
  number on an overloaded machine still can.
- The sample's notes and a failed read stay in the footer. They are sentences,
  and thirty-two cells would truncate them into the silence ADR-043 kept them out
  of; the rail says which figure is absent and the footer says why, on screen at
  once.
- There is one rendering of the machine and the narrow fallback uses it too. The
  one-line form is deleted rather than kept for the fallback, which would have
  been two renderings of the same sample to maintain and one of them visible only
  below the layout's minimum.

Consequence: `machineLine` and the three field renderers it composed are deleted,
and the absolute figures they carried — free bytes and total bytes, the load
average itself — are no longer on the dashboard; the share and the percentage
replace them, and `GET /v1/resources` still answers with all of them.
[04-functional-specification.md](04-functional-specification.md) FR-UI-005 moves
with the code, as it did for ADR-035, ADR-041, and ADR-043.
