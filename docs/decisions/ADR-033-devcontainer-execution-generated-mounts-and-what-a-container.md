# ADR-033 — Devcontainer execution, generated mounts, and what a container may not already hold

Status: accepted
Recorded: 2026-08-06, before implementation

Evidence found while planning devcontainer execution. Items 2 to 5 and 7 were
measured against Docker Compose v5.1.4 and Docker Desktop on the target machine
rather than reasoned about, because every one of them decides what the generated
override may assume:

1. The reference devcontainer's own Compose file mounts the ordinary checkout of
   every working repository read-write, at the paths the agent works in. A
   generated override that mounted task worktrees at *different* container paths
   would leave those in place, and the agent would hold both its task worktree
   and the user's real checkout. That breaks the property slice 4's first
   acceptance criterion exists to protect, and it breaks it silently: everything
   Feat records about the task would be correct.
2. Compose merges a service's `volumes` by target path. A base entry
   `./a:/mnt/x` is *replaced* by an override entry `./b:/mnt/x:ro` — source
   swapped, read-only applied — while unrelated targets survive and new ones are
   added. So a generated override can take over a mount, and the container path
   is the key it takes it over by.
3. `container_name` is global to the Docker daemon and a published port is
   global to the host, so a base file carrying either cannot be brought up three
   times at once whatever project name it is given. `container_name: !reset null`
   and `ports: !reset []` both erase the base value. The `!reset` tag requires
   Compose 2.24 or later.
4. `--project-directory` pinned at the first base file keeps that file's
   relative sources and build contexts resolving while the generated override
   lives somewhere else entirely, which it must: the override belongs in the
   state directory, not in the user's repository.
5. A `0700` host directory owned by the host user is writable by a container
   process running as a different uid on Docker Desktop for macOS, and a file
   the container writes with mode `0600` reads back from the host. The
   file-sharing layer maps ownership both ways. On Linux it does not, and the
   failure mode there is that every hook writes nothing and the task reports
   nothing — the silence ADR-032 was written to prevent.
6. Validation has to run where the agent will run, and for a devcontainer that
   means the container must already be up. ADR-032 put validation before
   anything was created, which is no longer possible without also being useless.
7. `docker compose exec` registers `-T/--no-tty` with a default of `true` in its
   own help output. Read literally, an interactive Claude in a tmux pane would
   get no terminal.
8. `feat doctor` may not start a container (ADR-028), but a container Feat
   started carries Feat's own ownership labels, and labels are discoverable
   without reading any persistent state.
9. A project may already supply the agent's Claude configuration through its own
   Compose file, as the reference project does by mounting the user's `~/.claude`.
   A configuration volume Feat mounted unconditionally would fight it.

Decisions:

- `internal/execution` holds the interface and neutral types; `internal/execution/compose`
  holds every Docker-specific decision. Both receive final values — absolute
  Compose files, project name, service, user, mounts, labels — and read neither
  configuration nor persistent state, under the rule ADR-029 established for Git
  and ADR-032 for the agent. An `execution-stays-an-adapter` `depguard` rule
  makes it mechanical; ADR-025 requires an ADR for a boundary rule, and this is
  that record.
- The interface in [06-technical-architecture.md](06-technical-architecture.md)
  is amended in three places, and the amendments are the reason this bullet
  exists rather than a silent divergence. `Command` returns an argument vector
  rather than an `*exec.Cmd`, because the terminal backend constructs the process
  and an `*exec.Cmd` returned here would be a process nobody runs. `Run` is added
  for probes, because validation asks questions of an environment rather than
  attaching a terminal to it. `Shell` is folded into `Command`, because the
  daemon already builds the shell command and a second entry point would be a
  second place to decide what a task shell is. `Destroy` is not implemented:
  destruction policy — what is retained, what requires confirmation — is slice
  12's, and an untested destructive path with no caller is worse than none.
- The agent's Compose project is `feat-agent-{project_id}-{task_id}`, fixed
  rather than configurable. It is Feat's own resource, the prefix cannot collide
  with a project the user runs by hand from the same files, and a template would
  be a knob whose only use is to break the guarantee it exists to provide.
- The generated override is written to
  `<state>/execution/<project-id>/<task-id>/compose.override.yaml`, beside the
  control root rather than inside a task's snapshot directory. It is host-only
  and never mounted, so nothing the agent can read or write decides what its own
  container mounts; and the snapshot directory holds the documents the storage
  interface owns, which a file an execution adapter writes is not. `internal/paths`
  gains the root, and the daemon builds the per-task path below it after
  validating both identifiers, exactly as `internal/control` does.
- Every task worktree is mounted at the container path its repository
  configures, read-only when the task's access is read-only, and a
  `stable_read_only` repository is mounted read-only from its ordinary checkout.
  For evidence 2 this replaces whatever the base file mounted at that path, which
  is what makes a configured `container_path` that disagrees with the base file a
  configuration error rather than a preference.
