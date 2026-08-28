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

## Global settings

Not everything Feat is told is a fact about a project. A second file holds what
is true of the machine and the person using it — the external commands review
opens, how notifications behave, and how often resources are sampled:

```text
~/.config/feat/settings.yaml
```

It sits beside the `projects/` directory rather than inside it, because it has
no project to be named after. `.yml` is accepted; settings written by both
extensions is an error rather than a preference, for the reason a project
configured twice is one.

The file is **optional and global**. Every value in it has a documented default,
so a machine that has never written one is fully configured, and there is no
per-project override: precedence rules are load-bearing and hard to remove once
written, and nothing has yet asked for one. An override can be added later, on
evidence that somebody wants it. See ADR-079.

It carries its own `version`, separate from the project file's, because the two
files are two compatibility surfaces and a change to one has no reason to
invalidate the other. Its schema is published at `schema/feat-settings.schema.json`
and held to the implementation by the same drift test that holds the project
schema.

`feat settings show` prints the resolved settings with every default filled in
and each value marked with where it came from — `default`, `configured`, or, for
the editor alone, `from $EDITOR`. That last marker is the reason the review
commands belong here: the default has always reached for a user-level source and
until now had no user-level file to reach for. `feat settings path` prints where
the file belongs, whether or not it exists.

`feat settings init` writes it, with every value shown, commented out, and
explained; `docs/examples/settings.yaml` is that same text, and a test holds the
two together. Only the `version` is live, so running it changes nothing — a
default written down is a value that stops following Feat when Feat's own
changes, and this whole file is defaults. An existing file is never overwritten
and there is no force flag, which is the rule `feat project init` follows for the
file it writes. `feat settings edit` opens it in the editor
`review.editor.command` names or in `$EDITOR`, writing the commented default
first when there is no file, and reads the result back rather than trusting that
a clean exit means a valid file.

`$EDITOR` is split on whitespace, because it holds a command rather than a
program: `code -w` is an ordinary value of it.

**The daemon reads this file once, when it starts.** That is the opposite of how
project configuration is read — every operation re-reads a project's YAML, so an
edit takes effect at the next one — and the difference is what the two files are
about. A project file describes work in progress and is edited while Feat is
running; these are the machine's own dispositions, and they change about as often
as the machine does. Reading them per use meant the resource sampler parsing a
file every two seconds to be told the same number. So changing a setting takes a
daemon restart. A file that cannot be read costs the defaults rather than the
daemon: an optional file with a typo in it must not stop a control plane from
starting, and the failure is logged once at startup.

Every command reads the file as it is now; only the daemon holds a snapshot. So
`feat settings show` and `feat settings edit` can print values a running daemon
is not working from, and both say so — once, and only when there is something to
do about it: *the settings have changed since the daemon started*. A machine with
no daemon says nothing, and neither does a daemon that started after the file was
last written, because a line reporting that no restart is needed is a line nobody
can act on. Neither asks the daemon anything — the endpoint record carries when
it took ownership and the file carries when it was written — so the answer costs
a stat and works on the same terms as the rest of the command.

The command is `settings` rather than `config` deliberately. `feat project show`
already prints project configuration, and two commands called "config" would
blur exactly the line this file draws. `~/.config/feat/` keeps its name, which is
XDG rather than a product noun.

## Illustrative schema

The exact Go structs may still evolve, but the semantics below are accepted.

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
    forge:
      kind: gitlab
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

runtime:
  provider: compose
  start_policy: manual
  static_overrides: []
  env_files:
    - ~/projects/app/dashboard/.env
  project_name_template: "feat-{project_id}-{task_id}"
  port_range: "21000-21999"
  bind_address: "127.0.0.1"

checks:
  dashboard:
    - id: test
      command: ["pytest"]
      execution: agent

tracker:
  kind: command
  command: ["tickets-for-me"]
```

And the settings file beside it, holding what is true of the machine rather than
of any project:

```yaml
version: 1

review:
  diff:
    command: ["git", "diff", "{base_commit}"]
  editor:
    command: ["nvim", "{repository_path}"]
  status:
    command: ["git", "status", "--short", "--branch"]

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

### Capabilities Feat cannot vary

`docker`, `network`, and `git` are the whole section, and they accept one value each — `denied`, `unrestricted`, and `full`. Feat has no mechanism that grants an agent Docker, restricts its network, or limits its Git access, so any other value would record a promise the binary does not keep. The declaration is still made, because the execution adapter checks the running container against it. See ADR-028.

