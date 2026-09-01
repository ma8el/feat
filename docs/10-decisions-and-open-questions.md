# Decisions and Open Questions

This document is the architecture decision log. Each accepted decision is one
file under [decisions/](decisions/), recording what was decided, the evidence
that decided it, and when; this document is the index over them. Read the index,
and open the decisions your change touches. Accepted decisions should not be
reopened without new evidence.

A new decision is written as a file and gains a row here in the same change. A
test in `internal/guard` holds the two together, and holds every `ADR-NNN` named
anywhere in the repository to resolving to exactly one file (ADR-089).

## Accepted decisions

- **[ADR-001 — Working name and CLI](decisions/ADR-001-working-name-and-cli.md)** · accepted  
  The product is Feat, the binary is `feat`, bare `feat` opens the dashboard,
  and commands are scoped rather than ambiguous.

- **[ADR-002 — Product type](decisions/ADR-002-product-type.md)** · accepted  
  Feat is an orchestration layer over native tools and replaces none of them.

- **[ADR-003 — Task invariant](decisions/ADR-003-task-invariant.md)** · accepted  
  One task owns one agent session, one set of worktrees, and one feature
  runtime; tasks do not share feature environments.

- **[ADR-004 — Multi-repository from the beginning](decisions/ADR-004-multi-repository-from-the-beginning.md)** · accepted  
  The domain model carries several repositories per task from v0.1, because the
  reference project needs three.

- **[ADR-005 — tmux backend](decisions/ADR-005-tmux-backend.md)** · accepted  
  tmux is required, runs on a dedicated Feat socket, and is an execution backend
  rather than the source of task truth.

- **[ADR-006 — TUI shape](decisions/ADR-006-tui-shape.md)** · accepted  
  A structured dashboard and task detail with native terminal attach; no
  recreated Claude UI and no source diff renderer in v0.

- **[ADR-007 — Technology](decisions/ADR-007-technology.md)** · accepted  
  Go, Cobra, Bubble Tea, one binary with daemon, TUI, and CLI modes, Apache 2.0.

- **[ADR-008 — Daemon from the beginning](decisions/ADR-008-daemon-from-the-beginning.md)** · accepted  
  The TUI auto-starts a local daemon, explicit daemon commands exist, and the
  daemon is the sole state writer.

- **[ADR-009 — Local API](decisions/ADR-009-local-api.md)** · accepted  
  HTTP/JSON over a Unix-domain socket with SSE for state events, and no TCP
  listener in v0.

- **[ADR-010 — Configuration and state formats](decisions/ADR-010-configuration-and-state-formats.md)** · accepted  
  YAML for configuration, JSON for snapshots, JSON Lines for events, Markdown
  for briefs, and file-backed storage behind an interface.

- **[ADR-011 — Agent providers](decisions/ADR-011-agent-providers.md)** · accepted  
  Claude Code only in v0, behind provider-neutral interfaces from the beginning.

- **[ADR-012 — Agent execution profiles](decisions/ADR-012-agent-execution-profiles.md)** · accepted  
  Devcontainer execution is required for the dogfood and optional product-wide;
  host-native execution joins public v0.

- **[ADR-013 — Runtime integration](decisions/ADR-013-runtime-integration.md)** · accepted  
  Invoke the Docker Compose CLI on the host, with base files, a generated
  override, and a manual application lifecycle.

- **[ADR-014 — Docker security](decisions/ADR-014-docker-security.md)** · accepted  
  The dogfood agent is non-root and receives no Docker socket or CLI, and a
  devcontainer is not claimed to resist deliberate kernel exploitation.

- **[ADR-015 — Network](decisions/ADR-015-network.md)** · accepted  
  General internet access is allowed, and Feat claims no network data-loss
  prevention.

- **[ADR-016 — Git access](decisions/ADR-016-git-access.md)** · accepted  
  Claude has full Git access, the shared worktree metadata is documented, and
  Feat never commits on its own in v0.

- **[ADR-017 — GitHub/GitLab access](decisions/ADR-017-github-gitlab-access.md)** · accepted  
  Claude may hold authenticated `gh`/`glab` in its environment; Feat validates
  the declared capabilities and still denies Docker.

- **[ADR-018 — Task input](decisions/ADR-018-task-input.md)** · accepted  
  v0 takes prompts and Markdown files with an editable preparation step, and
  holds shortcut ingestion for later.

