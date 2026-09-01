# ADR-052 — A folded project is a cursor position

Status: accepted
Recorded: 2026-08-12, alpha review, after ADR-051 shipped

ADR-051 gave the rail's fold marker the control it had always been promising, and
paired it with a rule: the cursor never stays inside a fold, because the main
region draws whatever the cursor is on and the keys act on it. Both halves of the
rail obeyed it — folding a project moved the cursor to the next task still
listed, and `J`/`K` stepped over folded projects rather than through them.

Evidence: the maintainer folded a project in use and could not open it again.
`space` acts on the project the cursor is in, and the two halves of the rule
together mean no cursor position exists inside a folded project: nothing moves
onto one, so nothing can press `space` on one. The only case that worked was
folding every project, where the cursor had nowhere to go and stayed by accident
— which is also the case the header was already written to draw, naming the task
the fold was holding. Folding was a one-way door for as long as one project
remained open, and the control the ADR added to stop the rail claiming a control
it did not have now claimed a reversal it did not have.

Decisions:

- A folded project is one cursor stop: `J`/`K` move onto the fold, and past it in
  one step whatever it holds. One stop for the project rather than one per hidden
  task is what keeps folding worth pressing — the point is to move past a project
  quickly — and is what makes the fold reachable at all.
- `space` folds and opens the project the cursor is in, and does not move the
  cursor either way. The task under the cursor stays selected across a fold, so
  folding no longer takes a user's selection away as the price of reading less
  about other projects.
- What the rail must always say is which task is selected, not that the selected
  task has an entry. A fold holding the cursor names the task's key on its header,
  beside the count and the attention glyph it already carried, which is the
  rendering ADR-051 wrote for the all-folded case and is now the general one.
- The footer names the key for what it would do where the cursor is — `space
  fold` or `space open` — because one control in two directions is otherwise
  legible only from the marker beside the cursor.

Consequence: the main region can draw a task whose rail entry is hidden, which is
what ADR-051's rule was protecting against. It is bounded by the header naming
that task, by the main region's own header naming it in words, and by the user
having pressed a key to get there. No stored format, endpoint, or state moves,
and no new binding: `space` is the same key with a reachable inverse.
