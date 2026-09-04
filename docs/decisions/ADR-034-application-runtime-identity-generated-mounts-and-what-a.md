# ADR-034 — Application runtime identity, generated mounts, and what a manual lifecycle owns

Status: accepted
Recorded: 2026-08-07, before implementation

Evidence found while planning the manual application runtime:

1. [06-technical-architecture.md](06-technical-architecture.md) lists
   `runtime/start`, `runtime/stop`, and `runtime/logs-info`, while FR-RUN-005
   requires create, start, stop, status, logs, and destroy. Three of the six
   actions have no endpoint. `status` in particular cannot be a read of the
   stored snapshot: a runtime's state is an observation, and the snapshot holds
   the last one somebody took.
2. A repository's `container_path` is documented as the path *the agent's*
   Compose files already mount it at (ADR-033, [07-configuration-model.md](07-configuration-model.md)).
   The application's Compose files are a different set and may mount the same
   repository somewhere else. Compose merges a service's `volumes` by target, so
   a path that disagrees adds a second mount rather than replacing one, and the
   services run the user's ordinary checkout while every record Feat keeps about
   the task stays correct. It is ADR-033 evidence 1 in the zone where the
   security model does not forbid the mount, because the application runtime is
   inside the trusted host and the agent is not in it.
3. `container_name` and a published port are both global, which for the agent
   service made resetting both necessary (ADR-033 evidence 3). For the
   application runtime they are not the same question. A container name is
   Feat's own problem; a published port is how the user reaches the application
   they are testing, and v0.1 excludes port allocation
   ([08-v0-scope.md](08-v0-scope.md)).
4. Nothing observes a runtime unless something asks it to. The dashboard
   re-reads task state on every event and every two seconds, so observing inside
   a read would run one `docker compose ps` per task per refresh — and an
   observation is a write, so a write inside a read would publish an event,
   which would cause the next read. Slice 6 has already paid for that shape once.
5. `docker compose config` renders the resolved project including the values of
   the project's environment files, which Feat must never read (ADR-028). The
   ports, networks, and volumes a runtime owns have to come from somewhere else.
6. Slice 9's work list contains destroy, while slice 12 owns cleanup plans, plan
   tokens, the separation of destructive classes, and the confirmation rules for
   dirty or unmerged work.
7. `domain.RuntimeEnvironment` records the exact inputs a runtime was created
   from, and a project file can be edited between one action and the next.
8. `internal/execution/compose` already drives the Docker Compose CLI, and
   CLAUDE.md keeps the application runtime separate from agent execution even
   where both use Compose.

Decisions:

- `internal/runtime` holds the interface and neutral types;
  `internal/runtime/compose` holds every Docker decision. Both receive final
  values and read neither configuration nor persistent state, under the rule
  ADR-029 established for Git and ADR-033 for execution. A
  `runtime-stays-an-adapter` `depguard` rule denies it configuration, storage,
  the daemon, the transport, and the execution adapter; ADR-025 requires an ADR
  for a boundary rule, and this is that record.
- The Compose CLI plumbing — the runner, the `ps` decoding, the version check —
  is duplicated rather than shared with `internal/execution/compose`. A shared
  package was considered and rejected: it is not in the documented package
  layout, and it would put the agent's environment and the application runtime
  behind one type, which is the distinction the domain model, the security
  model, and CLAUDE.md all keep. Roughly a hundred and fifty lines is the price
  of a boundary that three documents state.

  **Extended by ADR-094**: the price is roughly three hundred lines a side now,
  and the cost this paragraph knowingly accepted has been paid once — ADR-033
  evidence 15 records the `depends_on`-closure reset fixed in the runtime adapter
  and not in the execution one, and calls itself "ADR-034 evidence 12 exactly".
  The boundary stands. What is added is that where duplication is mandated the
  correspondence is pinned by a test rather than left to attention, and that the
  candidate here — both `parseContainers` implementations run over one recorded
  Compose output — is named there with the condition under which it becomes worth
  its upkeep.
