# ADR-031 — Task drafts, confirmation, and the first user-facing task lifecycle

Status: accepted
Recorded: 2026-08-06, before implementation

Evidence found while planning task preparation and the initial TUI:

1. The slice 6 acceptance criterion "confirming launches the previously displayed snapshot" is not satisfied by planning at confirmation time. ADR-029 made preparation plan, record, apply in one call, and a `remote` base policy fetches inside the plan. Between the screen the user reads and the key they press, a fetch can move a remote-tracking ref, and the task then starts from a commit nobody was shown. The failure is silent, which is what makes it worth designing against rather than documenting.
2. [06-technical-architecture.md](06-technical-architecture.md) names `POST /v1/task-drafts`, `PUT /v1/task-drafts/{draft_id}`, and `POST /v1/task-drafts/{draft_id}/launch` without saying what a `draft_id` is, and lists no way to abandon a draft. A draft that cannot be cancelled is a record that accumulates, and criterion 1 is about cancelling one.
3. The Claude adapter arrives with slice 7 and devcontainer execution with slice 8, but slice 6 must connect the attach and shell actions, which need a live pane. Nothing in slice 6 can start an agent.
4. `domain.WorkflowWorking` is documented as "a task with a running agent session". A launch that reached it while the pane held a shell would record a claim about the world that is not true, which is the same defect shape ADR-026 pinned the transition table against.
5. FR-TASK-003 requires the draft to show resolved bases, proposed branches, worktree paths, and an editable brief. Resolving a base needs Git, and Git runs in the daemon, so a draft cannot be a client-side object that is posted once at the end.
6. `internal/ui` is denied `os/exec` by the `process-execution-stays-in-adapters` rule, and the three things the dashboard must launch — native tmux attach, a task shell, and `$EDITOR` — all have to take over the terminal Bubble Tea owns.
7. A draft's shape is exactly what `Task` already makes mutable in `draft` and frozen afterwards (ADR-026). A second draft entity would duplicate the repository binding, the base resolution, and the brief, and then have to agree with the first one.

Decisions:

- A draft is a task in `draft` state, and `{draft_id}` is its task identifier. Drafts are persisted, so several drafts and live tasks coexist across a daemon restart, and they appear in the task list as the drafts they are.
- Preparation becomes plan, confirm, apply. `POST /v1/task-drafts/{id}/plan` resolves every base and records the proposal on the draft while leaving it `draft`; `POST /v1/task-drafts/{id}/launch` carries a fingerprint of what was displayed and refuses to launch anything else. This amends ADR-029, which recorded that slice 6 would confirm a draft by calling `PrepareTask`; the plan, record, apply ordering that ADR-029 chose for recoverability is unchanged, and only the point at which the user's confirmation enters it moves.
- The fingerprint covers the task's frozen shape: title, brief, source, and each binding's repository, access, base ref, base commit, branch, worktree path, and container path, in canonical order. It is computed from the stored task rather than stored beside it, for the reason ADR-026 derived the task key from the task identifier: two records of one fact can disagree. No stored format changes, so no migration is needed.
- A mismatch is reported and never resolved. Feat does not silently re-plan, for the same reason ADR-029 does not silently rename a colliding branch: the user would act on a plan they never saw.
- Planning is its own request because it fetches. A network call against the user's repositories should follow a key they pressed, not a field they edited.
- Cancelling a draft archives it. `draft` to `archived` already exists and `Task.notReadyFor` already exempts archived, because a cancelled draft never had a brief, a base, or a session. Nothing is removed from disk; slice 12 owns archival storage.
- Launch opens the task terminal with the user's `$SHELL` in the primary task worktree and leaves the task `preparing`. The pane is real, so attach and shell are genuinely connected and slice 5's creation seam gets its production caller, and `preparing` to `working` remains the edge slice 7 takes when Claude actually starts. A devcontainer-mode project gets the same host shell, labelled as one: the honest report is more useful than refusing the action, and slice 8 replaces it.
- Fields the dashboard cannot fill yet — resource usage from slice 10, verification state from slice 7, change counts beyond the recorded observation — render as absent and name the slice that delivers them. This is the rule ADR-028 established for `feat doctor`: a value that was never measured is never displayed as one.
- The TUI hands native processes to `tea.Exec` as a `tea.ExecCommand`, which `internal/cli` constructs. `internal/ui` names no `os/exec` type, so the boundary rule stays mechanical rather than becoming an exemption.
- `feat implement` gains `--project`. The picker still appears when several projects are registered and no flag is given, and the flag pre-fills rather than making the command headless: confirmation is required before anything is created, so a terminal is required. The command surface changes as it did in ADR-028, and [README.md](README.md) and the golden file move in the same change.

Consequence: slice 6 is the first slice with a user-facing task lifecycle, so the event stream, the store, the Git adapter, and the tmux adapter all gain production callers at once. Slice 2's remaining structurally verified criterion — that state events arrive in order — becomes fully behavioural, because a draft created, planned, launched, and attached publishes the sequence a client reads.

Slice 6 starts no agent. Anything that would report one — a `working` task, a Claude session identifier, a control workspace — is absent rather than approximated, and slices 7 and 8 add them.

The Slice 3 target-machine acceptance check remains outstanding, as it did for slice 5. Slice 6 proceeds under the same explicit maintainer approval, recorded here rather than by marking slice 3 complete without its evidence.
