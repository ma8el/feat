# ADR-043 — Removing the overview table

Status: accepted
Recorded: 2026-08-09, after use

ADR-041 kept the overview tab's wide task table provisionally, to be removed if
the three-task runs never used it. The maintainer used the dashboard and asked
for it to go before those runs, which answers the question earlier than planned
and in the same direction.

Evidence:

1. Nothing needs it. The rail answers which task to go to next — identity,
   attention, agent state, elapsed time, changed files, grouped by project — and
   the task panel answers everything the table's remaining columns did, for the
   task the rail sent you to. The table was the same facts a second time, laid
   out for a comparison nobody made.
2. It never fitted. Eleven columns are 158 cells against a supported width of 80
   to 160, which is the defect ADR-041 was built to fix; `fitColumns` dropped
   columns from the right until they fitted and told the user which were missing.
   A view that reports what it cannot show is honest, not useful.
3. Two things were living on it that are not overviews, and both were already
   invisible in the three-region layout, because the page it drew them on was
   reachable only in the narrow fallback. The recovery band — reconciliation's
   findings and the action for each — had no other surface anywhere, in the TUI
   or the CLI. The resource sample's notes, which say why a figure is absent, had
   none either.

Decisions:

- The tab set is terminal, task, and runtime. Every tab is about the selected
  task; the overview was the one that was not. **Amended: the brief takes a
  fourth tab, see ADR-086.**
- Reconciliation findings that name a listed task appear on that task's panel,
  above the fields they contradict. A workflow of working beside a worktree that
  is not on disk is the contradiction the pass exists to surface, and the panel
  is where both are read and where the keys that resolve it are.
- Everything the pass found is also in one overlay, on `!`, and the rail carries
  its count and that key. The footer was tried first and is the wrong shape: a
  finding is three lines — what, where, and what to do — and a machine with
  several of them has a list rather than a line, so the footer either truncated
  the action or became the thing nobody reads. The rail's job here is the one it
  already does above the task list with the attention summary: say that something
  needs a person, and let them choose when to look. The overlay is where an
  orphan whose task record is gone, an enumeration that failed outright, and a
  previous daemon that died rather than stopped can each have their three lines.
- The pass carries the time it ran, and looking again is a key on the overlay.
  Nothing repeats a pass on a timer, so what is shown is always what was true at
  startup; a user who has just resumed a task is reading history unless the view
  says when it was taken and offers to retake it.
- The resource sample's notes join the machine figures in the footer, for the
  reason FR-UI-005 gives: a figure nothing measured is shown as absent, and an
  absent figure with no reason beside it is the same silence in another form.
  **Amended: the figures moved to the rail and the notes stayed in the footer,
  see ADR-044.**
- The narrow fallback draws the rail's own entries rather than the wide table.
  It is the only way to choose a task below the layout's minimum, and the table
  fitted no terminal small enough to reach it.

Consequence: the recovery band, the machine card, the eleven-column task row, and
the column-fitting they needed are all deleted. Nothing in FR-UI-002 is lost —
ADR-041 already rewrote it to the triage set the rail carries — and FR-UI-005's
"secondary view" for per-container metrics remains a MAY that nothing implements.
A rail entry now names a task's workflow when it has no session, because the rail
is the only list left and a draft that reported an absent agent state read like a
task whose agent had stopped.
