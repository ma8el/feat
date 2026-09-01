# ADR-035 — Resource observation, notification policy, and what a machine can honestly report

Status: accepted
Recorded: 2026-08-07, before implementation

Evidence found while planning notifications and resources. Items 1 to 6 were
measured on the target machine — Docker 29.5.2, Compose 5.1.4, tmux 3.7b, macOS
26.5 — rather than reasoned about, because each of them decides something the
design cannot take back later:

1. `docker stats --no-stream` takes **1.1 to 2.0 seconds**, whether it is asked
   about one container or all of them, while `resources.sample_interval`
   defaults to two seconds. A sample therefore cannot be taken inside a request,
   and the configured interval is a floor rather than a promise.
2. `docker ps --filter label=… --format '{{.ID}}\t{{.Label "dev.feat.task"}}…'`
   answers in **25 ms** and extracts one label directly. Docker's own `Labels`
   field is a comma-joined string, so a label a user's Compose file sets with a
   comma in it would otherwise split into values that were never there.
3. `docker stats` reported a container's memory against **7.653 GiB** on a
   machine with **16 GiB**: on macOS that limit is the container runtime's own
   virtual machine. A per-task container total is therefore not a share of host
   memory, and presenting it as one would be a claim nothing measured.
4. tmux exposes `#{window_active_clients}`: 1 for the window an attached client
   is viewing, 0 for every other, following a window switch immediately. It is
   the per-task answer to "is the user watching this", which `#{session_attached}`
   is not — a user attached to a project's session is looking at one of its task
   windows and not at the others.
5. `osascript` with an `on run argv` handler delivers a notification in 141 ms,
   exits 0, and reads its text out of `argv` rather than out of the script. It
   also exits 0 when macOS drops the notification for want of permission, which
   it does silently.
6. `ps -A -o pid=,ppid=,rss=,time=` returns five hundred processes in 18 ms. Its
   `%cpu` column means different things on the two supported platforms — a
   decaying recent average on macOS, a lifetime average on Linux — while the
   cumulative `time` column means the same thing on both.
7. A per-core processor utilisation figure is not obtainable on macOS from Go
   without cgo. The Mach call that carries it, `host_processor_info`, is not
   reachable from pure Go, and the usual third-party library returns "not
   implemented" in its no-cgo build.
8. `agent.claude.idle_grace_period` and `notifications.idle_grace_period` both
   exist in [07-configuration-model.md](07-configuration-model.md), both default
   to five seconds, and only the first has a reader. The document says what
   neither of them means.
9. `resources.sample_interval` is per-project configuration for a measurement
   that is machine-wide. Three registered projects can ask for three intervals
   for one machine.
10. The daemon applies the control messages that arrived while it was stopped
    before it serves anything. Every state change that catch-up produces looks,
    to anything downstream, exactly like one that happened just now.

Decisions:

- `internal/resources` is an adapter under the rule ADR-029 established for Git:
  it receives final values — process identifiers, a label selector, a filesystem
  path — and reads neither configuration nor persistent state. A
  `resources-stays-an-adapter` `depguard` rule denies it both Compose adapters,
  and that denial is the point of it: an agent's container and an application's
  are the same thing to a resource observer, and a package that could tell them
  apart would eventually be asked to treat them differently. ADR-025 requires an
  ADR for a boundary rule, and this is that record.
- `internal/notify` holds the policy and its platform adapters under the same
  rule, with a `notify-stays-a-policy` rule that denies it configuration, the
  control protocol, and storage. The daemon resolves a project's settings into a
  `Policy` and hands it a `Subject` carrying a task's key, title, and project.
- Sampling is a background loop with a cache, and the local API serves the cache.
  Evidence 1 makes this the difference between a working dashboard and one that
  blocks for two seconds per refresh, and it gives the first two acceptance
  criteria their shape: no request path can be slowed or failed by a metric, and
  a sample that failed leaves the previous one with its own time and its notes.
- The interval is the shortest any registered project asks for, floored at one
  second and at however long the last sample took. Evidence 9 is an oddity of the
  configuration model rather than a design, and the most eager project winning is
  the reading under which no project's setting weakens another's.
