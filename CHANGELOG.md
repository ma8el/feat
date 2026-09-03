# Changelog

Written by hand, one section per tag, newest first. Each section is the body of
its own GitHub release: `.goreleaser.yaml` publishes the section for the tag it
is building, so what is written here is what a reader sees on the release page.

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