- `container_name` and `ports` are reset unconditionally for the agent service,
  and the task detail says so in fixed words. Emitting them conditionally would
  mean rendering the user's resolved Compose configuration to find out whether
  they were there, and that document contains the values of their environment
  files.
- After the service is up, Feat inspects the running container's mounts and
  refuses the launch when one of them is a Docker socket or has a configured
  repository checkout as its source. The check reads the container rather than
  the resolved Compose configuration, so no environment-file value is ever
  rendered, and it is evidence about what exists rather than a claim about what
  was asked for. This is what makes "the agent has no Docker access" and "the
  agent cannot reach your ordinary checkout" statements about the running system.
- Launch order becomes start, validate, prepare, attach: the service is brought
  up, every probe runs inside it, the provider adapter then generates its files,
  and only then does the task get a terminal. This amends ADR-032's "validation
  creates nothing" for devcontainer execution alone, and the thing created before
  validation is the environment the validation is about.
- A failure after the service is up leaves it running and marks the task
  `failed`, naming the Compose project to inspect. Nothing is undone, for
  ADR-029's reason and one more: an entrypoint may already have had effects that
  stopping the container does not undo.
- Control-workspace and worktree write access are probed inside the container as
  the configured user before Claude is launched, and a failure is actionable
  rather than silent. Evidence 5 says this passes on the dogfood platform and
  will not on Linux, and a probe is the difference between a diagnosis and a task
  that runs all day reporting nothing.
- `agent.claude.config_volume` stays optional and gains `agent.claude.config_path`,
  default `/feat-claude`, validated for absoluteness and non-overlap as
  `control_path` is. When no volume is configured Feat mounts nothing and sets no
  `CLAUDE_CONFIG_DIR`, leaving the provider's configuration to the project, which
  is what evidence 9 requires and what the security model already permits as an
  explicit project choice.
- A Claude configuration volume in a `host` project is rejected rather than
  ignored, which it was until now. It is the same rule ADR-028 applied to the
  execution fields, and the belief it prevents is a specific one: a user who
  configured a dedicated volume and is in fact handing the agent their own
  `~/.claude`.
- `feat doctor` still starts nothing. It probes inside a container that Feat's
  ownership labels identify as a live task container of the project, and reports
  `skipped` with the reason otherwise. The reason names the condition rather than
  a slice, because from this slice on the check is deliverable and it is the
  machine's state that decides whether it can run.
- The minimum supported Docker Compose version is 2.24, for the `!reset` tag in
  evidence 3, and `feat doctor` checks it. A version below it fails at launch
  with a YAML error from Compose that says nothing about why Feat wrote that
  document.
- The form of `docker compose exec` that yields an interactive terminal is
  pinned by a test that runs it in a real pane. Evidence 7 is the ADR-032
  evidence-12 shape exactly: a flag whose documented default would break the
  session while every signal Feat observes reports success.
- ADR-032's statement that the provider-native completion gate "waits for slice
  8" is corrected rather than left standing. Slice 8's work list and acceptance
  criteria never contained it, and a promise the plan does not schedule is a
  promise nothing keeps. The gate moves to slice 11, which owns review state,
  with its own work item and acceptance criterion; the strings in `internal/ui`
  that name slice 8 move with it.

Consequence: `verifying` and `verification_failed` remain unreached, and
verification stays the agent's own claim, exactly as ADR-032 narrowed it. What
changes is which slice says so.

Amended after running the adapter against real Docker, with evidence the unit
tests could not produce:

10. Docker Compose reports "no such executable" on **standard output** rather
    than standard error. Reading only standard error is the obvious
    implementation, passes every test written against a fixture, and reports
    every absent tool as present. A container with no `mktemp` would therefore
    have launched an agent Feat could never hear from, and a `required` provider
    CLI that was not installed would have passed validation — the two failures
    the probes exist to prevent. Both streams are now read, and both directions
    are pinned by a test.
11. A tool that ran and could not open a file also says "no such file or
    directory". The first version of the same check read that as an absent
    executable, so `cat` failing on a missing file was reported as `cat` not
    being installed. The check now requires the container runtime's own quoted
    refusal to start the program, which is what distinguishes the two.

Evidence 10 and 11 are the same shape as ADR-032's evidence 12: Feat asking a
correct question and reading the answer wrongly, with every signal it observes
reporting success. Neither was reachable from a fixture, because both are
statements about what the real tool prints and where.

Amended again after launching a task in the reference project's own
devcontainer:

12. **A task worktree is not a repository inside the container.** Its `.git` is a
    file holding the absolute host path of the main checkout's
    `.git/worktrees/<name>`, and nothing mounted that directory. Every Git
    command in the container therefore failed with "not a git repository", while
    the container, its mounts, its user, and every state Feat recorded were
    exactly right. `agent.capabilities.git: full`, FR-GIT-006, and acceptance
    criterion 5 were all false at once, and nothing Feat observes would have said
    so.
13. A base file's mount that lives *inside* a repository mount is satisfied by
    the ordinary checkout and not by a task worktree. The reference devcontainer
    masks each repository's ignored `.env` by mounting `/dev/null` over it, and a
    worktree holds only what Git tracks, so the file is not there and the
    container runtime refuses to create it. The same shape breaks a named volume
    nested inside a repository the task selected read-only.
