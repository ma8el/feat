# ADR-104 — A bind source is never rewritten, so the source-side mount question goes and the same question is asked of build contexts

Status: accepted
Recorded: 2026-09-26, from four measurements against Docker Desktop 4.76.0 / Docker 29.5.2 on macOS

ADR-081 built a mount pre-flight on one sentence: "a task works in a worktree, and
a worktree holds only what Git tracks, so a bind of an ignored `.env` names
something that will not be there." ADR-098 then found the half that bites — the
mount's *target* — and silenced both halves for a repository a task mounts no
worktree of. What was never measured is whether the source half was ever true.
It is not. The premise fails where ADR-081 asked the question, and it holds
exactly where nothing asks it.

Evidence:

1. **Feat rewrites no bind source, so a source resolves against the ordinary
   checkout.** The agent's files resolve against
   `filepath.Dir(cfg.Agent.Execution.ComposeFiles[0])`, and a runtime include
   entry carries the repository's own checkout as its project directory. Both
   generated overrides write only Feat's own mounts, and Compose merges volumes by
   target, so Feat's worktree mount replaces whatever sat at that exact target and
   leaves every other entry as written. Nothing anywhere substitutes a worktree
   path into a source.

2. **Measured: an untracked source whose target is outside the container path is
   harmless.** A worktree bind at `/srv/api` holding only the tracked file, beside
   `<checkout>/.env:/etc/app/env:ro` where Git ignores `.env`:

   | What was asked | Result |
   | --- | --- |
   | does the container start | yes |
   | what the application reads at `/etc/app/env` | the checkout's own content |

   So the source-side premise is dead in general rather than merely shaky. There
   is no absence for a worktree to fail to supply, because the path that was
   mounted was never in a worktree. The entry ADR-081 warned about describes a
   mount that works.

3. **ADR-081's evidence 5 is ADR-098's shape on its second attempt, and not a
   third mechanism.** Evidence 5 describes "a mount over a file that is simply
   created empty succeeds, and the application then misbehaves". Reconstructed
   whole, with a worktree bind at `/srv/api` and `/dev/null:/srv/api/.env:ro`:

   | Attempt | Result |
   | --- | --- |
   | first | refused, exit 125, `mountpoint … is outside of rootfs` |
   | the worktree afterwards | holds a `.env` of zero bytes, created by the refused attempt |
   | second | starts |
   | what the application reads at `/srv/api/.env` | nothing |

   Every part of evidence 5's sentence is there and its target is inside the
   container path, which is ADR-098's question. The empty file is the mount point
   the refused attempt created (ADR-098, evidence 5), and the mask then masks it,
   which is the state ADR-098's evidence 8 argues against. Evidence 5 was one
   failure seen from its second half.

   The reading that would have made it a third mechanism was measured and rejected.
   A bind whose source is absent from the checkout, with its target outside the
   container path, also starts and also produces something empty — but Docker
   creates a **directory**, in the checkout rather than in any worktree, which is
   `create_host_path` behaving identically with or without Feat. It is not a file,
   not a worktree, and not evidence 5.

4. **Measured: a build context the worktree does not hold fails the build.** A
   service that bakes its code has no mount to replace, so `runtimeBuilds` points
   its context at `<worktree>/<relative>` — the one place Feat does rewrite a path
   read out of a project's Compose file. With that directory in `.gitignore`, so
   absent from the worktree:

   ```
   unable to prepare context: path ".../worktrees/web/generated" not found
   ```

   The build client resolves the context before any layer runs, so this owes
   nothing to what a container runtime does with mount points and is not runtime-
   sensitive the way ADR-098's finding is. **`internal/project/checks.go` asked Git
   about no build context at all.** The question sat where its premise fails and
   was absent where it holds.

5. **A bind source pointing into the checkout is a task-isolation leak, and it is
   not the question ADR-081 asked.** Evidence 2 is the leak as well as the
   refutation: the container read the user's own working copy. That is true of a
   tracked path exactly as of an untracked one, so Git is the wrong oracle for it,
   and `checkMounts` could not have been re-reasoned into asking it. It would be a
   different check, on a different set, with a different oracle.

Decisions:

