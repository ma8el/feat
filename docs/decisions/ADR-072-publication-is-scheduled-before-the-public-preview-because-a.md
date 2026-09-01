# ADR-072 — Publication is scheduled before the public preview, because a dogfood task cannot finish without it

Status: accepted
Recorded: 2026-08-24, from the ordering question the dogfood machine raised

ADR-070 and ADR-071 describe Phase 3 and Phase 6 work, and the declared next
milestone is Phase 1, the public preview. The machine Feat is now dogfooded on
needs the later phases first, so the order this roadmap records and the order the
work is wanted in came apart. The phases are ordered by product value and
dependency rather than by date, which makes this a question about dependency
rather than about impatience.

Evidence:

1. Feat has no push path at all. No production code runs `git push`;
   `internal/git` only observes whether a branch has a remote-tracking ref, for
   the unpushed count a cleanup plan carries
   (`internal/git/cleanup.go:263`). v0.1 excluded a Feat-owned push/PR/MR
   workflow deliberately and left publication to the agent running `glab` in its
   own environment.
2. That one remaining route is closed on the dogfood machine. The agent receives
   no provider credential there, and Feat propagates none: there is no handling
   of SSH agent forwarding, a credential helper, a proxy, or a certificate
   authority anywhere in `internal/`. `agent.capabilities.gitlab_cli` therefore
   has nothing to authenticate with, whatever it is set to.
3. The credential is already on the host, and Feat already uses it. `git fetch`
   and `git worktree add` run there (ADR-050), which is how a task's worktrees
   get their content in the first place. Host-side publication reuses
   authentication that is demonstrably working on that machine rather than
   needing a second one to be arranged.
4. Phase 1 is partly downstream of the dogfood by its own text. The
   clean-installation-to-first-task documentation is to be written against what
   the dogfood runs turned out to need, the wizard's second pass against what
   users hit, and OQ-013 and OQ-014 both name dogfood as the evidence that
   settles them. A dogfood that stops before publication produces less of that
   evidence, and produces none at all about the end of a task.
5. Phase 1's largest single item is the one the dogfood cannot exercise.
   `internal/execution/host` is a package comment and nothing else;
   `FEAT_HOST_AGENT` (ADR-032) launches Claude natively but is an opt-in in the
   daemon's environment rather than the second implementation behind the
   execution interface. The dogfood machine mandates devcontainers, so nothing
   there runs host-native execution whatever the order.
6. ADR-070 and ADR-071 were recorded together and are not equally urgent.
   Publication is at the end of a task and needs a credential the agent
   environment does not have. Ticket ingestion is at the start, and what it
   replaces is pasting text into a brief that is Markdown the user composes
   anyway. One removes a wall; the other removes friction.

Decision: the whole dogfood feature set is built before the public-preview
milestone — publication (ADR-070) first, because evidence 6 makes it the wall
rather than the friction, and the tracker (ADR-071) second. Phase 1 follows both,
with its first-task documentation and its second pass over the wizard last within
it, because those two wait on dogfood runs rather than on dogfood features.

Interleaving Phase 1's evidence-independent items between the two was considered
and rejected. What recommends them — that packaging, Linux notifications, and
machine-readable output are what make Feat installable by somebody other than its
author — is an argument about an audience that is not there yet, which is what
evidence 5 observes of host-native execution and evidence 4 of the documentation.
Building the installable half first would serve nobody while the dogfood still
could not finish a task, and would split two features that one machine needs for
one reason.

The phases keep their numbers. ADR-070 and ADR-071 each name the phase they
belong to, so renumbering would rewrite decisions that are already accepted, and
it would misrepresent what the numbers are — an ordering by value and dependency,
not a schedule. A capability wanted earlier than its phase is scheduled where it
is wanted and says so in both places, which is what Phase 1, Phase 3, and Phase 6
now do.

What this does not decide is whether host-native execution earns its place in the
public preview. Evidence 5 is an argument about exercise rather than about worth:
the user it serves is somebody with no devcontainer, which is a real audience
that the dogfood happens not to contain. It stays in Phase 1 unchanged, and a
decision to drop it would need evidence about that audience rather than about
this machine.

Consequence: Phase 1 of `docs/09-roadmap.md` gains the note that publication and
the tracker both precede it and says which of its own items come last, and
Phase 6 gains where the tracker falls in the same order. Phase 3 gains the ordering note and
has its capability list rewritten against ADR-070, which reverses two things it
promised: `gh`/`glab` validated inside the agent environment as the way the loop
closes, and a closing note making container-side native CLI access first-class
with host-side execution a possible second mode. A third goes away rather than
being dropped — discovering one MR/PR per changed repository existed because the
agent published and Feat had to find the result, and a host that opens the
request records it instead.
