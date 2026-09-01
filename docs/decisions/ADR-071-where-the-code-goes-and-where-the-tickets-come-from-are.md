# ADR-071 — Where the code goes and where the tickets come from are different questions

Status: accepted
Recorded: 2026-08-23, from a dogfood project whose repositories are on GitLab and whose tickets are in Shortcut

`gh` and `glab` each speak to a forge and to an issue tracker, so a single
provider setting appears to cover both. The dogfood machine shows it does not:
its code and merge requests live on a self-hosted GitLab and its tickets live in
Shortcut. A project needs both, and neither implies the other.

The configuration made this separation once already, for the same reason.
`RepositoryAgent` and `RepositoryRuntime` are separate types because the two
answer different questions with different owners, and one field could not say
both (`internal/config/config.go:99`, ADR-065).

Evidence:

1. Forge and tracker coincide only where a forge hosts its own issues. Shortcut,
   Jira, and Linear host no code; a self-hosted GitLab hosts code for a team
   whose planning is somewhere else. One setting makes that case unconfigurable.
2. Trackers do not agree on where a ticket lives. GitHub and GitLab attach issues
   to a repository; Shortcut, Jira, and Linear attach them to a workspace or
   team; GitHub Projects attaches them to an organisation-level board that spans
   repositories and carries its own iteration field. There is no level at which
   "this is where tickets are" holds generally.
3. The forge belongs to the repository and the ticket belongs to the task. A
   repository lives on exactly one forge and publication is one merge request per
   changed repository; a task spans repositories (ADR-004), so a ticket that
   seeds one belongs to none of them in particular. A project holding a private
   repository and a public dependency is not exotic.
4. Tickets are often filed where no code lives. A planning repository holds
   issues describing work done in other repositories entirely, and it is not a
   `Repository` in Feat's sense: that type means a checkout tasks get worktrees
   of, carrying a host path, a default branch, a remote, a default access level,
   an agent mount, and optionally a runtime contribution. A per-repository
   tracker would force a planning repository to be registered as one in order to
   be read, which would make it a candidate for a worktree, a branch, and a
   Compose project.
5. The mechanism does not transfer. `gh` and `glab` hold their own credentials
   and Feat never handles a token; it runs a command that is already
   authenticated. Shortcut publishes no first-party general-purpose CLI — client
   libraries and community tools — so an adapter written in Go would have Feat
   holding a credential, which it does for nothing today.

Decision: forge and tracker are separate, optional sections, following
`Runtime *RuntimeSection` in being absent for a project that has neither. The
tracker is configured per project and the forge per repository, inferred from the
remote's host where that host is recognisable and declared otherwise; a
self-hosted instance is not guessable, so Feat asks rather than guesses.

The tracker sits on the project because a task does, not because tickets do.
Evidence 2 is that tickets have no general home, and evidence 4 is that their
home is sometimes a repository Feat should never be told about; what makes the
project the right level is evidence 3, that the thing a ticket seeds is
project-level. Feat therefore does not model where tickets live at all. The
tracker section says how to obtain a list, and the scope of the source belongs to
whatever produces it — for the adapter below, to the command.

The tracker runs a configured command that prints JSON, in the class `review` and
`checks` already establish for user-supplied host commands. Feat publishes the
shape as `schema/feat-tickets.schema.json`, beside the project schema that
`internal/config/config.go:21` already describes the configuration file with, and
the user's command conforms to it. The inverse — the user supplying a schema for
Feat to interpret — is rejected: reading an arbitrary shape means a mapping
language in configuration, and a mapping language has no end. Making Feat's shape
the contract keeps the user's obligation to one sentence, and it is the same
document any other source of tickets would have to produce.

The section carries a kind, whose only value is that command. Nothing below
schedules a second one, so the field buys nothing today; it is kept because the
configuration file is a compatibility surface Phase 1 finalises, and a
discriminator added after that means either a breaking change or an inference
from which fields happen to be present. One field now is the cheaper option to
hold.

