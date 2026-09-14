# ADR-101 — A daemon's endpoint record is republished for as long as it runs, and stop asks the daemon when the record is gone

Status: accepted
Recorded: 2026-09-14, from this machine's runtime directory and from a
measurement of the system's temporary-directory cleaner taken while writing this

ADR-027 put three files in the user runtime directory — `feat.sock`,
`daemon.lock`, and `endpoint.json` — and gave good reasons for each and for the
directory. All of them still hold. What it did not decide is how long the record
lives once written, because nothing suggested that was a question: the daemon
writes it at startup and removes it at shutdown, and in between it is a file
sitting in a directory the daemon owns.

On macOS it is also a file in a directory the system sweeps. A daemon that stays
up past its third day loses the record to `com.apple.bsd.dirhelper`, keeps
serving, and becomes a daemon that `feat daemon status` can describe in full and
`feat daemon stop` reports does not exist. This is not a race and not a rare
configuration: it lands on every macOS daemon at the first cleaner run after its
third day of uptime. It was found on 2026-09-13 on a daemon that had been up
since 2026-09-03, and the only way out of it was to send the signal by hand.

The field report that recorded it proposed two candidate fixes for the second
half and said to establish which rule the cleaner applies before choosing — the
standard ADR-098 set when a check rested on what a runtime does. Doing that
first was worth it, because the measurement eliminated the option the report
preferred.

Evidence:

1. **Two commands, run one after the other, disagreed about whether a daemon
   existed.** The daemon was alive, listening, and answering the API throughout.

   ```
   $ feat daemon status
   status    ok
   pid       60313
   uptime    235h38m35s
   $ feat daemon stop
   feat: no feat daemon is running; start one with `feat daemon start`
   $ echo $?
   4
   ```

2. **They ask different oracles, and only one of them is collectable.**
   `daemon status` dials the socket and reads `/v1/health`, so the identifier it
   prints is the running daemon describing itself. `Stop` never touched the
   socket: it began with `ReadEndpoint` because it needed an identifier to
   signal, and a missing record returns `ErrNotRunning`, which the CLI renders
   as the flat claim that nothing is running.
3. **Nothing ever refreshed the record.** `writeEndpoint` was called from
   exactly one place, inside `Acquire`, and never again for the life of the
   daemon. It is written by atomic replacement and then closed. Written on 3
   September, it was three days untouched by the 7th.
4. **The cleaner's threshold is three days and its run is nightly.**

   ```
   $ plutil -p /System/Library/LaunchDaemons/com.apple.bsd.dirhelper.plist
     "EnvironmentVariables" => { "CLEAN_FILES_OLDER_THAN_DAYS" => "3" }
     "StartCalendarInterval" => { "Hour" => 3, "Minute" => 35 }
     "ProgramArguments" => [ "/usr/libexec/dirhelper" ]
   ```

5. **The rule is not age alone, and the runtime directory proves it.** Listing
   the same directory with every timestamp rather than the one `ls` prints:

   | file | atime | mtime | ctime | size | type |
   | --- | --- | --- | --- | --- | --- |
   | `daemon.lock` | 2026-09-01 | 2026-09-01 | 2026-09-01 | 0 | regular |
   | `endpoint.json` | 2026-09-14 | 2026-09-14 | 2026-09-14 | 230 | regular |
   | `feat.sock` | 2026-09-14 | 2026-09-14 | 2026-09-14 | 0 | socket |
   | `tmux.sock` | 2026-09-01 | 2026-09-01 | 2026-09-13 | 0 | socket |

   `daemon.lock` is a regular file untouched for thirteen days, through roughly
   eleven cleaner runs, and it is still there — and it is the only regular file
   anywhere in this machine's per-user `$TMPDIR` at depth two or less whose
   access time is older than five days. Everything else that old has been
   collected. Something spares it that is not its age.

6. **What spares it is not that it is held open, and this is the measurement
   that changed the fix.** The field report concluded that the survivors are the
   files held open by a live process — the lock's descriptor, tmux's listener —
   and proposed holding the record open for the daemon's lifetime, as "a
   property rather than a schedule". `dirhelper` cannot see that. Its imports
   are the whole of what it can do:

   | purpose | symbols |
   | --- | --- |
   | deciding | `getattrlist`, `getattrlistbulk`, `lstat`, `stat`, `fstatfs` |
   | deleting | `removefile`, `unlink`, `unlinkat`, `rmdir` |
   | sysctl | `kern.boottime`, `kern.safeboot`, and nothing else |

   There is no `libproc`, no `proc_listpids`, no `proc_pidinfo`, and no `fcntl`
   or `flock`. Nothing in that list can ask whether a file is open. And `unlink`
   succeeds on an open file in any case — it removes the directory entry and the
   inode outlives it — so a held descriptor could not have protected the *path*
   even if something had checked. Reasoning from the survivors got the
   correlation right and the mechanism wrong, and the fix it recommended would
   have been built on a mechanism that does not exist.
7. **Age is nonetheless a conjunct of whatever the rule is, which is enough to
   fix it without knowing the rest.** `CLEAN_FILES_OLDER_THAN_DAYS` and
   `dirhelper`'s own `Cleaning %s older than %ld days` put age in the predicate;
   evidence 5 puts something else in it as well. What that something else is —
   the two socket survivors are not regular files, and the third survivor is a
   zero-byte regular file where the casualty was 230 bytes — does not need
   answering, because a record that is never old is spared under either reading.
   A fix that turns on age is correct without a complete account of the rule; a
   fix that turns on an exemption would have needed one.
