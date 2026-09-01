# ADR-078 — The wizard stops asking about verification, and `checks:` stays exactly as it is

Status: accepted
Recorded: 2026-08-27, from the recorded history of 45 tasks across three projects

`feat project init` walked every user into configuring a gate, three questions
before the end, at the one moment they know least about the machine the gate will
run on. The measurements below are from `~/.local/share/feat/projects/*/tasks/*/`
on the author's machine: 45 tasks with recorded history, across `feat`,
`jobharbor`, and `jobharbor-dev`.

Evidence:

1. Re-running host-side what the agent already ran has never caught anything.
   `feat`'s unit suite as a gate: **0 failures in 18 runs**.
2. The gate that earned its place earned it for one reason. `feat`'s integration
   suite failed **3 of 18**: one genuine defect the agent structurally could not
   have found — the daemon passed `FEAT_DAEMON_SPAWNED` into the environment its
   own checks ran in, so a test that starts a daemon failed — and two that were
   the gate's own environment rather than the work, a `TempDir` cleanup denied on
   Feat's 0700 control directory and a fixture the run could not open. The only
   thing a host-side gate does that the agent cannot is run what the agent has no
   Docker for.
3. The failures that cost the most were not failures. `jobharbor`'s test command
   failed **4 out of 4**, at least twice because Feat's own gate could not find
   `pytest`: the virtual environment built, thirty packages installed, and the
   development dependency group never did. Each of those bounced a finished task
   back to an agent that had done nothing wrong.
4. The agent never overclaimed, so the gate was not catching a liar. Where its
   self-report said everything passed, it had also written "integration-docker-tmux
   skipped — no Docker in this sandbox". It reported what it could not run, and
   the gate then failed on exactly that.
5. The harm has a mechanism, and the mechanism is the prompt. `jobharbor`'s false
   failures came from a command configured in passing, mid-wizard, into an
   environment that could not run it. If the only way to get a gate is to open
   the file deliberately, the people who get gates are the people who want one.
6. The hand-written path is already complete, so removing the questions costs
   only the questions. `docs/examples/project.yaml` documents `checks:` with a
   worked example, `schema/feat-project.schema.json` gives editor completion, and
   `feat doctor` validates hand-written checks independently of the wizard.
7. The product without them already exists and works. Four of the six projects
   configured on that machine declare no checks, and their recorded lifecycles
   are `preparing | working | review_requested | archived` — never `verifying`,
   never `verification_failed`. With no checks the helper never waits, the system
   prompt never mentions a gate, and the daemon returns before the `verifying`
   transition.

Decisions:

- The three verification questions are removed, and with them the section they
  formed. `internal/wizard` no longer writes a `checks:` block, and the path an
  asker draws has four sections rather than five.
- Everything else about checks stays: the configuration model, the JSON schema,
  the worked example, `feat doctor`'s validation, the gate itself, and the
  workflow states it drives. This removes a prompt, not a feature.
- `feat project init` names the file and the block once, where the file has just
  been written. Opt-in is not the same as hidden, and the conversation is where a
  user would otherwise have learned the feature exists; a scrollback carries a
  sentence at no cost to anything else on it.
- The dashboard's dialog says nothing about it. Its last screen is a short fixed
  panel about what just happened, and a line there advertising a feature nobody
  asked about is the same prompt this removes, one size quieter. The file it
  wrote carries its own header naming `docs/examples/project.yaml` and the
  schema, so a user who opens the file to add a gate finds the way in where the
  gate goes.
- What this does not decide is whether checks are eventually deleted. It is
  deliberately left open, and it sets up the measurement that would settle it:
  checks were quasi-default, so "people configure them" said nothing. Once they
  are opt-in, the opt-in rate is evidence — if nobody hand-writes a block over
  the coming months, deleting the feature becomes cheap and properly backed, and
  if `feat`'s own integration suite keeps earning its place, that is evidence
  too. See OQ-013.
- A full removal, if it ever comes, has to settle one thing this does not:
  `ready_for_review` is reachable in practice only by passing a gate, so a
  project with no checks goes `review_requested` → approved or archived and
  never touches it. Deleting checks outright would have to collapse the two
  states or land review requests in `ready_for_review` directly.

Consequence: this amends ADR-062, which recorded verification as one of the six
things the wizard asks about; five remain, and the file it writes is still a file
the previous build would read. `internal/wizard` loses three stages and the
argument vector they accumulated, `internal/cli/init.go` gains the sentence
pointing at the file, and [02-user-workflows.md](02-user-workflows.md) records
that the wizard does not ask.
