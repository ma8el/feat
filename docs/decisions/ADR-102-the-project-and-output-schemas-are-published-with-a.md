# ADR-102 — The project and output schemas are published with a compatibility promise, and shell completion is supported

Status: accepted
Recorded: 2026-09-26, with the implementation

ADR-099 deferred to the public preview what a caller may rely on in
`schema/feat-output.schema.json`, and left shell completion hidden until the
same milestone. The roadmap puts the configuration schema there too. This
records all three.

Evidence:

1. Two tests already hold the project and output schemas to the Go types in
   both directions (ADR-028, ADR-099). Neither says what may change between
   releases, so a caller who validated against one release had no statement
   about the next.
2. The output schema was neither embedded nor printed. A user with a release
   binary has none of the repository (ADR-093, evidence 6), so the scripts the
   schema was written for could not reach it.
3. The project schema's `$id` was `https://github.com/ma8el/feat/schema/…`,
   which GitHub does not serve. The output schema's `$id` was the raw URL on
   `main`, which it does.
4. Cobra generated a completion command and Feat hid it. With it enabled and
   nothing else changed, every positional argument completed to file names, and
   no Feat command takes a file as a positional argument.
5. Project identifiers are file names in the configuration directory, and
   `config.List` reads them without a daemon. Task identifiers are the daemon's
   state (ADR-008), and completing them would need a running daemon.

Decisions:

- **The promise covers `feat-project` and `feat-output`.** The settings and
  tickets schemas stay unpublished.

- **Each schema lives at `schema/<name>.schema.json` and never moves.** Its
  `$id` is `https://raw.githubusercontent.com/ma8el/feat/main/schema/<name>.schema.json`.
  The same path under a tag, such as `…/feat/v0.2.0/schema/…`, is that
  release's copy and does not change. The binary prints the copy it was built
  with: `feat project schema` and `feat output schema`.

- **A field may be added in any release.** In the project schema an added field
  is optional, so a file written for an earlier release stays valid. A file that
  uses the new field needs a binary that knows it, because Feat rejects unknown
  fields. In the output schema an added field may be required, and an enumerated
  value may be added. A caller must ignore fields and tolerate values it does
  not know.

- **Removing, renaming, or retyping a field is a breaking change.** So is
  removing an enumerated value, renaming a `$defs` entry, making an existing
  configuration field required, or making an output field optional. A breaking
  change is never made in a patch release. While Feat is `0.x` it may be made in
  a minor release, and that release's CHANGELOG section names it.

- **The promise covers names, types, required fields, enumerated and constant
  values, and `$defs` names.** Descriptions, patterns, lengths, formats, and
  defaults are not covered. `feat doctor` is the validator of record for a
  configuration, and those constraints follow its rules as they change.

- **A validation error's wording is not promised.** Feat's own messages and any
  schema validator's may change in any release. A script reads the exit code
  (ADR-027), and the message goes to standard error (ADR-099). A document that
  passes the schema can still fail `feat doctor`, whose semantic rules the schema
  cannot express.

- **A test enforces the promise.** `testdata/published/<name>.txt` records one
  line per covered fact. `TestThePublishedSchemasKeepTheirPromise` fails when a
  recorded line is missing, when a line is not recorded, when a new required
  configuration field sits in an object that already existed, and when `$id`
  moves. Deleting a recorded line is how a breaking change is made on purpose.

- **The output schema is printed by `feat output schema`.** It repeats the
  noun-then-`schema` form of `feat project schema`, so both published schemas
  are found the same way. A `feat schema` group would invite a second path to
  the project schema, which is published surface and does not move.

- **Shell completion is supported for bash, zsh, and fish.** PowerShell's
  generator is removed, because Feat targets macOS and Linux. A project argument,
  and `feat implement --project`, completes from the configuration directory
  with no daemon running. A task argument completes to nothing rather than to
  file names. Completing task identifiers needs the daemon and is not built.

Consequence: the output schema is embedded beside the project schema in the
root `feat` package, and the existing embed test covers it. The project schema's
`$id` changes to the raw URL. `feat completion` and `feat output schema` join the
command surface. Bash completion was verified against a built binary on Linux;
zsh and fish use Cobra's generated scripts unchanged and were not run when this
was recorded.
