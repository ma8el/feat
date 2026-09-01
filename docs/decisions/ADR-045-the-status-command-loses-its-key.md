# ADR-045 — The status command loses its key

Status: accepted
Recorded: 2026-08-09, after use

The task panel bound `s` to the project's configured status command, the third of
the external commands FR-REV-002 asks for. The maintainer pressed it and reported
that nothing happened.

Evidence:

1. It ran. `tea.Exec` leaves the alternate screen, runs the command in the
   selected repository's worktree, and re-enters the alternate screen when it
   exits. The default status command — `git status --short --branch` — prints one
   or two lines and exits at once, so its output was written to the screen the
   TUI had just left and was gone within a frame. Diff and editor survive the
   same path only because a pager and an editor wait for the user; nothing about
   the status command does.
2. Making it visible costs more than it returns. The alternatives are a pause
   after every short-lived external command, which puts a key press between the
   user and a tool that finished, or rendering the output in the panel, which is
   the internal diff surface ADR-006 refused.
3. The panel already says it. Per repository it carries the recorded base, the
   head, the branch, the worktree path, the changed-file count, and whether the
   tree is dirty, merged, or ahead of its base. What `git status` adds over that
   is the names of the uncommitted and untracked files, which `d` shows for
   tracked work and the shell shows for all of it.
4. The key was borrowed. `s` opens the task's shell everywhere else in the
   dashboard, and the panel's own comment recorded that as a compromise made to
   keep the three external commands together.

Decisions:

- `s` opens the task's shell on the task panel, as it does on every other view.
  The panel's external commands are diff and editor.
- The `review.status` configuration stays. It is still expanded, still validated
  by `review.New` against the task's own worktrees, and still printed by
  `feat review` in a terminal that cannot show the screen, where an expanded
  command line is something a user can read and run themselves.
- No external command Feat launches is wrapped in a pause. A command that wants
  to be read is one that waits, and that is the tool's business rather than
  Feat's.

Consequence: one key case and one hint are deleted from the task panel and
FR-REV-002 moves with the code. Nothing in the daemon, the API, the
configuration, or `internal/review` changes: `review.KindStatus` is still one of
the three kinds, and a project that configures a status command still gets it
expanded and reported.
