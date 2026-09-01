# ADR-065 — A runtime is composed of its repositories, and a service that is not running the task's code says so

Status: accepted
Recorded: 2026-08-16, before implementation

Evidence found while making the reference project's whole application — an API
and a frontend in separate repositories — run for one task. Most of it is
measured against Docker 29.5.2 and Compose 5.1.4 rather than reasoned:

1. **A project configured for host execution mounted nothing, again.**
   `jobharbor.yaml` had no `container_path` on any repository, because nothing
   asks for one outside devcontainer execution. `runtimeMounts` skips a
   repository without one (`internal/daemon/runtime.go:540`), so the runtime
   generated no `volumes:` at all and every service ran the user's ordinary
   checkout. It is ADR-034 evidence 10 surviving its own fix: the daemon was
   corrected to read the mount target from configuration, and nothing was done
   about a configuration flow that never collects it.
2. **Two repositories' Compose files cannot be merged with `-f`.** Compose
   resolves relative paths in every file against the project directory, which
   Feat sets to the first configured file's directory
   (`internal/daemon/runtime.go:479`). Both of the reference project's files use
   `build: .`, so listing them together builds the frontend from the API
   repository. Measured, not inferred.
3. **`include` is the mechanism that does work.** Paths inside an included file
   resolve against that file's own directory, and the long form takes a
   `project_directory` per entry. Feat's generated override merges over the
   result unchanged: `!reset null` clears an included service's
   `container_name`, ownership labels and worktree mounts land on it, and
   `docker compose config --services` enumerates it, so ADR-034 evidence 12's
   dependency walk still holds. It needs Compose 2.20; ADR-034 already requires
   2.24.
4. **The generated override never touches `build.context`.** A service that
   bakes its code with `COPY` runs the ordinary checkout whatever
   `container_path` says, and ADR-034's ordinary-checkout note cannot fire
   because the note inspects mounts and there is no mount. The reference
   project's frontend is a multi-stage build ending in nginx: mounting a
   worktree into it is meaningless, and only the build context decides what
   runs. A fix that addresses mounts alone leaves that service broken and
   silent.
5. **One field is doing two unrelated jobs.** The agent's execution adapter
   (`internal/daemon/execution.go:207,233`) and the application runtime
   (`internal/daemon/runtime.go:549`) both read
   `repositories.<id>.container_path`. They are different questions with
   different owners: where the agent's devcontainer mounts a repository is the
   user's free choice, and where an application's own services expect their
   source is a fact about that application's Compose files. `jobharbor-dev.yaml`
   needs `/workspace/jobharbor-api` for the first and `/app` for the second, and
   cannot say both.
6. **The configuration flow neither collects the field nor shows it.**
   `feat project init` jumps from the execution mode straight to the provider
   CLI when the answer is not `devcontainer` (`internal/wizard/wizard.go:639`),
   so a host-execution project is never asked. `feat doctor` drops the CONTAINER
   PATH column for the same projects (`internal/cli/table.go:87`). Both
   assertions covering that column use a devcontainer fixture
   (`internal/cli/project_test.go:310,349`), so neither branch is pinned. The
   value the daemon depends on is one the product neither asks for nor prints.
   **Fixed by this ADR's own decisions below, and left standing as the finding
   rather than as a description of the flow:** the wizard asks for a runtime
   container path in every execution mode and proposes the path the
   repository's own Compose files state, and `feat doctor` prints the column in
   every mode. ADR-081 gave the agent's mount the same reading, and ADR-082 the
   words that go with both. Read as current behaviour this sends a reader
   looking for a hole that is no longer there.
7. **Every way of getting this wrong is silent.** A missing container path
   produces no mount; a mismatched one produces two mounts, because Compose
   merges a service's volumes by target and a target that does not collide is
   simply added; a baked build context produces neither. In all three the
   containers start, the application serves, and every record Feat keeps stays
   correct. The user sees a healthy runtime that is not running their task.
