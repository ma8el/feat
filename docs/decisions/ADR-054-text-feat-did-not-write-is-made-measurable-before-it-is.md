# ADR-054 — Text Feat did not write is made measurable before it is measured

Status: accepted  
Recorded: 2026-08-12, alpha review, after ADR-053

ADR-051 made the dashboard draw its own frame a line at a time, cutting by cell
through escape-aware primitives, precisely so that content could not run into the
border. Every measurement it added asks one question of a string — how wide is it
— and answers it with the display width of the characters in it.

Evidence: the maintainer opened the task tab on a task whose gate had captured
`go test` output, and the frame came apart. The panel's lines crossed the border,
the rail was overwritten, the footer was drawn halfway up the screen, and the
machine block appeared three times with different figures. The cause is one byte.
`go test` separates its columns with tabs; a tab has a display width of zero and
is drawn by the terminal as a jump to the next multiple of eight. A captured line
measured forty-eight cells and was drawn as sixty-two. Everything below it was
then painted over rows the renderer believed were somewhere else. The same defect
was in two further places, neither reported: a task title carrying a line break
would have added a row to a rail that counts its own rows, and a wrapped error
carrying a command's output was written into a footer whose height the regions
above it are sized against.

Decisions:

- Text from outside — a brief, a captured check detail, an error another program
  produced, a title a user pasted — is converted before it is measured. Tabs are
  expanded to the terminal's own stops, so the columns they were holding apart
  survive as spacing; the other C0 controls are dropped, because they move the
  cursor and nothing that moves the cursor may reach a screen laid out by counting
  cells.
- Escape sequences are not touched. The styling is Feat's own, and the conversion
  is by byte in a range no escape sequence uses.
- Values drawn on one line have their line breaks removed as well as their
  controls. A count of rows is the rail's and the footer's arithmetic, and a
  string that can add one is a string that can break it.
- The rendered tmux pane is not converted, and must not be. It is drawn by the
  splice ADR-042 describes, which is by cell and passes every escape through; its
  content is a grid tmux has already laid out, so the controls that break a layout
  are not in it.
- Storage keeps what the command actually emitted. The width constraint belongs to
  the screen, not to the record.

Consequence: `internal/ui` gains one conversion and three call sites — the task
panel before it is wrapped, the rail's title, and both footers. No stored format,
endpoint, or state moves. The general rule this leaves behind is worth more than
the fix: anything the dashboard did not compose is untrusted input to a layout,
and a region that measures what it draws has to be given something measurable.
