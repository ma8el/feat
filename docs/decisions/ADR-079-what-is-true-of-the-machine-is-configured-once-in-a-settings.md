# ADR-079 — What is true of the machine is configured once, in a settings file with no per-project override

Status: accepted  
Recorded: 2026-08-27, from the six project configurations on the author's machine

Three sections of project configuration were never about a project. Feat had no
file to put them in, so they were written once per project and reconciled
afterwards — in one case by a rule the code's own comment already called a
mistake.

Evidence:

1. They are byte-identical wherever they appear. Of the six projects configured
   on the author's machine, three define these sections — `feat`, `jobharbor`,
   and `jobharbor-dev` — and all three define them identically, down to the
   argument vectors: `git diff {base_commit}`, `nvim {repository_path}`,
   `git status --short --branch`, `desktop: true`, both grace periods `5s`,
   `suppress_while_attached: true`, `sample_interval: 2s`. The other three define
   none of them and get the same values as defaults.
2. `resources` was a correctness problem rather than a preference. Sampling
   produces one figure for the whole machine, so `resourceInterval` listed every
   registered project and called `config.Load` on each *inside the sampling
   loop* — six YAML files read and parsed from disk every two seconds to decide
   one number — and then reconciled the answers with "the most eager project
   wins", a rule that existed only because the setting was in the wrong file. The
   comment above it said so already: *an oddity of the configuration model rather
   than a design*.
3. `review` had already told us where it belongs. `review.editor` falls back to
   `$EDITOR`, so the configuration model was reaching for a user-level source for
   this value and had no user-level file to reach for — it reached into the
   environment instead. The commands are the user's tools: an editor is a fact
   about the person, not about the repository they are opening.
4. `notifications` is about the machine and the person at it. `desktop` is
   macOS-only in this build, and `suppress_while_attached` is a question about
   whether somebody is looking at a terminal. Neither varies by project, and
   neither did.
5. The layer did not exist, which is the cost. `~/.config/feat/` held `projects/`
   and one hand-placed Compose override; there was no global file of any kind. So
   this *adds* a concept — a file, a schema, a command, and diagnostics — while
   most of what is being decided around it removes them. The answer is still yes,
   but it is not free and should not be recorded as if it were.
6. It buys nothing for setup, and the case must not be made on setup. The wizard
   asks about projects, repositories, agent, services and checks, and has never
   asked about any of these three. Moving them saves nobody a question. The case
   is evidence 2 and not writing twelve identical lines once per project.

Decisions:

- A settings file at `~/.config/feat/settings.yaml`, beside the `projects/`
  directory rather than inside it, holding `review`, `notifications`, and
  `resources`. `.yml` is accepted, and both present is an error rather than a
  preference, for the reason a project configured by two files is.
- It is **global, with no per-project override.** Precedence rules are
  load-bearing and hard to remove once written, and nothing has asked for one.
  Adding an override later on evidence is cheap; removing one that turned out to
  be unused is not, because by then something depends on the order.
- The file is optional and every value has a default, so absence is a state
  rather than a failure: `LoadSettings` returns the defaults where a project's
  loader would return `ErrNotFound`. A project Feat was asked about and cannot
  find is a question with no answer; settings Feat was never told are settings it
  has.
- It carries its own `version`, separate from the project file's. Two files are
  two compatibility surfaces, and one number would make every change to either a
  version bump for both.
- The command is `feat settings`, not `feat config`. `feat project show` already
  prints project configuration, and two commands called "config" would blur
  exactly the line this change draws. `~/.config/feat/` keeps its name, which is
  XDG rather than a product noun.
- `feat settings show` marks every value as `default`, `configured`, or — for the
  editor alone — `from $EDITOR`. The file is optional and on most machines
  absent, so a printed value a user cannot tell from a default is one they cannot
  tell they set. The third marker is evidence 3 made visible.
- **No migration code.** Parsing is already strict, so a project file still
  carrying one of these sections fails to load as an unknown field rather than
  being silently ignored — loud, free, and enough at one user. The message is
  generic rather than naming the new home; a better one can be added later if it
  ever costs anything.
- `agent.claude.idle_grace_period` stays in project configuration. It is named
  here because the notification comments pair the two grace periods, so the
  question was asked and answered rather than left. It is provider-specific and
  lives inside the provider's own section, which is where CLAUDE.md keeps
  provider settings; the settings file has no provider section and gaining one
  would undo the distinction it is drawing. None of the three arguments above
  applies to it either: it is read once per idle transition rather than in a
  loop, it is about how an agent is driven rather than about the machine, and
  being identical in three configurations is what most defaults look like.
  Instead the settings schema names it where the confusion is, on
  `notifications.idle_grace_period`, and says which of the two is measured from
  where.

All three sections have moved. Each move deleted its section from the project
configuration struct and from the published project schema, and each broke the
three configuration files that carried it, by design.

- **`resources`.** `resourceInterval` no longer lists the projects, no longer
  loads their configuration, and no longer has a conflict rule to apply. It reads
  nothing at all: the interval is a number already in memory.
- **`notifications`.** `notifyPolicy` no longer takes a task, because there is no
  longer a project to ask. Its fallback went with it: a configuration that could
  not be read used to mean the default policy for that one notification, and the
  same fallback now happens once, at startup, for the machine.
- **`review`.** The commands are expanded per task exactly as before — the
  worktree, the base commit and the branch are still the task's — and only where
  the vector itself comes from has changed. `feat doctor`'s check that the
  configured programs exist followed them, from the project section to the host
  section: whether `nvim` is installed is one question with one answer, and asking
  it once per configured project reported the same three findings six times over.

