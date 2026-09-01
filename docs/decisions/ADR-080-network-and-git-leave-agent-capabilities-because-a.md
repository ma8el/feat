# ADR-080 — `network` and `git` leave `agent.capabilities`, because a capability is a claim Feat checks

Status: accepted  
Recorded: 2026-08-28, from reviewing what is left of `agent.capabilities` after ADR-075

ADR-028 created the section and recorded, in the same breath, that three of its
fields could only ever hold one value: Feat has no mechanism that grants an agent
Docker, restricts its network, or limits its Git access. It kept all three anyway,
on the reasoning that "the declaration is still worth making, because slice 8
checks the running container against it". ADR-075 removed `github_cli` and
`gitlab_cli` and deliberately left these three alone, saying that whether a field
with one legal value earns its place "is a separate question and is left open
here rather than answered in passing". This answers half of it.

The clause ADR-028 wrote is true of `docker` and was never true of the other two.

Evidence:

1. **Neither field gates anything.** Nothing in `internal/execution`,
   `internal/daemon`, or the agent adapters consults either. The readers were
   `internal/config` — parse, default, validate, describe, draft, schema — and
   one pair of `feat doctor` findings in `internal/project`, which printed the
   configured value back with a sentence attached. That pair is the sharper
   version of the problem rather than a counterexample: a passing check that
   asked the machine nothing reads as a third thing the diagnostic verified,
   next to two it did. F6-08 removed exactly that shape from the Docker
   capability, which used to be reported as `ok` without the container being
   asked.
2. **Neither field can vary.** Validation rejected anything but `unrestricted`
   and `full`. A field with one legal value is not configuration; it is a
   sentence, and this one was a sentence about a restriction Feat does not
   implement.
3. **The sentence is already written where it holds.** ADR-028's own reasoning
   for keeping them — the value is disclosure — is satisfied by
   [05-security-model.md](05-security-model.md), which states that full Git in a
   native worktree exposes shared repository metadata the agent may inspect and
   mutate, and that the devcontainer provides no network data-loss prevention.
   Those hold whatever a project writes in its YAML, which is precisely why they
   are not configuration. Saying it twice made one copy authoritative and the
   other decorative, and the decorative copy was the one with a syntax.
4. **`docker` is a different kind of thing, and the difference is checkable.**
   Its declaration is the one Feat stands behind: a launch refuses a container
   that carries a client speaking a container runtime's API, points one at a
   daemon through the environment, mounts a daemon socket, runs with host
   networking, or is privileged; and `feat doctor` asks a running container the
   half of that it can answer without a task's specification. Feat is standing
   somewhere the user cannot — between a container definition and a host daemon.
   Nothing stands behind `network` or `git`.
5. **Removal is a break rather than a migration, and the break is the good
   kind.** `internal/config` decodes with `yaml.Strict()`, so a file still
   naming either key fails to load, naming the key and the line it is on. Every
   configuration carrying one is on the author's machines, and the author is the
   only user (ADR-065).

Decisions:

- `agent.capabilities.network` and `agent.capabilities.git` are removed from the
  configuration types, the defaults, the validation, `feat project show`, the
  file `feat project init` writes, the JSON schema, and
  `docs/examples/project.yaml`. `CapabilityUnrestricted` and `CapabilityFull` go
  with them.
- `feat doctor` loses the check that reported them. The Docker capability keeps
  both of its findings, which are the ones that ask something: what `denied`
  means for an agent that runs as the user, and what a running container turns
  out to hold.
- No entry is added to `replaced` for either key, on ADR-075's reasoning: that
  mechanism earns its place where a field has a successor to name, and there is
  none here — the remedy is to delete the line, which the strict decoder's error
  already points at. A test asserts that the error names the removed key, so the
  thing standing in for a migration is held to working.
- **`agent.capabilities.docker` is unchanged, and stays in project
  configuration.** Its value, its validation, its wording, and the unconditional
  launch refusal in `internal/execution/compose/probe.go` are all left exactly as
  they are. This ADR removes two fields; it does not regrade the third.
