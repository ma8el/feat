# ADR-057 — The agent's environment is owned by its session, and the pair of verbs is resume and stop

Status: accepted
Recorded: 2026-08-13, from dogfooding the devcontainer lifecycle

A task's agent environment had two ways in and one way out of the product:
`feat implement` created it, cleanup removed it, and nothing else named it. The
maintainer's question, asked while looking at a task whose container had died:
why does a devcontainer have no management surface, when the application runtime
next to it has six verbs?

Evidence:

1. The command surface says it plainly. `feat runtime` has `create`, `start`,
   `stop`, `destroy`, `status`, and `logs` for the *application's* environment.
   The agent's had none, and `Resume` — the one verb that manages it — existed on
   the daemon and the client with the dashboard as its only caller. So
   reconciliation's own finding said "resume the task to start it again" to a
   user who, outside the dashboard, had no way to do that.
2. The record and the guard disagreed with each other, and one pass produced
   both halves. `reconcileEnvironments` wrote what it saw onto the execution
   record and left the session's process state alone; `resumable` decided from
   that process state, and its liveness check was the *existence* of a tagged
   tmux object. Feat sets `remain-on-exit` on every pane it creates (ADR-030), so
   a pane whose command is over is still a pane tmux reports — and the pane's own
   process is on the host side of a container, so it outlives one. Measured on a
   jobharbor-dev task whose devcontainer exited 137: the finding said to resume,
   the resume said "it is running in a terminal that is still there. Attach to it
   instead", and the only way out was `kill-window` on Feat's own tmux socket.
3. Stopping a container meant destroying the task. `feat task cleanup` was the
   only thing that would stop an agent's containers, and it removes the worktree,
   the branch, and the control workspace with them. A user freeing memory
   overnight had no lever that was not also a way to lose the work.
4. One container per project was evaluated and does not work. The generated
   override replaces the base file's mounts *by container path* with this task's
   worktrees, at the access this task selected (ADR-033 evidence 1 and 2) — so
   which worktree is at `/srv/api` is the task's identity expressed as container
   configuration, and one container cannot answer it three ways. The one shape
   that is mechanically possible, mounting the worktree root and giving each
   agent a subdirectory, gives up cross-task isolation, per-repository read-only
   access, and the per-task control workspace that ADR-032 splits — and it
   returns the user's real checkout to the agent, which is what evidence 1 exists
   to prevent.
5. A `start` verb would manufacture the resource class Feat handles worst. A
   launch that fails after its container exists already leaves a container the
   record does not name, and cleanup plans nothing for it. A verb whose purpose
   is to create a container with no session behind it would make that state
   reachable deliberately.

Decisions:

- The environment's lifetime is its session's, and this is a rule rather than an
  omission: it comes into being with a launch, comes back with a resume, sleeps
  with a stop, and is removed by cleanup. There is no `start` and no `create` for
  it. [04-functional-specification.md](04-functional-specification.md) states it
  where a reader looking for the missing verb will find it.
- One invariant ties the two halves of a session together: an agent process
  cannot be alive while the environment it runs in is not running. Every
  observation applies it, so a session whose container is gone stops claiming a
  running agent — which is what makes the recovery reconciliation already
  recommends available to the user it recommends it to.
- The state is `failed` rather than `stopped`, and it raises `session_failed`. A
  stop the user asked for records its own process state before any observation
  runs, so an alive process against a container that is not running is a death
  nobody asked for. No desired-state field is added: the act that had the
  intention is what writes it, which keeps every later reading a pure observation
  (CLAUDE.md's rule that persisted desired state is never assumed to equal
  observed state).
- Resume decides liveness from what a user could actually attach to: the window
  exists, its agent pane is not dead, and — for a containerised session — the
  environment is running. Each is asked rather than believed, and a failure to
  look is not evidence, so an unreadable tmux or Docker refuses the resume by
  name rather than guessing.
- `feat task resume` and `feat task stop` join the noun ADR-040 established.
  Neither takes a top-level alias: they are new, so there is no shell history to
  protect.
- Stop keeps the worktrees, branches, control workspace, volumes, and tmux
  window, does not move the workflow, does not touch the application runtime, and
  clears attention — a task with no agent is not waiting for anybody. It is
  `docker compose stop` rather than `down`, and it asks for no confirmation
  except in the dashboard, where a key press is cheaper to hit by accident than a
  typed command and only when the agent is mid-turn.
- A launch, a resume, and a stop are bounded by `api.AgentTimeout`, declared on
  the endpoint's contract the way `api.RuntimeTimeout` is, with the client
  waiting for it plus a margin. Three minutes, matching the daemon's own patience
  for a container to come up. This closes the launch half of the cancelled-launch
  finding: a client that gave up at ten seconds cancelled a launch the daemon was
  still serving and left a container behind that nothing could name.

Consequence: two commands, one endpoint, one interface method, and one invariant
in the two places that observe an environment. `feat project build`, an execution
poller, and any per-project container stay out — the first two are worth doing
and are not this, and the third is refused above. The orphaned container a failed
launch leaves is still open: the budget stops the launch being cancelled, and
finding a container for a task with no session is a separate piece of work.