- **[ADR-019 — Review](decisions/ADR-019-review.md)** · accepted  
  Group changes by repository, compare against immutable recorded bases, and
  launch configurable Git and editor commands.

- **[ADR-020 — Resource policy](decisions/ADR-020-resource-policy.md)** · accepted  
  Show whole-machine availability and per-task totals, and enforce no
  concurrency limit in v0.

- **[ADR-021 — Cleanup](decisions/ADR-021-cleanup.md)** · accepted  
  Cleanup is conservative and explicit: volumes are retained by default and
  nothing is removed by age.

- **[ADR-022 — Distribution and telemetry](decisions/ADR-022-distribution-and-telemetry.md)** · accepted  
  Public v0 ships release binaries, Homebrew, and `go install` on macOS and
  Linux, with no telemetry.

- **[ADR-023 — Open-source boundary](decisions/ADR-023-open-source-boundary.md)** · accepted  
  The local core and the local or LAN web client are open source; a hosted
  relay, push, and later team control may be commercial.

- **[ADR-024 — Plugin strategy](decisions/ADR-024-plugin-strategy.md)** · accepted  
  Adapters are compiled-in Go implementations, behind interface boundaries that
  leave a future external plugin protocol possible.

- **[ADR-025 — Package layout additions and mechanical rule enforcement](decisions/ADR-025-package-layout-additions-and-mechanical-rule-enforcement.md)** · accepted  
  Adds `internal/paths`, `internal/cli`, `internal/version`, and
  `internal/guard`, and makes the architectural import rules depguard rules and
  AST tests rather than review attention.

- **[ADR-026 — Domain and storage modelling](decisions/ADR-026-domain-and-storage-modelling.md)** · accepted  
  Attention lives on the task alone, a task's shape is mutable only while it is
  a draft, and stored documents are a versioned representation separate from the
  domain types.

- **[ADR-027 — Daemon ownership, runtime file layout, and local API surface](decisions/ADR-027-daemon-ownership-runtime-file-layout-and-local-api-surface.md)** · accepted  
  Daemon liveness is an advisory lock and a connect probe in the runtime
  directory, the API addresses a task by identifier, and a subscriber that falls
  behind is disconnected rather than blocking the daemon.

- **[ADR-028 — Configuration loading, project registration, and the honesty of diagnostics](decisions/ADR-028-configuration-loading-project-registration-and-the-honesty.md)** · accepted  
  Configuration parses, resolves, then validates every problem at once;
  `internal/config` asks the host nothing, and a check this build cannot run
  reports `skipped` rather than passing.

- **[ADR-029 — Git adapter boundary, preparation order, and path safety](decisions/ADR-029-git-adapter-boundary-preparation-order-and-path-safety.md)** · accepted  
  `internal/git` receives final names only, preparation is plan, record, apply,
  a failure part way through is left in place, and every worktree path is
  validated before it is created or removed.

- **[ADR-030 — tmux identity, command ownership, and attach boundary](decisions/ADR-030-tmux-identity-command-ownership-and-attach-boundary.md)** · accepted  
  Feat runs its own tmux server on a dedicated socket, identifies managed
  objects by versioned `@feat_*` options rather than by name or index, and
  attaches natively from the client.

- **[ADR-031 — Task drafts, confirmation, and the first user-facing task lifecycle](decisions/ADR-031-task-drafts-confirmation-and-the-first-user-facing-task.md)** · accepted  
  A draft is a task in `draft` state; preparation becomes plan, confirm, apply,
  and a launch refuses anything whose fingerprint differs from what the user was
  shown.

- **[ADR-032 — Control workspace, agent boundary, and where Claude runs before slice 8](decisions/ADR-032-control-workspace-agent-boundary-and-where-claude-runs.md)** · accepted  
  The control workspace is a host-owned tree split into agent-written and host-
  written halves, delivery is polling, generated hooks only write raw payloads,
  and `FEAT_HOST_AGENT` is the daemon-side opt-in for a host agent.

- **[ADR-033 — Devcontainer execution, generated mounts, and what a container may not already hold](decisions/ADR-033-devcontainer-execution-generated-mounts-and-what-a-container.md)** · accepted  
  The generated override takes over each repository's container path, resets
  `container_name` and `ports`, mounts each Git directory at its host path, and
  the running container's mounts are inspected before the agent starts.