The shape is sized by what Feat acts on rather than by what trackers offer. A
reference, a title, a body, a URL, a state, and an optional source; not story
points, epics, sprints, or custom fields, which Feat would carry without doing
anything with them. Anything richer belongs in the brief, which is Markdown and
holds whatever the user wants. The source is optional because a project drawing
on one tracker has nothing to disambiguate; a command that merges two labels each
ticket with it, and it is what the `provider` on `ExternalTaskReference`
(`docs/03-domain-model.md`) records.

The command decides what the user's tickets are. Feat passes it no filter,
because a filter vocabulary would have to map onto every tracker's query
language, and iteration is exactly where that fails: GitLab has iterations and
milestones, GitHub has them as a Projects field behind a different API, Shortcut
has them natively. Defining that vocabulary now would settle a permanent shape
for a question the dogfood answers with a script. Placeholders can be added when
a picker needs to re-filter without re-running the command.

The output is validated against the schema by `feat doctor`, so a tracker that
emits the wrong shape is found when the user asks whether the project is
configured rather than when they are trying to start work. It is bounded in size
for the reason a control message is: it becomes a brief, and a brief is what the
agent is told to do.

There is no second adapter, and what would have been one is an example instead.
`gh issue list --repo acme/planning --json number,title,body,url,state` is
already a configured command printing JSON, so the only thing a GitHub tracker
written in Go would add is the mapping from that output to the shape published
here, because `gh` says `number` where the schema says `reference`. Everything
else such an adapter might offer belongs to the command in either case: `gh`
holds its own credential (evidence 5), no filter is passed, and a change is found
by re-running and comparing.

So Feat ships worked example commands — one per tracker worth making easy, each
validated against `schema/feat-tickets.schema.json` by the test suite, for the
reason `docs/examples/project.yaml` is validated against the configuration
schema: the file a new user copies cannot drift from what Feat accepts. That is
cheaper than an adapter, and it declines a liability an adapter takes on, which
is that another tool's JSON field names become Feat's compatibility problem the
day they change. An adapter in Go is not forbidden; it is not scheduled, and
whoever schedules one should say what it buys beyond a mapping.

These trackers publish MCP servers, and using one instead was considered. It does
not remove the mapping. MCP carries transport and discovery rather than a shared
ticket vocabulary, so each server names its own tools and shapes its own results,
and Feat would map per provider exactly as an adapter does — the cost is not
avoided, only moved behind a protocol. What the protocol adds is a server process
to supervise and session state to hold, inside the daemon that is the only
writer, against `exec.Command` and a JSON decode. A credential still has to live
somewhere, which is evidence 5. And a client for an external adapter protocol is
the question OQ-009 holds open, under an instruction not to define one
speculatively.

Where an MCP server would be right is as the implementation of an adapter
somebody schedules, because what it buys beyond a mapping is a *maintained* one:
the service's own team tracking their own API, rather than Feat carrying a client
for it. Until then a command that wants one can drive it without Feat knowing.
Handing the server to the agent instead is a different proposal and is refused by
ADR-070's inbound argument — a ticket the agent fetches never passes the
confirmation step, so text written by whoever filed it becomes the agent's
instructions unread, and Feat cannot record a ticket it never saw.

Consequence: `internal/config` gains a tracker section, carrying a kind and a
command, and a per-repository forge; `schema/` gains
`feat-tickets.schema.json`; `internal/domain` gains a provider-neutral ticket
reference on the task, which is what lets a merge request name the ticket it
closes and what a later phase would observe to offer cleanup. `docs/examples/`
gains a worked ticket command per tracker, validated by the test suite. Phase 6
of `docs/09-roadmap.md` is reordered to match Phase 3 — GitLab and the command
adapter before GitHub, because the machine Feat is dogfooded on needs them in
that order — and its native adapters become those examples. Shortcut's endpoints
for assignment and current iteration were not verified when this was recorded,
and are the script's concern rather than Feat's.
