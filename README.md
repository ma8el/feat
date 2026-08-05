# Feat

Feat is a terminal-native development control plane for running feature work
through several coding-agent sessions in parallel. It connects a task to the
things needed to implement and review it — task context, selected repositories,
branches and worktrees, one native coding-agent session, an optional isolated
agent environment, an optional application runtime, and review — without
replacing the underlying tools.

One task owns one agent session, one set of Git worktrees, and one feature
environment. A task may span several repositories.

> **Status: pre-alpha.** Slices 0 to 4 of the
> [implementation plan](docs/11-implementation-plan.md) are complete: the
> repository has its package skeleton, the full command surface, its development
> and CI commands, a versioned domain model with file-backed storage, a local
> daemon serving a JSON API and a state-event stream over a Unix-domain socket,
> YAML project configuration with diagnostics, and the Git and worktree
> lifecycle that gives a task its branches and worktrees across several
> repositories.
> `feat daemon start|stop|status`, `feat doctor`, and
> `feat project add|list|show` work. The commands that create a task are
> registered but not implemented; each one reports the slice that will deliver
> it, and slice 6 is the one that will let you confirm a task draft and reach
> the Git lifecycle from the command line. Nothing here is usable for real work
> yet.

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

Requirements: Go as pinned in [`go.mod`](go.mod), plus `make`. Later slices add
runtime prerequisites (Git, tmux, and the Docker Compose CLI); `feat doctor`
will check them once it exists.

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
