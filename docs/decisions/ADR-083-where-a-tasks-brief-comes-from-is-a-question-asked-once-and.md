# ADR-083 — Where a task's brief comes from is a question, asked once and after the project

Status: accepted  
Recorded: 2026-08-29, from the interface for a model the domain already had

Feat models three brief sources and treats them as equals in the domain —
`SourcePrompt`, `SourceMarkdown`, and `SourceTicket`
(`internal/domain/task.go:89`) — each recorded on the task and carried on the
wire (`internal/api/dto.go:324`). The interface treated them as anything but
equal. One was the default, one was a keystroke nothing announced until the user
was already past the moment they would have wanted it, and one could not be
chosen from the dashboard at all.

Evidence:

1. **A Markdown brief could not be imported from the dashboard.** `--file` was
   read by the command before the screen opened, and preparation had no other
   way in: `prepareStart.brief` was filled by the caller or it was empty. A user
   who wrote the brief in an editor first had to leave the dashboard and
   re-enter through the command line, carrying a path by hand between two parts
   of the same program — which is the copy-pasting between systems that
   [01-product-vision.md](01-product-vision.md) names as the job Feat is for.
   Ticket → brief is a carry Feat removes; editor → brief was the same carry,
   removed only for people who knew the flag before they started.
2. **The ticket route was announced only after it was too late to matter.**
   `ctrl+t` was bound on the brief screen and named in that screen's key hints.
   It was discoverable, but only once the user was standing in the field they
   were about to type the brief into, and `feat implement`'s own help described
   it as "one key press away while you are writing the brief" — an accurate
   description of a door placed behind the room.
3. **The flag decided for a user who had not been asked.** `--file` and
   `--ticket` are refused together with "a task brief comes from one source",
   which is the product's own model, stated in an error, at the one moment the
   user cannot act on it.
4. **Two of the three answers need the project.** The tracker is configured per
   project (ADR-071) and the file completion is worth seeding from the project's
   own checkouts, which is the same reason `enterBrief` already deferred a
   `--ticket` lookup to the moment the project was known.

Decisions:

- **The source is asked once, after the project, and a flag is an answer to
  it.** One step between project and brief, offering "write it here", "from a
  ticket", and "from a Markdown file". `--file` and `--ticket` preselect the
  source exactly as `--project` preselects the project, so a run that passes one
  skips the step and neither flag's behaviour changes. "write it here" is first
  and the cursor opens on it, so Enter-Enter reproduces the preparation the
  screen has always had.
- **Selecting a source resets the brief and fills it from that source.** A
  ticket with the composed document, a file with its text, "write it here" with
  nothing. No confirmation and no exceptions. This follows from the shape of the
  step rather than being a policy bolted onto it: every source converges on the
  same editor, so the editor — not the source step — is where content is
  reviewed and adjusted, and there is nothing to return to the step *for* except
  starting over. A user who wants to keep what they have never leaves the
  editor.

  Three alternatives were considered and rejected, and all three exist only if
  re-entering the step can mean something other than "start over": leaving the
  fields alone unless they are empty; dropping the reference while keeping the
  text; and confirming before an overwrite. They are recorded because each
  becomes live again the day anything else can navigate to this step.
- **`esc` from the brief therefore destroys work, deliberately and with no
  guard.** There is no forward path back to the editor that does not pass
  through a selection, and every selection resets, so the key means "start
  over". A confirmation on it was offered and declined: a key whose whole
  purpose is that does not need to ask whether the user meant it. `esc` from the
  source step goes to the project step where more than one project is registered
  and leaves preparation otherwise, which is what the brief step did before;
  `esc` from the brief goes to the source step unless a flag preselected the
  source, in which case there is no answer of the user's own to return to and
  the old rule stands.
- **Any change of source discards a recorded draft, and this is the one
  correctness item in the feature.** ADR-071 recorded this rule for tickets and
  reasoned about it as a ticket problem, because tickets were then the only path
  that could change a source mid-preparation. It is a source problem. A draft
  records where its brief came from when it is created and nothing later
  replaces that — updating one replaces its title, brief, and repositories — so
  with a source step reachable by `esc` from the brief, a task could be launched
  whose brief came from a file and whose recorded source says `prompt`, which is
  a record nothing can act on. "Source" means the whole recorded value and not
  its kind: ticket A to ticket B leaves the kind alone and changes everything a
  merge request would name. The rule implemented is therefore the simplest one
  that covers every case — *any* selection on the step discards the draft, since
  the brief is being replaced wholesale either way — and it lives in the one
  function every answer passes through rather than on the ticket path.
  Discarding removes nothing: nothing is created for a draft (FR-TASK-003).
