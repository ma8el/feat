# ADR-084 — `feat project init` asks with the dashboard's widget, inline, and what it leaves behind is the transcript

Status: accepted
Recorded: 2026-08-29, from walking the command's worst question on a narrow terminal

The maintainer, at the question asking how a repository takes part in a task:
five options, joined with slashes, in brackets, answered by retyping
`stable_read_only` exactly. About 119 columns before the cursor, wrapped on any
80-column terminal, and asked once per repository. ADR-063 split the wizard into
one flow and two askers so that a question added once appears in both. It
carried the questions. It did not carry the interface: the flow describes each
question richly enough to be drawn well, and the command line used about a third
of what it was handed.

Evidence:

1. **`Options` is a list, drawn as prose.** Eight closed questions are defined
   in the flow — repository access, another repository, editable, primary,
   execution mode, Claude volume, application services, and each repository's
   contribution — and three of those are asked once per repository. Every one is
   a cursor and Enter in the dashboard and a retyped word at the shell, and the
   shell is where the exact spelling of `stable_read_only` had to come from.
2. **`Candidates` reached the conversation and were thrown away.** `Step`
   assembles them for every text question and the dashboard binds Tab to them
   (ADR-077); the conversation had no completion at all. So the user
   configuring a project from a shell retyped an absolute Compose path that the
   user configuring one from the dashboard took with a keystroke and edited.
3. **`Back` was never called.** `Wizard.Back` has existed since the flow was
   written, restores a snapshot rather than replaying answers, and its only
   caller outside the flow's own tests was the dashboard. A user who noticed at
   the mount-point question that they had mistyped a repository path three
   questions earlier had Ctrl-C and a fresh start. This is the one of the three
   that costs a user their work.
4. **What the second asker needed, the first one already was.** `askingKey`,
   `take`, `options`, `optionLabel`, and `choiceIndex`, with `questionView`,
   `field`, `below`, and `optionsView`, are a Bubble Tea sub-model over one
   `wizard.Question` that happened to be embedded in a dashboard dialog. That
   turns this change from a third rendering of the same question into the second
   rendering used twice, which is the same argument ADR-063 made about the
   questions themselves.
5. **The obvious selection rule would have made the line asker dead code.**
   `feat project init` already refuses unless both process streams are
   terminals, so there is no such thing as a real run with a redirected stdin.
   Under a terminal-only test the line conversation would have run for the
   command's sixteen scripted tests and for nobody else: sixteen tests guarding
   a path no user reaches.

Decisions:

- **The questions are drawn, and the command is still a conversation.** Each one
  is a Bubble Tea program of its own: it draws the question, takes the answer,
  prints the answered question as one permanent line, and exits. No alternate
  screen, so the transcript accumulates in the scrollback exactly as it did when
  every answer was typed. ADR-062 decided the wizard was a conversation and not
  a screen for two reasons; the first — it runs before there is a daemon —
  is untouched and is why this is a widget in a command rather than the
  dashboard's dialog, and the second — its scrollback is what somebody debugging
  their own configuration reads back — is now honoured by construction rather
  than by avoiding the widget. What moves is the conclusion, not either reason.
- **`Back` grows the transcript; it never erases one.** `esc` returns a step
  back rather than an answer, the conversation calls `Wizard.Back`, and the
  restored question is printed *below* what is already there, under a marker
  naming what it returned to. Erasing was rejected on two grounds: scrollback is
  the property above, and rewriting lines an inline program has already emitted
  is the part of terminal handling that breaks differently on every emulator.
  The marker is what makes a transcript holding two answers to one question read
  as a correction rather than as a contradiction.
