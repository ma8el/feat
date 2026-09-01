# ADR-064 — Diagnosis is read on the dashboard, and it says which process it is true of

Status: accepted
Recorded: 2026-08-15, from the hole ADR-063 left

The dashboard could configure a project and could not tell the user whether it
worked. The wizard's last screen named `feat doctor`, which is a command — so
the first-run path closed for writing a configuration and not for having one
that works, and that is where a project actually fails: a Compose service that
is not there, an agent that is not installed, a remote that does not resolve.

Evidence:

1. The questions cannot ask the host anything. Every proposal the wizard derives
   is about what a checkout *is*, not about whether the project *works*, and the
   difference is the whole of what `feat doctor` reports.
2. The findings are already data. `project.Diagnose` returns a report of
   `{check, severity, summary, action}`; the command's printer is one renderer
   of it, and a screen is another.
3. Diagnosis is worth having a second time. The first run tells a new user
   whether their configuration is right; every run after that is somebody whose
   task has stopped working, who is already looking at the dashboard.

Decisions:

- The checks run in the process the user is in front of, and reach the dashboard
  as data. `feat doctor` works before a daemon exists (ADR-028), so a daemon
  endpoint would be a second implementation of the same checks — and the answer
  would be about the daemon's environment rather than the one the user asked
  about. The dashboard's backend runs them and converts the report to `api`
  types, so the screen that draws them reaches no adapter (ADR-031).
- The screen says where the checks ran. A tool on this terminal's PATH is not
  necessarily on the daemon's, and the daemon is what launches agents: "checked
  from this terminal" is what the report is honestly about, and it is drawn with
  the findings rather than left to be assumed.
- Nothing runs a diagnosis on its own. The checks shell out to Git, Compose, and
  the container runtime; a dashboard that ran them on a timer would be one
  nobody could leave open. `D` runs one, `r` runs it again, and that is all.
- The subject is a project, not a task. Whether a Compose service exists is true
  of every task in a project or of none of them, so the screen checks the
  selected task's project — and every configured project when no task is
  selected, which is what `feat doctor` does.
- The wizard runs the checks itself once the file exists, rather than offering
  them. The user has just asked for the project to exist and is waiting either
  way, and the checks change nothing. What they find never fails the setup: the
  file is written, and a finding is a thing to fix rather than a reason to undo
  a project.
- A report opens at the first finding that is not a pass, with the heading it
  belongs to. Most of a report is passes and the pane holds a dozen lines; what
  is above the window is counted rather than hidden, so nothing is lost by
  starting where the problem is.
- A skipped check is drawn as a skipped check. It is not a pass, it says why it
  did not run, and it is counted separately wherever findings are counted
  (ADR-033).

Consequence: `api.Diagnosis` describes a report, `internal/cli` runs it, and
`internal/ui` draws it in two places — the dashboard's own screen and the
wizard's last step. The command is unchanged. What this does not do is diagnose
from the daemon, which is the only way to answer about the daemon's own
environment; that stays an open question for the machine where the two
environments differ.
