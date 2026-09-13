# ADR-098 — A mount whose target lands in a task's worktree is an error before the task exists, and the fix is refused

Status: accepted
Recorded: 2026-09-13, from the v0.1.0 acceptance run and from a measurement of the container runtime taken while writing this

ADR-081 built the mount pre-flight from one side and recorded the other as known
and not built: "a mount whose source is outside every checkout and whose target
is inside a container path — `/dev/null` bound over a `.env` to blank it — is the
shape the reference project actually uses, and it is not implemented here … one
organisation's compliance pattern with a known population of one". That
population then stopped the `v0.1.0` acceptance run at container creation. This
builds the diagnosis and refuses the fix, and it corrects what the failure was
believed to be.

Evidence:

1. **The acceptance run could not launch a task.** The work devcontainer masks
   environment files by policy:

   ```yaml
   - ${REPOS_ROOT:-${HOME}/repos}/dashboard:/workspace/repo-name
   - /dev/null:/workspace/repo-name/.env:ro
   ```

   ```
   error mounting "/dev/null" to rootfs at "/workspace/dashboard/.env":
   create mountpoint for /workspace/dashboard/.env mount: mountpoint
   ".../worktrees/<project>/<task>/dashboard/.env" is outside of rootfs
   ```

   To mount anything at a path inside a bind-mounted worktree the runtime has to
   create the mount point. That path resolves onto the host inside the worktree,
   and it refuses to create one outside the container's rootfs. An ordinary
   checkout satisfies the mount because `.env` is simply there; a worktree holds
   only what Git tracks, so it is not.

2. **`checkMounts` could not see it, because it reads the other half of the
   entry.** It receives the mounts whose *source* resolves inside the repository,
   asks `git ls-files --error-unmatch`, and warns. This mount's source is
   `/dev/null`. The gap was one-sided, and the sides are not equally bad: a
   source inside the repository that Git does not track is a container that
   starts and an application that misbehaves, while a target inside the worktree
   is a container that is never created.

3. **The reason `checkMounts` warns does not survive the crossing.** It reports
   and refuses nothing because "a file a build step creates, or one that arrives
   with a `postCreateCommand`, is a legitimate absence". No command in the
   container runs before its mounts, so on this side there is no build step that
   could have supplied the file and no legitimate absence to allow for.

4. **What actually fails is narrower than the error message reads, and reasoning
   from the message gets it wrong.** Measured against Docker 29.5.2 on this
   machine, with a bind mount standing in for a worktree and the target absent
   inside it:

   | The entry's source | Result |
   | --- | --- |
   | `/dev/null` | **refused**, exit 125, the message in evidence 1 |
   | a regular file | **refused**, identically |
   | a directory | starts; the runtime creates the directory |
   | a source that does not exist | starts; the runtime creates source and mount point as directories |
   | a named volume | starts |
   | a tmpfs | starts |
   | anything, where the worktree already holds the path | starts |

   So what decides it is whether the mount point would have to be a **file**,
   which is decided by the *kind* of the source and not by its contents. A
   `/dev/null` and a real file fail alike, which is the acceptance run's own
   observation that the source is irrelevant — but that observation generalised
   one step too far. The same entry pointed at a directory does not fail at all,
   and a rule that skipped the test would report every `node_modules` bind, every
   cache directory, and every named volume in the file as fatal. That is the
   commonest mount shape there is, so the broad rule would have been a check that
   fails correct projects.

5. **A refused launch is not inert, and this is why the failure looked stranger
   than it was.** The daemon creates the missing mount point in the worktree on
   its way to failing — an empty `.env` appears — and the *second* attempt with
   the same configuration starts. Measured twice: exit 125, then exit 0. So the
   per-task workaround the acceptance run found, `touch` the path and resume, is
   a step the runtime has already taken; `failed` to `preparing` is a recovery
   edge that already exists, and resuming is enough on its own. It also means the
   state a retry reaches is precisely the one evidence 8 argues against: the
   policy ends up masking nothing, because what is now mounted over is an empty
   file the mask itself created.

6. **The explanation existed and was wired to the wrong moment.**
   `internal/runtime/compose/explain.go` names this cause and its remedy, after
   the container runtime has failed. A post-mortem on an error string cannot be
   read before a task exists, and this is a project-shape problem that is true of
   the project from the moment it is configured.

7. **Two ruled-out approaches were ruled out for the wrong reason, and one of
   them is now measured.** The acceptance notes ruled out tmpfs on the grounds
   that it has the "same mountpoint requirement". It does not: a tmpfs at an
   absent target inside the worktree starts (evidence 4). tmpfs is still not the
   answer, because it mounts a directory and the thing being masked is a file, so
   the conclusion stands and its reason is replaced. The rest hold as recorded:
   `create_host_path` governs the source; reordering does not help, because
   Compose applies parent targets before child ones by depth; creating the file
   in the Dockerfile does not, because the bind shadows it and the runtime still
   needs it host-side; an entrypoint does not, because mounts precede any
   process; and there is no way to exclude a path from a bind mount.

