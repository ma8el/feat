# Configuration Model

## Format decisions

- Human-authored configuration: YAML
- Machine-authored state: JSON
- Append-only event history: JSON Lines
- Task briefs and reports: Markdown

YAML is selected because Feat configuration is hierarchical and users of the runtime feature already work with Docker Compose. JSON remains preferable for generated state because it is strict and unambiguous. TOML is not selected because multi-repository and nested runtime configuration becomes table-heavy.

Feat must parse YAML with strict unknown-field rejection and publish a JSON Schema for editor support. `feat doctor` performs semantic validation.

Strictness covers both ways a hand-edited file silently loses a value: a field Feat does not know, and a key given twice. Rejection reports the line, the column, and the surrounding text. The schema is published at `schema/feat-project.schema.json` and a documented example at `docs/examples/project.yaml`; both are held to the implementation by tests. See ADR-028.

## Local configuration

Initial location:

```text
~/.config/feat/projects/<project-id>.yaml
```

The file name carries the project identifier and must match `project.id`, so that one project is never described by two answers to the same question. `.yml` is accepted; a project configured by both extensions is an error rather than a preference.

The file may be written by hand or composed by `feat project init`, which asks for what has to be decided and derives the rest from the host. What it writes is what was decided: a field Feat has a default for is left out, because a default written down is a value that stops following Feat when Feat's own changes, and `feat project show` prints the resolved configuration with every default filled in. See ADR-062.

v0.1 is local-only. A later optional `.feat.yaml` may hold shareable repository conventions, but absolute machine paths and credential references remain local.

## Illustrative schema

The exact Go structs may evolve during the first implementation slice, but the semantics below are accepted.

```yaml
version: 1

project:
  id: app
  name: Application
  primary_repository: dashboard

repositories:
  dashboard:
    host_path: ~/projects/app/dashboard
    agent:
      container_path: /workspace/dashboard
    runtime:
      compose_files:
        - docker-compose.yml
        - docker-compose.dev.yml
      container_path: /app
      services:
        - frontend
      reachable:
        - frontend
    default_branch: main
    remote: origin
    default_access: read_write

  database:
    host_path: ~/projects/app/database
    agent:
      container_path: /workspace/database
    runtime:
      compose_files:
        - docker-compose.yml
      container_path: /srv/database
      services:
        - backend
    default_branch: main
    remote: origin
    default_access: selectable

  devcontainer:
    host_path: ~/projects/app/devcontainer
    agent:
      container_path: /workspace/devcontainer
    default_branch: main
    remote: origin
    default_access: stable_read_only

git:
  base_policy: remote
  fetch_before_task: true
  branch_template: "feat/{task_key}-{slug}"
  worktree_root: "~/.local/share/feat/worktrees/{project_id}/{task_id}"

agent:
  provider: claude
  execution:
    mode: devcontainer
    compose_files:
      - ~/projects/app/devcontainer/docker-compose.yml
      - ~/projects/app/dashboard/docker-compose.yml
    service: dev
    user: developer
    working_directory: /workspace/dashboard
    control_path: /feat

  claude:
    config_volume: feat-claude-config
    config_path: /feat-claude
    idle_grace_period: 5s

  capabilities:
    docker: denied
    network: unrestricted
    git: full
    github_cli: optional
    gitlab_cli: required

runtime:
  provider: compose
  start_policy: manual
  static_overrides: []
  env_files:
    - ~/projects/app/dashboard/.env
  project_name_template: "feat-{project_id}-{task_id}"
  port_range: "21000-21999"

review:
  diff:
    command: ["git", "diff", "{base_commit}"]
  editor:
    command: ["nvim", "{repository_path}"]
  status:
    command: ["git", "status", "--short", "--branch"]

checks:
  dashboard:
    - id: test
      command: ["pytest"]
      execution: agent

notifications:
  desktop: true
  idle_grace_period: 5s
  suppress_while_attached: true

resources:
  sample_interval: 2s
```

## Configuration semantics

### Repository access modes

- `read_write`: selected by default and receives a task branch/worktree.
- `read_only`: selected by default, receives a task worktree, and is mounted read-only.
- `selectable`: task preparation requires the user to choose omitted, read-only, or read-write.
- `stable_read_only`: use the configured stable checkout read-only unless the task explicitly promotes it to a task repository.
- `omitted`: not included by default.

### Base policies

- `remote`: resolve configured remote/default branch after fetch; recommended default.
- `local`: resolve the local default branch.
- `current`: resolve the ordinary checkout's current commit.
- `explicit`: user supplies a ref/commit during task preparation.

Feat records the resulting commit, not merely the ref name.

