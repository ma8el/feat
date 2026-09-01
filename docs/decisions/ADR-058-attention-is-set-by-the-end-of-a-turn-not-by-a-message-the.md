# ADR-058 — Attention is set by the end of a turn, not by a message the agent wrote mid-turn

Status: accepted  
Recorded: 2026-08-13, from the event logs of the dogfood runs

The maintainer's observation, from dogfooding: `possibly_waiting` shows up while
the agent is still running. The stored event logs say it did, on every task that
ever asked for review, and the cause is a row in the normalization table rather
than a race.

`possibly_waiting` is defined as the conservative reading of an end of turn:
Feat cannot tell a finished turn from a question, so it says the session may be
waiting rather than that it is. The review-request row set it too, on the theory
that an agent asking for review is an agent waiting for an answer. It is not.
The agent writes that message in the middle of a turn it then carries on with —
finishing what it was saying, waiting on the completion gate, and going back
into its own loop when the gate fails (ADR-036) — and nothing revisits the state
until the turn ends.

Evidence, all of it from `~/.local/share/feat/projects/*/tasks/*/events.jsonl`:

1. Task `e3065d30` requested review at 20:03:42 and set `possibly_waiting`. The
   gate ran, failed at 20:04:37, and the agent went back to work, committed, and
   reported again at 20:10:45. Feat said the session might be waiting for a
   person for seven minutes of demonstrably active work. Task `cde786f3` is the
   same shape across two gate runs, 19:59:25 to 20:06:54.
2. The state is inverted rather than merely stale. It is cleared by
   `KindTurnEnded` — the moment the agent actually stops — and re-set five
   seconds later by the idle grace. During the stretch where the agent is
   working it reads "possibly waiting"; at the instant it stops, it reads
   "none".
3. It is not a display-only wrong. `attentionSummary` counts both attention
   states into the "N tasks may need you" line the dashboard puts above
   everything else, so a working agent was in the count a user reads to decide
   whether anything needs them.
4. Nothing in the specification asked for it. FR-AGENT-008 is about semantic
   completion reaching a workflow state, which the same row does and keeps;
   03-domain-model.md defines the attention states without giving a review
   request one; and the two attention rules recorded in slice 7 above are about
   a launched agent that has said nothing and about an ended turn. The row was
   an implementation choice that was never written down, which is why nothing
   caught it disagreeing with the state's own definition.
5. `becomeIdle` wrote the state onto the task and recorded no event for it. Every
   `possibly_waiting -> needs_input` line in the logs has no matching arrival:
   the task's history explains the process going idle and never mentions the
   attention state the dashboard was counting.
6. `reportSilentStart` recorded the opposite error. It skipped the state change
   when the task already needed the user, and recorded the event anyway with a
   hard-coded `from: none`. Task `e7c9bfc9` at 19:22:19 carries
   `none -> needs_input` for a transition that did not happen: the task had been
   `needs_input` since a failure ninety seconds earlier.

Decisions:

- The review-request row sets no attention. The workflow state is what says a
  task is in review and what the notification is composed from; attention is the
  session dimension, and it stays the answer to "has the provider reported
  something that means a person is needed". A review request answers the first
  question and says nothing about the second.
- Attention after a review request is therefore decided where it is decided
  everywhere else: the turn ends, the idle grace expires, and the conservative
  state is set then — when it is true. An agent blocked on the gate helper is not
  idle and does not reach it, which is correct: it is waiting for Feat, not for a
  person.
- `becomeIdle` records the attention change it makes, as its own event with its
  own reason. A state that reaches the count above the dashboard must be
  explainable from the task's history.
- `reportSilentStart` records an event only when the state moved, and records the
  transition that happened. A task that already says it needs the user learns
  nothing from silence, so the report is a log line and not a second claim.

Consequence: one table row, two event-recording sites, and no change to any
stored format, endpoint, or state vocabulary. `possibly_waiting` now has exactly
one meaning in the product — the session went quiet and Feat will not guess why —
which is the meaning 03-domain-model.md always gave it. The narrower defect in
the same area is untouched and still recorded in `review/findings`: an idle timer
that has already fired cannot be cancelled, so a prompt arriving in that window
can still be followed by `becomeIdle` marking a working session idle. It needs a
generation check rather than a decision.