8. **In a worktree the mask has nothing to mask.** Dropping the mount satisfies
   the policy better than an empty file does — absent rather than empty — which
   is what makes "drop the mount" a remedy to offer rather than a policy
   violation to propose.

9. **A removal mechanism exists in Compose and there is exactly one, established
   against the real CLI on 2026-09-06.** `!reset` on a single volume entry is
   ignored and the mount survives; `volumes: !override [...]` on the whole list
   works, and anything not re-listed is dropped; a long-form mount at the same
   target with a different source replaces the source. There is no "keep
   everything except this one": Docker has no optional-mount concept and
   Kubernetes' `optional: true` has no Compose equivalent. Feat's generated
   override already uses this vocabulary (`container_name: !reset null`,
   `ports: !reset []`), so the syntax is available at the version Feat requires.
10. **A native Linux daemon does not refuse it, and this was found the way it
    should have been.** Evidence 4 was measured on Docker Desktop on macOS, and
    OQ-016 recorded the Linux half as open. Running the opt-in test on a Linux
    machine failed on exactly the two cases that assert the refusal: the runtime
    created the file mount point and the container started. Nothing is refused
    there in either direction — the three cases that start on Docker Desktop
    start on Linux too — so the file-and-directory asymmetry is a property of
    Docker Desktop rather than of container runtimes.

    The cause is visible in the error evidence 1 quotes: the mount point resolves
    through `/run/host_virtiofs/…`, a bind mediated by Docker Desktop's virtual
    machine, and runc refuses a path whose realpath escapes the container's
    rootfs. A native bind is in the same mount namespace and resolves inside it.

    So one project is a task that cannot launch on one machine and a task that
    launches on the next. What this did *not* cost is a wrong check shipped
    quietly: the test asserting the refusal is what failed, on the machine that
    proves it, which is what a test pinning a measurement is for.

Decisions:

- **The target side is reported before a task exists, at the severity the runtime
  earns.** A bind mount writing into where Feat mounts a task's worktree, on a
  path the worktree would not hold, is reported either way — the finding is about
  the project and is true on every runtime. What differs is the consequence, so
  what differs is the severity:
  - where Feat has established that this runtime refuses such a mount point, it
    **fails** the diagnosis and exits non-zero. Evidence 3 is why that severity
    is available here and not to its sibling, and the message says so: it names
    two of the three remedies `checkMounts` offers — commit it, or drop the
    mount — and names the third as one that cannot work here, because a user who
    has read the sibling warning will otherwise reach for it;
  - where it has not, it **warns**, and the action names both measured outcomes
    rather than choosing one, because which applies is exactly what is not known.

  Feat asks the runtime what it is rather than inferring it from this machine's
  operating system: what decides it is whether a bind crosses a virtual machine,
  which a Linux host running Docker Desktop or Colima also does, and which a
  daemon reached over a socket reports for itself. `docker info` reads nothing
  belonging to the project, so it is a question this command may ask (ADR-028).

  **A runtime Feat cannot place answers no**, including one there is no Docker to
  ask about. Under-claiming is the direction to fail in: the finding still
  reaches the user one severity lower, and a launch that does fail is explained
  where it fails, so a missed pre-flight costs a run — while a wrong refusal
  blocks a project that works, on a check whose exit code is what CI reads.
- **Only where the mount point would have to be a file**, which is evidence 4.
  Feat establishes that by asking what the resolved source is, and a source it
  cannot examine is reported as unread rather than judged either way, for the
  reason an interpolated entry is (ADR-081, ADR-028).
- **One entry, one finding.** The classification happens once, in the reader,
  where both halves of the entry are known: an entry writing into the container
  path is the target question and is not also the source question. So a mount
  that was warned about before and lands there is now an error rather than both,
  and a mount landing anywhere else keeps exactly the warning it had. This
  changes the severity of an existing finding, which is the intended correction:
  a fatal mount reported as a warning with a zero exit is the understatement this
  was written to fix.
- **Neither mount question is asked about a repository a task mounts no worktree
  of, and this amends ADR-081.** Every word both checks say is about a worktree —
  "a task works in a worktree, which holds only what Git tracks, so that path
  will not be there" — so both are silent where a task is given a checkout
  instead. ADR-081 asked the source-side question of every repository, which was
  right about the ones it was written for and wrong here, and the rule now lives
  in one predicate both checks consult. Two default access modes qualify, and
  both are named rather than left to be inferred, because the first draft of this
  decision stated the rule and implemented it one mode short:
  - **omitted**, which puts the repository in no task at all;
  - **stable read-only**, which is the sharper of the two. Such a repository is
    not a task repository — it has no branch and no worktree, and a task mounts
    the *ordinary checkout* of it read-only (`taskMounts`), while `runtimeMounts`
    passes over it because it iterates the task's own repositories. An ordinary
    checkout holds the ignored files a worktree does not, so a mask over one
    works, and judging it would report a project that runs as broken. That is the
    failure `checkRuntime`'s own comment warns about — "a diagnostic that
    reproduced it would report a project that works as broken" — and it is not
    hypothetical: `stable_read_only` is in `docs/examples/project.yaml`, in the
    fixture this is tested against, and in projects on this machine.

  A task may promote a stable read-only repository, and it then does get a
  worktree and such a mount could fail. `feat doctor` runs before any task exists
  and cannot know which future task promotes what, so it speaks about the
  default — the same reason an omitted repository is not asked either, though a
  task may select one.

  What is not silenced is the report of what Feat could not read. An unread entry
  is a statement about the reader rather than a claim about the project, and it is
  true of a repository whichever way a task mounts it.

  The condition lives in one predicate, `mountsNoWorktree`, which both checks and
  the container-path helper consult. Two implementations of one rule drift, and
  the one a user meets first would be the wrong one (ADR-081).

  The reader is given a container path by `feat doctor` and by nothing else, so
  the wizard deriving a container path, the daemon reading build contexts, and a
  repository whose services bake their code all read these files exactly as they
  did before.
