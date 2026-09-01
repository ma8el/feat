# ADR-085 — Plan mode travels with the initial prompt, and the plan is approved in the terminal

Status: accepted
Recorded: 2026-08-29, with the implementation

Feat launched every task the same way: the session opens, the agent reads the
brief, and it starts editing. For a task whose shape the user is sure of that is
right and it stays the default. For a task where the approach is the risky part,
the user wants to read what the agent intends before any file changes — and
Claude Code has the mode for it. The only way to get it was to type it into the
session after the session had already been told to begin.

Evidence:

1. `Prepare` already forks on one question: `req.Resume == ""` sends
   `generated.initialPrompt`, and `req.Resume != ""` sends `--resume <id>` and no
   prompt at all, because "a resumed session already holds the conversation this
   task has had, and a prompt invented here would be Feat putting words in the
   user's mouth" (ADR-037). Plan mode is a property of starting from the brief,
   so that fork is the seam and no second rule is needed.
2. A resumed session re-entered in plan mode is indistinguishable from one that
   resumed correctly: same terminal, same history, same everything, except that
   the agent refuses to edit and re-plans work the user approved an hour ago.
   ADR-037 recorded this shape for `--resume` itself — "a resumed session that
   lost its history looks identical from the outside".
3. `settings.go` states as a rule that the generated settings file carries hooks
   and nothing else, because one that also set "a model, a permission mode, or a
   tool policy would be Feat quietly deciding how somebody else's agent
   behaves".
4. The fingerprint exists because a fetch between the plan a user reads and the
   key they press can move a remote-tracking ref, and the task would start from a
   commit nobody was shown (ADR-031). It protects values that can drift
   underneath the screen: resolved bases, proposed branches, worktree paths.
5. `UpdateDraft` followed by a re-plan is a fetch and a base resolution in every
   repository — seconds, with the spinner up.
6. Measured on the machine, not assumed. A plan-first task was launched into the
   `feat` project's devcontainer and left alone until it produced a plan. Claude's
   own `UserPromptSubmit` hook reported `"permission_mode": "plan"`, which is the
   provider confirming the mode rather than Feat inferring it. When the plan was
   ready the `Notification` hook fired with
   `"message": "Claude Code needs your approval for the plan"` and
   `"notification_type": "permission_prompt"`, 27 seconds after the session
   started and while the approval prompt was on the pane. Feat recorded
   `task_attention_changed: none → needs_input`, "the agent is waiting for you
   (permission_prompt)", with the task still `working`.
7. A second plan-first task, whose brief needed no edits, produced no plan and no
   such notification: it went idle and reached `needs_input` 56 seconds later
   through Claude's ordinary `idle_prompt` notification. The two are worth
   distinguishing, because only the first is evidence about plan approval.
8. `git diff {base_commit}` is what a task's diff view runs
   (`internal/config/resolve.go`), and `git diff <commit>` compares the working
   tree against that commit. `internal/git/compare.go` counts untracked files
   separately, because `git diff <base>` never shows them. Publication requires a
   full commit per repository, verified current (ADR-070, ADR-076).

Decisions:

- **A launch-time mode is applied to a launch and never to a resume.** The flag
  goes in the branch that already carries `generated.initialPrompt`; the branch
  that carries `--resume` carries neither. That one rule handles every case
  without a second: a fresh launch plans; a resume does not; a session that never
  reported a provider identifier is a fresh launch and plans again, correctly,
  because nothing was planned and nothing was done; and a retry of a task that
  failed during launch is a fresh launch for the same reason. Stated generally as
  FR-AGENT-014, because this will not be the last such mode, and made mechanical
  by a test that pins all four vectors rather than left to a careful reader.
- An adapter whose provider has no such mode returns an error from `Prepare`
  rather than launching a session that will not honour it. Feat has just shown
  the user a screen promising that it would. This is the obligation ADR-037 put
  on `Resume`, and it lives beside it in the `Adapter.Prepare` contract.
- **It is a command-line flag, not the generated settings file.** Evidence 3's
  rule is unchanged rather than excepted. What it refuses is Feat deciding
  *quietly*: a flag passed because the user pressed a key on the screen two
  seconds earlier is not quiet, it applies to one launch, and it leaves the
  user's own `permissions.defaultMode` untouched for every task where they did
  not press it. The command line is where a per-launch decision belongs; the
  settings file is for standing ones, and Feat still has none to write there.
- **It is not a configuration key, project or global.** That would be a default
  for a decision that is per-task by nature. ADR-078 stopped the wizard asking a
  question whose answer was nearly always the same; ADR-079 put what is true of
  the machine in one settings file with no per-project override. Plan-first is
  neither. Revisit it with evidence that people set the same value every time,
  rather than in anticipation of it.
