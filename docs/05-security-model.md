# Security Model

## Purpose

Feat separates a trusted host orchestrator from coding-agent execution. It prevents the most direct host-control paths while remaining honest about the limits of containers, Git access, general internet access, and provider credentials.

## Trust zones

### Trusted host zone

Contains:

- Feat daemon and TUI;
- Git/worktree lifecycle;
- tmux server control;
- Docker Compose CLI access;
- host project configuration and environment files;
- local state store;
- notifications and resource monitoring.

The daemon is trusted to execute configured host commands and therefore must validate task ownership and destructive targets.

### Agent execution zone

May be:

- host-native, with no container isolation; or
- a configured non-root devcontainer.

The dogfood profile uses a non-root devcontainer.

The agent receives only configured mounts and capabilities. It does not receive Docker access.

### Application runtime zone

Contains task-scoped application services or connections to configured external/shared development services.

This zone may share a Compose project with the devcontainer operationally, but remains a separate domain concept.

### Remote service zone

Future hosted relay and push infrastructure. It is not part of v0 and must follow the maximum-privacy constraints below.

## Dogfood security profile

Required:

- Claude runs inside the configured devcontainer.
- The container user is non-root, and Feat probes for a way back to root rather than assuming there is none.
- The container runtime's own confinement is left on: its syscall filter, its mandatory access control profile, and the kernel interfaces it masks.
- No Docker socket is mounted.
- Docker CLI is absent or denied.
- No daemon/tmux control socket is mounted.
- Only declared repository, infrastructure, control, and explicitly configured credential mounts are available.
- Devcontainer-definition code is normally mounted read-only.
- General outbound internet is allowed.
- Full Git access is allowed.
- `glab` and/or `gh` access may be enabled.

Not required by the dogfood profile:

- read-only container root filesystem;
- dropped capabilities beyond runtime defaults;
- custom seccomp/AppArmor policy;
- network allowlisting;
- microVM isolation.

### What "non-root" is a statement about

The uid an agent runs as is read from the running container, as the configured user, before a session starts, and uid 0 is refused. That answers what the agent *starts* as. An image that installs `sudo` beside a `NOPASSWD` rule — which devcontainer templates commonly do, so that a developer can install a package mid-session — leaves the requirement true at the instant it is measured and not afterwards.

Feat therefore asks the second question too — at every launch, and in `feat doctor` wherever a container of the project is running: whether a tool in the image returns root without a password. What it finds is reported and not refused. Whether an agent may become root inside its own container is a project's decision, and a refusal would be answered by whichever edit made the check stop looking; what Feat can do honestly is say what it found, every time, so the choice is made deliberately rather than inherited from a template.

What passwordless root inside the container reaches is the container runtime's default capability set, which is wider than the agent's user and narrower than a privileged container. `CAP_DAC_OVERRIDE` is in Docker's default set, so file permissions stop constraining anything the container can already reach — including host-owned files under every writable bind mount. `CAP_SYS_ADMIN` is not, so a read-only mount stays read-only: the read-only control workspace holds by the kernel's rule, and Feat refuses a container granted `CAP_SYS_ADMIN` separately.

That capability rule is worth what its enforcement is worth, so Feat checks the enforcement too. Docker's default syscall filter is what stops a process calling `unshare(2)` into a user namespace where it holds `CAP_SYS_ADMIN` whatever `cap_add` says; the AppArmor profile Docker loads denies `mount(2)` on the hosts that carry it; and the kernel interfaces Docker masks are what stand between a root process in the container and `/proc/kcore`, `/proc/sysrq-trigger`, and a writable `/proc/sys`. A container that switches one of those off — `security_opt: seccomp=unconfined`, `apparmor=unconfined`, `systempaths=unconfined` — is refused, the third by its effect, because Docker records it nowhere. Not requiring a custom seccomp or AppArmor policy above is not permission to remove the default one.

A policy of the project's own is reported rather than refused. Feat compares names and does not evaluate policies, so a custom seccomp profile, a named AppArmor profile, and an SELinux label option are reported at launch with what they leave unestablished — including that a profile allowing every syscall is `unconfined` under another name. See ADR-067.

Feat's own `.devcontainer/` image grants passwordless `sudo` deliberately, and its Dockerfile says so.

