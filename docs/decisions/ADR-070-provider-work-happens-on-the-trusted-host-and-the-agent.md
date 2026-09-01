# ADR-070 — Provider work happens on the trusted host, and the agent writes the words rather than sending them

Status: accepted
Recorded: 2026-08-23, from the provider feature set the dogfood needs

The question this started as was how an agent could use `gh` or `glab` without
holding the credential. v0.1 does not answer it. `agent.capabilities.github_cli`
and `gitlab_cli` are `disabled`, `optional`, or `required`, and the only thing
the binary does with either is run `gh auth status` inside the agent environment
at launch (`internal/agent/claude/validate.go:44`). Where the credential comes
from is left to the project, and `docs/05-security-model.md` records the exposure
plainly: an agent with provider credentials can mutate remote repositories within
token scope.

A least-privilege token bounds that scope. It does not answer the question,
because scope and exposure come apart: general outbound internet is allowed in
the accepted configuration and Feat claims no data-loss prevention, so a token
inside the container is a durable secret reachable by any prompt injection —
including one arriving in the issue body `gh` just fetched.

Evidence, from the operations the dogfood actually needs:

1. There are four: fetch the tickets assigned to a user, turn one into a task,
   push a task's branch, and open a merge request. Every one of them happens
   either before a task exists or after the agent has stopped working. Not one is
   a step inside an agent's turn.
2. The approval the user wants before work reaches an agent is already an
   invariant. `PrepareTask` refuses a task with no brief before it makes a
   network call (`internal/daemon/prepare.go:85`), `SetBrief` accepts edits only
   while the task is a draft (`internal/domain/task.go:189`), confirmation
   carries a fingerprint that refuses a draft which changed after it was
   displayed (ADR-031), and `WriteBrief` is called at launch, so the control
   workspace holds the brief only once it was confirmed
   (`internal/control/workspace.go:220`).
3. The one part of the work that does need the agent is the merge request's title
   and description, because the agent is what knows what it did. That is text,
   and text is not an action.

Decision: every credentialed provider call is made by the daemon on the trusted
host, using the authentication the user already has there. The agent environment
receives no provider token, and `agent.capabilities.github_cli` and `gitlab_cli`
stay `disabled` for a project that needs nothing beyond this.

That containment argument is about the devcontainer, and it does not survive
host-native execution. An agent launched by `FEAT_HOST_AGENT` (ADR-032) runs as
the user and inherits the user's environment, so it reaches whatever `gh` or
`glab` authentication the user has, and can call the provider's API directly
besides. There is no inside for a token to be kept out of, and `disabled` becomes
a declaration Feat cannot make true rather than a mount it declines to make. Feat
never enforced it in either mode — a capability is probed only where a project
marks it `required` (`internal/agent/claude/validate.go:44`) — but in a
devcontainer the declaration has an effect anyway, because a credential the
project does not mount is not there.

Publication stays host-side in both modes regardless, for reasons that are not
containment. Host-native and devcontainer execution share one task domain and
selecting a mode must not change task semantics
(`internal/execution/doc.go`), and ADR-073's record — a plan, a result per
repository, a re-publication that skips what already published — exists only
where Feat is the one publishing. An agent running `glab` itself would put that
record back to being discovered rather than known, which is the capability Phase
3 dropped when this decision was taken. Keeping one path costs almost nothing,
because the host-side path has to exist for the devcontainer anyway; two paths
would cost a second set of failure modes and a second answer to what a task
published.

What the execution mode changes is therefore what the approval step is, not
whether it happens. In a devcontainer it is a control: the agent cannot publish,
so the user reading the draft is how anything reaches a forge at all. On the host
it is a product behaviour: the agent could publish on its own and nothing here
prevents it, so what the approval buys is that Feat's own publication is one the
user read. Which of the two a user has is worth saying out loud, and claiming the
first in both modes would be exactly the uniform security property ADR-066 and
ADR-067 exist to refuse.

Where the agent's knowledge is needed, it is carried as data rather than as an
action. The agent writes a publication draft — a title and a body per repository
— into the control workspace when it requests review, which is when it still
knows what it did and is still running. The draft requires no capability, because
it asks for nothing; it is weaker than a `runtime_requested`, which at least
asks. The user reads it, edits it through the configured editor command, and the
host composes the final request from the agent's prose together with what Feat
already knows: the base branch, the task, and the ticket the task came from.

