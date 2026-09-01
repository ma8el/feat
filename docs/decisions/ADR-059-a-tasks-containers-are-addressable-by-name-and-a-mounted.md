# ADR-059 — A task's containers are addressable by name, and a mounted directory is released before it is removed

Status: accepted  
Recorded: 2026-08-14, from dogfooding the mount-and-socket rules

Both halves of this are one sentence: the container outlives the request that
created it. A launch that fails after its container exists leaves resources the
product could not see, and a cleanup that removes the tree those resources mount
fails part way through. ADR-057 closed the third half — the client no longer
cancels a launch the daemon is still serving — and left these two, saying so.

Evidence, from a jobharbor-dev task (`d7f54fa5`) whose devcontainer Compose file
had been given a mount of the home directory on purpose:

1. Nothing in the product could remove what the cancelled launch had created.
   The container was on the machine, exited, with its network beside it; the task
   record had `session: null`, because the session is created after the container
   and a launch that fails never reaches it; and both passes that could have
   found it resolved from that record. `resolveEnvironmentCleanup` and
   `reconcileEnvironments` each began by returning early for a task with no
   recorded environment, so cleanup planned nothing and reported success, and no
   reconciliation pass mentioned the containers at all.
2. It was archived over. Cleanup archives a task once the classes a user chose
   are gone, and refuses to archive one that still owns resources — but the
   refusal reads the plan, and the plan was empty. So the record that named the
   task was retired while its containers stayed, and an archived task is one
   reconciliation stops looking at.
3. Widening the launch refusals makes it more frequent rather than less. Every
   rule added in `fix/mount-and-socket-rules` inspects the started container, so
   every one of them fires after the container is up (ADR-033's amendment to
   ADR-032). The refusals are right; what they leave behind was not resolvable.
