# ADR-037 — Quarantine, recovery, and what cleanup is allowed to resolve

Status: accepted
Recorded: 2026-08-07, before implementation

Evidence found while planning reconciliation and cleanup. Items 1 to 5 are
properties of code this repository already has:

1. `tmux.Discover` fails for the whole server when any tagged object is
   inconsistent. `parseSessions`, `parseWindows`, `parsePanes`, and `assemble`
   each return `nil, err`, so one task window whose agent pane was killed while
   its shell pane survived makes `EnsureTask` fail for every unrelated task and
   stops startup reconciliation before it reaches any task at all. ADR-030
   recorded this and deferred the answer here, because worktrees, Compose
   projects, and control messages raise the same question.
2. `CommandSpec.Validate` checks its program and arguments against NUL and
   newline, and checks only that the working directory is absolute. Discovery
   reads tab-separated formats and the directory is the one caller-supplied
   value tmux reports back, as `#{pane_current_path}`. A tab in a path therefore
   misaligns every pane field and breaks discovery for every terminal — the
   blast radius of evidence 1, reached through a different door. `safeArgument`
   does not reject a tab either.
3. `control.Workspace.Pending` already returns what it could read and what it
   could not, as `([]Message, []error, error)`, and reports a whole-directory
   failure separately. It is the shape evidence 1 is missing, and it is already
   in this repository rather than a design to invent.
4. `internal/git/cleanup.go` produces an exact inventory with per-target
   warnings and has had no production caller since slice 4. Nothing in it
   removes anything, and `CleanupPlanFor` already refuses a recorded path that
   is not inside the root Feat owns.
5. Slice 7 records `AgentSession.ProviderSessionID` from the session-start event
   before the process can fail, and `applyAgentEvent` suppresses the workflow
   change of a *continued* session start so that a user typing `/clear` does not
   move their task. The workflow table allows `failed` to reach `preparing`.
6. Measured against the installed Claude Code 2.1.220, in a real terminal inside
   a tmux 3.7b pane: `claude --resume <unknown-id>` prints
   `No conversation found with session ID: …` and exits 1. It does not open the
   interactive picker its `--help` describes for a bare `--resume`. So a resume
   that cannot find its session fails where somebody sees it, rather than
   producing the ADR-032 evidence-4 shape — a session that starts perfectly and
   has been given nothing.
7. `docker compose down --volumes` is all or nothing. It removes the volumes
   declared in the project's own Compose files, which is not the same set as the
   volumes a plan enumerated and named.
8. ADR-027 deferred `daemon.json` to "the first slice that reads a durable
   daemon record", and named this one. But no slice 12 acceptance criterion
   needs it: task identity survives a restart through task snapshots and tmux
   metadata, neither of which is a daemon record. Writing it here on the
   strength of the deferral alone would produce exactly what ADR-027 evidence 2
   refused — a versioned compatibility surface with no reader.
9. `docs/04-functional-specification.md` FR-CLEAN-002 names four destructive
   classes: containers/networks, volumes, worktrees, and branches. A task also
   owns a tmux window and a control workspace, and FR-CLEAN-001 requires the
   inventory to be exact.

Decisions:

- **Quarantine is one rule, stated for every resource class rather than for
  tmux.** An enumeration returns what it could read together with what it could
  not, and fails as a whole only when the enumeration itself failed. Evidence 3
  is the precedent and is named as one rather than left as a coincidence:
  `Pending` has behaved this way since slice 7. `tmux.Discover` returns a
  discovery carrying terminals and damaged entries. A damaged pane quarantines
  the terminal it belongs to, because half a terminal is not one; a damaged
  session quarantines its windows for the same reason. Everything else stays
  usable, which is acceptance criterion 5.
- Working-directory validation is settled with it, as ADR-030 said it would be:
  `CommandSpec.Directory` is checked with the same rule as the arguments, and
  that rule gains the tab. Quarantine bounds the damage a bad directory does;
  validation stops Feat creating one. Both are wanted, and neither replaces the
  other.