8. **Fixed published ports prevent the thing the runtime is for.** The reference
   project publishes 8000, 5173 and 5432 at fixed numbers, so a second task's
   runtime cannot start, and its frontend reaches the API through a URL baked to
   one of those numbers. Testing one task's application while other agents work
   is the reason a per-task runtime exists, and ADR-034's decision to leave
   published ports exactly as configured is what stops it.
9. **Hot reload across a bind mount is sound.** With the source mounted rather
   than baked, and the virtualenv moved outside the mount, an edit on the host
   reached both `uvicorn --reload` and the Vite dev server in two seconds, over
   VirtioFS, with no polling. Reload is therefore a mechanism the product may
   rely on rather than a hope — which matters because an agent confined to a
   devcontainer has no Docker and cannot restart anything it changes.

Decisions:

- **A runtime is composed of its repositories.** The global
  `runtime.compose_files` list is replaced by a per-repository runtime
  contribution: the Compose files that repository brings, resolved relative to
  it, the container path its own services expect, and the services it asks Feat
  to manage. Feat generates the `include` document that joins them, with a
  `project_directory` per entry. The user stops hand-writing the file that
  composes their application, and evidence 2 stops being reachable — nothing
  relative ever crosses a repository boundary.
- **`container_path` splits in two.** `repositories.<id>.agent.container_path`
  is where the agent's devcontainer mounts the worktree;
  `repositories.<id>.runtime.container_path` is where that repository's own
  services expect their source. They are separate because evidence 5 shows they
  are separate questions, and under a compliance regime that requires
  devcontainer execution both always exist.
- **A build context is redirected like a mount.** For a managed service whose
  build context is a configured repository, the generated override sets
  `build.context` to that repository's task worktree. Measured: the override can
  do it, and a relative `dockerfile:` follows the new context. Where the code
  comes from is one question, and a mount and a build context are two answers to
  it; fixing one and not the other is evidence 4 preserved.
- **A managed service that is not running the task's code is a state, not a
  note.** It is resolved at create, from configuration, and shown on the task.
  The half that needs no Docker at all — a repository selected by a task, with a
  runtime configured, and no runtime container path — is refused at
  configuration load, because it cannot produce anything but evidence 1.
  ADR-034's post-start inspection stays as the check that catches what
  configuration cannot.
- **Feat allocates published ports, and tells a service where its siblings
  are.** **Corrected by this ADR's amendment of 2026-08-22, below: the address
  is host-scoped and is not how one service calls another, and the variables are
  now named `FEAT_HOST_URL_<SERVICE>` and `FEAT_HOST_PORT_<SERVICE>`.** Read the
  heading of this bullet as "and tells a service the host address of each
  reachable one"; every statement of the sibling reading elsewhere in the product
  was derived from the sentence as it was first written here.
  [08-v0-scope.md](08-v0-scope.md) excludes port-range allocation
  "unless required to make the reference project run", and evidence 8 is that
  condition being met, so this is the exclusion's own escape rather than a scope
  change. A repository declares which of its services are reachable; Feat
  allocates a host port per reachable service per task, records it, writes it
  into the generated override in place of the configured publication, and
  releases it on destroy. The resulting address reaches every managed service of
  the task as a generated non-secret variable — `FEAT_URL_<SERVICE>` and
  `FEAT_PORT_<SERVICE>`, normalised to upper case with non-alphanumerics
  replaced, and refused when two service names normalise alike. This
  **supersedes** ADR-034's rule that published ports are left exactly as
  configured. That rule's stated reason was that a port is how the user reaches
  their application and that v0 allocates none of its own; the second half has
  now changed, and the first is better served by an address Feat can tell the
  user than by a number that only one task can hold.
