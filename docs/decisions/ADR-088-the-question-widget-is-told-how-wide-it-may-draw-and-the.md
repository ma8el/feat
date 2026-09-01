# ADR-088 — The question widget is told how wide it may draw, and the flow's line breaks become its default rather than its limit

Status: accepted
Recorded: 2026-08-30, with the implementation

Reading `configure a project`'s services question on a wide terminal: a box
taking three quarters of the screen, a paragraph wrapped at about seventy cells
inside it, and one note running to the right edge and ending in `…`. Nothing was
misconfigured. ADR-084 extracted the question widget so that a question added
once is drawn once and asked twice, and it carried everything about a question
except how much room there is to say it in.

Evidence:

1. **`ask.Model` holds a question, a field, and a cursor, and no width.**
   `SetWidth` sets `m.input.Width` — the *field's* width. `Context()` renders
   `Detail` and `Notes` straight out, and the dialog calls it unwrapped:
   `wizard_view.go` is `w.input.Context() + w.input.View()`.
2. **`Detail` is authored as pre-wrapped source lines.** Twelve blocks in
   `internal/wizard/wizard.go` are literal lines of at most seventy-six cells.
   Measured on the services question at 100, 120, and 160 columns, the paragraph
   wrapped at 71/73/13 cells at every one of them, because the wrap had already
   happened somewhere that could not see the box.
3. **`Notes` are single long strings, and the cut lands on the payload.**
   `provenance` returns one sentence of a hundred and forty-seven cells with the
   service list appended, so a hundred and sixty with two services. It was cut to
   the dialog's inner width at every terminal size, and it is cut *at the end* —
   which is where the list of services the note exists to name is.
4. **A truncated line is also a line that pins the box.** `clampBlock` truncates
   each line to `inner`, so an over-long line reports itself as exactly `inner`
   wide. The same note therefore took the box's full three-quarters allowance to
   show a sentence it had already cut short: at 160 columns the widest thing that
   needed room was the field at 103 cells, and the box was drawn at 120.
5. **`Context()` has one caller, and it is the dialog.** `internal/cli` never
   calls it. The conversation prints the same fields itself, in `introduce` and
   `announce`, with its own indentation and section rules — which is what
   `Context()`'s own comment says it does. So a width given to the widget reaches
   the dashboard and nothing else.
6. **The flow's hard wrap is load-bearing for the other asker.** `ruleWidth` in
   `internal/cli/init.go` is derived from it in as many words: the flow wraps its
   detail to seventy-six columns and the conversation indents by two. ADR-084
   chose that measure over the terminal's by name, because a rule taken from a
   wide terminal is three times the width of anything it separates.

Decisions:

- **The widget takes a width, and it is not the field's.** `SetContextWidth` is
  separate from `SetWidth` because the two measure different things and both
  callers already derive them differently — the dashboard's field is the block
  less its own indent and capped at a hundred cells, and the command's is the
  terminal less the question's indent. A widget that guessed one from the other
  would be guessing at a number its callers know. Nought is the default and
  means the flow's own lines, which is what evidence 5 makes free.
- **The rejected alternative was to stop hard-wrapping `Detail` in the flow.** It
  is the tidier shape — a layout decision does not belong in a data structure —
  and it is paid for twice by the asker that cannot afford it. `announce` runs
  outside the Bubble Tea program and has no terminal width to fold to; wrapping
  to the live terminal gives two-hundred-cell paragraphs in a transcript whose
  purpose is being read back later, and puts the rule back to the width ADR-084
  rejected. Wrapping to a constant instead moves the seventy-six from
  `internal/wizard` to `internal/cli`, re-derives `ruleWidth` from it, and leaves
  the dashboard the only beneficiary — which is what this decision achieves by
  touching two files.
- **The flow's breaks become the default rather than the limit.** A caller with a
  width rejoins each paragraph — the flow already marks paragraphs with a blank
  line — and folds it again; a caller without one draws the lines it was given.
  Grouping rather than joining is what lets both readings come out of one place,
  and neither asker's text is authored twice.
- **A note that folds hangs under its own first line.** A second line starting in
  the bullet's column reads as a second note, and these are the lines carrying
  what Feat found out about the previous answer.
- **The fold itself moves into `internal/ui/ask` and the dashboard reads it
  back**, as it already reads the four style tokens back. The dashboard had a
  wrapper for the paths and errors it draws around a question and none for the
  question's own prose, which is one screen folding two kinds of text on two
  measures. The dependency still runs one way.
- **Each line is styled rather than the block.** A style applied to several lines
  at once pads them all out to the widest, and a padded line reports itself as
  exactly as wide as the fold — which would have re-created evidence 4 with the
  fold width in place of the truncation. Measured after the change: at 160
  columns the box is 116 rather than 120, and the note ends in the service list.
- **And then the wizard's own allowance drops to half the terminal, where every
  other overlay keeps three quarters.** The fold fixed the truncation and
  inherited the box's measure: a hundred and sixteen cells is past where a line
  stops being read and starts being scanned back to, and this is the one overlay
  whose content is prose rather than the paths, identifiers, and lists the others
  hold — width is legibility for those and a cost for this one. Measured: 90 → 60
  at 120 columns and 120 → 80 at 160. The change is the wizard's alone;
  `dialogLimits` is untouched, because halving it stacks the key map into one
  column of forty-five lines in a dialog that holds twenty-six, truncates `feat
  doctor` findings and cleanup identities at fifty-two cells, and makes a
  publication draft take about half again as much scrolling before ADR-076's
  reading gate opens.
- **It has a floor, and the floor is what its own keys need.** The review step
  names sixty-five cells of them and half of a hundred-and-twenty-column terminal
  is fifty-six, so the narrower measure would have cut `ctrl+c cancel` off the end
  of the one step that writes a file. A hint that is cut is a key nobody can
  press. The floor binds below about a hundred and forty columns, never exceeds
  what the overlay would have been allowed before, and is read back by a test
  rather than trusted as a written-down number.

One accepted cost, stated because it is visible on an ordinary terminal: at 120
columns the review step now folds the composed file into fifty-four cells, so the
comments explaining each field — seventy-five to eighty-three cells as the
generator writes them — are cut with an ellipsis where before they fitted. The
file is scrolled and confirmed rather than read for its prose, and the bytes that
are written are unchanged either way; if that reads as the wrong trade the review
step is the one step that could keep the wider allowance, at the price of a box
that changes width when the user reaches it.

Consequence: `internal/ui/ask` gains `SetContextWidth`, an exported `Wrap`, and
the paragraph grouping; `internal/ui`'s `wizardModel` hands the width over in
`resize` and its own `wrap` delegates, and gains `wizardLimits`, `wizardSize`,
and `wizardSmallest` beside the shared `dialogLimits` and `preparationSize`.
`internal/wizard` is unchanged — no
question, proposal, validation, or order moves — and `internal/cli` is unchanged,
with a test added that the transcript still ends the flow's lines where the flow
ends them. No API change, no stored-format change, no schema version, and no
daemon change. ADR-084 stands: this adds the one thing the split left out, which
is that the caller with a width had no way to say so.
