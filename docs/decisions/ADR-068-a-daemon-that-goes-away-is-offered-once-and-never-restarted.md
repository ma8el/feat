# ADR-068 — A daemon that goes away is offered, once, and never restarted behind the user's back

Status: accepted
Recorded: 2026-08-23, from a reading of what the dashboard does without a daemon

ADR-008 says the TUI auto-starts a local daemon, and it does: `feat` with a
terminal spawns one before it opens the dashboard. What was never decided is what
the dashboard does when the daemon it started goes away while it is open —
because it was stopped deliberately, because a build was replaced, or because it
failed.

Evidence, from driving the built binary against a throwaway runtime directory and
killing the daemon under a live dashboard:

1. The dashboard survives, which is right: a failed read must not take the view
   of every running task with it. It then stays. The event stream ends and says
   so once; the two-second read fails from then on and puts
   `no feat daemon is listening on …` in the footer on every tick; the task list
   stays drawn, frozen at what it last knew, and nothing marks it as old. There
   is no key, no prompt, and no way back short of quitting, running
   `feat daemon start`, and reopening.
2. The advice in that footer could not be taken. `feat daemon start` is a shell
   command, and the user is inside a full-screen alt-screen dashboard; the only
   way to reach a shell is to leave the thing the advice is about repairing.
3. Every command except the dashboard already refuses without a daemon and exits
   4. That is ADR-028's rule for a mutation — behaviour must not depend on
   whether a terminal is attached — and it is not what this is about: the
   dashboard is the one place where a terminal is not incidental.

Decisions:

- The dashboard asks before starting a daemon, and starts nothing until it has
  been answered. A daemon that stopped may have been stopped on purpose, and one
  that died may have left tmux sessions, worktrees, and containers that the
  replacement's reconciliation pass is where the user finds out about. Restarting
  silently would hide that a crash happened, which is the thing worth seeing.
  This is the difference between this and ADR-008: opening the dashboard is an
  act the user performed, and a daemon dying is not.
- The offer is made once per outage, not on every failed read. The read that
  discovers an absent daemon runs every two seconds, and a dialog that reopened
  on each of them could not be dismissed. A daemon that answers again resets
  that, so the next outage is a new question rather than the same refusal still
  standing.
- It never takes the keyboard from an overlay that is already open. Preparation
  holds a brief somebody is typing and cleanup holds a confirmation somebody is
  answering; closing this dialog returns to the tab underneath, not to what they
  were doing. The failure is recorded, and the question waits.
- Declining leaves the key in the error message: `S` puts the question again.
  The footer is the only thing left on a dashboard whose daemon is gone, so it
  carries the way back rather than a command that can only be run somewhere else.
  `S` is on the documented key surface, and reports that there is nothing to
  start when a daemon is answering.
- Starting is asked of the backend adapter, not done in `internal/ui`. The TUI
  reaches the daemon over the socket and may not import `internal/daemon`
  (ADR-031, the `ui-is-a-client` depguard rule); `internal/cli`'s backend already
  builds the tmux attach, the task shell, and the editor for the same reason.
- The event subscription is reopened once, after a start the user asked for, and
  only when the stream had ended. ADR-027 declined automatic reconnection because
  it would have to decide how often to retry; this decides nothing of the kind —
  it follows a key press, happens once, and the daemon that just started has no
  record of the subscription the previous one was serving.
- A dashboard that could not start the daemon it needs says why. `describeDaemon`
  computed that reason and root discarded it on the interactive path, so a failed
  auto-start was reported as "no feat daemon is running on …; start one with
  `feat daemon start`" — the command that had just been run on the user's behalf
  and failed — while the same failure in a pipe printed the reason. `NotRunningError`
  gains a `Cause`, which replaces the advice rather than joining it. Exit code 4
  is unchanged: an absent daemon is still an absent daemon.

Not decided here, and deliberately: whether any command other than the dashboard
starts a daemon. ADR-008 is about the TUI and stays about the TUI. `feat
implement` is terminal-only and would be the arguable case; widening ADR-008 to
"any interactive entry point" is a change to that ADR and not a side effect of
this one.

Consequence: `internal/ui` gains `screenDaemon`, the `S` binding, and a
`StartDaemon` method on `Backend`; `internal/cli`'s backend implements it through
`daemon.Spawn`, which is the same call `feat daemon start` makes. The stale task
list behind the dialog is still not marked as stale — the footer's error is what
says the dashboard is not current — and that remains open.