- **The source-side mount check goes, and this amends ADR-081.** `checkMounts` is
  removed, with `Composition.Mounts`, `MountedPath` and the reader branch that
  filled them — a field no check reads is worse than no field. What replaces it is
  two things that already exist and one that did not: ADR-098's target-side check,
  which is the half that ever failed; the runtime's own post-mortem
  (`internal/runtime/compose/explain.go`), which was always a statement about
  targets; and the build-context check below.

  It is removed rather than narrowed because nothing is left for it to narrow to.
  Its population was "a bind source inside the repository that Git does not
  track", and evidence 2 says every member of that population is a mount that
  works. A check whose every finding is a false positive has no true subset.

- **The same question is asked of build contexts, where the premise is literally
  true.** `checkBuildContexts` asks Git whether the context of each declared
  runtime service is tracked, with `ls-files --error-unmatch` and a
  repository-relative pathspec, which answers for a directory when anything under
  it is tracked. Three boundaries, each for a reason rather than for tidiness:
  - **runtime-side only.** The redirect is runtime-side; the agent's Compose files
    have no equivalent, so an agent build context resolves in the checkout and the
    premise fails there exactly as it failed for sources.
  - **only a context Feat would redirect**, which makes the check's population the
    redirect's own. A context that interpolates is left unread, an unread context
    is not redirected, and such a service builds from its own checkout — so unlike
    a mount there is no coverage to disclose, because Feat acts on nothing it
    could not read. A context outside the checkout is not redirected either.
  - **silent for a repository a task mounts no worktree of**, through
    `mountsNoWorktree`, the predicate ADR-098 put the rule in. Nothing redirects a
    build context of a repository with no worktree.

- **It warns rather than fails**, which is ADR-081's own reason arriving at the
  question it fits. A generated or vendored directory is created in the worktree
  on the host, and a task's services are started manually after the agent has
  worked, so an absence `feat doctor` sees may be gone by the time the build runs.
  That is the legitimate absence ADR-098 established does not survive to the
  target side — no command in a container runs before its mounts — and it does
  survive here. Under-claiming is the direction to fail in (ADR-098): a build that
  does fail says so, with the path, where it fails.

- **The isolation leak is recorded and not checked.** Every bind whose source is
  inside the checkout puts the working copy in the container, which is the whole
  population and includes `./docker/nginx.conf` and every cache directory. A
  project binding its own paths into its own services is the ordinary arrangement,
  Feat cannot tell a leak that matters from one that is the point, and a check on
  that set is the diagnostic `checkRuntime`'s comment warns about: one that reports
  a project that works as broken. Where the bound path is source code the target
  is inside the container path, and ADR-098 already reports it.

  **Triggers for revisiting, so the refusal has an expiry rather than a mood:** a
  task whose result was wrong because a service read the checkout rather than the
  worktree, or a shape where the leaked path is code and the target is outside the
  container path — which is the case this reasoning assumes does not occur.

Consequence: [04-functional-specification.md](04-functional-specification.md)
FR-PROJ-004 loses the source-side mount bullet and gains the build-context one.
The finding reaches the dashboard without any code, because it is an ordinary
finding and the dashboard runs the same checks (ADR-064). ADR-081's fourth and
fifth evidence items, its `feat doctor` decision, and its `Composition.Mounts`
decision are amended; its container-path derivation, its parameter split, its "~"
expansion and its `UnreadMounts` disclosure are untouched, and the wizard reads
these files exactly as it did. ADR-098 is untouched in every part: its question
was the target's, the two access modes it exempted stay exempted, and the
predicate it centralised now has a third caller.

The four measurements above are opt-in tests rather than paragraphs.
`TestRealABindSourceReachesTheOrdinaryCheckout` holds evidence 2 and 3 —
including the two attempts, so that a runtime which does not refuse reaches the
same state in one — and `TestRealARedirectedBuildContextTheWorktreeLacksFailsTheBuild`
holds evidence 4. Neither asserts a platform: the first asks Feat what it expects
of this runtime through `RefusesFileMountPoint`, which is what its sibling does
and why that sibling caught a wrong expectation on the second machine it met. The
new check's own behaviour is a table of unit tests against a fake Git runner, and
the removal is pinned in the reader too, so that a source inside the repository
producing nothing is asserted rather than left to be noticed.
