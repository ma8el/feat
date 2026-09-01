# ADR-081 — The agent's Compose files are read the way a repository's are, and a mount a worktree cannot hold is reported before a task runs

Status: accepted
Recorded: 2026-08-28, from the reference project's setup walkthrough and from reading what the structural reader already threw away

`feat project init` asked where each repository is mounted in the agent's own
container and proposed `/srv/<id>`, which it made up. The reader that answers
that question already existed: `project.ComposeComposition` reads a Compose file
structurally and reports, among other things, where its services agree they mount
a repository. Only the runtime branch of the wizard reached it. The agent branch
called `ComposeServices` — service names and nothing else — and `feat doctor`
only checked that the agent's files exist. **The agent section read its own
Compose files with a weaker reader than the runtime section used on the same kind
of file**, and the same reading turned out to answer a second question nobody was
asking it.

Evidence:

1. **One parameter was doing two jobs, and they coincide only in the runtime
   case.** `ComposeComposition(root, files...)` documented `root` as the
   `project_directory` Compose will be given, and three helpers used it for two
   different things: `absolutePath` wanted the directory relative paths resolve
   against, while `isRepositoryRoot` and `within` wanted the repository being
   asked about. For a repository's application files those are one directory —
   the generated include entry carries that repository's checkout as its project
   directory. For the agent's files they are not: the project directory is the
   first configured file's own directory, and the repository is whichever of the
   project's checkouts is being asked about.
2. **The agent's project directory is not a guess.** `internal/daemon`
   computes `filepath.Dir(cfg.Agent.Execution.ComposeFiles[0])` and passes it to
   Compose as `--project-directory` when it starts a task's agent (ADR-033).
   Reading those files against anything else would answer a different question
   from the one Compose is going to be asked.
3. **`/srv/<id>` is a default rather than a wrong derivation.** Feat generates
   the agent's mount itself, so where a task's worktree lands in the agent's
   container is Feat's to choose when the files say nothing — unlike the runtime
   container path, which has to match where somebody else's services expect the
   code. It is only wrong when the files do say where a repository goes and Feat
   ignores them.
4. **The reader was already walking every bind mount and discarding most of
   them.** It resolved each source and target, kept the ones naming the
   repository itself, and dropped everything else — including every mount of a
   file or directory inside the repository. Those are the mounts a task cannot
   take for granted: a task works in a worktree, and a worktree holds only what
   Git tracks, so a bind of an ignored `.env` or of a `node_modules` built in
   place names something that will not be there.
5. **The explanation for that failure existed and was wired to the wrong
   trigger.** `internal/runtime/compose/explain.go` says it almost exactly —
   "a worktree holds only what Git tracks, so a file that mount expects to find —
   an ignored .env, for instance — is not there" — but only after the container
   runtime has already failed with `is outside of rootfs` or `read-only file
   system`. The shape the reference project uses produces neither: a mount over a
   file that is simply created empty succeeds, and the application then misbehaves
   with nothing anywhere naming the cause. A post-mortem on an error string cannot
   reach it; a pre-flight can.

Decisions:

- **The parameter is split rather than reinterpreted.**
  `ComposeComposition(projectDir, repository string, files ...string)`. The
  runtime call sites pass the checkout for both and behave identically, which is
  why their tests pin the existing behaviour unchanged. This is a refactor, not
  a decision of its own; it is recorded because it is what made the two callers
  expressible at all.
- **The wizard's agent section reads the files, once per configured
  repository.** `stageMount` proposes the container path the files state and
  keeps `/srv/<id>` where they state none. Where a repository got no proposal and
  something in those files was left unread because it interpolates, the question
  says so: a default a user cannot tell from a derivation is a value that
  appeared out of nowhere.
- **`Composition` gains the paths a mount needs**, as `Mounts`, and the bind
  entries left unread as `UnreadMounts`. The second exists so that a report about
  mounts can disclose what it did not read; the same entries stay in `Undecided`,
  which is the wizard's list and is unchanged.
- **`feat doctor` asks Git whether each of those paths is tracked**, for the
  agent's Compose files against every repository a task takes by default, and for
  each repository's own runtime contribution. It asks with
  `git ls-files --error-unmatch` and a repository-relative pathspec, and it
  **reports rather than refuses**: a file a build step creates, or one a
  container writes on first start, is a legitimate absence, and Feat cannot tell
  it from the one that will hurt. A bind entry that interpolates is reported as
  not checked rather than passed over, because a report that listed only what it
  checked would read as a report on everything.
- **The masking case is known and not built.** A mount whose source is outside
  every checkout and whose target is inside a container path — `/dev/null` bound
  over a `.env` to blank it — is the shape the reference project actually uses,
  and it is not implemented here. It is one organisation's compliance pattern
  with a known population of one, and the general rule that would catch it would
  have to reason about targets inside a mount Feat generates. Recorded so that
  its absence is a decision rather than an oversight.

Consequence: [04-functional-specification.md](04-functional-specification.md)
FR-PROJ-004 gains the mount pre-flight, and FR-PROJ-005 names where a repository
is mounted among what the guided configuration derives. No existing behaviour
changes: the runtime reading is identical, and the two new findings are a warning
and a not-checked note that no correct project produces.

