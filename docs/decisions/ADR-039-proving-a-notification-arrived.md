# ADR-039 — Proving a notification arrived

Status: accepted
Recorded: 2026-08-08

Evidence found while walking every notifiable condition against a real desktop:

1. A task that passed its completion gate reached `ready_for_review` and the user
   was not told. Observed by hand while exercising slice 11. The state, the
   event, and the review record were all correct, so what was missing was the
   interruption rather than the transition.
2. Slices 10 and 11 each added notifications with unit tests over a fake
   notifier, and a fake notifier proves the daemon asked rather than that anybody
   was told. No test crossed the boundary the defect was on.
3. `notifyTask` had five paths that did not deliver. Four returned silently and
   one logged at debug, so "why was I not told" was a question the daemon's own
   log could not answer.
4. A notification that does not arrive leaves nothing behind. The state change it
   was about is recorded correctly either way, and macOS drops an unauthorised
   notification without saying so, so a policy Feat applied on purpose and a
   notification the desktop swallowed are indistinguishable after the fact.
5. `notify.Absent.Notify` documents that "a caller checks Available first and
   never reaches this", and `notifyTask` did not. On a build that delivers
   nothing, every notifiable change reached a notifier that refused it and was
   logged as a failed delivery rather than as a platform that has none.
6. The condition for failed application services had no test that reached it, and
   could not have had one: the Compose fixture hard-coded `ExitCode: 0`, and a
   non-zero exit is what separates a failed runtime from a stopped one. The walk
   found it — six conditions delivered and this one did not.

Decisions:

- Every path that does not deliver names the policy that stopped it, at info,
  with the task, its key, the project, and the condition. Info rather than debug
  because the reader is a user working out why they were not told something, not
  somebody debugging Feat.
- It is a log line and not a task event. A suppressed notification is not
  something that happened to the task, and recording an event publishes it, which
  is a step towards notifying somebody about not having notified them.
- The reasons are phrased as the user's own setting or situation —
  `notifications.desktop`, `notifications.suppress_while_attached`, being
  attached, a daemon still catching up — rather than as internal state.
- Whether this platform can deliver is asked once at startup and kept, so a
  platform that delivers none drops a notification saying that, rather than
  handing it to a notifier that refuses it.
- Every condition is walked against a real desktop by an opt-in test that drives
  the actual state change — a hook, a control message, a gate over the project's
  own checks, a runtime observation — and asserts both that Feat handed one over
  and that it recorded doing so. `notify.Conditions()` exists so a condition
  added later has to be walked too: a list extended by hand is a list that
  eventually is not.
- The walk asserts delivery and never sight, which is all Feat can know. What it
  prints is what to compare against the desktop, and the two logs that tell the
  two failures apart.
- ADR-036's suppression of `review_requested` while a gate is about to run
  stands. The concern that a gated task might arrive more quietly than an ungated
  one was the reason for the walk, and the walk shows both arriving.

Consequence: [06-technical-architecture.md](06-technical-architecture.md) and the
README were updated in the same change. No notification policy changed: the
conditions, the tables, the grace periods, and the suppression rules are exactly
what slices 10 and 11 decided. What changed is that not being told now has an
answer, and that every condition has been shown to reach a desktop once.
