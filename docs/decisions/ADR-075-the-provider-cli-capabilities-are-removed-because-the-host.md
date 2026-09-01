# ADR-075 — The provider-CLI capabilities are removed, because the host holds the credential

Status: accepted
Recorded: 2026-08-25, from reviewing what `agent.capabilities` still does

ADR-070 moved every credentialed provider call to the trusted host, and ADR-071
did the same for ticket ingestion. `agent.capabilities.github_cli` and
`gitlab_cli` were written before either, when container-side native CLI access
was the first-class path, and they were left in place afterwards as "a path a
project may configure". Reviewing them against what the binary now does found
them to be a declaration Feat can no longer act on.

Evidence:

1. Nothing in Feat's own loop reads either field to decide anything. The two
   consumers were a launch probe in the Claude adapter and two `feat doctor`
   findings. Both only re-checked a setup the project made for itself: Feat never
   installs `gh` or `glab` in the agent environment and never supplies it a
   credential, so the fields declared someone else's arrangement.
2. `required` is a gate on a state the security model says must not exist. The
   agent environment is to hold no provider token
   ([05-security-model.md](05-security-model.md)), so `gh auth status` succeeding
   inside the container is the condition the recommended configuration is
   arranged to prevent. A correctly configured project that set `required` would
   fail every launch, and one that passed would be one with a durable token
   reachable by any prompt injection.
3. `feat doctor` already asks the question that matters, in the place that
   matters. The publication check probes `gh`/`glab` on the host per forge the
   repositories declare, in both execution modes, because that is where the push
   and the merge request happen (ADR-074). The capability findings asked the same
   tools about a different machine, and two findings naming the same executable
   with opposite verdicts is a diagnostic that has to be explained rather than
   read.
4. Removing them is a clean break rather than a migration. `internal/config`
   decodes with `yaml.Strict()`, so a file that still names either key fails to
   load naming the key and the line it is on. Every configuration that has one is
   on the author's machine, and the author is the only user (ADR-065).

Decisions:

- `agent.capabilities.github_cli` and `gitlab_cli` are removed from the
  configuration, the JSON schema, the generated file `feat project init` writes,
  and the wizard, which loses its provider-CLI question. `agent.capabilities`
  keeps `docker`, `network`, and `git`.
- No entry is added to `replaced` for either key. The mechanism exists so a break
  is not diagnosed as a typo, and it earns that where a field was replaced and
  the message can name the successor. Here there is none to name: the remedy is
  to delete the line, which the strict decoder's own error already points at. The
  configurations that carry the key are the author's six, edited once.
- The Claude adapter's `Validate` asks only whether the agent executable runs. A
  provider CLI in the agent environment stops no launch.
- `feat doctor` reports nothing about `gh` or `glab` in the agent environment.
  The host publication check is the only provider-CLI finding, which is what
  makes it answerable.
- What the fields permitted is not withdrawn, only undeclared. A project may
  install `gh` or `glab` in its own image and mount its own credential;
  [05-security-model.md](05-security-model.md) states the exposure that follows
  and Feat neither prevents, checks, nor reports it.

Consequence: `internal/agent` loses `CapabilityLevel` and the two `Environment`
fields; `internal/agent/claude` loses `probeCLI`; `internal/project` loses
`checkProviderCLI` and `providerCLIs`; `internal/config` loses
`DraftCapabilities`, three constants, and the level validation.
[02-user-workflows.md](02-user-workflows.md),
[04-functional-specification.md](04-functional-specification.md) FR-PROJ-004,
[05-security-model.md](05-security-model.md),
[06-technical-architecture.md](06-technical-architecture.md),
[07-configuration-model.md](07-configuration-model.md),
[09-roadmap.md](09-roadmap.md), [README.md](README.md), and CLAUDE.md's product
contract are updated in the same change.

The two daemon and adapter tests that used a required provider CLI as their
trigger keep their assertions and change what does the refusing: what they prove
is that a task whose agent could never start creates no terminal and no session,
which is FR-PROJ-004's rule and outlives the trigger.

This does not touch `docker`, `network`, or `git`. That those three accept one
value each, and that only `docker` is backed by a probe, is a separate question
and is left open here rather than answered in passing.