- **Configuration is derived and confirmed rather than transcribed.** Feat reads
  a project's own Compose files structurally: service keys, `volumes` targets,
  `build.context`, and published `ports`. It never resolves interpolation — an
  entry containing `${...}` is a value Feat could not derive, and the user is
  asked — and it never reads `environment:` values, `build.args`, or an
  `env_file`. This does not touch ADR-034 evidence 5, whose subject was
  `docker compose config` rendering environment-file values into its output;
  reading the document resolves nothing. The rule is not who reads but where a
  value comes to rest: a derived value becomes configuration only when the user
  accepts it into their own YAML, and nothing Feat inferred is persisted in
  Feat's own state. `internal/project/discover.go` already draws this line for
  service names and is extended rather than replaced.
- **The wizard asks in every execution mode, and `doctor` prints in every
  execution mode.** Evidence 6 is one decision applied to two commands. A
  project with a runtime is asked for its runtime container paths whether or not
  its agent is containerised, and the mapping table shows them whatever the mode,
  because it is the mapping that decides whether the user's services run their
  task.
- **The configuration interface breaks without a compatibility period.** Feat is
  used by its author and nobody else, so a version bump would buy ceremony. The
  old shape fails the strict unknown-field rejection that
  [07-configuration-model.md](07-configuration-model.md) already requires, and
  the message names the replacement rather than reporting an unknown key: a
  break the user has agreed to is still a break they should not have to diagnose.
- **What this does not decide.** It does not make an application
  hot-reloadable — that stays in the user's own Compose files, and Feat's
  contribution is saying which services will not reload. It does not deliver
  stable per-task hostnames: OQ-008 stays open, and the evidence recorded against
  it is that a proxy is a machine-wide resource with a lifetime no task owns,
  which is the `shared` lifecycle ADR-034 called roadmap work, and that a
  label-driven proxy wants the Docker socket that this product's headline denies
  the agent. Allocated ports are the first half of that feature rather than a
  detour from it, since a proxy must route to something. Nothing here addresses
  what several parallel application stacks cost a laptop.

Amended during slice 14, which implemented the first of the three.

Evidence 12, found by running `feat project init` against the reference project
after the rest of the slice was green: **the command meant to prevent evidence 1
produced it.** Compose-file discovery looked only for the four names Compose
itself defaults to, and both of the reference project's `docker-compose.dev.yml`
overlays — which carry the bind mounts a worktree replaces, the reset of a
published port, and, in the frontend, the only service anybody runs — were
invisible to it. What the wizard proposed was the base files alone: no runtime
container path for either repository, the frontend's static production build in
place of its dev server, and a database offered as reachable. The result loads,
starts, and runs the user's ordinary checkout in every service. Two further
defects compounded it: a file loop proposed the next candidate *and* claimed an
empty answer would finish, so pressing Enter added files rather than ending the
list; and the agent's Compose question proposed files found beside any
configured repository, which after this slice are overwhelmingly the
application's. Discovery now finds overlays, a loop proposes only its first
candidate, and the agent's question proposes nothing — it is asked before the
application section exists, so it has nothing to exclude with.

Two decisions the composition needed and this ADR had not made:

- **The Compose project directory is Feat's own generated directory.** It used
  to be the first configured file's, so that file's relative paths resolved as
  they do by hand — and with an include document, every entry carries the
  directory its own repository's paths resolve against, so a project directory
  belonging to one of the repositories could only be the directory a second
  repository's paths were wrongly resolved against. It is therefore the
  directory holding the generated documents, whose own paths are all absolute.
  One consequence is user-visible and is documented rather than left to be
  discovered: Compose's implicit `.env` lookup beside a repository no longer
  applies, so an environment file a project needs is named in
  `runtime.env_files`, and a relative path inside a `static_overrides` file
  resolves against Feat's directory rather than a repository's.
- **A worktree is mounted into the services that named the repository, not into
  every managed service.** The ADR says a repository's container path is where
  *its own services* expect their source; mounting every worktree into every
  service would additionally make two repositories that expect their source at
  the same path a collision, which is an ordinary arrangement between two
  applications rather than a mistake. A service may appear in more than one
  repository's `services`, which is how a service that runs an application and a
  shared library it depends on receives both, at their own container paths. The
  managed list Compose is asked for is the union.

