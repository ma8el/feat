# ADR-076 — The publication draft is read on the screen that sends it

Status: accepted
Recorded: 2026-08-25, from opening the publication screen on a terminal of an
ordinary size

ADR-070 rests the whole of publication on one control: the agent's words can
carry anything it read, so a person reads them before they are sent. ADR-074
built the screen and the editor document that carry it. Opening that screen on a
normal terminal found the control unbuilt.

Evidence:

1. The screen was not drawn. `dialogView` had a case for every overlay except
   this one and fell through to its default, so at any size at or above 96×18 —
   which is every terminal the three-region layout is for — `P` set the screen,
   asked the daemon for a plan, and returned the frame that was already there.
   The publication screen existed only in `stackedView`, the fallback for a
   terminal too small for the layout. What a user saw after pressing it was the
   unchanged dashboard, a footer reading "esc close", and the daemon's socket
   path.
2. Its tests could not see it either. Every one of them asserted against
   `publicationBody()` — the function the missing case would have called —
   rather than against `View()`. Cleanup's dialog is exercised through `View()`,
   and cleanup's dialog works. A screen's rendering is not covered by testing
   the string it would have rendered.
3. Reading and editing were one act, and the wrong one was the gate. The screen
   drew a title per repository; the description — the part that can carry
   anything the agent read — existed only inside the editor. So `e` was
   mandatory, undiscoverable from what the screen said, and what it satisfied
   was "the editor was opened". A user who quit the editor without scrolling
   passed that gate. ADR-074 had already met this from the other end when it
   kept `code -w` as `code -w`, because "an editor that returns immediately is a
   draft approved unread"; the same argument applies to a person, and the screen
   was relying on the editor to make it.

Decisions:

- The publication screen is a dialog, on the same terms as cleanup: drawn over
  the task list it is about, carrying its own keys because the frame's footer
  only says how to close an overlay. The narrow fallback keeps the stacked
  screen it already had.
- What would be sent is drawn in full — the title and the whole description, per
  repository, scrolling under the window. Prose is folded to the measure rather
  than truncated, and each of the agent's own lines is folded on its own, so
  that a list stays a list: a description shown with its ends cut off is one
  that was not read.
- The gate becomes what has been displayed. `enter` is refused until every line
  of the words that would be sent has been on the screen, or until they have
  come back from the editor. Both are the same condition — the user has seen
  them — and the editor satisfies it because that is where they were seen.
- Reading is therefore sufficient to publish, and the editor becomes what it
  should have been: how the words are rewritten, not how they are read. A draft
  nobody edited is composed from the plan the screen drew, trimmed exactly as
  the editor document's parser trims what comes back through it, so that an
  unedited draft is the same request whichever client sent it.
- What has been displayed is recorded where the window is known: the key
  handler, the arrival of a plan, and a resize. A Bubble Tea `View` is pure and
  cannot write back what it drew, which is the same reason the cleanup inventory
  resolves its own region. The count never decreases while a plan stands and is
  reset by the next one, because fresh words have not been read.
- Feat's own record is not part of the gate. What must be read is what would be
  sent; the account of what this task already published sits under it, and
  nobody is made to scroll past a merge request that exists to open the ones
  that do not.
- The screen says the sequence in words at every step, rather than leaving it to
  the key hints. A footer says which key; a sentence says why it is there and
  what follows it, and this sequence is the part of the dashboard nobody has
  memorised.
- A repository the editor came back without is drawn as removed rather than
  blank, and one that would be sent with no title is refused by name rather than
  dropped. A publication quietly narrower than the screen it was approved from
  is one the user believes covered every repository they read.
- The key map's `A / C / P — approve/change/pending` becomes two entries. There
  is no pending review action and there never was: the line named nothing before
  `P` was bound and misnamed it afterwards. The task panel's own hints gain `P`,
  which was the only key on that panel absent from the footer of the only screen
  it works on.

What does not change: publication stays host-side and user-approved (ADR-070),
the configured editor command keeps its own flags and is given the draft to open
(ADR-074), the daemon's plan-record-apply order is untouched (ADR-073), and
`feat task publish` is the same command with the same document.

Consequence: `internal/ui/publication.go` gains the document, the window, and the
gate; `internal/ui/app.go` gains the dialog case and witnesses the window on a
resize; `internal/ui/dialog.go` and `internal/ui/taskpanel.go` carry the key map
fix. [02-user-workflows.md](02-user-workflows.md) records that the draft is read
on the screen and edited in the editor. The screen's tests render through
`View()` at both sizes, which is what the omission this ADR is about would have
had to survive.