## Docker boundary

The coding agent MUST NOT receive:

- `/var/run/docker.sock` or an equivalent daemon socket;
- Docker-over-TCP credentials;
- Docker CLI intended to reach the host daemon;
- a Feat runtime-broker API capable of arbitrary operations.

Only the host runtime adapter invokes Docker Compose.

An agent may write a schema-valid `runtime_requested` file. A request is inert until the host daemon validates it and the user approves it.

## Container limitation

A standard devcontainer is a boundary against ordinary tool access, accidental host interaction, prompt-injected shell commands, and common misuse. It is not a hard defense against a deliberate kernel or container-runtime exploit.

The initial threat model does not include undisclosed kernel zero-days. Strong hostile-code isolation requires a later microVM/hardened-runtime backend.

## Filesystem mounts

Every mount MUST be declared by project configuration or generated by Feat for the task.

Categories:

- read-write task worktrees;
- read-only task worktrees;
- stable read-only infrastructure checkout;
- dedicated task control workspace;
- dedicated Claude configuration volume;
- explicitly configured environment/credential mounts.

Feat MUST NOT mount the host home directory or workspace parent by default.

## Git boundary

Full Git inside a native host worktree exposes shared repository metadata. The agent may inspect and mutate refs, branch metadata, worktree metadata, and configuration reachable through the common Git directory.

This is an accepted v0 tradeoff. Feat must not describe it as strict repository-metadata isolation.

Devcontainer execution makes the same exposure explicit rather than incidental: each task repository's Git directory is mounted into the container at its host path, because a task worktree is not a repository without it. The working copy is not mounted, and a container that turns out to mount one is refused. So the agent can commit, branch, and read history, and cannot reach the files the user is editing themselves.

For a read-write task that mount is writable, and it is a host code-execution path. `.git/hooks` and `.git/config` belong to the common Git directory, which the container shares with the user's ordinary checkout. An agent that writes `hooks/post-checkout`, or sets `core.fsmonitor`, `core.pager`, or `diff.external` in `config`, has arranged for a program of its choosing to run on the host, as the user, outside the container. No commit is required and in the cheapest case no user action is: `core.fsmonitor` runs on `git status`, and Feat's own `git fetch` and `git worktree add` run the `reference-transaction` and `post-checkout` hooks when the next task in that project is created.

The accurate claim is:

> The devcontainer is a boundary against ordinary tool access and accidental host interaction everywhere except the Git directory Feat mounts into it. That mount is writable for a read-write task, and write access to it is host code execution.

This is an accepted v0 tradeoff, recorded with its rejected alternatives in ADR-050. It requires no exploit and no misconfiguration, so the container limitation stated above is not the relevant caveat: this is the supported configuration behaving as designed. It is reachable by a prompt-injected agent as readily as a deliberately hostile one.

Mounting the metadata read-only is not available as a mitigation: `git commit` writes through that directory, so it would take FR-GIT-006 and FR-GIT-007 with it. A user who needs the boundary to hold without exception runs the agent against repositories they are willing to treat as trusted, or waits for a task-local Git metadata or clone backend, which is OQ-006 and would reduce this exposure.

## Network and data egress

General internet is allowed in the accepted configuration. Therefore Feat cannot promise that source code visible to the agent cannot be transmitted elsewhere.

The accurate claim is:

> The devcontainer prevents direct access to undeclared local host resources; it does not provide network data-loss prevention.

Source-code egress control needs allowlisted networking and approved inference/registry destinations, which are outside v0.

## Published ports and who can reach them

The section above is about traffic leaving the machine. This one is about traffic arriving: Feat opens listening sockets on the host, and how many is a function of how the product is used.

A task's application runtime publishes one host port per reachable service, allocated from `runtime.port_range`. Three concurrent tasks over two reachable services is six listeners. This is not incidental — running several tasks' applications at once is the feature, and a published port is what makes one reachable.

Feat binds them to `127.0.0.1` by default, so they answer on the machine running them and nowhere else. `runtime.bind_address` changes that, and the two answers differ by more than one line of configuration:

- `127.0.0.1` — reachable by processes on this machine. Not reachable from another machine on the network, and **not reachable from a container** on a bridge network: the loopback interface is not the bridge, so dialling the bridge gateway does not find a socket bound here. The exception is a container sharing the host's network namespace, and that is why the launch check refuses `network_mode: host` for an agent service, in those terms.
- `0.0.0.0` — reachable on every interface the machine has. That means every network it is joined to, including an untrusted one such as a café's Wi-Fi, and it means the Docker bridge: a port on every interface is reachable from *every container on the machine*, so one task's agent container can dial another task's database at the bridge gateway. A loopback binding refuses the same dial.

The accurate claim is:

> Feat's published ports are bound to the loopback address unless the project configures otherwise. Feat does not firewall, authenticate, or otherwise restrict what may connect to them; the binding address is the whole of the control.

A port a repository's own Compose file already gave an address to keeps that address. Feat replaces the host port — which it must, because a fixed one is one task at a time — and never widens an address the user chose.

Feat does not decide what a service does with a connection. An application service reached on a published port answers as its own image is written to, with whatever authentication the project gave it, which for a development stack is often none.

The agent's own execution environment publishes nothing: the generated execution override resets every port of the agent's service, in every configuration. No host port is opened for an agent.

## GitHub and GitLab capabilities

Git-provider access is independent from Docker access. There are two modes, and they differ by which side of the boundary the credential is on.

### Host-side, which is the recommended mode

The daemon makes every credentialed provider call on the trusted host, with the `gh` or `glab` authentication the user already has there. Fetching tickets, pushing a task's branch, and opening a PR/MR are all either before a task exists or after the agent has stopped, so none of them needs a credential inside the agent environment, and `agent.capabilities.github_cli` and `gitlab_cli` stay `disabled`.

What that declaration is worth depends on the execution mode. In a devcontainer it has an effect: a credential the project does not mount is not in the agent's environment. Host-native execution has no inside. An agent launched by `FEAT_HOST_AGENT` runs as the user and inherits the user's environment, so it reaches whatever provider authentication the user has and can call the provider's API directly; there, `disabled` describes intent rather than enforcement, and `feat doctor` says so when the daemon is running host-native. Publication stays host-side in both modes, but for record-keeping rather than containment (ADR-070) — so in a devcontainer the approval step is what stops the agent publishing, and on the host it is what makes Feat's own publication one the user read.

Where the agent's knowledge is needed — the title and description of a PR/MR — it is carried as data. The agent writes a publication draft into the control workspace, requiring no capability because it asks for nothing, and the user reads and edits it before the host sends anything. That review is a control rather than a convenience: the description is agent-authored text bound for somewhere durable, and it can carry anything the agent read.

The reverse direction carries the same caution. A brief composed from a ticket was written by whoever filed the ticket and becomes the agent's instructions, so the confirmation step displays the composed brief rather than the ticket it came from, and ticket comments are excluded by default.

A host-side push runs Git in a repository whose configuration and hooks the agent can write (ADR-050). That exposure is not widened here — Feat already runs `git fetch` and `git worktree add` in it — but the push disables hooks and the external pager and diff commands, so a user-approved publication is not what fires an agent-authored `pre-push`.

Disabling hooks is not free for every reader of this document. A `pre-push` hook is not always its user's own convenience: it may be what scans for secrets before anything leaves the machine, or what refuses a protected branch. Where it is, Feat's publication is the one route out that does not run it. Feat still does not run it, because the agent can write `.git/hooks` and approving a description should not be how a user executes whatever is in there; what Feat refuses is to make that trade silently. The approval step names the hook it is skipping, so a user who depends on one can push by hand instead. OQ-015 holds whether a hook can be attributed to the user rather than to the agent.

See ADR-070 and OQ-015.

### In the agent environment

When a project enables it:

- `gh`/`glab` may be installed in the agent environment;
- authentication may be mounted, injected, or provided by the user's environment;
- Feat validates authentication where possible;
- Claude may push, create PRs/MRs, comment, label, or perform other operations allowed by the credential and user prompt.

The security impact is explicit: an agent with provider credentials can mutate remote repositories within token scope. Least-privilege credentials are recommended. Feat does not automatically merge in initial versions.

