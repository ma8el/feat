# ADR-036 — Review comparisons, external commands, and where a completion gate can honestly interrupt an agent

Status: accepted
Recorded: 2026-08-07, before implementation

Evidence found while planning review and the completion gate. Items 1 to 3 are
properties of code this repository already has, and each of them decides
something this slice cannot take back later:

1. `api.NewVerification` labels every verification `agent`. Its loop reads
   `check.Reporter == provider && verification.Source != agent`, and `Source` is
   initialised to `agent` and written nowhere else, so the condition is
   unreachable and the comment above it describes a rule the code inverts. It
   produces the right answer today because every check is an agent's claim.
   Slice 11 is the first slice that produces a provider-gated one, which is what
   turns dead code into acceptance criterion 5.
2. The workflow table has no way out of a failed or an interrupted gate.
   `verifying` reaches only `ready_for_review`, `verification_failed`, and
   `failed`; `verification_failed` reaches only `working`, `changes_requested`,
   and `failed`. So an agent that is handed a failing check, fixes it, and asks
   for review again produces no transition at all, and a daemon that restarts
   while checks are running leaves a task in `verifying` for ever.
3. `agent.HostRunner` bounds every command at twenty seconds. That is right for
   a probe asking whether `glab` is authenticated and wrong for a test suite, so
   a configured `execution: host` check cannot use it.
4. [06-technical-architecture.md](06-technical-architecture.md) describes the
   gate as "provider-native completion hooks". Claude's `Stop` hook is the hook
   that can block, and it fires at the end of **every** turn — so a gate built
   on it either runs the project's suite whenever the agent stops speaking, or
   needs shell logic to work out whether a review request is outstanding.
   ADR-032 made every generated hook inert for a related reason: a hook that
   prints changes the model's context and a hook that exits 2 stops the session
   it exists to observe.
5. The agent's outbox is agent-writable by design, so any result delivered
   through it is a result the agent could have authored. A "gated" label on such
   a result would be Feat claiming enforcement it did not perform, which is the
   distinction `CheckReporter` exists to keep.
6. `domain.Check.Detail` is documented as never carrying a secret, "because
   review state reaches the dashboard and the event stream". A failing test
   prints whatever the project's own program prints, and that is the one thing a
   person reviewing a failed check needs to see.
7. `git.ChangedFiles` counts untracked files as well as tracked ones, and
   `git diff --numstat` cannot report line counts for a file Git has never been
   told about without writing to the index — which every observation in
   `internal/git` deliberately avoids through `--no-optional-locks`.

Decisions:

- The gate is triggered by the explicit review request and never by an end of
  turn. Evidence 4 rules out the `Stop` hook, and the trigger this leaves is the
  one signal that already means what the gate is about: an agent that says its
  work is ready.
- The **daemon** runs the checks, for evidence 5. A result recorded as
  `provider` is therefore evidence Feat collected by running a configured
  command itself, and the agent cannot author one. This is what makes the second
  half of acceptance criterion 5 a property of where the code runs rather than a
  label.
- The failure returns to the native agent loop through the **exit status of the
  helper the agent invoked**. The agent asks for review by running the generated
  `feat-report review_requested`; that helper now waits for the daemon's verdict
  and, when a check failed, prints the failing checks and a bounded excerpt of
  their output to standard error and exits non-zero. The model reads a failed
  tool call and carries on in the same turn, which is as native as the loop
  gets, and it needs no provider-specific blocking semantics — so the mechanism
  survives a second provider adapter. [06-technical-architecture.md](06-technical-architecture.md)
  and [02-user-workflows.md](02-user-workflows.md) are corrected in the same
  change rather than left describing a hook.
- The helper is the only generated script that waits, and it is not a hook.
  ADR-032's rule stands untouched: the six hook scripts still write a file and
  exit 0, and nothing that observes a session can block one.
- The verdict is written to the task's **inbox**, which gains its first writer.
  It is a versioned line-oriented document — `feat-verification 1 <status>`,
  then the report — rather than JSON, because the only thing that reads it is a
  shell script, and ADR-032's reason for keeping parsing out of generated
  scripts applies to reading exactly as it does to writing.
- Two workflow edges are added for evidence 2, and each carries its reason in
  the table: `verification_failed → review_requested`, so an agent that fixed
  what the gate caught can ask again; and `verifying → review_requested`, so a
  gate interrupted by a daemon restart returns the task to where it was rather
  than stranding it. The pinned transition test moves with them, and neither
  edge reaches a review state without an agent having asked: `TestIdleIsNotCompletion`
  still holds.
- Each check runs where its `execution` field says: `agent` inside the task's
  execution environment, `host` on the trusted host in the task worktree. The
  field has had no reader outside `feat doctor` since slice 3, and a
  configuration value nothing acts on is a promise the binary does not keep.
  `internal/review` owns the host runner, with the gate's bound rather than the
  probe's (evidence 3).
- Only repositories a task bound **read-write** are checked. A read-only binding
  holds code the task cannot have changed, and running its suite would spend
  minutes to learn nothing; the checks are recorded as `skipped` naming that
  reason, because a check that did not run is never absent.
