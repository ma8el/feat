# ADR-046 — Shifted keys move the frame, plain keys move the view

Status: accepted  
Recorded: 2026-08-09, after use

The maintainer reported that dashboard navigation was inconsistent: the arrow
keys sometimes moved the task rail and sometimes moved a cursor inside the main
region, with no way to tell which without pressing them.

Evidence:

1. It was true, and the split was per tab. The dashboard's fall-through key
   handler bound the plain arrows to the rail's cursor, but a view with its own
   keyboard is answered before that handler is reached — so the arrows moved the
   rail on the terminal tab and a repository on the task panel, and did nothing
   on runtime. One key, three meanings, chosen by a tab the user was not thinking
   about.
2. The frame already had shifted keys and they were incomplete. `shift+↑`/`↓`
   selected a task from any view and `tab`/`shift+tab` changed view, so the rule
   existed; the plain arrows duplicating half of it is what made it unlearnable.
3. A terminal has no modifier bit for a shifted letter. `shift+j` arrives as
   `J`, so the Vim-shaped binding for this is the uppercase letter itself. `J`,
   `K`, `H`, and `L` were unbound; the capitals in use were `A`, `C`, `P`, `V`,
   and `R`.
4. Shifted arrows are not reliably delivered. That is why `ctrl+p`/`ctrl+n`
   already existed beside them, and it is why the letters are the primary
   binding rather than a convenience.
5. The same swallowing defect had a second half, reported straight after the
   first: `?` opened nothing from the task panel or the runtime view. Those
   handlers answered their own keys and returned for everything else, so the
   dashboard's keys — `?`, `!`, `n`, `z`, `x`, `v`, and on runtime `a` and `s`
   too — were reachable only from the terminal tab. The footer on both views
   advertised `? keys` throughout, because the frame's hints are drawn whatever
   has the keyboard.

Decisions:

- Shifted keys move the frame. `J`/`K`, `shift+↓`/`shift+↑`, and `ctrl+n`/
  `ctrl+p` select a task; `L`/`H`, `shift+→`/`shift+←`, and `tab`/`shift+tab`
  change view. They are answered before any view sees them, so they work from
  everywhere.
- Plain keys move within whatever the main region draws, and never reach the
  frame. `j`/`k`/`h`/`l` and the plain arrows are equivalent. A view with
  nothing to move through — the terminal tab, whose unfocused pane has no cursor
  — moves nothing, rather than reaching past itself to the rail.
- `h`, `j`, `k`, and `l` are reserved for that movement even where a view has no
  use for them yet. The runtime view's logs action moves from `l` to `o`.
- The narrow fallback keeps the plain arrows on the task list. Below the
  layout's minimum there is no rail: the list is what the single column draws,
  so it is the main region, and the rule is unchanged rather than excepted.
- A focused pane still takes every key except `ctrl+q`. That is not an exception
  to this rule but the layer above it: while the keyboard belongs to the agent,
  the dashboard has no keys at all.
- Movement is answered before a view and actions are answered after it. A view
  that swallowed movement would trap the user inside itself, so the frame takes
  those keys first; a view that is overruled on its own actions would lose `r`,
  which means compare again on the task panel, refresh on runtime, and look
  again on the dashboard. So a view keeps every key it claims, and everything it
  does not claim falls through to the dashboard's meaning.
- A dialog is not a view and does not fall through. Preparation, cleanup, and a
  pending confirmation answer every key themselves, because an overlay is
  something to be answered before work continues.

Consequence: the change is contained to `internal/ui`. No daemon, API,
configuration, or documented CLI surface moves — `feat runtime logs` is
unaffected by the dashboard's key for the same action. The cost is that plain
arrows no longer select a task on the terminal tab, which is the dashboard's
opening view; it is paid deliberately, because that binding is the one that made
the two meanings look interchangeable.
