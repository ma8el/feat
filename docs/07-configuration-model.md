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
user reaches from the host. It is collected and validated from this version and
is not acted on until Feat allocates ports.

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
`FEAT_RUNTIME_PROJECT` on every managed service. They are generated task
metadata and never a value read from an environment file.

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

Published ports are reset here and kept by the application runtime below, which
is a difference in what the two documents are about rather than an inconsistency.
The agent's Compose project is the environment the agent works in and Feat
surfaces no port from it; the application runtime is what the user is testing, and
reaching it is the point of the port.

The application runtime has two generated documents. The first is the `include`
that joins the repositories the application is composed of, one entry per
repository with its own project directory. The second is the override, merged
last over the result: it resets `container_name` on every service the included
files define and leaves published `ports` exactly as the project configured
them. A container name is Feat's problem and a
published port is how the user reaches the application they are testing, and v0
allocates no ports of its own: two tasks that both want one host port is
explained in Feat's terms rather than prevented by making the application
unreachable. It also mounts each task worktree at the runtime container path
its repository configures, in the services that repository named, and carries
the generated non-secret variables — the
project and task identifiers, the Compose project name, and each external
resource's selector. Those last two apply to the managed services only: a service
Feat was not asked to manage gets the reset and the ownership labels and nothing
else. See ADR-034.

Resetting requires Docker Compose 2.24 or later, which `feat doctor` checks.

## Validation rules

At minimum:

- IDs match a documented safe pattern.
- Project and repository IDs are unique, and the file name matches the project ID.
- Primary repository exists and can be read-write.
- Host paths resolve to expected Git repositories/Compose files.
- Container paths are absolute. The agent's must not overlap one another, and the control path overlaps none of them; two repositories' runtime container paths may coincide, because a repository's worktree reaches its own services only.
- A repository contributing to an application runtime the project does not configure is rejected, as is a runtime container path with no service to mount it in.
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