- **No fix is built.** The two candidates are refused:
  - *Materialise declared files* — a per-repository list of paths Feat creates
    empty in each worktree. It solves this case and is the wrong shape generally:
    for the commoner `env_file: .env` case an empty file yields an application
    running without its configuration, which is worse than a clear failure.
    Evidence 5 sharpens this rather than softening it — the runtime already
    performs exactly this materialisation on the failing attempt, and what it
    produces is the empty-file outcome, reached by accident.
  - *Drop unsatisfiable mounts* — re-emit the volume list under `!override` with
    such mounts removed, which evidence 9 says is possible. It is strictly more
    correct here and strictly more invasive: Feat would rewrite the project's
    mount list wholesale rather than add to it, and a wrong reconstruction loses
    mounts silently. Everywhere else Feat resets only keys that provably cannot
    work per task — `container_name`, `ports` — and "this mount looks
    unsatisfiable" is a judgement rather than a fact of that kind. It would also
    have to be reconstructed from a deliberately partial reading: Feat parses
    these files structurally and never runs `docker compose config`, because that
    renders values taken from the project's environment files and Feat must not
    read them (ADR-028), so interpolation, `extends` and `volumes_from` are all
    unresolved. The objection is stronger than it first looks.

  One setup with an unusual masking policy is not evidence enough to add
  configuration surface or to start rewriting projects' mounts. **Triggers for
  revisiting, recorded so that the refusal has an expiry rather than a mood:** a
  second project needing a file materialised in a worktree, or the first case
  where an empty file is the right answer and the user cannot work around it.

Three things this deliberately does not do:

- It does not touch `internal/runtime/compose/explain.go`. That explanation is
  correct and well worded for the moment it runs; what was missing was an earlier
  moment, not a better sentence.
- It does not report named volumes or tmpfs mounts, which evidence 4 shows start
  without complaint. They are also not bind mounts, so the structural reader
  never carried them.
- It does not check a repository's runtime files against another repository's
  container path. Each repository's files are read against its own repository,
  which is the boundary the source-side check already draws, and widening it is a
  different change with no evidence behind it.

OQ-016 asked whether a native Linux bind mount refuses the same file mount point
and is **answered**: it does not, which is evidence 10 and is why the severity is
the runtime's rather than the check's. What replaces the open question is not
another claim but a mechanism — the opt-in test no longer asserts a platform, it
asserts that what Feat expects of the runtime in front of it is what that runtime
does. Colima, Rancher Desktop, WSL2 and a daemon over a socket are all runtimes
nobody here can try, and each of them fails that test on the machine that has it
rather than being reasoned about in this file.

Consequence: [04-functional-specification.md](04-functional-specification.md)
FR-PROJ-004 gains the target side of the mount pre-flight and states why its
severity differs from the source side's. `project.Composition` gains `Targets`
and `project.ComposeReader` gains `ContainerPath`, which is the switch the
previous decision turns on. The finding reaches the dashboard without any code,
because it is an ordinary finding and the dashboard runs the same checks
(ADR-064). ADR-081's fifth decision — the masking case, known and not built — is
answered in its diagnosis half and reaffirmed in its fix half, with triggers.

A repository that mounts no worktree is tested rather than reasoned about too,
against the fixture's own stable read-only repository and on both sides at once:
the exemption is the difference between a check and a check that fails correct
projects, and it was already once left out.

The tests are in three places, because a check built on a measurement needs all
three. The classification is a unit test on the reader, which is where one entry
becomes one finding and where the file-or-directory question is asked. The
severities are unit tests on the checks against a fake Git runner: the source and
target halves in one Compose document so that the discriminator between them is
asserted rather than arranged, and one runtime of each kind so that both
severities are pinned to what the runtime is rather than to what the check
prefers.

And the runtime's behaviour is an opt-in test against real Docker, which asks
Feat what it expects through the same predicate the check uses and asserts the
runtime agrees. That is the part evidence 10 changed. Asserting the refusal
outright was a test of one machine wearing the clothes of a test of the rule, and
it failed on the second machine it met — correctly, but a version that had
instead been written against `runtime.GOOS` would have passed while being just as
wrong. Both directions of disagreement now fail: an expected refusal that does
not happen is an error reported against a project that works, and an unexpected
one is a launch that fails after a diagnosis that only warned.
