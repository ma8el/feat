# ADR-077 — A proposal is a value the user can take into the field and edit, and what the flow derived beside it is offered rather than only named

Status: accepted  
Recorded: 2026-08-27, from walking the wizard question by question on the reference project

The maintainer, at the question asking which Compose service the agent runs in,
looking at the service the flow had derived correctly and twice over: not sure if
this is a placeholder. Enter would have taken it. Nothing on the screen said so.

Evidence:

1. One visual device carried two opposite meanings. ADR-063 made a proposal a
   placeholder — Enter takes it, typing replaces it — while the preparation
   dialog uses a placeholder for a genuine hint, "what the task is, in a few
   words", which is not a value any key accepts. A user cannot tell the two
   apart, and the one that mattered was the one they hesitated over.
2. A proposal could be taken whole or retyped whole, with nothing in between.
   Six of the flow's questions take a path — the checkout, the agent's Compose
   files, the agent's mount point, a repository's Compose files, where its
   services expect its source, and an environment file — and three of those are
   loops. Wanting a proposed absolute path with its last segment changed meant
   reading it off the screen and typing all of it back in.
3. The derivation was never the missing part. `ComposeFiles` finds every Compose
   file beside a repository and `proposedContribution` takes the first;
   `ComposeServices` finds every service and `suggestService` picks one;
   `Composition.Services` and `.Reachable` are joined into a string. Feat had
   worked the answers out and had one slot to put them in.
4. One of those lists was not merely unproposed, it was unsaid. The Compose
   question writes the other files it found into its own notes, and `Step`
   assigned the flow's notes over the top of them — so "others found beside it"
   has never reached either asker, in any run, at any point. A list with nowhere
   to go stops being maintained as a list, which is what makes this the cheapest
   moment to give it one.
5. The widget has shipped this since it entered `go.mod` and it was switched
   off. `bubbles@v0.21.0` has `ShowSuggestions`, `SetSuggestions`, Tab bound to
   accept, `down`/`ctrl+n` to step, and dim rendering of the completion. What
   this cost was a field that defaults to false.

Decisions:

- `Question` gains `Candidates`: the values an asker may complete a text answer
  to. `Step` assembles them — the proposal first, then whatever the question
  found beside it — so a question with a proposal always has at least one.
- The proposal is the head of that list, always. A list whose first entry was
  something else would be two answers to one question: the dashboard's Tab would
  offer one value and the conversation's brackets another, and the same question
  would mean different things depending on where it was asked.
- Candidates are offered and never required. An empty answer still takes
  `Proposed`, an optional question is still finished by leaving it empty, and an
  asker with no way to complete ignores the list. `feat project init` keeps its
  brackets and is unchanged.
- Tab moves a value into the field and never past it. An empty field takes the
  proposal, a field holding one candidate exactly steps to the next and around
  the list, and everything between the two is the widget's own prefix
  completion. What is in the field is what Enter sends, whichever key put it
  there — so completing is editing an answer, not giving one.
- Closed questions have no candidates. Their answers are `Options`, they are
  drawn as a list with a cursor, and nothing is typed into one.
- A question's own notes are appended to the flow's rather than assigned over
  them, in the order the two became true: what the last answer established, then
  what this question found out about what it is proposing. Both askers read them
  off the question, so the flow no longer has a second way to hand the same
  sentences over and a note that reaches one asker reaches both.
- The runtime Compose question offers every file it found beside the repository
  that has not been answered yet, at both ends of its loop, and still says so in
  a sentence. The sentence is for the asker that can only print, and it is the
  one a user reads back in their scrollback afterwards.
- The repeat of that loop offers without proposing, and this is what the
  completion is for. A loop could not both propose the next file and finish on an
  empty answer — two meanings for one key, and finishing lost: a user pressing
  Enter at "blank to finish [/some/path]" added the bracketed file instead,
  twice, and ended up with an application's Compose files defining the container
  their agent runs in. The rest of the files now reach the user through Tab, so
  Enter is left meaning what the prompt says it means and the collision stops
  existing rather than being worked around.
- What is left to add is named at the repeat as well as at the question before
  it, in the same sentence, because it is the same thing in both places: the
  files found beside this repository that are not in the field. A completion
  cannot draw itself into a field nobody has typed in, so a prompt about
  finishing over an empty field reads as a loop with nothing left in it — and the
  reason this loop repeats is that Compose merges a base with the overrides
  beside it. The sentence is the flow's, so it reaches the conversation too,
  where the repeat had nothing to go on at all.
- The sentence names no key, and the dashboard says the key itself, under the
  field, where nothing in the field shows what Tab would give. A flow that named
  a key would be wrong in the conversation, which has none; a question that
  proposes something already has that value under the cursor and needs no
  sentence about it.

Consequence: `internal/wizard` gains one field and one function that assembles
it, and loses `Notes`, whose one caller now reads them off the question;
`internal/ui/wizard.go` turns the widget's completion on, sets the suggestions
per question, and adds one key; the key hints say `tab` on the questions that
have something to complete. The field is drawn in the width it was given, which
it was not: the widget pads its line from the typed value and writes the
completion after the padding, so this dialog — which is as wide as its widest
line — jumped to its full allowance on the first character of a path and crept
back a cell per keystroke afterwards. This amends ADR-063's "a proposal is
a placeholder, never the field's contents" — it is still never the field's
contents until the user asks for it to be, which is the part that decision was
protecting. What this does not do is derive anything new: the agent section's
Compose file, service, user, and mount questions still propose too little, and
each of them is its own change. This is what those changes now have somewhere to
put.

Amended by ADR-084: the decision that the flow's sentence names no key rested on
the conversation having none to name, and the conversation has one. The sentence naming `tab` moves out of
the dashboard and into the flow, on the questions that offer files without
proposing one, so that both askers say it and neither says it twice.