- **[ADR-034 — Application runtime identity, generated mounts, and what a manual lifecycle owns](decisions/ADR-034-application-runtime-identity-generated-mounts-and-what-a.md)** · accepted  
  `internal/runtime` is its own adapter with its own Compose plumbing, the
  runtime's project name is the user's, every action addresses the whole Compose
  project rather than the named services, and destroy retains volumes.

- **[ADR-035 — Resource observation, notification policy, and what a machine can honestly report](decisions/ADR-035-resource-observation-notification-policy-and-what-a-machine.md)** · accepted  
  Sampling is a background loop the API serves from cache, the machine figure is
  load average with the core count rather than a processor percentage,
  suppression asks tmux which window a client is watching, and nothing
  unmeasured is published.

- **[ADR-036 — Review comparisons, external commands, and where a completion gate can honestly interrupt an agent](decisions/ADR-036-review-comparisons-external-commands-and-where-a-completion.md)** · accepted  
  The completion gate is triggered by the explicit review request, run by the
  daemon rather than by the agent, and returned to the agent through the exit
  status of the report helper.

- **[ADR-037 — Quarantine, recovery, and what cleanup is allowed to resolve](decisions/ADR-037-quarantine-recovery-and-what-cleanup-is-allowed-to-resolve.md)** · accepted  
  An enumeration returns what it could read together with what it could not,
  reconciliation reports and never adopts, cleanup is seven independently
  confirmed classes, and volumes are removed by name rather than by `--volumes`.

- **[ADR-038 — Naming a task](decisions/ADR-038-naming-a-task.md)** · accepted  
  A task is named by its short key, its whole identifier, or a prefix of one,
  and ambiguity is reported with every candidate rather than resolved to one of
  them.

- **[ADR-039 — Proving a notification arrived](decisions/ADR-039-proving-a-notification-arrived.md)** · accepted  
  Every path that does not deliver a notification names the policy that stopped
  it, and every notifiable condition is walked against a real desktop by an opt-
  in test.

- **[ADR-040 — Where a command lives](decisions/ADR-040-where-a-command-lives.md)** · accepted  
  Every command that takes a task lives under `feat task`, which is the scoped
  shape ADR-001 chose.

- **[ADR-041 — What the dashboard is shaped like](decisions/ADR-041-what-the-dashboard-is-shaped-like.md)** · accepted  
  The dashboard becomes three regions that persist — a rail grouped by project,
  a tabbed main region, and a footer — with overlays in place of screens that
  replace everything.