- **A heading opens under a rule, and so does the composed file.** A transcript
  of questions is a column with a live prompt at the bottom of it; a transcript
  of answers is a column with nothing moving in it at all, and a section's
  heading is then a bare line at the left margin among indented lines that look
  much like it. The blank line that used to mark it is what a transcript of
  answers is already full of. The rule is measured against the paragraph it sits
  over rather than against the terminal, which on a wide screen would make it
  three times the width of anything it separates. A section's detail is parted
  from the answers under it for the same reason, which is the break the dialog
  already draws under a question's detail — and one blank line is opened where
  there is not one already, because a second is not twice the separator but a
  gap that reads as something dropped.
- **The widget is a package, `internal/ui/ask`, and it owns the styles it draws
  with.** The four tokens it needs are defined there and read back by
  `internal/ui` for the rest of the dashboard, and the palette they are built
  from moved with them, because a palette is chosen as a set and splitting it
  three and three would have left the essay explaining the set in one package
  and half of what it explains in another. Passing a style set in as a parameter
  was the alternative, and it was rejected for what it permits: two callers of
  one widget, free to drift its appearance apart. The dependency runs one way,
  and the command never imports the package that draws the dashboard in order to
  draw a prompt.
- **The rich asker when the command's input is a terminal and `TERM` is neither
  `dumb` nor empty; the line conversation otherwise.** The test is on the reader
  the command actually holds rather than on the process streams, because that is
  the one a scripted conversation replaces — the command's sixteen tests set an
  interactive run with a `strings.Reader` and reach the line asker by that
  clause, unchanged. The `TERM` clause is what gives the line asker a user who
  is not a test: an Emacs shell buffer, a stripped CI terminal, anything that
  mangles raw mode. Without it the plain path would be guarded by tests and
  reached by nobody, which is evidence 5. There is no `--plain` flag: an
  environment variable already reaches the plain asker, and a flag would be a
  support surface for a case that has one.
- **Every yes-or-no the command asks is drawn by the same widget.** Whether to
  write the file, whether to check it against this machine, and whether to
  register it are three questions of the shape the flow's confirms have, asked
  after ten answered with a cursor. Leaving the one that writes the file as a
  bracketed prompt would have made the command's most consequential answer the
  only one given differently. The review itself, the exclusive create, and the
  report the diagnosis prints stay line output: they are read, not answered.
- **ADR-063's list of what the dialog adds and the conversation does not
  narrows.** The cursor on the closed questions, `esc` to step back out of an
  answer, and `tab` to complete one are both askers' now. What the dialog still
  adds is what a screen adds: the trail saying which part of the file is being
  answered, the file scrolled in a pane before it is written, and the diagnosis
  and registration screens ADR-064 put behind it.
- **ADR-077's constraint is retired, and its sentence moves into the flow.** It
  reasoned that the flow's own text must not name a key "because the
  conversation has no such key to name", and drew "press tab to use one of them"
  in the dashboard instead. The conversation has the key now, so the sentence
  belongs where the list it is about is derived: the flow says it, on the
  questions that offer files without proposing one, and both askers show it
  because both read their notes off the question. The cost is stated rather than
  hidden: the line fallback prints a sentence naming a key it has not got. That
  is the one place this change leaves the flow's text ahead of an asker, and it
  is preferred to a sentence per asker, which is the drift the one-flow rule
  exists to prevent.

Consequence: `internal/ui/ask` is new and holds the question widget, the four
style tokens, and the palette; `internal/ui` reads all of them back and its
dialog embeds the widget rather than holding its parts, with its own tests
unchanged as the extraction's regression suite. `internal/cli` gains an asker
seam with two implementations, and the conversation gains the step-back branch,
the marker, and the rules over what opens a part of the transcript; `internal/wizard` gains four words on one note. No new module
dependency — `bubbletea`, `bubbles`, and `lipgloss` were already direct — no
configuration change, no schema change, and no daemon change.
[02-user-workflows.md](02-user-workflows.md) §1 stops attributing the cursor,
`esc`, and `tab` to the dialog. What this does not do is convert `feat publish`
or `feat cleanup`, which put their confirmations through the same prompter and
are a separate change with their own reasons; or change any question, any
proposal, any validation, or the order they are asked in — an asker still
decides nothing.
