# ADR-086 — The task panel keeps only what nothing else on the screen says

Status: accepted
Recorded: 2026-08-29, before implementation

The maintainer read the task tab after the dogfood runs and reported it as
overloaded: most of it looked unnecessary, and the approve and request-changes
actions had never been used. Read against `internal/ui` and against the daemon's
own record — every `events.jsonl` under `~/.local/share/feat/projects/*/tasks/`,
51 tasks, 46 of which reached a session.

Evidence:

1. Four of the panel's seven always-on fields are already on screen.
   `taskPanel` (`internal/ui/taskpanel.go:129`) draws seven fields and up to
   eight sections. The rail carries attention, agent state, and elapsed time
   about four cells to the left (`internal/ui/rail.go:256`), and runtime is a
   whole tab. Counted from the code, a one-repository task is about thirty-five
   lines before the brief begins, so the panel is already a scroller before the
   document that makes it scroll. This is ADR-041 evidence 9 one level down: the
   fields were put here when the panel was the only place they could live, and
   the rail that now carries them arrived afterwards without them being taken
   out.
2. The review decision was used once in fifty-one tasks. Workflow transitions
   across every task record: `review_requested -> verifying` 53, `working ->
   review_requested` 52, `draft -> preparing` 50, `preparing -> working` 44,
   `verifying -> ready_for_review` 38, `ready_for_review -> working` 32,
   `working -> archived` 31, `ready_for_review -> approved` 1, and anything to
   `changes_requested` 0. The one approval was at 05:45 on 2026-08-29, which is
   the maintainer pressing the key in order to ask this question. The two
   transitions that carry the real loop are the ones nobody pressed a decision
   key for: sending work back is `ready_for_review -> working`, which happens
   when the user attaches and types, and finishing is `working -> archived`,
   which is cleanup.
3. Neither decision state does anything once it is reached. `approved` is
   terminal (`internal/domain/states.go:89`) and no reader anywhere gates on it;
   publication's "approved" is the approved *text*, not the workflow state.
   Requesting changes records a transition and stops
   (`internal/daemon/review.go:279`), after which the panel renders "changes
   requested (a to attach and say what to change)" (`internal/ui/review.go:335`)
   — a key that records a label and then instructs the user to go and do the
   real thing. `feat review` only observes, so these two keys are the whole of
   the capability.
4. The tmux target is one constant and three pointers. Across the 46 sessions in
   the task records there is exactly one distinct socket value, because it is
   `Layout.Runtime/tmux.sock` by construction (`internal/paths/paths.go:149`).
   The other three fields are tmux object ids — real values from the records are
   `$12 @40 %51` — and ADR-030 chose ids over display names precisely because
   they are stable identity, which is what makes them Feat's handle and not a
   human's: nothing about `%51` can be recognised, guessed, or verified by eye,
   and `$12` is shared across several tasks, so the session printed under a task
   does not identify it. The one real use, running a tmux command by hand, is
   served by `a` and by `feat attach`. These fields are also rendered from a
   daemon response, so in the one situation where going around Feat makes sense
   — the daemon being down — the panel is not on screen at all, while the same
   values sit in `task.json` and are readable with `jq`.
5. The environment section ends in a paragraph that cannot vary.
   `executionDetail` (`internal/ui/dashboard.go:91`) closes with two lines about
   generated overrides and reset container names that are identical on every
   task: documentation living in a status panel. Its `container` field renders
   Docker's uptime phrase verbatim, which is why it reads "Up Less than a
   second" on a task that is plainly older. What must survive is narrower than
   the section and wider than nothing: the log holds `reconciliation observed the
   agent container as not running; it was not started or stopped` four times, and
   that is a state the process word cannot express.
6. `resources` gives two figures and does not say what they are. `resources  2%
   448 MiB` leaves the reader to infer each meaning from its unit, which is the
   machine block's defect before ADR-044 one level down. Underneath it, a
   breakdown line and one row per container repeat the same total in two further
   forms, and the container row's name is the compose project name plus a suffix.

Decisions:

- **The tab set becomes terminal, task, brief, runtime.** The brief is a document
  and is unbounded; it is what makes the panel scroll, and it is read heavily
  before launch and rarely during. On its own tab the panel stops scrolling in
  the common case, which is ADR-041 evidence 4 restored: a field is where it was
  last time. `source` moves there too, being a fact about the brief.
- **The panel drops what the rail and the tabs already carry**: attention, agent
  state as a bare word, elapsed time, the runtime detail, and the task UUID.
- **The tmux block goes entirely.** Not relocated, and no `feat task show` is
  required for it: evidence 4 is the reason, and the facts are already on disk in
  a form that survives the daemon being down, which is the only condition under
  which anybody wants them.
- **The environment section collapses into the agent field**, which names what
  runs, where, and the compose project, with the container's state appended only
  when it is not simply running:

  ```
    agent         running · claude in devcontainer
                  feat-agent-feat-33abeee0-3edd-45e1-ab53-3ac09c5bec4e

    agent         stopped · claude in devcontainer · container not running
                  feat-agent-feat-33abeee0-3edd-45e1-ab53-3ac09c5bec4e
  ```

  The compose project is on a continuation line of its own rather than inside the
  value, so that the field is the same two-line shape at 96 columns and at 160.
  The provider is named although v0 has one: it is a line that would otherwise be
  edited twice. The per-container rows under `resources` go, rather than losing
  only their figures, because stripped of those they are this same string again.
- **`resources` takes the rail's vocabulary and not its bars**: `cpu 2%   memory
  448 MiB`, with an absent figure shown absent. No bar, because the rail's bars
  are shares of this host and these figures are not — ADR-035 is explicit that a
  container's memory is what the runtime reported, inside its own VM on macOS,
  and a bar against the host's total would invite exactly the comparison that
  decision refuses. The breakdown line goes. `resourceCell` folds into
  `resourceDetail`: its only other caller died with ADR-043's table, and its
  comment still names that table.
- **`runtime` survives as one word**, without the "(v0 starts application
  services only when you ask)" apology. It is the literal word `absent` rather
  than the em dash, so that it stays distinguishable from a figure nothing
  measured (`internal/ui/style.go:18`).
- **Approve and request changes are removed, and `approved` and
  `changes_requested` leave the domain.** Evidence 2 and 3 are the reason. The
  exits that exist are publishing and cleanup, and the workflow line names them:
  `P to publish · a to attach and revise`. `approvalOffer`
  (`internal/ui/runtime.go:409`) goes with them, being reachable only from a
  state that will no longer exist.

Measured effect: the panel goes from about thirty-five lines plus an unbounded
brief to about twenty, against thirty-four rows of main region at 100×42. The
task tab's footer hints go from about 156 cells to about 105, which fits a
120-column terminal for the first time; today they truncate silently at every
supported width.

Known cost, accepted: a fourth tab is 35 cells of tab bar, so at the 96-column
minimum the main header drops the "which task" subject entirely —
`internal/ui/card.go:143` drops the aside when the gap is under two cells. At 120
it is comfortable. Accepted rather than solved; the fallback, if it bites in use,
is the brief on a key from the task tab rather than a tab of its own, which is
the same view with less discoverability.

Consequence: the requirements move with the code that makes them true, which is
what ADR-041 and ADR-043 each did.
[04-functional-specification.md](04-functional-specification.md) FR-UI-003
becomes a requirement about what the dashboard exposes for the selected task
across its tabs rather than about one panel, and it moves with the panel's field
block. Nothing loses its requirement: the tmux target is *shown* by the terminal
tab rather than named, runtime services are on the runtime tab, and the brief is
on its own. FR-REV-004 is rewritten rather than trimmed, and
[03-domain-model.md](03-domain-model.md)'s workflow states move with it, in the
change that removes the two states from `internal/domain/states.go`. FR-UI-005 is
unchanged, and is the reason `resources` stays on the panel at all: it is the
only per-task total, and per-container metrics remain a MAY that nothing
implements. Nothing crosses the socket differently for the panel's own work; the
removal of the two decision states does change `internal/api` and the daemon,
which is why it is the last of the four changes rather than the first.