The draft is its own control-message type rather than a field on the review
request. The protocol already separates the act from the account:
`review_requested` is the only way a task reaches that state, and
`completion_report` is the agent's account of what it did
(`internal/control/message.go:38`). A draft is an account, so a third type
follows that grain rather than cutting across it. Both types already require no
capability, so the separation costs nothing to make; a project with no forge
configured never sees the type, where a field would sit unused in every generated
protocol document; and correcting a description does not have to re-enter a
workflow state to do it. What the separation costs is that two messages can
drift, which is why the draft carries the commit it describes.

This is not a weaker version of letting the agent publish. The description is
agent-authored text bound for somewhere durable, and it can carry anything the
agent read — a value out of a configuration file, text injected through a
dependency or an issue body. A user who reads it before it is sent is the only
control that exists; an agent that publishes on its own has none. The same
argument runs inbound: a brief composed from a ticket is written by whoever filed
the ticket and becomes the agent's instructions, so what the confirmation step
displays is the composed brief rather than the ticket it came from. Reviewing one
document and sending another would make the approval a formality.

A host-side push runs Git in a repository whose configuration and hooks the agent
can write, which ADR-050 records as an accepted exposure. It does not widen it —
Feat already runs `git fetch` and `git worktree add` there — but a user-approved
publication should not be the thing that fires an agent-authored `pre-push`, so
the push runs with hooks and the external pager and diff commands disabled.

Disabling hooks costs nothing on the dogfood machine, whose repositories define
no `pre-push` hook and set no `core.hooksPath`. It does not cost nothing
everywhere. A `pre-push` hook is not always its user's own convenience: it may be
what scans for secrets before anything leaves the machine, or what refuses a
protected branch. Where it is, Feat's publication is the one route out that skips
the check, which a user who never chose that has no way to learn. The decision
stands, because what it closes is host code execution on approval; what changes
is that it stops being silent. Where a repository carries a `pre-push` hook or a
configured `core.hooksPath`, the approval step names the hook it is not running,
so a user who depends on one can push by hand rather than find out afterwards
that a check was skipped on their behalf. Whether Feat should go further and run
a hook it can attribute to the user rather than to the agent is OQ-015, which
needs somebody who has one.

`feat doctor` reports the same fact at configuration time, for the reason ADR-071
validates a tracker's output there: what Feat will not run is better learned when
the user asks whether the project is configured than at the moment they are
approving a publication. It warns rather than fails, because Feat cannot tell a
load-bearing hook from a personal convenience — that is exactly what OQ-015
leaves open — and it says nothing where there is no hook, because a check with
nothing to report reports nothing (ADR-028).

What this does not decide is the agent-facing broker. An agent that asks for a
provider operation mid-turn — a schema-valid message, validated, capability-gated,
and inert until the user approves it — remains the shape for that case. It is not
built, because nothing in this feature set needs it.

Consequence: `internal/forge` gains the provider-neutral interface and
`internal/forge/gitlab` and `internal/forge/github` the adapters, mirroring
`internal/agent` and `internal/agent/claude` so that a forge's own flags and
output parsing stay inside its adapter. Pushing stays Git's: `internal/git` gains
`Push`, and the hook and pager suppression joins the `GIT_CONFIG_COUNT` settings
already there (`internal/git/environment.go:45`) rather than being rebuilt in a
forge adapter, which never runs `git`. `internal/review` gains publication
alongside its viewer commands, and the two differ in that a publication has a
result to record; `internal/forge` records none of it, because the daemon is the
only writer (ADR-008). The task gains the merge request it opened and the commit
its draft described, so that a draft written before further work is refused
rather than published, as ADR-031 refuses a stale plan. `feat doctor` gains the
`pre-push` report described above, beside the per-repository checks in
`internal/project/checks.go`, and reports under a host-native daemon that
capability levels describe intent rather than enforcement there. The GitHub and GitLab section of
`docs/05-security-model.md` gains
the host-side mode and the reason a token in the container is a different question
from a token's scope. Phase 3 of `docs/09-roadmap.md` keeps its GitLab-first order.
