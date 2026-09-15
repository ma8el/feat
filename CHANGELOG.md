# Changelog

Written by hand, one section per tag, newest first. Each section is the body of
its own GitHub release: `.goreleaser.yaml` publishes the section for the tag it
is building, so what is written here is what a reader sees on the release page.

## v0.1.1 — 2026-09-15

Four fixes and four additions. The fixes are all things that happened while using
it: a task stuck showing `running` after its agent had gone idle, a review
request arriving while its checks were still running, a cleanup that could not
delete a branch and then could not archive the task, and a daemon that could not
be stopped once it had been up for three days. The last two had no way out but
the shell.

The additions are creating a task from a script with no terminal, `--json` on the
commands that report, a setup wizard that asks where a repository publishes and
what command prints your tickets, and a `feat doctor` check for a mount that
would stop a task before it started.

Everything `v0.1.0` said about itself still holds: pre-release, macOS, Claude
Code, no telemetry, and no compatibility promise about the configuration or the
command line.

### Fixed

**A task that said `running` while its agent sat idle.** Refreshing the dashboard
wrote what tmux could see over what Claude's hooks had reported, and tmux cannot
tell a running session from an idle one. Reconciliation now says only what it can
see: a live pane under a session already recorded as alive is left alone. A
resumed session that never reports is marked as needing you, and an ended turn
survives a daemon restart inside its grace period (ADR-096).

**A review request that arrived while its checks were still running.** The same
refresh told a task whose checks were running that they had been interrupted by a
daemon restart that never happened — after which the results were recorded, the
task stayed where it was, and nobody was told the checks had passed.
Reconciliation now asks whether a gate is actually running before it says one was
interrupted, and a gate that cannot start reaches you instead of the log
(ADR-096).

**A cleanup that could not delete a branch, and a task that could then never be
archived.** Feat asked whether a branch was contained by the ref it branched
from; `git branch -d` asks whether it is contained by the checkout's HEAD. On any
checkout that has fetched, the two disagree, so Feat saw nothing at risk, offered
no confirmation, and sent a command Git refused — while archiving was refused
over the branch still present. Deletion now follows the containment Feat
established, and records what it forced on (ADR-097).

**A daemon that outlived its own endpoint record.** On macOS a daemon up for more
than three days lost `endpoint.json` to the system's temporary-directory cleaner,
kept serving, and became one `feat daemon status` could describe and `feat daemon
stop` reported did not exist. The record is now republished hourly, and `stop`
asks the daemon over its socket when the record is missing or unreadable
(ADR-101).

### Added

**Creating a task without a terminal.** An invocation that says what the task is
creates it: `--project` and either `--brief` or `--file`, with `--file -` reading
the brief from a pipe. The confirmation is the invocation. `--dry-run` prints the
same proposal and creates nothing, `--tui` opens the preparation screen when the
flags would otherwise have been enough, and everything that worked before works
unchanged. There is no `--yes`: a flag that skips a confirmation somebody was
shown is the unattended path v0 excludes, and `--ticket` still needs a terminal,
because what you approve is the brief Feat composed rather than the ticket it came
from (ADR-099).

**`--json` on the commands that report.** `feat task list`, `feat task review`,
`feat runtime status`, `feat project show`, and `feat implement` print a document
instead of a table. Standard output carries one document or nothing; an error
goes to standard error and is said by the exit code. The shape is described by
`schema/feat-output.schema.json` and held to the Go types by a test — it is
deliberate and visible, and it is promised by nothing until the public preview
decides what a caller may rely on (ADR-099).

**The wizard reaches the forge and the tracker.** `feat project init` asks where
each repository a task may write to publishes its merge requests, proposing the
forge its remote names where that host is `github.com` or `gitlab.com` and saying
so — a self-hosted GitLab or a GitHub Enterprise instance is not guessable, and
is asked about rather than guessed at. It also asks, last and optionally, for the
command that prints your tickets. The application is now answered before the
agent's environment, which is what lets the agent's Compose question offer the
files no repository claimed, and the managed-services question proposes the
services that run a repository's code rather than every service its files declare
(ADR-100).

**A mount that would stop a task before it started.** `feat doctor` reports a bind
mount writing a file into where Feat mounts a task's worktree, on a path the
worktree would not hold — the masking pattern that stopped a task launching on a
real project. Where Feat establishes that this machine's container runtime
refuses such a mount point it fails the diagnosis; where it has not, it warns and
names both outcomes, because a native Linux daemon creates the file and starts
the container (ADR-098).

### Changed

`feat task list` leaves archived tasks out of the table **and** the document
unless `--all` is given, and counts them either way. Before this, the table hid
them and a script saw every one.

`feat doctor` can now report something it did not before, and on Docker Desktop
it can fail a project that previously passed. The mount it names would have
failed at container creation.

A brief that is blank is refused where you typed it, whichever flag named it,
rather than creating a task with nothing in it.

### Installing