- `internal/reconcile` is a policy package under the boundary ADR-029 set for
  Git and ADR-036 for review, with a `reconcile-stays-a-policy` depguard rule.
  It owns the vocabulary of a finding, the cleanup classes, the plan token, and
  the consent rule. It reads no configuration and no persistent state, and it
  drives no adapter: the daemon observes, and this package decides what the
  observations permit.
- **Reported, never adopted.** Missing, orphaned, inconsistent, and damaged
  resources appear in a report and change nothing. Nothing in reconciliation
  starts a container, restarts a process, removes a directory, or adopts an
  orphan, which is FR-STATE-004 generalised from containers to every class.
- **The cleanup token names what, and the fresh observation decides whether.**
  The token covers the task, the resource identities, and the schema — not the
  warnings. A token computed over observations would expire whenever the agent
  wrote a file, so a user would learn that their plan was stale rather than that
  their worktree was dirty. Execute therefore re-resolves the plan, refuses a
  token that no longer names the same resources, and refuses a selection whose
  currently observed warnings the confirmation does not cover. A stale
  confirmation is the failure this rule exists to prevent; a refused one is an
  inconvenience.
- **Seven classes, each an independent choice.** The four FR-CLEAN-002 names,
  with the agent's containers and the application's kept apart because they are
  separate concepts everywhere else in this product, plus the task's tmux window
  and its control workspace, for evidence 9. A dead window and an audit trail
  that no cleanup can ever remove are resources a task owns and Feat cannot
  resolve, which is the thing FR-CLEAN-001 forbids. That they are an extension
  of the specification's list rather than in it is recorded here rather than
  discovered later; [04-functional-specification.md](04-functional-specification.md)
  moves in the same change.
- **Volumes are removed by name, never by `--volumes`.** They are enumerated
  from the container runtime's own project label and removed one at a time, for
  evidence 7: a plan that says exactly what will go and a command that removes
  exactly that is what "resolve the exact task-owned resources" means. It also
  makes the external-resource rule structural — a resource the project declares
  external carries no task label and cannot appear in the enumeration, so no
  code path has to remember to exclude it.
- `execution.Destroy` arrives as the method ADR-033 deferred, and removes
  containers and networks only. Volume removal is a separate method on both
  Compose adapters rather than a flag, so that "volumes are retained by default"
  is a shape of the interface rather than an argument somebody can pass wrongly.
- **Archiving is refused while the plan still names resources the user did not
  select.** An archived task is one Feat stops tracking, and stranding a running
  container behind it would manufacture precisely the orphan this slice exists
  to report. Archiving itself needs no new stored document: the task reaches
  `archived`, its snapshot keeps the branches, bases, and session it recorded,
  and the append-only event log carries what each class removed. So "Feat can
  explain what happened later" is satisfied by the two durable records slice 1
  already built, and no stored format changes.
- **Recovery of a dead agent session is an offered action and resumes the
  recorded session.** It re-plans the launch and passes the provider session
  identifier through a neutral `Resume` field, which the Claude adapter turns
  into `--resume <id>` with no initial prompt: a resumed session already holds
  its history, and inventing a prompt would be Feat putting words in the user's
  mouth. Evidence 6 is what makes this safe to offer — the failure is visible.
- Evidence 5's suppression narrows rather than disappears: a continued session
  start does not move a task **that is already working**. The rule was written
  so that `/clear` could not move a task's workflow, and that reason does not
  reach a task in `preparing`. Without the narrowing a resume would go
  `failed → preparing` and stop there, leaving a task that looks broken while
  its agent is running — so the resume takes the ordinary launch path,
  `preparing` to `working`, with slice 7's own silent-launch safety net behind
  it.