- **`ctrl+t` is removed from the brief screen and from its hints.** A source is
  chosen in one place. A second door into ticket selection, opened from inside
  the editor, would be a key that wipes the document the user is standing in —
  which is the ambiguity the step exists to remove, and it would have to carry
  the reset rule into a screen whose whole purpose is that the brief is now the
  user's. It costs a sentence of `feat implement`'s published help and a line of
  key hints. With one way in, the ticket list and the file screen both belong to
  the source step: `esc`, a failed read, and an empty list all return to it, and
  the trail highlights `source` throughout. No field remembers where the user
  came from, because there is one place they can have come from.
- **The file is chosen in a path field with Tab completion**, reusing the
  completion the wizard dialog already has (ADR-077). Candidates are the
  project's repository host paths, the process working directory, and then the
  entries of whatever directory the field names, with directories marked by a
  trailing separator. It handles an absolute path, a `~` path, and a path
  outside every project without a navigation model, and a user who knows the
  path types it. `bubbles/filepicker` was rejected: it pulls
  `github.com/dustin/go-humanize` into a module with eight direct dependencies
  and no indirect one the TUI did not need. A hand-rolled directory list, in the
  style of every other list on this screen, was not rejected — it is the upgrade
  if navigation turns out to be what people want, and it is a screen rather than
  a redesign.
- **A project with no tracker keeps the option and gets the explanation.**
  Choosing "from a ticket" against a project that configures none fails locally
  and immediately, with the daemon's own sentence — "project X configures no
  tracker, so Feat has nowhere to read tickets from; add a tracker section
  naming a command that prints them" (`internal/daemon/tickets.go:100`) — for
  one socket round trip and no network wait, and the user stays on the step
  where the other two answers are. The alternative, publishing tracker presence
  on `api.Project` so the option could be drawn as unavailable, is recorded and
  not recommended: `api.Project` is built from the stored domain snapshot, which
  does not carry the tracker, so the flag would be stale the moment a user added
  a `tracker:` section, and loading each project's configuration on every list
  is a cost every caller pays for one screen. A visible option that explains how
  to configure the thing it needs is also the better answer to a finding this
  work was specified alongside: the tracker section stayed unconfigured on the
  project Feat is dogfooded in, and every surface that could have mentioned it
  stayed quiet. This adds a surface that names it, which is a partial mitigation
  and not a fix.
- **An imported file fills the title only when the title is empty**, which is
  the rule the `$EDITOR` round trip already follows rather than the
  import-at-start path's, which overwrites. A file whose text is only whitespace
  is refused on the import screen rather than at `enterRepositories`, because
  the import screen is where the user can do something about it.
- **The reading policy moves to `internal/brief`**, one exported function,
  `Read(path) (text, absolute string, err error)`, carrying `--file`'s rule
  unchanged: expand a leading `~`, absolute the path, refuse a directory, refuse
  over 256 KiB. `internal/ui` must not import `internal/cli` — the dependency
  runs the other way — so the rule belongs to a package both can call.
  `internal/brief` knows nothing about `api.Source`, so each caller builds its
  own; putting the function in `internal/ui` and having the command call it
  would also compile, and would put a file-reading rule in the package that
  draws screens. The file is read by the client and its path is never named to
  the daemon, which is ADR-028's rule and what `--file` already did.

What this changes about approval is nothing, which is the point. An imported
document goes into the same editable field a typed prompt is written in, so the
confirmation, the fingerprint, and every other invariant of preparation apply to
it unchanged — and what the confirmation displays is that document rather than
the file it came from. That is ADR-070's inbound argument, and it applies here
with the same force: a Markdown file is also text somebody else may have
written.

Consequence: `internal/ui/prepare.go` gains a step, its key handler, the two
screens behind it, the reset rule on selection, and the discard rule
generalised, and loses `ctrl+t` and its handler; `internal/ui/prepare_view.go`
gains two views and two key-hint sets and loses the `ctrl+t` hint;
`internal/brief` is new and `internal/cli/implement.go` calls it instead of
holding it. `feat implement`'s long help stops saying the ticket list is a key
press away while you are writing the brief.
[02-user-workflows.md](02-user-workflows.md) §2 gains the step and §3 names
where the list is reached from. No daemon change, no configuration change, no
schema change, and no new module dependency. The step implements what was
already specified — FR-TASK-004's "MUST offer them as a selection" and workflow
2's "enters a prompt or imports Markdown" — and contradicts no recorded
decision.
