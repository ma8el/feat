# ADR-096 — Reconciliation says what it can see, and a live daemon's own work is not something to recover from

Status: accepted
Recorded: 2026-09-13, from the dogfood machine's own state directory

`Reconcile` is an API request, not a startup step. The dashboard calls it on the
`r` key (`internal/ui/app.go:1341`), on a resume (`:1498`), on a stop (`:1542`),
and after every cleanup action (`internal/ui/cleanup.go:492`, `:498`). Two places
in it assumed the opposite — that they were running at startup, in a daemon doing
nothing else — and both wrote over state the live daemon owned. They are one
mistake in two places, so they are one decision: splitting it would leave neither
half stating it.

The measurements below are from this machine's state directory on 2026-09-12: 80
tasks across six projects, 70 of which reached an agent session.

Evidence:

1. **A terminal cannot tell a running agent from an idle one, and reconciliation
   wrote its answer down anyway.** `Terminal.ProcessState()`
   (`internal/tmux/types.go:201`) returns `running` whenever the pane is not
   dead, because alive, exited, and signalled are the whole of what tmux
   publishes about a pane. The terminal pass wrote that straight onto the session
   (`internal/daemon/reconcile.go:277-283`), so a task that had correctly gone
   `idle` five seconds after its Stop hook was promoted back to `running` by the
   next pass — and stayed there, because the only path into `idle` is a *new*
   end-of-turn event arming a fresh grace period
   (`internal/daemon/agent.go:474`), and a session that has finished speaking
   sends no more. `task_reconciled idle -> running` appears **53 times** in the
   record, the most frequent event of its kind, and **13 of 70 tasks** ended
   their life showing `running`, the longest for 41 hours. This contradicts the
   rule the domain states about itself (`internal/domain/states.go:8-14`):
   process state is an observation of a world Feat does not control, and whether
   a session is running or idle is the provider's to report through its hooks.
2. **The startup grace does not cover a resume.** `reportSilentStart` returned
   early unless the task was `preparing` (`internal/daemon/agent.go:538`), and a
   resume leaves the workflow where it was — `working`, or a review state the
   task's previous life reached. Two of the three longest-stuck tasks are resumes
   whose history reads `stopped -> running`, "tmux terminal is available", and
   then not one agent event ever. The timer that reaches that function is armed
   by every launch and every resume and cancelled by the first agent event of any
   kind, so it had already established what the workflow check then discarded.
3. **The armed idle transition does not survive a restart.** The end-of-turn
   message was settled in the outbox as soon as it was applied, and the timer
   that would act on it lives in memory, so a daemon stopping inside the grace
   period — five seconds by default, `internal/daemon/launch.go:380` — lost the
   transition for good. A settled message is never read again, and evidence 1's
   promotion is what then kept the task looking busy. It is the one place in the
   agent path that did not already plan, record, then apply.
4. **A live gate was read as an interrupted one, and the checks passed into a
   task that was no longer verifying.** `reconcileReviews` treated every task in
   `verifying` as an interrupted gate
   (`internal/daemon/reconcile.go:945-969`), on ADR-036's reasoning that a gate
   does not outlive the process that started it. That is true of the process and
   false of the call. Recorded twice on 2026-09-01, in UTC:

   ```
   18:56:16  review_requested -> verifying   running 2 configured check(s)
   18:56:21  verifying -> review_requested   "interrupted by a daemon restart"   ← untrue
   18:57:37  review_state_changed            "the checks: 2 passed"   ← no transition, no notification
   ```

   `finishGate` then found the task no longer `verifying`
   (`internal/daemon/review.go:546`), recorded the results, and skipped both the
   transition and the notification — a guard evidence 9 of ADR-036 added for the
   user who approves while the suite runs, doing its job against a caller that
   should never have moved the task. The checks passed and nobody was told, and
   while they ran the dashboard said the agent had asked for review. The daemon
   already knows which gates are live: `gates.running`
   (`internal/daemon/review.go:327`) was asked only to stop a second run
   (`:399`).