- The machine sample is **load average with the core count**, available memory,
  and available disk on the filesystem holding the state directory. Evidence 7
  means a utilisation percentage would exist on one supported platform and not
  the other, and one measure on both is worth more than two that look alike and
  are not. This narrows the words "whole-machine CPU" in
  [06-technical-architecture.md](06-technical-architecture.md) and FR-UI-005,
  which is updated in the same change. Load is reported beside the core count
  because the number means nothing without it.
- Per-task usage is containers and processes, summed and also reported apart.
  Containers are found by Feat's own ownership labels in one `docker ps` and
  measured in one `docker stats`, so the cost is two Docker calls per sample
  whatever the number of tasks — which is the number the dashboard exists to make
  large. Processes come from one `ps`, summed over the subtree of each task's
  tmux panes, with processor use differenced between samples for evidence 6.
  Evidence 3 is stated in the interface and on the screen rather than smoothed
  over.
- A Feat-owned container with no `dev.feat.kind` label is the agent's. The
  runtime adapter labels its own `kind: runtime` and the execution adapter labels
  nothing, and the asymmetry is left alone rather than corrected: a container an
  older build created would carry no new label either, so the inference has to
  exist regardless, and adding one would change a document slice 8 pinned for no
  gain.
- `GET /v1/resources` is added to the endpoint list, as slice 9 added three. A
  sample is not persisted (`docs/06-technical-architecture.md`, storage rules),
  so it is not part of what a task record says about itself; it has its own time
  and its own failure mode, which a task carrying the figures would have to
  borrow. Every figure nothing measured is published as null rather than zero.
- `notifications.idle_grace_period` is measured **from the idle transition**: how
  long a task must have *been* idle before Feat interrupts somebody about it. The
  other reading — both graces measured from the end of the turn — was considered
  and rejected, because a notification grace shorter than the provider's would
  then expire before the task was idle and no notification would ever be
  delivered. A configuration that silently turns off the thing it configures is
  worse than one that needs explaining. Evidence 8 is resolved in
  [07-configuration-model.md](07-configuration-model.md) in the same change.
- Notification suppression asks tmux, per window, using evidence 4. It is an
  observation rather than a memory of somebody having run `feat attach`: a user
  who detached, or who switched to another task's window, stops watching without
  telling Feat anything. A tmux that cannot answer counts as nobody watching, so
  the notification is delivered — of the two mistakes, an unnecessary
  notification is noise and a missing one is the failure the slice exists to
  prevent.
- What is worth interrupting somebody for is two pinned tables, in the shape
  ADR-026 used for the workflow transitions and ADR-032 for agent events. Their
  most important property is again an absence: nothing maps an end of turn or an
  idle process to a notification, because idle is not a state a task arrives in
  but one it stays in, and how long it has stayed is what decides. That is armed
  by a timer instead, which makes "idle notifications do not fire immediately" a
  property of the mechanism rather than of a value somebody chose.
- One change produces one notification. A session that dies moves both the
  process and the workflow, and the workflow wins; a process failure that left
  the workflow where it was is reported, because nothing else reports it.
- Startup catch-up records without notifying (evidence 10). Restarting Feat in
  the morning must not announce every turn that ended overnight.
- Notification text is composed from a task's key, title, and project plus a
  fixed phrase per condition. There is deliberately no way to reach a brief, an
  agent's summary, a path, a command, or a configured value, so the fourth
  acceptance criterion is a property of what the code can see rather than a
  filter over what it writes — the shape slice 3 used for secrets in diagnostics.
  A test pins the fields a `Subject` may hold.
- Delivery is `osascript` alone on macOS, with the text passed as arguments and
  read out of `argv` (evidence 5). Feat reports that it **delivered** a
  notification and never that one was seen: macOS decides per application whether
  to show one and drops an unauthorised one without saying so, and evidence 12
  shows the application it decides about may not be one the user can find. Linux
  support stays slice 17's, and this build says so rather than failing silently
  there.
- A delivered notification is recorded as a task event, `notification_sent`. A
  desktop notification is gone the moment it is dismissed, so without this there
  would be no record that Feat asked for somebody's attention — and slice 13 has
  to measure how many idle notifications turned out to be false. The event type
  is deliberately not itself notifiable, which a test pins, because recording an
  event publishes it.

Consequence: the user-visible additions are one endpoint, a machine resource card
and a filled resource column on the dashboard, attention badges, and macOS
desktop notifications. The command surface does not change, so its golden file
and the README's command list are untouched. No stored format changes — samples
are not persisted and the event vocabulary gains one additive type — so no
migration is needed.

