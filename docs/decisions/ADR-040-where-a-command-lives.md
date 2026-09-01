# ADR-040 — Where a command lives

Status: accepted
Recorded: 2026-08-08, before implementation

Evidence found by reading the command surface rather than by running it, which is
why it is recorded before the change rather than after a failure:

1. The surface is two designs at once. `project`, `task`, `runtime`, and `daemon`
   are nouns with verbs beneath them; `implement`, `attach`, `review`, `cleanup`,
   and `doctor` are verbs at the top level. ADR-001 already decided for the first
   shape — "use scoped commands such as `feat project add`" — and five commands
   arrived in the second one, each added by the slice that needed it.
2. The seam runs through the `task` noun. Every command that takes a `<task>` is
   an operation on a task: `attach`, `review`, `cleanup`, and all six `runtime`
   actions. `feat task --help` lists one of them, `list`, and it is the least
   interesting one. A user who has learned `feat task list` has no route from
   there to `feat attach` except the documentation.
3. ADR-038 is the field evidence that these are one family rather than five
   separate commands. A single defect landed on `attach`, `review`, every
   `runtime` action, and `cleanup` together, through one helper, because naming a
   task is what they have in common. A defect whose extent is exactly that set is
   a set the surface should name.
4. The present shape can be justified, and the justification does not survive.
   Top level for what opens a screen or hands over the terminal, a noun for what
   prints and exits, fits today's commands. It is already leaky — `review` and
   `cleanup` both print without a terminal (ADR-036, ADR-037) — it sorts commands
   by a property a user cannot see from outside, and adding `feat task show` or
   `feat task stop` would leave the top-level verbs reading as whatever was there
   first.
5. The window is this slice. The surface is pinned by a golden file and described
   in three documents, slice 13 is already rewriting every `<task>` argument for
   ADR-038, and slice 17 publishes v0.2. After that, moving a command breaks a
   shell history that is not Feat's to break.
6. Found while implementing this: cobra's own "Did you mean this?" had never
   fired, for any command. It is built on the path cobra takes for a command
   with no `Args` of its own, and every command in this tree has one, so
   `feat tsk` was answered with "unknown command" and nothing else. A moved name
   made it visible; a typo had always asked the same question.

Decisions:

- A command that takes a task lives under `feat task`: `task attach`,
  `task review`, and `task cleanup`, beside `task list`. That is the rule, and it
  is the one ADR-001 stated.
- `feat implement` stays where it is and is not renamed to `feat task add`. It
  does not take a task, it produces one, so the rule above does not reach it. The
  name is also the activity — it fetches, resolves an immutable base commit per
  repository, proposes branches and worktrees, and launches an agent session —
  while `add` describes appending a row to a list. `feat task`'s own help names
  it as where a task comes from, so the noun a user explores still answers that
  question.
- `feat attach` and `feat review` keep their top-level names as aliases, because
  brevity is earned by how often something is typed and these are typed all day.
  Both are hidden from help so that `feat --help` stays equal to the documented
  surface, which is ADR-027's rule for `feat daemon run`, and both appear in the
  golden file, which walks hidden commands.
- `feat cleanup` gets no alias. It is rare and it is irreversible, and making the
  longer path the only path is the argument ADR-037 made when it refused a
  blanket `--yes`. What it gets instead is a rejection that leads somewhere: the
  old name is answered with the noun that now holds it. There is no compatibility
  shim behind that, because nothing has been released and a shim would put the
  name back on the surface this ADR took it off.
- The suggestion is built where a command's positional arguments are checked,
  which is the one place every rejection passes through, so an unknown word gets
  it whether it is a name that moved or a name that was mistyped. Restoring it
  only for `cleanup` would have left evidence 6 in place for everything else.
- One implementation with two names, never two implementations. The alias is
  built by the same constructor and runs the same `RunE`; cobra sets a parent in
  `AddCommand`, so one value cannot hold both positions, and two bodies would
  drift. An alias says in its own help which command it is.
- `feat runtime` stays a top-level noun rather than becoming
  `feat task runtime`. A feature environment is a co-equal thing a task owns,
  with its own identity and lifecycle (ADR-003, ADR-034), rather than an
  attribute of the task, and three levels before an argument is worse than the
  inconsistency. This is recorded as an exception rather than dressed up as a
  rule, because it is one.
- `feat project`, `feat daemon`, `feat doctor`, and `feat version` do not move. A
  diagnostic named `doctor` at the top level is what the tools this one is
  installed beside already do.
- The asymmetry between `feat runtime destroy --yes` and a `feat task cleanup`
  with no such flag is deliberate and stays. What changes is that each says why in
  its help, because a user who meets the second after the first reads it as an
  omission rather than as a decision.

Consequence: the golden file, [README.md](README.md),
[06-technical-architecture.md](06-technical-architecture.md), and the README
moved in the same change, which is the rule ADR-028 and ADR-031 followed for a
command-surface change. Nothing crosses the socket differently: no endpoint, no
domain type, and no storage path changes, and ADR-038's rule for naming a task is
untouched. Three reconciliation findings that told a user to run `feat cleanup`
now name the command that exists.

Two things this deliberately leaves alone. Machine-readable output is a separate
gap — every command can be read by a person and parsed by nothing — and belongs
with slice 17's JSON Schema. And an unknown subcommand of a group, `feat task lst`
or `feat daemon bogus`, still prints the group's help and exits zero, which
predates this change and is the same on every group: a script cannot tell that
one from a command that ran.
