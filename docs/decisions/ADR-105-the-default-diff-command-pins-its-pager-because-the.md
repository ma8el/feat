# ADR-105 — The default diff command pins its pager, because the dashboard repaints over a command that returns

Status: accepted
Recorded: 2026-09-27, from a measurement on two machines during the Linux run

Opening a task's diff from the dashboard showed it for an instant and took the
terminal back. The text was left on the terminal and the dashboard painted over
it, so there was nothing to read. It was found on Linux and is not a Linux
defect.

Evidence, measured with a stub pager that prints its own `$LESS` under a
pseudo-terminal, because the two machines disagreed and the environment was the
variable nobody had looked at:

1. **Git exports `LESS=FRX` to its pager when `LESS` is unset**, and `-F` tells
   `less` to quit at once when the content fits one screen. A task's diff often
   does; the diff that found this was one line.

   | Machine | git | `LESS` git exported | holds? |
   | --- | --- | --- | --- |
   | Ubuntu 26.04.1 | 2.53.0 | `FRX` | no |
   | the dogfood Mac, as it is | 2.52.0 | `-R` | yes |
   | the dogfood Mac, `LESS` unset | 2.52.0 | `FRX` | no |

   The third row is the finding. `LESS=-R` in one machine's environment is the
   only reason the dogfood never met this. The platforms never differed.

2. **Two simpler explanations were measured and rejected.** `less` is installed
   on both machines and is the same build, version 668. And it is not Bubble
   Tea's handover: plain `git diff` in an ordinary shell returns immediately in
   the same way, while a diff longer than one screen holds the terminal under the
   dashboard unchanged.

3. **A pager that returns is harmless in a shell and destructive in the
   dashboard.** `tea.Exec` releases the terminal, runs the command, and restores
   the terminal when it exits — the mechanism ADR-049 measured for signals. In a
   shell the text stays on screen and the user reads it. Under a restore it is
   painted over. So the review path depends on its command blocking, and nothing
   said so.

4. **`feat doctor` cannot currently say this.** `checkReviewCommands` resolves
   the command's program and stops, so `git` being installed is the whole of what
   it checks.

Decisions:

- **The default diff command pins the pager:**
  `git -c core.pager=less -+F diff {base_commit}`. `-+F` resets `F` on the
  command line, where it beats the environment, and leaves `R` and `X` from
  `FRX` alone.

  It is set with `-c` rather than by exporting `LESS`, because the command is an
  argument vector Feat owns end to end, and because Feat must not reach into the
  environment of a command a user configured.

- **A review command that reads must hold the terminal until the user leaves
  it.** That is now stated where the commands are configured — the settings
  template, `docs/examples/settings.yaml`, the README, and
  `docs/07-configuration-model.md` — rather than assumed. A command that opens
  its own window is free to return at once and is still correct.

- **Feat changes nothing about a command the user configured.** The default is
  Feat's to choose; `review.diff.command` is the user's. Somebody who sets their
  own and loses their diff to `-F` has the same three levers git gives
  everybody — `core.pager`, `GIT_PAGER`, `LESS` — and the README says so.

- **The narrow doctor check is not built here.** Whether an arbitrary command
  holds a terminal is undecidable, and the decidable case — a configured command
  whose pager resolves to `less` with `F` in effect — is a check with a real
  answer and a small population. It is worth having and it is not this change.

Consequence: the default that `feat settings show` reports changes, so the
`v0.2.0` changelog says so. `docs/04-functional-specification.md` is unchanged:
FR-REV-003 says Feat opens external commands and renders no diff, which is still
exactly what happens.

This is why the dogfood could not have found it. One machine had `LESS` set, and
real diffs are usually longer than a screen. The first person to read a one-line
diff on a machine with git's own defaults was always going to be a stranger,
which is the milestone this was found in.