A least-privilege token bounds scope, and scope is not the whole of the exposure. General internet is allowed and Feat claims no data-loss prevention, so a token in the agent environment is a durable secret reachable by any prompt injection, including one arriving in the issue body `gh` just fetched. That is the reason the host-side mode exists, and the reason it is the recommended one.

## Claude authentication and state

The recommended devcontainer setup is a dedicated persistent Claude configuration volume using `CLAUDE_CONFIG_DIR`, authenticated interactively once. `agent.claude.config_volume` configures it, and Feat mounts it for every task.

Mounting the user's ordinary host `~/.claude` directory instead is supported only as an explicit project choice. The two ways of doing it are not two degrees of the same risk, and the difference is which direction the mount runs:

- **Read-only** (`~/.claude:/home/<user>/.claude:ro`) exposes what is in the directory: global settings, the record of what the user has approved, and plaintext session data — to every task container, and so to anything that reaches an agent through a repository file, a dependency, or an issue body `gh` fetched.
- **Read-write** is not an exposure with a larger radius. It is a path from the container to code execution on the host, outside every container, as the user. The directory is not only data: `settings.json` holds `hooks`, and `.claude.json` holds `mcpServers`, and both are commands — run by the user's own Claude Code, on the host, the next time they open it. An agent that can write there can arrange that, and the same directory holds the approvals record that would otherwise be asked first.

Read-write is therefore a choice to give the agent host execution deferred by one session, and it should be made in those terms or not made. A project that needs the host directory should mount it read-only; a project that needs Claude's state to persist across tasks should use the configuration volume, which is what it is for.

The dedicated shared volume still permits parallel orchestrated sessions to see one another's Claude-local state. Per-task configuration profiles with safe authentication transfer are a later hardening option.

## Secrets and environment files

- Feat MUST NOT copy secrets into generated YAML or generated Compose overrides.
- Host-side environment files are passed to Docker Compose by path.
- Feat SHOULD avoid reading their values.
- Whether Claude can see a secret depends on whether Compose injects or mounts it into the devcontainer.
- `feat doctor` MUST redact values and print only safe metadata.
- Generated task variables such as task ID, Compose project name, ports, and database selector are non-secret unless project configuration declares otherwise.

## Control workspace

The task control workspace is a constrained file protocol, not a command socket.

Requirements:

- per-task directory;
- files owned by the task container user where write access is required;
- host-generated input written atomically;
- agent output written to a temporary file and atomically renamed;
- versioned schemas;
- monotonically increasing event sequence or unique event ID;
- path traversal prevention;
- size limits;
- capability validation;
- processed-event tracking and idempotency.

No control message directly executes shell text.

## Local daemon API

- Unix-domain socket only in v0;
- socket owned by the current user with restrictive permissions;
- no TCP listener;
- explicit request validation;
- no secrets in event payloads;
- destructive actions resolve stable task/resource identities, not caller-supplied arbitrary paths.

## tmux

Feat uses a dedicated tmux server/socket. The agent container receives neither the tmux socket nor host tmux commands.

Loading the user's tmux configuration is a compatibility feature. Feat must not trust window indexes, names, or user hooks as stable identity; it tags managed sessions/windows with product metadata and reconciles them.

A tagged object whose metadata cannot be read is quarantined and reported rather than guessed at, and never adopted. Values Feat supplies to tmux are checked against the separators its own discovery parses, so a working directory cannot make the server's report unreadable.

## Cleanup safety

Before destructive cleanup, Feat MUST:

1. resolve resources from persisted task ownership and observed state;
2. reject broad or unresolved paths, and re-check every path against the directory Feat owns immediately before deleting it;
3. display dirty, unpushed, and unmerged Git state;
4. separate containers/networks, volumes, worktrees, and branches;
5. retain volumes by default;
6. require explicit confirmation for dirty/unmerged work;
7. archive task metadata.

## Remote privacy target

The future hosted service should follow these constraints:

- local daemon creates the outbound connection;
- paired clients and daemon establish end-to-end encryption;
- relay stores no source code or terminal transcripts;
- notification payloads contain no task text by default;
- last-known non-sensitive state is stored on the client device;
- no queued terminal input while host is offline initially;
- high-impact actions require explicit confirmation;
- device sessions can be revoked;
- local/LAN use remains available without the hosted relay.

Exact terminal-streaming protocol and mobile UI remain open product decisions.

