# ADR-093 — A skill is installed into the user's own session, and it carries method rather than schema

Status: accepted
Recorded: 2026-09-01, with the implementation

The maintainer: a skill that helps the user with setting up a project and
overall with the handling of this app — and, on the second pass, that the user
might need help writing the project YAML in particular.

The design was first recorded on 2026-08-23, on a branch that was pruned before
it landed. It is re-recorded here with the implementation, unchanged in
substance, together with the answer to the one question that first recording
left open — what a reinstall does about a skill the user has since edited.
ADR-090 sets the schedule: the skill and its two emitters are v0.1, because the
release is what creates the user they are for.

Evidence:

1. ADR-062 closed the create path and said what it left open in its own last
   sentence: the wizard writes a project once, and the file is the user's from
   then on. Adding a repository, adding a check, moving to a different
   devcontainer service, and every other edit a project accumulates over its
   life have exactly the help the create path had before the wizard — an
   example and `feat doctor`. Evidence 1 of ADR-062 applies again to the half it
   did not cover.
2. The wizard is a conversation and refuses where there is nobody to converse
   with. Outside a terminal `feat project init` names the example to copy, which
   is the right refusal and leaves the scripted and remote cases authoring 258
   lines by hand.
3. The failure that actually breaks a configured project is invisible to
   diagnostics at configuration time. `feat doctor` says so itself: the checks
   inside the agent's execution environment are asked where that environment is,
   and are skipped until a task is running. ADR-055 records what that costs — a
   check configured as `pytest` against a project that runs its tests through a
   wrapper, with the bare program not on the path inside the agent's
   environment. The agent cannot fix it, because an agent that edits the
   configuration governing its own gate certifies itself.
4. The devcontainer guard refuses at task launch rather than at configuration
   time. A Compose service that reaches a container runtime's socket, has
   `docker` installed, sets `DOCKER_HOST`, or mounts the user's home directory
   is a configuration mistake reported when the first task fails to start,
   which is the last moment it is useful (ADR-066, ADR-067).
5. What answers all four is a session that can run commands on the machine and
   read what they say. The user already has one open; it is the one thing on the
   machine Feat has no way to reach, and it is where the questions are asked.
6. The two files that are held to the implementation are not on the machine that
   needs them. `schema/feat-project.schema.json` and
   `docs/examples/project.yaml` live in this repository; a user who installed a
   release binary, a Homebrew cask, or `go install` has the binary and none of
   the repository, and nothing embeds either file or prints it. A rule that
   sends a reader to those paths is a rule about a checkout, not about an
   installation.

Decisions:

- The skill is installed into the user's environment, not checked into this
  repository. A user runs `feat` from their own project, so a skill that lives
  beside Feat's source reaches people working on Feat and nobody else.
- Feat carries the skill and writes it. The document, the schema, and the
  example are embedded in the binary, and `feat skill install` writes the skill
  out; `feat project schema` and `feat project example` print the other two,
  beside `feat project show`, which already prints a resolved configuration. One
  build ships all three, so the skill on disk is the one that matches the binary
  that wrote it, and the files evidence 6 says are missing arrive with the
  binary that needs them.
- The skill is never the authority on the configuration, and it does not read a
  path to avoid becoming one. It asks the binary — `feat project example` for a
  file to start from, `feat project schema` for what a field may hold — and both
  answers come from artefacts held to the Go types by tests in both directions
  (ADR-028). A field added to the configuration changes the schema and the
  example in the same commit, the emitted answer changes with them, and the
  skill's procedure is unchanged, because the procedure is the thing the skill
  carries.
- Having drafted, it runs Feat's own validation and fixes what that reports:
  `feat project init --dry-run` where the wizard applies, and `feat doctor` for
  the semantic rules the schema says it cannot express. The skill's output is
  therefore checked by the implementation before the user sees it, which is the
  rule ADR-062 already applied to the wizard's own rendering.
