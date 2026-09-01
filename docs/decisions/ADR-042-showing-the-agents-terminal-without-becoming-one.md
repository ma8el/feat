# ADR-042 — Showing the agent's terminal without becoming one

Status: accepted  
Recorded: 2026-08-08, before implementation

ADR-041 built the dashboard around views Feat writes itself, on the reasoning
that the alternative was a terminal emulator inside Bubble Tea. The maintainer
used it and said it was not what they wanted: the main region should hold the
agent's session, and possibly a shell beside it. The reasoning was right about
the emulator and wrong about the alternatives.

Evidence:

1. The main region is thin without a terminal in it. Detail and review are
   conceptually different and share most of their content — workflow,
   repositories, checks — and neither fills the region at 120 columns. The
   region was built for something substantial and given something that is not.
2. Moving the agent's pane next to the rail costs an accepted decision. Measured
   against tmux 3.5a on a scratch socket: `join-pane` preserves the pane id and
   every `@feat_*` option, so identity survives, but the pane's window changes
   and the source window is destroyed when its last pane leaves. ADR-030
   requires matching metadata at session, window, and pane scope, and slice 12's
   reconciliation reads it, so a joined pane is a task Feat can no longer
   discover. Pane surgery is therefore not the cheap path it looks like.
3. Two tools solving this problem move no panes and emulate no terminals.
   claude-squad renders a preview with `capture-pane -p -e -J` and attaches for
   real with `attach-session` over a pty. agent-manager holds a persistent
   `tmux -C` control-mode connection, redraws on the `%output` notification tmux
   pushes when a pane paints, and sends input with `send-keys` and with
   `load-buffer`/`paste-buffer -p` — the latter because `send-keys` truncates
   around a kilobyte and bracketed paste stops an application swallowing the
   trailing Enter. Their own note on control mode is the argument for it: a
   focused preview then needs no per-tick process forks and no polling.
4. Rendering tmux's output is not emulating a terminal. tmux owns the pty,
   interprets the program's escape sequences, and maintains the screen grid.
   `capture-pane -e` returns that finished grid as text with colour attributes.
   What is left for Feat is placing a rectangle and cutting it by cell without
   splitting a sequence, which is what `internal/ui/overlay.go` already does for
   dialogs.
5. The TUI cannot hold the connection. The `ui-is-a-client` depguard rule denies
   `internal/ui` any import of `internal/tmux`, as `cli-is-a-client` does for the
   CLI. That rule is the executable form of "the daemon is the only writer", and
   sending keys to an agent is a write.
6. Preparing a pane for display is not free and not idempotent, which evidence 3's
   preference for control mode understated. Measured against tmux 3.7b with a
   zoomed two-pane window already sized 179x52, reading `stty size` inside the
   pane: a `resize-window` to the size the window already has sets the zoomed
   pane's pty to 90 columns — its share of the split — and then back to 179. tmux
   reports `pane_width` as 179 throughout, so the window looks motionless from
   outside while a full-screen program repaints itself at half the region's width
   and repaints again. The same measurement puts a settled frame's five `tmux`
   invocations at 30.5 ms and the two it actually needs at 16.3 ms, which at a
   60 ms focused poll is half the interval spent forking processes.
7. A resize is a request to repaint, and a pane whose program has ended cannot
   answer it. tmux reflows the screen instead. Measured against tmux 3.5a, a
   pane retained by `remain-on-exit` holding a full-width prompt, sized from 20
   columns to 14 and back:

   ```
   20 columns              14 columns
   │ > type here      │    │ > type here
   ╰──────────────────╯         │
                           ╰─────────────
                           ─────╯
   ```

   The maintainer reported it as the terminal tab drawing an agent's prompt over
   two rows, for some tasks and not others: the tasks whose agent had stopped.
   Feat keeps those panes on purpose — a stopped agent's pane is the account of
   what the session did (ADR-030) — and the same measurement back at 20 columns
   returns the box whole, because tmux rejoins exactly the rows it split. So the
   damage is one-directional and so is the repair.