- The bounds are fixed constants, not configuration: thirty minutes for one
  check, sixty for a whole gate run, sixty seconds for the helper to be
  acknowledged and sixty-one minutes for it to be answered. A check that exceeds
  one is recorded as timed out with the bound named, and never as a failure it
  did not have. This is the argument ADR-032 made for the startup grace: the
  value bounds how long Feat will wait while knowing nothing, and any value
  comfortably beyond a working case serves equally well.
- A check's output is stored as a bounded excerpt in `Check.Detail`, shown on
  the review screen, and never put into an event payload. *Narrowed in use: a
  check Feat ran and that passed shows no output on the panel, because its
  excerpt is the whole of a build command's stdout printed under a line that
  already said "passed" — forty lines of `ok <package> (cached)` on this
  repository. It is still stored and still bounded; what changed is one panel's
  display rule, not where the value may travel, which is what this bullet was
  answering. A failure, a skip, and a check that did not report all keep their
  detail: those are why, and Feat's own account of it.* Evidence 6 is resolved
  by narrowing where the field may travel rather than by adding a field, which
  would change a stored format that has carried every field this slice needs
  since slice 1. The excerpt is the output of the user's own program shown to
  its owner, which is the same class of thing as the Compose logs Feat opens
  wholesale; what the security model forbids is copying secrets into generated
  documents and event payloads, and neither happens here.
- `internal/review` receives final values and expands nothing, under the rule
  ADR-029 established for Git: the daemon expands review and check templates
  with `config.Expand`, because the placeholder vocabulary belongs to
  `internal/config`. A `review-stays-a-policy` `depguard` rule makes it
  mechanical; ADR-025 requires an ADR for a boundary rule, and this is that
  record.
- An expanded command is refused unless its working directory is one of *this*
  task's recorded worktree paths, and that path passes the same safety check
  `internal/paths` applies to a directory Feat would later remove. Acceptance
  criterion 3 is therefore a property checked in one package against one list,
  rather than a rule spread over the three commands that happen to exist today.
- The external commands are run by the client, as `feat attach` runs tmux and
  `feat runtime logs` runs Compose: they take over the caller's terminal, and
  the daemon has neither the terminal nor the user's `$EDITOR`. The client
  checks what it was handed before running it — the same user, but a client that
  ran whatever it received would be one nobody could reason about.
- `POST /v1/tasks/{task_id}/review/{action}` carries `observe`, `approve`,
  `changes`, `pending`, and `verify`, and the endpoint list in
  [06-technical-architecture.md](06-technical-architecture.md) gains the four it
  is missing. It is the shape slice 9 used for the runtime: one action per thing
  a user asks for, and `observe` is a POST because it observes and records what
  it observed.
- `verify` exists as a user action because it is what makes an interrupted gate
  recoverable by hand, which is this product's idiom everywhere else: recovery
  is offered and never automatic. Slice 12 still owns the reconciliation pass
  that finds such a task at startup.
- Approval decides the work and touches nothing else. No review action starts,
  stops, or destroys a runtime, which is checked by counting the commands an
  approval produces rather than by asserting that a recorded state did not
  change — and it is what finally exercises the offer slice 9 rendered for an
  approval that had not been implemented yet.
- Insertions and deletions cover tracked changes only, for evidence 7, and the
  screen says so beside the file count rather than presenting one number derived
  from two definitions. It is the same choice ADR-035 made about load average:
  one figure that means what it says beats two that look alike.

Consequence: the user-visible additions are the review screen, `feat review`,
four endpoints, and a gate that can move a task through `verifying` to
`ready_for_review` or `verification_failed` — the two states no build has reached
until now. The command surface does not change, so its golden file and the
README's command list are untouched, and no configuration field is added. No
stored format changes and the event vocabulary gains nothing, so no migration is
needed.

`verifying` and `verification_failed` stop being unreachable, which also gives
the two notification conditions slice 10 wrote for them their first delivery.

Amended after running the slice end to end against a real daemon, real tmux, and
the generated helper under a real shell. Two defects, and neither was reachable
from this repository's own fakes:

8. **tmux mangles its own output when the client has no locale.** A tmux client
   whose locale is not UTF-8 replaces every non-printable character in the
   output of `-F` with an underscore — a tab, a newline, and a unit separator
   alike — and every format `internal/tmux` uses is tab-separated. So a daemon
   started without `LANG` or `LC_ALL` cannot parse the identifiers of the
   terminal it has just created: every task launch fails with `tmux returned
   "$0_@0_%0", want stable session, window, and pane ids`, and discovery finds
   nothing at all, which would make every task look like a task whose terminal
   had gone. Measured against tmux 3.7b, where the substitution follows the
   *client's* locale and not the server's.

   An environment with no locale is not an exotic case: it is what a process
   started by launchd or systemd gets, which is how slice 17 intends a daemon to
   run, and it is what any sanitised environment looks like. The suite never saw
   it because `go test` inherits the developer's own environment.

   Every control invocation now passes `tmux -u`, which is the documented flag
   for exactly this and changes nothing about the environment a pane inherits.
   Interactive attachment deliberately does not pass it: there the client is the
   user's own terminal, and what it can render is theirs to declare. It is
   slice 5's defect, found by running slice 11 — the shape slice 7 recorded when
   a dashboard's stream loop surfaced as a Git error.

