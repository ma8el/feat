# ADR-062 — A project is configured by answering questions, and the answers are checked before there is a file

Status: accepted
Recorded: 2026-08-15, from the cost of adding a project by hand

The maintainer, adding a second project: adding a project to Feat is quite
involved as it stands — the YAML file has to be created by hand, copied from the
template.

Evidence:

1. The first thing a new user does is the thing with no help in it. Every other
   command explains itself and validates its input; the file every one of them
   reads is written in an editor, against a 176-line example, with `feat doctor`
   as the only feedback loop.
2. Most of what the file asks for is already true of the machine. The
   working-tree root, the remote, the default branch, the Compose files beside a
   checkout, and the services those files declare are all facts a tool can read,
   and every one of them was being retyped — which is also how a configuration
   acquires a value that was never true, such as `main` in a repository whose
   branch is `trunk`.
3. The parts that are decisions are few: which repositories take part, how each
   takes part by default, where the agent runs, which provider CLI it expects,
   whether a task runs application services, and what verifies the work. Six
   questions, against a file with sixty fields in it.
4. [02-user-workflows.md](02-user-workflows.md) had already put a wizard in
   public v0, and the plan of the day had made it conditional on manual
   configuration being "the dominant public blocker". Dogfooding answered that
   question ahead of schedule: it is the step that is hardest and the only one
   Feat does not help with.

Decisions:

- The wizard is a conversation on the command line, not a screen. `feat project
  init` runs before there is a daemon, before there is a project, and possibly
  before Feat has ever run on the machine, and the dashboard is a client of a
  daemon. A line-oriented conversation is also what a user can read back in
  their scrollback, which is what somebody debugging their own configuration
  does next.
- The answers are collected into `config.Draft`, and the file is rendered from
  it once. Nothing is written down as it is answered, so an interrupted run
  leaves nothing behind, and the text the user is shown is rendered from the
  same value the file is written from.
- The rendering is parsed, resolved, and validated before it is displayed. What
  is offered is therefore a configuration Feat accepts, not a proposal that might
  be; a rule the questions failed to cover fails while the answers still exist,
  naming its field, rather than after the file is on disk.
- What Feat can find out, it finds out; what it assumes, it says. A path is
  inspected rather than trusted, and a repository with no remote is reported as
  having none and gets the local base policy — which is the one value the wizard
  decides on the user's behalf, decided from what it found rather than from a
  preference.
- The file states decisions and omits defaults. A default written into a
  generated file is a value that stops following Feat when Feat's own changes,
  and `feat project show` already prints the resolved configuration. The
  capability block is the deliberate exception: what the agent may reach is
  written down, with the sentence saying why it cannot vary, because that is the
  paragraph somebody deciding to run Feat on their own work will look for.
- Nothing is written until the whole file has been displayed and confirmed, and
  an existing configuration is never overwritten — the create is exclusive, so
  even a file that appeared during the conversation is left alone. There is no
  `--force`: a project's configuration is the one thing on the machine Feat asks
  the user to author, and losing it to a mistyped command is not a trade this
  command makes.
- Diagnosing and registering stay the commands they already are. The wizard
  offers each at the end and runs neither on its own: `feat doctor` is what
  checks the project against the host, `feat project add` is what the daemon
  records, and the wizard calls exactly those rather than growing versions of
  its own.
- Writing a file by hand remains supported and unchanged. Outside a terminal the
  command refuses and names the example to copy, rather than asking questions
  into a pipe.

Consequence: one command, one draft type in `internal/config` whose only
capability is to render itself and be validated, and two host discoveries in
`internal/project` — what a checkout says about itself, and which services a
Compose file declares. The configuration schema does not change, no field is
added, and the file the wizard writes is a file the previous build would have
read. What this does not do is keep an edited file in step with anything: the
wizard writes a project once, and the file is the user's from then on.

Amended by ADR-084: the wizard is still a conversation and its questions are
now drawn. Both reasons this decision gave stand — it runs before there is a
daemon, which is why the widget is in the command rather than the dashboard's
dialog, and its scrollback is what somebody debugging their own configuration
reads back, which is why the widget is inline, prints the answered question as a
permanent line, and never rewrites one. What moves is "not a screen".
