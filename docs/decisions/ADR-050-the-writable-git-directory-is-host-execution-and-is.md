# ADR-050 — The writable Git directory is host execution, and is disclosed rather than closed

Status: accepted
Recorded: 2026-08-11, alpha review

The alpha review asked what the devcontainer boundary is worth and found one
mount that goes straight through it. ADR-033 gives a task's container the main
checkout's Git directory at its host path, with the access the worktree has,
because a linked worktree is not a repository without it. For a read-write task
that mount is writable, and `.git/hooks` and `.git/config` are common-directory
files shared with the user's own checkout. Writing either is host code execution
as the user, outside the container.

[05-security-model.md](05-security-model.md) already accepts Git metadata
mutation, but it accepts it for full Git inside a *native host worktree*, where
there is no boundary to cross. Nothing in the documents said that the same mount
in devcontainer mode converts container write access into host execution, and
[06-technical-architecture.md](06-technical-architecture.md) cited that
acceptance as though it covered this case.

Evidence:

1. The mount is the user's own Git directory, not a copy. `gitMetadataMount`
   returns `filepath.Join(repository.HostPath, ".git")` as both source and
   target, with `ReadOnly` set only when the task holds the repository
   read-only. `hooks/` and `config` are common-directory files: one checkout and
   every linked worktree share them.
2. Feat's own checks pass it by design. `CheckMounts` tests each mount for a
   container-runtime socket, then for a forbidden source, then for writability
   against a read-only declaration. The Git directory is exempt from the
   forbidden-source rule deliberately — without the exemption the mount ADR-033
   requires would refuse every launch — and it is writable exactly as declared,
   so the writability rule is satisfied rather than violated.
3. No commit is needed, and in the cheapest case no Git command is. A hook is
   the slow path: `post-checkout` waits for a checkout, `post-merge` for a pull,
   `pre-commit` for a commit. `.git/config` is the fast one. `core.fsmonitor`
   names a program that Git runs on every index refresh, so `git status` alone
   fires it; `core.pager` fires on `git log`; `diff.external` on `git diff`;
   `core.sshCommand` on any fetch. An `[alias]` entry is the weakest of these
   and should not be the example anyone reaches for: Git will not let an alias
   shadow a built-in, so it waits for the user to type a name they had no reason
   to type.
4. Feat detonates it without the user touching Git. The host Git adapter runs
   `fetch` when it plans a task (`git.go:72`), `worktree add` when it creates one
   (`git.go:233`), and `worktree prune`, `worktree remove`, and `branch -d` when
   it cleans one up. `fetch` updates refs, which runs the `reference-transaction`
   hook; `worktree add` runs `post-checkout` as well. So an agent that writes a
   hook during one task gets host execution from the next `feat task create` in
   that project, through Feat's own ordinary loop.
5. Git offers no protection to rely on. `safe.directory` is an ownership check
   and the user owns the repository. Git has always treated write access to
   `.git/config` as equivalent to code execution — it is why `clone` does not
   copy the remote's configuration — so the exposure is the documented
   consequence of the mount, not a defect in Git.
6. The exposure and the feature are one mount. FR-GIT-006 requires full Git in
   the agent environment and FR-GIT-007 requires that the agent be able to
   commit. Mounting the metadata read-only satisfies neither: `git commit` writes
   objects, refs, and the index through that directory.

Decisions:

- The mount stays writable for a read-write task, and the exposure is written
  down in the terms above rather than left to be inferred. § Git boundary states
  the devcontainer case explicitly; the architecture document stops citing an
  acceptance that did not cover it.
- The claim Feat makes about the devcontainer is narrowed to what is true: it is
  a boundary everywhere except the one place Feat itself opens. The existing
  hedge — that the checks are not a defence against a kernel or
  container-runtime exploit — is not the relevant one here. This path needs no
  exploit and no misconfiguration; it is the supported configuration working as
  designed.
- Mounting `.git` read-only is rejected, with its cost stated so the option
  stays visible: it would take FR-GIT-006 and FR-GIT-007 with it and leave an
  agent that can read history and write nothing, which is not the product.
- Restricting which metadata paths are writable is rejected as a boundary that
  cannot be drawn where it would need to be. `hooks/` and `config` could be
  masked, but `commit` needs `objects/`, `refs/`, and `index`, and a writable
  `objects/` plus a writable `refs/` is enough to move any branch the user has;
  `config.worktree`, `info/attributes`, and the hooks path itself are further
  doors of the same kind. A partial mask would read as a fix while leaving the
  class open, which is worse than the honest mount.
- Host-mediated Git is neither adopted nor rejected here, and the premise it is
  usually argued from is corrected: Feat has no host mediation to extend.
  `docs/06` § Agent capabilities supports *direct* `gh`/`glab` use in the
  container, authenticated by a mounted or injected credential. Proxying Git
  through the host would be new machinery, and it inherits the problem rather
  than solving it — a host-side Git that accepts commits from the container must
  still run hooks and read configuration somewhere, and deciding where is the
  whole design.
- A separate container-side Git metadata directory remains the one option that
  keeps both the feature and the boundary, and it stays open as OQ-006 rather
  than being decided here. It is the maintainer's call whether that work is
  worth its complexity, and it is deferred until the dogfood test is finished:
  an exposure this branch could only reason about is one that running the
  product against real repositories will price properly, and committing to a
  metadata backend before then would be choosing a permanent design early.
  This ADR records what is true today so that the choice is made against a
  stated exposure rather than an implied one.

Consequence: no code changes. Three documents move — this record, § Git boundary
in [05-security-model.md](05-security-model.md), and the Devcontainer execution
paragraph in [06-technical-architecture.md](06-technical-architecture.md) — and
the product's behaviour is unchanged, which is the point: a user who accepted the
devcontainer on the strength of the old wording accepted something narrower than
they were told. What this leaves open is a real hole with a name, reachable by a
prompt-injected agent as easily as a deliberate one, and closing it is OQ-006's
to schedule. The user-facing text is not edited here; the README block that
enumerates what Feat refuses hedges only against kernel exploits and the network,
and belongs to `fix/security-claims`.
