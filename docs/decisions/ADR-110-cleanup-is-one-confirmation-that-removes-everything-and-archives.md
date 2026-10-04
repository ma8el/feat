# ADR-110 — Cleanup is one confirmation that removes everything and archives

Status: accepted
Recorded: 2026-10-04, after the dogfood
Amends: ADR-037, ADR-061

Evidence:

1. **Every finished task took the same path through the screen.** Archiving is
   refused until every class with a present target is selected (ADR-037). The
   only way to archive was therefore to tick every class, tick the archive row,
   press enter, and answer `y`. That is nine key presses that each repeat the
   same decision.
2. **Most of the classes cannot lose work.** The terminal, both sets of
   containers, and the control workspace hold nothing the event log does not
   record. Only a dirty worktree, an uncontained branch, and a volume can lose
   anything, and each of those already carries a warning.
3. **The partial choices are not wanted.** The maintainer archives every
   finished task and does not want the per-class choices. `feat task stop`
   already stops an agent and keeps its containers.

Decisions:

- Cleanup removes everything the plan names and archives the task. The TUI's `C`
  and `feat task cleanup` each ask one question.
- The question lists every warning the plan carries, one line per resource,
  under the question itself. A plan with no warnings asks only the question. The
  volume class's standing warning appears whenever a volume exists, because
  removing one is data loss.
- The wire format and the daemon's checks are unchanged. The request names every
  class, echoes every warning, and sets `archive`. ADR-037's stale-confirmation
  refusal still applies, so a warning that appears after the question was asked
  makes the daemon refuse rather than remove.
- When a removal is refused or stops halfway, the TUI resolves the plan again and
  asks again. The new question lists what is left and what it would now cost.
- A plan with problems is not archivable. Cleanup shows the problems and asks
  nothing.
- The classes stay separate in the policy package, because they fix the removal
  order and name each removal in the event log. They are no longer separate
  choices for the user. FR-CLEAN-002 moves in the same change.

Consequence: the per-class selection screen, its scrolling inventory, and the
archive row leave the TUI. The CLI's per-class and per-warning questions leave
with them. ADR-037 still forbids a blanket `--yes`, and this decision keeps that
rule: without a terminal, the command prints the plan and removes nothing.
