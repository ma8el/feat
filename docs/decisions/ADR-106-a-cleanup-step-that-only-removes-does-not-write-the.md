# ADR-106 — A cleanup step that only removes does not write the generated input a step before it deleted

Status: accepted
Recorded: 2026-09-27, from a leftover directory found while verifying the worked Compose example

`feat task cleanup` reported removing a task's generated runtime input and left it
there. ADR-037 evidence 16's rule was right and did not reach far enough: it says
the directory goes with the Compose project it defines, "after the destroy and
never before", which constrains the step that removes it and says nothing about
the steps after.

Evidence:

1. **The directory was removed and put back 176 milliseconds later.** In the task's
   own event log, sequence 30 at `13:04:18.631` names the path as removed with the
   application containers, and sequence 31 at `13:04:18.807` removes the volumes.
   The `compose.include.yaml` left in that directory had an mtime of `13:04:18`.
   Sequence 30 was true when it was written.

2. **Building the runtime adapter writes the include.**
   `internal/runtime/compose/New` did so unconditionally, for the reason its own
   comment gives: a status, a stop and a destroy all name that file, and the first
   thing a user does with a runtime is ask what it is doing before anything has
   created it.

3. **`removeVolumes` builds that adapter and does not need the file.** It runs
   after the container classes, and for a task with a runtime it asks the runtime's
   adapter for whatever volumes the agent's did not remove. `RemoveVolumes` runs
   `docker volume rm <name>` — no Compose file, no project directory, no include.
   So the construction wrote a document nothing was going to read.

4. **The existing test could not catch it.**
   `TestCleanupRemovesTheGeneratedRuntimeInput` selects `ClassRuntimeContainers`
   alone, so `removeVolumes` never ran, so nothing rewrote the file. The bug needed
   two classes in one selection and the test used one.

5. **The agent root is not this.** It has a leftover on the dogfood machine too,
   and it is ADR-037 evidence 16's own accepted residual rather than this defect:
   that task went `preparing` → `failed` → `archived` and its cleanup removed only
   worktrees, branches and the control workspace, so no `agent_containers` class was
   ever offered for the directory to be removed under. The agent adapters also write
   nothing when built — `compose.ByName` validates and resolves Docker, and the
   override is written by `Prepare`, which brings the service up.

Decisions:

- **A construction whose only purpose is to remove does not write the generated
  input.** `compose.Options` gains `Removing`, which skips the include, and
  `removeVolumes` builds through it. The flag exists for one caller on purpose: it
  is the caller that runs after the file has been deleted, and naming that is
  cheaper than making every step defend against it.

- **The ordering rule stands and is now stated as an invariant rather than a step
  order.** Nothing after the removal of a task's generated input may construct
  something that writes one. Today `removeVolumes` is the only step that would;
  worktrees, branches and the control workspace are Git and the filesystem. A new
  step that builds a Compose adapter has to say which side of the removal it is on.

- **Removing the directory last instead was rejected.** It is the more robust shape
  — nothing could recreate what is deleted after everything — but it moves the
  deletion out of the class that reports it, which ADR-037 evidence 16 decided on
  purpose: the directory is reported with the class that removed it and is not a
  target of its own. Buying robustness against a hypothetical future step by
  splitting the report from the act, in the subsystem that deletes things, is the
  worse trade.

- **A test that removes volumes asserts the directory stays gone.**
  `TestCleanupWithVolumesKeepsTheGeneratedRuntimeInputRemoved` is the sibling of the
  test evidence 4 names, with the second class in the selection. It was watched
  failing before the fix.

Consequence: ADR-037 evidence 16 is amended with a pointer here. Nothing about what
cleanup removes, reports, or refuses changes; what changes is that a step which only
removes no longer writes.