### Execution mode

`host` requires an agent working directory in the primary task worktree.

`devcontainer` requires Compose inputs, service, non-root user expectation, container working directory, and container control path. The configured Compose service may reference application files, but application runtime lifecycle remains separate.

`repositories.<id>.agent.container_path` must be the path the devcontainer's own
Compose files already mount the repository at. Compose merges a service's mounts
by target, so Feat's generated override replaces that mount with the task's
worktree; a path that disagrees adds a second mount instead and leaves the agent
holding the user's ordinary checkout as well as its own worktree. Feat refuses a
launch whose container turns out to mount a configured repository checkout, but
the configuration is where the mistake is fixed.

It applies to `devcontainer` execution only and is rejected under `host`, which
has no container around the agent at all. Where the *application's* services
expect a repository's source is a separate field on the same repository —
`repositories.<id>.runtime.container_path` — and it applies in both modes,
because an application has containers whatever the agent does. See ADR-065.

### Claude configuration

`agent.claude.config_volume` is optional. When it is set, Feat mounts that named
volume at `agent.claude.config_path`, which defaults to `/feat-claude` and is
validated like `control_path`, and sets `CLAUDE_CONFIG_DIR` to it — one
interactive login shared by every task rather than the user's own `~/.claude`
exposed to every container.

When it is not set, Feat mounts nothing and sets nothing, and the provider's
configuration is whatever the project's own Compose files provide. A project
that mounts the user's host `~/.claude` itself is making the explicit choice
[05-security-model.md](05-security-model.md) permits, and Feat does not
second-guess it.

### Provider CLI capability

Each provider capability supports:

- `disabled`: Feat neither expects nor validates it.
- `optional`: `doctor` reports availability/authentication but does not fail.
- `required`: task launch fails validation when executable/authentication is absent.

Validation occurs inside the same execution environment where Claude will run the command. Slice 7 therefore validates them for host execution, where that environment is the host, and slice 8 validates them for devcontainer execution once there is a container to run them in. A check this build cannot run is reported as skipped rather than passing, and names the slice that delivers it (ADR-028, ADR-032).

### Capabilities Feat cannot vary

`docker`, `network`, and `git` accept one value each — `denied`, `unrestricted`, and `full`. Feat has no mechanism that grants an agent Docker, restricts its network, or limits its Git access, so any other value would record a promise the binary does not keep. The declaration is still made, because the execution adapter checks the running container against it. See ADR-028.

### A runtime is composed of its repositories

An application's Compose files belong to the repositories that bring them, so
that is where they are configured. `repositories.<id>.runtime.compose_files`
lists what one repository brings, resolved against that repository's own
checkout, and Feat generates the Compose `include` document that joins them —
one entry per repository, each carrying its own `project_directory`.

Listing two repositories' files in one place instead does not work, and this is
measured rather than reasoned: Compose resolves the relative paths in every file
against a single project directory, so a second repository's `build: .` builds
from the first repository's checkout. Nothing relative may cross a repository
boundary, and the generated include is what keeps it from having to (ADR-065
evidence 2 and 3).

Feat's own Compose project directory is the directory holding the documents it
generates. Every path in them is absolute, and each include entry names the
directory its own repository's relative paths resolve against, so a project
directory belonging to one of the repositories could only be the directory a
second repository's paths were wrongly resolved against. One consequence is
worth stating: Compose's implicit `.env` lookup beside a repository does not
apply, so an environment file a project needs is named in `runtime.env_files`.
The same is true of a relative path inside a `runtime.static_overrides` file,
which is why those are best written absolute.

`repositories.<id>.runtime.services` names the services that run that
repository's code, which are the services a create and a start target. Two
repositories may name one service: a service running an application and a shared
library it depends on runs the code of both, and each repository's worktree is
mounted at its own container path. A service receives the worktrees of the
repositories that named it and no others, because a service that runs one
repository's code has no reason to hold another's — and mounting every worktree
into every service would make two repositories expecting their source at the
same path a collision rather than the ordinary arrangement it is.

`repositories.<id>.runtime.reachable` names the services of that repository a
user reaches from the host. Feat allocates a host port for each publication such
a service declares, per task, from `runtime.port_range`; it reads the container
port out of the project's own Compose files structurally, publishes the service
there in the generated override, and releases the port when that task's runtime
is destroyed. A service it cannot read a publication for — an interpolated entry,
a port range, or no published port at all — publishes nothing, and the task says
which services those are rather than leaving an address that answers nothing.

