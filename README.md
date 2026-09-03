<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/feat-wordmark-dark.svg">
    <source media="(prefers-color-scheme: light)" srcset="docs/assets/feat-wordmark-light.svg">
    <img width="352" alt="Feat" src="docs/assets/feat-wordmark-light.svg">
  </picture>
</p>

<p align="center">
  A terminal-native development control plane for running feature work<br>
  through several coding-agent sessions in parallel.
</p>

Feat connects a task to the things needed to implement and review it — task
context, selected repositories, branches and worktrees, one native coding-agent
session, an optional isolated agent environment, an optional application
runtime, and review — without replacing the underlying tools.

One task owns one agent session, one set of Git worktrees, and one feature
environment. A task may span several repositories.

> **Status: alpha.** The v0.1 scope is complete and Feat is being used on real
> work, on macOS, with Claude in a devcontainer or on the host. The first
> release, `v0.1.0`, packages that scope for its author and is marked
> pre-release.

## Installing

This version is end-to-end tested on macOS only. Linux compiles and CI runs the
whole suite on `ubuntu-latest` on every commit, but nobody has run a task on a
Linux machine yet, which is why there is no Linux archive below. `go install`
and `make build` build it there; expect bugs.

Feat needs these on the machine:

- **Git** and **tmux**. Feat drives Git for every branch, worktree, and diff,
  and runs every agent session in tmux.
