# ADR-029 — Git adapter boundary, preparation order, and path safety

Status: accepted
Recorded: 2026-08-05

Evidence found while implementing the Git and worktree lifecycle:

1. The slice 4 acceptance criterion that a failure halfway through creation leaves a recoverable record needs a writer, and the daemon is the only one (ADR-008). The Git adapter cannot write it, and slice 6 owns the draft API that would normally call both.
2. Generating a branch name needs the placeholder vocabulary that `internal/config` validates. Putting expansion in the adapter would duplicate the vocabulary; putting the adapter behind the configuration types would make the Git CLI adapter depend on the shape of a YAML file.
3. `git.worktree_root` names the directory holding a task's worktrees, and its documented placeholders include `{repository_id}`. A template that uses it expands to one directory per repository; a template that does not expands to one directory for all of them, and the second worktree would then fail on the first one's files.
4. A record of what a task owns can be edited, restored from a backup, or written by an older version. Cleanup reads it to decide what to remove.
5. FR-GIT-001 requires a fetch "when network access is available", which does not say what to do when it is not. Failing task creation because a laptop is offline would make the last fetched state unusable; using it silently would let a user believe their base is current.
6. `TaskRepository` has no field saying whether its worktree exists. Adding one would be a change to a stored format that slice 1 pinned with golden files and a migration policy.
7. Repository names, remotes, and branches come from configuration and reach an argument vector. Nothing is handed to a shell, but Git still reads an argument beginning with `-` as an option, and `--upload-pack=...` in place of a remote name is a command of somebody else's choosing.

Decisions:

- `internal/git` is the adapter and imports only the standard library, `internal/domain`, and `internal/paths`. A `git-stays-an-adapter` `depguard` rule denies it `internal/config`, `internal/store`, and every other package; ADR-025 requires an ADR for a boundary rule, and this is that record.
- `internal/config` gains `Expand`, `Values`, `Uses`, `Slug`, and `StaticPrefix`, so the closed placeholder vocabulary has one owner. The daemon expands templates and hands the adapter final names.
- Task preparation is plan, record, apply, in that order, and the daemon owns it: `service.PrepareTask` plans, writes the plan onto the task, leaves draft, and then creates one repository at a time, recording each before the next begins. Slice 4 adds no endpoint and no command; slice 6 confirms a draft by calling this. The ordering is the criterion: every path and branch that could exist afterwards is written down first, so no resource can exist that the record cannot name, and an interruption at any point is recoverable rather than mysterious.
- A failure part way through is left in place and the task becomes `failed`. Nothing is rolled back, because a worktree that was created may already have been mounted, entered, or written to, and tidying up a failed launch is a destructive act the user did not ask for. `failed` has recovery edges to `preparing` and `working`.
- Whether a worktree exists is observed rather than stored. The planned branch and path are desired state; `GitObservation` is what Feat saw, and a repository with no observation is one nothing has confirmed. No field is added to the stored format, and reconciliation asks Git rather than trusting a stored flag, which is what CLAUDE.md means by never assuming persisted desired state equals observed state.
- A worktree root that does not name the repository gets the repository identifier appended; one that does is used as it expands. Both readings then produce one worktree per repository.
- Every worktree path must be absolute, written cleanly, strictly inside the fixed leading directory of the configured root, outside every repository checkout, and not a shared system directory — checked after symbolic links are resolved as far as the path exists, so a link cannot move a task's directory somewhere Feat would never have accepted. Cleanup applies the same check to a recorded path and refuses the target rather than removing it: the moment a path from a record decides what gets deleted, the record has stopped being a record and become an instruction. The shared-directory list moves from `internal/config` to `internal/paths`, so the package that validates a configured root and the package that removes directories under it use one list.
- A fetch is best effort: a failure becomes a note on the plan, the base resolves from the last fetched state, and the user is told the base may be stale. A missing remote-tracking ref is still an error, and it names `git fetch <remote>` as the remedy. The command is plain `git fetch <remote>`, without `--prune`, `--all`, or `--tags`, each of which changes refs Feat was not asked to change.
- A fetch runs only for the `remote` base policy. Fetching cannot change what a local, current, or explicit policy reads, and a network call whose result nothing depends on is one the user did not ask for.
- Collisions — an existing branch, an occupied path, a path Git has already registered — are reported at plan time and never resolved by choosing another name. A branch Feat renamed is a branch the user did not agree to and will look for under the name they saw.
- Remotes, branches, and refs are rejected when they begin with `-` or contain a control character, rather than relying on every Git subcommand to honour `--`.
- A repository the project configures as `read_only` cannot be promoted to read-write by a task. Taking less access than the default is always allowed; taking more is not.
- Cleanup plans are produced and never executed in slice 4. There is no execution path and no plan token, which slice 12 owns.

Consequence: slice 4 has no user-visible surface. The command surface, the API, and the golden files are unchanged, and `PrepareTask` has no caller in production until slice 6 confirms a draft with it. That is the opposite of the reasoning ADR-027 and ADR-028 used to defer `daemon.json` and the doctor endpoint, and the difference is that those are published compatibility surfaces with no reader, while this is orchestration whose absence would leave a slice 4 acceptance criterion verifiable only against a fake.

Slice 2's structurally verified event-ordering criterion becomes partly behavioural: preparation is the first production code that appends to a task's event log and publishes to the stream.

The four acceptance criteria that are really about Git's behaviour are verified against real repositories in opt-in tests named `TestReal…`, which CI runs on macOS and Linux; the unit tests use a fake runner and pin the argument vectors.
