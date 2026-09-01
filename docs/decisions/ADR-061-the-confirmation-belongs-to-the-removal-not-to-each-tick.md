# ADR-061 — The confirmation belongs to the removal, not to each tick that led to it

Status: accepted  
Recorded: 2026-08-14, from using the cleanup screen

The maintainer, on the dashboard's cleanup dialog: it is clunky — why the extra
confirmation, and why the extra archive button.

Evidence:

1. The screen asked twice. Ticking a class with warnings raised a `y/N` that took
   the keyboard immediately, and `enter` raised another. A task with three risky
   classes was four questions and three ticks, and the first three arrived while
   the user was still reading the list they were choosing from.
2. The first question bought nothing the daemon can see. `selection()` sends
   `ConfirmedWarnings: class.Warnings` whatever was accepted — the `accepted` map
   only gated `removable()` — so ADR-037's defence against a stale confirmation
   rests entirely on the daemon comparing those strings with what is true at
   removal. One question and two produce the identical request.
3. The screen already says what the first question said. Since the inventory
   change earlier in this slice, each warning is drawn beside the target it is
   true of. The modal was reading a line back to the user that was on the screen
   behind it.
4. The archive row was not a button but a checkbox rendered only once every class
   was selected. Because it is drawn under the inventory, and the inventory is
   sized by what the tail takes, ticking the last class shortened the list by two
   lines and moved what the user was looking at. `A` was in the key map
   throughout, and for most of the interaction did nothing.
5. `r` looked dead, reported after the above, and then: in which case is a
   re-resolve even required. It always asked the daemon and always replaced the
   inventory, and on a task nothing had touched it redrew a list identical to the
   one on the screen — while setting `m.status = ""`, so asking the question
   blanked the one line that could have reported the answer. The plan has carried
   a `ResolvedAt` since the endpoint existed, "when the inventory was taken", and
   no surface displayed it.
6. The case a re-resolve is required for is narrow and real, and it is not the
   one the key implied. Cleanup is reachable on any non-draft task, including one
   whose agent is mid-turn — so the likeliest change under an open cleanup screen
   is a worktree going dirty while it is being read. The daemon refuses that at
   `checkWarnings`, comparing what was confirmed against what it observes at
   removal, so the user meets it as a rejection naming a warning they were never
   shown. The other case, a resource gained or lost, needs a second actor and is
   rare. Both are answered by looking at the moment the answer matters, and
   neither is answered by a key a user has to know to press.

Decisions:

- One confirmation, at the removal. It names the classes, lists every distinct
  warning of everything selected, and defaults to no. That is FR-CLEAN-003 met
  where the consent is actually given: a user reads the whole cost of the whole
  decision, against the request that is about to be sent.
- Selecting asks nothing. A tick is a decision being assembled. The warnings stay
  visible beside their resources throughout, and the class title carries a marker
  so a class does not read as free when the window has scrolled past its
  warnings.
- The confirmation's question is its first line and the warnings follow it. A
  region too small for both keeps the line that says what answering does; the
  warnings are drawn twice over and the question is drawn nowhere else. The
  inventory yields its lines to the question rather than the other way round, and
  says how many it yielded.
- `feat task cleanup` keeps its per-class questions, because there they are the
  selection. A terminal prompt has nothing to tick, so the question is how the
  choice is expressed rather than an interruption of it. What changes is that the
  specification now says which shape belongs to which kind of surface, instead of
  the TUI inheriting the CLI's sequence because it was written second.
- The archive choice is a row of the screen and not a key of its own. Everything
  on this screen with a checkbox is reached the same way — down to it, space to
  tick it — and the archive choice was the one checkbox the cursor could not land
  on, ticked instead by a key that did nothing for most of the interaction. A
  second way of doing the one thing the screen does is a way that has to be
  learnt separately and remembered separately.
- It is drawn wherever the plan could ever be archived, greyed and saying what it
  is waiting for when it may not be taken yet. The rule it is waiting for is
  unchanged and is ADR-037's: archiving a task that still owns a running
  container manufactures the orphan reconciliation exists to report. Drawing it
  throughout is what keeps the inventory above it from moving as classes are
  ticked, and it is what stops a cursor stop from blinking in and out of
  existence underneath the cursor. Pressing space on it while it waits answers
  where the press happened rather than only in the status line.
- A screen with a question outstanding advertises that question's keys and no
  others. The key map and the scroll note both offered keys the confirmation had
  taken, which is a promise a user has to try in order to disbelieve.
