# ADR-107 — Linux desktop notifications are unscheduled, because no machine here can confirm one arrives

Status: accepted
Recorded: 2026-09-27, after the Linux run of the public-preview milestone

`docs/09-roadmap.md` listed Linux notifications in Phase 1, and
`internal/notify/notify_other.go` told every Linux user that "support for this
platform is v0.2 work". Neither is going to be true, and a promise the product
prints to the user is the wrong place to leave a plan.

Evidence:

1. **Nothing is half-built.** `internal/notify` has `notify_darwin.go` and
   `notify_other.go`, and the second is an `Absent` notifier: `Available` returns
   false with a reason naming the platform, and `Notify` refuses rather than
   pretending. There is no Linux backend, so this defers work rather than
   abandoning any.

2. **The delivery cannot be confirmed on the machine the milestone had.** The
   Linux run happened on a headless cloud box. `osascript`'s absence is not the
   obstacle — a Linux notifier would use the desktop's own bus — but a box with no
   desktop session has nothing to receive a notification, so the one clause of
   item 1's done-when that this would satisfy could not be exercised there at all.

3. **A notifier is exactly the thing that must not be shipped unverified.** Its
   whole behaviour is a side effect on a desktop. A unit test proves the policy
   decided to deliver; only a person seeing a banner proves delivery. CLAUDE.md's
   definition of complete — "the promised behaviour is verified, not the code that
   would produce it exists" — bites hardest here.

4. **The demand is unknown.** No Linux user has asked. Feat's Linux support is
   one week old and has one user, who is the maintainer and was using a server.

Decisions:

- **Linux desktop notifications leave Phase 1 and are scheduled nowhere.** Not an
  exclusion: Feat is not declining to deliver notifications on Linux the way it
  declines to be an editor. It is unbuilt work with no date, and the roadmap says
  so where a reader of Phase 1 would look for it.

- **Nothing in the product names a version for it.** The message a Linux user sees
  becomes "Feat delivers desktop notifications on macOS only; the dashboard's
  badges work everywhere", which says what is true and what to use instead. The
  package overview and the README stop naming `v0.2`. A version in a string is a
  promise the string cannot keep.

- **What a Linux user loses is named rather than glossed.** The dashboard's
  attention badges are rendered from task state and work on every platform, so
  which task wants them is still visible; what is missing is the banner when they
  are not looking at Feat. The README says that, and `feat doctor` and the daemon's
  log already say the platform delivers none.

- **The triggers for taking it up**, so the deferral has an end rather than a mood:
  a Linux desktop somebody on this project actually uses, or a Linux user who asks
  for it. Either supplies what item 2 lacks — a machine on which the delivery can
  be seen — and the second also answers item 4.

Consequence: item 1 of the public-preview milestone loses the notification clause
from its done-when, leaving the run in anger and the archives. The `Notifier`
interface is untouched, so the backend remains a file to add rather than a design
to revisit, which is what ADR-028's rule about an absent capability bought.

[04-functional-specification.md](04-functional-specification.md) FR-UI-004 said
Linux desktop notifications "are required for public v0 where supported", which
this decision removes; it now states that the badges are what every platform gets
and that a platform Feat does not deliver on must say so. That amendment was missed
when this decision was first recorded and added when the release was prepared —
`docs/08-v0-scope.md` lost the bullet in the same pass.