`feat doctor` also gained a `settings` finding, which is the diagnosis the file
had nowhere to be reported from before. It is a host finding for the same reason:
one file, no override. A settings file that does not parse is an error and stops
nothing else — the defaults apply, in a running daemon as well as here, so the
rest of the diagnosis is still about the machine Feat will actually use.

One duration did not move, and the two are now separated by a file rather than by
a paragraph. `agent.claude.idle_grace_period` decides when an ended turn counts as
idle, which is provider-specific and a fact about how the agent is driven;
`notifications.idle_grace_period` decides how long idle must last before somebody
is interrupted, which is a fact about the somebody. Naming them alike was always
the confusion, and the settings schema now names the project's one where the
confusion is.

Settings are resolved **once, when the daemon starts**, and this is the one place
the settings file deliberately behaves unlike project configuration, which is
re-read on every operation. The difference is what the two are about: a project
file describes work in progress and is edited while Feat is running, and these
are the machine's own dispositions — how often it is sampled, whether it may
interrupt you, which editor is yours — which change about as often as the machine
does. Reading them per use was built first and rejected on what it looked like:
the sampler parsing a file every two seconds to be told the same number is the
same shape of problem that put the section in the wrong file to begin with, one
sixth the size. So changing a setting takes a daemon restart, which is a small
price paid rarely. A file that cannot be read costs the defaults rather than the
daemon — an optional file with a typo in it must not stop a control plane from
starting — and it is logged once at startup, beside the other thing this daemon
asks once and remembers.

**Only the daemon holds a snapshot. Every command reads the file as it is now,**
and that asymmetry is the part worth stating, because it is the part that
surprises: a maintainer who had removed the configured editor expected the old
one to survive until a restart, and it did not, because `feat settings edit` is
not the daemon. Making the client honour a daemon's copy instead would be worse
in three ways — it would need a daemon running to know which editor to open, it
would make `feat settings show` print stale values on the one command somebody
runs to check what they just wrote, and it would leave a machine with no daemon
with no configured editor at all. All three work today with no daemon,
deliberately.

Stating the rule was not enough, and that is the evidence: a footer naming the
daemon's behaviour, printed by a command that had just re-read the file, reads as
"Feat reads settings once". So `feat settings show` and `feat settings edit` say
it as a fact about this machine now rather than as a rule — *the settings have
changed since the daemon started* — answered from the endpoint record's ownership
time and the file's own modification time, so it costs one stat, asks the daemon
nothing, and works on the same terms as the rest of the command.

It is one line and it appears only where there is something to do about it. The
first version also reported the other states — no daemon, and a daemon already
working from this file — and they were cut on the reading that a sentence nobody
can act on is noise, which is the rule diagnostics already follow: a check with
nothing to report reports nothing (ADR-028). A machine with no settings file is
not compared either: there is nothing to stat, and a file deleted while a daemon
ran would leave that daemon holding what it read with nothing here able to see
it, so the output says nothing rather than claiming agreement.

`feat daemon restart` was added instead of a settings-specific reload, and is
what the line above names. It is the two existing commands in one, and it is safe
as one because stopping already waits: `Stop` returns when the socket has stopped
answering and the process has exited, so the new daemon never races the old one
for the runtime directory. It starts a daemon when none was running rather than
inheriting `stop`'s refusal, which is what makes it the command to reach for
without checking first, and it says so when the new one fails to start, because
the old one is already gone by then. It is deliberately the daemon's own verb
rather than the settings': a new build or a daemon somebody wants reconciled from
scratch is the same command, and its cost — an interrupted check run returned to
its review request, an unsent idle notification — is the same whatever the reason.

An on-demand `feat settings reload` was proposed and is deliberately not built.
The measured evidence is that these values are never edited — evidence 1 above —
so it would be machinery for a case that does not arise; it would need the held
settings made race-safe and a new local-API endpoint, which is most of what
reading per use gave away for free, so it re-litigates the decision above rather
than extending it; and by the criterion this project uses to size features, it is
a convenience over two commands in a shell the user is already in rather than
something that stops information being carried by hand. What made the restart
requirement feel expensive was not knowing whether one was needed and having to
type two commands when it was; the line above answers the first and
`feat daemon restart` answers the second, without a held value having to become
race-safe. Reconsider a reload on evidence that somebody edits these often enough
for a restart's cost to matter.

`feat settings init` writes the file and `feat settings edit` opens it. What
`init` writes has every value shown, commented out and explained, and only the
`version` live — so running it changes nothing, which is the same rule ADR-062
gave the wizard and it matters more here rather than less, because this whole
file is defaults. It never overwrites and takes no force flag, for the reason the
wizard does not: this is a file the user authored. `edit` writes that default
first when there is none, so what opens is the file with every value in it rather
than an empty buffer; it opens a file that does not parse, because that is the
file somebody runs it to fix, falling back to `$EDITOR` rather than to a command
it could not read; and it reads the result back, because an editor that exited
cleanly says nothing about what is now in the file. `docs/examples/settings.yaml`
is the same text as the template, held there by a test.

One defect fell out of it and is fixed in the same change. `$EDITOR` was split on
whitespace by the client and taken whole by `internal/config`, so the same
variable named a program in one place and a command in the other: `code -w`
worked for a publication draft and would have been looked up as an executable
called `code -w` here. It is now split in both, which is what every tool that
reads this variable does, and the cost — an editor whose path contains a space —
is the one they all pay.

Consequence: this amends the configuration model, which had one file and now has
two — see [07-configuration-model.md](07-configuration-model.md) § Global
settings. `internal/config` gains `Settings` beside `Config`, sharing the section
types and the review resolution and validation rather than copying them;
`schema/feat-settings.schema.json` is published and held to the Go type by the
same drift test that holds the project schema; and `feat --help` gains one
command group of four.