5. **A gate that could not start reached the log and nobody else.**
   `gateWillRun` (`internal/daemon/review.go:707`) returned false when the
   project's configuration could not be read, so the notification policy
   announced the review request at request time as though no gate were
   configured; `beginGate` then failed on the same file, and the failure reached
   only the daemon's log. Recorded twice on 2026-09-01, while `feat.yaml` was
   mid-edit with a broken `git.branch_template`: the task sat in
   `review_requested` with no verdict, and the agent's helper waited out its
   60-second acknowledge timeout. *No checks are configured* and *Feat cannot
   tell* had been collapsed into one boolean, and they are opposite cases: the
   first is the honest occasion for announcing the request now, because there is
   no later moment; the second has one, a moment later, on the same file.

Decisions:

- **A reconciliation pass records what the terminal establishes and no more.** A
  live pane under a session already recorded in an alive state — `starting`,
  `running`, or `idle` — leaves that state alone, because the terminal has said
  nothing the record does not already hold. It still records a pane that has
  ended, which no provider event may arrive to report, and it still records
  `running` where a live pane meets a session recorded as over, which is a
  session started again in it and is what a resume is. The rule lives in
  `AgentSession.ReconcileTerminal`, in the domain, because it is a statement
  about what a terminal observation can establish and because all three callers
  — the reconciliation pass, the launch and resume path, and `AttachInfo` —
  must not answer it differently.
- **Reconcile stays a request the dashboard may make at any time.** Looking again
  is the point of the key, and making the pass startup-only would fix these
  defects by removing the feature they were found in. What was wrong is what
  looking did.
- **The startup grace covers any session that has reported nothing since it was
  launched or resumed.** The timer establishes the silence; the workflow state
  does not, and asking it was what let a resumed session sit unreported. What is
  left to refuse is an archived task, which has no terminal to attach to and
  nobody an attention state could reach. The state recorded is still the
  conservative `possibly_waiting`: Feat knows it has not heard from the agent,
  and does not know the agent is blocked.
- **A turn end is recorded on the session before it is applied, and re-armed from
  that record at startup.** `AgentSession.TurnEndedAt` is set exactly when the
  provider has reported a turn ending and nothing has been observed about the
  process since; every observation of the process drops it, because an
  observation either *is* the transition — the session went idle — or supersedes
  it, and a session that spoke without changing its process state drops it
  explicitly. A daemon that starts re-arms the grace period from the moment the
  provider reported, after the control poller has caught up on the outbox, so a
  turn reported while the daemon was down is armed by the message that reports it
  and a turn reported before it stopped is armed by the record. The field is an
  added optional one at the same snapshot schema version: a snapshot written
  earlier decodes to no pending turn end, which is the truth about a session that
  had none.
- **A task whose gate this daemon is running is not interrupted, and
  reconciliation says nothing about it.** This **amends** the reasoning ADR-036
  recorded about when a gate may be assumed dead: a gate does not outlive the
  *process* that started it, which is what its recovery was built on, and the
  pass that acts on it is a request any live daemon answers. The event a genuine
  recovery records stops naming a restart and says what was established instead
  — the checks are not running and no daemon is running them — because a gate
  this daemon released without finishing is the same state reached without a
  restart.
- **A gate that cannot start lands as `verification_blocked`.** That landing
  already exists for a run that established nothing (ADR-055), and a run that
  could not begin establishes nothing in exactly the same way: the review request
  stands where it is, the user is told because the configuration or the
  environment is theirs to fix, and the waiting agent is answered with the report
  that says the failure is not its own. `gateWillRun` now returns whether a gate
  will run *and* whether Feat could tell, and the notification policy treats "I
  cannot tell" as a gate that will speak for itself rather than as a project with
  no checks.

Consequence: `docs/04-functional-specification.md` gains what FR-STATE-003 left
implicit — that reconciliation is a request as well as a startup step, and that a
pass records only what an observation establishes — and FR-AGENT-007 gains the
sentence that makes the grace period a property of the record rather than of one
process's memory. `internal/domain` gains `TurnEndedAt` on the agent session and
the terminal-observation rule on `ReconcileTerminal`; the snapshot codec carries
the field at the same schema version. ADR-036 keeps every decision it made: the
recovery it built is the one that runs, narrowed to the case it was written for.
ADR-055's blocked landing gains a second occasion, one step earlier in the same
run. Nothing here derives `idle` from terminal output, which ADR-031 and
FR-AGENT-006 forbid and which is the opposite of the fix: what was wrong was
overwriting what the hooks had already reported.