- A resume brings up a devcontainer, which is not FR-STATE-004's forbidden
  automatic restart: it is what a user asked for. The rule is kept structural by
  making the resume unreachable from reconciliation, and the report says a
  container will be started before the user commits to it.
- **`daemon.json` is written because it has three readers, not because ADR-027
  named this slice.** It records the state directory's own schema version, so a
  build older than the directory refuses it instead of overwriting documents it
  does not understand; whether the previous run ended cleanly, so a report can
  say a daemon crashed rather than leaving the user to infer it from an
  interrupted gate; and when it stopped, which is how long Feat was not looking.
  It carries no process identifier, socket, or lock — those stay in the runtime
  directory, which is the whole of ADR-027 evidence 1, and a durable record that
  acquired one would reintroduce the reused-identifier bug that decision
  prevents. Had these readers not existed the record would have been deferred
  again rather than written unread.
- The local API gains `GET`/`POST /v1/reconciliation`, the two cleanup endpoints
  [06-technical-architecture.md](06-technical-architecture.md) already lists,
  and `POST /v1/tasks/{task_id}/resume`. The endpoint list moves in the same
  change. `POST` on the reconciliation path re-runs the pass and records what it
  observed, which is the shape slice 9 used for runtime status and slice 11 for
  review observation.
- `feat cleanup <task>` has no blanket `--yes`. FR-CLEAN-002 requires separate
  choices and FR-CLEAN-003 explicit confirmation, and one flag that answers
  every question is the thing both rules exist to refuse. In a terminal it asks
  once per class in increasing order of risk and again for each warning;
  anywhere else it prints the inventory and removes nothing, which is the split
  ADR-027 made for `feat` and ADR-036 for `feat review`.

Consequence: the user-visible additions are `feat cleanup`, a TUI cleanup
screen, a resume action on a failed task, a recovery report, and five endpoints.
The command surface gains no flag, so its golden file is untouched. One stored
document is added and none changes, so no migration is needed; the event
vocabulary gains the additive types a removal and a recovery finding are
recorded as.

These decisions are recorded before implementation; evidence found while
implementing that contradicts one of them amends this ADR in the same change,
per the decision change process below.

Amended after running the slice against the real binary. Three defects, and none
of them was reachable from this repository's own fixtures:

10. **A daemon that shut down cleanly could never start again.** The claim
    carried the previous run's stop time forward into the new run's record, so
    the record described a run whose stop preceded its own start — which
    `DaemonRecord.Validate` refuses, correctly. Only a daemon that had *crashed*
    could start, because a crashed run leaves no stop time to carry.

    The suite missed it for a reason worth recording: every fixture in
    `internal/daemon` freezes the clock, so the carried stop and the fresh start
    were the same instant and the invariant held. The first regression test
    written for it passed against the injected defect. A daemon that is stopped
    and started has a clock that moved, and the test now moves one.

    Decision: a record describes one run — its start, and its stop once it has
    one. What the previous run left is read once when the state directory is
    claimed and held in memory for that run's reconciliation, because claiming
    replaces the record on disk with this run's own. The invariant stands
    unchanged; it was right and the writer was wrong.

11. **A live task's own directory was reported as an orphan a user should
    delete.** With a worktree root of `…/worktrees/{project_id}/{task_id}`, the
    directories the orphan scan listed were task directories rather than
    worktrees, and comparing only for equality found nothing claiming them. The
    report said "a directory under the project's worktree root that no task
    records" and advised removing it if it looked stale — about the directory
    holding both of a running task's worktrees.

    It is the worst shape a false positive can take here, because the product's
    whole discipline is that Feat reports and the user acts: a report that
    recommends the wrong deletion turns that discipline against the user. The
    same scan also missed the orphan it existed to find, since an abandoned task
    directory sits at the same depth.

    Decision: the scan descends only where a task's own paths lead. A directory
    that holds something a task records is walked into rather than reported; one
    that holds nothing is reported. The walk is bounded by the template's depth
    rather than by a number somebody chose, and it works for a root whose
    worktrees are direct children as well as for one whose are not.

