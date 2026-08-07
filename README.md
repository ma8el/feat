# Feat

Feat is a terminal-native development control plane for running feature work
through several coding-agent sessions in parallel. It connects a task to the
things needed to implement and review it — task context, selected repositories,
branches and worktrees, one native coding-agent session, an optional isolated
agent environment, an optional application runtime, and review — without
replacing the underlying tools.

One task owns one agent session, one set of Git worktrees, and one feature
environment. A task may span several repositories.

> **Status: pre-alpha.** Slices 0 to 9 of the
> [implementation plan](docs/11-implementation-plan.md) are complete. The
> repository has its package skeleton, the full command
> surface, its development and CI commands, a versioned domain model with
> file-backed storage, a local daemon serving a JSON API and a state-event
> stream over a Unix-domain socket, YAML project configuration with
> diagnostics, and the Git and worktree lifecycle that gives a task its
> branches and worktrees across several repositories. It also has a dedicated
> tmux backend with tagged stable identity, native attachment, shell-pane
> creation, and daemon-restart reconciliation.
>
> You can prepare, confirm, launch, list, and inspect a task: `feat` opens the
> dashboard, `feat implement` opens task preparation, and `feat attach` yields
> your terminal to a task's own. **A task now runs a real Claude Code session**
> in a devcontainer or on the host, reports its lifecycle through a
> task control workspace, and goes idle only after a grace period — idle never
> means done, and only an explicit request from the agent reaches review.
>
> A project configured for a devcontainer now runs its agent inside one: Feat
> starts the configured Compose service, mounts each task worktree at the
> container path its repository configures, mounts the task's control workspace,
> and launches Claude there as the configured non-root user. It refuses to start
> an agent in a container that has a Docker socket, a Docker client, or a mount
> of your ordinary checkout, and each task gets its own Compose project so tasks
> run side by side. Set `FEAT_HOST_AGENT=1` in the daemon's environment to run
> Claude directly on your host instead, with no container boundary around it.
>
> A task's **application services** are now yours to run from Feat: create,
> start, stop, status, logs, and destroy, each under that task's own Compose
> project. Nothing starts on its own and nothing stops because a task reached
> review or approval. Review and cleanup commands are registered but not
> implemented, and each reports the slice that delivers it. Nothing here is
> usable for real work yet.

## Preparing a task

```sh
feat implement                       # or: feat implement --file task.md
feat implement --project myproject   # when several are registered
```

Preparation asks what the task is, which repositories it may read and write,
and then shows what Feat resolved: each repository's immutable base commit, the
branch and worktree it would create, and anything it wants to warn you about.
Nothing exists until you confirm that screen, and confirming creates exactly
what it showed — a draft edited in between is refused rather than launched.

```sh
feat            # the dashboard: every task, across every project
feat task list  # the same, without a terminal
feat attach <task>
```

`<task>` is a task's full identifier. The lists show the short key derived from
it, and the dashboard's task detail shows the whole thing — so that is where to
copy it from until a later slice lets a command take the key.

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

## Configuring a project

A project is one YAML file, one per project, named after the project's
identifier:

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
- [Implementation plan](docs/11-implementation-plan.md)

## Development

Requirements: Go as pinned in [`go.mod`](go.mod), `make`, Git, and tmux. Projects
that use a devcontainer or application runtime also need the Docker Compose CLI.
`feat doctor` checks the tools required by a project's configuration.

```sh
make check    # everything CI runs: tidy, format, lint, test, build
make build    # build ./bin/feat
make test     # unit tests with the race detector
make lint     # golangci-lint, including the architectural boundary rules
make help     # list all commands
```

`make lint` and `make fmt` install the golangci-lint version pinned in
[`.golangci-version`](.golangci-version) into `bin/` on first use.

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

## License

Apache 2.0. See [LICENSE](LICENSE). No telemetry.