`github_cli` and `gitlab_cli` were here until ADR-075 and are not replaced. Publication and ticket ingestion run on the trusted host with the credential the user already has there, so the agent's environment is never asked for one, and Feat has nothing to declare about a `gh` or `glab` that a project installs in its own image. A file that still names either key fails to load as an unknown field, and the line is deleted.

Under host-native execution there is no container to check. The agent runs as the user and holds whatever the user holds — Docker, the network, and Git — so every capability describes intent there rather than a condition Feat verified. `feat doctor` reports this when the daemon is running host-native, for the reason ADR-067 discloses a policy Feat cannot read: a level that was never checked must not read as one that passed.

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

`runtime.bind_address` is the host address an allocated port is published on and
defaults to `127.0.0.1`, so a task's services answer on the machine running them
and nowhere else. It must be a literal IP address: it reaches the generated
override as a `host_ip` and Compose binds it, and a name would be resolved at a
moment Feat cannot see, to an address Feat could not then say the service was at.

Widening it is a decision, and `0.0.0.0` is the way to say it — for reaching a
dev server from a phone on the same network, say. What it means is in
`docs/05-security-model.md` § Published ports and who can reach them: every
interface the machine has, which includes every network it is joined to and the
Docker bridge every other task's containers are on.

An address a repository's own Compose file named is kept as it is. `bind_address`
is the default for a publication that named none, not an address applied over
one, so a project that deliberately published on the loopback address keeps doing
so whatever this key says.

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
`FEAT_RUNTIME_PROJECT` on every managed service, and `FEAT_HOST_URL_<service>`
and `FEAT_HOST_PORT_<service>` for each reachable one — upper-cased, with
everything that is not a letter or a digit replaced. A configuration in which two reachable
service names produce the same variable is rejected, because one service would
receive the other's address. A service publishing more than one port also gets a
pair per port, named by the container port, since the unsuffixed pair can only
name one of them. They are generated task metadata and never a value read from
an environment file.

`FEAT_HOST_URL_<service>` is the address a consumer **on the host** reaches the
service at — a browser opening a frontend, a shell running `curl`, or a build
baking an API address into a bundle a browser will then load. It is not how one
service calls another. A published port belongs to the host's network namespace:
inside a container that address is the container's own loopback, and with the
default `bind_address` the host's port is not reachable from a container at all.
A service calling a sibling uses the Compose service name and the container
port, both of which the project's own files already state and neither of which
needs an allocated port, because nothing about a container-to-container call is
global to the machine.

`HOST` is in the prefix because the prefix is the only thing present where this
value is used, and documentation is not: someone writing `${FEAT_HOST_URL_api}`
into a service's `environment:` is reading the name and nothing else. The
earlier `FEAT_URL_<service>` said nothing, and the failure it allowed is a
silent connection refused against the caller's own loopback.

The variables still reach every managed service, because the container that
bakes a host address into something a browser will load is itself a service.

The addresses reach the Compose process as well as the containers. A service
finds one under the project's own name — a frontend whose framework exposes only
variables with a particular prefix cannot read `FEAT_HOST_URL_api` — so the
project maps it in its own Compose file with `${FEAT_HOST_URL_api}`, and Compose
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
- **Nothing names a host address by a fixed port.** The allocated port differs
  per task, so a value baked into an image or written into a file can be right
  for one task at most. What needs the generated address is what a consumer on
  the host will use: a build baking an API address into a bundle a browser then
  loads, and a service that must accept that browser's origin — a CORS
  allow-list — reading the same variable for the service the browser reached.
  Both read `${FEAT_HOST_URL_<service>}`. Write them with a `:-` default so the
  files still work run by hand. A call from one service to another needs none of
  this and must not use it: it names the Compose service and the container port,
  which do not differ per task.
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

### Where the code goes and where the tickets come from

`repositories.<id>.forge` and `tracker` are separate optional sections, absent
for a project that publishes nowhere and for one whose tasks are all written by
hand. They answer different questions with different owners, and they coincide
only where a forge hosts its own issues: a self-hosted GitLab holds the code of
a team whose planning is in Shortcut, and Shortcut, Jira, and Linear host no
code at all (ADR-071).

The forge belongs to the repository, because a repository lives on exactly one
forge and a publication is one merge request per changed repository. It carries
a kind alone: the project path and the host are in the repository's own Git
remote, and what cannot be read from there is which forge a self-hosted instance
is. That is declared rather than guessed, because guessing wrong would open a
merge request somewhere the user did not mean.

The tracker belongs to the project, because the thing a ticket seeds is a task
and a task belongs to one project. Feat does not model where tickets live at
all: issues hang off a repository, stories off a workspace, and a board off an
organisation, so the section says how to obtain a list and the scope of that list
belongs to whatever produces it. A planning repository that holds no code is one
command away and is never registered with Feat as a repository, which would make
it a candidate for a worktree, a branch, and a Compose project.

