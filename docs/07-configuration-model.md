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
    container_path: /workspace/dashboard
    default_branch: main
    remote: origin
    default_access: read_write

  database:
    host_path: ~/projects/app/database
    container_path: /workspace/database
    default_branch: main
    remote: origin
    default_access: selectable

  devcontainer:
    host_path: ~/projects/app/devcontainer
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
  compose_files:
    - ~/projects/app/dashboard/docker-compose.yml
  static_overrides: []
  env_files:
    - ~/projects/app/dashboard/.env
  project_name_template: "feat-{project_id}-{task_id}"
  services:
    - frontend
    - backend
  external_resources:
    database:
      type: postgres
      lifecycle: external
      selector_variable: FEATURE_DATABASE

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

Every repository's `container_path` must be the path its Compose files already
mount the repository at. Compose merges a service's mounts by target, so Feat's
generated override replaces that mount with the task's worktree; a path that
disagrees adds a second mount instead and leaves the agent holding the user's
ordinary checkout as well as its own worktree. Feat refuses a launch whose
container turns out to mount a configured repository checkout, but the
configuration is where the mistake is fixed.

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

### Runtime ownership

Runtime resources are either:

- `managed`: created/observed/removed by Feat;
- `external`: referenced but never provisioned or destroyed by Feat;
- later `shared`: product-managed but shared with explicit isolation semantics.

`runtime.services` names the services a create and a start target. It is not the
whole of what runs: Compose starts whatever those services depend on, and
everything it starts belongs to the task's own Compose project. Feat therefore
owns all of it — a stop, a status, a logs, and a destroy address the project
rather than the list — and a status says which services the project named and
which are there because another service needs them. A service Feat was not asked
to manage is not given the task's worktrees or the generated variables; it is
given its `container_name` reset and Feat's ownership labels, without which two
tasks could not run the same application at once (ADR-034).

The reference staging PostgreSQL databases are external. Feat generates the
value of an external resource's `selector_variable` — the task key, which is
short, unique, safe in a name, and not a secret — and sets it on every managed
service. Naming a share is all it does: Feat never creates, migrates, or drops
anything behind that name, and what a project makes of it is the project's
(OQ-011).

Every repository's `container_path` is used by the application runtime as well
as by the devcontainer, so a project whose application Compose files mount a
repository somewhere else adds a second mount instead of replacing one, and its
services run the user's ordinary checkout rather than the task's worktree. Feat
inspects the started containers and reports that in its own terms rather than
refusing the start: the application runtime is inside the trusted host, so it is
a correctness problem rather than a boundary breach (ADR-034).

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
- enabled capabilities;
- external resource selectors.

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

The agent execution override additionally resets `container_name` and `ports` on
the agent service, unconditionally and in that document rather than by
inspection. Both are global — a container name to the Docker daemon, a published
port to the host — so a base file carrying either cannot be started twice, and
one task per machine is not the product. The reset is stated in the task detail
rather than done quietly, and it applies only to the service the agent runs in.

The application runtime's generated override resets `container_name` on every
service the project's Compose files define and leaves published `ports` exactly
as the project configured them. A container name is Feat's problem and a
published port is how the user reaches the application they are testing, and v0
allocates no ports of its own: two tasks that both want one host port is
explained in Feat's terms rather than prevented by making the application
unreachable. It also mounts each task worktree at the container path its
repository configures, and carries the generated non-secret variables — the
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
- Container paths are absolute and non-overlapping unless explicitly allowed, and the control path overlaps none of them.
- Branch and runtime templates produce safe names, use only known placeholders, and contain a per-task placeholder so that two tasks cannot share a branch or a Compose project.
- Worktree roots cannot resolve to a broad unsafe path, cannot be rooted at one, and cannot overlap a repository checkout.
- Devcontainer user is non-root when the policy requires it.
- Docker capability is denied.
- Command arrays contain a non-empty executable, and the executable itself is never a placeholder.
- Runtime destroy never targets external resources, so an external resource may not also be a managed service.
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

