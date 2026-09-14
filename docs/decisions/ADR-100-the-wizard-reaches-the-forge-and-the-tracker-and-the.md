# ADR-100 — The wizard reaches the forge and the tracker, and the application is answered before the agent

Status: accepted
Recorded: 2026-09-14, with the implementation

`feat project init` exists because dogfooding showed manual configuration to be
the hardest step of adopting Feat (ADR-062), and ADR-095 moved its second pass
into `v0.1.1` because every input it was waiting on is now recorded. There are
four, and the fourth reorders the conversation the other three live in, which is
why this is one decision rather than four.

Two are the roadmap's own: the managed-services proposal offers every service a
repository's files declare, and the agent's environment is answered before the
application's. Two came from the `v0.1.0` acceptance run on a second machine,
where the wizard was described as noticeably better than the previous attempt
and then had two things added to its output by hand — a forge, and a tracker
command.

Evidence:

1. **Both hand-edits were of configuration the wizard already had no question
   for.** `repositories.<id>.forge` and `tracker` have been in
   `internal/config` since ADR-071, are validated, are described by
   `feat project show`, are in `schema/feat-project.schema.json`, and are
   documented in `docs/examples/project.yaml`. `internal/wizard` contained
   neither word. So the gap is the wizard reaching existing configuration, and
   not configuration surface to widen.
2. **ADR-071 prescribed an inference nothing implements.** Its decision says the
   forge is "inferred from the remote's host where that host is recognisable and
   declared otherwise; a self-hosted instance is not guessable, so Feat asks
   rather than guesses". Nothing in the tree reads a remote's host. It could not
   have: configuration is loaded without Git, and `Repository.Forge` is a field
   a user writes. The only place an inference can live is where a proposal is
   made and accepted, which is the wizard.
3. **The tracker cannot simply be asked for, because the answer may not exist
   yet.** A tracker is a command whose output conforms to
   `schema/feat-tickets.schema.json`, and writing one is work in itself —
   `docs/examples/tickets` ships four worked ones for exactly that reason. A
   question demanding one would put a thing to go away and build in the middle
   of configuring a project.
4. **A managed service is more than a service that runs.** The generated
   override writes an entry per managed service, and Feat creates, starts,
   stops, and destroys them for one task. A database declares no mount of the
   repository and no build context inside it, so it runs none of the project's
   code and has no worktree to be given — and a user accepting the whole list
   was managing it anyway.