12. **The plan token did not cover which repository a target belongs to.** One
    branch template gives every repository of a task the same branch name, so a
    two-repository task's inventory printed the same branch twice — which is what
    made it visible. A token over the name alone cannot tell a plan naming one
    repository's branch from one naming another's, while the removal is pointed
    at a repository by exactly the field the token omitted.

    Decision: the repository is part of what a target is, and the token covers
    it. Nothing else changes; the plan already carried the field, and the
    removals already used it.

13. **The one task that most needed recovery was the one that could not have
    it.** Resuming transitioned unconditionally to `preparing`, and the ordinary
    state of a task whose agent died is not `failed` — it is `working` with a
    failed process. A process that dies while no daemon is watching leaves the
    workflow where it was, and reconciliation reports the dead process rather
    than moving it, because reporting instead of repairing is the whole rule. So
    `working` is the state a resume mostly meets, and `working` has no edge to
    `preparing`, correctly: the work did not go back to being prepared.

    Found by resuming a real task whose devcontainer had been killed a day
    earlier; it failed in fifteen milliseconds with a generic message, before
    touching Docker. The fixtures all reached the resume from `failed`, because a
    test that arranges a dead agent naturally arranges the whole of it.

    Decision: only a `failed` task is moved to `preparing`, and only a task this
    call moved is moved back when the launch fails. A task that was already
    working stays working — its agent is dead either way, and a failed resume is
    not new information about the work. `ensureTerminal`'s confirmation gate
    admits a restart for the same reason, while still refusing a draft and an
    archived task.

14. **The recovery band could never be brought up to date.** Reading the last
    pass and running a new one are deliberately different requests, because a
    pass asks the container runtime about every task and the dashboard refreshes
    every two seconds. But nothing in the dashboard ever ran one: the periodic
    refresh and the refresh key both re-read, so the band went on describing the
    pass that ran when the daemon started. A user who resumed a task or cleaned
    one up was still being told about resources they had just dealt with, and
    the only way to clear it was to restart the daemon.

    Reported by the maintainer from the dashboard itself, which is the only
    place it is visible: every test asserted the band's *content* against a
    report handed to the model, so none of them could notice that no report ever
    arrived twice.

    Decision: the split stands — the periodic refresh still only reads. What
    changes is that the two actions which resolve a finding, a resume and a
    finished cleanup, run a pass, and so does the explicit refresh key, because a
    user who pressed it is asking for what is true now and the cost is theirs to
    spend. The band also says when it looked, since everything it names can be
    acted on from the screen it is on: one with no time on it reads as current
    however old it is.