Every other service publishes nothing, whether the project manages it or not. A
published port is global to the machine exactly as a container name is global to
the Docker daemon, so one left as configured is one task at a time — which is
the whole failure allocation removes. A service you reach from the host is
therefore one to manage and declare reachable, which is how it is given a port of
its own.

`runtime.port_range` is written `<first>-<last>` and defaults to `21000-21999`:
a thousand ports above the privileged range and below the ephemeral ports the
kernel hands out, so an allocation needs no privilege and collides with nothing
the machine opened for itself. A project that already uses them says so.

### Runtime ownership

Every runtime resource Feat models is one it created, observes, and may remove.
A `shared` lifecycle, product-managed with explicit isolation semantics, is
roadmap work.

The managed services are not the whole of what runs: Compose starts whatever
those services depend on, and everything it starts belongs to the task's own
Compose project. Feat therefore owns all of it — a stop, a status, a logs, and a
destroy address the project rather than the list — and a status says which
services the project named and which are there because another service needs
them. A service Feat was not asked to manage is not given the task's worktrees or
the generated variables; it is given its `container_name` reset and Feat's
ownership labels, without which two tasks could not run the same application at
once (ADR-034).

Feat sets `FEAT_PROJECT_ID`, `FEAT_TASK_ID`, `FEAT_TASK_KEY`, and
`FEAT_RUNTIME_PROJECT` on every managed service, and `FEAT_URL_<service>` and
`FEAT_PORT_<service>` for each reachable one — upper-cased, with everything that
is not a letter or a digit replaced. A configuration in which two reachable
service names produce the same variable is rejected, because one service would
receive the other's address. A service publishing more than one port also gets a
pair per port, named by the container port, since the unsuffixed pair can only
name one of them. They are generated task metadata and never a value read from
an environment file.

The addresses reach the Compose process as well as the containers. A service
finds its siblings under the project's own names — a frontend whose framework
exposes only variables with a particular prefix cannot read `FEAT_URL_api` — so
the project maps one in its own Compose file with `${FEAT_URL_api}`, and Compose
interpolates that from the environment of the process running it. Nothing read
from an environment file is ever in that environment.

`FEAT_TASK_KEY` is the one a project shares an external resource by — a staging
PostgreSQL database on a server of its own, say. It is short, unique, safe in a
name, and not a secret, so an application can use it to name its own share.
Naming a share is all Feat contributes: it neither creates, migrates, drops, nor
reclaims anything behind that name, and it cannot, because the connection string
lives in an `env_files` entry Feat passes to Compose by path and never opens.
What a project makes of the name is the project's (ADR-048).

`repositories.<id>.runtime.container_path` must be where that repository's own
Compose files mount it, so that Feat's generated override replaces that mount
rather than adding a second one beside it. A path that disagrees leaves the
services running the user's ordinary checkout with every record Feat keeps still
correct. Feat inspects the started containers and reports that in its own terms
rather than refusing the start: the application runtime is inside the trusted
host, so it is a correctness problem rather than a boundary breach (ADR-034).

### Where a service's code comes from

A mount is not the only way a repository's code reaches a service. A service
whose image copies the repository in has no mount to replace, and a container
path decides nothing about it: what it runs was decided when the image was built.
For such a service Feat points the build context at the task's worktree — at the
same place inside it, so a context of `./web` becomes the worktree's own `web` —
and writes only the context, so a relative `dockerfile:` beside it follows.

The build contexts are read out of the project's own Compose files, structurally
and never interpolated, exactly as the proposals are. `docker compose config`
would answer the same question by rendering the project including the values of
its environment files, which Feat must never read.

The task records, per managed service, which repositories' worktrees it mounts
and which it builds from. A service with neither runs whatever the project's own
files give it, and Feat says so when the runtime is created rather than after it
is started; a service that only builds from a worktree runs the task's code and
goes on running the copy it was built from, so Feat says that too. `feat runtime
create` rebuilds, which is what makes such a change appear — the agent cannot,
having no Docker.

A `container_path` is therefore not required of a repository whose services bake
their code, and giving one a path its services do not use would add a mount for
nothing. What is refused, at configuration load, is a runtime that could mount no
task worktree at all: a project that configures a runtime, whose repositories a
task selects, and where no repository says where its source goes. That
configuration can only produce services running the user's ordinary checkout, and
it needs neither Docker nor a file to diagnose (ADR-065 evidence 1).

### What a project's own Compose files must provide

The rules above are stated from Feat's side — what it mounts, redirects, resets,
and publishes. This is the same set from the other side: what a project's own
Compose files have to look like for a task to run its own code and be reachable.
Five things are required of them, and each one is silent when it is missing,
which is why they are collected here rather than left to be assembled from the
sections above.