8. **The daemon already publishes everything the record holds.** `api.Daemon`
   carries version, commit, process identifier, start time, and socket — the
   whole of `Endpoint` but its schema version — and its own doc comment
   describes the identifier as "the process identifier, which is also what stops
   it". The fallback this decision adds is that sentence, built.
9. **The dependency direction already allows the daemon to ask itself.**
   `internal/client` is denied `internal/daemon` by the
   `transport-stays-transport` rule, because the daemon implements the API's
   service interface and not the reverse. Nothing denies the other direction, and
   `internal/daemon` already used the client in its tests. No architectural rule
   had to move.

Decisions:

- **`Stop` takes the process identifier from the socket when the record cannot
  supply one.** It reads the record first, as before; when that fails it asks
  `/v1/health` and signals what the daemon says it is. The daemon is the better
  authority of the two, because the record is a file something else can remove
  and the daemon cannot go missing while it is answering. The trigger is any
  unusable record and not only an absent one: a record that is corrupt or of a
  schema this build does not understand leaves the user in the same predicament,
  a live daemon nobody can stop. When the socket does not answer either, the
  record's own failure is what the caller sees, unchanged — which is what keeps
  `feat daemon restart` declining to spawn a second daemon over a record it could
  not read. Nothing else about stopping changes: it is still `SIGTERM`.
- **The record is published again on an interval for as long as ownership is
  held**, hourly by default, against a threshold of three days. It is a rewrite
  rather than a touch of the timestamps, because a rewrite also restores a record
  that has already been collected, where setting times on a path that no longer
  exists fails and keeps failing. Its contents do not change after `Acquire`, so
  republishing is idempotent, and the write is the same 230-byte atomic
  replacement that startup performs.
- **The keeper belongs to ownership, and `Release` stops it before removing the
  record.** A write landing after the daemon gave up the directory would leave a
  file naming a process that is no longer running, on a system free to reuse its
  identifier — which is the failure ADR-027's evidence 1 exists to prevent, and a
  worse one than the record going missing. Making the keeper part of `Ownership`
  makes that ordering structural rather than a consequence of the order in which
  a serving loop registered its deferred calls.
- **A daemon that answers without a record is one whose record was removed, and
  three messages now say so.** It was modelled everywhere as a daemon in the
  middle of starting, which is a state that lasts microseconds; the state a user
  actually meets is a daemon that has been serving for a week. `Status` gains a
  `RecordMissing` predicate and `Diagnose` a case for it, `AlreadyRunningError`
  distinguishes the two by whether the socket answers, and `feat daemon start`
  and `feat daemon status` name the commands that still reach the daemon instead
  of describing a wait that ended days ago. A record that is present but
  unreadable reaches the same fallback, so it is told apart from an absent one in
  those messages rather than described as one.
- **This amends ADR-027**, which decided the runtime directory's three files and
  where they live. Both loads that decision bears are kept and neither is
  weakened: the runtime directory stays where it is, because its evidence 1 —
  that the state directory survives a reboot and the runtime directory does not,
  so a durable process identifier can outlive the machine's uptime and name an
  unrelated process — is the reason the cleaner is a consequence of the right
  location rather than an argument against it; and the advisory lock remains the
  authority on liveness without a heartbeat, per its evidence 3, with the record
  still a convenience the lock does not depend on. What is added is that one of
  the three files has to stay alive rather than merely be written.

Four things this deliberately does not do:

- **Move the runtime directory off `$TMPDIR`**, for the reason in ADR-027's
  evidence 1, restated above.
- **Delete a record that cannot be read.** `Stop` already declines to remove a
  record whose process is gone, on the reasoning that the next start reclaims the
  directory after checking the lock. That is still the safer decision and it is
  untouched.
- **Weaken the lock**, or make the record anything the lock depends on.
- **Hold the record open for the daemon's lifetime**, which was the preferred
  candidate before evidence 6.

A trigger for revisiting, recorded so the unanswered half of the rule has a name
rather than being forgotten: which property besides age spares `daemon.lock` —
its size or its being a regular file versus a socket — is not established, and
nothing here depends on the answer. A probe of back-dated files was left in this
machine's `$TMPDIR` on 2026-09-14 to settle it against the cleaner's own 03:35
run. If it finds that age is *not* in the predicate after all, the hourly
republish is inert and this decision needs reopening, which is the outcome worth
watching for.

Consequence: `internal/daemon/endpoint.go` gains `askEndpoint`, which turns an
`api.Health` into an `Endpoint`; `internal/daemon/spawn.go`'s `Stop` falls back
to it; `internal/daemon/ownership.go` gains `recordKeeper`, `Ownership.keepRecord`
and `Ownership.republish`, and `Ownership.Release` stops the keeper first;
`internal/daemon/daemon.go` gains `Options.RecordInterval`, following the
convention `Heartbeat`, `PollInterval`, `RuntimeInterval` and `ResourceInterval`
already use, where zero is the default and a negative value turns the thing off;
`internal/daemon/status.go` gains `Status.RecordMissing` and a `Diagnose` case;
`internal/daemon/errors.go` gains `AlreadyRunningError.Answering`; and
`internal/cli/daemon.go` says something actionable in `start` and `status`. The
regression is `TestBinaryStopsADaemonWhoseRecordWasRemoved`, which removes the
record from under a daemon it started and asserts that `stop` and `restart` still
find it — opt-in, because it builds and runs the binary, and there rather than in
`internal/cli` because stopping a daemon signals a process and the only process a
test may safely signal is one it started itself.
