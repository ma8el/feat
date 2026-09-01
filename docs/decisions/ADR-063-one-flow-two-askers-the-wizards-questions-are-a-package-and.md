# ADR-063 — One flow, two askers: the wizard's questions are a package, and the dashboard asks them itself

Status: accepted
Recorded: 2026-08-15, from asking whether the wizard is reachable from the TUI

The maintainer, after `feat project init` landed: is it possible to execute the
project wizard in the TUI? It was not. The dashboard's only answer to an
unconfigured machine was a sentence telling the user to quit and go and type.
Asked next whether it should be a dialog rather than a released terminal: it
should.

Evidence:

1. The dashboard is where a new user is. `feat` with no subcommand opens it, and
   it opens on a machine with no project as readily as on one with twelve — at
   which point the one thing that would help was somewhere else. Preparing a
   task, the key a new user presses first, could only fail there.
2. Releasing the terminal to the line conversation worked and cost two
   workarounds, which is what a wrong seam costs. Bubble Tea holds the interrupt
   while it has released the terminal (ADR-049) and the conversation ran in the
   same process, so Ctrl-C could not reach it and a Ctrl-D exit had to be taught;
   and the dashboard repaints the moment the command returns, so a "press Enter"
   pause had to be added to stop it eating the outcome.
3. Task preparation is already a multi-step form drawn as a dialog, with a step
   back out of an answer and the rail still visible behind it. Next to that, a
   released terminal reads as the odd one out: no going back, no cursor between
   fields, none of the dashboard's own shape.
4. What the two askers would share is most of what the wizard is. The draft
   renders and validates itself in `internal/config`, and the host discoveries
   live in `internal/project`; what was in the command was the sequence — each
   question's proposal, its validation, and what it decides about the next one.

Decisions:

- The questions are a package, `internal/wizard`, and both askers drive it.
  `Step` says what to ask, `Answer` applies one answer, `Back` undoes one, and
  `Review` renders and validates. Neither asker decides what comes next, what is
  proposed, or whether an answer is acceptable.
- The flow reaches nothing. What it needs to know about the machine — whether a
  path is a checkout, what Git says about it, which Compose files are beside it
  and what they declare — it asks through `Host`, which `internal/cli`
  implements over `internal/project`. That is what lets the dashboard drive the
  same questions while remaining a client that reaches no adapter (ADR-031).
- `feat project init` keeps its conversation and owns its presentation: the
  headings, the indentation, the brackets around a proposal, and the offers to
  diagnose and register at the end. ADR-062's reasons stand — it runs before
  there is a daemon, and its scrollback is what somebody debugging their own
  configuration reads back.
- The dashboard asks the same questions on `p`, as a dialog over the rail. It
  adds what a screen can add and a conversation cannot: a cursor on the closed
  questions, `esc` to step back out of an answer, and the file scrolled in a pane
  before it is written. It does not add a question, a proposal, or a rule.
- A proposal is a placeholder, never the field's contents. Enter takes it and
  typing replaces it, which is what the brackets mean at a shell — prefilling
  the field meant typing appended to it, and an identifier proposed from the
  working directory became that directory's name with the answer stuck on the
  end.
- Stepping back restores a snapshot rather than replaying the answers. An answer
  changes more than the field it names — an access mode decides whether a
  repository is asked for a mount point, a mode decides whether the devcontainer
  is asked about at all — so the flow keeps the state each answer replaced.
- The dashboard asks its backend to write the file and to register the project.
  The exclusive create that refuses to replace an existing configuration lives
  once, in `internal/wizard`, and the daemon is reached over the socket as it is
  for everything else.
- Diagnosis stays a command. The dialog says `feat doctor` checks the project
  against the host and does not run it: a report of findings is a screen of its
  own, and the dashboard has never had one.

Consequence: `internal/wizard` holds the flow and its tests; `internal/cli` holds
the conversation, the host, and the file; `internal/ui` holds a dialog that draws
questions it does not author. A question added to the flow appears in both
askers, which is the property the split exists for. What this does not do is
diagnose from the dashboard, or offer the wizard where there is no daemon — the
dashboard is a client, and `feat project init` is the answer on a machine that
has never run Feat.

Amended by ADR-084: the list of what the dialog adds and the conversation does
not — a cursor on the closed questions, `esc` to step back out of an answer,
`tab` to complete one — is what that change deletes. All three are both askers'
now, drawn by one extracted widget. What the dialog still adds is what a screen
adds: the trail, the file scrolled in a pane, and the two screens after it.