- **Each repository's source arrives at one container path, and it is the one
  `runtime.container_path` names.** Compose merges a service's volumes by target,
  so Feat's generated override replaces a mount only where the targets agree; a
  path that disagrees adds a second mount and leaves the services running the
  ordinary checkout. A service that bakes its code with `COPY` needs no mount and
  no container path: its build context is redirected instead.
- **A reachable service's published port is written plainly.** Feat reads the
  container side of the publication and replaces the host side with a port
  allocated for the task. An entry containing a `${...}` is one Feat must not
  resolve, and a port range is several publications where an allocation is one;
  either way the service publishes nothing and the task says so.
- **Nothing names a sibling by a fixed host port.** The port differs per task, so
  a value baked into an image or written into a file can be right for one task
  at most. Both directions need the generated address: a service calling another
  reads `${FEAT_URL_<service>}`, and a service that must accept another's origin —
  a CORS allow-list — reads the same variable for the service the browser loads.
  Write them with a `:-` default so the files still work run by hand.
- **Every environment file is named in `runtime.env_files`.** Feat's Compose
  project directory is the directory holding its own generated documents, so
  Compose's implicit `.env` lookup beside a repository does not apply.
- **The files a repository brings are the ones that reload.** A production file
  that bakes its source into an image serves what it was built from, whatever is
  mounted over it, so a project that keeps a development overlay beside it brings
  the overlay.

What a project should *not* write is as load-bearing, because a workaround left
in place reads as a requirement. Feat resets `container_name` on every service in
the task's Compose project, replaces every published port, gives the project a
per-task name so its named volumes are per task too, applies its own ownership
labels, and redirects a build context into the task's worktree. A file that
resets a port or renames a container for itself is doing something Feat has
already done.

One thing this list deliberately leaves to the project: whatever the image put
inside the path the worktree is mounted at — an installed virtualenv, a
`node_modules` — has to be moved out of it or shadowed by a named volume, or the
mount hides it. That is what mounting source over a built image costs, and Feat
neither checks it nor needs it.

### Notifications and resource sampling

Two grace periods exist and they are measured from different moments, which is
what tells them apart.

`agent.claude.idle_grace_period` decides when an ended turn becomes `idle`: a
turn that ends and immediately continues is not a session waiting for anybody.
`notifications.idle_grace_period` decides how long a task must have *been* idle
before Feat interrupts the user about it, measured from that idle transition. So
the dashboard reports idle after the first period and a desktop notification
arrives after both, and a project that wants to be told only about a long silence
raises the second one alone.

Measuring both from the end of the turn was the other reading and is rejected: a
notification grace shorter than the provider's would expire before the task was
idle, and no notification would ever be delivered. See ADR-035.

`notifications.desktop` turns desktop delivery off without touching the
dashboard's own attention badges, which are rendered from task state.
`notifications.suppress_while_attached` drops a notification about a task the
user is already looking at, which Feat asks tmux per window rather than
remembering that somebody once attached.

`resources.sample_interval` is how often the machine and each task's containers
and processes are observed. It is per-project configuration for a machine-wide
measurement, so the shortest interval any registered project asks for is what the
machine is asked for, floored at one second and at however long the previous
sample took — asking the container runtime what it is using takes between one and
two seconds whatever it is asked. Sampling never blocks a request or a task.

### Configuration Feat derives

`feat project init` reads a project's own Compose files structurally to propose
configuration: service keys, the container targets of bind mounts whose source
is the repository itself, `build.context`, and published `ports`. It never
resolves interpolation — an entry containing a `${...}` is a value Feat could
not derive, and the user is asked instead — and it never reads an `environment`
value, a `build.args` entry, or an `env_file`.

The rule is not who reads but where a value comes to rest: a derived value
becomes configuration only when the user accepts it into their own YAML, and
nothing Feat inferred is persisted in Feat's own state. Reading a document
resolves nothing, which is what separates this from `docker compose config` —
that command renders the values of a project's environment files into its
output, and no path to a diagnosis runs it (ADR-034 evidence 5, ADR-065).

### Secrets

Configuration may contain secret file paths but not copied secret values. Generated task overrides contain only generated non-secret values unless a future secret-provider interface explicitly handles otherwise.

## Generated task configuration

For every confirmed task, the daemon resolves configuration into an immutable launch snapshot containing:

- source config version/hash;
- selected repositories and access;
- base commits;
- branch/worktree paths;
- exact Compose file list;
- exact runtime project name;
- agent command specification;
- review command specifications;
- enabled capabilities.

Later edits to project YAML do not silently mutate an active task. The user may explicitly reconcile or recreate it.

