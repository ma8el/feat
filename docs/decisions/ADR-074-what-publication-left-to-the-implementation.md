# ADR-074 — What publication left to the implementation

Status: accepted
Recorded: 2026-08-24, from building ADR-070 and ADR-073

ADR-070 says the agent writes a publication draft and the user reads and edits it
before anything is sent. ADR-073 says a publication plans every repository,
records the plan, and applies one at a time. Four questions were left between
them, and each was answered by something already in the codebase rather than by a
new mechanism.

Evidence:

1. There is nowhere in the domain to keep the draft's prose, and there should not
   be. `Publication` holds the plan and the result — the forge, the remote, the
   base branch, the commit, and the merge request — and deliberately not the
   words: the words are what the user rewrites, and a stored copy would be a
   second answer to what was sent. The one place they already live durably is the
   outbox, which "stays an audit trail until slice 12 cleans it up" (ADR-032) and
   is the account of what the agent sent.
2. `Workspace.Pending` never returns a message twice, which is what stops one
   message being applied twice, so reading a draft back is a different question
   from delivering it.
3. The review command vocabulary has no placeholder for "the document to open".
   Every placeholder `internal/config` expands names something about a repository
   (`internal/config/template.go`), and a draft is not one.
4. There is no `glab` on the machine this was built on, and
   [06-technical-architecture.md](06-technical-architecture.md) requires a
   provider CLI's flags to be verified against the installed version rather than
   assumed.

Decisions:

- The draft is read back out of the control workspace when the user asks to
  publish, through the provider adapter that parsed it when it arrived.
  `internal/control` gains `Workspace.Latest`, which reads the newest message of
  one type whether or not it was applied and settles nothing. Two callers, one
  parser: a message the poller applied to the task's history is the same message
  a publication composes from, hours later and across a restart.
- The publication endpoint is plan and apply, as cleanup's is, and the apply
  carries the words verbatim. What is displayed is what is sent, so the daemon
  composes each request from the approved text rather than from the agent's
  message — reviewing one document and sending another would make the approval a
  formality (ADR-070).
- The configured editor command keeps its own flags and is given the draft to
  open. The daemon returns the program and the arguments that are not the
  repository placeholder, and the client appends the document it wrote. `code -w`
  stays `code -w`, which matters: an editor that returns immediately is a draft
  approved unread. Expanding `{repository_path}` to a draft file was rejected —
  the vocabulary is closed and that placeholder means a worktree.
- Staleness is checked twice against the same fact and refuses the whole request
  before the plan is recorded. The approved commit must still be the repository's
  head, and the agent's draft must describe that head where it drafted one at
  all. A repository that has already published is exempt from both, because
  re-publishing skips it as already published and asking whether its words are
  current would turn that skip into a refusal — which is the confusion ADR-073
  keeps the two reasons apart to avoid.
- `internal/forge` gains a `forge-stays-an-adapter` `depguard` rule, mirroring
  `agent-stays-an-adapter`. ADR-025 requires an ADR for a boundary rule, and this
  is that record. It denies `internal/git` as well: pushing is Git's, and a forge
  adapter never runs `git`.
- `domain.EventPublicationChanged` is added, which is the one domain shape this
  work needed. Every other lifecycle records what it did in the task's history,
  and a publication — which creates something on somebody else's server, one
  repository at a time, and does not roll back — is the one that most needs an
  account of a run that stopped half way. It carries no from and no to: a task
  has no publication state of its own to move between.
- `glab`'s flags are verified by an opt-in test that reads `glab mr create
  --help` wherever glab is installed, and `internal/integrationtest` gains `glab`
  as a demandable tool. It is demanded by nothing by default, like `claude` and
  `notify`: the check needs an installed glab and nothing else — no account, no
  network, no project — so a maintainer with one can make it mandatory and nobody
  is made to install it.

  It has now run, against glab 1.114.0, which `gitlab.Verified` records. The five
  flags are accepted, the project is resolved from the repository the command
  runs in, and a title and description supplied together with `--yes` reach the
  API without a prompt or an editor. Two things the flag list could not have
  shown came out of it:

  - glab writes a recovery file under the user's own configuration directory
    when a creation fails, and its documented behaviour is to load the options
    back out of that file when `--recover` is given. Feat must never take that
    path — what is sent has to be the words the user just read, not the ones a
    previous attempt left on disk — so the flag is never passed. A recovery file
    whose contents had been replaced with nonsense was ignored and overwritten by
    a run that did not pass it, so nothing loads it by default.
  - a description of exactly `-` is glab's own shorthand for "open an editor",
    and a daemon has no terminal to open one on. The installed version sent it
    through as text, which is the behaviour that would hang if a later one
    honoured its own documentation. The description is the one field where a
    leading hyphen is ordinary prose — a Markdown list — so it cannot be refused
    by the rule that keeps an option out of an argument vector, and the adapter
    refuses that one value by name instead. It is refused rather than altered: a
    description Feat quietly changed would be worse than one it declined to send.

- `feat doctor` asks the host whether it can publish at all, once per forge the
  project's repositories declare. ADR-070's consequence named two additions to
  the diagnostics and this is a third, found by a user reading the first one and
  asking why a forge needed a hook to be noticed. Nothing else asks:
  `agent.capabilities.github_cli` and `gitlab_cli` describe the *agent's*
  environment, are probed inside the container on a devcontainer project, and are
  what ADR-070 expects to be `disabled` — so on the configuration that decision
  recommends, no check touched the machine that actually runs `glab`. A project
  could pass every check and then push a branch and fail to open the merge
  request. It warns rather than fails, because a project may be configured before
  anybody has logged in, and it says nothing for a project that declares no
  forge.
- A forge the configuration accepts and this build has no adapter for is reported
  there too, rather than found out after a branch has been pushed.
  `forge.Built` declares which forges are built and a guard test holds it and the
  daemon's registry together, so the two cannot drift into doctor reporting a
  project as publishable that a publication then refuses. Both of Phase 3's
  forges are built now — the GitHub adapter followed within the same change,
  because Feat's own repository is on GitHub and the GitLab-only build could not
  publish its own development — so the refusal is currently the guard for the
  next forge rather than a state a user can configure.
- The pre-push report is `repositories.<id>.publication`. It was
  `repositories.<id>.forge` — the configuration field that decides whether it
  runs — which reads as a check on the forge declaration. That declaration is
  validated when the configuration loads; this is about what a push will skip,
  and a check name that names the wrong thing is a check that gets asked the
  wrong question.

What this does not decide is whether Feat should run a `pre-push` hook it can
attribute to the user rather than to the agent. That is OQ-015, and it still
needs somebody who has one.

Consequence: `internal/control` gains `TypePublicationDraft`, the payload it
validates, and `Workspace.Latest`; `internal/agent` gains `KindPublicationDraft`
and the draft on its event; `internal/agent/claude` instructs the agent and
parses what it wrote; `internal/git` gains `Push`, `PushEnvironment`, and
`SuppressedHooks`; `internal/forge` and `internal/forge/gitlab` are new;
`internal/review` gains `NewPublication` and the result a publication records;
`internal/daemon` owns the sequencing; `internal/api`, `internal/client`,
`internal/cli`, and `internal/ui` carry the surface. `feat doctor` gains the
`pre-push` report, the host-native capability note ADR-070 asked for, and the
host publication check above.
[06-technical-architecture.md](06-technical-architecture.md) gains the two
packages, the two routes, and the adapter's new obligation; [README.md](README.md)
gains the command.