4. Removing the control workspace failed while a container still mounted it. The
   first `cleanup/execute` failed with `unlinkat …/control/jobharbor-dev/
   d7f54fa5-…/outbox: permission denied`; the second, after the container had
   died, succeeded. On macOS the file-sharing layer holds a directory that is an
   active bind-mount source, so this is an ordering rule rather than a
   permissions bug. It became reachable when the control workspace stopped being
   one mount and became three (ADR-032's read-only split).
5. The class order alone does not establish the ordering. Cleanup removes the
   agent containers before the control workspace, but only when a user chose
   both — the classes are independent choices (FR-CLEAN-002) — and the case above
   is a task whose plan named containers nowhere at all.
6. The name was there the whole time. The agent's Compose project is
   `feat-agent-{project_id}-{task_id}`, generated rather than configured
   precisely so it cannot collide (ADR-033), and Compose labels every container,
   network, and volume with the project it belongs to. `docker compose
   --project-name <name> ps --all` and `down` were measured against real Docker
   with no `--file` at all: they find an exited container and its network, they
   remove both, and they exit zero over a project that no longer has anything.

Decisions:

- A task's agent Compose project is addressable by its name alone, and the name
  is derived from the two identifiers rather than read from a record. That makes
  it total: a task that never recorded an environment has the name it would have
  used, and a project whose own Compose file has changed since — which is what
  made the launch slow enough to be interrupted — still answers, because nothing
  in the query reads that file.
- This is a derivation and not a scan. What it finds belongs to the task by
  construction, which is the exactness FR-CLEAN-001 asks for reached without the
  record that ordinarily supplies it. Feat looks at nothing else on the machine
  and adopts nothing: a container carrying another project's name is not a thing
  this can name.
- Reconciliation reports it as an orphan of the record — the finding names the
  task the containers were created for, and the action is the cleanup that
  resolves them. Nothing is restarted or removed there, as in every other pass
  (FR-STATE-004).
- Cleanup plans them as the agent-container class it already has, with the
  volumes carrying the same label as the volume class they already are. No new
  class and no new state: what changes is that the two passes have an answer for
  a task whose record has none. Archiving is refused over them by the rule that
  already existed, now that the plan can see them.
- The control workspace is removed only after establishing that no container of
  the task's agent Compose project is still there. Only that project is asked
  about, because it is the only one the workspace is mounted into — the
  application runtime's override mounts worktrees and never the control tree.
- What is established is about Feat's own containers, and the wording says so. A
  container somebody else pointed at that directory is not one Feat knows to ask
  about, and claiming the directory is unheld would be the overclaim the honesty
  rule exists for.
- An unanswerable question refuses. A Docker that will not say what it holds
  leaves a user with a workspace they can still remove once it is fixed; removing
  it on an unanswered question leaves a half-removed tree, which is the outcome
  this rule exists to prevent.

Consequence: one type in the Compose execution adapter — a project addressed by
name, which observes and removes and can never create — and the two early
returns replaced. The recorded path is untouched: a task with a session still
resolves its environment from the specification its launch recorded, because a
task's environment is the one it was launched with (ADR-033, ADR-057).
[06-technical-architecture.md](06-technical-architecture.md) states both rules
where a reader meets them. What stays open is a task archived by an earlier build
over resources nobody could see: cleanup refuses an archived task and
reconciliation skips one, so those are removable by `docker compose --project-name
feat-agent-{project}-{task} down` and by nothing in the product. It is a state no
build from here on can create, and closing it for the machines that already have
it would mean tracking a task Feat has stopped tracking.

Amended after the round-2 review traced this ADR's own edges. The evidence below
is read from the code rather than measured against Docker, which is stated
because evidence 1–6 were the other kind:

7. **The two queries this ADR exists for were the only Compose invocations in
   either adapter that named no directory.** Neither `--file` nor
   `--project-directory` nor a working directory, so `exec.Cmd.Dir` was empty
   and Compose discovered its files by walking up from whatever directory the
   daemon inherited — `feat daemon start` sets none. A user who starts the
   daemon from an application repository, which by construction holds the
   Compose files, gives every one of these queries a `compose.yaml` to find.
   Evidence 6 measured the no-file case in a directory where nothing was
   discovered; nothing measured the case where something is. What is certain
   without measuring it is that what `ps` and `down` act on had stopped being a
   question the product controls.
8. **The release rule refused on the state evidence 4 measured the removal
   succeeding in.** `Remaining` carried Docker's free-text status and no state,
   though `ps --format json` reports one and the parser already decoded it and
   threw it away. ADR-057's `feat task stop` keeps a task's containers on
   purpose, and the classes of a cleanup are independent choices (FR-CLEAN-002),
   so the ordinary morning after — a task stopped overnight, its control
   workspace cleaned up alone — was refused with "mounted into container …
   (Exited (137) 3 hours ago)". Exactly the container that had died, which is
   the case that worked.
9. **"An unanswerable question refuses" was implemented one call below the two
   places the question stopped being asked.** `agentProject` returned nil — the
   same nil that means there is nothing to ask about — when the Docker CLI was
   not on `PATH`, and when today's `agent.execution.mode` no longer said
   devcontainer. The first is a machine that cannot answer. The second is a line
   in a file the user edits, deciding what Feat asks about a container a launch
   had already created. Both left the workspace removed without anything
   established, and both left the plan naming nothing with `Archivable` true,
   which is evidence 2 reached by another route; the second also left the volume
   removal reporting a user's confirmed selection as not removed, with no error
   and no reason.
10. **One report said both things.** A task with no session was offered "clean
    it up and prepare the task again; its agent never ran, so nothing it did is
    lost", and the finding beside it named the containers its launch had left.
    The reassurance is true of a task interrupted before its container existed
    and false of the task this ADR exists for, and the record does not
    distinguish them — the answer the same pass had already fetched does.

Decisions:

- The by-name queries run from a directory Feat names, carried both as
  `--project-directory` and as the invocation's own working directory. Any
  directory Feat owns would do; what it may not be is the one the daemon
  inherited or one a project controls. The type refuses to be constructed
  without an absolute one, because an omission is the value that reinstates the
  dependency and omissions are what nobody notices.
- The release rule is about containers that have not stopped. `exited` and
  `dead` release what they mounted; every other state, and a state Compose did
  not report at all, counts as holding it — so a state nothing established comes
  out the way an unanswerable question does. `created` is in the careful half on
  that ground rather than on evidence, and is the one a measurement could move.
- "The question does not apply" may be read only from the task's own record: a
  draft, or a session recording host mode, which the domain already enforces
  carries no execution environment. Configuration is not consulted at all. A
  task's environment is the one it was launched with (ADR-033, ADR-057), and a
  session-less task is the one case where the record does not say — so it is
  asked about rather than assumed away, which is what the derived name was built
  for.
- Everything else that stops the question being asked is an error rather than an
  absence, and each caller says so in its own terms: the release rule refuses,
  the cleanup plan carries a problem and is rendered un-archivable, the volume
  removal fails rather than declining a confirmed selection in silence, and
  reconciliation records a problem.
- Reconciliation asks what a session-less task left once per pass, and every
  pass that reports on it reads that one answer. Two queries a few milliseconds
  apart are two moments, and a report is one document.

Consequence: [04-functional-specification.md](04-functional-specification.md)
FR-CLEAN-002 and [06-technical-architecture.md](06-technical-architecture.md)
state the rule as "still running" rather than "still there", and say that a
question that could not be asked refuses as an answer of "still there" would.
Two things are knowingly left. Whether Compose would have loaded a discovered
file for `ps` and `down` given a project name and no `--file` is unmeasured; the
dependency is gone either way, and the regression test is on the invocation
rather than on the answer. And `Plan.Check` refuses an archive over the targets
a plan names and not over the problems it records, so the refusal is complete on
both shipped surfaces — each reads `Archivable` — and incomplete for a client
that calls the daemon directly. That is a property of every problem source
rather than of this one, and is recorded here as open rather than closed.