8. Releasing the pinned size as the attach target is handed out does not survive
   the attach. The maintainer reported it after the release was already in
   place: attaching to a task showed the agent in part of the terminal with
   tmux's fill characters over the rest. Read off the running dedicated server
   afterwards, every task window was `window-size manual` at 171x49 — the
   dashboard's main region — while the terminal attaching was larger.

   The gap is between the release and the client. Starting a `tmux` client takes
   tens of milliseconds, the terminal tab polls every 250 ms and every 60 ms
   while the pane has the keyboard, and a frame drawn in that gap asks tmux who
   is attached, is told nobody, and pins the window again. The client then
   arrives to exactly the state the release existed to prevent.

   It stays there because nothing looks again. Bubble Tea's `tea.Exec` blocks
   the event loop for as long as the terminal is handed over (`tea.go`: "NB:
   this blocks"), so the dashboard that would have noticed the client is not
   polling at all — which is why this reads as permanent rather than as a flash.

   Measured against tmux 3.7b, a window pinned at 171x49 with a control-mode
   client attached and sized to 200x60 with `refresh-client -C`:

   ```
   left alone          -u window-size
   171x49              200x60
   ```

   So the option coming off is what hands the window back, and it takes effect
   the moment it does.

Decisions:

- The main region shows the selected task's agent pane. What is drawn is the
  output of `capture-pane -p -e` against the pane ADR-030 already identifies, and
  a shell pane may be shown beside it where one exists.
- Feat does not implement a terminal. It keeps no screen grid, interprets no
  cursor movement, implements no scroll regions, and stores no scrollback. Every
  escape sequence it handles it passes through; the only thing it reads is cell
  width, in order to clip. A change that requires Feat to understand what a
  sequence means is a change this decision refuses.
- This is display and never a source of truth. ADR-030's rule that the tmux
  adapter "never parses terminal output or infers semantic completion" is
  unchanged and is extended rather than weakened: capturing a pane in order to
  draw it is allowed, and deriving any task, agent, attention, or workflow state
  from those bytes is not. Agent state continues to come from provider hooks
  (ADR-032, ADR-036). The distinction is stated because the two operations look
  alike from outside and only one of them is permitted.
- The daemon owns the control-mode connection and proxies. It resolves task to
  pane, validates the target, holds one `tmux -C` client, and publishes frames
  for the focused pane only. The TUI never names a pane and never runs tmux. This
  keeps evidence 5's rule intact, gives the validation one home, and is the shape
  a remote client would need if OQ-002 is ever answered yes.
- Frames are published for what is focused, not for every task. A dashboard
  watching three agents does not need three streams, and the cost of this
  decision is the traffic it avoids.
- Keys travel the same way, and focus is explicit. A key gives the pane the
  keyboard, a key takes it back, and while the pane has it the rail stays on
  screen and the dashboard's own keys do not fire. Text goes through
  `load-buffer` and `paste-buffer -p` for the reason evidence 3 gives.
- Attach stays exactly as ADR-030 defined it. Control-mode rendering is a view of
  a terminal, not a terminal: it has no scrollback and no mouse, and the native
  `attach-session` remains how a user gets the real thing. Both tools in evidence
  3 keep both for the same reason.
- Rendering a frame changes nothing that is already as it should be. The size and
  the zoom are read first and set only where they differ, and the pane is measured
  again only when one of them moved. Evidence 6 is the reason this is a rule
  rather than an optimisation: a preparation step that looks idempotent from
  tmux's side is visible to the program in the pane, and a poll that repeats it is
  a poll that disturbs what it is trying to show. It holds for the control-mode
  transport too, which changes when a frame is asked for and not what asking costs.
- A window holding a pane that has stopped is never made smaller. Evidence 7 is
  the reason: sizing it reflows a screen nothing will repaint, and the reflow is
  then permanent. Growing one stays allowed and is the repair. The question is
  asked about the window rather than about the pane being drawn, because a resize
  reflows every pane in the window — including the stopped agent behind a live
  shell — and it is answered inside the measurement a frame already takes.
- A window a client is on belongs to that client, and Feat's sizing comes off it.
  Evidence 8 is the reason. The release at hand-over stays, because it makes the
  common attach correct with no frame in between, but it is no longer the whole
  of it: a rendering that finds a client on a window Feat pinned releases the pin
  rather than only declining to resize. That covers the client the daemon never
  saw — a user attaching with `tmux` itself, or from a second terminal while this
  dashboard polls — and it is the only step of a frame that acts on a watched
  window at all. It costs nothing per frame: the option's value rides along in
  the measurement a frame already takes, and an unpinned window is left alone.
- A terminal handed to a client is left alone until that client arrives or is
  judged not to be coming. The daemon remembers which task's window it last gave
  an attach target for, and a rendering treats that task as watched for five
  seconds. Evidence 8's gap is the reason, and the record is held against the
  render rather than only consulted by it: a frame that has already asked tmux
  who is attached would otherwise pin the window immediately after the release,
  which is the same defect a few milliseconds later. Nothing about this is
  persistent — a daemon that restarted has no attach in flight — and a client
  that never arrives costs one wrongly-sized frame after the grace expires.
- What the region cannot fit, the renderer clips, and it clips to the foot. A
  window is larger than the region whenever Feat is not the one sizing it: the
  window a native client owns, and now the window of a stopped pane. A terminal's
  newest output is at its bottom and the prompt a user reads is its last row, so
  the rows to drop are the ones above — ending at the last row the panes wrote,
  because a window with blank rows under its content has a foot that is not the
  content's.
- Detail and review become one task view rather than two tabs. Evidence 1 is the
  reason, and a main region holding a live session leaves room for one panel
  rather than four.

Consequence: ADR-041's tab set changes and its layout does not — the rail, the
footer, the overlays, the minimum width, and the compositor all stand, and this
is why that work was committed before this decision rather than after it. The
overview table's fate stops being an open question in ADR-041's terms, because
the main region now has an occupant; whether a cross-task comparison is still
wanted is decided with the three-task runs as before. A new endpoint and a new
stream cross the socket, carrying rendered bytes rather than state, which is the
first traffic of that kind and the reason it is scoped to one pane.
