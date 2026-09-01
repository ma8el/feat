# ADR-041 — What the dashboard is shaped like

Status: accepted  
Recorded: 2026-08-08, before implementation

Evidence found by reading the dashboard against the terminal it is read in, after
the maintainer reported it as confusing while preparing the three-task dogfood
runs:

1. A task row does not fit. `taskColumns` is eleven columns totalling 136 cells,
   joined by two-space separators and preceded by a two-cell cursor marker, so a
   row is 158 columns wide (`internal/ui/task.go:43`, `internal/ui/style.go:54`).
   A standard terminal is 80 and a wide one is 120 to 160. Every row wraps onto
   the next, which is why a list of three tasks reads as nine lines of unaligned
   text rather than as three tasks.
2. The requirement is what makes it that wide. FR-UI-002 names nine fields a row
   MUST show, and each of them earned its place. So the row cannot be narrowed by
   dropping columns without changing the specification, and the only remaining
   move is to stop putting all nine on one line per task.
3. Every screen replaces every other. `screen` has six values and each renders
   the whole terminal (`internal/ui/app.go:59`). Opening review, runtime, or
   cleanup discards the task list, so a user watching three tasks can look at one
   of them. Three concurrent tasks is the case v0.1 acceptance criterion 1 is
   about, and criterion 14 — that the user no longer manually coordinates
   sessions, paths, and branches — is a claim about whether this screen carries
   the coordination. A screen that shows one task at a time hands part of it back.
4. Nothing has a fixed position. The dashboard stacks a heading, an attention
   summary, a machine card, a recovery band, the table, an archived note, and
   twelve key hints in one column, and the recovery band appears only when
   reconciliation found something (`internal/ui/dashboard.go:11`). So the row a
   user's eye learned moves down the screen on the day something breaks, which is
   the day the position mattered.
5. The TUI is width-unaware. `m.width` is recorded on `tea.WindowSizeMsg` and
   read by task preparation alone (`internal/ui/app.go:352`); every other view
   renders at fixed widths. Whatever fixes evidence 1 has to introduce
   width-awareness, and a layout is the thing that would consume it, so the two
   are one job rather than two.
6. FR-UI-001 already asks for "project drill-down" and the dashboard is a flat
   global list. Grouping by project is the requirement rather than an addition to
   it.
7. The main region cannot hold the live agent session. Attachment is the native
   tmux client inheriting this process's terminal (`internal/cli/attach.go:51`,
   released by `internal/ui/app.go:522`), which ADR-030 decided deliberately. To
   render that session inside a Bubble Tea viewport, Feat would run tmux under a
   pty and become a terminal emulator — escape-sequence parsing, resize
   propagation, key and mouse forwarding, scrollback — which is larger than the
   rest of this change together and puts a second emulator between the user and a
   multiplexer.

Found while reviewing the first draft of this ADR, which had decided to keep the
row and move it:

8. Moving the table does not make it fit. A rail of about 30 columns leaves 128
   of a 160-column terminal and 88 of a 120-column one, against a row of 158.
   Scoping the table to one project saves the width the project implied and
   nothing like the sixty columns needed. So relocating the nine fields was not a
   decision, and the row has to lose fields or lose the single line.
9. FR-UI-002 restates requirements that already exist. Four of its nine fields
   are named by another requirement: repositories by FR-UI-003's "repository/base
   mapping", runtime state by its "runtime services", verification state by its
   "completion/check summary", and resource usage by FR-UI-005's "per-task
   environment totals". The five that no other requirement claims — identity,
   agent state, attention state, elapsed time, changed-file count — are exactly
   the fields that answer which task to go to next. The duplication is historical:
   the row was the only place a field could live, because the dashboard had no
   persistent region beside it. A layout that always shows one removes the reason.
10. An overlay costs less than a modal screen and preserves more.
    `github.com/charmbracelet/x/ansi` is already in the module graph as an
    indirect dependency and cuts styled text by cell, so compositing a dialog over
    rendered content is a small helper rather than a dependency change; lipgloss
    v1.1.0's `Place` fills a whitespace box and cannot layer. A full-screen modal
    discards the task list for the duration, which is evidence 3 again, and an
    overlay does not.

Found by the maintainer using the first build of this layout, and fixed in it:

11. Two of the tabs ended the tab cycle. Review and runtime answer their own keys
    and return for everything else, so the key that was meant to leave them never
    reached the frame: `tab` moved overview, detail, review, and then stopped. The
    same shape had a second exit — both views refuse a draft without changing the
    screen, so cycling onto one would have stranded the user too.
12. The rail was unreachable from half the dashboard. The plain arrows belong to
    whichever view has the keyboard, and review spends them on its repository
    cursor, so from the review or runtime tab there was no way to change task at
    all. The rail is the thing this layout is built around and two of its four
    tabs could not reach it.
13. The events tab could not answer the question it existed for. It showed the
    state changes this client had seen, so it was empty on open and could never
    describe what happened while the user was away — which is the only reason to
    look. The daemon's log holds the record.

Decisions:

- The dashboard is three regions that persist: a left rail, a main region, and a
  footer. Only the main region's content changes. This replaces the model where
  each screen owns the terminal.
- The left rail is a task selector grouped by project, and it is not the task
  row. It carries the five fields evidence 9 found unclaimed — the
  eight-character key, the title, attention, agent state, elapsed time, and the
  changed-file count — over two lines per task, which is what buys the width back.
- The rail shows attention and agent state as two things and never as one. A
  glyph carries attention and a word carries the process state. Feat keeps
  process, attention, workflow, and runtime states separate in the domain, and a
  composite status badge would put them back together in the one place a user
  actually reads. Colour cannot carry the distinction either, because a terminal
  without it would lose the distinction silently rather than visibly.
- FR-UI-002 becomes a requirement about what the task list carries for triage and
  stops restating FR-UI-003 and FR-UI-005. No field loses its requirement; four
  of them stop having two. This is the specification following the code's shape,
  which is what ADR-028, ADR-031, and ADR-040 each did.
- The main region is tabbed over views of the selected task: overview, detail,
  review, and runtime. A tab is a view you leave and come back to, and its state
  survives the leaving. ADR-042 replaced this set with terminal, task, and
  runtime; what stands here is the shape of a tab, not the list.
- There is no events tab. It was built and removed after the first use: it could
  only show what had happened since the dashboard opened, so the one thing a
  user would want it for — what happened while they were away — is what it could
  not answer, and the daemon's log holds the record either way. Removed rather
  than kept cheaply, because a tab that looks like a history and is not is worse
  than no tab.
- The layout's own keys are answered before the tab's. Moving between tabs and
  moving between tasks belong to the frame, and a view with its own keyboard
  returns for every key it does not recognise, so it would otherwise swallow
  them. No tab declines to open: a tab that refuses is a tab the cycle cannot
  pass, so one with nothing to show for the selected task opens and says so. That
  replaced an earlier fallback to the detail tab, which worked only because the
  refusing tabs happened to come after it in the order.
- Selecting a task has a pair of keys of its own, distinct from the plain
  arrows, and they work from every tab. The arrows belong to whichever view has
  the keyboard — review moves a repository with them — so a rail reachable only
  by them is a rail unreachable from half the dashboard. Changing the task
  re-opens the tab for it, because a view holding one task's services under
  another task's name is worse than a view that reloads.
- Task preparation, cleanup, every confirmation, and the key map are overlays
  over the live dashboard rather than screens that replace it. An overlay is for
  something that is not about the selected task, or that must be answered before
  work continues; it ends, by completion or by cancellation. Preparation is the
  clearest case, because it has no selected task — it produces one.
- The size test decides an ambiguous case. Something that needs the whole main
  region is not a dialog, whatever it is called. That is why review and runtime
  are tabs and their decisions — approve, request changes, destroy — are overlays.
- An overlay closes on a keybind without ceremony, including cleanup's. This is
  not ADR-037 eroding: a cleanup plan is inert until it is confirmed, so
  discarding an unexecuted one costs nothing, and what ADR-037 made deliberate was
  triggering an execution rather than opening a screen. An overlay whose execution
  has started is not dismissible.
- The overview tab keeps the wide table, provisionally. It is the only place
  resource usage or check counts can be compared across tasks, which is FR-UI-005's
  case, and it is also the part of this design with the least evidence behind it.
  It is kept for the three-task runs and removed if those runs never use it —
  recorded here so that a later reader knows it was a question rather than a
  preference. **Superseded: removed, see ADR-043.**
- Attachment stays a handover. Pressing attach yields the terminal to tmux and
  returns to the tab the user left, which is what ADR-030 decided and what
  evidence 7 says the alternative costs. Feat does not embed a terminal emulator
  in v0.
- The footer carries the selected task's worktree path and the machine's
  resources, which moves the machine card out of the vertical stack, and the key
  hints for the focused region rather than all twelve at once. **Amended: the
  machine's figures moved on to the foot of the rail, see ADR-044.**
- There is a minimum width. Below it the three regions collapse to the single
  column that exists today, because a rail and a main region inside 80 columns
  gives neither one enough to be read.
- The recovery band keeps a fixed position rather than appearing between other
  things, so that evidence 4 does not survive the change that was made for it.
- `internal/tmux` gains the split direction deferred on 2026-08-06: a shell pane
  beside the agent rather than below it. It is the same question — what a user
  sees when they look at a task — and it was parked for the moment every screen
  existed, which is now.

Consequence: [04-functional-specification.md](04-functional-specification.md)
FR-UI-001 and FR-UI-002 move with the code, which is the rule ADR-028, ADR-031,
and ADR-040 followed. `github.com/charmbracelet/x/ansi` becomes a direct
dependency, which it already was in effect. Nothing crosses the socket
differently: no endpoint, no domain type, and no storage path changes. The
dashboard's keys are not
renumbered, because a user who learned them during dogfood is the only user there
is. This is presentation work, which the maintainer batches for after every screen
exists; it is taken now rather than in slice 17 because the three-task runs are
read through this screen, and a screen that shows one task at a time cannot
produce evidence about three.