`notifications` and `resources` have been parsed, resolved, and defaulted since
slice 3 without a reader. This slice is their first, which is why the semantics
of two of their four fields had to be settled here rather than found in the
configuration model.

Amended after running the slice against a real daemon, a real tmux client, and
real Docker rather than against its own fakes:

11. A control-mode tmux client attached without a held-open standard input is
    accepted by tmux and then leaves at once, so `window_active_clients` reports
    zero and the notification is delivered. That is correct behaviour and it is
    worth recording, because the first attempt to verify suppression by hand
    proved nothing: the test looked as though it had shown suppression when it
    had shown an unattached window. Verification held the client's input open and
    checked `list-clients` before drawing any conclusion.
12. A notification `osascript` posts is attributed to `com.apple.ScriptEditor2`
    and travels the legacy notification path, which does not require the
    per-application registration the modern one does. On macOS 26 that bundle has
    **no entry** in Notification Center's preferences at all, so the advice to
    allow notifications for Script Editor named a switch that was not there,
    while delivery worked the whole time. `log show --predicate 'process ==
    "usernoted"'` reports both the delivery and the presentation decision, and is
    the only diagnostic that distinguishes "macOS dropped it" from "it was shown
    and missed". The README and
    [06-technical-architecture.md](06-technical-architecture.md) are corrected in
    the same change.
13. `go test` caches a passing result and replays its output verbatim, including
    `--- PASS` under `-v`. An opt-in test whose entire purpose is a side effect
    outside the process therefore reports success while posting nothing, which is
    evidence 11's failure mode in a second form: a check that appears to have run.
    `-count=1` is part of the instruction for running these, not an optimisation.
14. What a broken observation command can break is platform-shaped. macOS reads
    load and memory through `sysctl` and `vm_stat`, which are processes, while
    Linux reads both out of `/proc` and the disk through `statfs`, which are not.
    A daemon test that failed every command therefore lost two machine figures on
    one platform and none on the other, and its assertion that they were absent
    passed on macOS for a reason that was never the acceptance criterion. It is
    the tasks, whose sources run commands on both platforms, that carry the
    property at the daemon; the machine's half belongs to `internal/resources`,
    where an injected machine reader makes it platform-neutral. This is the third
    form of the same failure — a check that looked like proof — and the first one
    a machine caught rather than a person.
15. `syscall.Statfs_t.Bsize` is signed on Linux and unsigned on macOS, so the
    conversion `gosec` refuses under G115 exists on one platform only. A negative
    block size cannot come from a working kernel, and it is reported as an
    unmeasured filesystem rather than converted, which is what every other
    figure this build cannot trust does.
16. `ps -o time=` reports cumulative processor time in **whole seconds on Linux**
    and in centiseconds on macOS — `00:00:00` against `149:45.95`. Evidence 6
    established that the column means the same thing on both platforms, which is
    true, and missed that it does not carry the same resolution. The integration
    test spun for 200ms and asserted a positive figure: a tenth of what Linux can
    represent, so Linux answered zero and was right to.

    The consequence outlives the test. Per-process use is a difference of that
    counter over the sampling interval, so on Linux the difference is a whole
    number of seconds and the reported percentage is quantised to `1s/interval`
    — steps of 50 points at the default two-second interval, where macOS resolves
    to about one. A task steadily using a quarter of a core reports 0% and 50% by
    turns rather than 25%.

    This is the shape ADR-035 rejected once already, when it chose load average
    over a processor percentage because two figures that look alike and are not
    are worse than one figure on both platforms. It is recorded rather than fixed
    here: the honest fix is `/proc/<pid>/stat`, whose `utime` and `stime` are
    clock ticks at the same 10ms USER_HZ macOS reports, and adding a
    platform-specific process reader is more than a red build justifies. Until
    then a Linux per-task processor figure is coarse rather than wrong, and OQ-012
    carries it.

The end-to-end run is what settled the timing. A turn ended at 10:35:51, the task
became idle at 10:35:56 after the provider's five-second grace, and the
notification was delivered at 10:35:59 after the project's three-second
notification grace — the two periods measured from the two moments this ADR says
they are. A third turn ended with a real client watching that task's window
produced the idle transition and no notification, while the two before it
produced both.
