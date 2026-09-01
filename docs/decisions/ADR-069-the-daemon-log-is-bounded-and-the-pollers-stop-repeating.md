# ADR-069 — The daemon log is bounded, and the pollers stop repeating themselves

Status: accepted
Recorded: 2026-08-23, from a 300 MB `daemon.log` on a dogfood machine

Nothing bounded the daemon log. `OpenLog` opened it with `O_APPEND` and wrote for
as long as the daemon ran; no rotation, no pruning, and no startup truncation.
Its size was therefore a function of uptime rather than of anything happening,
and on a machine that had been running a while it reached 300 MB.

Evidence, from reading what actually writes to it:

1. There are sixty logging call sites and none at debug level, so the volume is
   not chattiness. It comes from loops. The access log in `internal/api` wrote
   one `info` record per HTTP request, and every dashboard refresh and every CLI
   invocation is a request. The control poller runs every 250 ms and logged a
   `warn` per task per tick when a control workspace could not be read; the
   runtime poller (5 s) and the resource sampler (2 s) have the same shape.
2. A persistent failure in the control poller is roughly four records a second
   for as long as it lasts — about 70 MB a day, which reaches 300 MB inside a
   week. The failures these loops report are almost never momentary: a task whose
   workspace has gone, or a container runtime that is not running, is still true
   at the next tick.
3. `LogTail` reads the whole file with `os.ReadFile` to quote the end of it, on
   the path that explains why a daemon did not start. Against an unbounded log
   that is a 300 MB read inside an error message.

Decision: the log is rotated at a fixed size, keeping a fixed number of numbered
generations, so it occupies a bounded amount of disk however long the daemon
runs. The values are constants rather than configuration: there is no
daemon-level configuration file in v0 — `internal/config` is per-project — and
inventing one to hold a single number would be settling a permanent shape for the
sake of a default nobody has yet needed to change. A configured bound can be
added later without changing this decision.

Rotation copies and truncates rather than renaming. This process does not hold
the only descriptor for the file: `feat daemon start` opens the log and passes it
to the process it spawns as standard output and standard error (ADR-027), and a
descriptor refers to the inode, not to the name. A rename would leave the spawned
daemon's own output — which is to say its panics, the output that exists so that
a process dying before its logger does still explains itself — going to the
rotated file while its logger wrote to a new one. Truncating in place keeps every
descriptor pointing at the same inode, and an `O_APPEND` write after a truncation
resumes from the beginning of it.

A log that is already past the bound when it is opened is cut down to its most
recent records rather than copied whole into a generation. Copying 300 MB aside
would honour the bound going forward while leaving the 300 MB that prompted this
on disk, which is the opposite of what the user wanted. The tail is cut at a
record boundary so that every line of the result still parses.

The rotation is best-effort and never costs a record: a rotation that fails is
reported into the log itself and the record that triggered it is still written.
The failure is not retried on the next record, because on a full disk that would
be its own runaway.

Separately, the loops stop repeating themselves. The three pollers report a
failure when it appears and again when it changes, rather than on every tick, and
clear the subject on success so that a failure recurring after a recovery is
reported again. The size of the log becomes a function of how many distinct
things went wrong rather than of how long the daemon has been running. The access
log moves to debug level: it is the one record here whose volume is set by uptime
rather than by anything going wrong, and what the default level keeps is the part
that reports a problem — a request the daemon could not explain, and one that
panicked.

This bound is not a retention policy. It answers "the log must not grow without
limit"; how long a record is worth keeping is a different question and is not
asked here.

Consequence: `internal/daemon` gains `rotatingFile` and `repeats`; `OpenLog`
wraps the log file in the bound; `pollControl`, `pollRuntimes`, and
`sampleResources` report through `repeats`; `internal/api`'s `logRequests` logs at
debug. `LogTail`'s whole-file read is now bounded by construction. The state
directory listing in `docs/06-technical-architecture.md` gains the rotated
generations.