- **The confirmation carries it, and the fingerprint does not cover it.**
  `api.Service.LaunchDraft` takes a `Confirmation` rather than a bare
  fingerprint, which is where the second of these options goes when there is one.
  A value that arrives in the same request that confirms cannot have drifted
  since it was displayed, so ADR-031's rule that what is created is what was
  shown holds by construction rather than by checking — and covering it would
  refuse a plan resolved before the user made their mind up, for a value evidence
  4 does not describe. Evidence 5 is the other half: a toggle that re-planned
  would put a network call behind a key that changed nothing about where the task
  starts.
- **It is recorded on the task even though it is consumed once, moments later.**
  `confirmDraft` sets it while the task is still a draft, and it freezes with the
  brief and the bases. `confirmDraft` creates the worktrees and leaves draft;
  `planLaunch` then builds the session, and a failure between them leaves a
  `failed` task the workflow resumes from. A retry re-reads the task, so a mode
  held only in the request that started the first attempt would launch a session
  the user did not ask for with nothing to say why. Plan, record, then apply.
  `plan_first,omitempty` is additive at the same schema version: an older
  snapshot decodes to false, and false is the old behaviour.
- It is not published on `api.Task`. Nothing renders it after launch — the
  terminal shows what the session is doing far better than a field would — and
  leaving it off keeps the API goldens and every client untouched. Add it the day
  something needs to draw it.
- The adapter reads `req.Task.PlanFirst` and gets no field of its own on
  `agent.PrepareRequest`. The task is already in the request, and `domain.Task`'s
  `Attention` comment gives the reason a second copy is refused: it would be a
  second source of truth for the same question. `Gate` and `Publication` are
  fields there because they are derived from configuration and have nowhere else
  to live; this has somewhere else to live.
- **Feat is told when the plan is ready, and the screen may say so.** Evidence 6
  settles what was an open question: the `Notification` hook fires for the
  plan-approval prompt, so the task reaches `needs_input` and the dashboard flags
  it with no new code. What Feat does not get is a semantic signal that a plan was
  *accepted* — attention returns through the ordinary path when the session speaks
  again. Approval itself stays in the terminal, between the user and Claude, which
  is FR-AGENT-003's consequence rather than a gap.

Not built, and why:

- **A `planning` workflow state.** It needs a signal that the plan was accepted,
  and the only one Claude offers is a `PostToolUse` hook matched on
  `ExitPlanMode`. `settings.go` installs no tool hooks on purpose — "they fire
  many times a turn and tell Feat nothing about the task's state that these six do
  not" — and `ExitPlanMode` is the rare one that fires once and means something,
  so it is a defensible exception rather than an impossible one. It is not this
  change: a new workflow state is new transitions, a new event kind, and a
  dashboard that has to explain a fourth thing a task can be. Recorded so it is
  reopened deliberately.
- **A commit-behaviour option**, proposed alongside this one. The case for it was
  that a task's diff view goes empty once the agent commits, so an option to keep
  work uncommitted would fix a real pain. Evidence 8 says it would not, and the
  three facts belong here because someone who has just seen an empty diff will
  propose it again: `git diff <base>` shows committed, staged, and unstaged work
  alike, so the panel does not go blank when the agent commits; `git diff <base>`
  never shows untracked files, so the reproducible empty diff in the product today
  is an agent that creates new files and never commits them, which committing is
  what fixes; and publication requires a full commit per repository, so a task
  that never commits cannot finish. Any future commit option starts from those
  three rather than from the empty-diff premise. Review commands are
  settings-only with no per-project override (ADR-079), so no configuration
  reaches the diff command in any case.

One known and accepted rough edge: plan mode restricts what the agent may run, so
the generated `feat-report` helper may be refused mid-plan — `open_question` in
particular, which is exactly what an agent wants while planning. The instructions
file is not changed for it. The user is at the terminal by construction, an
unapproved tool call surfaces there as a prompt rather than as a silent failure,
and rewriting the protocol document around a mode it is not about would cost more
than it saves.

Consequence: one field on `domain.Task` and one on `taskDocument`, a
`Confirmation` on the service interface, one key on the review step, one flag on
`feat implement`, and two lines in the branch of the Claude adapter that already
forks. No new package, no new endpoint, no new dependency, no new workflow state,
no configuration key, and no schema version. `--permission-mode` was verified
against the installed Claude Code 2.1.236, whose help gives its accepted values
as `acceptEdits`, `auto`, `bypassPermissions`, `manual`, `dontAsk`, and `plan`;
the version constants in `version.go` still record 2.1.220, because that is the
build the hook payloads were read from and nothing has re-read them.