14. `docker compose up` narrates every build step and every resource it creates,
    so the first line of a failed `up` is "Image … Building" and the reason is the
    last line. Feat reported the first, which turned a precise mount error in the
    user's own Compose file into a progress message.

Decisions:

- Each task repository's Git directory is mounted at the same absolute path it
  has on the host, with the access its worktree has. That is what makes the link
  a worktree records resolve, whatever Git version wrote it and whether it wrote
  a relative path or an absolute one. What it exposes is repository metadata,
  which [05-security-model.md](05-security-model.md) already accepts by name and
  declines to call isolation; what it does not expose is the working copy, since
  the checkout's own directory then holds nothing else in the container.
- The rule that refuses a mount of an ordinary checkout is widened to any
  directory containing one or contained by one, with that Git directory as its
  single exception. Before this it caught a parent and missed a child, so
  mounting `<checkout>/src` would have exposed part of the working copy
  unnoticed.
- A failure that the container runtime reports against a path Feat mounted is
  explained in Feat's own terms — which repository, which access decision, and
  what to change — rather than left as an accurate sentence about a path. The
  two cases evidence 13 produces are both recognised.
- A failed `up` reports its last line rather than its first.

Evidence 12 is the most important thing this slice found, and it is the reason
the acceptance criteria are verified by running commands inside the container
rather than by reading what Feat generated. Everything Feat generated was
correct.

Acceptance criterion 5 asks that full Git and the required provider CLI work
inside the agent environment. The reference devcontainer has `gh` installed and
deliberately logged out, and the project configures `github_cli: optional`, so
the criterion is verified as full Git against a real task worktree plus the
refusal path — a project that requires a CLI its environment cannot authenticate
fails launch with an actionable message. The positive required-CLI path is not
verified, and is recorded here as not verified rather than reported as passing.

The Slice 3 target-machine acceptance check remains outstanding, as it did for
slices 5, 6, and 7. Slice 8 proceeds under the same explicit maintainer
approval.

Amended a third time, after the alpha review traced acceptance criterion 1
through the generated document:

15. **The reset covered the agent's service and not what starting it starts.**
    `Prepare` runs `docker compose up --detach <service>`, which brings up that
    service's whole `depends_on` closure, and the override named one service. So
    a devcontainer whose `dev` depends on a `db` with a fixed `container_name` and
    a published port ran once per machine: the second task's launch was refused by
    Docker over the first task's `db`, in a message about a service the user did
    not know Feat was starting. It is ADR-034 evidence 12 exactly — "the one thing
    a per-task Compose project exists to prevent, reintroduced by the services
    nobody had listed" — and the runtime adapter had already fixed it while the
    execution adapter had not.

Decisions:

- The generated execution override reaches every service the project's own
  Compose files define. Which they are is read with
  `docker compose config --services` against those files, without the generated
  override so a stale one cannot reintroduce a removed service and so the first
  launch does not fail on a file that does not exist yet. It prints names and
  nothing else, so evidence 5 of ADR-034 still holds: no environment-file value is
  rendered.
- **A service the agent does not run in has both `container_name` and `ports`
  reset**, which is deliberately not what ADR-034 decided for the application
  runtime. ADR-034 keeps a published port because it is how the user reaches the
  application they are testing; a dependency of a devcontainer is not that
  application. Feat surfaces no port from an `feat-agent-*` project, services in
  one Compose project reach each other over its network rather than through a
  published port, and a host port left in place is acceptance criterion 1 failing
  on the second task whatever the container name says. The cost is stated rather
  than hidden: a devcontainer dependency the user reached at a fixed host port is
  no longer published. The alternative is that the second task cannot start.
- Such a service gets those two lines and nothing else — no task worktree, no
  generated variable, and no ownership label. Labels here are not ADR-034's: they
  are how `feat doctor` finds the container the agent runs in without reading
  stored state, and putting them on a database would send that diagnostic looking
  for Claude inside Postgres. Cleanup does not need them, because it resolves what
  a task owns through Compose's own project label.
- The task detail says the reset covers the agent's service and everything Compose
  starts alongside it, in fixed words, as it already said the narrower thing.

Found by review rather than by running, and the reference project could not have
found it: its devcontainer defines one service, with no `depends_on`, no
`container_name`, and no published port, so both resets were already no-ops there
and acceptance criterion 1 held before this change as well as after — verified by
two tasks running side by side on it, each with one container, alongside the
maintainer's own hand-started devcontainer. The defect is reachable for any
devcontainer whose agent service has a name- or port-bearing dependency, which is
the ordinary shape of one that develops against a database.

So the reproduction lives in the opt-in fixture rather than in the dogfood
project: its devcontainer depends on such a service, and against the previous
generator the second of three tasks fails to launch with Docker's own conflict
message. That is the whole of the evidence, and it is worth being exact about
which project it comes from.