- All six actions get an endpoint, and the endpoint list in
  [06-technical-architecture.md](06-technical-architecture.md) gains `create`,
  `status`, and `destroy`. `status` is a POST because it observes and records
  what it observed. `destroy` carries `{"confirm": true}` and is refused without
  it, so a stray request cannot remove anything: it is the shape ADR-031 used
  for the launch fingerprint, where the request carries what the user agreed to.
- The generated override is written to
  `<state>/runtime/<project-id>/<task-id>/compose.override.yaml`, beside the
  execution root that ADR-033 placed under the same rule. It is host-only and is
  never mounted anywhere.
- The runtime's identity is `runtime.project_name_template` expanded for the
  task, which validation already requires to carry `{task_id}` or `{task_key}`.
  Unlike the agent's Compose project it is configured rather than generated,
  because the user brings these services up by hand today and the name is theirs.
- Task worktrees are mounted at each repository's configured `container_path`,
  into every service the project lists under `runtime.services`. After a start,
  Feat inspects the started containers and records a note when one of them
  turned out to mount an ordinary checkout. It reports rather than refuses:
  evidence 2 is a correctness problem here and not a boundary breach, and
  refusing would stop a project whose own Compose file mounts a checkout Feat
  has no task worktree for. The note names the service and the repository, so
  the silent version of the failure does not exist.
- `container_name` is reset for every service in the task's Compose project — the
  managed ones and, since evidence 12, everything Compose starts alongside them —
  and published `ports` are left exactly as configured. A port two tasks both want is explained in Feat's
  terms — that this is the other task's runtime and that v0 allocates no ports —
  rather than passed through as a bind error. [07-configuration-model.md](07-configuration-model.md)
  gains the runtime half of the rule it currently states for the agent alone.

  **Superseded for ports by ADR-065.** The rule rested on the second half of its
  own reason: that a published port is how the user reaches their application and
  that v0 allocates none of its own. v0 now allocates them, so what a task
  publishes is a port Feat can tell the user about rather than a number only one
  task can hold, and every other publication in the project — a managed service
  the project did not declare reachable, and a dependency it never named — is
  reset like the container name it sits beside. The explanation this rule offered
  instead was an explanation of a task that could not start.
- Runtime state is observed by a slow poll over the tasks that hold a runtime
  record, and a poll writes and publishes only when the observed state or health
  changed. Ports come from `ps`; networks and volumes come from
  `docker network ls` and `docker volume ls` filtered on Compose's own project
  label, so evidence 5 never has to be worked around.
- Destroy is `docker compose down` without `--volumes` and without
  `--remove-orphans`. It removes the containers and networks of the task's own
  Compose project, retains every volume and says which ones it retained, never
  names an external resource, and never looks at a worktree or a branch. The
  wider question — which classes a user may choose, and what a dirty worktree
  requires — stays slice 12's, and this is deliberately the narrow half.
- The recorded inputs win while a runtime exists, as they do for the agent's
  environment. A runtime whose state is `absent` — never created, or destroyed
  since — is re-resolved from current configuration through a domain method that
  refuses in any other state. So a user who fixes their Compose file after
  destroying a runtime gets the fixed one, and a user who edits it while services
  are running does not silently point an action at a different project.
- The selector value Feat generates for an external resource is the task key: a
  short, unique, non-secret identifier the application can use to pick its share
  of a shared development database. Feat names it and never creates, migrates, or
  drops anything behind it. OQ-011 stays open.
- No workflow transition starts, stops, or destroys a runtime. Approval offers
  to stop and does not act, which is the one acceptance criterion of this slice
  that reaches into a slice that has not happened yet: slice 11 delivers the
  approval action, and the offer is rendered for a task that has reached
  `approved` however it got there.

Consequence: the user-visible additions are four `feat runtime` subcommands and
three endpoints. No stored format changes — `domain.RuntimeEnvironment` and its
document have carried every field this slice fills since slice 1 — so no
migration is needed, and `verifying` and `ready_for_review` stay exactly as
narrow as ADR-033 left them.

Amended after running the adapter against real Docker, with evidence the unit
tests could not produce:

9. **Stopping a service makes it exit 137.** `docker compose stop` sends
   SIGTERM and kills the container when it does not exit, and a process running
   as PID 1 has no default signal handlers — so the ordinary
   `command: sleep infinity` service that every devcontainer and most
   application images use exits by signal. The obvious rule, that a non-zero
   exit is a failure, therefore reported every stop the user had just asked for
   as `failed`. It is the same shape as ADR-033 evidence 10 and 11: Feat asking
   a correct question and reading the answer wrongly, with every fixture-based
   test passing.

Decision: an exit produced by SIGINT, SIGKILL, or SIGTERM — 130, 137, and 143 by
the shell's convention — is `stopped`, and any other non-zero exit is `failed`.
The distinction is the difference between a state that means something and one
that cries wolf on the ordinary path, and a state people learn to ignore is
worse than no state at all. Both readings are rows in the pinned aggregation
table, so the defect has to be introduced by editing the table that documents it.

Amended again after driving a real daemon, a real client, and real Docker
through one task's whole lifecycle. Two more defects, and neither was reachable
from the adapter's own tests:

10. **A host-execution project mounted nothing.** A task's recorded
    `container_path` is filled only for devcontainer execution, because that
    field says where the *agent's* container mounts the worktree and a
    host-native agent has no container (slice 8). An application runtime has
    containers whatever the agent does, so reading the mount from the binding
    produced a generated override with no `volumes:` at all: the services ran
    the user's ordinary checkout, every record Feat kept was correct, and the
    only thing that said so was the note added for the other half of this
    problem. The mount target now comes from the project's configuration, which
    is where a project declares where its containers hold a repository; the
    binding stays as honest as slice 8 made it.
11. **Asking what is running failed before anything had been created.** Every
    Compose command carries the generated override, and that document does not
    exist until a create or a start writes it — so the first thing a user does,
    `feat runtime status`, answered with a Compose error about a file Feat
    generates. The file is now passed only when it is there. Every path that
    creates something writes it first, so nothing else changes.

Both are the ADR-033 evidence-1 shape rather than the evidence-10 one: not a
wrong answer read wrongly, but a correct implementation of something that had
quietly stopped being the question. Each is now a test that fails against the
behaviour it replaced.

Amended a third time, after the slice was used on a real application:

12. **A stop left a task's database running.** The user's project manages `api`
    and `nginx`; `api` depends on a migration that depends on PostgreSQL, so
    `docker compose up api nginx` started four containers. Every action then
    named the managed services, so the stop stopped two of them. The database
    stayed up, kept its published port — which is global to the host, so no
    second task could ever have bound it — and appeared in no status Feat
    printed, because `ps` named the same two services. Nothing short of a
    destroy would have stopped it. The two containers Compose had started also
    kept the fixed `container_name` their base file gives them, because the
    generated override named only the managed services: the one thing a per-task
    Compose project exists to prevent, reintroduced by the services nobody had
    listed.

Decision: `runtime.services` is what a create and a start *target*, and it is not
what exists. Everything Compose starts to satisfy those services lands in the
task's own Compose project, and everything in that project is there because Feat
acted — so stop, status, logs, and destroy address the project and name no
service, and the generated override reaches every service the project defines.
A service the project did not name gets exactly two things: its `container_name`
reset, and Feat's ownership labels. It is not given the task's worktrees or the
generated variables, because the project did not ask Feat to manage it.

**Extended by ADR-065**: it also loses its published ports, which is the second
global value this evidence is about — the database of this very defect kept
5432 and no second task could ever have bound it. A dependency's port is not
replaced by an allocated one, because a service the project never named is not a
service it asked to reach; a project that does reach one manages it and declares
it reachable. The rest of the rule stands: no worktree, no generated variable,
nothing else.

The aggregation table gains one row with it: a service Compose started alongside
a managed one counts towards the runtime's state unless it exited cleanly. A
one-shot migration that has done its job is the ordinary path of every project
that uses `service_completed_successfully`, and a runtime that reported `degraded`
every time one succeeded would be the state-that-cries-wolf of evidence 9 again.
One that is up, restarting, or failed does count, because then the application
really is partly there or broken.