9. **A finishing gate lost its results to the request that started it.** The
   daemon is the only process that writes state (ADR-008) and every write is
   atomic (FR-STATE-002), and neither of those makes a load-change-save cycle
   safe against another one. A gate finishing while the review request that
   started it was still comparing repositories left a task recorded as
   `ready_for_review` whose review held no checks at all: the workflow said the
   checks had passed and the record of what passed had been overwritten by a
   copy loaded a moment earlier. Every fixture-based test passed, because the
   background gate is the first thing in Feat that writes one task's records
   from two goroutines.

   Decision: a per-task lock, held across a cycle rather than across an
   operation — the gate takes it to record what it found and releases it while
   the checks run, which take minutes. Every path that loads, changes, and saves
   one task takes it: the review actions, the gate's two halves, control
   delivery, the idle and startup timers, and the runtime actions. The gate's
   second half re-reads everything after taking it, because a task can move
   while its checks run: a user who approved in the meantime has decided, and a
   gate must not undo that.

   Slice 12 owns reconciliation and will meet the same question for the paths
   that run before a task can have a gate. This is the narrow half.

10. **The notification this decision suppresses one for did not arrive.** A task
    that passes its gate reaches `ready_for_review` and the user is not told,
    although the argument above for dropping the `review_requested` notification
    is that the later one is the one that means something. So a gated task
    currently arrives more quietly than an ungated one, which inverts what the
    suppression was for. The transition, the event, and the review record are all
    correct, and `notify.notifiableWorkflow` maps the state, so nothing here is a
    rule that was decided wrongly: what is unproven is the flow between a
    transition and a desktop, and this ADR's closing sentence about giving slice
    10's two conditions "their first delivery" should be read as untested rather
    than as observed.

    Not diagnosed and deliberately not fixed inside slice 11, because the same
    walk is owed to every condition: slices 10 and 11 both tested notifications
    over a fake notifier, which proves the daemon asked and not that anybody was
    told. Slice 13 owns it, against a real desktop, along with the layers that
    can legitimately drop one — the suppress-while-attached policy in
    particular, since a user who has just told an agent to request review is by
    definition attached to it.

11. **A gate outlived the daemon that started it.** `startGate` detached its run
    from the request context, which is right — a test suite outlives the message
    that asked for it — and detached it from the daemon's lifetime too, which is
    not. Nothing cancelled a running gate at shutdown and nothing waited for one,
    so `Serve` could return while a goroutine was still writing a task's records,
    and a check that had not finished was left running with no daemon to report
    to. Found by CI on Linux, where three tests that emit a review request failed
    in cleanup with `TempDir RemoveAll: directory not empty`: the gate was
    writing the control workspace the testing package was removing. macOS lost
    the same race quietly.

    Decision: gates are owned like the pending transitions beside them in
    `Serve`'s shutdown, which already carry the rule — nothing may fire into a
    daemon that can no longer write what it decided. Stopping cancels each run
    and then waits for it; cancelling is what ends the check itself, and the wait
    is bookkeeping rather than the suite, so it costs milliseconds.

    What a stopped gate must not do is record. A cancelled run produces
    inconclusive results, `Decide` reads inconclusive as not passing, and
    recording that would fail a task because Feat was restarted and answer the
    waiting agent with a verdict its checks never produced. So a cancelled run
    leaves the task in `verifying` and writes nothing, which is exactly the state
    evidence 2's recovery was built for.

12. **A gate that landed on an archived task rebuilt the control workspace the
    cleanup had just removed.** The maintainer found two control workspaces on
    tasks whose archive event reads "the task was archived after removing its …
    control workspace", each holding one file: `inbox/verification-<request>.txt`.
    The task's own event log dates the removal at 17:18:28.637 and the gate's
    landing at 17:18:28.764. So the cleanup did remove the workspace, and 115ms
    later the gate wrote a verdict into it — and every write in `internal/control`
    creates the directory it writes into, because that is what a workspace's own
    launch needs.

    `finishGate` guards the *transition* on the task still being `verifying`,
    which evidence 9 added for the user who approves while the suite runs. It
    guards nothing else, so the review record, the event, and the verdict all
    still ran. An archived task is the other way a task moves while its checks
    run, and it is the one where every write is addressed to something that no
    longer exists: the worktree the checks ran in is gone, and so is the session
    that would read the verdict.

    Decision: the rule evidence 11 states for a cancelled run holds for an
    archived one. A gate that finds its task archived records that its results
    were discarded and stops — no review record, no transition, no verdict. The
    event is written rather than the run ending in silence, because a user who
    watched a gate start and then archived the task is owed the reason no verdict
    ever appeared. And `answer` checks that the workspace is still there before
    writing, at the write rather than only at this caller: the classes of a
    cleanup are independent choices, so removing the control workspace without
    archiving is a supported thing to ask for, and rebuilding a tree the user
    confirmed the removal of is wrong however the caller reached it.