Consequence: the configuration gains a per-repository runtime section and loses
a global one, the agent's and the runtime's container paths become separate
fields, and `domain.RuntimeEnvironment` gains the port allocations it must
release. `feat doctor`, `feat project init`, the JSON schema and the documented
example all move with it, because [07-configuration-model.md](07-configuration-model.md)
holds the last two to the implementation by test. The work exceeds slice 13's
outcome and is ordered as three slices, each of which ends with a product that
runs: the configuration shape, its validation, and the composition that consumes
it, because a slice that reshaped the configuration and left nothing reading it
would not build, let alone start a runtime; then build contexts and the
provenance state; then allocation and reachability. The first also rewrites the
project configurations on this machine, since `feat.yaml` is among those that
stop loading and it is how Feat's own tasks are run. It pushes the current slice
14 out by three.
The reference project's whole application then runs for one task, hot-reloading,
several times over, and the failure this ADR exists for stops being silent.

Amended during slice 15, which implemented the second of the three: the build
contexts and the provenance state.

Evidence 13, found while writing the reader against the service this slice
exists for: **the structural reader could not read the reference project's
frontend at all.** Its production service writes a plain `context: .` beside a
`build.args` entry carrying a "${...}" — a value Feat never reads and has no
business reading — and the reader judged the interpolation on the whole `build`
mapping, so the plainest build context in the project came back undecided.
Measured against the repository itself: the slice 14 reader answers
`builds-from-source=false` for `jobharbor-frontend` and lists its build context
as unread, while the fixed one resolves the context to the checkout. That service
is the multi-stage build ending in nginx of evidence 4, where the build context
is the only thing that decides what runs: a reader that could not see it could
not have redirected it, and `feat project init` would have proposed nothing about
it either. Interpolation is now judged on the context alone.

Evidence 14, found dogfooding this slice against the reference project: **a
managed service the project's files no longer define is reported as Feat's own
generated document being invalid.** Switching one repository's `compose_files` to
the file defining its production service while its `services` still named the
development one produced `service "web" has neither an image nor a build context
specified: invalid compose project` on every create, and the same message from
every poll afterwards. Both halves of it point away from the mistake: the
service is one the user named in their own configuration, and the document
without an image is the override Feat writes an entry into for every managed
service. Compose is right and unhelpful. The adapter now compares the managed
services against what `config --services` says the project defines — a list it
already had — and refuses before writing the override, naming both sides.
`feat doctor` reports the same mismatch per repository; this is that question
asked at the moment it is the reason nothing started.

Evidence 15, from the same run: **a mount does not make a baked service
current, and Feat said it did.** The frontend was configured with the production
file and a runtime container path left in place, so the service both mounted the
task's worktree at `/web` and built its image from it. The report that a change
needs a rebuild was suppressed, on the reasoning that a mounted worktree is
current the moment it is written — which is true of the API, whose server reloads
from `/app`, and false of an nginx image serving what its build produced and
never reading `/web` at all. Whether the mount is read is a fact about the image
that Feat cannot see. The report is therefore made whenever a service builds the
task's code, and says what the mount is still worth: current wherever the image
reads it. Suppressing it whenever a mount existed was true of exactly one of the
two services the reference project runs.

Three decisions this slice needed:

- **A build context inside a repository is that repository's.** The ADR said the
  context *is* a configured repository's checkout; a monorepo writes
  `build: ./web`, and the task's worktree holds the same subdirectory. The
  redirect therefore points at the same place inside the worktree, and a context
  above or beside the checkout is left alone. A bind source is still matched
  against the repository root exactly: a mount of a subdirectory is a partial
  mount that a whole worktree mounted at one container path would not replace,
  so it is not a candidate for a container path. Two questions, two rules.
