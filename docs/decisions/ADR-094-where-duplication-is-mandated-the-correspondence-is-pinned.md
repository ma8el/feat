# ADR-094 — Where duplication is mandated, the correspondence is pinned by a test rather than removed

Status: accepted
Recorded: 2026-09-04, with the implementation

Evidence, from a sweep of the six seams in this repository where two parts were
suspected of saying the same thing twice:

1. Two of the six arrived at the same follow-up independently, and neither named
   it. Seam 3b — `domain.Project.Validate` and
   `config.validateProject`/`validateRepositories`, which state the same five
   project-shape rules — proposed a guard test that the two agree. Seam 6 —
   `internal/execution/compose` and `internal/runtime/compose` — proposed a
   fixture-driven test running both `parseContainers` implementations over the
   same recorded Compose output. They are one move made twice.
2. ADR-034's own arithmetic has moved. It says that "roughly a hundred and fifty
   lines is the price of a boundary that three documents state". The twin
   inventory today is roughly three hundred lines a side: `version.go` (63 each,
   with a comment-only diff), the `HostRunner` skeleton, `parseContainers` and
   its `container` struct, `firstLine`/`lastLine`, and the volume enumeration.
   Within `internal/execution/compose` the volume helpers are properly shared
   between its own two callers, so the copy exists only across the mandated
   boundary rather than wherever a second caller appeared.
3. The cost ADR-034 knowingly accepted has been paid once. ADR-033 evidence 15
   records the `depends_on`-closure reset fixed in the runtime adapter and not in
   the execution one, and says of itself that it is "ADR-034 evidence 12 exactly"
   — one defect crossing the mandated boundary twice, which is the defect class a
   shared package would have fixed once.
4. This repository already makes the move four times without having named it.
   The command surface is held by a golden test (`internal/cli/surface_test.go`)
   and the stored format by another (`internal/store/fs/format_test.go`), so a
   change to either fails in a test rather than in a user's state directory; the
   published schema is held to the configuration structs in both directions
   (`internal/config/schema_test.go`, ADR-028); and the decision log's numbering,
   its index, and the relations between decisions became guards in
   `internal/guard` rather than conventions anyone remembers (ADR-089).
5. Nothing in the sweep argued for dissolving a boundary. Every seam it examined
   was mandated by a `depguard` rule under ADR-025's regime, or parallel by
   design because the two sides answer different questions with different
   consequences. The four remaining findings were small, and each already carried
   the condition under which acting on it would be worth the change.

Decisions:

- Where a rule requires two things to stay separate, the duplication between them
  is pinned by a test rather than removed. Merging is what the rule forbids;
  drifting apart is what the separation costs; and a test asserting that the two
  still agree is the only move left. It is also the cheap one: it adds no
  dependency, moves no code across the boundary, and fails at the moment one copy
  changes rather than at the moment the divergence is noticed.
- The boundary ADR-034 drew stands, and this decision is not an argument against
  it. `runtime-stays-an-adapter` and `execution-stays-an-adapter` deny each
  adapter the other in the same sentence, ADR-034 considered a shared package and
  rejected it, and the sweep's own analysis supports keeping the separation: the
  agent's environment and the application runtime are separate concepts even
  where both drive Compose, and a shared type is exactly what the domain model,
  the security model, and CLAUDE.md keep apart. What evidence 2 and 3 change is
  the price and the demonstrated failure mode, which is the kind of evidence
  CLAUDE.md says to record rather than absorb silently.
- Such a test asserts the correspondence and not the wording. Two validators
  written for different audiences are supposed to say different things; holding
  their messages identical would be the merge this decision refuses, arriving
  through the test instead of through the code.
- The first instance is implemented with this decision: a guard test that
  `domain.Project.Validate` and `config.validateProject`/`validateRepositories`
  agree on the five rules they both state — a name, at least one repository, no
  repository named twice, a primary repository that is one of them, and a primary
  a task can edit (FR-PROJ-003). The drift it guards against is a real defect
  rather than untidiness: FR-PROJ-003 changing in one place and not the other
  lets a project register that its own snapshot then refuses, or refuses one the
  snapshot would have accepted. The two are not merged, because the configuration
  collects every problem in file vocabulary for ADR-028's honesty while the domain
  returns the first violation as a storage-integrity gate against a hand-edited
  snapshot, and the domain has to stay importable by both.
- The next candidate is seam 6's, and it is not implemented here. A fixture-driven
  test over both `parseContainers` implementations is the same idea against a much
  bigger surface: it needs recorded Compose output, which is a fixture nothing else
  in the tree keeps, so its upkeep is real rather than near-zero. It becomes worth
  paying at the second crossing — a defect fixed in one adapter's `ps` decoding
  and not the other's, evidence 3 being the first — or at the first change to
  either implementation that is not a field the two genuinely differ on.
- The sweep's remaining findings are left alone deliberately, and the triggers are
  recorded here because a decision not to act is a decision:
  - Worktree presence is classified twice, by `internal/daemon/reconcile.go` and
    `internal/git/cleanup.go`, in the same `os.Lstat` three-way switch and in the
    same words. If either is touched again, give `internal/git` a cheap
    presence-only query and have both callers use it. The stakes are low: a drift
    misreports a finding and removes nothing wrongly, because the plan's own copy
    is the one cleanup re-resolves and checks.
  - Inside `internal/daemon/cleanup.go`, the volume-target construction loop
    appears three times, identical but for one string, and class validity is
    checked twice on one request path — once in `resolveSelection` and once in
    `reconcile.Plan.Check`. Unlike the two-gate path and root-user checks, which
    the code states are deliberate, nothing says this pair is. Fold either only if
    the file is edited for another reason.
  - `internal/config/describe.go` and `internal/project/checks.go` word the same
    three facts twice: what `agent.capabilities.docker` means per execution mode,
    that `FEAT_HOST_AGENT` overrides the devcontainer mode, and that environment
    files are passed by path and never read. Two commits have already edited both
    files for one fact. A third hoists the mode-gloss sentences into
    `internal/config` as exported notes that `checks.go` composes into findings;
    what that costs is per-context wording, which is why it has not happened yet.
  - `parse` in the GitHub and GitLab forge adapters is token-identical modulo the
    URL pattern and the reference sigil, about 25 lines twice. The `Open` skeleton
    around it is parallel by design — the shape repeats because the CLI domain
    repeats, and merging it would put per-CLI behaviour behind conditionals in
    shared code, which is what ADR-070's adapter split exists to avoid. Hoist
    `parse` when a third forge exists and three copies make the helper's shape
    obvious.
- Seam 5 is recorded as verified rather than assumed. ADR-063's one flow and
  ADR-084's one widget hold: `internal/ui/wizard.go` and `internal/cli/init.go`
  drive the same four methods and own only presentation, the question widget is
  drawn by both, and `wizard-asks-through-its-host` denies the flow both askers.
  A question added to the flow still reaches both or neither.

Consequence: one guard test beside the decision-log guards, and no change to any
code the sweep discussed. The test builds a project configuration and the domain
record it registers as, through `project.FromConfig` rather than a mapping of its
own, and asserts the two paths agree on accept or reject. Three of the five rules
are reachable that way; the other two correspond asymmetrically and say so — a
configuration cannot present an empty name, because resolution fills it from the
identifier, and it cannot name one repository twice, because repositories are a
strictly decoded mapping — so for those the test pins what each side actually
guarantees. ADR-034 gains a back-reference and nothing else: its decision is
unchanged, and what is new beside it is the price and the receipt.