- **[Claude Code](https://code.claude.com/docs/en/setup), installed and
  authenticated** — the agent Feat launches, and the one tool without which
  nothing runs. It has to be where the agent runs: on this machine in host
  mode, inside the image for a devcontainer.
- **The Docker Compose CLI**, only for a devcontainer or an application
  runtime.

`feat doctor` checks all of this and says what to do about what it finds.

There are three ways to get the binary.

**A release archive.** The [releases
page](https://github.com/ma8el/feat/releases) has one `tar.gz` per macOS
architecture — `arm64` for Apple Silicon, `amd64` for an Intel Mac:

```sh
curl -LO https://github.com/ma8el/feat/releases/download/v0.1.0/feat_0.1.0_darwin_arm64.tar.gz
tar xzf feat_0.1.0_darwin_arm64.tar.gz
mv feat_0.1.0_darwin_arm64/feat /usr/local/bin/   # or anywhere on your PATH
feat version
```

The archives are not notarized, so an archive taken through a browser rather
than with the `curl` above is quarantined: clear it with
`xattr -d com.apple.quarantine feat`.

**`go install`**, with the Go toolchain [`go.mod`](go.mod) pins:

```sh
go install github.com/ma8el/feat/cmd/feat@latest
```

**From source.**

```sh
git clone https://github.com/ma8el/feat.git
cd feat
make build   # ./bin/feat
```

Then, in one of your project's checkouts:

```sh
feat doctor          # changes nothing, needs no daemon: run it first
feat project init    # answer the questions; it writes a validated configuration
feat daemon start
feat project add myproject
feat implement       # or `feat` for the dashboard
```

`feat project init` prints those last commands itself, and offers to run the
checks and the registration for you. [Configuring a
project](#configuring-a-project) is the same ground at length.

Additional support for setting up a project: `feat skill install` writes a setup
skill into your own Claude Code session, for editing a project that already
exists or authoring one from scratch; `feat project schema` and `feat project
example` print the schema and a commented example out of the binary, for writing
the file by hand.

## Preparing a task

```sh
feat implement                       # or: feat implement --file task.md
feat implement --ticket ACME-14      # or choose one from the list it offers
feat implement --project myproject   # when several are registered
feat implement --plan                # or press p on the review step
```

Preparation asks where the brief comes from — typed here, composed from one of
the project's tickets, or imported from a Markdown file you have already
written — and `--file` and `--ticket` are answers to that question rather than
the only way to give one: the same import and the same ticket list are on the
step, so `n` in the dashboard reaches both. Whichever source filled it, the
brief is one editable document, and it is that document you confirm.

Preparation then asks which repositories the task may read and write, and shows
what Feat resolved: each repository's immutable base commit, the branch and
worktree it would create, and anything it wants to warn you about. Nothing
exists until you confirm that screen, and confirming creates exactly what it
showed — a draft edited in between is refused rather than launched.

That screen also chooses how the session starts. The default is straight into
the work; `p` — or `--plan` — asks the agent to investigate and propose a plan
first, and to change nothing until you approve it. You read the plan in the
task's own terminal and approve it there, which is where Claude already asks;
the dashboard marks the task as needing you while it waits. Resuming a stopped
session never re-enters plan mode, so work you have already approved is not
planned again.

```sh
feat            # the dashboard: every task, across every project
feat task list  # the same, without a terminal
feat task attach <task>   # or just: feat attach <task>
```

The dashboard is one window: a rail of tasks grouped by project on the left, a
tabbed view of the selected task in the middle, and a footer holding that task's
worktree path and what the machine has left. Each region is a panel with its own
header — the rail says how many tasks want you, the main region says which task
its tabs are about — and the footer is ruled off from both. `tab` moves between
views and `shift+↑`/`shift+↓` change task from any of them — the plain arrows
belong to whichever view has the keyboard, so review spends them on its
repository cursor. `space` folds a project away, and a folded project still says
how many tasks it holds and whether any of them needs you. `p` configures a new
project and `D` checks the selected one against your machine. `?` lists every
key. Preparing a task, configuring a project, reading a diagnosis, or cleaning a
task up opens over the dashboard rather than replacing it, so the tasks you were
watching stay on screen. A terminal too narrow for three regions falls back to
one column.

Opening the dashboard starts the daemon it needs. If that daemon later stops —
because you stopped it, or because it failed — the dashboard says so and offers
to start another rather than starting one behind your back: a daemon that died
may have left tmux sessions and containers running, and the reconciliation pass
of the one that replaces it is where you find out. The offer is made once per
outage; decline it and the footer carries `S`, which puts the question again.
Nothing on screen is destroyed by any of this, and nothing the daemon was
supervising is touched — but what the dashboard is showing stops being current
the moment it stops answering.

Everything that acts on a task you already have is under `feat task`. Attaching
and reviewing are typed often enough to keep their shorter top-level names too.

`<task>` is a task's short key — the eight characters every list prints, in
`feat task list`, in the dashboard, and in every notification. The whole
identifier works too, which is what the dashboard's task detail shows, and so
does any prefix of one. A prefix that matches two tasks is reported with both,
never resolved to either.

Confirming launches Claude Code in the task's primary worktree with the brief
you accepted. Attaching hands your terminal to that session: it is the native
Claude interface, with your own configuration, keybindings, and checked-in
`CLAUDE.md` still applying. Detaching returns you to the dashboard and leaves
the session running.

Feat watches the session through hooks and a per-task control workspace, so the
dashboard distinguishes states the terminal alone cannot: a finished turn
becomes `idle` after a short grace period and never means the work is done, and
a task reaches review only when the agent explicitly asks for it. The first
launch in a new worktree waits for Claude's own workspace-trust prompt; Feat
says a task is waiting rather than answering on your behalf.

A project configured for a devcontainer runs its agent inside one, in a Compose
project of its own, so tasks run side by side. Before starting the agent Feat
inspects that container and refuses one that can reach a container runtime — a
socket, a `docker`, `podman`, or `nerdctl` client, a variable pointing one at a
daemon — or that mounts Feat's own state, your home directory, or the ordinary
checkout of a repository the task is working in. Those are checks on how a
container is configured rather than a guarantee about it: they are no defence
against a deliberate kernel or container-runtime exploit, and nothing here
restricts the network. A project that configures `agent.execution.mode: host`
runs Claude in the task's own worktree instead, with no container boundary
around it at all.

```sh
feat task stop <task>     # put the agent's container to sleep, or press t
feat task resume <task>   # bring it back and continue the same session, or z
```

A task you are not working on today does not need its container running. Stopping
one keeps everything the work lives in — the worktrees, the branches, the control
workspace, the volumes, and the terminal holding what the session printed — and
resuming brings the same containers back and continues the recorded conversation
rather than opening an empty one. The application's services are separate and
stay where you left them.

That pair is the whole lifecycle. There is no command that starts a task's
container by itself: coming back is always a resume of the session that owns it,
which is what keeps every container on your machine something a task can account
for. If one dies on its own, Feat says so, says the session inside it is over,
and offers you the resume.

## Running the application

Each task's services are its own: Feat gives them their own Compose project,
mounts that task's worktrees where the repositories say, and generates the
non-secret variables an application needs to tell which task it is serving.

```sh
feat runtime start <task>     # or create, stop, status, logs, destroy
feat runtime logs <task>      # the ordinary docker compose logs
```

Press `R` in the dashboard for the same actions on the selected task.

What you configure is what Feat starts; what your services need comes up with
them. A database or a migration your services depend on is Compose's to start and
the task's to own, so Feat shows it beside the rest, says it is there as a
dependency, and stops and removes it with the others.

The lifecycle is manual and stays that way. Nothing starts because a task was
launched, nothing stops because it reached review, and approving a task offers
to stop its services rather than doing it. Destroying removes that task's
containers and networks; volumes are always retained, and a resource the project
declares external — a shared staging database, for instance — is never touched.

Feat allocates no ports in this version, so two tasks that both publish the same
host port cannot both be up; the second one says so in those words rather than
passing a Docker error through.

## Reviewing the work

```sh
feat task review <task>   # or: feat review <task>, or press v in the dashboard
```

Review groups the changes by repository and compares each one against the commit
that repository started from, which never moves: however far the branch it came
from has travelled since, what you see is what this task changed. Line counts
cover tracked changes; an untracked file is counted as changed and said to be
untracked, because counting its lines would mean adding it to your index.

Feat renders no diff of its own. It opens the commands you configured, in the
worktree of the repository you selected, and takes the terminal back when you
leave them:

```yaml
review:
  diff:
    command: ["git", "diff", "{base_commit}"]
  editor:
    command: ["nvim", "{repository_path}"]   # or leave it out and Feat uses $EDITOR
  status:
    command: ["git", "status", "--short", "--branch"]
```

If the project configures `checks`, Feat runs them itself when the agent asks for
review — in the environment the agent works in, or on your host where the check
says so — and the task reaches `ready_for_review` only if they pass. A failure
goes straight back to the running session as a failed command with the output
attached, so the agent reads it and carries on. Results Feat ran are marked as
its own; results the agent merely reported are marked as its claim, and the two
never look alike.

Approving records a decision and nothing else. Nothing is stopped, nothing is
removed, and a task whose services are still running is offered the stop rather
than given it.

## Publishing the work

```sh
feat task publish <task>   # or press P on a task's panel in the dashboard
```

Feat opens one merge request per changed repository, from your machine, with the
authentication you already have here. The agent never holds a provider credential
and never opens a merge request: what it writes is a draft — a title and a
description for each repository it changed, and the commit each one describes —
and what you read and edit is what is sent.

```yaml
repositories:
  api:
    forge:
      kind: gitlab   # or github; declared, not guessed from the remote
```

GitLab and GitHub are both supported, per repository: a task spanning a private
repository on one and a public dependency on the other opens a merge request on
each. Feat drives your own `glab` and `gh`, already authenticated on this
machine.

Publishing shows what it would do, opens the draft in your editor, and asks once.
It then pushes each task branch and opens each merge request one repository at a
time, recording every result before the next begins. Nothing is undone: if the
third of five fails, the first two are open, the fourth and fifth are still
attempted, and publishing again skips whatever already has a merge request rather
than opening a second one. A draft describing a commit that is no longer current
is refused rather than published — that is a different answer from "this already
published", and the two never read alike.

`feat doctor` checks that this machine can publish what the project declares:
that the forge's command line is installed and authenticated here, and that Feat
has an adapter for the forge at all. It asks the host in both execution modes,
because that is where publication runs, and nothing asks the agent's own
environment about `gh` or `glab` — the credential is here, not there.

The push runs with hooks, the pager, and external diff commands disabled, because
a task's worktrees share `.git/hooks` with your own checkout and the agent can
write them: approving a publication should not be how you run what the agent left
there. Where a repository has a `pre-push` hook or a configured `core.hooksPath`,
Feat names it before you approve, and `feat doctor` says so at configuration
time — so if that hook is what scans for secrets before anything leaves your
machine, you can push by hand instead.

## Cleaning up

`C` on the dashboard and `feat task cleanup` show the same inventory of what a
task owns — every resource, what it is, and whether it is still there — and
remove only what you select, one class at a time, with volumes retained unless
chosen. Anything that would lose work says so beside the resource it would lose
it on, and again in the confirmation that removes it. Feat also compares what it
recorded with what the machine actually has and reports whatever is missing,
orphaned, or inconsistent, repairing none of it on its own.

## Knowing when to look

The dashboard shows what the machine has left — load against its processor count,
free memory, and free space on the filesystem Feat keeps its state on — and what
each task's own containers and processes are using. Nothing is enforced: Feat
never refuses a task because a machine looks busy, and a figure it could not
measure is shown as absent rather than as zero.

Load rather than a processor percentage, because macOS has no per-core
utilisation figure Feat can read without linking C, and one measure on both
platforms is worth more than two that look alike and are not. A task's container
memory is what the container runtime reported, which on macOS is memory inside
its own virtual machine, so it is shown beside the host-process figure rather
than added into the machine's.

Feat also tells you when a task may need you: an agent that has gone quiet, an
explicit request for review, a failed check, a failed session, and failed
application services. An idle notification waits twice — once for the agent's own
grace period before the task is called idle, and again for
`notifications.idle_grace_period` before it is worth interrupting you — and is
dropped entirely while you are looking at that task's terminal, which Feat asks
tmux rather than assuming from an earlier attach. The text names the task and
says what happened; it never carries your brief, the agent's words, or anything
from your configuration. It is headed by Feat's own mark:

```text
❯ feat · 7f3a1c2e · example
the agent asked for review — Add a scheduled export job
```

What a project decides about being interrupted:

```yaml
notifications:
  desktop: true                  # macOS in this version; Linux arrives with v0.2
  idle_grace_period: 5s          # how long idle before you are told
  suppress_while_attached: true
```

Desktop delivery is macOS-only in v0.1. Feat can tell you it handed a
notification over; it cannot tell you one was shown, because macOS decides that
per application and drops an unauthorised notification without saying so.

A notification Feat sends is attributed to **Script Editor**, which is what
`osascript` posts as, and the icon beside it is Script Editor's for the same
reason — an application's own, and not something the sender chooses. That is
why the heading carries the mark: it is the only part of the notification Feat
writes.

Depending on the macOS version, Script Editor may or may not appear as an entry
under System Settings › Notifications, so if you never see one, the setting is
not always there to check and this is the answer:

```sh
log show --last 5m --predicate 'process == "usernoted"' --style compact \
  | grep ScriptEditor2
```

`Presenting … as banner` means macOS showed it and the question is where you were
looking; no line at all means it never arrived.

A notification Feat decided not to send says so in the daemon's own log, naming
the policy that stopped it — that it was still catching up after a restart, that
this project turned desktop notifications off, that you were attached to the
task, or that this platform delivers none:

```sh
grep 'not interrupting the user' "${XDG_DATA_HOME:-$HOME/.local/share}/feat/logs/daemon.log"
```

Between that and the `usernoted` log above, a notification you expected and did
not get always has an answer.

## Configuring a project

A project is one YAML file, one per project, named after the project's
identifier. Run this in one of its checkouts and answer the questions:

```sh
feat project init                                # write the file by answering questions
```

Pressing `p` in the dashboard asks the same questions as a dialog over your task
list, with `esc` to step back out of an answer and the whole file scrolled in
front of you before it is written. Both are one flow, so neither can drift from
the other: what differs is the cursor, not the questions.

It asks what has to be decided — which repositories take part, where the agent
runs, what verifies the work — and finds out the rest for itself: whether a path
is a Git repository, its remote and default branch, the Compose files beside it
and the services they define. Every proposal is in brackets and Enter accepts
it. The whole file is shown, already validated, before anything is written;
nothing is written until you say so; an existing configuration is never
overwritten. It then offers to check the project against your machine and to
register it, which are the two commands below.

`--dry-run` prints the file it would write and writes nothing.

To write one by hand instead, or to edit the one it wrote:

```sh
$EDITOR ~/.config/feat/projects/myproject.yaml   # see docs/examples/project.yaml
feat doctor                                      # validate it and check the host
feat daemon start
feat project add myproject                       # register it
feat project show myproject                      # what Feat will act on
```

`feat doctor` changes nothing and needs no daemon, so it is the first thing to
run. It reports what it checked, what it found, and what to do about it, and a
check this build cannot run yet is reported as skipped rather than passing.

The dashboard runs the same checks on `D`, for the project of the task you have
selected, and `r` runs them again once you have fixed something. The wizard runs
them itself as soon as it has written a file, because that is the moment they
answer what the questions could not ask. Wherever they are read, the report says
it was checked from your terminal: a tool on this terminal's PATH is not
necessarily on the daemon's.

[`docs/examples/project.yaml`](docs/examples/project.yaml) is a commented
example showing every field with its default; the semantics are in
[docs/07-configuration-model.md](docs/07-configuration-model.md).
[`schema/feat-project.schema.json`](schema/feat-project.schema.json) is a draft
JSON Schema for editor support.

## Documentation

The specification in [`docs/`](docs/) is authoritative and is meant to be read
in order, starting with [`docs/README.md`](docs/README.md).

- [Product vision](docs/01-product-vision.md)
- [User workflows](docs/02-user-workflows.md)
- [Domain model](docs/03-domain-model.md)
- [Functional specification](docs/04-functional-specification.md)
- [Security model](docs/05-security-model.md)
- [Technical architecture](docs/06-technical-architecture.md)
- [Configuration model](docs/07-configuration-model.md)
- [v0 scope](docs/08-v0-scope.md)
- [Roadmap](docs/09-roadmap.md)
- [Decisions and open questions](docs/10-decisions-and-open-questions.md)

## Development

Requirements: Go as pinned in [`go.mod`](go.mod), `make`, Git, and tmux. Projects
that use a devcontainer or application runtime also need the Docker Compose CLI.
`feat doctor` checks the tools required by a project's configuration.

```sh
make check      # everything CI runs: tidy, format, lint, test, test-real, build
make build      # build ./bin/feat
make test       # unit tests with the race detector
make test-real  # the opt-in tests, against real Git, tmux, and Docker
make lint       # golangci-lint, including the architectural boundary rules
make help       # list all commands
```

`make lint` and `make fmt` install the golangci-lint version pinned in
[`.golangci-version`](.golangci-version) into `bin/` on first use.

`make check` includes `make test-real`, which drives the real tools, and it
**demands** the ones named in `INTEGRATION_TOOLS` — Git, Docker and tmux by
default. A demanded tool that is missing or unanswering, a stopped Docker
daemon included, fails the run rather than skipping it: a Go package whose
selected tests all skipped still prints `ok`, so a gate that skipped would
report green having proved nothing. On a machine short of one, say so:

```sh
make check INTEGRATION_TOOLS=git,tmux   # no Docker here, and this run knows it
```

Two further tools are demandable and demanded by nothing: `claude`, whose tests
spend a real account's tokens, and `notify`, which needs a desktop no CI runner
has. Name them on a machine that has them and the proofs behind them stop
skipping quietly:

```sh
make check INTEGRATION_TOOLS=git,docker,tmux,notify
```

### How the design rules are enforced

The architectural and security rules in [`CLAUDE.md`](CLAUDE.md) are checked
mechanically rather than by review attention alone:

- **Import boundaries** are `depguard` rules in
  [`.golangci.yml`](.golangci.yml). The domain package may not import an
  adapter or the TUI, storage may not import the daemon or the UI, clients may
  not touch persistent state, and process execution is confined to adapter
  packages. Changing one of these rules is an architectural change.
- **No reference-project identifiers** reach the binary, checked by a test over
  every Go string literal in the repository. The denylist and its exemptions
  live in
  [`internal/guard/testdata/reference-identifiers.txt`](internal/guard/testdata/reference-identifiers.txt).
- **No shell interpolation** for Git, tmux, or Docker Compose, checked by an
  AST test over every `exec.Command` call.
- **No TCP listener or dial**, checked by an AST test over every `net` and
  `net/http` call in the repository, tests included. The local API is a
  Unix-domain socket only.
- **Only the daemon reaches persistent state**, checked by an import test over
  every non-test file, so a package added later is covered without anyone
  remembering to extend the lint configuration.
- **The command surface** is pinned by a golden file, so the published command
  model cannot drift silently. Update it with `make golden`.
- **The JSON Schema and the configuration structs** are compared in both
  directions, so a field that exists in one and not the other fails in
  `go test` rather than in a user's editor. The documented example is validated
  by the same suite.
- **The completion gate proves something**, checked by tests over the
  `Makefile` and the CI workflow: `make check` must run the integration tier,
  and both must pass `-count=1` and name the tools they demand. Neither
  property is visible to a Go test suite, and both were once wrong — a green
  `make check` on a laptop with Docker stopped, and a CI re-run that replayed a
  cached pass without touching a tool.
- **Every gated test is one the runner selects**, checked by an AST test: a
  test that refuses to run without `FEAT_INTEGRATION` but is not named
  `TestReal…` or `TestBinary…` runs in neither tier, and nothing else would say
  so.
- **Every skip the gated tier can reach is demandable**, checked by its sibling
  AST test: a gated test — or any helper one reaches — that gives up on this
  machine must do so through `integrationtest.Unavailable`, so the run's demand
  list can turn it into a failure. A bare `t.Skip` there prints `ok` for the
  package, which is how a run that demanded Docker and got one that could not
  start a container reported green.

## License

Apache 2.0. See [LICENSE](LICENSE). No telemetry.