- Interactive creation stays the wizard's, and is run rather than reimplemented.
  ADR-063 made the questions a package because two askers drift; a third asker
  would drift the same way. Where a terminal exists, the skill's answer to "help
  me configure this" is `feat project init`.
- What the skill adds is the half with no command: editing a configuration that
  already exists, authoring one where the wizard refuses, and judging an answer
  the wizard can only collect — whether these repositories are one project,
  whether this check command exists where the agent will run it, whether this
  Compose service is a container the guard will accept.
- It establishes those answers by running things and reading them, before the
  first task rather than after it, rather than by telling the user what to type.
- The skill launches nothing. `feat implement` needs a terminal, because a task
  is not created until the user confirms it, and the session the skill runs in
  has none — so the skill's last step is a handoff, naming the command for the
  user to run in their own terminal, not an attempt that fails on the final step
  of the setup it exists to help with.
- Nothing in it addresses the agent Feat launches. The generated protocol is
  short and almost entirely about the protocol, and the project's own CLAUDE.md
  tells that agent how to work; this is a second audience, not a second voice in
  the first one's ear.
- A reinstall replaces what Feat wrote and refuses what it did not, and a
  recorded marker is what tells the two apart. The canonical skill is embedded,
  but Claude Code discovers a skill as a file on disk, so `feat skill install`
  creates a second copy that is an ordinary file from then on. When the next
  install finds that file differing from what this binary would write, the two
  causes — the user edited it, or an older binary wrote it — are
  indistinguishable without a record, and the two uniform answers each get one
  of them wrong: overwrite-always loses the user's edits silently, and
  refuse-always strands every user on the skill their first binary shipped. So
  the install writes a marker beside the skill — the version that wrote the
  file and a checksum of what was written, the record before the file, which is
  the order everything else follows — and the next install compares: a file
  still matching what Feat installed is replaced without ceremony, because
  replacing it loses nothing anyone authored, and one that does not match is
  refused with the reason and an explicit `--force`. This closes what the first
  recording left open, with the answer its own framing leaned toward:
  `feat project init`'s exclusive create is the rule for a file the user
  authored, and this is the weaker case because Feat authored it — Feat may
  replace its own words and may not replace the user's.
- A plugin marketplace is not how it ships, though it may later be how it is
  found. A marketplace refreshes on its own schedule and a user upgrades their
  binary on theirs, and two clocks are how the skill and the build come to
  disagree — the same drift this decision refuses, moved from the skill against
  the schema to the skill against the binary. Discovery is a separate problem
  with a separate answer, and a catalogue entry pointing at `feat skill install`
  would solve it without shipping anything.

What this does not do: it does not restate the command surface. Every command
explains itself, `feat project show` prints the resolved configuration, and a
skill that lists flags is the pair that agrees on the day it is written and
drifts afterwards — the thing ADR-063 exists to refuse. The limit is honest
rather than incidental: a skill is not held to the implementation by any test,
so nothing belongs in it that a test could pin instead, and everything that does
belong in it is a method for reaching Feat's own answer rather than a copy of
one.

Consequence: a skill document, the first embedded assets in the binary, and
three commands — `feat skill install`, `feat project schema`, and
`feat project example`. The skill's content, the skills directory it installs
into, and the marker beside it are Claude's and live behind the provider
adapter; the one command file that wires the install reaches that adapter
directly, recorded as an exception in the lint configuration, because
installing is a host-side act of a client — the machine that needs the skill
most has no daemon yet — and every other client rule still applies to it. No
configuration field is added and no existing command changes. Because the
install is a write into the user's own environment, both of its halves are
readable before anything is on disk: `feat skill show` prints the document an
install writes, byte for byte — which is also what a refused install is
diffed against — and `feat skill install --dry-run` reports the resolved
destination and the verdict by running the same decision the install runs, so
the dry run and the real one cannot drift. The
two emitters are worth having on their own: a user who wants to write the file
by hand, which ADR-062 keeps supported, otherwise has nothing to write it
against unless they have a checkout. ADR-090 already places all three in
v0.1.0, because the release is what creates the user with the binary and none
of the repository.
