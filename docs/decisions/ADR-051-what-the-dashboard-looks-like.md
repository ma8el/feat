# ADR-051 — What the dashboard looks like

Status: accepted
Recorded: 2026-08-11, alpha review

ADR-041 decided what the dashboard is *shaped* like — a rail, a main region, and
a footer, all on screen at once — and left what it looks like to whatever each
screen was written with. The maintainer read the result in use and reported four
things, and each of them turned out to be a rule the dashboard did not have
rather than a preference about taste.

Evidence:

1. The two colours a user reads most are not from the same family. The selection
   colour was a pale sky blue (`#7cc4ff` on dark) and the attention colour a
   saturated orange (`#ffb454`), which differ in saturation as much as in hue:
   one is washed out and the other shouts. A rail holding both reads as two
   programs sharing a window, and the difference the eye actually registers is
   loudness rather than meaning.
2. Every project header offered a control that did not exist. The rail drew
   `▾ project` above each group from the day it was written, which is the
   universal marker for a disclosure triangle, and nothing was bound to it. A
   control a user tries and finds inert is worse than no control: what it teaches
   is that the rest of the screen may also be decoration.
3. Neither region's header was separated from its content. The rail's heading and
   the tab bar were the first line of their own block, so an eye scanning down met
   `tasks` and the first task, or the tab bar and the first line of a rendered
   pane, as two entries of one list. The tab bar had the same defect twice over:
   the selected tab differed from the others in shade alone, which is a
   comparison rather than a thing seen.
4. The footer was not separated either. It is the one part of the frame that
   holds still while the regions change, and nothing said so.
5. The regions were divided by a column of `│`. Two blocks sharing one drawn edge
   read as one block with a line down it, which is the opposite of what the layout
   is for: the rail and the main region are about different things and answer
   different questions.
6. Sizing was left to lipgloss, which re-flows what it is given. A line wider than
   the region was wrapped rather than cut, so the task panel's long sentences put
   their tails against the region's left edge, under an indented label they did
   not belong to — and the panel's own scroll arithmetic, which counts lines
   before they are drawn, could not see it.

Decisions:

- The palette is six named colours in one file, chosen as a set: an accent for
  what the user has chosen, an amber for what may want them, a red for what has
  failed, two neutrals for text and its labels, and one quiet colour for every
  line the layout is drawn with. The accent and the amber are at the same weight,
  so the eye reads the difference as meaning. Every colour stays adaptive, because
  a terminal's background belongs to the user.
- Each region is a card: a rounded box with a header row, a rule between that
  header and its content, and content that is cut to the box rather than re-flowed
  inside it. The two are set apart by a blank column rather than joined by a drawn
  one. The footer is ruled off from both.
- The rail's heading and the tab bar become those headers, and each carries a
  summary on its right: how many tasks are waiting, and which task every tab is a
  view of. The selected tab takes the accent as a background, which is what the
  focused task entry already did.
- The region holding the keyboard says so by taking the accent for its border.
  An overlay's border is always the accent, because an overlay always has the
  keyboard.
- A project folds, on the space bar, which is what its marker has been promising.
  A folded project keeps saying how many tasks it holds and whether any of them
  wants the user: a fold that could hide the one task that stopped would make the
  rail unsafe to fold at all. The cursor never stays inside a fold, because the
  main region draws whatever the cursor is on and the keys act on it.
- The box is drawn by Feat rather than by lipgloss, a line at a time, cutting by
  cell through the escape-aware primitives the overlay already uses and ending
  each line's styling before the border it is about to write. The content of the
  main region is a rendered tmux pane: a capture carries the colour tmux emitted
  and not the clearing tmux does as it draws, so a line re-flowed mid-escape-
  sequence sets a colour and keeps it, across the border and the region beside it.
- The one body that is re-flowed is the task panel, deliberately and before it is
  measured. It is the only prose the dashboard draws, its sentences say what to do
  about what they report, and wrapping before the split is what keeps the scroll
  honest: the lines counted are the lines drawn.
- A task list too long for the rail is cut above the rail's foot and says so,
  naming the key that makes room. The region used to be clipped by the layout,
  which cut from the bottom and so took the machine's figures — the one part of
  the rail that is read by position — instead of the tasks.

Consequence: the frame is four cells narrower and two lines shorter for its
content than it was, which the main region pays: 79 cells at a 120-column
terminal and 55 at the narrowest supported width, where it was 87 and 63. That is
the price of the borders and the gutters, and it buys the separation the four
reports above were all about. No stored format, endpoint, or state moves; the
task-preparation dialog is also sized to the dialog it is drawn in rather than to
the terminal, which is where its ellipsis-per-line came from. `space` is the one
new binding.

Amended by ADR-052: the rule that the cursor never stays inside a fold is what
made folding a one-way door, and a fold is now a cursor position of its own.