- **The configuration-load refusal is asked of the runtime, not of each
  repository.** The plan wrote it as a task-eligible repository with no runtime
  container path. Implemented that way it refuses a project that is correct: the
  frontend of evidence 4 mounts nothing anywhere and wants no mount — pointing
  its build context at the worktree is what makes it run the task's code — and
  requiring a container path of its repository would demand a path its services
  do not use. Worse than pointless: a worktree mounted over an image's own
  `/app` hides the `node_modules` and the built output that were baked there, so
  the requirement would break the service it was meant to protect. What is
  refused is therefore evidence 1 exactly — a configured runtime, repositories a
  task selects, and no runtime container path anywhere, which can mount no task
  worktree at all — and the message names the repository and its services. Which
  particular service is not running the task's code is the provenance state's
  answer, resolved where the project's own Compose files can be read.
- **`create` builds again; `start` does not.** Redirecting a build context is
  half an answer while `up` reuses the image it made the first time: the service
  would run the copy of the worktree it was first built from, for the life of the
  task, and the agent that changed the code has no Docker to rebuild with
  (evidence 9). `create` therefore passes `--build`, which is the action that
  makes a service's image and the one a user asks for when they want it made
  again; `start` stays as it was, because a start is what a user asks for when
  they want their application up now, and Docker's cache makes the rebuild cheap
  when nothing changed. The report a baked service carries names that command.

Consequence beyond the plan's list: `runtime.Spec` gains the redirected build
contexts, `domain.RuntimeEnvironment` gains the per-service provenance, and the
three places that re-applied a task's recorded inputs to a freshly resolved
specification became one — which also drops a mount or a build naming a service
the task does not manage, so a project file that gains a service can no longer
refuse the stop or the destroy of a runtime created before it.

Amended during slice 16, which implemented the third of the three: the allocation
and the reachability.

Evidence 16, found by running three tasks of the reference project at once:
**a poll that started before a create finished gave the task's ports away.** The
runtime poller lists the tasks outside any lock and asks Docker about each in
turn, so a create that finishes while it is asking leaves it holding an answer
about the world as it was — nothing existed, therefore the runtime is absent.
Applied, that released the host ports the create had just allocated, while the
containers created with them were bound to those ports, and the next task was
given them. Measured as the second start of a task publishing nothing at all: the
generated override was rewritten with `ports: !reset []` on every service, and
the task the ports belonged to said its own reachable services were unreadable.
The state alone would have survived it, because the next poll corrects a state;
a released port is corrected by nothing. An observation is now applied only if
the record it was taken against has not changed since.

Decisions this slice needed:

- **Every published port is replaced, not only a managed service's.** The plan
  said to reset the publications of managed services the project did not declare
  reachable. Implemented that way it leaves a dependency's fixed port bound —
  which is ADR-034 evidence 12 exactly, and it is enough on its own to stop the
  second task. A published port is global to the machine as a container name is
  global to the Docker daemon, so it is treated the same way: reset everywhere,
  and replaced only where Feat allocated something. What that costs is a
  dependency a user reached at a fixed port by hand; what it buys is that
  concurrency is a property of Feat rather than of the user's diligence, and the
  remedy is one line of configuration — manage the service and declare it
  reachable.
- **The addresses reach the Compose process as well as the containers.** A
  service finds its siblings under the project's own names rather than Feat's.
  The reference project's frontend is a Vite dev server, which exposes only
  `VITE_`-prefixed variables to the browser, so `FEAT_URL_nginx` in the
  container's environment is a value nothing can use; what the project needs is
  to write `VITE_API_BASE_URL: ${FEAT_URL_NGINX}` in its own Compose file, and
  Compose interpolates that from the environment of the process running it. The
  generated variables are therefore passed to the Compose command as well as
  written into the override. They are generated task metadata either way, and
  nothing read from an environment file is in them.