15. **Every cleanup left a directory behind, and the recovery pass asked the
    user about it.** A worktree path is generated — `…/worktrees/{project_id}/{task_id}/{repository_id}`
    by default — and preparing a task creates the directories above the
    worktree; `git worktree remove` removes the worktree and, correctly, nothing
    above it, because Git did not create those. So a confirmed cleanup left an
    empty `…/worktrees/{project_id}/{task_id}` on the machine for every task ever
    run, and evidence 11's scan then reported each of them to whoever still had a
    live task in that project: an orphan "no task records", with advice to look
    at it and remove it if it was stale.

    Reported by the maintainer from the dashboard, which is where it is visible:
    the recovery band counts findings, so the residue of ordinary use accumulated
    into a permanent warning marker. Nothing in the suite could see it — every
    fixture asserts on the worktrees, and the directory holding them was nobody's
    assertion.

    The same report named a second directory: `orphaned worktrees
    …/worktrees/jobharbor-dev`, the directory the root gives one project, about a
    project whose tasks had all been cleaned up. That one is not residue at all.
    Feat generates it, creates it for the project's first task, and creates every
    later task inside it, so a project between tasks has one exactly as a busy
    project does — and the scan could not tell it from a leftover because it
    learned which directories were Feat's from the *tasks*, and this project had
    none.

    Decision: a worktree root generates three kinds of directory, and each is
    treated differently. The fixed leading prefix is shared by every project and
    is never touched. The directory between it and a task — `{project_id}` under
    the default root, resolved by `config.ProjectPrefix` — belongs to the
    project: it is not removed with a task, and it is never reported, because a
    project with no task right now is not a leftover. What a task was given is
    removed with the task: removal is the mirror of creation, so `RemoveWorktree`
    walks up from the worktree it removed and removes each directory that is now
    empty, stopping at the project's directory and at the first directory that
    still holds something — another task's worktree, a repository the task no
    longer records, or a file the user put there. Each directory that goes is
    reported with the worktree, so the event log and `feat task cleanup` account
    for it.

    The orphan scan therefore reads the registered projects rather than only the
    tasks: what Feat manages here is a question about the projects, and asking the
    tasks answered a different one. Nothing else about reconciliation changes —
    it still removes nothing, and a directory that really is nobody's is still
    reported, including a stale task directory inside a project's own. What
    changed there is one sentence: an orphan that is empty is named as empty and
    given the command that clears it, because the machines that have this residue
    already will keep it until somebody removes it by hand.

16. **The two generated-input roots are the same defect as evidence 15, at the
    two directories that walk does not reach.** The maintainer read
    `~/.local/share/feat` and asked which of its per-project trees cleanup
    actually removes. The answer is two of five: `ClassControl` removes
    `control/<project-id>/<task-id>/` and `ClassWorktrees` removes the worktree
    and the directories above it, `projects/` is kept on purpose, and
    `execution/` and `runtime/` are removed by nothing — a grep for every
    `os.Remove`/`os.RemoveAll` in the tree finds `internal/control/workspace.go`
    and `internal/git/remove.go` and no third place. On the dogfood machine, 47
    of the 48 directories under `execution/feat/` belonged to tasks that were
    already archived, and 4 of the 6 under `runtime/jobharbor/` did.

    ADR-033 and ADR-034 placed those roots and said what goes in them. Neither
    says anything about their lifetime and neither adds a class, because both
    arrived after the class list was fixed: this is an omission rather than a
    decision. Each directory holds one generated file, so the residue is 240K and
    52K rather than anything a machine notices, which is why it survived the whole
    dogfood unseen.

    Decision: evidence 15's rule, at both roots. A task's generated Compose
    document is what its Compose project is defined by and what the destroy is run
    against, so it is removed with that project — the execution directory with the
    agent's containers, the runtime directory with the application's. After the
    destroy and never before, since for the application runtime that directory is
    also the working directory of every Compose command Feat runs for the task.
    The walk stops at the project's own directory for evidence 15's reason. It is
    reported with the class that removed it and is not a target of its own, again
    for evidence 15's reason: it is where a resource was defined, not a resource
    the user chooses about.

    The one place evidence 15 does not carry over is what a failure does. That
    rule — "nothing here fails a removal" — rests on reconciliation naming the
    directory that was left, and nothing scans these two roots. So a removal that
    fails fails the class, naming the path; cleanup re-resolves its plan and is
    re-runnable, and a silent failure would recreate exactly the situation this
    entry reports. Removing both directories on archive instead was rejected: it
    reaches the same directories in practice, but it ties a generated document's
    lifetime to a record's rather than to the resource it defines, and it leaves
    the document behind for a user who cleans up without archiving.

    One residual, accepted: a launch that wrote the override and then died before
    recording a session, whose containers were later removed by hand, has no
    `agent_containers` target for the class to be offered under, so its directory
    stays. Everything that reaches the class is covered — for a recorded session
    the target is added whether or not the containers are still present — and
    closing the last case would mean making a generated file a target, which is
    what this decision declines to do. As with evidence 15, the machines that have
    this residue already keep it until somebody removes it by hand.