- **[ADR-042 — Showing the agent's terminal without becoming one](decisions/ADR-042-showing-the-agents-terminal-without-becoming-one.md)** · accepted  
  The main region draws the selected task's agent pane from `capture-pane`,
  proxied by the daemon over one control-mode client; Feat implements no
  terminal and derives no state from those bytes.

- **[ADR-043 — Removing the overview table](decisions/ADR-043-removing-the-overview-table.md)** · accepted  
  The wide overview table goes, the tabs become terminal, task, and runtime, and
  reconciliation's findings move to the task panel and an overlay.

- **[ADR-044 — The machine's resources at the foot of the rail](decisions/ADR-044-the-machines-resources-at-the-foot-of-the-rail.md)** · accepted  
  The machine's figures move to the foot of the rail as fixed-column bars
  carrying a share and a percentage, and a share nothing measured draws no bar.

- **[ADR-045 — The status command loses its key](decisions/ADR-045-the-status-command-loses-its-key.md)** · accepted  
  `s` opens the task's shell on every view, the panel keeps diff and editor, and
  no external command Feat launches is wrapped in a pause.

- **[ADR-046 — Shifted keys move the frame, plain keys move the view](decisions/ADR-046-shifted-keys-move-the-frame-plain-keys-move-the-view.md)** · accepted  
  Shifted keys select a task and change view from anywhere; plain keys move
  within whatever the main region draws and never reach the frame.

- **[ADR-047 — One record of a review decision](decisions/ADR-047-one-record-of-a-review-decision.md)** · accepted  
  The task's workflow state is the only record of a review decision, and the
  review aggregate keeps only the comparisons, the agent's claim, and the check
  results.

- **[ADR-048 — Removing the external resource declaration](decisions/ADR-048-removing-the-external-resource-declaration.md)** · accepted  
  `runtime.external_resources` is removed and `FEAT_TASK_KEY` is the documented
  per-task discriminator; Feat models nothing about a resource it cannot reach.

- **[ADR-049 — Who owns the interrupt while another program has the terminal](decisions/ADR-049-who-owns-the-interrupt-while-another-program-has-the.md)** · accepted  
  The dashboard detaches from the process-wide interrupt context, and an exit
  produced by an interrupt is how a user leaves a program the dashboard ran
  rather than a failure to report.

- **[ADR-050 — The writable Git directory is host execution, and is disclosed rather than closed](decisions/ADR-050-the-writable-git-directory-is-host-execution-and-is.md)** · accepted  
  The read-write Git directory mount stays, and the devcontainer claim is
  narrowed to what is true rather than the mount being masked, closed, or
  proxied.

- **[ADR-051 — What the dashboard looks like](decisions/ADR-051-what-the-dashboard-looks-like.md)** · accepted  
  Six named colours chosen as a set, each region a card whose content is cut by
  cell rather than re-flowed, and a project header that folds because its marker
  had always promised it.

- **[ADR-052 — A folded project is a cursor position](decisions/ADR-052-a-folded-project-is-a-cursor-position.md)** · accepted  
  A folded project is one cursor stop, so `space` can open it again, and the
  fold's header names the task it is holding.

- **[ADR-053 — The palette is ordered by chroma, and measured](decisions/ADR-053-the-palette-is-ordered-by-chroma-and-measured.md)** · accepted  
  The palette is ranked by chroma — failure above attention, attention level
  with the accent — with every value measured in OKLCH and under simulated
  colour-vision deficiency.

- **[ADR-054 — Text Feat did not write is made measurable before it is measured](decisions/ADR-054-text-feat-did-not-write-is-made-measurable-before-it-is.md)** · accepted  
  Text the dashboard did not compose has its tabs expanded and its other C0
  controls dropped before it is measured or drawn.

- **[ADR-055 — A check that could not run belongs to the user, not to the agent](decisions/ADR-055-a-check-that-could-not-run-belongs-to-the-user-not-to-the.md)** · accepted  
  `review.Decide` returns passed, failed, or blocked; a blocked run leaves the
  task in `review_requested`, interrupts the user, and answers the waiting agent
  with a zero exit.

- **[ADR-056 — One stash stack is shared by every worktree, and Feat says so rather than working around it](decisions/ADR-056-one-stash-stack-is-shared-by-every-worktree-and-feat-says-so.md)** · accepted  
  The session is told that the stash is one stack per repository,
  `rebase.autoStash` and `merge.autoStash` are turned off through the
  environment, and the per-task clone is named and not taken.

- **[ADR-057 — The agent's environment is owned by its session, and the pair of verbs is resume and stop](decisions/ADR-057-the-agents-environment-is-owned-by-its-session-and-the-pair.md)** · accepted  
  The agent environment has no `create` and no `start`: it lives with its
  session, `feat task resume` and `feat task stop` are its surface, and an agent
  cannot be alive while its environment is not running.

- **[ADR-058 — Attention is set by the end of a turn, not by a message the agent wrote mid-turn](decisions/ADR-058-attention-is-set-by-the-end-of-a-turn-not-by-a-message-the.md)** · accepted  
  A review request sets no attention; the conservative state is set where every
  other one is, when the turn ends and the idle grace expires.

- **[ADR-059 — A task's containers are addressable by name, and a mounted directory is released before it is removed](decisions/ADR-059-a-tasks-containers-are-addressable-by-name-and-a-mounted.md)** · accepted  
  A task's agent Compose project is derived from the two identifiers rather than
  read from a record, and the control workspace is removed only after
  establishing that no container of that project still holds it.

- **[ADR-060 — A failed task carries the reason it failed](decisions/ADR-060-a-failed-task-carries-the-reason-it-failed.md)** · accepted  
  `FailWith` is the only way into `failed`, the reason is stored verbatim beside
  the state, and leaving `failed` discards it.

- **[ADR-061 — The confirmation belongs to the removal, not to each tick that led to it](decisions/ADR-061-the-confirmation-belongs-to-the-removal-not-to-each-tick.md)** · accepted  
  The cleanup dialog asks once, at the removal, naming every warning of
  everything selected; enter re-resolves before it asks, and there is no re-
  resolve key.

- **[ADR-062 — A project is configured by answering questions, and the answers are checked before there is a file](decisions/ADR-062-a-project-is-configured-by-answering-questions-and-the.md)** · accepted  
  `feat project init` is a conversation whose answers render one file, which is
  parsed, validated, and displayed before anything is written.

- **[ADR-063 — One flow, two askers: the wizard's questions are a package, and the dashboard asks them itself](decisions/ADR-063-one-flow-two-askers-the-wizards-questions-are-a-package-and.md)** · accepted  
  `internal/wizard` owns the questions, their proposals, and their validation,
  and both the command and the dashboard only ask them.

- **[ADR-064 — Diagnosis is read on the dashboard, and it says which process it is true of](decisions/ADR-064-diagnosis-is-read-on-the-dashboard-and-it-says-which-process.md)** · accepted  
  The dashboard runs `feat doctor`'s own checks in the process the user is in
  front of, says where they ran, and never runs them on a timer.

- **[ADR-065 — A runtime is composed of its repositories, and a service that is not running the task's code says so](decisions/ADR-065-a-runtime-is-composed-of-its-repositories-and-a-service-that.md)** · accepted  
  Each repository brings its own Compose files and container path, Feat
  generates the `include` that joins them, redirects build contexts as well as
  mounts, allocates a host port per reachable service, and says which services
  are not running the task's code.

- **[ADR-066 — What a container grants and no rule refuses is said out loud](decisions/ADR-066-what-a-container-grants-and-no-rule-refuses-is-said-out-loud.md)** · accepted  
  A path back to root inside the agent's container is probed, reported at every
  launch and by `feat doctor`, and never refused.

- **[ADR-067 — The confinement the other rules stand on is checked, and a policy Feat cannot read is disclosed](decisions/ADR-067-the-confinement-the-other-rules-stand-on-is-checked-and-a.md)** · accepted  
  The three `unconfined` forms are refused, `systempaths` is found by its effect
  and named by its cause, and a custom profile is reported as a policy Feat did
  not evaluate rather than printed.

- **[ADR-068 — A daemon that goes away is offered, once, and never restarted behind the user's back](decisions/ADR-068-a-daemon-that-goes-away-is-offered-once-and-never-restarted.md)** · accepted  
  The dashboard asks before starting a replacement daemon, asks once per outage,
  and leaves `S` in the footer as the way back.

- **[ADR-069 — The daemon log is bounded, and the pollers stop repeating themselves](decisions/ADR-069-the-daemon-log-is-bounded-and-the-pollers-stop-repeating.md)** · accepted  
  The daemon log rotates in place at a fixed size over a fixed number of
  generations, and the three pollers report a failure when it appears or changes
  rather than on every tick.

- **[ADR-070 — Provider work happens on the trusted host, and the agent writes the words rather than sending them](decisions/ADR-070-provider-work-happens-on-the-trusted-host-and-the-agent.md)** · accepted  
  Every credentialed provider call is made by the daemon on the trusted host;
  the agent writes a publication draft, the user reads and edits it, and what
  was displayed is what is sent.

- **[ADR-071 — Where the code goes and where the tickets come from are different questions](decisions/ADR-071-where-the-code-goes-and-where-the-tickets-come-from-are.md)** · accepted  
  The forge is configured per repository and the tracker per project; a tracker
  is a user command printing JSON that conforms to a schema Feat publishes, and
  there is no second adapter.

- **[ADR-072 — Publication is scheduled before the public preview, because a dogfood task cannot finish without it](decisions/ADR-072-publication-is-scheduled-before-the-public-preview-because-a.md)** · accepted  
  Publication is built first and ticket ingestion second, both before the public
  preview, and the roadmap phases keep their numbers.

- **[ADR-073 — A publication is applied one repository at a time, and a partial one is recorded rather than undone](decisions/ADR-073-a-publication-is-applied-one-repository-at-a-time-and-a.md)** · accepted  
  A publication plans every repository, records the plan, applies one repository
  at a time, and records a partial result rather than undoing it.

- **[ADR-074 — What publication left to the implementation](decisions/ADR-074-what-publication-left-to-the-implementation.md)** · accepted  
  The draft is read back out of the control workspace when the user publishes,
  the apply carries the approved words verbatim, staleness refuses before the
  plan is recorded, and `glab`'s flags are pinned against the installed version.

- **[ADR-075 — The provider-CLI capabilities are removed, because the host holds the credential](decisions/ADR-075-the-provider-cli-capabilities-are-removed-because-the-host.md)** · accepted  
  `agent.capabilities.github_cli` and `gitlab_cli` are removed, and the host
  publication check becomes the only provider-CLI finding.

- **[ADR-076 — The publication draft is read on the screen that sends it](decisions/ADR-076-the-publication-draft-is-read-on-the-screen-that-sends-it.md)** · accepted  
  The publication screen becomes a dialog drawing every word that would be sent,
  and `enter` is refused until all of it has been on the screen or has come back
  from the editor.

- **[ADR-077 — A proposal is a value the user can take into the field and edit, and what the flow derived beside it is offered rather than only named](decisions/ADR-077-a-proposal-is-a-value-the-user-can-take-into-the-field-and.md)** · accepted  
  `Question` gains `Candidates` with the proposal at their head, Tab moves a
  value into the field and never past it, and a question's own notes are
  appended to the flow's rather than assigned over them.

- **[ADR-078 — The wizard stops asking about verification, and `checks:` stays exactly as it is](decisions/ADR-078-the-wizard-stops-asking-about-verification-and-checks-stays.md)** · accepted  
  The three verification questions leave the wizard and everything else about
  `checks:` stays; this removes a prompt rather than a feature.

- **[ADR-079 — What is true of the machine is configured once, in a settings file with no per-project override](decisions/ADR-079-what-is-true-of-the-machine-is-configured-once-in-a-settings.md)** · accepted  
  `review`, `notifications`, and `resources` move to
  `~/.config/feat/settings.yaml`, global with no per-project override, resolved
  once when the daemon starts while every command reads the file as it is now.

- **[ADR-080 — `network` and `git` leave `agent.capabilities`, because a capability is a claim Feat checks](decisions/ADR-080-network-and-git-leave-agent-capabilities-because-a.md)** · accepted  
  The two capability fields that gated nothing and could hold one value are
  removed; `docker` stays, because a launch refuses a container against it.

- **[ADR-081 — The agent's Compose files are read the way a repository's are, and a mount a worktree cannot hold is reported before a task runs](decisions/ADR-081-the-agents-compose-files-are-read-the-way-a-repositorys-are.md)** · accepted  
  The structural Compose reader takes the project directory and the repository
  as separate parameters, the wizard proposes the mount the agent's own files
  state, and `feat doctor` asks Git whether each bound path is tracked.

- **[ADR-082 — The container-path detail repeats on every mount question, because this is the field whose wrong answer says nothing](decisions/ADR-082-the-container-path-detail-repeats-on-every-mount-question.md)** · accepted  
  Both mount groups carry the warning on every question rather than only on the
  first, and a proposal read out of a Compose file names the file it came from.

- **[ADR-083 — Where a task's brief comes from is a question, asked once and after the project](decisions/ADR-083-where-a-tasks-brief-comes-from-is-a-question-asked-once-and.md)** · accepted  
  Preparation gains a source step between the project and the brief; choosing a
  source resets the brief and discards the draft, and `--file` and `--ticket`
  are answers to it.

- **[ADR-084 — `feat project init` asks with the dashboard's widget, inline, and what it leaves behind is the transcript](decisions/ADR-084-feat-project-init-asks-with-the-dashboards-widget-inline-and.md)** · accepted  
  `feat project init` draws each question with the dashboard's own widget as an
  inline program, `esc` steps back by growing the transcript, and the line
  conversation survives for a terminal that cannot draw.

- **[ADR-085 — Plan mode travels with the initial prompt, and the plan is approved in the terminal](decisions/ADR-085-plan-mode-travels-with-the-initial-prompt-and-the-plan-is.md)** · accepted  
  Plan-first is a per-launch flag carried on the confirmation and recorded on
  the task, applied to a launch and never to a resume, with the plan approved in
  the terminal.

- **[ADR-086 — The task panel keeps only what nothing else on the screen says](decisions/ADR-086-the-task-panel-keeps-only-what-nothing-else-on-the-screen.md)** · accepted  
  The brief takes a tab of its own, the panel drops what the rail and the tabs
  already carry, the tmux block goes, and `approved` and `changes_requested`
  leave the domain.

- **[ADR-087 — The checks run again on work that passed them](decisions/ADR-087-the-checks-run-again-on-work-that-passed-them.md)** · accepted  
  `ready_for_review` gains one edge back to `review_requested`, so the gate can
  run again on work that passed it, and a run with nothing to run is refused
  before anything moves.

- **[ADR-088 — The question widget is told how wide it may draw, and the flow's line breaks become its default rather than its limit](decisions/ADR-088-the-question-widget-is-told-how-wide-it-may-draw-and-the.md)** · accepted  
  `SetContextWidth` is separate from the field's width: a caller with a width
  refolds the flow's paragraphs, and a caller without one draws the lines it was
  given.

- **[ADR-089 — An ADR is a file, and `docs/10` becomes the index that finds it](decisions/ADR-089-an-adr-is-a-file-and-docs-10-becomes-the-index-that-finds-it.md)** · accepted  
  Each decision becomes a file under `docs/decisions/`, this document becomes
  the index that finds it, and a test in `internal/guard` holds the two
  together.

- **[ADR-090 — The v0.1.0 release](decisions/ADR-090-the-v0-1-0-release.md)** · accepted  
  The tag packages the finished dogfood scope for its author and widens
  nothing: release binaries, `go install`, and the setup skill move into it,
  Linux moves out of it, and host-native execution is recorded as delivered.

## Open questions

These are recorded so that they are not answered in passing. An open question is
not permission to choose a permanent design early.

### OQ-001 — Natural-language orchestrator

Should mature natural-language commands be interpreted by a constrained master native agent or by a host-integrated model? Do not decide during v0.

### OQ-002 — Remote interaction surface

Should the remote client primarily stream the real tmux terminal or present a simplified prompt/response surface? Terminal streaming is the working hypothesis and needs user validation.

### OQ-003 — Automatic runtime rules

What lifecycle rule language is sufficient for task start, review request, approval, and cleanup? Are task-level overrides necessary?

### OQ-004 — Cleanup retention

Which generated volumes can safely become ephemeral after real-project evidence? Initial answer: none automatically.

### OQ-005 — Strong isolation

Which microVM/hardened runtime provides useful hostile-code isolation without excessive resource cost or Git friction?

### OQ-006 — Task-local Git metadata

Can Feat provide full useful Git behavior without exposing shared worktree metadata, and is the complexity justified?

### OQ-007 — Claude configuration isolation

When should the shared dedicated Claude profile become per-task while preserving one interactive login?

### OQ-008 — Stable hostname implementation

Which local proxy and name-resolution approach works consistently across macOS and Linux with minimal privilege?

**Open, and half built.** Feat now allocates a host port per reachable service
per task and tells every managed service the address (ADR-065), which is what
such a proxy would have to route to. It is not a substitute for the feature: an
allocated port changes with the task, and the point of a stable hostname is a URL
that does not. What still blocks it is what was recorded against it before — a
proxy is a machine-wide resource with a lifetime no task owns, which is the
`shared` lifecycle ADR-034 called roadmap work, and a label-driven one wants the
Docker socket this product's headline denies the agent.

### OQ-009 — Plugin protocol

What external adapter protocol is justified after internal interfaces have stabilized? Do not define it speculatively.

**Open, with one candidate declined for one use.** MCP is the first concrete
answer this question has had, and ADR-071 declines it as the tracker's mechanism:
it carries transport and discovery rather than a shared ticket vocabulary, so it
moves a per-provider mapping behind a protocol instead of removing it, while
adding a server process and session state to the only writer. That is an argument
about one use rather than about the protocol, and the same ADR records where MCP
would be the right implementation — an adapter somebody schedules, where a
maintained mapping is what it buys. The question stays open.

### OQ-010 — Mobile product scope

Which remote actions users actually perform on a phone remains a product discovery question. Do not build native mobile apps before PWA usage evidence.

### OQ-011 — External/shared database automation

The dogfood project uses pre-existing staging databases. Assignment, migration, seed, and cleanup conventions need project evidence before generalization. **Resolved: the evidence arrived and says not to generalize. The declaration Feat could not verify is removed and the per-task discriminator stays, see ADR-048.**

### OQ-012 — Per-process processor resolution on Linux

`ps` reports cumulative processor time in whole seconds on Linux and in
centiseconds on macOS, so a per-task processor figure differenced over a
two-second interval resolves to about one point on macOS and to fifty on Linux
(ADR-035 evidence 16). Reading `/proc/<pid>/stat` on Linux would close the gap at
the cost of a platform-specific process reader beside the platform-specific
machine readers that already exist.

Whether that is worth doing depends on evidence this version cannot supply: how
often a task's processor figure is the one a user acts on, rather than the memory
figure or the attention badge beside it. Decide it against dogfood use, not
before. Until then the dashboard shows what was measured and the figure is coarse
rather than wrong, which is the rule ADR-028 set.

### OQ-013 — What the explicit review request earns

The agent protocol is a generated system-prompt section, a generated helper
script, an outbox, an inbox, message validation, a poller, and a helper that
blocks on a verdict with its own timeouts and recovery. ADR-032 and ADR-036 built
it on the rule that an end of turn means idle and never done, and that semantic
completion needs an explicit act by the agent.

The maintainer's objection, raised while dogfooding slice 13: a user mostly wants
to know that an agent went idle, and to review a task that changed something.
Both are observable without the agent's cooperation — Feat already records a
`GitObservation` per repository — and the explicit message may be paying for
itself only in the projects that configure a completion gate.

Two things are already clear and neither settles it. Idle plus changed files is
not a substitute for the message: an agent writes files within minutes and stays
dirty for the rest of the task, so the condition would be true at every turn
boundary and would say nothing the idle notification does not. And the gate does
need an explicit trigger, because a project's test suite cannot run at every
pause — which makes the gate, rather than the notification, what the protocol is
actually for.

That points at a narrower question than "keep or remove": whether the protocol
should be generated at all for a project that configures no checks. There, the
whole apparatus produces a workflow state, one notification, and the agent's own
summary in the review record — and the agent's summary is the only part nothing
else supplies.

ADR-078 supplies the other half of the evidence, and could not have before it.
The wizard no longer asks about checks, so a project that configures them is one
somebody opened the file for — which turns "how many projects configure a gate"
from a measurement of the wizard's defaults into a measurement of what people
want. That is the population this question's narrow form is about.

The evidence to decide on is the measurement slice 13 already owes: how often the
agent requested review unprompted, how often the user had to ask for it, how
often an idle notification was already the moment they would have reviewed, and
whether a gate ever caught something that would otherwise have been reviewed
broken. Three real tasks answer all four. Do not decide before them, and do not
decide the narrow question by removing the general one.

### OQ-014 — Whether a linked worktree is the right per-task boundary

ADR-029 gives a task a linked worktree per repository, and ADR-056 records what
that shares with the user's checkout and with every other task on the same
repository: one stash stack, one configuration, one set of hooks, one object
store, and one branch namespace. Git protects the branch namespace itself, and
ADR-056 closes the autostash route onto the stash; the rest is unguarded, and a
read-write mount of the user's Git directory carries all of it into every task
container.

The alternative is a per-task clone — hardlinked, or sharing objects through
`alternates` — which isolates refs, configuration, hooks, and the stash for
roughly a worktree's cost, and which would let that mount become task-scoped.
Against it: branch visibility in the user's repository, the review comparison,
and the cleanup plans of ADR-029 all assume a linked worktree, and `--shared`
adds a pruning hazard the worktree does not have.

The evidence to decide on is dogfood, and it is narrow: how often two tasks run
against one repository at the same time, whether an agent ever reaches shared
state a worktree does not isolate, and what a task branch being invisible in the
user's own checkout would cost the review workflow in practice. Do not decide
before that; a boundary changed on reasoning alone would move ADR-029, ADR-032,
and the cleanup contract at once.

### OQ-015 — Whether a Git hook can be attributed to the user rather than to the agent

ADR-070 disables hooks for the host-side push, because the agent can write
`.git/hooks` (ADR-050) and approving a publication should not be how a user runs
whatever is in `pre-push`. The dogfood machine has no such hook, so the decision
is unexposed there, and the ADR discloses what it costs elsewhere rather than
resolving it: where a `pre-push` hook is somebody's check rather than its user's
own convenience, Feat's publication is the one path that skips it.

The question is whether the two can be told apart. A hook Feat could attribute to
somebody other than the agent could be run — one under a `core.hooksPath` outside
every worktree the agent can reach, or one whose content still matches what it
was when the task launched, which is a digest recorded beside the base commit
Feat already records. Against both: `core.hooksPath` is repository configuration
the agent can also write, so the path would have to be read from somewhere the
agent cannot reach before it could be trusted; a digest taken at launch checks
against modification rather than against a hook that was already hostile; and
either one puts a trust decision inside a step whose whole value is that the user
is reading the words before they are sent.

The evidence to decide on is a user who depends on a `pre-push` hook they did not
write, and the dogfood cannot supply one. Do not decide before the public preview
has such a user; a mechanism built now would be fitted to a hook nobody has.

## Decision change process

During implementation:

1. Record new evidence.
2. Identify affected requirements and milestones.
3. Add or amend an ADR.
4. Update linked specification files in the same change.
5. Do not silently let implementation behavior become the specification.