```sh
go install github.com/ma8el/feat/cmd/feat@v0.1.1
```

Or download the archive for your architecture from the release page.
`checksums.txt` beside it is what there is to verify it with; the archives are
not code-signed or notarized.

## v0.1.0 — 2026-09-03

The first release. Nothing was tagged before it, so this entry says what
`v0.1.0` is rather than what changed since something else. It packages the
completed v0.1 dogfood scope for its author and widens nothing (ADR-090).

### What Feat is

A terminal-native control plane for running feature work through several
coding-agent sessions in parallel. One task owns one agent session, one set of
Git worktrees, and one feature environment, and a task may span several
repositories. Feat connects those things and replaces none of the tools
underneath: it drives your Git, your tmux, your Docker Compose, your `gh` and
`glab`, and your Claude Code, and it renders no diff of its own.

It is a single binary. `feat` opens a dashboard, and everything the dashboard
does is also a command. The daemon behind it listens on a Unix-domain socket and
on nothing else, keeps its state in files you can read, sends no telemetry, and
is Apache 2.0.

### What you can do with it

**Configure a project.** A project is one YAML file naming the repositories a
task may work in, where the agent runs, and what verifies the work.
`feat project init` — or `p` in the dashboard — asks what has to be decided,
finds the rest out from your checkouts, and writes a file it has already
validated. `feat doctor` checks that file against this machine and changes
nothing.

**Prepare and launch a task.** `feat implement` takes a brief you type, import
from Markdown, or compose from one of your own tickets — a project points
`tracker.command` at a command of yours that prints them as JSON, run on your
machine with your own authentication. You then read and edit the composed brief,
choose which repositories the task may touch, and see the branch, worktree, and
immutable base commit Feat would create for each one before anything exists.
Confirming creates exactly what it showed, and launches Claude Code there —
straight into the work, or into a plan you approve first.

**Watch it without watching it.** Feat follows the session through Claude's
hooks and a per-task control workspace rather than by reading the terminal, so
`idle` means the turn ended and never means the work is done; a task reaches
review only when the agent asks. The dashboard shows every task across every
project, what the machine has left, and what each task is using. On macOS, a
notification tells you when a task may need you, and is dropped while you are
looking at it.

**Run the application.** Each task's services get their own Compose project and
its worktrees mounted where the repositories say. Create, start, stop, status,
logs, and destroy are yours to run; nothing starts or stops on its own.

**Review and publish.** Review compares each repository against the base commit
that task started from and opens the diff and editor commands you configured. If
the project declares checks, Feat runs them itself when the agent asks for
review and holds the task back until they pass — results it ran are marked as
its own, and results the agent merely reported are marked as its claim.
Publishing opens one merge request per changed repository from your machine with
your credentials: the agent drafts the words, you read and edit them, and what
you read is what is sent. The agent never holds a provider token.

**Recover and clean up.** Feat compares what it recorded with what the machine
actually has, reports what is missing, orphaned, or inconsistent, and repairs
none of it on its own. Cleanup lists every resource a task owns and removes only
what you select, keeps volumes unless you choose them, and says beside a
resource when removing it would lose work.

### Running it

macOS, with Git, tmux, and Claude Code installed. A project that uses a
devcontainer or an application runtime also needs the Docker Compose CLI on the
host; Feat gives the agent's container no access to a container runtime, and
refuses to start one that has it.

Download the archive for your architecture from the release page and put `feat`
on your `PATH`, or build it yourself with Go 1.26 or newer:

```sh
go install github.com/ma8el/feat/cmd/feat@v0.1.0
```

Then `feat doctor`, `feat project init`, and `feat` for the dashboard.
`feat skill install` puts a Claude Code skill in your own session for authoring,
editing, and troubleshooting that configuration, with the binary you installed
as the authority on its own fields.

### What it does not claim

**It is marked pre-release.** The repository is public, so the choice was
between advertised and unadvertised rather than visible and hidden. This is
usable software that one person uses daily; it is not software that has been
installed by somebody who did not write it.

**macOS only.** Linux is compiled and tested on every commit and has never been
run in anger, and it has no desktop notifications, which is how a task tells you
it wants you. `go install` will build it there for whoever wants to try; the
release does not claim it works. Linux is v0.2, and so are a Homebrew tap and
any apt path.

**The configuration and the command line may change without deprecation before
v0.2.** Project YAML, the settings file, and the command surface are what a
public preview would generalize, and this release does not freeze them. Nothing
here is a compatibility promise.

**Claude Code is the only agent**, the runtime lifecycle is entirely manual,
Feat allocates no host ports — two tasks publishing the same port cannot both be
up, and Feat says so rather than passing a Docker error through — and the checks
Feat makes on an agent container are checks on how it is configured. They are
not a defence against a deliberate kernel or container-runtime exploit, and
nothing restricts what the agent reaches over the network.

The archives are not code-signed or notarized, and `checksums.txt` beside them
is what there is to verify them with.