- **The container port is read, and the host port is not.** A publication's
  target is a fact about the project's own Compose files, so Feat reads it
  structurally, in every syntax Compose accepts; the host port beside it is the
  thing an allocation replaces, so it is not read at all. What cannot be read —
  an interpolated entry, which resolving would mean reading the values Feat is
  forbidden to read, or a port range, which is several publications where an
  allocation is one — publishes nothing, and the task says which services those
  are. Refusing the action instead was considered and rejected: a project can
  reach that state by editing one file, and a runtime nobody can stop or destroy
  is a worse answer than a service that says it is unreachable.
- **The range has a default.** `runtime.port_range` defaults to `21000-21999`:
  above the privileged ports, below the ephemeral ones the kernel hands out, and
  a thousand wide. A required field would have broken every configuration written
  for slice 14, which collected the reachable declaration before anything
  allocated from it, and where a machine's own ports lie is exactly the kind of
  value a default should carry and `feat project show` should print.

Consequence: `domain.RuntimeEnvironment` gains the allocations it holds and
releases, beside the publications it observes — an intention and an observation
that can disagree, which is why they are two fields. `runtime.Invocation` gains
an environment, `feat runtime status` and the dashboard show the allocated
address rather than the observed publication, and the reference project's own
frontend names `${FEAT_URL_NGINX}` where it used to name a fixed port.

Three tasks of the reference project then ran their whole applications at once,
each frontend reaching its own task's API and no other's.

Amended after the round-2 review, settled 2026-08-22, correcting what the
generated address means and renaming the variables that carry it.

Evidence 17, `G4-08`: **the address this ADR said tells a service where its
siblings are does not reach a sibling.** The value written into every managed
service's `environment:` is `http://localhost:<allocated port>`, and a published
port belongs to the *host's* network namespace. Read inside a container that
address is the container's own loopback, so a service following the documented
pattern and calling `$FEAT_URL_api` connects to itself and gets a connection
refused; with the loopback `bind_address` Feat now publishes on by default, the
host's port is not reachable from a container at all. The value is right for the
one consumer that runs on the host — a browser opening a frontend, a shell
running `curl`, or a build baking an API address into a bundle a browser then
loads — which is the reference project's own case, and is why three tasks ran
their whole applications at once without this being caught. For a genuine
service-to-service call there is nothing to allocate: the Compose service name
and the container port are already in the project's own files and do not differ
per task.

Two decisions:

- **The generated address is host-scoped, and the documentation that said
  otherwise was derived from this ADR.** The corrected statement lives in
  [07-configuration-model.md](07-configuration-model.md) § Runtime ownership,
  which is where a user reads it. The bullet above is marked rather than
  rewritten, because it is the sentence four other statements were copied from
  and a reader who lands on it has to be turned around there.
- **The variables are renamed `FEAT_URL_` → `FEAT_HOST_URL_` and `FEAT_PORT_` →
  `FEAT_HOST_PORT_`.** The mistake is made while typing a variable into a
  service's `environment:`, and the name is the only thing present at that
  moment — the paragraph explaining it is not. A prefix that says `HOST` refuses
  the sibling reading where the reading happens; a document only corrects a
  reader who is already looking somewhere else. It breaks the configuration
  surface with no compatibility period, on the same calculus as `G5-01`'s format
  break and this ADR's own: one user, pre-alpha, and a migration for a
  population of one is waste. A project's own Compose file that interpolates
  `${FEAT_URL_NGINX}` — including the reference project's frontend, named in the
  slice 16 amendment above — names the new variable instead, and Compose
  interpolates an unset one to empty, so the break is visible on the next start
  rather than silent.

Consequence: `internal/domain/runtime.go` constructs both prefixes and is the
only place that does, so the rename is one edit and a set of assertions that
follow it; the three override goldens carry the new names and their environment
blocks re-sort. Nothing persisted and nothing on the wire carries a variable
name — `api.PortAllocation` carries the address itself — so an existing task's
generated override is simply rewritten with the new names the next time its
services are created or started. A container already running keeps the old names
in its environment until it is recreated, which is the ordinary consequence of
editing an environment and is what `feat runtime create` does.