## Generated Compose override

The generated override may contain:

- task worktree bind sources targeting configured container paths;
- task control-workspace mount;
- generated non-secret environment variables;
- allocated host ports in automated-runtime versions;
- generated labels for project/task ownership;
- named-volume/network adjustments when configured.

It should not contain copied secrets or unnecessary `container_name` fields.

The agent execution override additionally resets `container_name` and `ports`,
unconditionally and in that document rather than by inspection. Both are global —
a container name to the Docker daemon, a published port to the host — so a base
file carrying either cannot be started twice, and one task per machine is not the
product. The reset is stated in the task detail rather than done quietly.

It applies to every service the project's own Compose files define, not only to
the one the agent runs in. Starting that service starts whatever it depends on,
and everything Compose starts is in the task's own Compose project; a dependency
that kept either would be the same collision arriving one service over. A service
the agent does not run in gets those two lines and nothing else — no task
worktree, no generated variable, and no ownership label, because Feat's labels
are how the container the agent does run in is found.

Published ports are reset in both documents, for the same reason: a host port is
global to the machine. What differs is what replaces them. The agent's Compose
project is the environment the agent works in and Feat surfaces no port from it,
so nothing does; the application runtime is what the user is testing, so the
services it declares reachable are published on ports Feat allocated for that
task alone.

The application runtime has two generated documents. The first is the `include`
that joins the repositories the application is composed of, one entry per
repository with its own project directory. The second is the override, merged
last over the result: it resets `container_name` on every service the included
files define, and replaces every published port. A service the project declared
reachable is published on the host ports Feat allocated for this task, and every
other service publishes nothing. Both values are global to the machine, so a base
file carrying either could be started for one task and no more — which is exactly
what happened to the reference project's second task (ADR-065 evidence 8,
superseding ADR-034). It also mounts each task worktree at the runtime container path
its repository configures, in the services that repository named, points the
build context of a managed service built from a repository at that repository's
task worktree, and carries the generated non-secret variables — the
project and task identifiers, the Compose project name, each external resource's
selector, and the address of every reachable service. The worktrees and the
variables apply to the managed services only: a service Feat was not asked to
manage gets both resets and the ownership labels and nothing else. See ADR-034
and ADR-065.

Resetting and overriding require Docker Compose 2.24 or later, which
`feat doctor` checks.

## Validation rules

At minimum:

- IDs match a documented safe pattern.
- Project and repository IDs are unique, and the file name matches the project ID.
- Primary repository exists and can be read-write.
- Host paths resolve to expected Git repositories/Compose files.
- Container paths are absolute. The agent's must not overlap one another, and the control path overlaps none of them; two repositories' runtime container paths may coincide, because a repository's worktree reaches its own services only.
- A repository contributing to an application runtime the project does not configure is rejected, as is a runtime container path with no service to mount it in.
- A configured runtime where no repository a task selects says where its source goes is rejected: it could mount no task worktree anywhere, so every service would run the user's ordinary checkout. It is asked of the runtime rather than of each repository, because a repository whose services build their code needs no container path and configuration cannot see a build context.
- A reachable service names one of its own repository's managed services, and two reachable services must not produce one generated variable name: `FEAT_URL_<service>` is upper-cased with every other character replaced, so `web-app` and `web.app` would be one address arriving under both names.
- The host port range holds at least one port per reachable service, lies above the privileged ports, and ends where it begins or later. A range Feat could not allocate one task's ports from is refused where it can name the field rather than at the first create.
- Configuration written in a shape a previous version read is rejected with an error naming what replaced it, rather than as an unknown field.
- Branch and runtime templates produce safe names, use only known placeholders, and contain a per-task placeholder so that two tasks cannot share a branch or a Compose project.
- Worktree roots cannot resolve to a broad unsafe path, cannot be rooted at one, and cannot overlap a repository checkout.
- Devcontainer user is non-root when the policy requires it.
- Docker capability is denied.
- Command arrays contain a non-empty executable, and the executable itself is never a placeholder.
- Unknown fields fail validation, as do repeated keys.
- Configuration that the selected execution mode would ignore is rejected rather than ignored.

Whether a configured path exists, holds a Git repository, or names a real Compose service is a host question and belongs to `feat doctor`, not to loading: a configuration must stay loadable on the machine whose repository is missing.

## State schema policy

Every generated JSON document contains:

```json
{
  "schema_version": 1,
  "id": "...",
  "updated_at": "..."
}
```

Schema migrations must be explicit, tested, and one-way with a backup or retained previous snapshot until success. Before public v0, compatibility expectations should be documented.