- **The section does not move to the settings file.** ADR-079 moved `review`,
  `notifications`, and `resources` there because they are identical across
  projects — facts about the user rather than about a project.
  `agent.capabilities` is identical across projects for the opposite reason: it
  cannot vary. Same symptom, opposite cause. If `docker` is ever regraded it
  varies per project, because a devcontainer that legitimately needs Docker for
  its own workflow is a fact about that project's container and not about the
  machine; and if it is never regraded, a constant is deleted rather than
  relocated. A machine-level ceiling with per-project values beneath it is
  coherent and is also the precedence model ADR-079 rejected, so introducing it
  here would put override semantics under the most security-sensitive setting in
  the configuration. If that model is ever wanted it is a deliberate decision
  about precedence generally, not one arrived at through this section.
- The Docker regrade — whether a level should exist that permits a sibling
  daemon the user provisioned while still refusing the host's own socket — is
  not decided here and is not open by omission either. It is a security-boundary
  change: telling `tcp://dind:2375` from `unix:///var/run/docker.sock` means
  reading a value `probe.go` refuses to read on principle, and
  [05-security-model.md](05-security-model.md) names Docker-over-TCP credentials
  as something the agent must not receive. It needs its own ADR, with the
  host-daemon versus sibling-daemon line drawn explicitly against CLAUDE.md's
  standing rule. `docker: denied` is left in place as the field that decision
  would act on.

Consequence: ADR-028's capability clause is amended — of the three fields it
described, one survives on the ground it gave, and the other two are removed for
failing it. `internal/config` loses two struct fields, two constants, two
defaults, two validation branches, two described rows, and two drafted lines;
`internal/project` loses `checkCapabilities`. No behaviour changes: nothing read
either field to decide anything, which is the finding.

Every project configuration that names either key is hand-edited once — three
files across the author's two machines, including the reference project's — and
until that is done those projects fail to load with an unknown-field error naming
the line. [07-configuration-model.md](07-configuration-model.md) and
`schema/feat-project.schema.json` are updated in the same change, as is
[04-functional-specification.md](04-functional-specification.md) FR-GIT-006,
which required full Git access "when configured" and named a field there is no
longer a configuration for. [05-security-model.md](05-security-model.md) is not
updated, because what it says about Git metadata and network data-loss prevention
was never a restatement of these fields — the fields were a restatement of it.
That is the whole argument, so the disclosure it carries is now load-bearing
rather than duplicated, which FR-GIT-006 says out loud.

Amended immediately after, from sweeping the rest of the documentation for the
same claim. Removing a field is not finished when the field is gone: eight
sentences across six documents still described these capabilities as things a
project configures, and both this ADR and ADR-075 had left them. `docs/01`
listed network and provider-CLI access among the capabilities that do not imply
one another; `docs/03` invariant 12 said provider capability "may be enabled
inside the agent environment", which ADR-070 had already reversed; `docs/08`
said Feat validates the configured provider-CLI capability; `docs/09` called
full Git and provider CLI "capabilities a project grants deliberately";
`docs/README.md` called provider capabilities explicit project configuration;
and CLAUDE.md said full Git and provider CLI access "are allowed only when
configured", contradicting its own product contract four lines above it. And
`docs/07` listed "enabled capabilities" among the contents of a task's launch
snapshot, which records none and needs none: a snapshot freezes what could change
under a running task, and this cannot.

The specification files are corrected to say what Feat does: these are exposures
it neither varies nor checks, and a capability is something Feat can withhold.
CLAUDE.md's two lines are deleted rather than rewritten, which is the better
answer for that file: it holds rules to work by, and a rule about a permission
that cannot vary is not one. The Docker boundary it existed to protect is stated
on its own three lines above, where it never depended on the comparison. The
lesson is worth the paragraph — a decision that deletes a configuration surface
has to grep for the surface, not only for its readers.