Two things this leaves behind, both worth stating rather than discovering later:

1. **A derived path and a default can compose a configuration Feat refuses.**
   Configuration rejects two repositories mounted inside one another, so a
   devcontainer that mounts one repository at `/srv` plus a second repository
   defaulting to `/srv/<id>` fails validation — at `Review()`, which in
   `feat project init` ends the conversation and loses every answer. Two defaults
   could never overlap, so this is reachable only now, and only in a devcontainer
   project with two or more repositories whose files name a path around `/srv`.

   **Fixed on request, immediately after, and the fix is the rejection rather
   than a cleverer proposal.** The mount question refuses an answer that overlaps
   a container path another repository already has, names both repositories, and
   asks again — which is the stance `feat project init` already takes on every
   other refusable answer, "because the alternative is a command that fails at
   the end of a conversation over something the user could have corrected when
   they typed it". The rule is `config.PathsOverlap`, exported for this and used
   by both, because two implementations of one rule drift and the one a user
   meets first would be the wrong one. The proposal is left alone: a second
   invented fallback, `/srv/<id>-2` or the like, would put a name nobody chose
   into a configuration file to avoid a message that is easy to read.

   Nothing is added for the runtime container paths, which are not overlap-
   checked at all and should not be: a repository's worktree is mounted into its
   own services, so two repositories expecting their source at `/app` are two
   containers agreeing rather than a collision.
2. **The end-to-end value of the proposal is unverified.** Neither project on
   this machine can show it: one devcontainer mounts no repository source, and the
   other writes its sources through `${...}`, so they land in `Undecided` and Feat
   derives nothing from them by rule. The proposal is unit-tested and the fallback
   is what both local projects exercise. It wants a run on the reference project,
   whose devcontainer does bind-mount its repositories.
3. **A "~" in a bind source is resolved by Compose and not by Feat, and the code
   said the opposite.** `absolutePath` carried the claim that "Compose does not
   expand one either, so a path starting with one names a directory called `~`".
   Measured against Docker Compose v2.40 while testing this change: `~/.claude`
   renders as the user's home directory. The claim was harmless while the only
   question asked of a resolved source was whether it *equalled* the repository —
   `<project_dir>/~/.claude` equals nothing — and stopped being harmless the
   moment the question became whether it lies *inside* the repository, because a
   path joined onto the project directory always does. It reported
   `~/.claude:/home/dev/.claude`, the mount Feat's own devcontainer recommends,
   as a path a task's worktree would not hold.

   The first fix was to skip such a source, and it was the wrong shape: it made
   the symptom go away by declining to answer. **The reader now expands a "~" the
   way Compose does**, through `paths.Expand`, which is the same expansion the
   configuration loader already applies to the paths in a project's YAML — so a
   "~" written in a Compose file and a "~" written beside it in `feat`'s own
   configuration name one directory.

   Three consequences, in ascending order of how much they matter:

   - `~/.claude` resolves to the home directory, which is nowhere near the
     repository, so the false positive is gone on the merits rather than by rule.
   - A repository mounted as `~/repos/api:/srv/api` is recognised as the
     repository, so its container path is derivable. It was invisible before.
   - **A build context written `~/repos/api` was actively wrong, and is the one
     defect here rather than a missed proposal.** Joined to the project
     directory it became `<project_dir>/~/repos/api`, which — because a
     repository's own files are read with that repository as the project
     directory — passes the inside-the-repository test. So `BuildsFromSource`
     was true, and `runtimeBuilds` redirected the build at
     `<worktree>/~/repos/api`, a directory that exists nowhere and that
     `redirectBuild` does not check for. A task's launch would have failed on a
     path produced entirely by a spelling.

   **Two corrections to what this ADR said before.** The first version of this
   item claimed that without expansion "Feat's generated override does not
   replace that mount with the task's worktree and those services run the user's
   ordinary checkout". That is wrong about the mechanism: `runtimeMounts` builds
   the override's mounts from the **configured** `runtime.container_path`, and
   nothing outside the wizard and `feat doctor` reads `SourceTargets` at all. A
   "~"-written repository mount cost a *proposal*, which the user then typed by
   hand. The build context above is the part that acted on a resolved path, and
   it is where the real failure was.

   It also framed the decision around how rarely a "~" appears locally — four
   sources across fifteen Compose files, all of them `~/.claude`. That
   measurement supports "there is no local instance to test against" and nothing
   more; frequency on one machine is not evidence about what people write, and
   the argument that settles it is simpler: the reader's whole contract is to
   answer the question Compose will be asked, and on this spelling it answered a
   different one. Unlike reading `devcontainer.json`, expanding a "~" creates no
   obligation — nobody reads it as Feat implementing a specification, any more
   than reading a mount target out of a Compose file does.

   A "~other" is refused rather than resolved, following `paths.Expand`, which
   declines to reach into another user's home. Such a mount is reported as unread
   in the mount check, for the reason an interpolated one is: a report that
   passed over it silently would claim to have checked a mount nobody looked at.
   A machine whose home directory cannot be established resolves no "~" at all
   and reports each one the same way.