- Enter resolves before it asks, and there is no re-resolve key. Freshness is
  worth something at exactly one moment — the moment consent is given — so that is
  when Feat looks, rather than leaving a user to know they should ask. The
  confirmation is then built from a plan taken a moment ago, which is what makes
  "the warnings of everything selected" a statement about now.
- The inventory says the moment it was taken. The screen is an observation and
  not a live view, which is the same thing the recovery overlay says by naming
  when it last checked, and it is what stops the timestamp on a dialog left open
  for ten minutes from being read as current.
- What the resolve found decides whether the question is asked at all, on the two
  axes a plan moves along. They are separate because the token only sees one: it
  covers the identity of every target and deliberately not the warnings, so that
  an agent writing a file is not reported as a stale plan.
  - A cost that moved under the same resources asks anyway. The warning is listed
    in the confirmation, above the answer, which is where it is read — and this is
    the case the whole arrangement exists for.
  - A resource gained or lost does not. The confirmation names classes rather than
    targets, so a class that quietly grew a third worktree would be confirmed by a
    user who had read two. The inventory is replaced and the question waits for
    another enter, which is FR-CLEAN-001's rule about choosing against a summary
    applied to the moment it would be broken.
  - A selection whose resources have all gone says so instead of either, because
    "read it and press enter again" is poor advice for a screen with nothing left
    to press it for.
- The selection survives a plan that moved, minus any class the plan no longer
  names. A tick is a choice about a resource and a resource that has gone takes
  its choice with it; the rest stand, because discarding them would charge the
  user for a change they did not make.
- A cleanup that finished closes the dialog, and what it did becomes a line of
  the footer. The overlay is a transaction the user opened (ADR-041) and the
  transaction is over: what stayed open afterwards was a screen about a decision
  already taken, showing an inventory of what was left rather than what had been
  asked about. For an archived task it was worse than redundant — the screen's
  next resolve is one the daemon refuses, because an archived task is one Feat has
  stopped tracking, so the dialog sat over an error it had caused by remaining.
- The line names the classes and counts what was already gone. The classes because
  they are what the user chose; the count because a resource that was already gone
  is not a failure — the user asked for it to be absent and it is — but a cleanup
  that removed nothing because everything had gone is a different morning from one
  that removed six things. The itemised list is in the event log, which is where
  "Feat can explain what happened later" already lives.
- A cleanup that failed halfway keeps the dialog, and re-reads the plan. There the
  screen is the only account of what happened, the classes are removed in a fixed
  order so some of them went, and the inventory from before the attempt describes
  a machine that no longer exists. Reading it again is what makes a partial
  cleanup recoverable by looking at it, which is what ADR-029 said it would be.
- The keyboard is held while the resolve is in flight. A tick landing in between
  would put a class into the question that the plan under it was never checked
  for, which is the defect this whole decision is about, arriving through the
  door built to prevent it.
- The key that executes says what it acts on: `enter cleanup selected`. It takes
  the whole selection and not the row the cursor is on, and a key map that said
  `remove` beside a cursor resting on one class read as an offer to remove that
  class. Sixty-three cells against the sixty-eight a dialog has at the layout's
  minimum width, so the distinction costs nothing to draw. It says "cleanup"
  rather than "remove" because that is what the screen and the command are both
  called, and the removal of particular classes is what the confirmation names.

Consequence: the `accepted` map and the `confirming` state leave `cleanupModel`,
which removes the only place the screen held consent separately from selection,
and the cursor runs one row past the classes. The wire format, the daemon's
validation, and ADR-037's stale-confirmation refusal are untouched — the request
is byte-for-byte what it was. A removal of two risky classes goes from seven key
presses to four, and the screen's key map from six keys to three: `A` and `r` are
both unbound here, space is the only way to tick anything, and enter is the only
way to ask for anything. The screen's per-resource result rendering goes with the
dialog that held it — and what does not come back is a partial result after a
failure: the daemon returns one alongside its error, and the HTTP layer's
`send[T]` discards the body of a non-2xx, so the UI has never had it. The error
names the class that failed and the classes are removed in a fixed order, so what
went is derivable; carrying the result itself would mean changing the shape of
every error response, which is a wider change than this one.
[04-functional-specification.md](04-functional-specification.md) states the
surface rule under FR-CLEAN-003. What this does not settle is whether the CLI's
per-warning question is worth its own press on top of its per-class one; it is
the same sequence it has always been, and no evidence from using it says
otherwise yet.