`tracker.command` is a host command printing JSON that conforms to
`schema/feat-tickets.schema.json`, in the class `review` and `checks` already
establish for user-supplied commands. The published shape is the contract rather
than the tracker's own, because reading an arbitrary shape would mean a mapping
language in configuration, and a mapping language has no end. It carries a
reference, a title, a body, a URL, a state, and an optional source, and nothing
richer: anything else belongs in the brief, which is Markdown and holds whatever
the user wants. Feat passes the command no filter, because a filter vocabulary
would have to map onto every tracker's query language; which tickets are the
user's is the command's decision. `tracker.kind` has one value, `command`, and
is kept because the configuration file is a compatibility surface: a
discriminator added later means either a breaking change or an inference from
which fields happen to be present.

The output is validated against the published schema by `feat doctor`, and
bounded in size for the reason a control message is: it becomes a brief, and a
brief is what the agent is told to do. A tracker that emits the wrong shape is
then found when the user asks whether the project is configured rather than when
they are trying to start work.

The command runs on the trusted host as the user, in their home directory. There
is no task when it runs, so there is no worktree to run it in, and an explicit
directory is what makes `feat doctor` and the daemon ask the same question of the
same machine rather than inheriting whichever directory each was started in.

`docs/examples/tickets` holds a worked command per tracker — GitHub Issues,
GitHub Projects, GitLab Issues, and Shortcut — each beside the document it
printed, and the test suite validates every one of those documents with the code
`feat doctor` uses, for the reason `docs/examples/project.yaml` is validated
against the configuration schema.

### Notifications and resource sampling

Two grace periods exist and they are measured from different moments, which is
what tells them apart.

They also live in different files now, which is the plainest statement of the
difference there has been. `agent.claude.idle_grace_period` decides when an ended
turn becomes `idle`: a turn that ends and immediately continues is not a session
waiting for anybody. That is a fact about how the agent is driven, so it stays in
the project's configuration, and it is provider-specific besides.
`notifications.idle_grace_period` decides how long a task must have *been* idle
before Feat interrupts the user about it, measured from that idle transition —
which is a fact about the user, so it is one of the machine's settings. So the
dashboard reports idle after the first period and a desktop notification arrives
after both, and somebody who wants to be told only about a long silence raises the
second one alone.

Measuring both from the end of the turn was the other reading and is rejected: a
notification grace shorter than the provider's would expire before the task was
idle, and no notification would ever be delivered. See ADR-035 and ADR-079.

`notifications.desktop` turns desktop delivery off without touching the
dashboard's own attention badges, which are rendered from task state.
`notifications.suppress_while_attached` drops a notification about a task the
user is already looking at, which Feat asks tmux per window rather than
remembering that somebody once attached.

`resources.sample_interval` is how often the machine and each task's containers
and processes are observed. It is a **global setting** rather than project
configuration, because one sample measures the whole machine whatever project
asked for it: it is read from `~/.config/feat/settings.yaml`, floored at one
second and at however long the previous sample took — asking the container
runtime what it is using takes between one and two seconds whatever it is asked.
Sampling never blocks a request or a task.

It used to be per project, which meant the sampler listed every registered
project and parsed each one's YAML on every tick, then reconciled the answers
with "the most eager project wins" — a rule that existed only because the setting
was in the wrong file. See ADR-079.

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
- A reachable service names one of its own repository's managed services, and two reachable services must not produce one generated variable name: `FEAT_HOST_URL_<service>` is upper-cased with every other character replaced, so `web-app` and `web.app` would be one address arriving under both names.
- The host port range holds at least one port per reachable service, lies above the privileged ports, and ends where it begins or later. A range Feat could not allocate one task's ports from is refused where it can name the field rather than at the first create.
- Configuration written in a shape a previous version read is rejected with an error naming what replaced it, rather than as an unknown field.
- Branch and runtime templates produce safe names, use only known placeholders, and contain a per-task placeholder so that two tasks cannot share a branch or a Compose project.
- Worktree roots cannot resolve to a broad unsafe path, cannot be rooted at one, and cannot overlap a repository checkout.
- Devcontainer user is non-root when the policy requires it.
- Docker capability is denied.
- Command arrays contain a non-empty executable, and the executable itself is never a placeholder. A tracker command may contain no placeholder at all: it runs before there is a task, so nothing would fill one.
- A repository's forge is one Feat publishes to, and a tracker's kind is one it knows how to run.
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