5. **Nothing is lost by leaving it out.** Feat addresses the managed services by
   name: `up --detach <services>` and `up --no-start --build <services>`
   (`internal/runtime/compose/compose.go:152,160`), and Compose brings up the
   `depends_on` closure with them — which ADR-034 evidence 13 already records
   and `docs/examples/project.yaml:61` already tells a hand-editing user
   ("Feat starts, stops, and destroys them for one task, and starts whatever
   they depend on").
6. **The agent's Compose question had nothing to go on, and said so.** It
   proposed nothing, because the files beside a repository are overwhelmingly
   its application's and offering one of them for the devcontainer is the
   failure ADR-077 records: a user ended up with an application's Compose files
   defining the container their agent runs in. Proposing nothing is the honest
   answer to a question asked too early, and it is the whole of what asking it
   later changes.
7. **The order was an accident of how the sections were added, not a
   requirement.** No question in the agent section reads anything the
   application section answers, and the application section reads nothing of the
   agent's: `runtime.mount` is asked in every execution mode and was already
   asked before the mode was known to matter (ADR-065 evidence 6). What the new
   order buys is the reverse dependency — the agent's files are what is left
   over once the application has claimed its own.
8. **ADR-063's split held, and was checked rather than assumed.** Both askers
   are renderers over one `wizard.Question`: `internal/ui/wizard.go` draws
   `ask.Model.Context()` and `ask.Model.View()`, and `internal/cli`'s
   conversation prints the heading, detail, and notes itself so they survive in
   the scrollback and then hands the question to the same widget (ADR-084).
   Neither branches on a question's identifier anywhere; the only
   `wizard.Question` an asker authors is the three yes-or-no offers the
   conversation puts after the file is written. The one asymmetry is the one
   ADR-084 recorded and accepted: the line fallback draws neither `Optional`'s
   sentence nor the candidates, because it has no Tab.

Decisions:

- **Every repository a task may write to is asked where it publishes.** A closed
  question between a repository's access and the offer of another, carrying the
  forges configuration accepts and then `none`. Four of the five access modes
  reach it: `omitted`, `selectable`, and `stable_read_only` all permit read-write
  once the repository is explicitly selected, so a forge may yet be used.
  `read_only` is the one that cannot — a repository a project declared read-only
  must not become writable because one task asked — and publication refuses a
  binding that is not read-write in every place it looks at one, so an answer
  there is configuration no task can reach. It would not be inert either, which
  is what makes it worth refusing rather than tolerating: `feat doctor` collects
  the forges the repositories declare without looking at access, so accepting the
  proposal on a read-only repository whose remote is on `github.com` buys a
  standing warning demanding a command line for a repository that can never use
  it. `DefaultAccess.Permits` decides this rather than a mode named in the
  wizard, so a sixth mode would be followed rather than missed. One gap is left
  rather than closed: a project whose repositories are all read-only is asked
  which one a task may edit, and the one promoted there was never asked where it
  publishes. That path exists only because a project with no editable workspace
  has to be given one, and what it produces is a configuration Feat accepts with
  an optional section missing, which is added by editing the file.
- **The remote proposes the answer, and says so.** A remote on `github.com` or
  `gitlab.com` proposes that forge and names the remote it was read from; any
  other host proposes `none` and says that the remote was read and not
  recognised, because a default a user cannot tell from a finding is a value that
  appeared out of nowhere. Exactly those two hosts and no subdomain of either:
  GitHub Enterprise and a self-hosted GitLab are both unguessable, which is
  evidence 2's own point. `none` is the absence of the section rather than a
  value in it — configuration has no forge kind meaning "nowhere".
- **The list of forges is the domain's, read by both the question and the
  rejection.** `domain.ForgeKinds` is new, `ForgeKind.Valid` answers from it, and
  `config.forgeKinds` and the wizard's options both read it. A question offering
  a kind the configuration refuses would compose a file Feat will not load, and
  one missing a kind it accepts would be a field the wizard cannot reach. This is
  ADR-094's problem with ADR-094's cheaper answer available: no rule mandates the
  separation here, so the duplication is removed rather than pinned.
- **The tracker is one optional question, asked last.** Evidence 3 rules out
  demanding one and evidence 1 rules out leaving it to the skill, because a
  project needing a tracker has to be configurable by the wizard alone. What is
  left is asking optionally, and asking last: an empty answer writes no section,
  and the question says so and says that the section can be added to the file
  afterwards — which is the "editing a configuration that already exists" case
  ADR-093 gives the skill. The kind is neither asked nor written, because
  resolution fills it in and a generated file states decisions rather than
  defaults.
- **The answer is split into an argument vector by the flow, honouring quotes.**
  A real tracker command carries an argument with a space in it, and splitting
  one into pieces composes a configuration that is wrong in a way nothing
  downstream can explain: `feat doctor` would run it and report that the output
  is not the published shape, which says nothing about the typing. Single quotes
  are literal, double quotes take a backslash escape, and a backslash outside
  either escapes the next character — a shell's reading of the same line, minus
  every expansion, because Feat runs the vector directly and expands nothing. An
  unterminated quote and an unnamed program are refused at the question, where
  the answer can still be given again.
- **The managed-services question proposes the services that run this
  repository's code.** The ones whose files mount the repository, and the ones
  built from it; `wizard.Composition` gains `Mounted` beside the `Baked` it
  already had. The rest are named in a note saying that they were not proposed,
  why, that Feat starts whatever the managed services depend on, and that naming
  one here manages it. It is still a free-text answer and the question is still
  mandatory: this narrows a proposal and forbids nothing.
- **The application is answered before the agent's environment, and the agent's
  Compose question offers what is left.** The files found beside the project's
  repositories, minus the ones a repository's runtime contribution claimed, minus
  the ones already given in this loop — the shape the application's own loop
  already has. The one in a `.devcontainer` directory heads the list, because
  that is where the Dev Containers specification puts a project's container
  definition and where a file for this question is ordinarily kept; a file the
  application claimed is named in a note, because a file Feat withheld and a file
  Feat never found are otherwise the same absence. The repeat still proposes
  nothing, so Enter still means "no more" (ADR-077).
- **The tracker is a section of its own, and the sections are
  `project › repositories › services › agent › tracker`.** The forge belongs to a
  repository and is asked with one; the tracker belongs to the project and is
  asked once. ADR-071 separated them because a forge hosts code and a tracker
  holds tickets and neither implies the other, and that is the same reason they
  are in different parts of this conversation.
- **No new question is mandatory, and none of them is free.** Each of the two
  additions always carries a proposal, so Enter answers it wherever it is asked,
  and neither can stop a project being configured. What they cost a project that
  wants neither is one keystroke per repository and one at the end. That is
  stated rather than hidden: the alternative considered was a project-level gate
  in the shape of `runtime.wanted`, and it was declined because it costs the user
  who does publish an extra question to reach the ones they need, and costs the
  user who does not exactly what it saves them on a single-repository project,
  which is the ordinary case.
- **Extends ADR-063's rule to the questions this adds.** Both of them are an
  ordinary closed question and an ordinary optional text question, so both reach
  the dialog, the inline widget, and the line fallback without any asker knowing
  they exist. Evidence 8 is the check that rule was still being kept, and it was.
- **It amends ADR-062's account of what the wizard asks, and nothing else about
  it.** The command is still a conversation, still writes nothing until it is
  confirmed, still never replaces a configuration, and still asks nothing about
  verification (ADR-078). What changes is which questions it holds and the order
  they are asked in.

Consequence: `internal/wizard` gains two stages, the narrowed proposal, the
agent's candidate list, and the helpers behind them — the remote's host, the
forge options, and the command splitter; `wizard.Checkout` gains `RemoteURL` and
`wizard.Composition` gains `Mounted`, both supplied by `internal/cli`'s host over
`internal/project`, which reads the remote's URL with `git remote get-url` in
`Inspect`. `config.Draft` gains a forge per repository and a tracker command, and
renders both in the form `docs/examples/project.yaml` documents. `internal/domain`
gains `ForgeKinds`. No configuration field, no schema change, no command surface
change, and no daemon change: every value this writes is one the configuration
already modelled and `feat doctor` already checks — the forge's command line on
the host, and the tracker's output against `schema/feat-tickets.schema.json`.
[02-user-workflows.md](../02-user-workflows.md) §1 gains the two questions and the
new order, and loses a clause naming a provider-CLI question ADR-075 removed.

What this does not do: it does not validate in the wizard what `feat doctor`
validates on the host, it does not infer a forge from anything but an exact host
match, it does not run the tracker command, and it does not touch the dashboard's
layout — the question widget is still told how wide it may draw (ADR-088) and the
task panel is still settled (ADR-086).