Which services Compose will start is read with `docker compose config --services`
against the project's own files, without the generated override so that a stale
one cannot reintroduce a service the project has since removed. It prints service
names and nothing else, so evidence 5 still holds: no value from an environment
file is read.

Two smaller things were fixed with it: a published port was listed once per
protocol family, so the same port appeared twice on a screen whose purpose is
telling a user where to reach their application; and a status showing a
container the project never named now says where it came from, because a service
appearing without explanation is a service a user has to go and investigate.

The Slice 3 target-machine acceptance check was settled during slice 8, so slice
9 is the first slice since slice 4 that starts with none outstanding.

Amended a fourth time, after the first create a user asked for on a new task:

13. **`docker compose create` does not build what it is about to create.**
    Given `docker compose create api`, where `api` depends on a service built
    from the project's own Dockerfile, Compose builds the image of `api`, then
    creates a container for the dependency from an image it never built, and
    fails with `No such image: feat-<project>-<task>-prepare:latest`. `up` on the
    same services builds the whole closure. The image name carries the Compose
    project name, which is per task, so no image exists the first time a task is
    created — create failed on every new task and start on none, which made the
    action look broken rather than the command wrong. Measured on Docker 29.5.2
    and Compose 5.1.4, with and without bake, and with `--build`, which does not
    change it.

Decision: create is `docker compose up --no-start` over the managed services.
It builds the dependency closure, creates every container in it, and starts
nothing, which is what FR-RUN-005's create means; against containers that
already exist it does exactly what `create` did. The name of the Compose
subcommand was never the contract — the state the user asked for is — and this
is the same shape as evidence 12: an action Feat targets at the managed services
has to account for everything Compose brings with them. The opt-in fixture's
one-shot dependency is now built rather than pulled, so the defect fails a test
against real Docker rather than waiting for the next new task.

Amended a fifth time, after a user started a task's services for the first time:

14. **The client stopped waiting before the work could finish.** A
    `runtime/start` failed with `Post "http://feat/v1/tasks/…/runtime/start":
    context deadline exceeded` and succeeded when the user asked again.
    `internal/client` bounded every request at ten seconds, on the reasoning that
    the daemon is local and a local request that takes longer is stuck rather
    than slow. That reasoning holds for every endpoint that answers out of what
    the daemon already knows and for none of the ones where it drives Docker: a
    first start pulls the images the project names and runs the builds it
    defines, and the second start of the same task answers in about a second —
    which is why the ceiling is invisible until the first run of a project
    nobody has built on that machine, and why trying again looks like a fix.

    It is the launch defect of slice 13's work list, at a second endpoint and
    with worse consequences. The client's deadline cancels the request, the
    daemon's handler context is the request's, and `exec.CommandContext` kills
    the process when its context ends — so the ten seconds did not only produce
    a misleading error, it killed a `docker compose up` part way through
    creating a project. Nothing was written about it anywhere: the failure
    classifies as an invalid request, the daemon logs only what it answers with
    a 500, and the connection the answer would have gone to had already gone.

Decision: one manual runtime action has one budget, `api.RuntimeTimeout`, which
lives in the API package because it is a term of the endpoint's contract rather
than either end's private business. The daemon bounds the whole action with it
instead of relying on the ten minutes `runtime.HostRunner` allows each Docker
command, so the ceiling is a single number rather than an unknown multiple of
one. The client waits for that number plus a minute, the same margin the daemon
already allows a completion gate over its own timeout, so what ends a slow
request is the daemon's diagnosis rather than the client's silence.

Both ways of running out — Feat's own budget, and a caller that went away — are
reported as what they are, name the action and what it did not finish, and point
at `feat runtime status`, because what they leave behind is a Compose project
that may be half created. The record already names it before anything is created
(ADR-029, ADR-033), the observer corrects the state within one poll, and neither
path undoes anything: tidying up after a start that was interrupted is the
destructive act nobody asked for. A caller that went away is logged as well,
since by definition there is nobody left to answer.
