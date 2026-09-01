# ADR-049 — Who owns the interrupt while another program has the terminal

Status: accepted
Recorded: 2026-08-09, after use

The maintainer opened the Compose logs from the runtime tab and found no way out
of them that was not also a way out of Feat.

Evidence, measured against Bubble Tea 1.3.10 on a real pseudo-terminal rather
than reasoned about, because two signal handlers disagreeing is exactly the kind
of thing reasoning gets right in the wrong order:

1. `docker compose logs --follow` ends when the user interrupts it. There is no
   other exit: it follows until something stops it, which is what FR-RUN-006
   asks for.
2. The terminal driver sends the interrupt to every process in the foreground
   process group, not to the program the user is looking at. The dashboard is in
   that group, so it receives the key that was meant for the logs.
3. Bubble Tea already has a policy for this. `ReleaseTerminal`, which it calls
   before running a command, sets its `ignoreSignals` flag, and its own handler
   drops signals while that flag is set — the program that holds the terminal is
   the program the interrupt is for. `RestoreTerminal` clears it again.
4. `tea.WithContext` is not covered by that flag. A cancelled external context
   ends the event loop wherever it is, and `feat` handed the program the
   process-wide interrupt context that `main` derives with
   `signal.NotifyContext`. So Feat had a second signal handler that did not know
   when the dashboard was not in charge: on a pseudo-terminal, the same program
   with that context died on the interrupt that left its child, and without it
   the child died, the program repainted, and the user carried on. That is the
   defect exactly.
5. Compose exits 130 when it is interrupted, and the dashboard turned any
   non-zero exit from a command it ran into an error banner. Leaving the logs
   therefore also reported a failure — evidence 9's state that cries wolf, in
   the other adapter.
6. Ctrl-C typed at the dashboard itself is not a signal at all. Bubble Tea holds
   the terminal in raw mode, where the key arrives as input and the model
   already quits on it. The interrupt context was doing nothing for the ordinary
   path it appeared to serve.

Decisions:

- The dashboard's lifetime is its own. `ui.Run` detaches from the process-wide
  interrupt context and passes no external context to the program; Bubble Tea's
  own handling ends it, which is the one place that knows whether the dashboard
  or a program it lent the terminal to currently owns it. `tea.ErrInterrupted`
  joins `tea.ErrProgramKilled` as an ordinary shutdown rather than a failure.
- An exit produced by an interrupt — 130, or the signal itself for a program
  that installs no handler — is how a user leaves a program the dashboard ran,
  and is not reported. Any other non-zero exit still is: a diff tool that could
  not open is something the user needs to know about. It is ADR-034 evidence 9's
  rule, applied at the client to the same distinction.
- The health screen keeps its context. It renders and exits, lends the terminal
  to nothing, and has no child whose interrupt could be mistaken for its own.
- What this costs is stated rather than hidden: while a child holds the
  terminal, a `SIGTERM` aimed at Feat waits for that child. The previous
  behaviour was not better — it tore the dashboard down while another program
  still had the terminal — and a user who needs the child gone can interrupt it,
  which is the same key this decision exists to make work.

Consequence: three small changes — the program's context, the exit status a
command's failure is read from, and a test for each that fails against the
behaviour it replaced. No stored format, endpoint, or key binding moves. The
gap this leaves is discoverability: nothing on screen says that the interrupt is
the way back, which belongs with the deferred dashboard polish rather than here.
