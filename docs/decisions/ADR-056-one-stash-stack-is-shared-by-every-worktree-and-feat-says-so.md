# ADR-056 — One stash stack is shared by every worktree, and Feat says so rather than working around it

Status: accepted  
Recorded: 2026-08-13, from an assessment of what concurrent tasks on one repository share

A linked worktree has its own working tree, index, and HEAD. Everything else
belongs to the repository it was created from, and Git's per-worktree ref
namespace is exactly `HEAD`, `refs/bisect/*`, `refs/worktree/*`, and
`refs/rewritten/*`. `refs/stash` is not in it. Every task worktree Feat creates
for a repository, and the user's own checkout, therefore push onto and pop from
one stack, and `git stash pop` takes the newest entry in the repository
regardless of who made it.

Evidence:

1. It is deterministic, not a race. Task A stashes, task B stashes, task A pops:
   A's working tree now holds B's changes and B's entry is gone. Both commands
   exit zero and print what they normally print. Under concurrency it is worse
   and just as quiet — three worktrees running forty stash/pop cycles each
   restored another worktree's content 91 times out of 120, reported zero
   errors, and left six orphaned entries behind.
2. The user's own checkout is on the same stack. An agent can pop the
   maintainer's uncommitted work into a task worktree, and one `git stash clear`
   removes every entry the user had. That is the preservation rule in
   [05-security-model.md](05-security-model.md) failing in the one place nothing
   was watching.
3. Containers do not bound it. A task worktree is not a repository on its own,
   so `internal/daemon/execution.go` mounts the main checkout's whole Git
   directory, read-write, at its host path in every task container. The shared
   stack is reachable from inside an environment that is otherwise isolated.
4. Git protects the neighbouring cases and not this one. Checking out or
   deleting a branch another worktree holds is refused with a message naming the
   worktree; the stash has no such guard, and `git worktree remove` leaves a
   task's entries behind in the user's repository.
5. Redirecting the ref does not work. `git symbolic-ref refs/stash
   refs/worktree/stash` looks like the fix and is not one: the ref dereferences
   per worktree while the reflog stays shared, which produces `warning: log for
   ref refs/stash unexpectedly ended` and `error: 'refs/stash@{0}' is not a
   stash reference` — an unpoppable stack, which is worse than a shared one.
   Git 2.52 has no configuration for the stash ref; `stash.index`,
   `stash.showPatch`, `stash.showStat`, and `stash.showIncludeUntracked` are all
   of it.
6. Nobody upstream has fixed it and nobody documents it. `git-worktree(1)`
   recommends a worktree as the alternative to stashing without mentioning what
   the two share. The same defect is open against another agent CLI
   (github/copilot-cli#1725, February 2026), and the published analyses of
   worktrees as an agent boundary reach this list independently: hooks, config,
   the stash, refs, and the object store.
7. Two of the settings that reach the stack are not commands the agent chose.
   `rebase.autoStash` and `merge.autoStash` live in the repository's shared
   configuration, which is the user's, and on a conflicting re-apply the
   autostash entry is left on `refs/stash`. An instruction the session follows
   perfectly does not cover them.

Decisions:

- The session is told. The generated Claude instructions state that the
  repositories are worktrees sharing one Git directory, that the stash is one
  stack per repository, that `pop` takes an entry that may not be its own, and
  that work in progress belongs on the task branch. It is the one thing in that
  document that is not about the protocol, because it is not advice about the
  work: it is a fact about an environment Feat built and the session cannot see.
- The settings that decide on the session's behalf are turned off.
  `git.WorktreeEnvironment` renders `rebase.autoStash=false` and
  `merge.autoStash=false` as `GIT_CONFIG_COUNT` entries, and the daemon adds
  them to every agent launch in both execution modes. A rebase with a dirty tree
  then stops with `error: cannot rebase: You have unstaged changes`, which names
  what to do next, instead of stashing where anyone can pop it.
- They travel as environment, never as a configuration write. The file that
  would have to hold them is the user's `.git/config`, shared by the worktree
  and the checkout, and a value written there to protect a task would outlive
  the task and change the user's own commands.
- The environment reaches the process in both modes. Devcontainer execution
  already passed the adapter's variables through `docker compose exec --env`;
  host execution dropped them, so `tmux.CommandSpec` carries variables and
  `respawn-pane` renders them as `-e` before the working directory and program.
  A guard that held in one mode would be a guard the user could not rely on.
- Stashing is not forbidden. Nothing refuses `git stash`, and no tool policy is
  added to the generated settings: a session that stashes has decided something,
  and this is for the settings that decide without it. What that leaves is
  recorded below rather than described as solved.
- The structural fix is named and not taken. A per-task clone — hardlinked, or
  sharing objects through `alternates` — gives each task its own refs, config,
  hooks, and stash, and would let the read-write Git directory mount in evidence
  3 become task-scoped. It is not a patch: branch visibility in the user's
  repository, the review comparison, and the `git worktree`-based cleanup plans
  of ADR-029 all assume linked worktrees. It stays available and unchosen.

Consequence: the loud half of the hazard is closed and the quiet half is
narrowed to something a session had to choose. An agent can still stash, a
`git stash clear` still reaches the user's entries, and a removed worktree still
leaves its entries in the repository. Revisit on the first dogfood incident, or
when concurrent tasks on one repository stop being occasional — whichever comes
first — and decide then between a refusing hook and the per-task clone, which
OQ-014 holds open.
